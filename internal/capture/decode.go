//go:build windows

package capture

// decode.go ports the message-level decoding from the reference Aion 2 DPS meter
// (C:\projects\Aion2-Dps-Meter, StreamProcessor.kt) into aion2-buddy. It is a
// READ-ONLY analysis layer: it frames a server→client payload into individual
// length-prefixed messages (handling the extra-flag byte and FF FF LZ4 bundles),
// dispatches by 2-byte opcode, and decodes the combat-relevant ones (damage,
// DoT, buff, boss HP, battle toggle) into human-readable log lines. It never
// modifies packets, so it can't affect the combat-speed edit path.
//
// Framing (per the reference): each message is varint-length-prefixed; the real
// byte length = lengthVarint.value + lengthVarint.byteLen - 4. A byte in
// [0xF0,0xFE] right after the length is an "extra flag" (one extra byte before
// the opcode). If the bytes after the length are FF FF, the body is LZ4-block
// compressed: uint32 LE original-length, then the compressed block, expanding to
// more inner messages.

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// opcodeNames is the corrected/expanded 2-byte opcode map (key = b0 | b1<<8),
// from the reference decoder. Replaces aion2-buddy's older, partly-mislabelled
// table (41 36 is Summon not "death"; 44 36 is a nickname not "spawn").
var opcodeNames = map[uint16]string{
	0x3633: "own-nickname",   // 33 36
	0x3644: "other-nickname", // 44 36
	0x3641: "summon",         // 41 36
	0x3804: "damage",         // 04 38
	0x3805: "dot",            // 05 38
	0x382A: "buff",           // 2A 38
	0x382B: "buff2",          // 2B 38
	0x8D21: "battle-toggle",  // 21 8D
	0x8D00: "boss-hp",        // 00 8D
	0x9707: "join-request",   // 07 97
	0x9725: "join-cancel",    // 25 97
	0x970B: "join-admit",     // 0B 97
	0x9709: "join-refuse",    // 09 97
	0x9718: "instance-start", // 18 97
	0x971D: "exit-party",     // 1D 97
}

func opcodeName(b0, b1 byte) string {
	if n, ok := opcodeNames[uint16(b0)|uint16(b1)<<8]; ok {
		return n
	}
	return fmt.Sprintf("op%02x%02x", b0, b1)
}

// ── little-endian readers (bounds-checked) ────────────────────

func u16le(d []byte, o int) (uint16, bool) {
	if o+2 > len(d) {
		return 0, false
	}
	return binary.LittleEndian.Uint16(d[o : o+2]), true
}
func u32le(d []byte, o int) (uint32, bool) {
	if o+4 > len(d) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(d[o : o+4]), true
}
func u64le(d []byte, o int) (uint64, bool) {
	if o+8 > len(d) {
		return 0, false
	}
	return binary.LittleEndian.Uint64(d[o : o+8]), true
}

// lz4DecompressBlock expands an LZ4 *block* (not frame) of known original size.
// Pure Go, no dependency. Returns ok=false on malformed input.
func lz4DecompressBlock(src []byte, dstLen int) ([]byte, bool) {
	if dstLen <= 0 || dstLen > 8*1024*1024 {
		return nil, false
	}
	dst := make([]byte, 0, dstLen)
	s := 0
	for s < len(src) {
		token := int(src[s])
		s++
		litLen := token >> 4
		if litLen == 0xf {
			for s < len(src) {
				b := int(src[s])
				s++
				litLen += b
				if b != 0xff {
					break
				}
			}
		}
		if s+litLen > len(src) {
			return nil, false
		}
		dst = append(dst, src[s:s+litLen]...)
		s += litLen
		if len(dst) >= dstLen {
			break // last sequence: literals only
		}
		offset, ok := u16le(src, s)
		if !ok || offset == 0 {
			return nil, false
		}
		s += 2
		matchLen := token & 0xf
		if matchLen == 0xf {
			for s < len(src) {
				b := int(src[s])
				s++
				matchLen += b
				if b != 0xff {
					break
				}
			}
		}
		matchLen += 4 // minmatch
		start := len(dst) - int(offset)
		if start < 0 {
			return nil, false
		}
		for i := 0; i < matchLen; i++ {
			dst = append(dst, dst[start+i]) // byte-wise: handles overlap
		}
	}
	return dst, true
}

