//go:build windows

package capture

import (
	"testing"
	"time"
)

// A lock carried over from a previous instance (its entity id never casts
// again) must not arm the mask: entity ids are re-assigned on instance entry,
// so an armed stale lock would rewrite the player's own casts to dodges.
func TestAnimMaskArmsOnlyWhileLockedCasterIsCasting(t *testing.T) {
	e := NewEngine(func(string, any) {})
	e.SetCharacterNames([]string{"Maliwan"})
	e.acceptLocalIdentity(actorBinding{actorID: 42, name: "Maliwan"})
	now := time.Now().UnixNano()
	lock := e.effectiveCaster()
	if lock != 42 {
		t.Fatalf("lock = %d, want 42", lock)
	}
	if e.animMaskTarget(true, lock, 99, false, now) {
		t.Fatal("mask armed before the locked caster ever cast")
	}
	e.recordActCaster(42, now)
	if !e.animMaskTarget(true, lock, 99, false, now+int64(time.Second)) {
		t.Fatal("mask not armed right after the locked caster cast")
	}
	if e.animMaskTarget(true, lock, 42, false, now+int64(time.Second)) {
		t.Fatal("own cast was targeted")
	}
	later := now + int64(animMaskArmWindow) + int64(time.Second)
	if e.animMaskTarget(true, lock, 99, false, later) {
		t.Fatal("mask still armed after the locked caster went silent")
	}
	if e.animMaskTarget(true, 0, 99, false, now) {
		t.Fatal("mask armed with no lock")
	}
	if e.animMaskTarget(false, lock, 99, false, now+int64(time.Second)) {
		t.Fatal("mask targeted while switched off")
	}
}

// Whatever the lock says, a caster bound to the configured character name is
// the player and is never masked (a stale manual pick must not hide own casts).
func TestAnimMaskNeverTargetsConfiguredCharacter(t *testing.T) {
	e := NewEngine(func(string, any) {})
	e.SetCharacterNames([]string{"Maliwan"})
	e.learnActorBinding(actorBinding{actorID: 42, name: "Maliwan"})
	e.SetCasterFilter(7) // stale manual pick
	now := time.Now().UnixNano()
	e.recordActCaster(7, now)
	if !e.tracker.isMine(42) {
		t.Fatal("tracker did not bind Maliwan")
	}
	if e.animMaskTarget(true, 7, 42, true, now) {
		t.Fatal("configured character's cast was targeted")
	}
	if !e.animMaskTarget(true, 7, 99, true, now) {
		t.Fatal("another player's cast was not targeted")
	}
}

// The UI is told when the mask arms or pauses, once per change.
func TestAnimMaskReportsArmedState(t *testing.T) {
	var events []animMaskEvent
	e := NewEngine(func(event string, payload any) {
		if event == "capture:anim-mask" {
			events = append(events, payload.(animMaskEvent))
		}
	})
	now := time.Now().UnixNano()
	// "No identity" is the state both sides start in, so it is not announced.
	e.animMaskTarget(true, 0, 99, false, now)
	e.animMaskTarget(true, 0, 99, false, now)
	e.recordActCaster(42, now)
	e.animMaskTarget(true, 42, 99, false, now)
	e.animMaskTarget(true, 42, 99, false, now+int64(time.Second)) // unchanged: no event
	e.animMaskTarget(true, 42, 99, false, now+2*int64(animMaskArmWindow))
	want := []animMaskEvent{
		{Armed: true, LockID: 42, Reason: "armed"},
		{LockID: 42, Reason: "idle"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, events[i], want[i])
		}
	}
}
