//go:build windows

package capture

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// Replays a recorded session (Ping Maker's "record session" JSONL) through the real
// cast parsing, and reports whether any outbound packet size can identify which
// caster is the local player.
//
// This exists because that idea was tried and did not work. Our skill requests are
// encrypted, but the hope was that a request-shaped packet leaving just before an
// inbound ACT would mark that ACT as ours. Run against a session with two Sorcerers
// in the party (2026-07-24, 4m40s, 278 casts across casters 7334 and 10327), no
// size came close: the best coverage of any one caster's casts was ~59%, and the
// 28-byte packet that earlier analysis had flagged as the request covered 10% of
// one caster's casts and 31% of the other's. Chance level was 64%. Correlating
// against our own traffic simply doesn't separate the two players, so the pairing
// was removed rather than shipped as a coin flip that overrides the user's pick.
//
// Kept as a measuring stick: any future idea for auto-detecting the local player
// can be checked here before it reaches the engine. Skipped without a recording:
//
//	PAIRTEST=<path-to.jsonl> go test ./internal/capture/ -run Replay -v
//
// Optionally narrow which skills count (defaults to the Sorcerer id families):
//
//	PAIRSKILLS=15050000,15210000
type sessionPacket struct {
	Time string `json:"time"`
	Dir  string `json:"dir"`
	Len  int    `json:"len"`
	Hex  string `json:"hex"`
}

func loadSession(t *testing.T, path string) []sessionPacket {
	t.Helper()
	f, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	var out []sessionPacket
	for _, line := range strings.Split(string(f), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p sessionPacket
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			continue // tolerate a truncated final record
		}
		out = append(out, p)
	}
	return out
}

