//go:build windows

package capture

import (
	"testing"
	"time"
)

// casterHarness drives recordActCaster on a virtual clock while capturing the
// emitted picker lists, so tests assert on exactly what the UI receives and can
// step time without sleeping.
type casterHarness struct {
	e    *Engine
	last []actCaster
	now  int64 // virtual clock, ns
}

func newCasterHarness() *casterHarness {
	h := &casterHarness{now: time.Date(2026, 7, 24, 20, 0, 0, 0, time.UTC).UnixNano()}
	h.e = NewEngine(func(event string, payload any) {
		if event != "capture:act-casters" {
			return
		}
		if list, ok := payload.([]actCaster); ok {
			h.last = list
		}
	})
	return h
}

func (h *casterHarness) advance(d time.Duration) { h.now += int64(d) }

func (h *casterHarness) cast(caster uint64) {
	h.advance(time.Second)
	h.e.recordActCaster(caster, h.now)
}

func (h *casterHarness) ids() []uint64 {
	out := make([]uint64, 0, len(h.last))
	for _, c := range h.last {
		out = append(out, c.ID)
	}
	return out
}

// Everyone casting your configured skills is offered as an option — the picker is
// how you tell yourself apart from a same-class party member.
func TestActCasterListsEveryCaster(t *testing.T) {
	h := newCasterHarness()
	h.cast(7334)
	h.cast(10327)
	h.cast(7334)

	ids := h.ids()
	if len(ids) != 2 || ids[0] != 7334 || ids[1] != 10327 {
		t.Fatalf("picker lists %v, want both casters in first-seen order", ids)
	}
}

// Re-entering an instance re-assigns entity keys. The previous run's key must drop
// out of the picker, otherwise the UI's sticky selection keeps pointing at a key
// that will never cast again and every run needs a manual re-pick.
func TestActCasterExpiresAfterTTL(t *testing.T) {
	h := newCasterHarness()
	h.cast(1001)

	h.advance(actCasterTTL + time.Second)
	h.cast(2002) // first cast of the new run prunes the stale key

	if ids := h.ids(); len(ids) != 1 || ids[0] != 2002 {
		t.Fatalf("picker lists %v, want only the new key 2002", ids)
	}
}

// A caster that's still casting must never be expired out from under the selection.
func TestActCasterSurvivesWhileStillCasting(t *testing.T) {
	h := newCasterHarness()
	for elapsed := time.Duration(0); elapsed < 3*actCasterTTL; elapsed += actCasterTTL / 2 {
		h.advance(actCasterTTL / 2)
		h.cast(1001)
	}
	if ids := h.ids(); len(ids) != 1 || ids[0] != 1001 {
		t.Fatalf("picker lists %v, want the active caster kept", ids)
	}
}

// Only the silent caster ages out; an active one stays selectable.
func TestActCasterExpiresIndependently(t *testing.T) {
	h := newCasterHarness()
	h.cast(1001)
	h.cast(2002)

	for i := 0; i < 4; i++ { // 2002 keeps casting while 1001 goes quiet
		h.advance(actCasterTTL / 3)
		h.cast(2002)
	}

	if ids := h.ids(); len(ids) != 1 || ids[0] != 2002 {
		t.Fatalf("picker lists %v, want only the still-active caster 2002", ids)
	}
}

// Starting a new capture session must leave no trace of the previous one's keys.
func TestResetCasterStateClearsEverything(t *testing.T) {
	h := newCasterHarness()
	h.cast(42)
	h.e.autoCaster = 42
	h.e.autoCasterName = "Mali"

	h.e.resetCasterState()
	if len(h.e.actLog) != 0 || len(h.e.actSeen) != 0 || h.e.casterFilter != 0 || h.e.autoCaster != 0 {
		t.Fatalf("reset left caster state behind: actLog=%v actSeen=%v auto=%d override=%d",
			h.e.actLog, h.e.actSeen, h.e.autoCaster, h.e.casterFilter)
	}
}

