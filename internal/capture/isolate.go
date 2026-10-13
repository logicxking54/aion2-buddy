package capture

// Game-thread isolation: the measured profile of Aion 2 is one serial UE
// GameThread (~0.9 core) with everything else spread over ~2 more cores, on a
// CPU with 8+ cores. Nothing is throughput-bound, so the only scheduler cost
// left is the game thread occasionally sharing a core's execution units with a
// worker that woke on its SMT sibling, or being migrated to a cold core. The
// plan below gives the game thread one logical CPU of its own, keeps that
// core's sibling empty, and moves every other game thread (and this app) to the
// remaining cores. Nothing is injected into the game: it is the same
// SetThreadAffinityMask that Task Manager / Process Lasso use.

// threadInfo is one thread of the game process as the sweep sees it.
type threadInfo struct {
	ID       uint32
	Name     string // the description UE sets (SetThreadDescription); "" if none
	Creation uint64 // FILETIME ticks; 0 when unreadable
}

// gameThreadName is how UE 5 labels its main loop thread.
const gameThreadName = "GameThread"

// pickGameThread finds the thread that runs the game loop: the one UE named
// "GameThread", or failing that the earliest-created thread, which is the
// process's main thread and in UE the game thread. Returns 0 when nothing is
// known.
func pickGameThread(threads []threadInfo) (id uint32, byName bool) {
	var earliest threadInfo
	for _, t := range threads {
		if t.Name == gameThreadName {
			return t.ID, true
		}
		if t.Creation != 0 && (earliest.ID == 0 || t.Creation < earliest.Creation) {
			earliest = t
		}
	}
	return earliest.ID, false
}

// isolationPlan splits the usable cores into one logical CPU reserved for the
// game thread and the rest for every other thread. The reserved core is the
// LAST usable core: CPU 0 is where Windows parks most interrupts and DPCs, and
// plenty of software defaults to the low CPUs. The game thread gets one logical
// CPU of that core; its SMT sibling is left out of both masks so no game thread
// shares the core's execution units with it. ok is false when fewer than two
// cores are usable (everyone else would have nowhere to go).
func isolationPlan(cores []coreInfo, usable uint64) (game, others uint64, ok bool) {
	var last coreInfo
	n := 0
	for _, c := range cores {
		if c.Mask == 0 || c.Mask&usable != c.Mask {
			continue // outside the process's cores (e.g. an E-core the boost excluded)
		}
		n++
		if c.Mask > last.Mask {
			last = c
		}
	}
	if n < 2 {
		return 0, 0, false
	}
	game = last.Mask & -last.Mask // the core's lowest logical CPU
	others = usable &^ last.Mask
	return game, others, true
}