func (p sessionPacket) at(t *testing.T) time.Duration {
	t.Helper()
	var h, m int
	var s float64
	if _, err := fmt.Sscanf(p.Time, "%d:%d:%f", &h, &m, &s); err != nil {
		t.Fatalf("bad timestamp %q: %v", p.Time, err)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute +
		time.Duration(s*float64(time.Second))
}

// castsIn returns the caster of every 0x02 ACT of a target skill in this payload,
// mirroring the engine's own inbound path.
func castsIn(payload []byte, ids map[uint32]struct{}, firstBytes map[byte]struct{}) []uint64 {
	var casters []uint64
	for _, hh := range findAllSkillIDs(payload, ids, firstBytes, 0) {
		if hh.offset+6 > len(payload) || !hh.prefixOK || payload[hh.offset+5] != 0x02 {
			continue
		}
		if caster := extractEntityKey(payload, hh.offset); caster != 0 {
			casters = append(casters, caster)
		}
	}
	return casters
}

func TestReplaySessionPairing(t *testing.T) {
	path := os.Getenv("PAIRTEST")
	if path == "" {
		t.Skip("set PAIRTEST=<session.jsonl> to replay a recording")
	}
	packets := loadSession(t, path)
	if len(packets) == 0 {
		t.Fatal("session had no packets")
	}

	ids := map[uint32]struct{}{}
	firstBytes := map[byte]struct{}{}
	addID := func(id uint32) {
		ids[id] = struct{}{}
		firstBytes[byte(id)] = struct{}{}
	}
	if list := os.Getenv("PAIRSKILLS"); list != "" {
		for _, s := range strings.Split(list, ",") {
			var id uint32
			if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &id); err == nil {
				addID(id)
			}
		}
	} else {
		// Every id in the Sorcerer families seen in this recording's catalog range.
		for base := uint32(15000000); base < 15400000; base += 10000 {
			for variant := uint32(0); variant < 1500; variant += 10 {
				addID(base + variant)
			}
		}
	}

	// Per outbound size: how often a cast follows inside the pairing window, and
	// WHICH caster it was. A size that carries our skill requests should be followed
	// overwhelmingly by one caster — us. A size that's just background chatter gets
	// followed by whoever happened to cast, split roughly by how much each casts.
	// That split is the whole test: without it a size can look "correlated" purely
	// because casts are frequent.
	type sizeStat struct {
		sent     int
		followed map[uint64]int
	}
	sizes := map[int]*sizeStat{}
	var pendingSize int
	var pendingAt time.Duration
	havePending := false

	h := newCasterHarness()
	castsSeen := 0
	casterCasts := map[uint64]int{}
	var start time.Duration
	type outEvent struct {
		at   time.Duration
		size int
	}
	type castEvent struct {
		at     time.Duration
		caster uint64
	}
	var outLog []outEvent
	var castLog []castEvent

	for i, p := range packets {
		at := p.at(t)
		if i == 0 {
			start = at
		}
		raw, err := hex.DecodeString(p.Hex)
		if err != nil {
			continue
		}
		switch p.Dir {
		case "out":
			if _, ok := sizes[p.Len]; !ok {
				sizes[p.Len] = &sizeStat{followed: map[uint64]int{}}
			}
			sizes[p.Len].sent++
			outLog = append(outLog, outEvent{at, p.Len})
			pendingSize, pendingAt, havePending = p.Len, at, true
		case "in":
			casters := castsIn(raw, ids, firstBytes)
			if len(casters) == 0 {
				continue
			}
			if havePending {
				if d := at - pendingAt; d >= ownReqMinDelay && d <= ownReqMaxDelay {
					sizes[pendingSize].followed[casters[0]]++
					havePending = false
				}
			}
			for _, c := range casters {
				castsSeen++
				casterCasts[c]++
				castLog = append(castLog, castEvent{at, c})
				h.e.recordActCaster(c, at.Nanoseconds())
			}
		}
	}

	t.Logf("session %s — %d packets over %s", path, len(packets), packets[len(packets)-1].at(t)-start)
	t.Logf("ACT casts of target skills: %d across %d casters", castsSeen, len(casterCasts))
	if castsSeen == 0 {
		t.Fatal("no casts decoded — the skill id set or the recording doesn't match")
	}

	type row struct {
		id    uint64
		casts int
	}
	rows := make([]row, 0, len(casterCasts))
	for id, n := range casterCasts {
		rows = append(rows, row{id, n})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].casts > rows[j].casts })
	for _, r := range rows {
		t.Logf("  caster %-12d casts=%d", r.id, r.casts)
	}

	// Coverage, the other way round: of each caster's casts, how many had a packet of
	// a given size go out just before? Our own casts are all preceded by whatever
	// carries the request; another player's casts are preceded by it only when our
	// traffic happens to overlap. So the request size is the one with a wide gap
	// between the two casters — and the caster on the high side is us.
	//
	// Measuring per cast (rather than crediting only the last packet before it)
	// matters: frequent sizes are almost always the most recent packet, which makes
	// background chatter look correlated with everything.
	type coverRow struct {
		size int
		sent int
		cov  map[uint64]float64
		gap  float64
	}
	crows := make([]coverRow, 0, len(sizes))
	for size, st := range sizes {
		if st.sent < 10 {
			continue
		}
		hits := map[uint64]int{}
		for _, c := range castLog {
			for _, o := range outLog {
				if o.size != size {
					continue
				}
				if d := c.at - o.at; d >= ownReqMinDelay && d <= ownReqMaxDelay {
					hits[c.caster]++
					break
				}
			}
		}
		cov := map[uint64]float64{}
		hi, lo := 0.0, 1.0
		for id, n := range casterCasts {
			f := float64(hits[id]) / float64(n)
			cov[id] = f
			if f > hi {
				hi = f
			}
			if f < lo {
				lo = f
			}
		}
		crows = append(crows, coverRow{size, st.sent, cov, hi - lo})
	}
	sort.Slice(crows, func(i, j int) bool { return crows[i].gap > crows[j].gap })

	t.Log("share of each caster's casts preceded by an outbound packet of this size:")
	for i, r := range crows {
		if i >= 8 {
			break
		}
		var b strings.Builder
		for _, row := range rows {
			fmt.Fprintf(&b, "  caster %d: %3.0f%%", row.id, 100*r.cov[row.id])
		}
		t.Logf("  len=%-5d sent=%-6d%s   gap=%.0f%%", r.size, r.sent, b.String(), 100*r.gap)
	}

	// A usable signal would cover nearly all of one caster's casts and few of the
	// other's. Report whether this recording contains one; a bare log keeps the
	// tool useful for evaluating future ideas without asserting on session data
	// that legitimately varies.
	if len(crows) > 0 && crows[0].gap >= 0.5 {
		t.Logf("CANDIDATE: len=%d separates the casters by %.0f%% — worth investigating",
			crows[0].size, 100*crows[0].gap)
	} else {
		t.Log("no outbound size separates the casters; outbound-traffic correlation " +
			"cannot identify the local player in this recording")
	}
}
