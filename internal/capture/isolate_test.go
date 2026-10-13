package capture

import "testing"

// 12900K with the boost's P-core mask: the game thread goes to CPU 14 (the last
// P-core's first thread), CPU 15 stays empty, everything else gets 0-13. The
// E-cores (16-23) have higher masks but are outside the usable set.
func TestIsolationPlanReservesLastUsableCore(t *testing.T) {
	game, others, ok := isolationPlan(hybridCores(), 0xFFFF)
	if !ok || game != 1<<14 || others != 0x3FFF {
		t.Fatalf("plan = game %#x others %#x ok %t; want game 0x4000 others 0x3fff", game, others, ok)
	}
}

// With SMT off every core is one CPU: the last core is reserved whole.
func TestIsolationPlanWithoutSMT(t *testing.T) {
	var cores []coreInfo
	for i := 0; i < 8; i++ {
		cores = append(cores, coreInfo{Mask: 1 << uint(i)})
	}
	game, others, ok := isolationPlan(cores, 0xFF)
	if !ok || game != 1<<7 || others != 0x7F {
		t.Fatalf("plan = game %#x others %#x ok %t; want game 0x80 others 0x7f", game, others, ok)
	}
}

func TestIsolationPlanNeedsTwoUsableCores(t *testing.T) {
	if _, _, ok := isolationPlan([]coreInfo{{Mask: 0x3}}, 0x3); ok {
		t.Fatal("one core was accepted")
	}
	if _, _, ok := isolationPlan(hybridCores(), 0x3); ok {
		t.Fatal("a usable mask covering one core was accepted")
	}
	if _, _, ok := isolationPlan(nil, 0xFFFF); ok {
		t.Fatal("no topology was accepted")
	}
}

func TestPickGameThreadPrefersUEName(t *testing.T) {
	threads := []threadInfo{
		{ID: 10, Name: "RenderThread", Creation: 100},
		{ID: 11, Name: "", Creation: 50}, // earliest, but UE told us which one it is
		{ID: 12, Name: gameThreadName, Creation: 200},
	}
	if id, byName := pickGameThread(threads); id != 12 || !byName {
		t.Fatalf("picked %d byName=%t, want 12 by name", id, byName)
	}
}

func TestPickGameThreadFallsBackToEarliest(t *testing.T) {
	threads := []threadInfo{
		{ID: 10, Creation: 300},
		{ID: 11, Creation: 0}, // unreadable creation time must not win by default
		{ID: 12, Creation: 100},
		{ID: 13, Creation: 200},
	}
	if id, byName := pickGameThread(threads); id != 12 || byName {
		t.Fatalf("picked %d byName=%t, want 12 by creation time", id, byName)
	}
	if id, _ := pickGameThread(nil); id != 0 {
		t.Fatalf("picked %d from no threads", id)
	}
}
