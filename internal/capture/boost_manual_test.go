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
// It proves the OpenProcess rights, the CPU topology read, and that apply and
// restore leave the process exactly as found.
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
		var procMask, sysMask uint64
		if r, _, e := procGetProcessAffinityMask.Call(uintptr(h), uintptr(unsafe.Pointer(&procMask)), uintptr(unsafe.Pointer(&sysMask))); r == 0 {
			t.Fatal(e)
		}
		return prio, procMask
	}
	prioBefore, maskBefore := read()

	b := NewGameBoost()
	st := b.SetEnabled(true, pid)
	t.Logf("apply: %+v", st)
	if !st.Applied {
		t.Fatalf("boost not applied: %s", st.Detail)
	}
	prio, mask := read()
	if prio != windows.HIGH_PRIORITY_CLASS {
		t.Fatalf("priority class = %#x, want HIGH", prio)
	}
	if want := performanceCoreMask(cores); want != 0 && mask != want {
		t.Fatalf("affinity = %#x, want %#x", mask, want)
	}

	// A second tick on the same process must be a no-op that keeps Applied.
	if st := b.Tick(pid); !st.Applied {
		t.Fatalf("tick lost the applied state: %+v", st)
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