func TestLocalIdentityDrivesCasterBeforeFirstACT(t *testing.T) {
	e := NewEngine(func(string, any) {})
	e.acceptLocalIdentity(actorBinding{actorID: 10327, name: "Mali茉莉"})

	if got := e.effectiveCaster(); got != 10327 {
		t.Fatalf("effective caster = %d, want authoritative local id 10327", got)
	}
	if len(e.actLog) != 0 {
		t.Fatalf("identity should not require an ACT cast, actLog=%v", e.actLog)
	}
}

func TestManualCasterOverridesAutoUntilInstanceChanges(t *testing.T) {
	var last localCasterEvent
	e := NewEngine(func(event string, payload any) {
		if event == "capture:local-caster" {
			last, _ = payload.(localCasterEvent)
		}
	})
	e.acceptLocalIdentity(actorBinding{actorID: 10327, name: "Mali茉莉"})
	e.SetCasterFilter(7334)

	// Repeated self records in the same instance must not undo a deliberate pick.
	e.acceptLocalIdentity(actorBinding{actorID: 10327, name: "Mali茉莉"})
	if got := e.effectiveCaster(); got != 7334 {
		t.Fatalf("manual override lost: effective caster = %d", got)
	}

	// Entity ids are reassigned on instance entry, so an override tied to the old
	// instance is stale and auto identity takes over again.
	e.acceptLocalIdentity(actorBinding{actorID: 1235, name: "Mali茉莉"})
	if got := e.effectiveCaster(); got != 1235 {
		t.Fatalf("new instance did not restore auto identity: effective caster = %d", got)
	}
	if !last.ManualOverrideCleared {
		t.Fatal("frontend was not told to clear the stale manual override")
	}
}

func TestCasterMatchFailsClosedUntilIdentityIsKnown(t *testing.T) {
	cases := []struct {
		name      string
		effective uint64
		observed  uint64
		want      bool
	}{
		{name: "no identity", effective: 0, observed: 10327, want: false},
		{name: "local caster", effective: 10327, observed: 10327, want: true},
		{name: "same-class party member", effective: 10327, observed: 7334, want: false},
		{name: "coalesced cast with unreadable id", effective: 10327, observed: 0, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := casterMatches(tc.effective, tc.observed); got != tc.want {
				t.Fatalf("casterMatches(%d, %d) = %t, want %t", tc.effective, tc.observed, got, tc.want)
			}
		})
	}
}

func TestFirstConfiguredCasterIsImmediateProvisionalFallback(t *testing.T) {
	e := NewEngine(func(string, any) {})
	if got := e.observeConfiguredCaster(4695, time.Now().UnixNano()); got != 4695 {
		t.Fatalf("effective caster = %d, want first observed caster 4695", got)
	}
	if !e.autoProvisional || e.autoAmbiguous {
		t.Fatalf("fallback state provisional=%t ambiguous=%t", e.autoProvisional, e.autoAmbiguous)
	}
}

func TestSecondConfiguredCasterFailsProvisionalFallbackClosed(t *testing.T) {
	e := NewEngine(func(string, any) {})
	now := time.Now().UnixNano()
	e.observeConfiguredCaster(4695, now)
	if got := e.observeConfiguredCaster(7334, now+int64(time.Second)); got != 0 {
		t.Fatalf("ambiguous effective caster = %d, want 0", got)
	}
	if e.autoProvisional || !e.autoAmbiguous {
		t.Fatalf("fallback state provisional=%t ambiguous=%t", e.autoProvisional, e.autoAmbiguous)
	}

	// Once ambiguous, later ACTs cannot silently pick whichever caster fires next.
	if got := e.observeConfiguredCaster(4695, now+int64(2*time.Second)); got != 0 {
		t.Fatalf("ambiguous fallback relocked to %d", got)
	}
}

