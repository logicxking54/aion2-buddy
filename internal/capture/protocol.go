//go:build windows

package capture

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"unicode"
)

// Wire format (from skill_offset X), per pingmaker's protocol.py:
//   [X-4:X]   prefix        4 bytes   38 XX NN YY
//   [X:X+4]   skill_id      int32
//   [X+4]     tick          uint8
//   [X+5]     packet_type   uint8     0x02=Start(ACT), 0x00=End, 0x03=Effect
//   [X+6:?]   entity_key    varint
//   [?:?]     parts_id      varint    (optional, if high entity_key)
//   [?:?+16]  position      4x float32
//   [?:?]     attack_speed  varint    <- the value we modify

// ── Varint codec ──────────────────────────────────────────────

// parseVarint reads a protobuf-style varint. Returns (value, bytesConsumed).
func parseVarint(data []byte, offset int) (uint64, int) {
	var result uint64
	var shift uint
	consumed := 0
	length := len(data)
	for offset+consumed < length {
		b := data[offset+consumed]
		result |= uint64(b&0x7F) << shift
		consumed++
		if b&0x80 == 0 {
			break
		}
		shift += 7
		if consumed > 10 {
			break
		}
	}
	return result, consumed
}

// encodeVarint encodes a value as a minimal protobuf-style varint.
func encodeVarint(value uint64) []byte {
	var parts []byte
	for value > 0x7F {
		parts = append(parts, byte(value&0x7F)|0x80)
		value >>= 7
	}
	return append(parts, byte(value&0x7F))
}

// encodeVarintFixed encodes a varint padded to exactly targetLen bytes using
// non-canonical continuation bytes, so the decoder still reads the same value
// and the packet length is unchanged.
func encodeVarintFixed(value uint64, targetLen int) []byte {
	minimal := encodeVarint(value)
	if len(minimal) >= targetLen {
		return minimal
	}
	result := make([]byte, len(minimal), targetLen)
	copy(result, minimal)
	for len(result) < targetLen {
		result[len(result)-1] |= 0x80
		result = append(result, 0x00)
	}
	return result
}

// ── Skill packet parsing ──────────────────────────────────────

type skillHit struct {
	id       uint32
	offset   int
	prefixOK bool
}

// validPktType reports whether the byte after a cast's skill_id+tick marks a real
// skill action we handle (0x02 = damage/ACT cast, 0x00 = buff/instant, etc.).
func validPktType(pt byte) bool {
	return pt == 0x00 || pt == 0x02 || pt == 0x03 || pt == 0x04 || pt == 0x06
}

// hasSkillPrefix validates the bytes before a skill ID at offset, accepting both
// cast framings seen in the wild:
//   - legacy: `38 <a> <b!=0> <00|01> <skill_id>` — 0x38 four bytes before the id.
//   - 2026 patch: `38 66 <00|01> <skill_id>` — 0x38 three bytes before the id. The
//     player's own cast-confirmation (the one carrying the 4 position floats +
//     combat-speed field) now uses this shorter header. The byte before the id is
//     0x00 for normal casts and 0x01 for charge-skill casts (e.g. Hellfire).
//     Without this branch the engine skips every cast and combat-speed editing
//     silently stops working.
func hasSkillPrefix(data []byte, offset int) bool {
	if offset < 4 || offset+6 > len(data) {
		return false
	}
	pt := data[offset+5]
	if !validPktType(pt) {
		return false
	}
	// 2026 cast framing: 38 66 <00|01> immediately before the id.
	if data[offset-3] == 0x38 && data[offset-2] == 0x66 && (data[offset-1] == 0x00 || data[offset-1] == 0x01) {
		return true
	}
	// Legacy framing.
	prefix := data[offset-4 : offset]
	if prefix[0] != 0x38 {
		return false
	}
	if prefix[3] != 0x00 && prefix[3] != 0x01 {
		return false
	}
	if prefix[2] == 0x00 {
		return false
	}
	return true
}

// hasEffectApply detects the buff/effect-applied framing for a skill that has no
// ACT cast packet (e.g. Element Enhancement): in a 2a38 effect message the id
// appears as `01 <skill_id> 00 <x z y floats>`. The leading 0x01, trailing 0x00,
// and three valid position floats make the match specific (a damage record has
// the id followed by tick+0x02 instead, so it won't match).
func hasEffectApply(data []byte, offset int) bool {
	if offset < 1 || offset+4+12 > len(data) {
		return false
	}
	if data[offset-1] != 0x01 || data[offset+4] != 0x00 {
		return false
	}
	pos := offset + 5
	allZero := true
	for k := 0; k < 3; k++ {
		bits := binary.LittleEndian.Uint32(data[pos+k*4 : pos+k*4+4])
		if bits != 0 {
			allZero = false
		}
		f := float64(math.Float32frombits(bits))
		if math.IsNaN(f) || math.IsInf(f, 0) || !(f > -600000 && f < 600000) {
			return false
		}
	}
	return !allZero
}

