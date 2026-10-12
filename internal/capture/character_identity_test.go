//go:build windows

package capture

import (
	"encoding/hex"
	"testing"
	"time"
)

func TestConfiguredNameRejectsDifferentIdentity(t *testing.T) {
	e := NewEngine(func(string, any) {})
	e.SetCharacterNames([]string{"Maliwan"})
	e.acceptLocalIdentity(actorBinding{actorID: 10327, name: "Mali茉莉"})
	if got := e.effectiveCaster(); got != 0 {
		t.Fatalf("selected another character: %d / %s", got, e.autoCasterName)
	}
	e.acceptLocalIdentity(actorBinding{actorID: 42, name: "Maliwan"})
	e.acceptLocalIdentity(actorBinding{actorID: 10327, name: "Mali茉莉"})
	if got := e.effectiveCaster(); got != 42 || e.autoCasterName != "Maliwan" || !e.tracker.isMine(42) {
		t.Fatalf("another name replaced Maliwan: %d / %s", got, e.autoCasterName)
	}
}

func TestConfiguredNameDoesNotGuessFromFirstCast(t *testing.T) {
	e := NewEngine(func(string, any) {})
	e.SetCharacterNames([]string{"Maliwan"})
	if got := e.observeConfiguredCaster(10327, time.Now().UnixNano()); got != 0 {
		t.Fatalf("guessed an unnamed caster despite an explicit character name: %d", got)
	}
	e.SetCasterFilter(42)
	if got := e.observeConfiguredCaster(10327, time.Now().UnixNano()); got != 42 {
		t.Fatalf("manual selection stopped working while waiting for a name: %d", got)
	}
}

func TestConfiguredNameSurvivesUnrelatedCasterAfterQuietGap(t *testing.T) {
	e := NewEngine(func(string, any) {})
	e.SetCharacterNames([]string{"Maliwan"})
	e.learnActorBinding(actorBinding{actorID: 42, name: "Maliwan"})
	now := time.Now().UnixNano()
	e.recordActCaster(42, now)
	if got := e.observeConfiguredCaster(10327, now+int64(actCasterGenerationGap+time.Second)); got != 42 {
		t.Fatalf("quiet gap let another caster replace Maliwan: %d", got)
	}
	e.learnActorBinding(actorBinding{actorID: 43, name: "Maliwan"})
	if got := e.effectiveCaster(); got != 43 {
		t.Fatalf("matching name did not relock in the new instance: %d", got)
	}
}

func TestChangingCharacterNameClearsPreviousIdentity(t *testing.T) {
	var last localCasterEvent
	e := NewEngine(func(event string, payload any) {
		if event == "capture:local-caster" {
			last = payload.(localCasterEvent)
		}
	})
	e.SetCharacterNames([]string{"Mali茉莉"})
	e.acceptLocalIdentity(actorBinding{actorID: 10327, name: "Mali茉莉"})
	e.SetCasterFilter(10327)
	e.SetCharacterNames([]string{"Maliwan"})
	if got := e.effectiveCaster(); got != 0 || last.ID != 0 || !last.ManualOverrideCleared {
		t.Fatalf("name change kept the previous selection: %d, event=%+v", got, last)
	}
}

func TestCompressedIdentitySkipsOtherNameBeforeConfiguredCharacter(t *testing.T) {
	other, _ := hex.DecodeString("3336d7505fb1fb1a370a4d616c69e88c89e88e89")
	mine := []byte{0x33, 0x36, 42, 0, 0, 0, 0, 3, 7, 'M', 'a', 'l', 'i', 'w', 'a', 'n'}
	clean := append(other, mine...)
	// One literal-only LZ4 block, with the game's length/marker prefix.
	payload := append([]byte{byte(len(clean) + 8), 0xff, 0xff, byte(len(clean)), 0, 0, 0, 0xf0, byte(len(clean) - 15)}, clean...)
	e := NewEngine(func(string, any) {})
	e.SetCharacterNames([]string{"Maliwan"})
	handled, edited := e.editCompressedCasts(payload, payload, 0, nil, nil, nil, nil, false, false, 0, 0, false, 0)
	if !handled || edited {
		t.Fatalf("identity-only packet: handled=%t edited=%t", handled, edited)
	}
	if got := e.effectiveCaster(); got != 42 || e.autoCasterName != "Maliwan" {
		t.Fatalf("first unrelated name hid Maliwan in the same packet: %d / %s", got, e.autoCasterName)
	}
}

func TestConfiguredIdentityMatchUsesWholeName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want uint64
	}{
		{name: "Maliwan", want: 42},
		{name: "maliwan", want: 42},
		{name: "Mali", want: 0},
		{name: "Maliwan2", want: 0},
		{name: "Mali茉莉", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEngine(func(string, any) {})
			e.SetCharacterNames([]string{" Maliwan "})
			e.acceptLocalIdentity(actorBinding{actorID: 42, name: tc.name})
			if got := e.effectiveCaster(); got != tc.want {
				t.Fatalf("name %q selected %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestConfiguredIdentityScannerHandlesEverySplit(t *testing.T) {
	other, _ := hex.DecodeString("3336d7505fb1fb1a370a4d616c69e88c89e88e89")
	mine := []byte{0x33, 0x36, 42, 0, 0, 0, 0, 3, 7, 'M', 'a', 'l', 'i', 'w', 'a', 'n'}
	data := append(other, mine...)
	for split := 1; split < len(data); split++ {
		tracker := newEntityTracker()
		tracker.updateNames([]string{"Maliwan"})
		var scanner localIdentityScanner
		if found, ok := scanner.feed(data[:split], tracker.acceptsName); ok {
			t.Fatalf("split %d accepted a different or incomplete identity: %+v", split, found)
		}
		found, ok := scanner.feed(data[split:], tracker.acceptsName)
		if !ok || found.actorID != 42 || found.name != "Maliwan" {
			t.Fatalf("split %d: identity=%+v, ok=%t", split, found, ok)
		}
	}
}
