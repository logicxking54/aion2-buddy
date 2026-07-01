//go:build windows

package capture

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// detectedOpcodes tags an inspected packet with any known 2-byte opcodes it
// contains, using the corrected opcodeNames map (see decode.go) ported from the
// reference DPS-meter decoder.
func detectedOpcodes(p []byte) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i+1 < len(p); i++ {
		if name, ok := opcodeNames[uint16(p[i])|uint16(p[i+1])<<8]; ok && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// fmtID renders a skill id with its catalog name, e.g. "15030450(Burst)", or
// "15030450(?)" when the id isn't a known variant — used to make log lines
// human-readable.
func fmtID(id uint32, names map[uint32]string) string {
	if n := names[id]; n != "" {
		return fmt.Sprintf("%d(%s)", id, n)
	}
	return fmt.Sprintf("%d(?)", id)
}

// annotateCast renders the cast as an annotated byte map: each decoded field is
// shown as label@off[hex]=value, and the trailing region we don't fully parse is
// dumped raw and flagged undecoded (with the one 0f1838 record we do understand
// called out). Lets you eyeball which bytes are known vs unknown.
func annotateCast(data []byte, skillOffset int, names map[uint32]string) string {
	hx := func(a, b int) string {
		if a < 0 {
			a = 0
		}
		if b > len(data) {
			b = len(data)
		}
		parts := make([]string, 0, b-a)
		for i := a; i < b; i++ {
			parts = append(parts, fmt.Sprintf("%02x", data[i]))
		}
		return strings.Join(parts, " ")
	}
	if skillOffset+6 > len(data) {
		return "MAP (packet too short)"
	}
	var b strings.Builder
	b.WriteString("MAP:")
	fmt.Fprintf(&b, " prefix[%s]", hx(skillOffset-4, skillOffset))
	sid := binary.LittleEndian.Uint32(data[skillOffset : skillOffset+4])
	fmt.Fprintf(&b, " skill@%d[%s]=%s", skillOffset, hx(skillOffset, skillOffset+4), fmtID(sid, names))
	fmt.Fprintf(&b, " tick@%d[%02x]=%d", skillOffset+4, data[skillOffset+4], data[skillOffset+4])
	pkt := data[skillOffset+5]
	fmt.Fprintf(&b, " pkt@%d[%02x]=%s", skillOffset+5, pkt, map[byte]string{0x02: "cast", 0x00: "buff", 0x03: "effect"}[pkt])
	pos := skillOffset + 6
	ek, n := parseVarint(data, pos)
	fmt.Fprintf(&b, " entity@%d[%s]=%d", pos, hx(pos, pos+n), ek)
	pos += n
	if ek >= 100_000_000 {
		pid, pn := parseVarint(data, pos)
		fmt.Fprintf(&b, " parts@%d[%s]=%d", pos, hx(pos, pos+pn), pid)
		pos += pn
	}
	if pos+16 <= len(data) {
		f := func(o int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(data[o : o+4])) }
		fmt.Fprintf(&b, " pos@%d[%s]=(%.1f,%.1f,%.1f,%.1f)", pos, hx(pos, pos+16), f(pos), f(pos+4), f(pos+8), f(pos+12))
		pos += 16
		sp, sn := parseVarint(data, pos)
		fmt.Fprintf(&b, " speed@%d[%s]=%d", pos, hx(pos, pos+sn), sp)
		pos += sn
		if pos < len(data) {
			fmt.Fprintf(&b, " post@%d[%02x]", pos, data[pos])
			pos++
		}
	}
	if pos < len(data) {
		// Frame the trailing region into its length-prefixed sub-records and
		// label each (next-form / skill-refs / battle / trailer / unknown).
		fmt.Fprintf(&b, " || TRAILING@%d:%s", pos, annotateTrailing(data, pos, names))
	}
	return b.String()
}

// describeCast decodes every known field of a server-response cast at skillOffset
// into a one-line summary, for the "Decode" log. Best-effort: fields that don't
// fit in the packet are simply omitted.
func describeCast(data []byte, skillOffset int, names map[uint32]string) string {
	var b strings.Builder
	if skillOffset+6 > len(data) {
		return "DECODE (packet too short)"
	}
	sid := binary.LittleEndian.Uint32(data[skillOffset : skillOffset+4])
	tick := data[skillOffset+4]
	pkt := data[skillOffset+5]
	kind := map[byte]string{0x02: "cast", 0x00: "buff", 0x03: "effect"}[pkt]
	fmt.Fprintf(&b, "DECODE skill=%s tick=%d pkt=0x%02x(%s)", fmtID(sid, names), tick, pkt, kind)
	pos := skillOffset + 6
	ek, n := parseVarint(data, pos)
	pos += n
	fmt.Fprintf(&b, " caster=%d", ek)
	if ek >= 100_000_000 { // high keys carry an extra parts_id varint
		pid, pn := parseVarint(data, pos)
		pos += pn
		fmt.Fprintf(&b, " parts=%d", pid)
	}
	if pos+16 <= len(data) {
		f := func(o int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(data[o : o+4])) }
		fmt.Fprintf(&b, " pos=(yaw=%.1f x=%.1f z=%.1f y=%.1f)", f(pos), f(pos+4), f(pos+8), f(pos+12))
		pos += 16
		if pos < len(data) {
			sp, _ := parseVarint(data, pos)
			fmt.Fprintf(&b, " speed=%d", sp)
		}
	}
	return b.String()
}

// SkillSpeed is one configured skill from the UI. IDs is the expanded id set
// (every charge-tier variant, so one row covers all tiers); PrimaryIDs is the
// row's OWN tier ids, which override the expanded fallback so multiple tier rows
// (base / Level / Max) can carry different speeds without colliding.
type SkillSpeed struct {
	Name       string   `json:"name"`
	IDs        []uint32 `json:"ids"`
	PrimaryIDs []uint32 `json:"primaryIds"`
	SpeedPct   int      `json:"speedPct"`
	Break      bool     `json:"break"`
	Override   bool     `json:"override"` // use SpeedPct even in auto-mode
}

type skillCfg struct {
	name     string
	speedPct int
	brk      bool
	override bool
}