// findAllSkillIDs scans payload for every known skill ID (uint32 LE), using a
// first-byte prefilter. scanStart is 7 for server packets (scanStartDefault)
// and 0 for outbound requests where the ID may sit near the start.
const scanStartDefault = 7

func findAllSkillIDs(data []byte, targetIDs map[uint32]struct{}, firstBytes map[byte]struct{}, scanStart int) []skillHit {
	var results []skillHit
	dlen := len(data)
	end := dlen - 3
	for i := scanStart; i < end; i++ {
		if _, ok := firstBytes[data[i]]; !ok {
			continue
		}
		val := binary.LittleEndian.Uint32(data[i : i+4])
		if _, ok := targetIDs[val]; ok {
			results = append(results, skillHit{id: val, offset: i, prefixOK: hasSkillPrefix(data, i)})
		}
	}
	return results
}

// findAttackSpeedOffset locates a cast's attack_speed field. Returns
// (offset, length, value, isFloat, ok). It first tries the standard fixed layout
// (entity key → 4 position floats → speed); if that fails — some situational cast
// records use a compact position block with fewer floats — it falls back to a
// bounded search for the float speed signature. value is always in raw varint
// units (float form ×10000); isFloat tells the caller which wire form to write.
// The final bool reports whether the FALLBACK scanner found the speed (the fixed
// parser failed): a diagnostic signal — if it's true a lot in dense fights, the
// standard layout is breaking under coalescing.
func findAttackSpeedOffset(data []byte, skillOffset int, _ bool) (int, int, uint64, bool, bool, bool) {
	if off, ln, v, fl, ok := findSpeedFixed(data, skillOffset); ok {
		return off, ln, v, fl, ok, false
	}
	// Only accept fallback matches that still carry a same-family skill reference
	// after the speed. Compact/framed trailer-only matches looked useful in tests
	// but can select the wrong live field and trigger a reconnect.
	if off, ln, v, fl, ok := findSpeedSearch(data, skillOffset, false); ok {
		return off, ln, v, fl, ok, true
	}
	return 0, 0, 0, false, false, false
}

// findSpeedSearch handles compact casts whose position block is not four floats.
// It scans for either speed encoding followed by a marker and validates the
// candidate by requiring a same-family skill reference shortly after the speed.
func findSpeedSearch(data []byte, skillOffset int, _ bool) (int, int, uint64, bool, bool) {
	dlen := len(data)
	skillID := binary.LittleEndian.Uint32(data[skillOffset : skillOffset+4])
	isHellfireMax := skillID == 15063453 || skillID == 15062353
	start := skillOffset + 6
	end := start + 70
	if end > dlen-5 {
		end = dlen - 5
	}
	famBytes := data[skillOffset+1 : skillOffset+4]
	hasFamilyAfter := func(post int) bool {
		upper := post + 48
		if upper > dlen {
			upper = dlen
		}
		return bytes.Contains(data[post:upper], famBytes)
	}
	for pos := start; pos < end; pos++ {
		// Varint form first: a plausible value, then the marker + trailing-record
		// signature. Require v >= 10000 so a float whose low byte parses as a small
		// varint (Hellfire's 16 fb fb 3f reads as 22) falls through to the float
		// branch below instead of matching here.
		if v, ln := parseVarint(data, pos); v >= 10000 && v < 40000 {
			post := pos + ln
			if post < dlen && (data[post] == 0x01 || data[post] == 0x02) && hasFamilyAfter(post) {
				return pos, ln, v, false, true
			}
		}
		// Float form (charge skills store value/10000 in a 4-byte float32).
		if isHellfireMax {
			continue
		}
		f := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[pos : pos+4])))
		v := f * 10000
		if math.IsNaN(f) || math.IsInf(f, 0) || v < 10000 || v >= 40000 {
			continue
		}
		post := pos + 4
		if data[post] != 0x01 && data[post] != 0x02 {
			continue
		}
		if !hasFamilyAfter(post) {
			continue
		}
		return pos, 4, uint64(v + 0.5), true, true
	}
	return 0, 0, 0, false, false
}