// ── message framing + dispatch ────────────────────────────────

// decodeMessages frames payload into individual messages and returns a
// human-readable decoded line for each combat-relevant one. Best-effort: it
// assumes payload begins at a message boundary (true for most server pushes) and
// stops cleanly on a partial/odd frame rather than guessing. names resolves skill
// ids to labels. Panics from malformed data are contained.
func decodeMessages(payload []byte, names map[uint32]string) (out []string) {
	defer func() { _ = recover() }()
	pos := 0
	guard := 0
	for pos < len(payload) {
		guard++
		if guard > 4096 {
			break
		}
		val, n := parseVarint(payload, pos)
		if n <= 0 {
			break
		}
		if val == 0 { // padding
			pos++
			continue
		}
		realLen := int(val) + n - 4
		if realLen <= 0 || pos+realLen > len(payload) {
			break // malformed or incomplete (message spans TCP segments)
		}
		if line := decodeOneMessage(payload[pos:pos+realLen], names); line != "" {
			out = append(out, line)
		}
		pos += realLen
	}
	return out
}

// decodeOneMessage decodes a single framed message (incl. its length prefix),
// mirroring the reference onPacketReceived: handle extra-flag + LZ4, then opcode.
func decodeOneMessage(msg []byte, names map[uint32]string) string {
	if len(msg) < 4 {
		return ""
	}
	_, lenN := parseVarint(msg, 0)
	if lenN <= 0 || lenN >= len(msg) {
		return ""
	}
	extra := msg[lenN] >= 0xf0 && msg[lenN] < 0xff

	// LZ4 bundle: FF FF after the (optional extra) flag → decompress + recurse.
	ffAt := lenN
	if extra {
		ffAt = lenN + 1
	}
	if ffAt+1 < len(msg) && msg[ffAt] == 0xff && msg[ffAt+1] == 0xff {
		off := lenN + 2
		if extra {
			off++
		}
		origLen, ok := u32le(msg, off)
		if !ok {
			return ""
		}
		off += 4
		if restored, ok := lz4DecompressBlock(msg[off:], int(origLen)); ok {
			inner := decodeMessages(restored, names)
			if len(inner) > 0 {
				return "LZ4{ " + strings.Join(inner, " ; ") + " }"
			}
		}
		return ""
	}

	opOff := lenN
	if extra {
		opOff++
	}
	if opOff+1 >= len(msg) {
		return ""
	}
	b0, b1 := msg[opOff], msg[opOff+1]
	body := opOff + 2 // first byte after opcode

	switch opcodeName(b0, b1) {
	case "damage":
		return decodeDamage(msg, body, names)
	case "dot":
		return decodeDoT(msg, body, names)
	case "buff", "buff2":
		return decodeBuff(msg, body, names)
	case "boss-hp":
		return decodeBossHp(msg, body)
	case "battle-toggle":
		return decodeBattle(msg, body)
	}
	return ""
}

// ── per-opcode decoders (ported from StreamProcessor.kt) ───────

// specialFlags decodes the crit/back/parry/… bitfield (first byte of the flags
// block); the block is only present when its size is ≥10 (size 8 = none).
func specialFlags(block []byte) string {
	if len(block) < 10 {
		return ""
	}
	f := block[0]
	var s []string
	for bit, name := range map[byte]string{0x01: "back", 0x04: "parry", 0x08: "perfect", 0x10: "double", 0x20: "endure", 0x40: "restoration"} {
		if f&bit != 0 {
			s = append(s, name)
		}
	}
	if len(s) == 0 {
		return ""
	}
	return " " + strings.Join(s, "+")
}

// resolveSkill returns the skill id (normalized by /10 if needed) plus its name.
func resolveSkill(code uint32, names map[uint32]string) string {
	if n := names[code]; n != "" {
		return fmt.Sprintf("%d(%s)", code, n)
	}
	base := (code / 10) * 10
	if n := names[base]; n != "" {
		return fmt.Sprintf("%d(%s)", base, n)
	}
	return fmt.Sprintf("%d(?)", code)
}

