//go:build windows

package capture

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Why this exists: the capture engine sits inline on every game packet, and the
// game's own input thread competes with browsers, Discord and the like for the
// fast cores. Raising priorities and keeping Aion2.exe on the performance cores
// is what players otherwise reach for Process Lasso to do; doing it here means
// one program and no third-party hook in the game process.

var (
	kernel32                             = windows.NewLazySystemDLL("kernel32.dll")
	procGetLogicalProcessorInformationEx = kernel32.NewProc("GetLogicalProcessorInformationEx")
	procGetProcessAffinityMask           = kernel32.NewProc("GetProcessAffinityMask")
	procSetProcessAffinityMask           = kernel32.NewProc("SetProcessAffinityMask")
	procSetThreadPriority                = kernel32.NewProc("SetThreadPriority")
)

const (
	relationProcessorCore  = 0
	threadPriorityHighest  = 2
	gameBoostProcessAccess = windows.PROCESS_SET_INFORMATION | windows.PROCESS_QUERY_LIMITED_INFORMATION
)

// BoostOwnProcess raises this process one class above normal so the packet loop
// is scheduled ahead of ordinary background work. Best effort.
func BoostOwnProcess() error {
	return windows.SetPriorityClass(windows.CurrentProcess(), windows.ABOVE_NORMAL_PRIORITY_CLASS)
}

// pinCaptureThread locks the calling goroutine to its OS thread and raises that
// thread's priority. The lock matters: a thread priority is per OS thread, and an
// unlocked goroutine migrates. The returned func undoes the pin.
func pinCaptureThread() (unpin func(), err error) {
	runtime.LockOSThread()
	if r, _, e := procSetThreadPriority.Call(uintptr(windows.CurrentThread()), threadPriorityHighest); r == 0 {
		err = e
	}
	return runtime.UnlockOSThread, err
}

// readCores asks the OS for every physical core and its efficiency class.
func readCores() ([]coreInfo, bool, error) {
	var size uint32
	r, _, e := procGetLogicalProcessorInformationEx.Call(relationProcessorCore, 0, uintptr(unsafe.Pointer(&size)))
	if r != 0 || e != windows.ERROR_INSUFFICIENT_BUFFER || size == 0 {
		return nil, false, fmt.Errorf("GetLogicalProcessorInformationEx size query: %v", e)
	}
	buf := make([]byte, size)
	r, _, e = procGetLogicalProcessorInformationEx.Call(relationProcessorCore, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return nil, false, fmt.Errorf("GetLogicalProcessorInformationEx: %v", e)
	}
	return parseCores(buf[:size])
}

// GameBoost keeps the game process at High priority and on the performance cores
// while it is running, and puts both back when turned off. It is driven by
// Tick from the game watcher, so it also notices the game restarting.
type GameBoost struct {
	mu sync.Mutex

	enabled bool
	pid     uint32 // the process we changed; 0 when nothing is applied
	applied bool

	origPriority    uint32
	origAffinity    uint64
	affinityChanged bool

	mask      uint64 // performance-core mask, 0 = leave affinity alone
	maskKnown bool
	maskNote  string

	failPID    uint32 // a process that refused us; not retried every tick
	failDetail string
}

func NewGameBoost() *GameBoost { return &GameBoost{} }

// SetEnabled turns the boost on or off. Off restores the game's original
// priority and affinity if this app changed them.
func (b *GameBoost) SetEnabled(on bool, pid uint32) GameBoostState {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.enabled = on
	if on {
		b.failPID = 0
		if pid != 0 {
			b.applyLocked(pid)
		}
	} else {
		b.restoreLocked()
	}
	return b.stateLocked(pid)
}

// Tick is called periodically with the game's current PID (0 = not running).
func (b *GameBoost) Tick(pid uint32) GameBoostState {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case pid == 0:
		// The game is gone; there is nothing to restore and a new launch gets a
		// fresh attempt even if the previous process refused us.
		b.pid, b.applied, b.affinityChanged = 0, false, false
		b.failPID = 0
	case !b.enabled:
		if b.applied {
			b.restoreLocked()
		}
	case b.applied && b.pid == pid:
		b.verifyLocked()
	case pid != b.failPID:
		b.applyLocked(pid)
	}
	return b.stateLocked(pid)
}

