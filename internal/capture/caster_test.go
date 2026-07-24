//go:build windows

package capture

import (
	"testing"
	"time"
)

// casterHarness drives recordActCaster while capturing the emitted picker lists,
// so tests assert on exactly what the UI receives.
type casterHarness struct {
	e    *Engine
	last []actCaster
}

func newCasterHarness() *casterHarness {
	h := &casterHarness{}
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

// ownCast simulates pressing a skill: our request goes out, the server's ACT for
// caster comes back one round-trip later.
func (h *casterHarness) ownCast(caster uint64) {
	h.e.noteOwnRequest(ownReqSize, time.Now().UnixNano()-int64(80*time.Millisecond))
	h.e.recordActCaster(caster)
}

// otherCast simulates a different player casting with no request of ours in flight.
func (h *casterHarness) otherCast(caster uint64) {
	h.e.lastOwnReqAt = 0
	h.e.recordActCaster(caster)
}

// castInOurWindow simulates someone else's ACT arriving while one of our requests
// is still unanswered — the way a same-class party member can steal a pairing.
func (h *casterHarness) castInOurWindow(caster uint64) {
	h.e.recordActCaster(caster)
}

// sendRequest opens a pairing window without delivering any ACT yet.
func (h *casterHarness) sendRequest() {
	h.e.noteOwnRequest(ownReqSize, time.Now().UnixNano()-int64(80*time.Millisecond))
}

// age backdates a caster's last-seen time, standing in for elapsed real time.
func (h *casterHarness) age(caster uint64, d time.Duration) {
	h.e.actSeen[caster] -= int64(d)
}

func (h *casterHarness) mine() uint64 {
	for _, c := range h.last {
		if c.Mine {
			return c.ID
		}
	}
	return 0
}

func (h *casterHarness) ids() []uint64 {
	out := make([]uint64, 0, len(h.last))
	for _, c := range h.last {
		out = append(out, c.ID)
	}
	return out
}

func TestOwnCastPairingFlagsUsAfterEnoughVotes(t *testing.T) {
	h := newCasterHarness()

	for i := 0; i < ownVotesNeeded-1; i++ {
		h.ownCast(42)
		if h.mine() != 0 {
			t.Fatalf("flagged a caster after %d pairings, want to wait for %d", i+1, ownVotesNeeded)
		}
	}
	h.ownCast(42)
	if got := h.mine(); got != 42 {
		t.Fatalf("mine = %d after %d pairings, want 42", got, ownVotesNeeded)
	}
}

// The whole point of pairing: a same-class party member casting the same skills
// gets listed as an option but is never mistaken for us.
func TestOwnCastPairingIgnoresUnpairedCaster(t *testing.T) {
	h := newCasterHarness()

	for i := 0; i < ownVotesNeeded*3; i++ {
		h.otherCast(77)
	}
	if got := h.mine(); got != 0 {
		t.Fatalf("mine = %d, want 0 — that caster never followed one of our requests", got)
	}

	for i := 0; i < ownVotesNeeded; i++ {
		h.ownCast(42)
	}
	if got := h.mine(); got != 42 {
		t.Fatalf("mine = %d, want 42", got)
	}
	if len(h.last) != 2 {
		t.Fatalf("picker lists %v, want both casters as options", h.ids())
	}
}

// The case the pairing exists for: a party member of the same class casting the
// same skills. Their ACTs keep landing inside our pairing windows, but ours get
// there first, so the window is already spent and they never build a case.
func TestSameClassPartyMemberDoesNotStealTheLock(t *testing.T) {
	const me, friend = 4242, 9999
	h := newCasterHarness()

	for i := 0; i < 12; i++ {
		h.sendRequest()
		h.castInOurWindow(me)     // our own ACT answers our request
		h.castInOurWindow(friend) // theirs lands in the same window, but it's spent
	}

	if got := h.mine(); got != me {
		t.Fatalf("mine = %d, want %d", got, me)
	}
	if v := h.e.ownVotes[friend]; v != 0 {
		t.Fatalf("party member collected %d votes, want 0 — our ACT consumed every window", v)
	}
}

// Same party, but the friend's ACT sometimes beats ours back from the server. They
// pick up the odd vote; we should still end up locked correctly.
func TestSameClassPartyMemberWinningSomeRacesStillLosesOverall(t *testing.T) {
	const me, friend = 4242, 9999
	h := newCasterHarness()

	for i := 0; i < 20; i++ {
		h.sendRequest()
		if i%4 == 0 {
			h.castInOurWindow(friend) // they win the race this round
			h.castInOurWindow(me)     // ours arrives, window already spent
		} else {
			h.castInOurWindow(me)
			h.castInOurWindow(friend)
		}
	}

	if got := h.mine(); got != me {
		t.Fatalf("mine = %d, want %d", got, me)
	}
	if h.e.ownVotes[me] <= h.e.ownVotes[friend] {
		t.Fatalf("votes me=%d friend=%d, want ours clearly ahead", h.e.ownVotes[me], h.e.ownVotes[friend])
	}
}

// If the friend gets crowned early by luck, our steadier pairing must take it back
// rather than leaving the filter on the wrong player.
func TestLockMovesToUsAfterAnEarlyWrongDecision(t *testing.T) {
	const me, friend = 4242, 9999
	h := newCasterHarness()

	for i := 0; i < ownVotesNeeded; i++ { // unlucky start: they win every race
		h.sendRequest()
		h.castInOurWindow(friend)
		h.castInOurWindow(me)
	}
	if h.mine() != friend {
		t.Fatalf("setup expected the friend crowned first, got %d", h.mine())
	}

	for i := 0; i < 10; i++ { // then normal play resumes
		h.sendRequest()
		h.castInOurWindow(me)
		h.castInOurWindow(friend)
	}
	if got := h.mine(); got != me {
		t.Fatalf("mine = %d, want the lock to move to %d", got, me)
	}
}

// An ACT that arrives far from any request of ours is not evidence.
func TestOwnCastPairingRejectsOutOfWindowDelays(t *testing.T) {
	h := newCasterHarness()
	for i := 0; i < ownVotesNeeded*2; i++ {
		h.e.noteOwnRequest(ownReqSize, time.Now().UnixNano()-int64(ownReqMaxDelay+time.Second))
		h.e.recordActCaster(9)
	}
	if got := h.mine(); got != 0 {
		t.Fatalf("mine = %d, want 0 for ACTs outside the pairing window", got)
	}
}

// Outbound traffic that isn't request-shaped must not start a pairing window.
func TestOwnCastPairingIgnoresNonRequestPackets(t *testing.T) {
	h := newCasterHarness()
	for i := 0; i < ownVotesNeeded*2; i++ {
		h.e.noteOwnRequest(ownReqSize+7, time.Now().UnixNano()-int64(80*time.Millisecond))
		h.e.recordActCaster(9)
	}
	if got := h.mine(); got != 0 {
		t.Fatalf("mine = %d, want 0 — no skill request preceded those ACTs", got)
	}
}

// Re-entering an instance re-assigns entity keys. The previous run's key must drop
// out of the picker, otherwise the UI's sticky selection keeps pointing at it.
func TestActCasterExpiresAfterTTL(t *testing.T) {
	h := newCasterHarness()
	h.otherCast(1001)
	h.age(1001, actCasterTTL+time.Second)

	h.otherCast(2002) // first cast of the new run prunes the stale key
	if ids := h.ids(); len(ids) != 1 || ids[0] != 2002 {
		t.Fatalf("picker lists %v, want only the new key 2002", ids)
	}
}

// A key that stopped casting shouldn't leave behind a vote lead the next
// instance's key has to overcome — it should simply stand down.
func TestOwnCasterStandsDownWhenIdle(t *testing.T) {
	h := newCasterHarness()
	for i := 0; i < ownVotesNeeded*4; i++ {
		h.ownCast(1001) // build a big lead in the old instance
	}
	if h.mine() != 1001 {
		t.Fatalf("mine = %d, want 1001", h.mine())
	}

	h.age(1001, ownCasterIdle+time.Second)
	for i := 0; i < ownVotesNeeded; i++ {
		h.ownCast(2002)
	}
	if got := h.mine(); got != 2002 {
		t.Fatalf("mine = %d, want the new instance's key 2002 after %d pairings", got, ownVotesNeeded)
	}
}

// Starting a new capture session must leave no trace of the previous one's keys.
func TestResetCasterStateClearsEverything(t *testing.T) {
	h := newCasterHarness()
	for i := 0; i < ownVotesNeeded; i++ {
		h.ownCast(42)
	}
	if h.e.ownCaster != 42 {
		t.Fatalf("ownCaster = %d, want 42 before reset", h.e.ownCaster)
	}

	h.e.resetCasterState()
	if h.e.ownCaster != 0 || len(h.e.actLog) != 0 || len(h.e.actSeen) != 0 || len(h.e.ownVotes) != 0 || h.e.lastOwnReqAt != 0 {
		t.Fatalf("reset left caster state behind: ownCaster=%d actLog=%v actSeen=%v ownVotes=%v lastReq=%d",
			h.e.ownCaster, h.e.actLog, h.e.actSeen, h.e.ownVotes, h.e.lastOwnReqAt)
	}
}