// Engine intercepts inbound game packets and rewrites the combat-speed varint
// for configured skills. It mirrors pingmaker's CaptureEngine (core path only:
// no multi-character entity filtering, direct-mode ports).
type Engine struct {
	emit func(event string, payload any)

	lifecycle sync.Mutex // serializes Start/Stop so their goroutine/WaitGroup don't overlap
	mu        sync.Mutex
	// Modify config (the skills the user added).
	cfgIDs        map[uint32]struct{}
	cfgFirstBytes map[byte]struct{}
	lookup        map[uint32]skillCfg
	// Full catalog (all known skills) — used to log every cast, not just configured ones.
	catIDs        map[uint32]struct{}
	catFirstBytes map[byte]struct{}
	idToName      map[uint32]string
	recentUsed    map[uint64]int64 // dedupe "used" log lines per (skill, action tick)
	lastActAt     map[uint32]int64 // last 0x02 ACT time per skill id (guards 0x00 buff casts)
	tracker       *entityTracker   // character-name filter ("only my skills")
	reasm         streamReassembler
	curPorts      map[int]struct{} // server/proxy ports of the active handle (for direction)
	lastReqByConn map[int]int64    // client port -> last outbound time (ns), for RTT
	lastReqPkt    map[int][]byte   // client port -> last outbound payload, for request decoding
	autoMode      bool             // compute bonus from ping instead of per-skill field
	autoBase      float64          // packet add at 0ms ping
	autoPerMs     float64          // extra packet add per ms of ping
	pingEWMA      float64          // smoothed ping (ms); written only by packet goroutine
	pingHasValue  bool
	lastPingEmit  int64
	inspect       bool              // packet inspector enabled
	inspectAll    bool              // true = dump every inbound packet; false = only skill casts
	inspectCount  int64             // emitted-message counter (capped)
	decode        bool              // print all decoded fields for each cast
	maskOn        bool              // FPS mask: rewrite other players' cast skill_id to Dodge
	maskKeep      uint64            // your caster (entity key) left untouched; 0 = not locked yet
	maskDodge     uint32            // skill_id written over masked casts (a no-VFX Dodge id)
	animMaskOn    bool              // "disable skill anims except mine": rewrite non-edit-list, non-own casts
	animMaskID    uint32            // skill_id written over anim-masked casts (a tiny no-anim skill)
	swapMap       map[uint32]uint32 // Ping Maker "render as" override: rewrite a cast's skill_id to another; nil = off
	casterFilter  uint64            // engine-level caster filter: only this caster is processed; 0 = all
	actorNames    map[uint64]string // learned actor_id -> character name (for the caster picker)
	actLog        []uint64          // distinct configured-skill ACT casters seen this session (picker options)
	actSig        string            // signature of the last emitted act-casters list (dedupe)
	running       bool

	stop       chan struct{}
	ports      *portTracker
	curHandle  handle
	handleMu   sync.Mutex
	auditLogMu sync.Mutex
	auditCh    chan hellfireAuditEntry
	auditWG    sync.WaitGroup
	wg         sync.WaitGroup

	sessionCh chan string    // raw-packet session recorder lines (nil = off); read under mu
	sessionWG sync.WaitGroup // session writer goroutine

	modified uint64
}

// NewEngine creates an engine that emits UI events through emit.
func NewEngine(emit func(event string, payload any)) *Engine {
	return &Engine{
		emit:          emit,
		cfgIDs:        map[uint32]struct{}{},
		cfgFirstBytes: map[byte]struct{}{},
		lookup:        map[uint32]skillCfg{},
		catIDs:        map[uint32]struct{}{},
		catFirstBytes: map[byte]struct{}{},
		idToName:      map[uint32]string{},
		recentUsed:    map[uint64]int64{},
		lastActAt:     map[uint32]int64{},
		tracker:       newEntityTracker(),
		actorNames:    map[uint64]string{},
		curPorts:      map[int]struct{}{},
		lastReqByConn: map[int]int64{},
		lastReqPkt:    map[int][]byte{},
		autoBase:      6800,
		autoPerMs:     80,
		animMaskID:    17000101, // default no-animation skill for the anim mask
	}
}

const inspectCap = 500 // stop emitting after this many messages until re-enabled

// captureDiskLog gates the on-disk Hellfire audit log (pingmaker-hellfire-audit
// .log). TEMP: off because the per-entry file flush slows the packet loop in
// dense fights. The in-app UI log is unaffected. Set true to restore disk logging.
var captureDiskLog = false

type hellfireAuditEntry struct {
	at          time.Time
	event       string
	payload     []byte
	payloadLen  int
	skillOffset int
}

// SetInspect enables/disables the packet inspector. all=true dumps every packet
// in both directions; all=false dumps only packets where a skill cast (inbound)
// or skill request (outbound) is detected.
func (e *Engine) SetInspect(on, all bool) {
	e.mu.Lock()
	e.inspect = on
	e.inspectAll = all
	e.mu.Unlock()
	atomic.StoreInt64(&e.inspectCount, 0)
	if on {
		mode := "skill casts + requests"
		if all {
			mode = "all in+out"
		}
		e.emitLog("Inspector ON (" + mode + ")")
	} else {
		e.emitLog("Inspector OFF")
	}
}

// emitInspect sends one inspected message (hex + detected opcodes) to the UI,
// up to inspectCap entries.
func (e *Engine) emitInspect(payload []byte, label string, skillOffset int) {
	if atomic.AddInt64(&e.inspectCount, 1) > inspectCap {
		return
	}
	// Dump up to the first 600 bytes (the old capped window), not the whole packet.
	max := len(payload)
	if max > 600 {
		max = 600
	}
	e.fire("inspector:packet", map[string]interface{}{
		"label":   label,
		"len":     len(payload),
		"offset":  skillOffset,
		"hex":     hex.EncodeToString(payload[:max]),
		"opcodes": detectedOpcodes(payload),
	})
}

// SetAuto toggles auto-mode and sets the ping→add curve: add = base + perMs*ping
// (in raw packet units, added on top of the skill's existing speed).
func (e *Engine) SetAuto(enabled bool, base, perMs float64) {
	e.mu.Lock()
	e.autoMode = enabled
	if base >= 0 {
		e.autoBase = base
	}
	if perMs >= 0 {
		e.autoPerMs = perMs
	}
	b, p := e.autoBase, e.autoPerMs
	e.mu.Unlock()
	if enabled {
		e.emitLog(fmt.Sprintf("Auto-mode ON (add = %.0f + %.0f x ping)", b, p))
	} else {
		e.emitLog("Auto-mode OFF")
	}
}

// SetCasterMask toggles the FPS-saver mask. When on AND keepCaster is known
// (non-zero — i.e. we've seen your ACT and locked your caster), every inbound
// cast from a DIFFERENT caster has its main skill_id overwritten with dodgeID (a
// near-invisible Dodge skill) so the client renders nothing heavy for other
// players → higher FPS in crowded fights. It's a same-length in-place edit, so
// the TCP stream never desyncs; your own caster is never touched, and nothing
// happens until keepCaster is set. The frontend re-calls this whenever the
// toggle or your auto-detected caster changes, so the logging is de-duped to the
// transitions that matter.
func (e *Engine) SetCasterMask(on bool, keepCaster uint64, dodgeID uint32) {
	e.mu.Lock()
	wasOn, hadKeep := e.maskOn, e.maskKeep
	e.maskOn = on
	e.maskKeep = keepCaster
	if dodgeID != 0 {
		e.maskDodge = dodgeID
	}
	dodge := e.maskDodge
	e.mu.Unlock()

	switch {
	case on && keepCaster != 0 && (!wasOn || hadKeep == 0):
		// Became active: turned on with a known caster, or our caster was just
		// detected after the mask was armed and waiting.
		e.emitLog(fmt.Sprintf("FPS mask ON — hiding other players (keep caster %d, others -> Dodge %d)", keepCaster, dodge))
	case on && keepCaster == 0 && !wasOn:
		e.emitLog("FPS mask ARMED — waiting for your caster (cast a skill so we lock onto you)…")
	case !on && wasOn:
		e.emitLog("FPS mask OFF")
	}
}

// SetAnimMask toggles the "disable skill animations (except mine)" mod. When on
// AND your caster is locked (casterFilter != 0), every inbound cast that is from a
// DIFFERENT caster AND is NOT one of your configured (edit-list) skills has its
// main skill_id overwritten with replaceID — a tiny no-animation skill — so the
// client renders a quick dash instead of that cast. Your own casts and edit-list
// skills (any caster) keep their real animation. Same-length in-place edit, so the
// TCP stream never desyncs; nothing happens until your caster is locked. Works on
// both plaintext and LZ4-compressed (party) casts. replaceID == 0 keeps the
// current id (default 17000101).
func (e *Engine) SetAnimMask(on bool, replaceID uint32) {
	e.mu.Lock()
	was := e.animMaskOn
	e.animMaskOn = on
	if replaceID != 0 {
		e.animMaskID = replaceID
	}
	id := e.animMaskID
	e.mu.Unlock()

	switch {
	case on && !was:
		e.emitLog(fmt.Sprintf("Skill-anim mask ON — others' non-edit-list casts -> skill %d (your casts + edit-list kept; lock your caster first)", id))
	case !on && was:
		e.emitLog("Skill-anim mask OFF")
	}
}

