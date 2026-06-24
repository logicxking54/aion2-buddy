//go:build windows

package capture

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
)

func TestZZDeep(t *testing.T) {
	baseIDs := []uint32{
		15060000, 15060010, 15060020, 15060030, 15060040, 15060050,
		15060120, 15060130, 15060140, 15060150, 15060230, 15060240, 15060250,
		15060340, 15060350, 15060450, 15061230, 15061240, 15061250,
		15061340, 15061350, 15061450, 15062340, 15062350, 15062450, 15063450,
	}
	ids := map[uint32]struct{}{}
	fb := map[byte]struct{}{}
	tier := map[uint32]int{}
	for _, b := range baseIDs {
		for d := uint32(0); d <= 3; d++ {
			ids[b+d] = struct{}{}
			fb[byte((b+d)&0xFF)] = struct{}{}
			tier[b+d] = int(d)
		}
	}

	f, err := os.Open(`D:\Users\Singh\AppData\Roaming\aion2-buddy\4people-session2(2 times).jsonl`)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	relHist := map[int]int{}  // speed rel-offset -> count
	valBucket := map[int]int{} // value/1000 -> count
	pathCount := map[string]int{}
	var anomalies []string

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var rec struct {
			Dir string `json:"dir"`
			Hex string `json:"hex"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Dir != "in" {
			continue
		}
		payload, err := hex.DecodeString(rec.Hex)
		if err != nil {
			continue
		}
		for _, h := range findAllSkillIDs(payload, ids, fb, scanStartDefault) {
			if h.offset+6 > len(payload) || !h.prefixOK || payload[h.offset+5] != 0x02 || tier[h.id] != 0 {
				continue
			}
			fOff, _, fVal, _, fOK := findSpeedFixed(payload, h.offset)
			lOff, _, lVal, _, lOK := findLegacyChargeFloatSpeed(payload, h.offset)
			off, _, val, _, ok := findAttackSpeedOffset(payload, h.offset)
			if !ok {
				continue
			}
			rel := off - h.offset
			relHist[rel]++
			valBucket[int(val)/1000]++
			switch {
			case fOK:
				pathCount["fixed"]++
			case lOK:
				pathCount["fallback"]++
			}
			// flag anything outside the normal cluster
			if rel < 18 || rel > 30 || val < 15000 || val > 30000 {
				if len(anomalies) < 20 {
					anomalies = append(anomalies, fmt.Sprintf("    off=%d rel=%d val=%d fixed=(%t,%d,%d) fb=(%t,%d,%d) plen=%d hex=%s",
						h.offset, rel, val, fOK, fOff-h.offset, fVal, lOK, lOff-h.offset, lVal, len(payload),
						hex.EncodeToString(payload[h.offset:min(h.offset+44, len(payload))])))
				}
			}
		}
	}

	t.Logf("=== base cast speed analysis ===")
	t.Logf("path: %v", pathCount)
	t.Logf("--- speed rel-offset histogram ---")
	var rels []int
	for r := range relHist {
		rels = append(rels, r)
	}
	sort.Ints(rels)
	for _, r := range rels {
		t.Logf("  rel=%d : %d", r, relHist[r])
	}
	t.Logf("--- value histogram (×1000) ---")
	var vbs []int
	for v := range valBucket {
		vbs = append(vbs, v)
	}
	sort.Ints(vbs)
	for _, v := range vbs {
		t.Logf("  %dk : %d", v, valBucket[v])
	}
	if len(anomalies) > 0 {
		t.Logf("--- anomalies (rel<18|>30 or val<15k|>30k) ---")
		for _, a := range anomalies {
			t.Logf("%s", a)
		}
	} else {
		t.Logf("--- no anomalies: all speeds in normal cluster ---")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