// findAggressiveSpeedCandidate detects the old unsafe aggressive fallback
// candidates for diagnostics only. Callers must not edit this offset: these
// trailer-only matches can be false positives in live traffic.
func findAggressiveSpeedCandidate(data []byte, skillOffset int) (int, int, uint64, bool, bool) {
	dlen := len(data)
	skillID := binary.LittleEndian.Uint32(data[skillOffset : skillOffset+4])
	isHellfireMax := skillID == 15063453 || skillID == 15062353
	start := skillOffset + 6
	end := start + 70
	if end > dlen-5 {
		end = dlen - 5
	}
	famBytes := data[skillOffset+1 : skillOffset+4]
	hasFamilyAfter := func(post int) bool {
		upper := post + 48
		if upper > dlen {
			upper = dlen
		}
		return bytes.Contains(data[post:upper], famBytes)
	}
	hasCompactTrailer := func(post int) bool {
		if post+2 >= dlen || (data[post] != 0x01 && data[post] != 0x02) {
			return false
		}
		secondary, secondaryLen := parseVarint(data, post+1)
		return secondary >= 10000 && secondary < 40000 && post+1+secondaryLen < dlen
	}
	hasFramedTrailer := func(post int) bool {
		start := post + 1
		for start < dlen && data[start] == 0 && start < post+5 {
			start++
		}
		if start >= dlen {
			return false
		}
		declared, lenBytes := parseVarint(data, start)
		if lenBytes <= 0 || declared > uint64(dlen) {
			return false
		}
		recordLen := int(declared) + lenBytes - 4
		return recordLen >= lenBytes+2 && start+recordLen <= dlen
	}
	for pos := start; pos < end; pos++ {
		compactSpeedPos := pos-skillOffset >= 16 && pos-skillOffset <= 25
		if v, ln := parseVarint(data, pos); v >= 10000 && v < 40000 {
			post := pos + ln
			if post < dlen && (data[post] == 0x01 || data[post] == 0x02) &&
				!hasFamilyAfter(post) &&
				(hasCompactTrailer(post) || (compactSpeedPos && hasFramedTrailer(post))) {
				return pos, ln, v, false, true
			}
		}
		if isHellfireMax {
			continue
		}
		f := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[pos : pos+4])))
		v := f * 10000
		if math.IsNaN(f) || math.IsInf(f, 0) || v < 10000 || v >= 40000 {
			continue
		}
		post := pos + 4
		if post < dlen && (data[post] == 0x01 || data[post] == 0x02) &&
			!hasFamilyAfter(post) &&
			(hasCompactTrailer(post) || (compactSpeedPos && hasFramedTrailer(post))) {
			return pos, 4, uint64(v + 0.5), true, true
		}
	}
	return 0, 0, 0, false, false
}

// findSpeedFixed parses the standard cast layout: entity key, 4 position floats,
// then the speed (varint, or a 4-byte float ×10000 for charge skills).
func findSpeedFixed(data []byte, skillOffset int) (int, int, uint64, bool, bool) {
	dlen := len(data)
	pos := skillOffset + 6 // skip skill_id(4) + tick(1) + pkt_type(1)
	if pos >= dlen {
		return 0, 0, 0, false, false
	}

	entityKey, keyLen := parseVarint(data, pos)
	pos += keyLen
	if pos >= dlen {
		return 0, 0, 0, false, false
	}

	// optional parts_id for high entity keys
	if entityKey >= 100_000_000 {
		_, partsLen := parseVarint(data, pos)
		pos += partsLen
	}
	if off, ln, speed, isFloat, ok := findSpeedAfterPosition(data, skillOffset, pos); ok {
		return off, ln, speed, isFloat, true
	}
	// Dense combat can add a one-byte position-layout flag before the same four
	// floats. Hellfire Max commonly uses 0xf1 here. Structural validation below
	// still requires a valid speed marker and matching skill-family reference.
	return findSpeedAfterPosition(data, skillOffset, pos+1)
}

