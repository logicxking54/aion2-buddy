//go:build windows

package capture

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// benchHellfireConfig builds a minimal configured-skill set (Hellfire, all tiers)
// so the edit path actually runs inside the benchmarked functions.
func benchHellfireConfig() (scanIDs map[uint32]struct{}, scanFB map[byte]struct{}, lookup map[uint32]skillCfg, idToName map[uint32]string) {
	ids := []uint32{15063450, 15063451, 15063452, 15063453}
	scanIDs = map[uint32]struct{}{}
	scanFB = map[byte]struct{}{}
	lookup = map[uint32]skillCfg{}
	idToName = map[uint32]string{}
	for _, id := range ids {
		scanIDs[id] = struct{}{}
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], id)
		scanFB[b[0]] = struct{}{}
		lookup[id] = skillCfg{name: "Hellfire", speedPct: 1000}
		idToName[id] = "Hellfire"
	}
	return
}

// BenchmarkLZ4DecompressTrace measures the new per-packet cost of decompressing a
// party LZ4 packet WITH provenance tracking (the litSrc allocation). -benchmem
// shows the bytes/allocs we churn per compressed packet.
func BenchmarkLZ4DecompressTrace(b *testing.B) {
	payload, _ := hex.DecodeString(partyHellfireHex)
	bs, ol, ok := parseLZ4Frame(payload)
	if !ok {
		b.Fatal("not an LZ4 frame")
	}
	block := payload[bs:]
	b.ReportAllocs()
	b.SetBytes(int64(ol))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, ok := lz4DecompressTrace(block, ol); !ok {
			b.Fatal("decompress failed")
		}
	}
}

// BenchmarkEditCompressedCasts measures the FULL inbound-LZ4 work for one packet:
// decompress + trace + scan for skill ids + locate speed + edit the literals.
// This is the dominant per-packet cost our interception adds for party packets.
func BenchmarkEditCompressedCasts(b *testing.B) {
	payload, _ := hex.DecodeString(partyHellfireHex)
	scanIDs, scanFB, lookup, idToName := benchHellfireConfig()
	e := NewEngine(func(string, any) {})
	raw := make([]byte, len(payload))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(raw, payload)
		e.editCompressedCasts(raw, raw, 0, scanIDs, scanFB, lookup, idToName, 0, false, false, 0, 0, false, 0)
	}
}

// BenchmarkFindAllSkillIDs measures the plaintext-path core: scanning a payload
// for configured skill ids (what every non-compressed packet costs).
func BenchmarkFindAllSkillIDs(b *testing.B) {
	payload, _ := hex.DecodeString(partyHellfireHex)
	bs, ol, _ := parseLZ4Frame(payload)
	clean, _, _ := lz4DecompressTrace(payload[bs:], ol)
	scanIDs, scanFB, _, _ := benchHellfireConfig()
	b.ReportAllocs()
	b.SetBytes(int64(len(clean)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = findAllSkillIDs(clean, scanIDs, scanFB, scanStartDefault)
	}
}

// loadInboundPayloads reads a pingmaker session JSONL and returns every inbound
// packet's payload bytes (dir == "in").
func loadInboundPayloads(path string) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		var rec struct{ Dir, Hex string }
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Dir != "in" || rec.Hex == "" {
			continue
		}
		if p, err := hex.DecodeString(rec.Hex); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// BenchmarkInboundSession replays EVERY inbound packet of a real recorded session
// through the same work processPacket does per packet (LZ4 → editCompressedCasts,
// else → findAllSkillIDs), and reports the average ns added per packet across the
// real packet-size + compression distribution. Run with:
//
//	SESSION=...\pingmaker-session-XXXX.jsonl go test ./internal/capture/ \
//	  -run x -bench BenchmarkInboundSession -benchmem
func BenchmarkInboundSession(b *testing.B) {
	path := os.Getenv("SESSION")
	if path == "" {
		b.Skip("set SESSION=<pingmaker-session.jsonl> to run")
	}
	payloads := loadInboundPayloads(path)
	if len(payloads) == 0 {
		b.Skip("no inbound payloads in session")
	}
	scanIDs, scanFB, lookup, idToName := benchHellfireConfig()
	e := NewEngine(func(string, any) {})

	var lz4, plain int
	maxLen := 0
	for _, p := range payloads {
		if _, _, ok := parseLZ4Frame(p); ok {
			lz4++
		} else {
			plain++
		}
		if len(p) > maxLen {
			maxLen = len(p)
		}
	}
	raw := make([]byte, maxLen)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range payloads {
			buf := raw[:len(p)]
			copy(buf, p)
			if handled, _ := e.editCompressedCasts(buf, buf, 0, scanIDs, scanFB, lookup, idToName, 0, false, false, 0, 0, false, 0); !handled {
				_ = findAllSkillIDs(buf, scanIDs, scanFB, scanStartDefault)
			}
		}
	}
	b.StopTimer()

	perPkt := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / float64(len(payloads))
	b.ReportMetric(perPkt, "ns/packet")
	b.ReportMetric(float64(lz4)/float64(len(payloads))*100, "%lz4")
	b.Logf("inbound packets=%d (lz4=%d plain=%d) maxLen=%d", len(payloads), lz4, plain, maxLen)
}
