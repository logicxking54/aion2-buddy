//go:build windows

package oversize

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.zx2c4.com/wintun"

	"golang.org/x/sys/windows"
)

// wintun.dll is shipped with the app and extracted at runtime so the
// golang.zx2c4.com/wintun bindings can load it.
//
//go:embed embed/wintun.dll
var wintunDLLBytes []byte

var wintunOnce sync.Once
var wintunErr error

// ensureWintun extracts the embedded wintun.dll to a temp dir and pre-loads it
// into the process by full path. The golang.zx2c4.com/wintun bindings load the
// DLL with LoadLibraryEx(LOAD_LIBRARY_SEARCH_APPLICATION_DIR|SYSTEM32), which
// does NOT search PATH or SetDllDirectory — but once a module named "wintun.dll"
// is already loaded, that LoadLibraryEx call returns the existing handle by base
// name, so pre-loading it here is what makes the bindings resolve. Safe to call
// repeatedly.
func ensureWintun() error {
	wintunOnce.Do(func() {
		dir := filepath.Join(os.TempDir(), "aion2-buddy-wintun")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			wintunErr = err
			return
		}
		dst := filepath.Join(dir, "wintun.dll")
		if info, err := os.Stat(dst); err != nil || info.Size() != int64(len(wintunDLLBytes)) {
			if err := os.WriteFile(dst, wintunDLLBytes, 0o644); err != nil {
				wintunErr = err
				return
			}
		}
		// Pre-load by absolute path so the bindings' later by-name load resolves.
		if _, err := windows.LoadLibrary(dst); err != nil {
			wintunErr = fmt.Errorf("load wintun.dll from %s: %w", dst, err)
			return
		}
	})
	return wintunErr
}

// tunDevice wraps a WinTUN adapter + session and exposes packet read/write.
type tunDevice struct {
	name    string
	adapter *wintun.Adapter
	session wintun.Session
	ifIndex uint32
	writeMu sync.Mutex // serializes AllocateSendPacket/SendPacket on the ring
}

const tunRingCapacity = 0x400000 // 4 MiB ring (matches the reference)

// createTUN creates (or reopens) the named WinTUN adapter and starts a session.
func createTUN(name string) (*tunDevice, error) {
	if err := ensureWintun(); err != nil {
		return nil, fmt.Errorf("wintun.dll: %w", err)
	}
	// Reopen+close any stale adapter of the same name from a prior crash.
	if old, err := wintun.OpenAdapter(name); err == nil {
		old.Close()
	}
	adapter, err := wintun.CreateAdapter(name, "Wintun", nil)
	if err != nil {
		return nil, fmt.Errorf("create adapter (run as Administrator?): %w", err)
	}
	session, err := adapter.StartSession(tunRingCapacity)
	if err != nil {
		adapter.Close()
		return nil, fmt.Errorf("start session: %w", err)
	}
	return &tunDevice{name: name, adapter: adapter, session: session}, nil
}

// readPacket returns the next IP packet from the adapter (a fresh copy). It
// blocks on the read-wait event when the ring is empty. Returns an error when
// the session has ended.
func (d *tunDevice) readPacket() ([]byte, error) {
	for {
		packet, err := d.session.ReceivePacket()
		if err == nil {
			out := append([]byte(nil), packet...)
			d.session.ReleaseReceivePacket(packet)
			return out, nil
		}
		switch err {
		case windows.ERROR_NO_MORE_ITEMS:
			windows.WaitForSingleObject(d.session.ReadWaitEvent(), windows.INFINITE)
			continue
		default:
			return nil, err
		}
	}
}

// writePacket injects an IP packet toward the OS via the adapter.
func (d *tunDevice) writePacket(b []byte) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	pkt, err := d.session.AllocateSendPacket(len(b))
	if err != nil {
		return err
	}
	copy(pkt, b)
	d.session.SendPacket(pkt)
	return nil
}

func (d *tunDevice) close() {
	d.session.End()
	d.adapter.Close()
}

// configure sets the adapter's IPv4 address/mask and resolves its interface
// index (needed for `route ... IF <idx>`).
func (d *tunDevice) configure(ip, mask string) error {
	if out, err := runHidden2("netsh", "interface", "ip", "set", "address",
		"name="+d.name, "source=static", "addr="+ip, "mask="+mask); err != nil {
		return fmt.Errorf("netsh set address: %v: %s", err, out)
	}
	idx, err := adapterIndex(d.name)
	if err != nil {
		return err
	}
	d.ifIndex = idx
	return nil
}

// adapterIndex parses `netsh interface ipv4 show interfaces` for the adapter's
// interface index (mirrors the reference's get_adapter_index).
func adapterIndex(name string) (uint32, error) {
	out, err := runHidden2("netsh", "interface", "ipv4", "show", "interfaces")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, name) {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				if idx, err := strconv.Atoi(fields[0]); err == nil {
					return uint32(idx), nil
				}
			}
		}
	}
	return 0, fmt.Errorf("interface index for %q not found", name)
}

// runHidden2 runs a console command without a window and returns combined stdout.
func runHidden2(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
	return string(out), err
}