// SetSkillSwap sets the Ping Maker "render as" override: an in-place, same-length
// rewrite of a cast's main skill_id to another skill's id, so the client renders the
// chosen skill's animation + VFX (server-side outcome is unchanged). from[i] -> to[i]
// pairwise (every configured skill's variants -> the target skill's base id). Applies
// to ALL casters and works on both plaintext and LZ4-compressed (party) casts. Passing
// on=false (or empty lists) clears it. The mapping is small (a few skills' variants),
// so we build a fresh map each call under the lock.
func (e *Engine) SetSkillSwap(on bool, from, to []uint32) {
	e.mu.Lock()
	was := e.swapMap != nil
	if !on || len(from) == 0 {
		e.swapMap = nil
	} else {
		m := make(map[uint32]uint32, len(from))
		for i := range from {
			if i < len(to) && to[i] != 0 && to[i] != from[i] {
				m[from[i]] = to[i]
			}
		}
		if len(m) == 0 {
			m = nil
		}
		e.swapMap = m
	}
	nowOn := e.swapMap != nil
	n := len(e.swapMap)
	e.mu.Unlock()

	switch {
	case nowOn && !was:
		e.emitLog(fmt.Sprintf("Skill-swap (test) ON — %d id(s) remapped", n))
	case !nowOn && was:
		e.emitLog("Skill-swap (test) OFF")
	}
}

// SetCasterFilter sets the engine-level caster filter. When id != 0, only casts
// from that caster (entity key) are processed — every other caster is ignored
// entirely: no log line, no cast event, and no combat-speed edit. id == 0 clears
// the filter (process all casters). The caster id is entered manually in the UI
// (no auto-detect), and the frontend re-calls this whenever the field changes, so
// this stays quiet — no logging on every update.
func (e *Engine) SetCasterFilter(id uint64) {
	e.mu.Lock()
	e.casterFilter = id
	e.mu.Unlock()
}

const actLogMax = 20 // max distinct ACT casters kept as picker options

// actCaster is one recent caster of a configured-skill ACT cast, with its learned
// character name (empty until a name binding is seen in the stream).
type actCaster struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

// recordActCaster accumulates the DISTINCT casters of your configured skills (in
// first-seen order) for the current session and emits them (with any learned name)
// as "capture:act-casters". Anyone who casts a configured skill stays in the list
// even after they stop casting, so the picker never loses a valid option — the UI
// keeps your selection sticky and only auto-locks when there's no valid pick yet.
// Reset() clears the list on a session change, which lets the UI drop a stale
// (previous-session) caster id and re-lock. Dedupes the emitted list so it stays
// quiet. Runs only on the single packet goroutine, so the list needs no extra lock.
func (e *Engine) recordActCaster(caster uint64) {
	known := false
	for _, id := range e.actLog {
		if id == caster {
			known = true
			break
		}
	}
	if !known {
		e.actLog = append(e.actLog, caster)
		if len(e.actLog) > actLogMax {
			e.actLog = e.actLog[len(e.actLog)-actLogMax:] // bound: drop the oldest option
		}
	}
	list := make([]actCaster, 0, len(e.actLog))
	for _, id := range e.actLog {
		list = append(list, actCaster{ID: id, Name: e.actorNames[id]})
	}
	sig := fmt.Sprint(list)
	if sig == e.actSig {
		return
	}
	e.actSig = sig
	e.fire("capture:act-casters", list)
}

// SetDecode toggles full per-cast field decoding to the log.
func (e *Engine) SetDecode(on bool) {
	e.mu.Lock()
	e.decode = on
	e.mu.Unlock()
	if on {
		e.emitLog("Decode ON (full field dump per cast)")
	} else {
		e.emitLog("Decode OFF")
	}
}

func sessionPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "aion2-buddy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "pingmaker-session-"+time.Now().Format("20060102-150405")+".jsonl"), nil
}

// SetSessionRecord toggles raw-packet session recording. When on, EVERY captured
// packet (inbound + outbound, all sizes, original pre-edit bytes) is appended to
// a timestamped JSONL file for offline analysis — uncapped, written by an async
// buffered writer so it never slows the packet loop. Returns the file path when
// starting.
func (e *Engine) SetSessionRecord(on bool) (string, error) {
	if !on {
		e.mu.Lock()
		old := e.sessionCh
		e.sessionCh = nil
		e.mu.Unlock()
		if old != nil {
			close(old)
			e.sessionWG.Wait()
			e.emitLog("Session recording stopped")
		}
		return "", nil
	}
	path, err := sessionPath()
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	ch := make(chan string, 8192)
	e.sessionWG.Add(1)
	go func(ch <-chan string, f *os.File) {
		defer e.sessionWG.Done()
		defer f.Close()
		w := bufio.NewWriterSize(f, 256*1024)
		defer w.Flush()
		for line := range ch {
			_, _ = w.WriteString(line)
			_ = w.WriteByte('\n')
		}
	}(ch, f)

	e.mu.Lock()
	old := e.sessionCh
	e.sessionCh = ch
	e.mu.Unlock()
	if old != nil { // restart: drain the previous writer
		close(old)
	}
	e.emitLog("Session recording: " + path)
	return path, nil
}

// recordPacket appends one raw-packet line to the session recorder. ch is read
// once under e.mu by the caller, so this never locks on the hot path. The TCP
// sequence number is included so offline analysis can reconstruct stream order
// and spot casts split across segments. Drops the line (never blocks) if the
// writer is backed up.
func (e *Engine) recordPacket(ch chan string, dir string, raw, payload []byte) {
	seqStr := "null"
	if seq, ok := tcpSequence(raw); ok {
		seqStr = strconv.FormatUint(uint64(seq), 10)
	}
	line := fmt.Sprintf(`{"time":"%s","dir":"%s","len":%d,"seq":%s,"hex":"%s"}`,
		time.Now().Format("15:04:05.000"), dir, len(payload), seqStr, hex.EncodeToString(payload))
	select {
	case ch <- line:
	default:
	}
}

// recordCast appends one per-cast annotation line (the edit decision) to the
// session recorder, interleaved with the raw-packet lines so each cast can be
// correlated to the packet it came from. fields is the inner JSON of "cast".
func (e *Engine) recordCast(ch chan string, fields string) {
	line := fmt.Sprintf(`{"time":"%s","cast":{%s}}`, time.Now().Format("15:04:05.000"), fields)
	select {
	case ch <- line:
	default:
	}
}

// updatePing folds a new RTT sample into the smoothed ping and emits it
// (throttled). Called only from the packet goroutine.
func (e *Engine) updatePing(ms int) {
	if e.pingHasValue {
		e.pingEWMA = e.pingEWMA*0.8 + float64(ms)*0.2
	} else {
		e.pingEWMA = float64(ms)
		e.pingHasValue = true
	}
	now := time.Now().UnixNano()
	if now-e.lastPingEmit > int64(500*time.Millisecond) {
		e.lastPingEmit = now
		e.fire("capture:ping", int(e.pingEWMA+0.5))
	}
}

// SetCharacterNames restricts capture to the given character name(s); empty
// list = no filter (show every cast). When set, only casts owned by your
// character are logged/modified, once its entity key is learned from the stream.
func (e *Engine) SetCharacterNames(names []string) {
	clean := make([]string, 0, len(names))
	for _, n := range names {
		if s := strings.TrimSpace(n); s != "" {
			clean = append(clean, s)
		}
	}
	e.tracker.updateNames(clean)
	e.fire("capture:character", "")
	if len(clean) > 0 {
		e.emitLog("Detecting character: " + strings.Join(clean, ", ") + " — cast a skill or zone to lock on")
	} else {
		e.emitLog("Character filter cleared (showing all casts)")
	}
}