// hxRange renders bytes [a,b) as space-separated hex.
func hxRange(d []byte, a, b int) string {
	if a < 0 {
		a = 0
	}
	if b > len(d) {
		b = len(d)
	}
	if a >= b {
		return ""
	}
	parts := make([]string, 0, b-a)
	for i := a; i < b; i++ {
		parts = append(parts, fmt.Sprintf("%02x", d[i]))
	}
	return strings.Join(parts, " ")
}

// annot builds an annotated byte map for one framed message: a tag, the header
// (length+flag+opcode), then each decoded field as name@off[hex]=value, ending
// with the raw hex of whatever bytes we didn't decode. Lets you see exactly
// which hex is understood vs not.
type annot struct {
	d    []byte
	b    strings.Builder
	last int
}

func newAnnot(d []byte, tag string, hdrEnd int) *annot {
	a := &annot{d: d, last: hdrEnd}
	a.b.WriteString(tag)
	a.b.WriteString(" hdr@0[" + hxRange(d, 0, hdrEnd) + "]")
	return a
}

// f records a decoded field spanning n bytes at off, with an optional value.
func (a *annot) f(name string, off, n int, val string) {
	end := off + n
	if end > len(a.d) {
		end = len(a.d)
	}
	if val != "" {
		fmt.Fprintf(&a.b, " %s@%d[%s]=%s", name, off, hxRange(a.d, off, end), val)
	} else {
		fmt.Fprintf(&a.b, " %s@%d[%s]", name, off, hxRange(a.d, off, end))
	}
	if end > a.last {
		a.last = end
	}
}

// done appends the undecoded tail (if any) and returns the line.
func (a *annot) done() string {
	if a.last < len(a.d) {
		fmt.Fprintf(&a.b, " || undecoded@%d: %s", a.last, hxRange(a.d, a.last, len(a.d)))
	}
	return a.b.String()
}

// skillAt reads a uint32 LE skill id at off within pl and resolves its name.
func skillAt(pl []byte, off int, names map[uint32]string) string {
	if v, ok := u32le(pl, off); ok {
		return resolveSkill(v, names)
	}
	return "?"
}

// annotateTrailing frames the cast packet's trailing region into its
// length-prefixed sub-records (`<varint len><2-byte opcode><payload>`,
// realLen = len + lenBytes - 4) and labels each. Known sub-records:
//
//	18 38  next-form  : <skill:4> <01=has-next> <nextId:4>
//	19 38             : 01 <skill:4>
//	01 38             : 00 00 00 <skill:4>
//	00 8d             : trailing value (last 4 bytes as uint32 — e.g. a timer)
//	21 8d  battle      ;  00 36  trailer
//
// Unknown opcodes are shown as op<hex> with their raw payload.
func annotateTrailing(d []byte, start int, names map[uint32]string) string {
	var b strings.Builder
	pos := start
	guard := 0
	for pos < len(d) {
		if guard++; guard > 256 {
			break
		}
		val, n := parseVarint(d, pos)
		if n <= 0 {
			break
		}
		if val == 0 { // padding byte between records
			fmt.Fprintf(&b, " pad@%d[00]", pos)
			pos++
			continue
		}
		realLen := int(val) + n - 4
		if realLen <= 0 || pos+realLen > len(d) {
			fmt.Fprintf(&b, " || tail@%d: %s", pos, hxRange(d, pos, len(d)))
			break
		}
		b.WriteString(" " + annotateTrailingRecord(d[pos:pos+realLen], pos, n, names))
		pos += realLen
	}
	return b.String()
}

func annotateTrailingRecord(rec []byte, absOff, lenN int, names map[uint32]string) string {
	if lenN+2 > len(rec) {
		return fmt.Sprintf("rec@%d[%s]", absOff, hxRange(rec, 0, len(rec)))
	}
	op := fmt.Sprintf("%02x%02x", rec[lenN], rec[lenN+1])
	pl := rec[lenN+2:]
	switch op {
	case "1838": // next-form: <skill:4> <flag> <next:4>
		s := fmt.Sprintf("{next-form@%d skill=%s", absOff, skillAt(pl, 0, names))
		if len(pl) >= 9 && pl[4] == 0x01 {
			s += " next=" + skillAt(pl, 5, names)
		}
		return s + "}"
	case "1938": // 01 <skill:4>
		return fmt.Sprintf("{rec19-38@%d skill=%s}", absOff, skillAt(pl, 1, names))
	case "0138": // 00 00 00 <skill:4>
		return fmt.Sprintf("{rec01-38@%d skill=%s}", absOff, skillAt(pl, 3, names))
	case "008d": // trailing value (last 4 bytes), maybe a timer/cooldown
		val := ""
		if v, ok := u32le(pl, len(pl)-4); ok {
			val = fmt.Sprintf(" val=%d", v)
		}
		return fmt.Sprintf("{rec00-8d@%d%s [%s]}", absOff, val, hxRange(pl, 0, len(pl)))
	case "218d":
		return fmt.Sprintf("{battle@%d [%s]}", absOff, hxRange(pl, 0, len(pl)))
	case "0036":
		return fmt.Sprintf("{trailer@%d [%s]}", absOff, hxRange(pl, 0, len(pl)))
	default:
		return fmt.Sprintf("{op%s@%d len%d [%s]}", op, absOff, len(rec), hxRange(pl, 0, len(pl)))
	}
}

