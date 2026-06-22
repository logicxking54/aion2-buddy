//go:build windows

package capture

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// Real captured packets (Packet Inspector JSONL export, desktop/hellfire-skill.jsonl).
const (
	// Hellfire (charge skill) — speed encoded as a 4-byte float (1.9046 = 19046/10000).
	hellfireHex = "083b3800000d31380000c5a9010000271c38e848019ad9e5001602c5a9019e3a2742e1205b475da423470012b347efc9f33f010c1b3800009ad9e5000a218de8480001302a38e8480111820591e2f505ffffffffffffffff8075d52abb030000e848010049335a47e0cf22470012b3471d4936e84802cd0085020000d600acf4ffff0000000000000000060036"
	// Flame Arrow (normal skill) — speed encoded as a varint (19046).
	flameArrowHex = "083b3800000d31380000c5a9010000270238e84800d217e800de02c5a90199a72842e1205b475da423470012b347e6940101000f1838d217e80001b258e5000d0138000000d217e8000a218de8480001060036"
	// Hellfire - Max (the released meteor, skill id 15063453) — uses a NORMAL
	// varint speed (19046) at offset 80. Fails to modify only when the base
	// "Hellfire" row doesn't carry the Max tier's id (the tier-expansion fix).
	hellfireMaxHex = "280238a88301005a2ce9000e02a88301239d76421a1558479dc225470012b347e6940102640f0638a883015a2ce9000e00290238a260009dd9e5006f02d38001239d76429e145847bdc125470012b347e6940101aa8801151e380100009ad9e500030000009dd9e500290338a26000d380019dd9e5006fc2f2e7479e145847bdc125470012b34796010100000001000d2c38a2600100e101071d4936a26002cd003d0e0000d6003200000000000000000000000f008da26001010134270000060036"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}

func TestFindAttackSpeed_HellfireFloat(t *testing.T) {
	data := mustHex(t, hellfireHex)
	const skillOffset = 21 // from the inspector dump

	off, n, val, isFloat, ok := findAttackSpeedOffset(data, skillOffset)
	if !ok {
		t.Fatal("expected to find Hellfire's float-encoded speed, got not-found")
	}
	if !isFloat {
		t.Fatalf("expected isFloat=true, got false (off=%d len=%d val=%d)", off, n, val)
	}
	if off != 46 || n != 4 {
		t.Fatalf("expected offset 46 len 4, got off=%d len=%d", off, n)
	}
	if val != 19046 {
		t.Fatalf("expected normalized speed 19046, got %d", val)
	}

	// Simulate the engine's float write-back: +25000 (250%) → 44046 raw → 4.4046f.
	target := val + 25000
	binary.LittleEndian.PutUint32(data[off:off+4], math.Float32bits(float32(float64(target)/10000.0)))
	got := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4])))
	if r := got * 10000; r < 44045 || r > 44047 {
		t.Fatalf("float write-back round-trip wrong: got %.4f (raw %.1f), want ~4.4046 (44046)", got, r)
	}
}

func TestFindAttackSpeed_HellfireMaxMeteor(t *testing.T) {
	data := mustHex(t, hellfireMaxHex)
	const skillOffset = 55 // the Hellfire - Max hit, from the inspector dump

	off, n, val, isFloat, ok := findAttackSpeedOffset(data, skillOffset)
	if !ok {
		t.Fatal("expected to find the meteor's varint speed at offset 80")
	}
	if isFloat {
		t.Fatal("the meteor uses a normal varint, not a float")
	}
	if off != 80 || n != 3 || val != 19046 {
		t.Fatalf("expected off=80 len=3 val=19046, got off=%d len=%d val=%d", off, n, val)
	}
}

func TestFindAttackSpeed_FlameArrowVarint(t *testing.T) {
	data := mustHex(t, flameArrowHex)
	const skillOffset = 21

	off, n, val, isFloat, ok := findAttackSpeedOffset(data, skillOffset)
	if !ok {
		t.Fatal("expected to find Flame Arrow's varint speed")
	}
	if isFloat {
		t.Fatal("expected isFloat=false for a normal varint skill")
	}
	if off != 46 || n != 3 || val != 19046 {
		t.Fatalf("expected off=46 len=3 val=19046, got off=%d len=%d val=%d", off, n, val)
	}
}