func (e *Engine) fire(event string, payload any) {
	if e.emit != nil {
		e.emit(event, payload)
	}
}

func (e *Engine) emitLog(msg string) { e.fire("capture:log", msg) }

func hellfireAuditPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "aion2-buddy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "pingmaker-hellfire-audit.log"), nil
}

func isHellfireName(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "hellfire")
}

func skipSpeedEditForStability(name string, id uint32) bool {
	if !isHellfireName(name) {
		return false
	}
	// Hellfire charge tier is encoded in the final decimal digit. Keep the guard
	// centralized so unstable tiers can be disabled quickly after dungeon tests.
	return false
}

func (e *Engine) resetHellfireAudit() (string, error) {
	path, err := hellfireAuditPath()
	if err != nil {
		return "", err
	}
	e.auditLogMu.Lock()
	defer e.auditLogMu.Unlock()
	header := fmt.Sprintf("# Hellfire audit started %s\n", time.Now().Format(time.RFC3339Nano))
	return path, os.WriteFile(path, []byte(header), 0o644)
}

func (e *Engine) startHellfireAuditWriter(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	e.auditCh = make(chan hellfireAuditEntry, 256)
	e.auditWG.Add(1)
	go func(ch <-chan hellfireAuditEntry) {
		defer e.auditWG.Done()
		defer f.Close()
		w := bufio.NewWriterSize(f, 64*1024)
		defer w.Flush()
		for entry := range ch {
			_, _ = fmt.Fprintf(w, "%s | %s | offset=%d len=%d saved=%d hex=%s\n",
				entry.at.Format(time.RFC3339Nano), entry.event, entry.skillOffset,
				entry.payloadLen, len(entry.payload), hex.EncodeToString(entry.payload))
			_ = w.Flush()
		}
	}(e.auditCh)
	return nil
}

func (e *Engine) stopHellfireAuditWriter() {
	if e.auditCh == nil {
		return
	}
	close(e.auditCh)
	e.auditCh = nil
	e.auditWG.Wait()
}

// auditHellfire writes a bounded packet snapshot for Hellfire only. This keeps
// a full dungeon session small enough to inspect while retaining each request
// and edit decision in chronological order.
func (e *Engine) auditHellfire(event string, payload []byte, skillOffset int) {
	if !captureDiskLog { // TEMP: disk audit disabled for performance
		return
	}
	const maxAuditPayload = 2048
	snapshot := payload
	if len(snapshot) > maxAuditPayload {
		snapshot = snapshot[:maxAuditPayload]
	}
	entry := hellfireAuditEntry{
		at:          time.Now(),
		event:       event,
		payload:     append([]byte(nil), snapshot...),
		payloadLen:  len(payload),
		skillOffset: skillOffset,
	}
	select {
	case e.auditCh <- entry:
	default:
		// Diagnostics must never delay packet reinjection.
	}
}

// SetConfig rebuilds the speed lookup (and a config-only scan fallback) from
// the UI config.
func (e *Engine) SetConfig(skills []SkillSpeed) {
	ids := map[uint32]struct{}{}
	fb := map[byte]struct{}{}
	lk := map[uint32]skillCfg{}
	assign := func(id uint32, cfg skillCfg) {
		ids[id] = struct{}{}
		fb[byte(id&0xFF)] = struct{}{}
		lk[id] = cfg
	}
	// Pass 1: expanded ids — a single charge-skill row covers every tier (the
	// charged cast fires under a tier id, not the base).
	for _, s := range skills {
		cfg := skillCfg{name: s.Name, speedPct: s.SpeedPct, brk: s.Break, override: s.Override}
		for _, id := range s.IDs {
			assign(id, cfg)
		}
	}
	// Pass 2: own (primary) ids win. When the user adds multiple tier rows with
	// different speeds (e.g. Hellfire +1000% and Hellfire - Max +50%), each tier's
	// own row overrides the expanded fallback so they don't clobber each other.
	for _, s := range skills {
		cfg := skillCfg{name: s.Name, speedPct: s.SpeedPct, brk: s.Break, override: s.Override}
		for _, id := range s.PrimaryIDs {
			assign(id, cfg)
		}
	}
	e.mu.Lock()
	e.cfgIDs = ids
	e.cfgFirstBytes = fb
	e.lookup = lk
	e.mu.Unlock()
}

// CatalogEntry maps a skill name to all its in-game IDs. The catalog lets the
// engine recognise and log every skill cast, even ones not in the modify config.
type CatalogEntry struct {
	Name string   `json:"name"`
	IDs  []uint32 `json:"ids"`
}

// SetCatalog loads the full known-skill list (sent once by the UI).
func (e *Engine) SetCatalog(entries []CatalogEntry) {
	ids := map[uint32]struct{}{}
	fb := map[byte]struct{}{}
	names := map[uint32]string{}
	for _, en := range entries {
		for _, id := range en.IDs {
			ids[id] = struct{}{}
			fb[byte(id&0xFF)] = struct{}{}
			names[id] = en.Name
		}
	}
	e.mu.Lock()
	e.catIDs = ids
	e.catFirstBytes = fb
	e.idToName = names
	e.mu.Unlock()
}

// shouldLogUsed throttles duplicate "used" lines for the same (skill, action).
// Only called from the single packet-processing goroutine.
func (e *Engine) shouldLogUsed(id uint32, tick byte) bool {
	key := uint64(id)<<8 | uint64(tick)
	now := time.Now().UnixNano()
	if last, ok := e.recentUsed[key]; ok && now-last < int64(time.Second) {
		return false
	}
	e.recentUsed[key] = now
	if len(e.recentUsed) > 512 {
		for k, ts := range e.recentUsed {
			if now-ts > int64(2*time.Second) {
				delete(e.recentUsed, k)
			}
		}
	}
	return true
}

// Start begins capture. If already running, it just hot-reloads the config.
func (e *Engine) Start(skills []SkillSpeed) error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()

	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		e.SetConfig(skills)
		e.emitLog("Config updated")
		return nil
	}
	e.running = true
	e.stop = make(chan struct{})
	e.recentUsed = map[uint64]int64{}
	e.lastReqByConn = map[int]int64{}
	e.pingEWMA = 0
	e.pingHasValue = false
	e.lastPingEmit = 0
	e.reasm.reset()
	e.actLog = nil
	e.actSig = ""
	e.casterFilter = 0
	e.actorNames = map[uint64]string{}
	atomic.StoreUint64(&e.modified, 0)
	e.mu.Unlock()

	e.SetConfig(skills)
	auditPath := ""
	if path, err := e.resetHellfireAudit(); err == nil {
		auditPath = path
		e.emitLog("Hellfire audit: " + path)
	} else {
		e.emitLog("Hellfire audit error: " + err.Error())
	}

	if err := prepareDriver(); err != nil {
		e.fire("capture:error", "Driver setup failed: "+err.Error())
		e.fire("capture:status", "error")
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
		return err
	}
	if auditPath != "" {
		if err := e.startHellfireAuditWriter(auditPath); err != nil {
			e.emitLog("Hellfire audit error: " + err.Error())
		}
	}

	e.ports = newPortTracker()
	e.ports.start(func(ports []int, loopback bool) {
		e.fire("capture:ports", ports)
		e.closeCurHandle() // force the intercept loop to reopen with new ports
	})

	e.fire("capture:status", "running")
	e.fire("capture:count", uint64(0))
	e.wg.Add(1)
	go e.interceptLoop()
	return nil
}

