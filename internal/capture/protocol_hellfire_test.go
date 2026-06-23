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
	// Hellfire Max layout captured in a four-player dungeon. A 0xf1 position
	// layout byte appears between the entity varint and the four floats.
	hellfireMaxFlaggedHex = "9dd9e5005e029100f114d5a3c200380647001c044600d00146cea10101948701151e380100009ad9e500"
	// A variable-prefix Max layout where only the bounded structural fallback can
	// locate the speed. The matching family reference makes the candidate strict.
	hellfireMaxFallbackHex = "9dd9e5005e02ffffffffffffffffffffffffffffffffffcea10101948701151e380100009ad9e500"
	// Compact Max records can omit most position floats and the repeated family
	// id. The speed is followed by a marker and a second plausible speed varint.
	hellfireMaxCompactHex = "51d5e5003a02b9ab067ca508c09e0090a6940101baa4012903260031b9ab0629"
	// Compact charge records use a float speed followed by the older 0x0c
	// trailer and may omit the repeated Hellfire family id.
	hellfireCompactFloatHex = "9ad9e5001602c4933f00f008003f52d046816894468ab3af4516fbfb3f010c1b3800002300f00d0c473801"
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

	off, n, val, isFloat, ok, _ := findAttackSpeedOffset(data, skillOffset, false)
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

	off, n, val, isFloat, ok, _ := findAttackSpeedOffset(data, skillOffset, false)
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

func TestFindAttackSpeed_HellfireMaxFlaggedPosition(t *testing.T) {
	data := mustHex(t, hellfireMaxFlaggedHex)

	off, n, val, isFloat, ok, fallback := findAttackSpeedOffset(data, 0, false)
	if !ok {
		t.Fatal("expected to find speed after the one-byte position layout flag")
	}
	if fallback {
		t.Fatal("flagged position layout should be handled by the fixed parser")
	}
	if isFloat || off != 25 || n != 3 || val != 20686 {
		t.Fatalf("expected off=25 len=3 val=20686 varint, got off=%d len=%d val=%d float=%t", off, n, val, isFloat)
	}
}

func TestFindAttackSpeed_HellfireMaxVariablePrefix(t *testing.T) {
	data := mustHex(t, hellfireMaxFallbackHex)

	off, n, val, isFloat, ok, fallback := findAttackSpeedOffset(data, 0, false)
	if !ok || !fallback {
		t.Fatal("expected strict fallback to find variable-prefix Hellfire speed")
	}
	if isFloat || n != 3 || val != 20686 {
		t.Fatalf("expected len=3 val=20686 varint, got off=%d len=%d val=%d float=%t", off, n, val, isFloat)
	}
}

func TestFindAttackSpeed_HellfireMaxCompactPosition(t *testing.T) {
	data := mustHex(t, hellfireMaxCompactHex)

	if _, _, _, _, ok, _ := findAttackSpeedOffset(data, 0, false); ok {
		t.Fatal("secondary-speed compact trailers are disabled for connection stability")
	}
	if _, _, _, _, ok, _ := findAttackSpeedOffset(data, 0, true); ok {
		t.Fatal("aggressive parser must not accept secondary-speed compact trailers")
	}
	if _, _, _, _, ok := findAggressiveSpeedCandidate(data, 0); !ok {
		t.Fatal("expected diagnostic aggressive candidate for secondary-speed compact trailer")
	}
}

func TestFindAttackSpeed_HellfireCompactFloatTrailer(t *testing.T) {
	data := mustHex(t, hellfireCompactFloatHex)

	off, n, val, isFloat, ok, fallback := findAttackSpeedOffset(data, 0, false)
	if !ok {
		t.Fatal("expected exact legacy charge trailer to locate compact float speed")
	}
	if fallback {
		t.Fatal("exact legacy charge trailer should be handled by the fixed parser")
	}
	if !isFloat || off != 25 || n != 4 || val != 19686 {
		t.Fatalf("expected off=25 len=4 val=19686 float, got off=%d len=%d val=%d float=%t", off, n, val, isFloat)
	}
}

func TestFindSpeedSearchAcceptsAllFramedLegacyTrailerLengths(t *testing.T) {
	for _, declaredLen := range []byte{0x0b, 0x0c, 0x10, 0x20, 0x40} {
		data := mustHex(t, "9ad9e500010200000000000000000000cea10101")
		data = append(data, declaredLen, 0x18, 0x38)
		recordLen := int(declaredLen) + 1 - 4
		for len(data) < 20+recordLen {
			data = append(data, 0)
		}

		if _, _, _, _, ok := findSpeedSearch(data, 0, false); ok {
			t.Fatalf("trailer len 0x%02x: generalized framed trailers must remain disabled", declaredLen)
		}
	}
}

func TestFindSpeedSearchRejectsFloatForHellfireMax(t *testing.T) {
	data := mustHex(t, "9dd9e500010200000000000000000000")
	var speed [4]byte
	binary.LittleEndian.PutUint32(speed[:], math.Float32bits(2.0168))
	data = append(data, speed[:]...)
	data = append(data, 0x01, 0x0c, 0x18, 0x38, 0, 0, 0, 0, 0, 0)

	if _, _, _, _, ok := findSpeedSearch(data, 0, false); ok {
		t.Fatal("Hellfire Max must not accept a float fallback candidate")
	}
}

func TestFindAttackSpeed_FlameArrowVarint(t *testing.T) {
	data := mustHex(t, flameArrowHex)
	const skillOffset = 21

	off, n, val, isFloat, ok, _ := findAttackSpeedOffset(data, skillOffset, false)
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
