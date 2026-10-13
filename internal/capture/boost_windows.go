//go:build windows

package capture

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
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
	procSetThreadAffinityMask            = kernel32.NewProc("SetThreadAffinityMask")
	procGetThreadDescription             = kernel32.NewProc("GetThreadDescription")
	procGetThreadTimes                   = kernel32.NewProc("GetThreadTimes")
)

const (
	relationProcessorCore  = 0
	threadPriorityHighest  = 2
	gameBoostProcessAccess = windows.PROCESS_SET_INFORMATION | windows.PROCESS_QUERY_LIMITED_INFORMATION
	gameThreadAccess       = windows.THREAD_QUERY_LIMITED_INFORMATION | windows.THREAD_SET_INFORMATION
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

// gameThreadIDs lists the thread ids of one process from a Toolhelp snapshot.
func gameThreadIDs(pid uint32) ([]uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return nil, fmt.Errorf("thread snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)
	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	var ids []uint32
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID == pid {
			ids = append(ids, te.ThreadID)
		}
	}
	return ids, nil
}

// describeThread reads a thread's UE name and creation time; both are best
// effort and only need the limited query right.
func describeThread(tid uint32) threadInfo {
	t := threadInfo{ID: tid}
	h, err := windows.OpenThread(windows.THREAD_QUERY_LIMITED_INFORMATION, false, tid)
	if err != nil {
		return t
	}
	defer windows.CloseHandle(h)
	var desc *uint16
	if r, _, _ := procGetThreadDescription.Call(uintptr(h), uintptr(unsafe.Pointer(&desc))); int32(r) >= 0 && desc != nil {
		t.Name = windows.UTF16PtrToString(desc)
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(desc)))
	}
	var creation, exit, kernel, user windows.Filetime
	if r, _, _ := procGetThreadTimes.Call(uintptr(h), uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); r != 0 {
		t.Creation = uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	}
	return t
}

// setThreadAffinity pins one thread and returns the mask it had before.
func setThreadAffinity(tid uint32, mask uint64) (prev uint64, err error) {
	h, err := windows.OpenThread(gameThreadAccess, false, tid)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)
	r, _, e := procSetThreadAffinityMask.Call(uintptr(h), uintptr(mask))
	if r == 0 {
		return 0, e
	}
	return uint64(r), nil
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

	cores     []coreInfo
	mask      uint64 // performance-core mask, 0 = leave affinity alone
	maskKnown bool
	maskNote  string

	failPID    uint32 // a process that refused us; not retried every tick
	failDetail string

	// Game-thread isolation (SetThreadIsolation): the UE game thread alone on
	// the last usable core, every other game thread and this app elsewhere.
	// Re-swept on every tick because the game keeps creating threads, which
	// inherit the process mask.
	isolate     bool
	isoApplied  bool
	isoGame     uint64 // the game thread's CPU
	isoOthers   uint64 // every other game thread's CPUs
	isoReserved uint64 // the whole core kept for the game thread (CPU + sibling)
	isoTID      uint32 // the game thread; 0 = not identified yet
	isoByName   bool   // identified by its UE name rather than creation time
	isoThreads  int    // game threads pinned on the last sweep
	isoFailPID  uint32
	isoFail     string
	ownMask     uint64 // this app's affinity before isolation moved it; 0 = untouched
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

// SetThreadIsolation turns the game-thread isolation on or off. It rides on the
// boost: threads are only pinned while the process boost is applied, and a
// process that refuses thread access is not retried until it restarts or the
// switch is flipped again.
func (b *GameBoost) SetThreadIsolation(on bool, pid uint32) GameBoostState {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.isolate = on
	b.isoFailPID = 0
	if b.applied && b.pid == pid && pid != 0 {
		b.isolateTickLocked()
	}
	return b.stateLocked(pid)
}

