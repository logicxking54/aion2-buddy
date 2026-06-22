//go:build windows

// Package arduino drives the Aion 2 Buddy keyboard sketch over a serial (COM)
// port: open the port, send key tokens (one per line, 115200 baud), close.
// Native Windows COM via x/sys/windows — no external dependency.
package arduino

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Known Arduino-family USB vendor IDs (Leonardo and common 32u4 clones).
var arduinoVIDs = []string{"VID_2341", "VID_2A03", "VID_1B4F", "VID_239A", "VID_2886"}

// livePorts returns the set of currently-present COM ports (upper-cased),
// from SERIALCOMM. Used to filter out stale registry entries.
func livePorts() map[string]bool {
	m := map[string]bool{}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.READ)
	if err != nil {
		return m
	}
	defer k.Close()
	names, err := k.ReadValueNames(0)
	if err != nil {
		return m
	}
	for _, n := range names {
		if v, _, err := k.GetStringValue(n); err == nil && v != "" {
			m[strings.ToUpper(v)] = true
		}
	}
	return m
}

// DetectPort returns the currently-present Arduino COM port, or "".
func DetectPort() string {
	p, _ := Detect()
	return p
}

// Detect scans the USB device tree for an Arduino and returns its currently
// present COM port (or "") plus human-readable diagnostic lines describing what
// it saw (for the app log).
func Detect() (string, []string) {
	var info []string

	live := livePorts()
	liveList := make([]string, 0, len(live))
	for p := range live {
		liveList = append(liveList, p)
	}
	sort.Strings(liveList)
	if len(liveList) == 0 {
		info = append(info, "Live COM ports: (none)")
	} else {
		info = append(info, "Live COM ports: "+strings.Join(liveList, ", "))
	}

	root, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\USB`, registry.READ)
	if err != nil {
		info = append(info, "Cannot read USB device tree")
		return "", info
	}
	defer root.Close()

	devs, _ := root.ReadSubKeyNames(-1)
	chosen := ""
	for _, dev := range devs {
		up := strings.ToUpper(dev)
		isArduino := false
		for _, vid := range arduinoVIDs {
			if strings.Contains(up, vid) {
				isArduino = true
				break
			}
		}
		if !isArduino {
			continue
		}
		devKey, err := registry.OpenKey(root, dev, registry.READ)
		if err != nil {
			continue
		}
		insts, _ := devKey.ReadSubKeyNames(-1)
		for _, inst := range insts {
			instKey, err := registry.OpenKey(devKey, inst, registry.READ)
			if err != nil {
				continue
			}
			friendly, _, _ := instKey.GetStringValue("FriendlyName")
			port := ""
			if params, perr := registry.OpenKey(instKey, `Device Parameters`, registry.READ); perr == nil {
				port, _, _ = params.GetStringValue("PortName")
				params.Close()
			}
			instKey.Close()
			if port == "" {
				continue
			}
			label := dev
			if friendly != "" {
				label = friendly
			}
			state := "stale"
			if live[strings.ToUpper(port)] {
				state = "present"
			}
			info = append(info, "Found "+label+" -> "+port+" ("+state+")")
			if state == "present" && chosen == "" {
				chosen = port
			}
		}
		devKey.Close()
	}

	if chosen != "" {
		info = append(info, "Selected: "+chosen)
	} else {
		info = append(info, "No connected Arduino found")
	}
	return chosen, info
}

// DCB (device control block) — only the fields we set matter; the rest are zero.
type dcb struct {
	DCBlength  uint32
	BaudRate   uint32
	flags      uint32 // packed bitfields (fBinary, fDtrControl, fRtsControl, …)
	wReserved  uint16
	XonLim     uint16
	XoffLim    uint16
	ByteSize   byte
	Parity     byte
	StopBits   byte
	XonChar    byte
	XoffChar   byte
	ErrorChar  byte
	EofChar    byte
	EvtChar    byte
	wReserved1 uint16
}

type commTimeouts struct {
	ReadIntervalTimeout         uint32
	ReadTotalTimeoutMultiplier  uint32
	ReadTotalTimeoutConstant    uint32
	WriteTotalTimeoutMultiplier uint32
	WriteTotalTimeoutConstant   uint32
}

var (
	kernel32            = windows.NewLazyDLL("kernel32.dll")
	procSetCommState    = kernel32.NewProc("SetCommState")
	procSetCommTimeouts = kernel32.NewProc("SetCommTimeouts")
)

// Board is a connection to the Arduino. Safe for concurrent use.
type Board struct {
	mu     sync.Mutex
	handle windows.Handle
	port   string
	open   bool
}

func New() *Board {
	return &Board{handle: windows.InvalidHandle}
}

// Open connects to the given COM port (e.g. "COM3") at the given baud (115200).
func (b *Board) Open(port string, baud int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked()

	name, err := windows.UTF16PtrFromString(`\\.\` + port)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, nil, windows.OPEN_EXISTING, 0, 0,
	)
	if err != nil {
		return fmt.Errorf("open %s: %w", port, err)
	}

	var d dcb
	d.DCBlength = uint32(unsafe.Sizeof(d))
	d.BaudRate = uint32(baud)
	d.flags = 0x1011 // fBinary | fDtrControl=ENABLE | fRtsControl=ENABLE
	d.ByteSize = 8
	d.Parity = 0   // NOPARITY
	d.StopBits = 0 // ONESTOPBIT
	if r, _, e := procSetCommState.Call(uintptr(h), uintptr(unsafe.Pointer(&d))); r == 0 {
		windows.CloseHandle(h)
		return fmt.Errorf("SetCommState %s: %w", port, e)
	}

	to := commTimeouts{ReadIntervalTimeout: 0xFFFFFFFF, WriteTotalTimeoutConstant: 200}
	procSetCommTimeouts.Call(uintptr(h), uintptr(unsafe.Pointer(&to)))

	b.handle = h
	b.port = port
	b.open = true
	return nil
}

func (b *Board) closeLocked() {
	if b.open {
		windows.CloseHandle(b.handle)
		b.handle = windows.InvalidHandle
		b.open = false
	}
}

// Close disconnects the board.
func (b *Board) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked()
}

func (b *Board) IsOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open
}

func (b *Board) Port() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.port
}

// SendKey writes one key token (newline-terminated) to the board.
func (b *Board) SendKey(token string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open {
		return fmt.Errorf("board not connected")
	}
	data := []byte(token + "\n")
	var written uint32
	if err := windows.WriteFile(b.handle, data, &written, nil); err != nil {
		// A write error usually means the board was unplugged — drop the handle.
		b.closeLocked()
		return err
	}
	return nil
}