func TestTrustedIdentityReplacesProvisionalFallback(t *testing.T) {
	e := NewEngine(func(string, any) {})
	e.observeConfiguredCaster(7334, time.Now().UnixNano())
	e.acceptLocalIdentity(actorBinding{actorID: 4695, name: "Mali茉莉"})
	if got := e.effectiveCaster(); got != 4695 {
		t.Fatalf("trusted identity did not replace fallback: %d", got)
	}
	if e.autoProvisional || e.autoAmbiguous {
		t.Fatalf("trusted identity left fallback flags: provisional=%t ambiguous=%t", e.autoProvisional, e.autoAmbiguous)
	}
}

func TestLocalIdentityRelocksConfiguredNameTracker(t *testing.T) {
	e := NewEngine(func(string, any) {})
	e.SetCharacterNames([]string{"Mali茉莉"})
	e.acceptLocalIdentity(actorBinding{actorID: 10327, name: "Mali茉莉"})
	if !e.tracker.isMine(10327) {
		t.Fatal("authoritative identity did not lock the name tracker")
	}

	e.acceptLocalIdentity(actorBinding{actorID: 4695, name: "Mali茉莉"})
	if !e.tracker.isMine(4695) || e.tracker.isMine(10327) {
		t.Fatal("new-instance identity did not replace the old tracker key")
	}
}

func TestQuietUnseenCasterStartsNewGenerationWithoutConfiguredName(t *testing.T) {
	var last localCasterEvent
	e := NewEngine(func(event string, payload any) {
		if event == "capture:local-caster" {
			last, _ = payload.(localCasterEvent)
		}
	})
	e.acceptLocalIdentity(actorBinding{actorID: 10327, name: "Mali茉莉"})

	start := time.Now().UnixNano()
	e.observeConfiguredCaster(10327, start)
	e.recordActCaster(10327, start)
	e.SetCasterFilter(10327) // an old manual choice is instance-scoped too

	next := start + int64(actCasterGenerationGap+time.Second)
	if got := e.observeConfiguredCaster(4695, next); got != 4695 {
		t.Fatalf("new generation effective caster = %d, want 4695", got)
	}
	e.recordActCaster(4695, next)

	if !e.autoProvisional || e.autoAmbiguous {
		t.Fatalf("new generation state provisional=%t ambiguous=%t", e.autoProvisional, e.autoAmbiguous)
	}
	if e.casterFilter != 0 {
		t.Fatalf("stale manual override was not cleared: %d", e.casterFilter)
	}
	if e.tracker.hasAnyKeys() {
		t.Fatal("stale character-name tracker key was not cleared")
	}
	if len(e.actLog) != 1 || e.actLog[0] != 4695 {
		t.Fatalf("picker retained stale generation: %v", e.actLog)
	}
	if last.ID != 4695 || !last.ManualOverrideCleared {
		t.Fatalf("local-caster event = %+v, want new id and cleared override", last)
	}
}

func TestRecentDifferentCasterDoesNotReplaceTrustedIdentity(t *testing.T) {
	e := NewEngine(func(string, any) {})
	e.acceptLocalIdentity(actorBinding{actorID: 10327, name: "Mali茉莉"})
	start := time.Now().UnixNano()
	e.observeConfiguredCaster(10327, start)
	e.recordActCaster(10327, start)

	if got := e.observeConfiguredCaster(7334, start+int64(time.Second)); got != 10327 {
		t.Fatalf("same-instance caster replaced trusted identity with %d", got)
	}
}

func TestConfiguredCharacterNameIsTrustedIdentity(t *testing.T) {
	e := NewEngine(func(string, any) {})
	e.SetCharacterNames([]string{"Mali茉莉"})
	e.learnActorBinding(actorBinding{actorID: 4695, name: "Mali茉莉"})
	if got := e.effectiveCaster(); got != 4695 {
		t.Fatalf("exact character-name binding produced caster %d", got)
	}
}