// Tick is called periodically with the game's current PID (0 = not running).
func (b *GameBoost) Tick(pid uint32) GameBoostState {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case pid == 0:
		// The game is gone; there is nothing to restore in it and a new launch
		// gets a fresh attempt even if the previous process refused us.
		b.forgetLocked()
	case !b.enabled:
		if b.applied {
			b.restoreLocked()
		}
	case b.applied && b.pid == pid:
		if b.verifyLocked() {
			b.isolateTickLocked()
		}
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
	switch {
	case b.isoApplied:
		st.Isolated = true
		st.IsolateDetail = fmt.Sprintf("GameThread → CPU %s, %d threads → CPU %s", cpuList(b.isoGame), b.isoThreads-1, cpuList(b.isoOthers))
		if !b.isoByName {
			st.IsolateDetail += " (main thread by creation time)"
		}
	case b.isolate && pid != 0 && pid == b.isoFailPID:
		st.IsolateDetail = b.isoFail
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
		b.cores = cores
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
	b.isoGame, b.isoOthers, b.isoReserved, b.isoTID = 0, 0, 0, 0 // plan against this process's mask
	b.isolateTickLocked()
}

// verifyLocked re-reads the boosted process each tick: if it exited, forget it;
// if something lowered its priority again, put it back.
func (b *GameBoost) verifyLocked() bool {
	h, err := windows.OpenProcess(gameBoostProcessAccess, false, b.pid)
	if err != nil {
		b.forgetLocked()
		return false
	}
	defer windows.CloseHandle(h)
	if prio, err := windows.GetPriorityClass(h); err == nil && prio != windows.HIGH_PRIORITY_CLASS {
		_ = windows.SetPriorityClass(h, windows.HIGH_PRIORITY_CLASS)
	}
	return true
}

// forgetLocked drops everything about a game process that is gone. Nothing in
// it needs restoring, but this app's own affinity does.
func (b *GameBoost) forgetLocked() {
	b.restoreOwnLocked()
	b.pid, b.applied, b.affinityChanged = 0, false, false
	b.failPID = 0
	b.isoApplied, b.isoTID, b.isoThreads, b.isoFailPID = false, 0, 0, 0
}

func (b *GameBoost) restoreLocked() {
	if !b.applied {
		return
	}
	b.unisolateLocked() // thread masks first: they must stay inside the process mask
	if h, err := windows.OpenProcess(gameBoostProcessAccess, false, b.pid); err == nil {
		_ = windows.SetPriorityClass(h, b.origPriority)
		if b.affinityChanged {
			_, _, _ = procSetProcessAffinityMask.Call(uintptr(h), uintptr(b.origAffinity))
		}
		windows.CloseHandle(h)
	}
	b.pid, b.applied, b.affinityChanged = 0, false, false
}

// processMaskLocked is the affinity the boosted process has right now, which is
// the ceiling for every thread mask.
func (b *GameBoost) processMaskLocked() uint64 {
	if b.affinityChanged {
		return b.mask
	}
	return b.origAffinity
}

func (b *GameBoost) isolateTickLocked() {
	switch {
	case b.isolate && b.pid != b.isoFailPID:
		b.sweepLocked()
	case !b.isolate && b.isoApplied:
		b.unisolateLocked()
	}
}

// sweepLocked pins every thread of the boosted process: the game thread to its
// own CPU, the rest away from that core. Run every tick so threads the game
// created since (they inherit the process mask) are caught within seconds.
func (b *GameBoost) sweepLocked() {
	fail := func(err error) {
		b.isoFailPID, b.isoFail = b.pid, err.Error()
		b.isoApplied = false
	}
	if b.isoGame == 0 {
		usable := b.processMaskLocked()
		game, others, ok := isolationPlan(b.cores, usable)
		if !ok {
			fail(fmt.Errorf("need at least two usable cores (process has CPU %s)", cpuList(usable)))
			return
		}
		b.isoGame, b.isoOthers, b.isoReserved = game, others, usable&^others
	}
	tids, err := gameThreadIDs(b.pid)
	if err != nil {
		fail(err)
		return
	}
	if len(tids) == 0 {
		fail(errors.New("no threads visible"))
		return
	}
	if b.isoTID == 0 || !slices.Contains(tids, b.isoTID) {
		threads := make([]threadInfo, 0, len(tids))
		for _, tid := range tids {
			threads = append(threads, describeThread(tid))
		}
		b.isoTID, b.isoByName = pickGameThread(threads)
		if b.isoTID == 0 {
			fail(errors.New("game thread not identified (thread query denied)"))
			return
		}
	}
	if !b.isoByName {
		// The first sweep usually lands within seconds of launch, before UE has
		// named its main thread, so keep looking for the name on every sweep
		// until it appears. Normally it turns up on the thread already pinned
		// (the primary thread) and only the status label changes; should it
		// appear on another thread, follow it: this loop pins by isoTID.
		for _, tid := range tids {
			if describeThread(tid).Name == gameThreadName {
				b.isoTID, b.isoByName = tid, true
				break
			}
		}
	}
	pinned := 0
	for _, tid := range tids {
		want := b.isoOthers
		if tid == b.isoTID {
			want = b.isoGame
		}
		if _, err := setThreadAffinity(tid, want); err != nil {
			if tid == b.isoTID {
				fail(fmt.Errorf("pin game thread: %w", err))
				return
			}
			continue // exited between the snapshot and now
		}
		pinned++
	}
	b.isoThreads = pinned
	b.isoApplied = true
	b.pinOwnLocked()
}

// unisolateLocked puts every thread back on the process's own mask.
func (b *GameBoost) unisolateLocked() {
	b.restoreOwnLocked()
	if b.isoApplied && b.pid != 0 {
		back := b.processMaskLocked()
		if tids, err := gameThreadIDs(b.pid); err == nil {
			for _, tid := range tids {
				_, _ = setThreadAffinity(tid, back)
			}
		}
	}
	b.isoApplied, b.isoTID, b.isoThreads = false, 0, 0
}

// pinOwnLocked keeps this app (its packet loop runs at HIGHEST) off the core
// reserved for the game thread.
func (b *GameBoost) pinOwnLocked() {
	if b.ownMask != 0 {
		return
	}
	self := windows.CurrentProcess()
	var procMask, sysMask uint64
	if r, _, _ := procGetProcessAffinityMask.Call(uintptr(self), uintptr(unsafe.Pointer(&procMask)), uintptr(unsafe.Pointer(&sysMask))); r == 0 {
		return
	}
	want := procMask &^ b.isoReserved
	if want == 0 || want == procMask {
		return
	}
	if r, _, _ := procSetProcessAffinityMask.Call(uintptr(self), uintptr(want)); r != 0 {
		b.ownMask = procMask
	}
}

func (b *GameBoost) restoreOwnLocked() {
	if b.ownMask == 0 {
		return
	}
	_, _, _ = procSetProcessAffinityMask.Call(uintptr(windows.CurrentProcess()), uintptr(b.ownMask))
	b.ownMask = 0
}
