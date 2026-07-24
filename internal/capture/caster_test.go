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

	h.e.resetCasterState()
	if len(h.e.actLog) != 0 || len(h.e.actSeen) != 0 || h.e.casterFilter != 0 {
		t.Fatalf("reset left caster state behind: actLog=%v actSeen=%v filter=%d",
			h.e.actLog, h.e.actSeen, h.e.casterFilter)
	}
}
