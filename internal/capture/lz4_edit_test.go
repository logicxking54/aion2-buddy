//go:build windows

package capture

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"github.com/pierrec/lz4/v4"
)

// A real party-load LZ4 packet (server→client) carrying a Hellfire base cast
// (id 15063450, speed 20686). Captured in pingmaker-session-20260625-042030.
const partyHellfireHex = "9804ffff75020000f111220438fcd1060400f41087d4e1006902c804375801000000aaa501c96e0100251f00f0010600c21135a5e50082031800bf88b459210091cea10199970601002c2200f2003400f410ced8da0063026d74795502410062b0350104ab050200310200262900f3020600c0b2062c2de9000c0310003ba5155b4c0070e0d401010028028d00f42b00fe8b1b002702c211f7a85841b3c202c62e9d29c7004ea846904e01f0ab0127023881b70a00348c1b001b0281b70abad0dd42cf5d03c67eb925260030000f062400012300f21000083b380000271c38c211019ad9e5001602fcd106cd822fc397ae0cc600cf5a00a0f2630440010c1b3800002300f00a1a0238b96600d862d6009500b96600000000f2a60101002903170030fcd106e9005894b5bcb044430020c801f600740200270238939e9f0021939e9f007889181bc6098e269f0023939e9f0071280238c0b206001301d302c0b2068f8a41437db20cc63a9b0070cea10102640f06250001240001610023fc0900013093fc090001a1baba1ec671662ac7005626010000013293fc09610063270238c7b104300030c7b104300078b4d6ebc5569d2b910032c7b104300020302a2b018311a80491e2f505ff0100f3028075d52abb030000c2110100b5c202c6269301f0031d4a36c21102cd0025120000d60010f5ffff2f01000200c014008dfcd10602010097048de401c0000e00360d1c82fb9e010000"

func TestLZ4_FrameParse(t *testing.T) {
	payload, _ := hex.DecodeString(partyHellfireHex)
	blockStart, origLen, ok := parseLZ4Frame(payload)
	if !ok {
		t.Fatal("frame not recognized")
	}
	if origLen != 629 {
		t.Fatalf("origLen=%d want 629", origLen)
	}
	if blockStart != 8 {
		t.Fatalf("blockStart=%d want 8", blockStart)
	}
}

func TestLZ4_TraceDecompressMatchesPierrec(t *testing.T) {
	payload, _ := hex.DecodeString(partyHellfireHex)
	blockStart, origLen, _ := parseLZ4Frame(payload)
	clean, litSrc, ok := lz4DecompressTrace(payload[blockStart:], origLen)
	if !ok {
		t.Fatal("trace decompress failed")
	}
	ref := make([]byte, origLen)
	n, err := lz4.UncompressBlock(payload[blockStart:], ref)
	if err != nil || n != origLen {
		t.Fatalf("pierrec decode: n=%d err=%v", n, err)
	}
	if string(clean) != string(ref[:n]) {
		t.Fatal("trace output != pierrec output")
	}
	if len(litSrc) != origLen {
		t.Fatalf("litSrc len=%d want %d", len(litSrc), origLen)
	}
}

// End-to-end: edit the Hellfire speed via the provenance map in the COMPRESSED
// block, then decompress with the reference (client-equivalent) decoder and
// confirm the client would see the boosted value — with the block length and
// the rest of the stream unchanged.
func TestLZ4_InPlaceSpeedEditVisibleToClient(t *testing.T) {
	payload, _ := hex.DecodeString(partyHellfireHex)
	blockStart, origLen, _ := parseLZ4Frame(payload)
	block := payload[blockStart:]
	origBlockLen := len(block)

	clean, litSrc, _ := lz4DecompressTrace(block, origLen)

	// Locate the Hellfire cast (id 15063450 = 9a d9 e5 00) in the clean stream.
	id := []byte{0x9a, 0xd9, 0xe5, 0x00}
	skillOff := -1
	for i := 0; i+6 <= len(clean); i++ {
		if clean[i] == id[0] && clean[i+1] == id[1] && clean[i+2] == id[2] && clean[i+3] == id[3] &&
			clean[i+5] == 0x02 && hasSkillPrefix(clean, i) {
			skillOff = i
			break
		}
	}
	if skillOff < 0 {
		t.Fatal("hellfire cast not found in clean stream")
	}
	spdOff, spdLen, spdVal, isFloat, ok := findAttackSpeedOffset(clean, skillOff)
	if !ok || !isFloat || spdVal != 20686 {
		t.Fatalf("speed parse: off=%d len=%d val=%d float=%t ok=%t (want val 20686 float)", spdOff, spdLen, spdVal, isFloat, ok)
	}

	// All speed bytes must be literals (reachable) for this real capture.
	for k := 0; k < spdLen; k++ {
		if litSrc[spdOff+k] < 0 {
			t.Fatalf("speed byte %d is match-copied (unexpected for this capture)", k)
		}
	}

	// Boost +10000 (=+100%) and write back into the block literals.
	target := spdVal + 10000
	var nb [4]byte
	binary.LittleEndian.PutUint32(nb[:], math.Float32bits(float32(float64(target)/10000.0)))
	for k := 0; k < spdLen; k++ {
		block[blockStart-blockStart+litSrc[spdOff+k]] = nb[k] // edit in place
	}
	if len(block) != origBlockLen {
		t.Fatal("block length changed — must stay identical")
	}

	// Client decompresses the edited block.
	out := make([]byte, origLen)
	n, err := lz4.UncompressBlock(block, out)
	if err != nil || n != origLen {
		t.Fatalf("client decode of edited block: n=%d err=%v", n, err)
	}
	got := float64(math.Float32frombits(binary.LittleEndian.Uint32(out[spdOff:spdOff+4]))) * 10000
	if int(got+0.5) != int(target) {
		t.Fatalf("client sees speed %.0f, want %d", got, target)
	}
	// Everything before the speed must be unchanged (only the 4 speed bytes moved).
	for i := 0; i < spdOff; i++ {
		if out[i] != clean[i] {
			t.Fatalf("byte %d changed unexpectedly", i)
		}
	}
}
