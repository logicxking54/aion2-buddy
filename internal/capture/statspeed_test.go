//go:build windows

package capture

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// pkt452 is a real inbound stat-recalc packet (LZ4-compressed) captured on
// re-equip of a combat-speed item (session 20260705-203518). Its decompressed
// stat table has combat-speed stat 0x011a = 8352.
const pkt452Hex = "8004ffff6d020000d32f2a38864d011143337d6202ff0100f3098075d52abb030000864d010068cb02c80b0f2147001ca6472c003f441b812c00163f4503852c00163f46eb882c000ff2ff80d90185e201010117a77a090000b20100018fef0c0000c80100020a520b0000ca010003b2d20c0000d101000476a1070000e10100059d010b000074020006fb7f0c0000e501000758550900002b020008b3780d0000680100095f090b00009001000a962e050000c501000b023c0a00009201000c19ca0900001a02000d0ae50b00008101000ef9f10900009d01000f9c2c0d0000a7010010ea8203000033020011ab6909000065010012f46f0e00007a00001395fc0c0000cc01001425900e00005f000016d70c0d0000ed010017b8d60d000066010018152c38864d04004005003f05004205004105ad014a36864d1a0700460000000900740000000b00a30000000d00a80000000e008d0000000f00c20000001c00cc1600002c002c1100006800790400008000480900008500620500001a01a02000003301374200003d012f0e00007b01282d00008101f0f6ffff8201d8f0ffffa9014c2e0000aa01c6140000ad0108160000ba014e110000bb013f1d0000be0140100000bf01bc0c0000c001940c0000c101a00a000000000400700e1d56051600000400611656369e810c1700040800b00e0036cdcc7d329f010000"

func decodeStatTable(t *testing.T, payload []byte) []byte {
	t.Helper()
	bs, ol, ok := parseLZ4Frame(payload)
	if !ok {
		t.Fatal("parseLZ4Frame failed")
	}
	clean, _, dok := lz4DecompressTrace(payload[bs:], ol)
	if !dok {
		t.Fatal("lz4DecompressTrace failed")
	}
	return clean
}

func TestFindCombatSpeedStat(t *testing.T) {
	payload, _ := hex.DecodeString(pkt452Hex)
	clean := decodeStatTable(t, payload)
	off := findCombatSpeedStat(clean)
	if off < 0 {
		t.Fatal("stat 0x011a not found")
	}
	if got := binary.LittleEndian.Uint32(clean[off : off+4]); got != 8352 {
		t.Fatalf("stat 0x011a = %d, want 8352", got)
	}
}

func TestEditStatSpeedInPlace(t *testing.T) {
	payload, _ := hex.DecodeString(pkt452Hex)
	raw := append([]byte(nil), payload...) // raw == payload (payloadOffset 0)

	const target uint32 = 20000
	e := &Engine{} // emit==nil, so emitLog/countModify are no-ops
	handled, edited := e.editStatSpeed(raw, raw, 0, target)
	if !handled || !edited {
		t.Fatalf("handled=%v edited=%v, want both true", handled, edited)
	}

	// Re-decompress the edited packet and confirm the stat now reads target,
	// proving the in-place block-literal patch survives decompression.
	clean := decodeStatTable(t, raw)
	off := findCombatSpeedStat(clean)
	if off < 0 {
		t.Fatal("stat 0x011a not found after edit")
	}
	if got := binary.LittleEndian.Uint32(clean[off : off+4]); got != target {
		t.Fatalf("after edit stat 0x011a = %d, want %d", got, target)
	}

	// Length must be unchanged (no TCP desync).
	if len(raw) != len(payload) {
		t.Fatalf("packet length changed %d -> %d", len(payload), len(raw))
	}

	// A second call with the same target is a no-op (old == target).
	if _, ed := e.editStatSpeed(raw, raw, 0, target); ed {
		t.Fatal("second edit should be a no-op")
	}
}