// Stop ends capture and returns to idle.
func (e *Engine) Stop() {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()

	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}
	e.running = false
	close(e.stop)
	pt := e.ports
	e.mu.Unlock()

	// Close the handle first so interception stops immediately, and flip the UI
	// to idle right away — the cleanup below shouldn't delay the button.
	e.closeCurHandle()
	e.fire("capture:status", "idle")
	e.emitLog("Stopped")

	if pt != nil {
		pt.stopTracking()
	}
	e.wg.Wait()
	e.stopHellfireAuditWriter()
	_, _ = e.SetSessionRecord(false) // flush + close any active session recording
}

// IsRunning reports whether capture is active.
func (e *Engine) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

func (e *Engine) closeCurHandle() {
	e.handleMu.Lock()
	h := e.curHandle
	e.curHandle = 0
	e.handleMu.Unlock()
	if h != 0 {
		h.shutdown() // unblock the recv first (WinDivert 2.x), then close
		h.close()
	}
}

func (e *Engine) interceptLoop() {
	defer e.wg.Done()
	for {
		select {
		case <-e.stop:
			return
		default:
		}

		ports, loopback := e.ports.get()
		h, err := openHandle(buildFilter(ports, loopback), 0)
		if err != nil {
			e.fire("capture:error", adminHint(err))
			select {
			case <-e.stop:
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}
		h.tune()

		e.handleMu.Lock()
		e.curHandle = h
		e.handleMu.Unlock()

		// Record the active server/proxy ports for direction classification.
		// Safe to set here: runHandle (and processPacket) run in this goroutine.
		portSet := make(map[int]struct{}, len(ports))
		for _, p := range ports {
			portSet[p] = struct{}{}
		}
		e.curPorts = portSet

		switch {
		case len(ports) == 0:
			e.emitLog("Intercepting (waiting for game ports)")
		case loopback:
			e.emitLog("Intercepting loopback (proxy/VPN) ports " + fmtPorts(ports))
		default:
			e.emitLog("Intercepting ports " + fmtPorts(ports))
		}

		e.runHandle(h)

		e.handleMu.Lock()
		if e.curHandle == h {
			e.curHandle = 0
		}
		e.handleMu.Unlock()
	}
}

// runHandle reads packets until the handle is closed (stop or port change).
func (e *Engine) runHandle(h handle) {
	buf := make([]byte, 65535)
	for {
		n, addr, err := h.recv(buf)
		if err != nil {
			return // handle closed
		}
		e.processPacket(buf[:n], &addr, h)
	}
}

// lz4DecompressEdit gates the compressed-cast editor (party-load LZ4 packets).
// Length-preserving and safe, so it defaults on; flip to false to fall back to
// the legacy wire-literal heuristic if a problem ever shows up in the field.
var lz4DecompressEdit = true

// editCompressedCasts handles party-load LZ4-compressed inbound packets. It
// decompresses the block (with literal provenance), edits each configured cast's
// attack-speed in the CLEAN stream — where the strict id-anchored parser is
// reliable — then writes the new speed bytes back into the exact block literals.
// The edit is length-preserving (same bytes, same count), so the TCP segment is
// untouched and no sequence rewriting is needed. Casts whose speed is encoded as
// an LZ4 back-reference (not a literal) can't be reached this way and are logged.
//
// Returns handled=true when the payload was an LZ4 frame (the caller should then
// skip the legacy plaintext path), and edited=true if any speed byte was changed.
func (e *Engine) editCompressedCasts(
	raw, payload []byte, payloadOffset int,
	scanIDs map[uint32]struct{}, scanFB map[byte]struct{},
	lookup map[uint32]skillCfg, idToName map[uint32]string,
	casterFilter uint64, filtering, autoMode bool, autoBase, autoPerMs float64,
	animMaskOn bool, animMaskID uint32, swapMap map[uint32]uint32,
) (handled, edited bool) {
	blockStart, origLen, ok := parseLZ4Frame(payload)
	if !ok {
		return false, false
	}
	handled = true
	clean, litSrc, dok := lz4DecompressTrace(payload[blockStart:], origLen)
	if !dok {
		return handled, false // malformed → leave the packet untouched
	}

	hits := findAllSkillIDs(clean, scanIDs, scanFB, 0)
	for i := range hits {
		hh := hits[i]
		if hh.offset+6 > len(clean) {
			continue
		}
		if !(hh.prefixOK && clean[hh.offset+5] == 0x02) {
			continue // only 0x02 ACT casts carry the position+speed block
		}
		caster := extractEntityKey(clean, hh.offset)
		// Caster auto-detect: count ONLY casts of YOUR configured skills (the ones
		// that become an ACT edit) — not every catalog skill — so a party member's
		// casts can't thrash the lock onto the wrong caster. Runs before the filters
		// so it still sees every caster of your own skill.
		if caster != 0 {
			if _, conf := lookup[hh.id]; conf {
				e.recordActCaster(caster)
			}
		}

		// Skill-swap ("render as" override) in compressed (party) casts: rewrite the cast's
		// skill_id to another skill, mapping the 4 id bytes back into the LZ4 block
		// literals (length-preserving). Unreachable when any id byte is an LZ4
		// back-reference match (rare). Applies to any caster; skips the rest on swap.
		if swapMap != nil && hh.offset+4 <= len(clean) {
			if to, ok := swapMap[hh.id]; ok {
				var b [4]byte
				binary.LittleEndian.PutUint32(b[:], to)
				reachable := true
				for k := 0; k < 4; k++ {
					if hh.offset+k >= len(litSrc) || litSrc[hh.offset+k] < 0 {
						reachable = false
						break
					}
				}
				if reachable {
					for k := 0; k < 4; k++ {
						raw[payloadOffset+blockStart+litSrc[hh.offset+k]] = b[k]
					}
					edited = true
					e.countModify()
				}
				continue
			}
		}

		// "Disable skill animations (except mine)" in compressed (party) casts: rewrite
		// a known other caster's non-edit-list cast skill_id to the no-anim skill,
		// mapping the 4 id bytes back into the LZ4 block literals (length-preserving).
		// Unreachable when any id byte is an LZ4 back-reference match (rare). Your own
		// casts and edit-list skills are left untouched. Runs before the config filter.
		if animMaskOn && casterFilter != 0 && caster != 0 && caster != casterFilter {
			if _, inEdit := lookup[hh.id]; !inEdit {
				if animMaskID != 0 && hh.offset+4 <= len(clean) &&
					binary.LittleEndian.Uint32(clean[hh.offset:hh.offset+4]) != animMaskID {
					var b [4]byte
					binary.LittleEndian.PutUint32(b[:], animMaskID)
					reachable := true
					for k := 0; k < 4; k++ {
						if hh.offset+k >= len(litSrc) || litSrc[hh.offset+k] < 0 {
							reachable = false
							break
						}
					}
					if reachable {
						for k := 0; k < 4; k++ {
							raw[payloadOffset+blockStart+litSrc[hh.offset+k]] = b[k]
						}
						edited = true
						e.countModify()
					}
				}
				continue // other player's non-edit-list cast anim-masked
			}
		}

		cfg, isConfigured := lookup[hh.id]
		if !isConfigured {
			continue
		}
		if filtering && (caster == 0 || !e.tracker.isMine(caster)) {
			continue
		}
		if casterFilter != 0 && caster != 0 && caster != casterFilter {
			continue
		}
		name := idToName[hh.id]
		if name == "" {
			name = cfg.name
		}
		if skipSpeedEditForStability(name, hh.id) {
			continue
		}
		spdOff, spdLen, spdVal, isFloat, spdFound := findAttackSpeedOffset(clean, hh.offset)
		if !spdFound || spdVal < 10000 {
			continue
		}

		// Map every speed byte back to a block literal; bail if any is match-copied.
		blkPos := make([]int, spdLen)
		reachable := true
		for k := 0; k < spdLen; k++ {
			if spdOff+k >= len(litSrc) || litSrc[spdOff+k] < 0 {
				reachable = false
				break
			}
			blkPos[k] = litSrc[spdOff+k]
		}
		if !reachable {
			e.emitLog(fmt.Sprintf("ACT %s %d (lz4 match — unreachable) [Caster: %d, Id: %d]", name, spdVal, caster, hh.id))
			continue
		}

		// Compute the replacement bytes (same length as the original field).
		var newBytes []byte
		logSuffix := ""
		if cfg.brk {
			if isFloat {
				var b [4]byte
				binary.LittleEndian.PutUint32(b[:], math.Float32bits(float32(9999999.0/10000.0)))
				newBytes = b[:]
			} else {
				newBytes = bytes.Repeat([]byte{0xFF}, spdLen)
			}
			logSuffix = "break"
		} else {
			added := uint64(cfg.speedPct) * 100
			if autoMode && !cfg.override {
				a := autoBase + autoPerMs*e.pingEWMA
				if a < 0 {
					a = 0
				}
				added = uint64(a + 0.5)
			}
			target := spdVal + added
			if isFloat {
				if target > 9999999 {
					target = 9999999
				}
				var b [4]byte
				binary.LittleEndian.PutUint32(b[:], math.Float32bits(float32(float64(target)/10000.0)))
				newBytes = b[:]
			} else {
				maxVal := (uint64(1) << (7 * uint(spdLen))) - 1
				if target > maxVal {
					target = maxVal
				}
				newBytes = encodeVarintFixed(target, spdLen)
			}
			logSuffix = fmt.Sprintf("+%d", added)
		}
		if len(newBytes) != spdLen {
			continue // can't keep the field length — skip rather than desync
		}

		for k := 0; k < spdLen; k++ {
			raw[payloadOffset+blockStart+blkPos[k]] = newBytes[k]
		}
		edited = true
		e.countModify()
		e.emitLog(fmt.Sprintf("ACT %s %d %s [Caster: %d, Id: %d] (lz4)", name, spdVal, logSuffix, caster, hh.id))
	}
	return handled, edited
}

func (e *Engine) processPacket(raw []byte, addr *Address, h handle) {
	modified := false
	defer func() {
		if modified {
			calcChecksums(raw, addr)
		}
		_ = h.send(raw, addr) // always re-inject; ignore errors (handle may be closing)
	}()

	payload, payloadOffset, ok := parsePayload(raw)
	if !ok || len(payload) < 4 {
		return
	}

	srcPort, dstPort := tcpPorts(raw)
	_, srcIsServer := e.curPorts[srcPort]
	_, dstIsServer := e.curPorts[dstPort]

	e.mu.Lock()
	scanIDs, scanFB := e.catIDs, e.catFirstBytes
	if len(scanIDs) == 0 { // catalog not loaded — fall back to configured skills
		scanIDs, scanFB = e.cfgIDs, e.cfgFirstBytes
	}
	lookup := e.lookup
	idToName := e.idToName
	autoMode := e.autoMode
	autoBase := e.autoBase
	autoPerMs := e.autoPerMs
	inspect := e.inspect
	inspectAll := e.inspectAll
	decode := e.decode
	maskOn := e.maskOn
	maskKeep := e.maskKeep
	maskDodge := e.maskDodge
	animMaskOn := e.animMaskOn
	animMaskID := e.animMaskID
	swapMap := e.swapMap
	casterFilter := e.casterFilter
	sessionCh := e.sessionCh
	e.mu.Unlock()

	// Session recorder: dump every packet (original, pre-edit bytes) before any
	// other processing — runs even with no config so a whole dungeon is captured.
	if sessionCh != nil {
		dir := "other"
		switch {
		case srcIsServer:
			dir = "in"
		case dstIsServer:
			dir = "out"
		}
		e.recordPacket(sessionCh, dir, raw, payload)
	}
	if len(scanIDs) == 0 {
		return
	}

	// Outbound (client → server): timestamp skill requests so we can report how
	// long the server takes to respond (RTT). Best-effort — the request format
	// isn't documented, so we just scan for the skill ID.
	if dstIsServer && !srcIsServer {
		now := time.Now().UnixNano()
		e.lastReqByConn[srcPort] = now                          // last outbound time on this connection
		e.lastReqPkt[srcPort] = append([]byte(nil), payload...) // copy, for pairing/decoding
		requestHits := findAllSkillIDs(payload, scanIDs, scanFB, 0)
		seenHellfire := map[uint32]struct{}{}
		for _, hit := range requestHits {
			name := idToName[hit.id]
			if !isHellfireName(name) {
				continue
			}
			if _, duplicate := seenHellfire[hit.id]; duplicate {
				continue
			}
			seenHellfire[hit.id] = struct{}{}
			e.auditHellfire(fmt.Sprintf("REQUEST name=%q id=%d", name, hit.id), payload, hit.offset)
		}
		if len(e.lastReqByConn) > 256 {
			for k, t := range e.lastReqByConn {
				if now-t > int64(30*time.Second) {
					delete(e.lastReqByConn, k)
					delete(e.lastReqPkt, k)
				}
			}
		}
		// Inspector: surface client→server requests too (off the modify path).
		// "all" dumps every outbound packet; "skill" dumps only requests that
		// carry a known skill ID (the cast request), scanning from byte 0 since
		// outbound IDs sit near the start.
		if inspect {
			if inspectAll {
				e.emitInspect(payload, "outbound", -1)
			} else if len(requestHits) > 0 {
				h0 := requestHits[0]
				name := idToName[h0.id]
				if name == "" {
					name = fmt.Sprintf("ID:%d", h0.id)
				}
				e.emitInspect(payload, "request "+name, h0.offset)
			}
		}
		return
	}
	if !srcIsServer {
		return // not part of the server stream
	}

	// Inbound (server → client): the modifiable / loggable stream.
	if len(payload) < 40 {
		return
	}

	// Entity learning: feed the stream and lock onto the player's character key.
	for _, b := range e.reasm.feed(payload) {
		e.actorNames[b.actorID] = b.name // learn EVERY player's name, for the caster picker
		if name, locked := e.tracker.onBinding(b.actorID, b.name); locked {
			e.emitLog("Locked onto " + name)
			e.fire("capture:character", name)
		}
	}

	// Decode toggle: frame this payload into messages and log the combat ones
	// (damage / DoT / buff / boss-HP / battle) — ported from the DPS-meter
	// decoder. Read-only; runs per inbound payload independent of skill casts.
	if decode {
		for _, line := range decodeMessages(payload, idToName) {
			e.emitLog(line)
		}
	}

	if inspect && inspectAll {
		e.emitInspect(payload, "inbound", -1)
	}

	// Party-load packets are LZ4-compressed: the legacy plaintext scan below only
	// catches casts whose speed happens to land in an LZ4 literal. Handle the
	// compressed frame properly — decompress, edit speed in the clean stream, and
	// patch the block literals in place (length-preserving). Skip the mask path
	// (it edits other casters' ids and still uses the legacy route).
	filtering := e.tracker.isConfigured()
	if lz4DecompressEdit && !maskOn {
		if handled, ed := e.editCompressedCasts(raw, payload, payloadOffset,
			scanIDs, scanFB, lookup, idToName, casterFilter, filtering, autoMode, autoBase, autoPerMs,
			animMaskOn, animMaskID, swapMap); handled {
			if ed {
				modified = true
			}
			return
		}
	}

	hits := findAllSkillIDs(payload, scanIDs, scanFB, scanStartDefault)
	if len(hits) == 0 {
		return
	}

	pinged := false // ping is measured once per packet, on our own 0x02 cast

	// Process EVERY cast in the packet — rapid combos coalesce several skill
	// ACTs into one TCP segment, so we must not stop at the first.
	for i := range hits {
		hh := hits[i]
		if hh.offset+5 >= len(payload) {
			continue
		}
		// Classify this hit as a real "use" of the skill:
		//   prefixOK + 0x02 = damage / channelled ACT cast
		//   prefixOK + 0x00 = buff/instant activation  (e.g. Wish of Concentration)
		//   effect-apply    = buff with no ACT packet  (e.g. Element Enhancement)
		// The 0x00 and effect-apply paths are accepted only when this skill didn't
		// just fire a 0x02, so a normal cast's trailing events can't double-count.
		now := time.Now().UnixNano()
		recentAct := func() bool {
			t, ok := e.lastActAt[hh.id]
			return ok && now-t < int64(1500*time.Millisecond)
		}
		is02 := hh.prefixOK && payload[hh.offset+5] == 0x02
		switch {
		case is02:
			e.lastActAt[hh.id] = now
		case hh.prefixOK && payload[hh.offset+5] == 0x00:
			if recentAct() {
				continue
			}
		case hasEffectApply(payload, hh.offset):
			if recentAct() {
				continue
			}
		default:
			continue
		}
		caster := extractEntityKey(payload, hh.offset)

		// Caster auto-detect: count ONLY casts of YOUR configured skills (the ones
		// that become an ACT edit) — not every catalog skill — so a party member's
		// casts can't thrash the lock onto the wrong caster. When one caster exceeds
		// the threshold it becomes the active filter (replacing any current value)
		// and the UI is notified. Runs before the filters below so it still sees
		// every caster of your own skill.
		if is02 && caster != 0 {
			if _, conf := lookup[hh.id]; conf {
				e.recordActCaster(caster)
			}
		}

		// Skill-swap ("render as" override): rewrite this cast's skill_id to another skill so the
		// client renders the swapped skill (e.g. Pyroclasm -> Blaze). Same-length in-place
		// edit, so the TCP stream stays in sync. Applies to any caster; once swapped we
		// skip the rest (speed edit / mask / filter) for this hit.
		if swapMap != nil && hh.offset+4 <= len(payload) {
			if to, ok := swapMap[hh.id]; ok {
				binary.LittleEndian.PutUint32(raw[payloadOffset+hh.offset:payloadOffset+hh.offset+4], to)
				modified = true
				e.countModify()
				continue
			}
		}

		// FPS mask: rewrite OTHER players' cast skill_id to a no-VFX Dodge skill so
		// the client renders nothing heavy for them. Same-length in-place edit, so
		// the TCP stream stays in sync (unlike dropping the packet). Armed only when
		// the toggle is on AND our caster is known (maskKeep != 0); our own caster is
		// never touched. Runs BEFORE the "only my skills" filter below, because that
		// filter drops other casters' casts — which are exactly what we want to edit.
		if maskOn && maskKeep != 0 && caster != 0 && caster != maskKeep {
			if maskDodge != 0 && hh.offset+4 <= len(payload) {
				if orig := binary.LittleEndian.Uint32(payload[hh.offset : hh.offset+4]); orig != maskDodge {
					binary.LittleEndian.PutUint32(raw[payloadOffset+hh.offset:payloadOffset+hh.offset+4], maskDodge)
					modified = true
					e.countModify()
				}
			}
			continue // other player's cast masked; skip our own-cast processing
		}

		// "Disable skill animations (except mine)": rewrite the cast skill_id of any
		// cast from a KNOWN other caster that is NOT one of your configured (edit-list)
		// skills to a tiny no-animation skill, so the client shows a quick dash for it
		// instead of the full cast. Your own casts (caster == casterFilter) and
		// edit-list skills (any caster) keep their animation. Same-length in-place edit;
		// armed only once your caster is locked. Runs BEFORE the caster filter below
		// (which drops other casters), because masked casts are exactly other casters'.
		if animMaskOn && casterFilter != 0 && caster != 0 && caster != casterFilter {
			if _, inEdit := lookup[hh.id]; !inEdit {
				if animMaskID != 0 && hh.offset+4 <= len(payload) {
					if orig := binary.LittleEndian.Uint32(payload[hh.offset : hh.offset+4]); orig != animMaskID {
						binary.LittleEndian.PutUint32(raw[payloadOffset+hh.offset:payloadOffset+hh.offset+4], animMaskID)
						modified = true
						e.countModify()
					}
				}
				continue // other player's non-edit-list cast anim-masked
			}
		}

		if filtering {
			if caster == 0 || !e.tracker.isMine(caster) {
				continue // not our character
			}
		}

		// Engine-level caster filter (manual). The caster id is entered by the user
		// in the UI — there is no auto-detect, so the engine emits no re-lock or
		// observed-caster signal. When a filter is set, ignore every other caster: no
		// log line, no cast event, no combat-speed edit. Only drop when the caster is
		// KNOWN (non-zero) and confidently different — extractEntityKey occasionally
		// fails to read a clean key on rapidly coalesced casts (spamming several ACTs
		// into one TCP segment), returning 0. Dropping those would silently skip the
		// speed edit on some of your own spam casts, so a 0 (unknown) caster is given
		// the benefit of the doubt and processed.
		if casterFilter != 0 && caster != 0 && caster != casterFilter {
			if sessionCh != nil && is02 {
				e.recordCast(sessionCh, fmt.Sprintf(`"id":%d,"caster":%d,"result":"dropped-caster-filter"`, hh.id, caster))
			}
			continue
		}

		// Caster (entity) + skill id suffix for every log line.
		casterOnly := fmt.Sprintf(" [caster:%d, id:%d]", caster, hh.id)

		cfg, isConfigured := lookup[hh.id]
		name := idToName[hh.id]
		if name == "" && isConfigured {
			name = cfg.name
		}
		if name == "" {
			name = fmt.Sprintf("ID:%d", hh.id)
		}

		// Ping = your cast request (last data outbound on this connection) → your
		// own cast confirmation (the 0x02 ACT that carries combat speed). Measured
		// once per packet; the request is consumed so the next cast pairs with its own.
		if is02 && !pinged {
			pinged = true
			if t, ok := e.lastReqByConn[dstPort]; ok {
				ms := int((now - t) / int64(time.Millisecond))
				if ms > 0 && ms <= 3000 {
					e.updatePing(ms)
				}
				// Pair the outbound request with the cast it produced so we can
				// decode the request format (shown in the inspector when on).
				if inspect {
					if req, ok2 := e.lastReqPkt[dstPort]; ok2 {
						e.emitInspect(req, fmt.Sprintf("req→%s (%dms)", name, ms), -1)
					}
				}
				delete(e.lastReqByConn, dstPort)
				delete(e.lastReqPkt, dstPort)
			}
		}

		if inspect && !inspectAll {
			e.emitInspect(payload, "cast "+name, hh.offset)
		}

		// Full field dump of the original server-response cast (before any edit),
		// plus a raw hex window of the cast region for spotting undecoded fields.
		if decode {
			e.emitLog(describeCast(payload, hh.offset, idToName))
			e.emitLog(annotateCast(payload, hh.offset, idToName))
		}

		// Locate the attack-speed field for configured skills.
		var spdOff, spdLen int
		var spdVal uint64
		var isFloat, spdFound bool
		if isConfigured {
			spdOff, spdLen, spdVal, isFloat, spdFound = findAttackSpeedOffset(payload, hh.offset)
		}

		// Session recorder: annotate each configured ACT with the edit decision so
		// offline analysis can tell which casts actually edited vs were missed at
		// runtime (the raw-packet line only shows pre-edit bytes). willEdit mirrors
		// the edit condition in the block below; spdRel is the speed's offset from
		// the skill id (-1 if not found).
		if sessionCh != nil && isConfigured && is02 {
			spdRel := -1
			if spdFound {
				spdRel = spdOff - hh.offset
			}
			e.recordCast(sessionCh, fmt.Sprintf(
				`"id":%d,"caster":%d,"spdFound":%t,"spdRel":%d,"spdVal":%d,"float":%t,"willEdit":%t,"result":"processed"`,
				hh.id, caster, spdFound, spdRel, spdVal, isFloat, spdFound && spdVal >= 10000))
		}

		// Modify configured skills' combat speed (using the speed field located
		// on the original packet above).
		if isConfigured {
			if skipSpeedEditForStability(name, hh.id) {
				if isHellfireName(name) && is02 {
					e.auditHellfire(fmt.Sprintf("SKIP_STABILITY name=%q id=%d caster=%d", name, hh.id, caster), payload, hh.offset)
				}
				continue
			}
			if spdFound && spdVal >= 10000 {
				rawOff := payloadOffset + spdOff
				if cfg.brk {
					if isHellfireName(name) {
						e.auditHellfire(fmt.Sprintf("EDIT name=%q id=%d speed=%d target=break caster=%d", name, hh.id, spdVal, caster), payload, hh.offset)
					}
					// "Break" = max out the speed. Float-encoded skills (Hellfire
					// base) get the largest in-range float; varint skills get the
					// classic all-0xFF fill.
					if isFloat {
						binary.LittleEndian.PutUint32(raw[rawOff:rawOff+4], math.Float32bits(float32(9999999.0/10000.0)))
					} else {
						for j := 0; j < spdLen; j++ {
							raw[rawOff+j] = 0xFF
						}
					}
					modified = true
					e.countModify()
					e.emitLog(fmt.Sprintf("ACT %s %d break [Caster: %d, Id: %d]", name, spdVal, caster, hh.id))
					continue
				}
				// Bonus added on top of the skill's existing speed: from the
				// ping curve in auto-mode, else the per-skill field (×100).
				// Manual per-skill value wins; otherwise the auto curve in auto-mode.
				added := uint64(cfg.speedPct) * 100
				if autoMode && !cfg.override {
					a := autoBase + autoPerMs*e.pingEWMA
					if a < 0 {
						a = 0
					}
					added = uint64(a + 0.5)
				}
				target := spdVal + added
				if isHellfireName(name) {
					e.auditHellfire(fmt.Sprintf("EDIT name=%q id=%d speed=%d add=%d target=%d float=%t caster=%d", name, hh.id, spdVal, added, target, isFloat, caster), payload, hh.offset)
				}
				// Float-encoded skills (Hellfire base) carry speed as a 4-byte float
				// holding value/10000 — write it back in the same form.
				if isFloat {
					if target > 9999999 {
						target = 9999999
					}
					binary.LittleEndian.PutUint32(raw[rawOff:rawOff+4], math.Float32bits(float32(float64(target)/10000.0)))
					modified = true
					e.countModify()
					e.emitLog(fmt.Sprintf("ACT %s %d +%d [Caster: %d, Id: %d]", name, spdVal, added, caster, hh.id))
					continue
				}
				encoded := encodeVarintFixed(target, spdLen)
				if len(encoded) != spdLen {
					maxVal := (uint64(1) << (7 * uint(spdLen))) - 1
					if target > maxVal {
						target = maxVal
					}
					copy(raw[rawOff:rawOff+spdLen], encodeVarintFixed(target, spdLen))
					modified = true
					e.countModify()
					e.emitLog(fmt.Sprintf("ACT %s %d +%d capped [Caster: %d, Id: %d]", name, spdVal, added, caster, hh.id))
					continue
				}
				copy(raw[rawOff:rawOff+spdLen], encoded)
				modified = true
				e.countModify()
				e.emitLog(fmt.Sprintf("ACT %s %d +%d [Caster: %d, Id: %d]", name, spdVal, added, caster, hh.id))
				continue
			}
		}

		if isConfigured && is02 && isHellfireName(name) {
			switch {
			case !spdFound:
				e.auditHellfire(fmt.Sprintf("NO_SPEED name=%q id=%d caster=%d", name, hh.id, caster), payload, hh.offset)
			case spdVal < 10000:
				e.auditHellfire(fmt.Sprintf("LOW_SPEED name=%q id=%d speed=%d caster=%d", name, hh.id, spdVal, caster), payload, hh.offset)
			}
		}

		// Otherwise just log the cast (deduped per skill + action tick). Include the
		// packet_type so it's easy to spot buff (0x00) vs ACT (0x02) casts in the log.
		tick := byte(0)
		if hh.offset+4 < len(payload) {
			tick = payload[hh.offset+4]
		}
		pt := byte(0xff)
		if hh.offset+5 < len(payload) {
			pt = payload[hh.offset+5]
		}
		if e.shouldLogUsed(hh.id, tick) {
			e.emitLog(fmt.Sprintf("Used %s%s [pt: %d]", name, casterOnly, pt))
		}
	}
}

func (e *Engine) countModify() {
	e.fire("capture:count", atomic.AddUint64(&e.modified, 1))
}

// ── filter + helpers ──────────────────────────────────────────

// buildFilter captures BOTH directions: server→client responses (SrcPort, the
// data we modify/log, ≥40 bytes) and client→server requests (DstPort, small —
// used only to timestamp skill use for RTT).
func buildFilter(ports []int, loopback bool) string {
	src := orPorts(ports, "tcp.SrcPort")
	dst := orPorts(ports, "tcp.DstPort")

	if loopback {
		if len(ports) == 0 {
			return "loopback and tcp and !impostor and tcp.SrcPort > 1024 and tcp.PayloadLength >= 40"
		}
		return fmt.Sprintf(
			"loopback and tcp and !impostor and (((%s) and tcp.PayloadLength >= 40) or ((%s) and tcp.PayloadLength >= 4))",
			src, dst)
	}

	const noLoop = "ip.DstAddr != 127.0.0.1 and ip.SrcAddr != 127.0.0.1"
	if len(ports) == 0 {
		return "inbound and tcp and tcp.DstPort > 1024 and tcp.SrcPort > 1024 and " + noLoop + " and tcp.PayloadLength >= 40"
	}
	return fmt.Sprintf(
		"tcp and %s and ((inbound and (%s) and tcp.PayloadLength >= 40) or (outbound and (%s) and tcp.PayloadLength >= 4))",
		noLoop, src, dst)
}

func orPorts(ports []int, field string) string {
	if len(ports) == 0 {
		return field + " > 1024"
	}
	clauses := make([]string, len(ports))
	for i, p := range ports {
		clauses[i] = fmt.Sprintf("%s == %d", field, p)
	}
	return strings.Join(clauses, " or ")
}

func adminHint(err error) string {
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "access") || strings.Contains(low, "denied") ||
		strings.Contains(low, "elevat") || strings.Contains(low, "privilege") {
		return "Run as Administrator — WinDivert needs elevation"
	}
	return "Capture failed to open: " + err.Error()
}

func fmtPorts(ports []int) string {
	s := make([]string, len(ports))
	for i, p := range ports {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ", ")
}
