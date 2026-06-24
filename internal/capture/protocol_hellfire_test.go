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
	// Base Hellfire (charge skill) — speed encoded as a 4-byte float (1.9046 = 19046/10000).
	hellfireHex = "083b3800000d31380000c5a9010000271c38e848019ad9e5001602c5a9019e3a2742e1205b475da423470012b347efc9f33f010c1b3800009ad9e5000a218de8480001302a38e8480111820591e2f505ffffffffffffffff8075d52abb030000e848010049335a47e0cf22470012b3471d4936e84802cd0085020000d600acf4ffff0000000000000000060036"
	// Flame Arrow (normal skill) — speed encoded as a varint (19046).
	flameArrowHex = "083b3800000d31380000c5a9010000270238e84800d217e800de02c5a90199a72842e1205b475da423470012b347e6940101000f1838d217e80001b258e5000d0138000000d217e8000a218de8480001060036"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}

// Base Hellfire stores speed as a 4-byte float — the parser must decode it and
// flag isFloat so the engine writes it back in the same form.
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
	if off != 46 || n != 4 || val != 19046 {
		t.Fatalf("expected off=46 len=4 val=19046, got off=%d len=%d val=%d", off, n, val)
	}

	// Float write-back round-trip: +25000 (250%) → 44046 raw → ~4.4046f.
	target := val + 25000
	binary.LittleEndian.PutUint32(data[off:off+4], math.Float32bits(float32(float64(target)/10000.0)))
	got := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4])))
	if r := got * 10000; r < 44045 || r > 44047 {
		t.Fatalf("float write-back round-trip wrong: got %.4f (raw %.1f), want ~4.4046 (44046)", got, r)
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

// Real compact charge layouts captured in a 4-player dungeon (buffed Hellfire).
// The trailing record drops the base-id reference (ends `…00 00 23 00`) so the
// fixed parser's exact-id anchor fails; the compact-charge fallback recovers the
// float speed via that exact trailer. Without this, ~46% of party casts missed.
func TestFindAttackSpeed_HellfireCompactPartyLayout(t *testing.T) {
	cases := []struct {
		name    string
		hex     string
		wantOff int
	}{
		// 4 position floats + speed float, but trailer has no base id.
		{"4-float, no base-id trailer", "9ad9e5001602ade908ae8ea8c200b0b34500bd0ac70098ad46be301140010c1b3800002300f31c0f", 25},
		// Compact 3-float position block — speed lands earlier.
		{"compact 3-float position", "9ad9e5001602ade9088b0815c300b88345004100a058ca0a40010c1b38000023005f1f0438a83fa7", 21},
	}
	for _, tc := range cases {
		data := mustHex(t, tc.hex)
		off, n, val, isFloat, ok := findAttackSpeedOffset(data, 0)
		if !ok {
			t.Fatalf("%s: expected to find compact charge speed", tc.name)
		}
		if !isFloat || n != 4 || off != tc.wantOff || val < 10000 || val >= 40000 {
			t.Fatalf("%s: got off=%d len=%d val=%d float=%t, want off=%d len=4 float in [10000,40000)", tc.name, off, n, val, isFloat, tc.wantOff)
		}
	}
}
