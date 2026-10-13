//go:build windows

package capture

import (
	"os"
	"os/exec"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Gated end-to-end check of the game boost against a throwaway child process
// instead of Aion2.exe: BOOSTTEST=1 go test ./internal/capture -run Manual -v
// It proves the OpenProcess/OpenThread rights, the CPU topology read, the
// game-thread isolation on real threads, and that apply and restore leave the
// process (and this one) exactly as found.
func TestManualGameBoostOnChildProcess(t *testing.T) {
	if os.Getenv("BOOSTTEST") == "" {
		t.Skip("set BOOSTTEST=1 to run")
	}

	cores, multi, err := readCores()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cores=%d multiGroup=%t pcoreMask=%#x (%s)", len(cores), multi, performanceCoreMask(cores), cpuList(performanceCoreMask(cores)))

	cmd := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	pid := uint32(cmd.Process.Pid)

	processAffinity := func(h windows.Handle) uint64 {
		var procMask, sysMask uint64
		if r, _, e := procGetProcessAffinityMask.Call(uintptr(h), uintptr(unsafe.Pointer(&procMask)), uintptr(unsafe.Pointer(&sysMask))); r == 0 {
			t.Fatal(e)
		}
		return procMask
	}
	read := func() (uint32, uint64) {
		h, err := windows.OpenProcess(gameBoostProcessAccess, false, pid)
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(h)
		prio, err := windows.GetPriorityClass(h)
		if err != nil {
			t.Fatal(err)
		}
		return prio, processAffinity(h)
	}
	// threadAffinity reads a thread's mask without changing it: the set call
	// returns the previous mask, which is then put straight back. ok is false
	// for a thread that exited since the snapshot.
	threadAffinity := func(tid uint32, procMask uint64) (uint64, bool) {
		h, err := windows.OpenThread(gameThreadAccess, false, tid)
		if err != nil {
			return 0, false
		}
		defer windows.CloseHandle(h)
		prev, _, e := procSetThreadAffinityMask.Call(uintptr(h), uintptr(procMask))
		if prev == 0 {
			t.Fatal(e)
		}
		if r, _, e := procSetThreadAffinityMask.Call(uintptr(h), prev); r == 0 {
			t.Fatal(e)
		}
		return uint64(prev), true
	}
	prioBefore, maskBefore := read()
	ownBefore := processAffinity(windows.CurrentProcess())

	b := NewGameBoost()
	st := b.SetEnabled(true, pid)
	t.Logf("apply: %+v", st)
	if !st.Applied {
		t.Fatalf("boost not applied: %s", st.Detail)
	}
	prio, maskBoosted := read()
	if prio != windows.HIGH_PRIORITY_CLASS {
		t.Fatalf("priority class = %#x, want HIGH", prio)
	}
	if want := performanceCoreMask(cores); want != 0 && maskBoosted != want {
		t.Fatalf("affinity = %#x, want %#x", maskBoosted, want)
	}

	// A second tick on the same process must be a no-op that keeps Applied.
	if st := b.Tick(pid); !st.Applied {
		t.Fatalf("tick lost the applied state: %+v", st)
	}

	// Isolation: the child's main thread alone on its CPU (ping names no
	// threads, so this exercises the creation-time fallback), every other
	// thread off that core, and this process off that core too.
	// The snapshot is taken BEFORE the sweep: a thread the child creates after
	// it inherits the process mask until the next tick, by design.
	tids, err := gameThreadIDs(pid)
	if err != nil {
		t.Fatal(err)
	}
	st = b.SetThreadIsolation(true, pid)
	t.Logf("isolate: %+v", st)
	if !st.Isolated {
		t.Fatalf("isolation not applied: %s", st.IsolateDetail)
	}
	checked := 0
	for _, tid := range tids {
		want := b.isoOthers
		if tid == b.isoTID {
			want = b.isoGame
		}
		if got, ok := threadAffinity(tid, maskBoosted); ok && got != want {
			t.Fatalf("thread %d affinity = %#x, want %#x", tid, got, want)
		} else if ok {
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no child threads could be checked")
	}
	if own := processAffinity(windows.CurrentProcess()); own&b.isoReserved != 0 {
		t.Fatalf("this process still allowed on the reserved core: %#x", own)
	}
	if st := b.Tick(pid); !st.Applied || !st.Isolated {
		t.Fatalf("tick lost isolation: %+v", st)
	}

	if tids, err = gameThreadIDs(pid); err != nil {
		t.Fatal(err)
	}
	st = b.SetThreadIsolation(false, pid)
	if st.Isolated {
		t.Fatalf("disable left isolation on: %+v", st)
	}
	for _, tid := range tids {
		if got, ok := threadAffinity(tid, maskBoosted); ok && got != maskBoosted {
			t.Fatalf("thread %d affinity after un-isolate = %#x, want process mask %#x", tid, got, maskBoosted)
		}
	}
	if own := processAffinity(windows.CurrentProcess()); own != ownBefore {
		t.Fatalf("this process's affinity not restored: %#x, want %#x", own, ownBefore)
	}

	st = b.SetEnabled(false, pid)
	if st.Applied || st.Enabled {
		t.Fatalf("disable left state %+v", st)
	}
	prioAfter, maskAfter := read()
	if prioAfter != prioBefore || maskAfter != maskBefore {
		t.Fatalf("restore mismatch: priority %#x->%#x affinity %#x->%#x", prioBefore, prioAfter, maskBefore, maskAfter)
	}
}