func critStr(typ uint64) string {
	if typ == 3 {
		return "3(CRIT)"
	}
	return fmt.Sprintf("%d", typ)
}

// scalarHint renders a 4- or 8-byte little-endian range as candidate numeric
// interpretations (unsigned int + IEEE float). Used to hunt for a buff magnitude
// — e.g. an attack-speed / TimeDilation multiplier — hiding in bytes that DPS
// meters treat as opaque padding.
func scalarHint(d []byte, off, n int) string {
	switch n {
	case 4:
		if v, ok := u32le(d, off); ok {
			return fmt.Sprintf("u32=%d f32=%g", v, math.Float32frombits(v))
		}
	case 8:
		if v, ok := u64le(d, off); ok {
			return fmt.Sprintf("u64=%d f64=%g", v, math.Float64frombits(v))
		}
	}
	return ""
}

func orNone(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return "none"
	}
	return s
}

func decodeDamage(msg []byte, body int, names map[uint32]string) string {
	a := newAnnot(msg, "DMG", body)
	off := body
	target, n := parseVarint(msg, off)
	a.f("target", off, n, fmt.Sprintf("%d", target))
	off += n
	sw, n := parseVarint(msg, off)
	a.f("switch", off, n, fmt.Sprintf("%d", sw))
	off += n
	flag, n := parseVarint(msg, off)
	a.f("flag", off, n, fmt.Sprintf("%d", flag))
	off += n
	actor, n := parseVarint(msg, off)
	a.f("actor", off, n, fmt.Sprintf("%d", actor))
	off += n
	skill, ok := u32le(msg, off)
	if !ok {
		return a.done()
	}
	a.f("skill", off, 5, resolveSkill(skill, names)) // 4-byte id + 1 byte
	off += 5
	typ, n := parseVarint(msg, off)
	a.f("type", off, n, critStr(typ))
	off += n
	// special-flags block size is selected by switch & 0x0f
	var blk int
	switch sw & 0x0f {
	case 4:
		blk = 8
	case 5:
		blk = 12
	case 6:
		blk = 10
	case 7:
		blk = 14
	default:
		return a.done()
	}
	if off+blk > len(msg) {
		return a.done()
	}
	sp := specialFlags(msg[off : off+blk])
	consumed := blk
	if strings.Contains(sp, "restoration") {
		consumed += 2
	}
	a.f("flags", off, consumed, orNone(sp))
	off += consumed
	unk, n := parseVarint(msg, off)
	a.f("unknown", off, n, fmt.Sprintf("%d", unk))
	off += n
	dmg, n := parseVarint(msg, off)
	a.f("dmg", off, n, fmt.Sprintf("%d", dmg))
	off += n
	if hits, end := decodeMultiHit(msg, off); hits > 1 {
		a.f("multihit", off, end-off, fmt.Sprintf("x%d", hits))
	}
	return a.done()
}