func findSpeedAfterPosition(data []byte, skillOffset, pos int) (int, int, uint64, bool, bool) {
	dlen := len(data)

	// 4 coordinate floats (yaw, x, z, y)
	if pos+16 > dlen {
		return 0, 0, 0, false, false
	}
	for k := 0; k < 4; k++ {
		f := math.Float32frombits(binary.LittleEndian.Uint32(data[pos+k*4 : pos+k*4+4]))
		f64 := float64(f)
		if math.IsNaN(f64) || math.IsInf(f64, 0) || !(f64 > -600000 && f64 < 600000) {
			return 0, 0, 0, false, false
		}
	}
	pos += 16

	if pos+1 > dlen {
		return 0, 0, 0, false, false
	}

	// Speed is normally a varint (>=10000). Charge skills (e.g. Hellfire) store it
	// as a 4-byte float32 holding value/10000 — and that float's low byte can parse
	// as a small, valid-looking varint (Hellfire's 16 fb fb 3f reads as varint 22),
	// so "varint too big" alone can't catch it. Try the varint first and accept it
	// only when it's a plausible speed landing on a valid post-marker (0x01/0x02);
	// otherwise fall back to reading the 4 bytes as the float form.
	speed, speedLen := parseVarint(data, pos)
	isFloat := false
	varintGood := speed >= 10000 && speed <= 9999999 &&
		pos+speedLen < dlen && (data[pos+speedLen] == 0x01 || data[pos+speedLen] == 0x02)
	if !varintGood {
		if pos+4 > dlen {
			return 0, 0, 0, false, false
		}
		f := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[pos : pos+4])))
		v := f * 10000
		if math.IsNaN(f) || math.IsInf(f, 0) || v < 10000 || v > 9999999 {
			return 0, 0, 0, false, false
		}
		speed = uint64(v + 0.5)
		speedLen = 4
		isFloat = true
	}

	// byte after the speed field must be 0x01 or 0x02
	post := pos + speedLen
	if post >= dlen || (data[post] != 0x01 && data[post] != 0x02) {
		return 0, 0, 0, false, false
	}

	// structural validation: a skill id from the same family must reappear
	// shortly after the speed. Charge skills (e.g. Hellfire) tag the trailing
	// record with the *base* tier id even when the cast carries a higher tier,
	// so match the upper 3 id bytes (shared across tiers) rather than the exact
	// 4 — the low byte is the tier (0=base, 1/2=charge levels, 3=max).
	famBytes := data[skillOffset+1 : skillOffset+4]
	// Search well past the speed for the family reference: in dense party packets
	// extra sub-records get coalesced between the speed and the trailing skill ref,
	// pushing it farther back than the original 16-byte window — which silently
	// rejected casts whose speed we had already located and validated. The speed is
	// confirmed three other ways (sane floats, plausible value, 0x01/0x02 marker),
	// so a wider window here only loosens a redundant veto.
	upper := post + 48
	if upper > dlen {
		upper = dlen
	}
	if !bytes.Contains(data[post:upper], famBytes) {
		if !(isFloat && hasLegacyChargeSpeedTrailer(data, post)) {
			return 0, 0, 0, false, false
		}
	}

	return pos, speedLen, speed, isFloat, true
}

// ── IP/TCP header parsing ─────────────────────────────────────

// extractEntityKey reads the entity key from the prefix bytes before a skill ID
// (3 bytes back, then 2). Returns 0 if none found.
func hasLegacyChargeSpeedTrailer(data []byte, post int) bool {
	if post+8 > len(data) || data[post] != 0x01 {
		return false
	}
	return bytes.Equal(data[post+1:post+8], []byte{0x0c, 0x1b, 0x38, 0x00, 0x00, 0x23, 0x00})
}

func extractEntityKey(data []byte, skillOffset int) uint64 {
	for _, back := range [2]int{3, 2} {
		if skillOffset >= back {
			ek, ekLen := parseVarint(data, skillOffset-back)
			if ekLen > 0 && ek > 0 {
				return ek
			}
		}
	}
	return 0
}

// findTransformNextPos locates the trailing "next form" record
// `0f 18 38 <skillIdBytes> 01 <nextId>` for the cast at skillOffset and returns
// the byte position of its 4-byte little-endian nextId field (so it can be read
// OR rewritten), the current nextId, and whether the record was found. The
// position is relative to data (the payload).
func findTransformNextPos(data []byte, skillOffset int) (pos int, nextId uint32, ok bool) {
	if skillOffset < 0 || skillOffset+4 > len(data) {
		return 0, 0, false
	}
	pat := make([]byte, 0, 8)
	pat = append(pat, 0x0f, 0x18, 0x38)
	pat = append(pat, data[skillOffset:skillOffset+4]...)
	pat = append(pat, 0x01)
	idx := bytes.Index(data, pat)
	if idx < 0 {
		return 0, 0, false
	}
	p := idx + len(pat)
	if p+4 > len(data) {
		return 0, 0, false
	}
	return p, binary.LittleEndian.Uint32(data[p : p+4]), true
}

// findTransformNext returns the "next form" skill ID for a transform-cycle
// cast. Returns 0 when the cast isn't a transform skill (no such record).
func findTransformNext(data []byte, skillOffset int) uint32 {
	_, id, ok := findTransformNextPos(data, skillOffset)
	if !ok {
		return 0
	}
	return id
}