func (b *GameBoost) stateLocked(pid uint32) GameBoostState {
	st := GameBoostState{Enabled: b.enabled, Applied: b.applied, PID: pid}
	switch {
	case b.applied:
		st.Detail = "priority High" + b.maskNote
	case b.enabled && pid != 0 && pid == b.failPID:
		st.Detail = b.failDetail
	}
	return st
}

func (b *GameBoost) ensureMaskLocked() {
	if b.maskKnown {
		return
	}
	b.maskKnown = true
	cores, multiGroup, err := readCores()
	switch {
	case err != nil:
		b.maskNote = " (CPU affinity unchanged: " + err.Error() + ")"
	case multiGroup:
		b.maskNote = " (CPU affinity unchanged: more than one processor group)"
	default:
		b.mask = performanceCoreMask(cores)
		if b.mask == 0 {
			b.maskNote = " (CPU affinity unchanged: no efficiency cores)"
		} else {
			b.maskNote = ", CPU " + cpuList(b.mask)
		}
	}
}

func (b *GameBoost) applyLocked(pid uint32) {
	b.ensureMaskLocked()
	fail := func(err error) {
		b.applied, b.affinityChanged = false, false
		b.failPID, b.failDetail = pid, err.Error()
	}
	h, err := windows.OpenProcess(gameBoostProcessAccess, false, pid)
	if err != nil {
		fail(fmt.Errorf("open game process: %w", err))
		return
	}
	defer windows.CloseHandle(h)

	prio, err := windows.GetPriorityClass(h)
	if err != nil {
		fail(fmt.Errorf("read priority: %w", err))
		return
	}
	var procMask, sysMask uint64
	if r, _, e := procGetProcessAffinityMask.Call(uintptr(h), uintptr(unsafe.Pointer(&procMask)), uintptr(unsafe.Pointer(&sysMask))); r == 0 {
		fail(fmt.Errorf("read affinity: %v", e))
		return
	}
	if err := windows.SetPriorityClass(h, windows.HIGH_PRIORITY_CLASS); err != nil {
		fail(fmt.Errorf("set priority: %w", err))
		return
	}
	b.origPriority, b.origAffinity = prio, procMask
	b.affinityChanged = false
	if b.mask != 0 && b.mask&sysMask == b.mask && procMask != b.mask {
		if r, _, e := procSetProcessAffinityMask.Call(uintptr(h), uintptr(b.mask)); r == 0 {
			_ = windows.SetPriorityClass(h, prio) // leave the process as we found it
			fail(fmt.Errorf("set affinity: %v", e))
			return
		}
		b.affinityChanged = true
	}
	b.pid, b.applied = pid, true
	b.failPID = 0
}

// verifyLocked re-reads the boosted process each tick: if it exited, forget it;
// if something lowered its priority again, put it back.
func (b *GameBoost) verifyLocked() {
	h, err := windows.OpenProcess(gameBoostProcessAccess, false, b.pid)
	if err != nil {
		b.pid, b.applied, b.affinityChanged = 0, false, false
		return
	}
	defer windows.CloseHandle(h)
	if prio, err := windows.GetPriorityClass(h); err == nil && prio != windows.HIGH_PRIORITY_CLASS {
		_ = windows.SetPriorityClass(h, windows.HIGH_PRIORITY_CLASS)
	}
}

func (b *GameBoost) restoreLocked() {
	if !b.applied {
		return
	}
	if h, err := windows.OpenProcess(gameBoostProcessAccess, false, b.pid); err == nil {
		_ = windows.SetPriorityClass(h, b.origPriority)
		if b.affinityChanged {
			_, _, _ = procSetProcessAffinityMask.Call(uintptr(h), uintptr(b.origAffinity))
		}
		windows.CloseHandle(h)
	}
	b.pid, b.applied, b.affinityChanged = 0, false, false
}
