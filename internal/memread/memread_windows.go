//go:build windows

// Package memread is a strictly read-only memory reader for the Aion 2 client.
//
// It NEVER writes to or injects into the game process. It opens the process
// with only PROCESS_VM_READ | PROCESS_QUERY_INFORMATION and uses
// ReadProcessMemory to follow Cheat-Engine-style pointer paths. This is the
// same access profile as a passive DPS/cooldown reader (e.g. dmg-aion2): no
// memory writes, no thread creation, no input injection — which keeps the
// behavioural footprint as small as a read-only tool can be.
//
// The module is deliberately isolated from the packet-capture path (see the
// oversize package for the same isolation rationale) so that enabling memory
// reading is an explicit, opt-in code path.
package memread

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	processVMRead           = 0x0010
	processQueryInformation = 0x0400
)

// defaultModule is the Aion 2 main module. Override per resolve if needed.
const defaultModule = "Aion2.exe"

// Reader holds an open, read-only handle to the game process plus a cache of
// module base addresses. It is safe for concurrent use.
type Reader struct {
	mu      sync.Mutex
	pid     uint32
	handle  windows.Handle
	modBase map[string]uintptr // lowercased module name -> base address
	scan    *scanState         // current value-scan candidate set (see scan_windows.go)
	targets []string           // candidate process names (lowercased), matched in order
}

// defaultTargets are the process names tried when none is configured. The real
// client name can differ between regions/builds — use SetProcessName + the
// ListProcesses diagnostic to point at the right one.
var defaultTargets = []string{"aion2.exe", "aion2"}

// NewReader returns an unopened reader. Call Open (or any Read*, which opens
// lazily) before use.
func NewReader() *Reader {
	return &Reader{modBase: map[string]uintptr{}, targets: append([]string(nil), defaultTargets...)}
}

// SetProcessName overrides the target process name (e.g. once the diagnostic
// reveals the real client exe). Empty restores the defaults. Forces a
// re-attach on the next read.
func (r *Reader) SetProcessName(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		r.targets = append([]string(nil), defaultTargets...)
	} else {
		r.targets = []string{name}
	}
	r.closeLocked() // current handle (if any) may point at the wrong process
}

// ProcessName returns the active target name(s), comma-joined.
func (r *Reader) ProcessName() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.targets, ", ")
}

// Attached reports whether a process handle is currently open.
func (r *Reader) Attached() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handle != 0
}

// PID returns the attached process id, or 0.
func (r *Reader) PID() uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pid
}

// Close releases the process handle and clears the module cache.
func (r *Reader) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeLocked()
}

func (r *Reader) closeLocked() {
	if r.handle != 0 {
		windows.CloseHandle(r.handle)
		r.handle = 0
	}
	r.pid = 0
	r.modBase = map[string]uintptr{}
	r.scan = nil // candidate addresses are invalid once we re-attach
}

// open ensures a valid handle to the live Aion 2 process. If the cached pid no
// longer matches a running Aion2.exe (e.g. the client was restarted) it
// re-attaches, which invalidates cached module bases.
func (r *Reader) open() error {
	pid, err := r.findGamePID()
	if err != nil {
		return err
	}
	if r.handle != 0 && r.pid == pid {
		return nil
	}
	// Process changed or never opened — (re)attach.
	r.closeLocked()
	h, err := windows.OpenProcess(processVMRead|processQueryInformation, false, pid)
	if err != nil {
		return fmt.Errorf("OpenProcess(pid=%d): %w (run as Administrator?)", pid, err)
	}
	r.handle = h
	r.pid = pid
	return nil
}

// Status describes whether the game is running and whether we hold a read-only
// handle, with a human-readable reason when we don't. Unlike Attached(), this
// actively tries to attach so the UI's indicator reflects reality.
type Status struct {
	Running  bool   `json:"running"`
	Attached bool   `json:"attached"`
	PID      uint32 `json:"pid"`
	Error    string `json:"error,omitempty"`
}

// Status tries to find and open the game, returning a diagnosable result.
func (r *Reader) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	pid, perr := r.findGamePID()
	if perr != nil {
		return Status{Error: perr.Error()}
	}
	if err := r.open(); err != nil {
		// Process exists but we couldn't open it — almost always admin rights.
		return Status{Running: true, PID: pid, Error: err.Error()}
	}
	return Status{Running: true, Attached: true, PID: r.pid}
}

// ProcInfo is one running process, for the "what is the game really called?"
// diagnostic when Aion2.exe isn't found.
type ProcInfo struct {
	Name string `json:"name"`
	PID  uint32 `json:"pid"`
}

// ListProcesses returns running processes whose name contains substr
// (case-insensitive). substr "" returns everything.
func ListProcesses(substr string) ([]ProcInfo, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return nil, err
	}
	want := strings.ToLower(substr)
	var out []ProcInfo
	for {
		name := windows.UTF16ToString(e.ExeFile[:])
		if want == "" || strings.Contains(strings.ToLower(name), want) {
			out = append(out, ProcInfo{Name: name, PID: e.ProcessID})
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			break
		}
	}
	return out, nil
}

// ModuleBase returns the load base address of the named module in the game
// process (cached). name is matched case-insensitively, e.g. "Aion2.exe".
func (r *Reader) ModuleBase(name string) (uintptr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.open(); err != nil {
		return 0, err
	}
	return r.moduleBaseLocked(name)
}

func (r *Reader) moduleBaseLocked(name string) (uintptr, error) {
	key := strings.ToLower(name)
	if b, ok := r.modBase[key]; ok {
		return b, nil
	}
	base, err := moduleBaseForPID(r.pid, name)
	if err != nil {
		return 0, err
	}
	r.modBase[key] = base
	return base, nil
}

