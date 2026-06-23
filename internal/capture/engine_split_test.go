//go:build windows

package capture

import "testing"

func TestRecoverSplitCastsEditsSpeedInCurrentPayload(t *testing.T) {
	data := mustHex(t, hellfireMaxHex)
	const (
		skillOffset = 55
		speedOffset = 80
		splitAt     = 70
		skillID     = 15063453
	)

	e := NewEngine(func(string, any) {})
	key := flowKey{src: 12345, dst: 54321}
	const currentSeq = 1000
	e.tails[key] = flowTail{
		data:    append([]byte(nil), data[:splitAt]...),
		nextSeq: currentSeq,
	}

	current := append([]byte(nil), data[splitAt:]...)
	lookup := map[uint32]skillCfg{
		skillID: {name: "Split Test", speedPct: 100},
	}
	scanIDs := map[uint32]struct{}{skillID: {}}
	scanFB := map[byte]struct{}{byte(skillID & 0xFF): {}}
	names := map[uint32]string{skillID: "Split Test"}

	if splitAt <= skillOffset+6 || splitAt >= speedOffset {
		t.Fatal("test split must land after the cast header and before the speed")
	}
	if e.recoverSplitCasts(current, current, 0, key, currentSeq+1, scanIDs, scanFB, lookup, names, false, 0, 0, 0) {
		t.Fatal("non-contiguous TCP segment must not be joined to the saved tail")
	}
	caster := extractEntityKey(data, skillOffset)
	if caster == 0 {
		t.Fatal("test packet must expose a caster id")
	}
	if e.recoverSplitCasts(current, current, 0, key, currentSeq, scanIDs, scanFB, lookup, names, false, 0, 0, caster+1) {
		t.Fatal("split recovery must honor the active caster filter")
	}
	if !e.recoverSplitCasts(current, current, 0, key, currentSeq, scanIDs, scanFB, lookup, names, false, 0, 0, 0) {
		t.Fatal("expected split recovery to edit current payload")
	}
	got, _ := parseVarint(current, speedOffset-splitAt)
	if got != 29046 {
		t.Fatalf("expected split speed 19046 + 10000 = 29046, got %d", got)
	}
}

func TestRecoverSplitCastsRetainsContiguousMultiPacketTail(t *testing.T) {
	data := mustHex(t, hellfireMaxHex)
	const (
		skillID     = 15063453
		speedOffset = 80
		firstEnd    = 64
		secondEnd   = 72
		firstSeq    = 5000
	)

	e := NewEngine(func(string, any) {})
	key := flowKey{src: 12345, dst: 54321}
	first := append([]byte(nil), data[:firstEnd]...)
	second := append([]byte(nil), data[firstEnd:secondEnd]...)
	third := append([]byte(nil), data[secondEnd:]...)

	e.updateSplitTail(key, first, firstSeq)
	e.updateSplitTail(key, second, firstSeq+uint32(len(first)))

	lookup := map[uint32]skillCfg{
		skillID: {name: "Split Test", speedPct: 100},
	}
	scanIDs := map[uint32]struct{}{skillID: {}}
	scanFB := map[byte]struct{}{byte(skillID & 0xFF): {}}
	names := map[uint32]string{skillID: "Split Test"}
	thirdSeq := firstSeq + uint32(len(first)+len(second))

	if !e.recoverSplitCasts(third, third, 0, key, thirdSeq, scanIDs, scanFB, lookup, names, false, 0, 0, 0) {
		t.Fatal("expected recovery after two preceding contiguous packets")
	}
	got, _ := parseVarint(third, speedOffset-secondEnd)
	if got != 29046 {
		t.Fatalf("expected split speed 19046 + 10000 = 29046, got %d", got)
	}
}

func TestSkipSpeedEditForStabilityAllowsAllHellfireTiers(t *testing.T) {
	cases := []struct {
		name string
		id   uint32
		want bool
	}{
		{"Hellfire", 15063450, false},
		{"Hellfire - Level 1", 15063451, false},
		{"Hellfire - Level 2", 15063452, false},
		{"Hellfire - Max", 15063453, false},
		{"Flame Arrow", 15070453, false},
	}
	for _, tc := range cases {
		if got := skipSpeedEditForStability(tc.name, tc.id); got != tc.want {
			t.Fatalf("%s/%d: got %t, want %t", tc.name, tc.id, got, tc.want)
		}
	}
}