// ── Stream reassembly for entity (character) detection ────────

var gameMsgDelimiter = []byte{0x06, 0x00, 0x36}

const maxReassemblerBuffer = 2 * 1024 * 1024

type actorBinding struct {
	actorID uint64
	name    string
}

// streamReassembler splits the captured byte stream on the 06 00 36 message
// delimiter and extracts actor_id→name bindings from each complete message.
type streamReassembler struct {
	buf []byte
}

func (r *streamReassembler) feed(chunk []byte) []actorBinding {
	r.buf = append(r.buf, chunk...)
	if len(r.buf) > maxReassemblerBuffer {
		r.buf = r.buf[:0]
		return nil
	}
	var out []actorBinding
	for {
		idx := bytes.Index(r.buf, gameMsgDelimiter)
		if idx < 0 {
			break
		}
		out = append(out, scanActorNameBindings(r.buf[:idx+3])...)
		// Advance past this message; copy the remainder to release the head.
		r.buf = append([]byte(nil), r.buf[idx+3:]...)
	}
	return out
}

func (r *streamReassembler) reset() { r.buf = nil }

// scanActorNameBindings finds the strict pattern:
//
//	36 [varint actor_id] [4 gap bytes] 07 [name_len] [name]
func scanActorNameBindings(data []byte) []actorBinding {
	var out []actorBinding
	dlen := len(data)
	for i := 0; i < dlen; {
		if data[i] != 0x36 {
			i++
			continue
		}
		actorID, alen := parseVarint(data, i+1)
		if alen <= 0 || actorID < 100 || actorID > 99999 {
			i++
			continue
		}
		tagPos := i + 1 + alen + 4 // exactly 4 gap bytes, then 0x07
		if tagPos >= dlen || data[tagPos] != 0x07 {
			i++
			continue
		}
		lenPos := tagPos + 1
		if lenPos >= dlen {
			i++
			continue
		}
		nlen := int(data[lenPos])
		if nlen < 1 || nlen > 24 {
			i++
			continue
		}
		nstart := lenPos + 1
		nend := nstart + nlen
		if nend > dlen {
			i++
			continue
		}
		name := sanitizeNickname(string(data[nstart:nend]))
		if len([]rune(name)) >= 2 {
			out = append(out, actorBinding{actorID: actorID, name: name})
		}
		i = nend
	}
	return out
}

// sanitizeNickname strips a trailing non-alphanumeric suffix from a raw name.
func sanitizeNickname(raw string) string {
	if i := strings.IndexByte(raw, 0); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimSpace(raw)
	var b strings.Builder
	for _, ch := range raw {
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) {
			b.WriteRune(ch)
		} else {
			break
		}
	}
	return b.String()
}

// parsePayload returns the TCP payload and its offset within the raw packet.
// IPv4 only (loopback IPv6 IPC packets are skipped).
func parsePayload(raw []byte) (payload []byte, payloadOffset int, ok bool) {
	if len(raw) < 40 || raw[0]>>4 != 4 {
		return nil, 0, false
	}
	ipHdrLen := int(raw[0]&0x0F) * 4
	if ipHdrLen+13 >= len(raw) {
		return nil, 0, false
	}
	tcpHdrLen := int(raw[ipHdrLen+12]>>4) * 4
	payloadOffset = ipHdrLen + tcpHdrLen
	if payloadOffset >= len(raw) {
		return nil, 0, false
	}
	return raw[payloadOffset:], payloadOffset, true
}

// tcpPorts reads the TCP source/destination ports from an IPv4 packet.
func tcpPorts(raw []byte) (src, dst int) {
	ipHdrLen := int(raw[0]&0x0F) * 4
	if ipHdrLen+4 > len(raw) {
		return 0, 0
	}
	src = int(raw[ipHdrLen])<<8 | int(raw[ipHdrLen+1])
	dst = int(raw[ipHdrLen+2])<<8 | int(raw[ipHdrLen+3])
	return
}

// tcpSequence reads the TCP sequence number from an IPv4 packet.
func tcpSequence(raw []byte) (uint32, bool) {
	if len(raw) < 20 || raw[0]>>4 != 4 {
		return 0, false
	}
	ipHdrLen := int(raw[0]&0x0F) * 4
	if ipHdrLen+8 > len(raw) {
		return 0, false
	}
	return binary.BigEndian.Uint32(raw[ipHdrLen+4 : ipHdrLen+8]), true
}