func decodeDoT(msg []byte, body int, names map[uint32]string) string {
	a := newAnnot(msg, "DOT", body)
	off := body
	target, n := parseVarint(msg, off)
	a.f("target", off, n, fmt.Sprintf("%d", target))
	off += n
	if off >= len(msg) {
		return a.done()
	}
	a.f("bitflag", off, 1, fmt.Sprintf("0x%02x", msg[off]))
	off++
	actor, n := parseVarint(msg, off)
	a.f("actor", off, n, fmt.Sprintf("%d", actor))
	off += n
	unk, n := parseVarint(msg, off)
	a.f("unknown", off, n, fmt.Sprintf("%d", unk))
	off += n
	skill, ok := u32le(msg, off)
	if !ok {
		return a.done()
	}
	a.f("skill", off, 4, resolveSkill(skill, names))
	off += 4
	dmg, n := parseVarint(msg, off)
	if n > 0 {
		a.f("dmg", off, n, fmt.Sprintf("%d", dmg))
		off += n
	}
	return a.done()
}

func decodeBuff(msg []byte, body int, names map[uint32]string) string {
	a := newAnnot(msg, "BUFF", body)
	off := body
	target, n := parseVarint(msg, off)
	a.f("target", off, n, fmt.Sprintf("%d", target))
	off += n
	// pad + skip are opaque in DPS meters; surface them as numbers — a buff
	// magnitude could ride in either slot.
	if pad, ok := u16le(msg, off); ok {
		a.f("pad", off, 2, fmt.Sprintf("u16=%d", pad))
	} else {
		a.f("pad", off, 2, "")
	}
	off += 2
	sv, sn := parseVarint(msg, off)
	a.f("skip", off, sn, fmt.Sprintf("%d", sv))
	off += sn
	skill, ok := u32le(msg, off)
	if !ok {
		return a.done()
	}
	a.f("skill", off, 4, resolveSkill(skill, names))
	off += 4
	dur, ok := u32le(msg, off)
	if !ok {
		return a.done()
	}
	durStr := fmt.Sprintf("%dms", dur)
	if dur == 0xFFFFFFFF {
		durStr = "permanent"
	}
	a.f("duration", off, 4, durStr)
	off += 4
	// The 4 bytes after duration were folded into padding by DPS meters (which
	// only need code+duration). A per-buff magnitude (attack-speed/TimeDilation
	// multiplier) is the prime suspect here — show u32 + float32.
	a.f("durX", off, 4, scalarHint(msg, off, 4))
	off += 4
	if st, ok := u64le(msg, off); ok {
		a.f("serverTime", off, 8, fmt.Sprintf("%d", st))
		off += 8
		actor, an := parseVarint(msg, off)
		a.f("actor", off, an, fmt.Sprintf("%d", actor))
		off += an
	}
	return a.done()
}

func decodeBossHp(msg []byte, body int) string {
	a := newAnnot(msg, "BOSS-HP", body)
	off := body
	mobID, n := parseVarint(msg, off)
	a.f("mob", off, n, fmt.Sprintf("%d", mobID))
	off += n
	for i := 0; i < 3; i++ { // skip 3 varints
		_, sn := parseVarint(msg, off)
		a.f(fmt.Sprintf("skip%d", i+1), off, sn, "")
		off += sn
	}
	if hp, ok := u32le(msg, off); ok {
		a.f("hp", off, 4, fmt.Sprintf("%d", hp))
		off += 4
	}
	return a.done()
}

func decodeBattle(msg []byte, body int) string {
	a := newAnnot(msg, "BATTLE", body)
	off := body
	battle, n := parseVarint(msg, off)
	a.f("mob", off, n, fmt.Sprintf("%d", battle))
	off += n
	_, sn := parseVarint(msg, off)
	a.f("skip", off, sn, "")
	off += sn
	toggle, tn := parseVarint(msg, off)
	state := map[uint64]string{1: "start", 0: "end"}[toggle]
	if state == "" {
		state = fmt.Sprintf("%d", toggle)
	}
	a.f("toggle", off, tn, state)
	off += tn
	return a.done()
}

// decodeMultiHit returns the hit count and the end offset: a count byte followed
// by `count` equal damage varints (multi-hit skills). Returns (1, off) when not
// a multi-hit.
func decodeMultiHit(d []byte, off int) (count int, end int) {
	start := off
	if off >= len(d) {
		return 1, start
	}
	count = int(d[off])
	off++
	if count == 0 {
		return 1, start
	}
	first, n := parseVarint(d, off)
	if n <= 0 || first == 0 {
		return 1, start
	}
	off += n
	for i := 0; i < count-1; i++ {
		v, m := parseVarint(d, off)
		if m <= 0 || v != first {
			return 1, start
		}
		off += m
	}
	return count, off
}