// readInto reads len(buf) bytes from addr in the target process into buf.
func (r *Reader) readInto(addr uintptr, buf []byte) error {
	var n uintptr
	err := windows.ReadProcessMemory(r.handle, addr, &buf[0], uintptr(len(buf)), &n)
	if err != nil {
		return fmt.Errorf("ReadProcessMemory(0x%X, %d): %w", addr, len(buf), err)
	}
	if n != uintptr(len(buf)) {
		return fmt.Errorf("short read at 0x%X: got %d of %d bytes", addr, n, len(buf))
	}
	return nil
}

// readPtr reads a pointer-sized (8 byte, x64) value at addr.
func (r *Reader) readPtr(addr uintptr) (uintptr, error) {
	var buf [8]byte
	if err := r.readInto(addr, buf[:]); err != nil {
		return 0, err
	}
	return uintptr(*(*uint64)(unsafe.Pointer(&buf[0]))), nil
}

// Resolve walks a pointer path and returns the final value address, without
// reading the value itself. This is the heart of Cheat-Engine-style pointers.
//
// Semantics (matches Cheat Engine's multi-level pointers):
//   - addr = moduleBase(module) + rootOffset
//   - for each offset o in offsets (in the order applied):
//   - addr = deref(addr) + o
//
// So an empty offsets slice yields a STATIC address (moduleBase+rootOffset),
// and each offset performs one dereference then adds. In Cheat Engine's
// pointer view the offsets are listed bottom-up; enter them here in the order
// they are APPLIED, i.e. the reverse of CE's top-to-bottom display. The
// resolver explains exactly how to translate a CE entry in the UI.
//
// If module is "", defaultModule (Aion2.exe) is used.
func (r *Reader) Resolve(module string, rootOffset uintptr, offsets []uintptr) (uintptr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.open(); err != nil {
		return 0, err
	}
	if module == "" {
		module = defaultModule
	}
	base, err := r.moduleBaseLocked(module)
	if err != nil {
		return 0, err
	}
	addr := base + rootOffset
	for i, off := range offsets {
		p, err := r.readPtr(addr)
		if err != nil {
			return 0, fmt.Errorf("deref level %d at 0x%X: %w", i, addr, err)
		}
		if p == 0 {
			return 0, fmt.Errorf("null pointer at deref level %d (0x%X)", i, addr)
		}
		addr = p + off
	}
	return addr, nil
}

// Value is the multi-typed result of reading one resolved address, so the UI
// can show whichever interpretation makes sense for the hunted field.
type Value struct {
	Address uint64  `json:"address"` // resolved final address
	Int32   int32   `json:"int32"`
	UInt32  uint32  `json:"uint32"`
	Int64   int64   `json:"int64"`
	Float32 float32 `json:"float32"`
	Float64 float64 `json:"float64"`
	HexLE   string  `json:"hexLE"` // first 8 bytes, little-endian display
}

// ReadValue resolves the pointer path and reads 8 bytes at the final address,
// returning every common interpretation. Read-only.
func (r *Reader) ReadValue(module string, rootOffset uintptr, offsets []uintptr) (Value, error) {
	addr, err := r.Resolve(module, rootOffset, offsets)
	if err != nil {
		return Value{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var buf [8]byte
	if err := r.readInto(addr, buf[:]); err != nil {
		return Value{}, err
	}
	p := unsafe.Pointer(&buf[0])
	return Value{
		Address: uint64(addr),
		Int32:   *(*int32)(p),
		UInt32:  *(*uint32)(p),
		Int64:   *(*int64)(p),
		Float32: safeF32(*(*float32)(p)),
		Float64: safeF64(*(*float64)(p)),
		HexLE: fmt.Sprintf("%02X %02X %02X %02X %02X %02X %02X %02X",
			buf[0], buf[1], buf[2], buf[3], buf[4], buf[5], buf[6], buf[7]),
	}, nil
}

// --- process / module enumeration (Toolhelp, no PowerShell) ---

func (r *Reader) findGamePID() (uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, fmt.Errorf("snapshot processes: %w", err)
	}
	defer windows.CloseHandle(snap)

	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return 0, err
	}
	for {
		name := strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))
		for _, t := range r.targets {
			if name == t {
				return e.ProcessID, nil
			}
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			return 0, fmt.Errorf("process %q not found (is the game running?)", strings.Join(r.targets, "/"))
		}
	}
}

func moduleBaseForPID(pid uint32, name string) (uintptr, error) {
	const maxAttempts = 3 // module snapshots can race with process startup
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		snap, err := windows.CreateToolhelp32Snapshot(
			windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32, pid)
		if err != nil {
			lastErr = err
			continue
		}
		base, err := scanModules(snap, name)
		windows.CloseHandle(snap)
		if err == nil {
			return base, nil
		}
		lastErr = err
	}
	return 0, lastErr
}

func scanModules(snap windows.Handle, name string) (uintptr, error) {
	var me windows.ModuleEntry32
	me.Size = uint32(unsafe.Sizeof(me))
	if err := windows.Module32First(snap, &me); err != nil {
		return 0, fmt.Errorf("Module32First: %w", err)
	}
	for {
		mod := windows.UTF16ToString(me.Module[:])
		if strings.EqualFold(mod, name) {
			return me.ModBaseAddr, nil
		}
		if err := windows.Module32Next(snap, &me); err != nil {
			return 0, fmt.Errorf("module %q not found in process", name)
		}
	}
}
