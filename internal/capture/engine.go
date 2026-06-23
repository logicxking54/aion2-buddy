//go:build windows

package capture

import (
	"bufio"
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
	if _, nx, ok := findTransformNextPos(data, skillOffset); ok {
		fmt.Fprintf(&b, " rec[cast:%s next:%s]", fmtID(sid, names), fmtID(nx, names))
	}
	return b.String()
}

// SkillSpeed is one configured skill from the UI: its in-game IDs (all
// variants), the target combat-speed % to write, and the break flag.
type SkillSpeed struct {
	Name     string   `json:"name"`
	IDs      []uint32 `json:"ids"`
	SpeedPct int      `json:"speedPct"`
	Break    bool     `json:"break"`
	Override bool     `json:"override"` // use SpeedPct even in auto-mode
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
	tails         map[flowKey]flowTail // previous inbound bytes per server flow, for split cast recovery
	curPorts      map[int]struct{}     // server/proxy ports of the active handle (for direction)
	lastReqByConn map[int]int64        // client port -> last outbound time (ns), for RTT
	lastReqPkt    map[int][]byte       // client port -> last outbound payload, for request decoding
	autoMode      bool                 // compute bonus from ping instead of per-skill field
	autoBase      float64              // packet add at 0ms ping
	autoPerMs     float64              // extra packet add per ms of ping
	pingEWMA      float64              // smoothed ping (ms); written only by packet goroutine
	pingHasValue  bool
	lastPingEmit  int64
	inspect       bool              // packet inspector enabled
	inspectAll    bool              // true = dump every inbound packet; false = only skill casts
	inspectCount  int64             // emitted-message counter (capped)
	comboTest     bool              // experimental combo-chain next-id rewrite
	comboMap      map[uint32]uint32 // source skill family (id/10000) -> exact target id (nextId rewrite)
	comboCastMap  map[uint32]uint32 // source skill family -> exact target id (castId rewrite experiment)
	comboSkillMap map[uint32]uint32 // source skill family -> exact target id (main skill_id rewrite)
	decode        bool              // print all decoded fields for each cast
	aggressive    bool              // allow compact/framed speed fallback matches
	maskOn        bool              // FPS mask: rewrite other players' cast skill_id to Dodge
	maskKeep      uint64            // your caster (entity key) left untouched; 0 = not locked yet
	maskDodge     uint32            // skill_id written over masked casts (a no-VFX Dodge id)
	casterFilter  uint64            // engine-level caster filter: only this caster is processed; 0 = all
	running       bool

	stop       chan struct{}
	ports      *portTracker
	curHandle  handle
	handleMu   sync.Mutex
	missLogMu  sync.Mutex
	auditLogMu sync.Mutex
	auditCh    chan hellfireAuditEntry
	auditWG    sync.WaitGroup
	wg         sync.WaitGroup

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
		tails:         map[flowKey]flowTail{},
		curPorts:      map[int]struct{}{},
		lastReqByConn: map[int]int64{},
		lastReqPkt:    map[int][]byte{},
		autoBase:      6800,
		autoPerMs:     80,
	}
}

const inspectCap = 500 // stop emitting after this many messages until re-enabled

const splitTailMax = 192

type flowKey struct {
	src int
	dst int
}

type flowTail struct {
	data    []byte
	nextSeq uint32
}

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

// comboNextExperiment rewrites the nextId (chain follow-up) in a cast's trailing
// record to an EXACT skill id. Each entry: when castSkill is cast, its record's
// nextId is overwritten with toID.
var comboNextExperiment = []struct {
	castSkill string
	toID      uint32
}{
	{"Burst", 15090240},     // Burst's next -> Ice Chain (exact, rank 0240)
	{"Ice Chain", 15250240}, // Ice Chain's next -> Pyroclasm (exact, rank 0240)
	{"Pyroclasm", 15210240}, // Pyroclasm's next -> Flame Arrow (exact, rank 0240)
}

// comboCastIdExperiment rewrites the castId LABEL inside a cast's trailing record
// to an EXACT skill id (not a family/tier swap — the precise id is written). Used
// to observe how the client reacts to a mislabelled record. Each entry: when
// castSkill is cast, its record's castId is overwritten with toID.
var comboCastIdExperiment = []struct {
	castSkill string
	toID      uint32
}{
	// (none) — castId stays the cast's own skill; only nextId edits are active.
}

// comboSkillIdExperiment rewrites the cast's MAIN skill_id (at skillOffset, the
// id that identifies the cast itself — not the trailing record) to an EXACT id.
// Each entry: when castSkill is cast, the server-response skill_id is overwritten
// with toID. Applied AFTER speed detection and the trailing-record edits (both of
// which key off the original id), so it doesn't disturb them.
var comboSkillIdExperiment = []struct {
	castSkill string
	toID      uint32
}{
	// (none) — no main skill_id edits.
}

// SetComboTest toggles the experimental combo-chain rewrite. When on, a chain
// cast's trailing "next form" id is rewritten to point at the next skill in
// comboTestChain (same tier), changing which skill the client offers as the
// follow-up. It can only REDIRECT an existing chain record — it cannot create a
// combo button where the packet carries none. Requires the catalog to be loaded
// (SetCatalog) so skill names resolve to id families.
func (e *Engine) SetComboTest(on bool) {
	e.mu.Lock()
	e.comboTest = on
	// nextId experiment: source skill family -> EXACT target id to write.
	m := map[uint32]uint32{}
	for _, ex := range comboNextExperiment {
		if src, ok := e.familyOfLocked(ex.castSkill); ok {
			m[src] = ex.toID
		}
	}
	e.comboMap = m
	// castId experiment: source skill family -> EXACT target id to write.
	cm := map[uint32]uint32{}
	for _, ex := range comboCastIdExperiment {
		if src, ok := e.familyOfLocked(ex.castSkill); ok {
			cm[src] = ex.toID
		}
	}
	e.comboCastMap = cm
	// main skill_id experiment: source skill family -> EXACT target id to write.
	sm := map[uint32]uint32{}
	for _, ex := range comboSkillIdExperiment {
		if src, ok := e.familyOfLocked(ex.castSkill); ok {
			sm[src] = ex.toID
		}
	}
	e.comboSkillMap = sm
	names := e.idToName
	e.mu.Unlock()
	if !on {
		e.emitLog("Combo-chain TEST OFF")
		return
	}
	for _, ex := range comboNextExperiment {
		e.emitLog(fmt.Sprintf("Combo-chain TEST ON, next edit: %s record next -> %s", ex.castSkill, fmtID(ex.toID, names)))
	}
	for _, ex := range comboCastIdExperiment {
		e.emitLog(fmt.Sprintf("Combo-chain TEST ON, castId edit: %s record castId -> %s", ex.castSkill, fmtID(ex.toID, names)))
	}
	for _, ex := range comboSkillIdExperiment {
		e.emitLog(fmt.Sprintf("Combo-chain TEST ON, skillId edit: %s cast skill_id -> %s", ex.castSkill, fmtID(ex.toID, names)))
	}
	if len(m) == 0 && len(cm) == 0 && len(sm) == 0 {
		e.emitLog("Combo-chain TEST ON (no edits configured)")
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

// SetCasterFilter sets the engine-level caster filter. When id != 0, only casts
// from that caster (entity key) are processed — every other caster is ignored
// entirely: no log line, no cast event, and no combat-speed edit. id == 0 clears
// the filter (process all casters). To keep the auto filter able to re-lock when
// your caster id changes, the engine still emits a pre-filter "capture:act" event
// for your own configured casts, so the frontend can learn a new caster and push
// it back here. The frontend re-calls this whenever the (auto/manual) field value
// changes, so this stays quiet — no logging on every update.
func (e *Engine) SetCasterFilter(id uint64) {
	e.mu.Lock()
	e.casterFilter = id
	e.mu.Unlock()
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

// SetAggressiveParser toggles compact/framed fallback speed matching.
func (e *Engine) SetAggressiveParser(on bool) {
	e.mu.Lock()
	was := e.aggressive
	e.aggressive = on
	e.mu.Unlock()
	if was != on {
		if on {
			e.emitLog("Aggressive parser ON")
		} else {
			e.emitLog("Aggressive parser OFF")
		}
	}
}

// replaceU32After replaces every little-endian uint32 occurrence of oldVal with
// newVal in raw, scanning payload from `from` to the end (payload aliases raw, so
// edits are visible to the scan and won't re-match). Returns how many it changed.
// Used by the combo "change everywhere" pass to catch the next-skill id wherever
// trailing sub-records reference it, not just in the next-form record.
func replaceU32After(raw, payload []byte, payloadOffset, from int, oldVal, newVal uint32) int {
	var ob [4]byte
	binary.LittleEndian.PutUint32(ob[:], oldVal)
	n := 0
	for i := from; i+4 <= len(payload); i++ {
		if payload[i] == ob[0] && payload[i+1] == ob[1] && payload[i+2] == ob[2] && payload[i+3] == ob[3] {
			binary.LittleEndian.PutUint32(raw[payloadOffset+i:payloadOffset+i+4], newVal)
			n++
			i += 3 // step past this match
		}
	}
	return n
}

// familyOfLocked returns the id family (id/10000) of the named skill, found from
// the loaded catalog. Caller must hold e.mu. All variants of one skill share a
// family (e.g. every Burst id is 1503xxxx), so the first match is enough.
func (e *Engine) familyOfLocked(name string) (uint32, bool) {
	for id, n := range e.idToName {
		if n == name {
			return id / 10000, true
		}
	}
	return 0, false
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

func missLogPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "aion2-buddy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "pingmaker-misses.log"), nil
}

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

// emitMiss writes only failed edits to disk. Packet hex is included so rare
// layouts can be reproduced without enabling the high-volume inspector.
func (e *Engine) emitMiss(msg string, payload []byte, skillOffset int) {
	e.emitLog(msg)
	low := strings.ToLower(msg)
	if !strings.Contains(low, "hellfire") && !strings.Contains(low, "aggressive_candidate") {
		return
	}
	path, err := missLogPath()
	if err != nil {
		e.emitLog("MISS log error: " + err.Error())
		return
	}
	e.missLogMu.Lock()
	defer e.missLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		e.emitLog("MISS log error: " + err.Error())
		return
	}
	_, writeErr := fmt.Fprintf(f, "%s | %s | offset=%d len=%d hex=%s\n",
		time.Now().Format(time.RFC3339Nano), msg, skillOffset, len(payload), hex.EncodeToString(payload))
	closeErr := f.Close()
	if writeErr != nil {
		e.emitLog("MISS log error: " + writeErr.Error())
	} else if closeErr != nil {
		e.emitLog("MISS log error: " + closeErr.Error())
	}
}

// SetConfig rebuilds the speed lookup (and a config-only scan fallback) from
// the UI config.
func (e *Engine) SetConfig(skills []SkillSpeed) {
	ids := map[uint32]struct{}{}
	fb := map[byte]struct{}{}
	lk := map[uint32]skillCfg{}
	for _, s := range skills {
		for _, id := range s.IDs {
			ids[id] = struct{}{}
			fb[byte(id&0xFF)] = struct{}{}
			lk[id] = skillCfg{name: s.Name, speedPct: s.SpeedPct, brk: s.Break, override: s.Override}
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
	e.tails = map[flowKey]flowTail{}
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
		e.tails = map[flowKey]flowTail{}

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
	comboTest := e.comboTest
	comboMap := e.comboMap
	comboCastMap := e.comboCastMap
	comboSkillMap := e.comboSkillMap
	decode := e.decode
	aggressive := e.aggressive
	maskOn := e.maskOn
	maskKeep := e.maskKeep
	maskDodge := e.maskDodge
	casterFilter := e.casterFilter
	e.mu.Unlock()
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
	key := flowKey{src: srcPort, dst: dstPort}
	if seq, seqOK := tcpSequence(raw); seqOK {
		defer e.updateSplitTail(key, payload, seq)
		if e.recoverSplitCasts(raw, payload, payloadOffset, key, seq, scanIDs, scanFB, lookup, idToName, autoMode, autoBase, autoPerMs, casterFilter, aggressive) {
			modified = true
		}
	}
	if len(payload) < 40 {
		return
	}

	// Entity learning: feed the stream and lock onto the player's character key.
	for _, b := range e.reasm.feed(payload) {
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

	hits := findAllSkillIDs(payload, scanIDs, scanFB, scanStartDefault)
	if len(hits) == 0 {
		return
	}

	filtering := e.tracker.isConfigured()
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

		if filtering {
			if caster == 0 || !e.tracker.isMine(caster) {
				continue // not our character
			}
		}

		// Engine-level caster filter. Pre-filter re-lock signal first: emit your own
		// ACT (a real 0x02 cast of a configured skill) so the frontend's auto filter
		// can follow you when your caster id changes — even while every other caster
		// is suppressed below.
		//
		// Gate on isMine: only signal for a caster we've CONFIRMED is yours (its
		// entity key matches the learned character binding). Without a configured
		// character name we can't tell whose cast this is, so we must not auto-lock —
		// otherwise the frontend filter could latch onto a nearby player and then
		// suppress your own casts, producing an empty log (no ACT lines at all). No
		// name configured => isMine always false => no auto-lock => every cast logs.
		if is02 && e.tracker.isMine(caster) {
			if _, ok := lookup[hh.id]; ok {
				e.fire("capture:act", caster)
			}
		}
		// When a filter is set, ignore every other caster: no log line, no cast
		// event, no combat-speed edit. Only drop when the caster is KNOWN (non-zero)
		// and confidently different — extractEntityKey occasionally fails to read a
		// clean key on rapidly coalesced casts (spamming several ACTs into one TCP
		// segment), returning 0. Dropping those would silently skip the speed edit on
		// some of your own spam casts, so a 0 (unknown) caster is given the benefit of
		// the doubt and processed.
		if casterFilter != 0 && caster != 0 && caster != casterFilter {
			continue
		}
		if is02 && caster != 0 {
			e.fire("capture:caster", caster)
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

		// Structured cast event for transform-cycle tracking in the UI: the
		// cast's skill id + the "next form" the shared slot becomes (0 if the
		// skill isn't a transform skill). Set-based on the UI side, so safe to
		// re-emit on combos/retransmits.
		e.fire("capture:cast", map[string]any{"id": hh.id, "next": findTransformNext(payload, hh.offset)})

		if inspect && !inspectAll {
			e.emitInspect(payload, "cast "+name, hh.offset)
		}

		// Full field dump of the original server-response cast (before any edit),
		// plus a raw hex window of the cast region for spotting undecoded fields.
		if decode {
			e.emitLog(describeCast(payload, hh.offset, idToName))
			e.emitLog(annotateCast(payload, hh.offset, idToName))
		}

		// Locate the attack-speed field on the ORIGINAL packet, BEFORE the combo
		// block runs. The combo castId rewrite erases the family bytes that
		// findAttackSpeedOffset validates against, so detecting here keeps the
		// speed edit working for a skill whose castId we relabel. The field's
		// offset is unchanged by the combo edit (which only touches the trailing
		// record), so applying it to raw afterward is still correct.
		var spdOff, spdLen int
		var spdVal uint64
		var isFloat, spdFound, spdFallback bool
		if isConfigured {
			spdOff, spdLen, spdVal, isFloat, spdFound, spdFallback = findAttackSpeedOffset(payload, hh.offset, aggressive)
		}

		// Combo-chain test: rewrite this cast's trailing record
		// (`0f 18 38 <castId:4> 01 <nextId:4>`) in place. Two independent edits:
		//   nextId  — redirect the chain follow-up to another skill (same tier).
		//   castId  — relabel which skill the record belongs to (experiment).
		// Runs before the speed edit (which `continue`s for configured skills).
		// Both nextId and castId are EXACT id writes (the precise target id).
		// recCast/recNext capture the record's OUTGOING values so the rec[…] log
		// suffix is accurate even after a castId relabel (which would otherwise
		// make the record unfindable by this skill's id).
		var recCast, recNext uint32
		recHave := false
		if comboTest {
			fam := hh.id / 10000
			nextTargetID, doNext := comboMap[fam]
			castTargetID, doCast := comboCastMap[fam]
			if doNext || doCast {
				if pos, oldNext, found := findTransformNextPos(payload, hh.offset); found {
					// Record layout `0f 18 38 <castId:4> 01 <nextId:4>`: castId
					// starts 5 bytes before nextId (4 id bytes + the 0x01 marker).
					castPos := pos - 5
					oldCast := uint32(0)
					if castPos >= 0 {
						oldCast = binary.LittleEndian.Uint32(payload[castPos : castPos+4])
					}
					newNext, newCast := oldNext, oldCast
					extraRefs := 0
					if doNext {
						newNext = nextTargetID // exact target id, written verbatim
						binary.LittleEndian.PutUint32(raw[payloadOffset+pos:payloadOffset+pos+4], newNext)
						// "Change everywhere": replace any OTHER references to the old
						// next-skill id in the rest of the packet (the next-form record
						// at pos is already newNext, so it won't re-match).
						if newNext != oldNext {
							extraRefs = replaceU32After(raw, payload, payloadOffset, hh.offset, oldNext, newNext)
						}
					}
					if doCast && castPos >= 0 {
						newCast = castTargetID // exact target id, written verbatim
						binary.LittleEndian.PutUint32(raw[payloadOffset+castPos:payloadOffset+castPos+4], newCast)
					}
					recCast, recNext, recHave = newCast, newNext, true
					if newNext != oldNext || newCast != oldCast {
						modified = true
						e.countModify()
						extra := ""
						if extraRefs > 0 {
							extra = fmt.Sprintf(" (+%d more next-refs)", extraRefs)
						}
						// Show the whole record before and after the edit.
						e.emitLog(fmt.Sprintf("Combo %s edited: was[cast:%s next:%s] now[cast:%s next:%s]%s%s",
							name, fmtID(oldCast, idToName), fmtID(oldNext, idToName),
							fmtID(newCast, idToName), fmtID(newNext, idToName), extra, casterOnly))
					}
				} else {
					e.emitLog(fmt.Sprintf("Combo %s: no chain record in packet (nothing to rewrite)%s", name, casterOnly))
				}
			}
		}

		// rec[…] reflects the OUTGOING packet (what the client receives). Prefer the
		// values the combo block captured (accurate even after a castId relabel);
		// otherwise re-locate the record by this skill's id (unedited skills).
		casterStr := casterOnly
		if !recHave {
			if rpos, rnext, rok := findTransformNextPos(payload, hh.offset); rok && rpos >= 5 {
				recCast, recNext, recHave = binary.LittleEndian.Uint32(payload[rpos-5:rpos-1]), rnext, true
			}
		}
		if recHave {
			casterStr += fmt.Sprintf(" rec[cast:%s next:%s]", fmtID(recCast, idToName), fmtID(recNext, idToName))
		}

		// Main skill_id rewrite: change the id that identifies this cast itself.
		// Done LAST (after speed detection + trailing-record edits, which key off
		// the original id) and before the speed block so its `continue` can't skip
		// it. The speed field's offset is unaffected (it's located on the original).
		if comboTest && hh.offset+4 <= len(payload) {
			if newSkill, ok := comboSkillMap[hh.id/10000]; ok && newSkill != hh.id {
				binary.LittleEndian.PutUint32(raw[payloadOffset+hh.offset:payloadOffset+hh.offset+4], newSkill)
				modified = true
				e.countModify()
				e.emitLog(fmt.Sprintf("Combo %s: skillId %s => %s%s", name, fmtID(hh.id, idToName), fmtID(newSkill, idToName), casterOnly))
			}
		}

		// Diagnostic: mark edits that the fixed parser missed and the fallback
		// scanner recovered, so a dense-fight log shows how much load the fallback
		// is carrying (lots of "(fb)" => the standard layout is breaking).
		if isConfigured && spdFound && spdFallback {
			casterStr += " (fb)"
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
						e.auditHellfire(fmt.Sprintf("EDIT name=%q id=%d speed=%d target=break float=%t fallback=%t caster=%d", name, hh.id, spdVal, isFloat, spdFallback, caster), payload, hh.offset)
					}
					// "Break" = max out the speed. Charge skills store a float
					// (value/10000), so write the largest in-range float there;
					// varint skills get the classic all-0xFF fill.
					if isFloat {
						binary.LittleEndian.PutUint32(raw[rawOff:rawOff+4], math.Float32bits(float32(9999999.0/10000.0)))
					} else {
						for j := 0; j < spdLen; j++ {
							raw[rawOff+j] = 0xFF
						}
					}
					modified = true
					e.countModify()
					e.emitLog(fmt.Sprintf("ACT %s spd:%d -> break%s", name, spdVal, casterStr))
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
					e.auditHellfire(fmt.Sprintf("EDIT name=%q id=%d speed=%d add=%d target=%d float=%t fallback=%t caster=%d", name, hh.id, spdVal, added, target, isFloat, spdFallback, caster), payload, hh.offset)
				}
				// Charge skills (e.g. Hellfire) carry the speed as a 4-byte float
				// holding value/10000 — write it back in the same form.
				if isFloat {
					if target > 9999999 {
						target = 9999999
					}
					binary.LittleEndian.PutUint32(raw[rawOff:rawOff+4], math.Float32bits(float32(float64(target)/10000.0)))
					modified = true
					e.countModify()
					e.emitLog(fmt.Sprintf("ACT %s spd:%d +%d -> %d%s", name, spdVal, added, target, casterStr))
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
					e.emitLog(fmt.Sprintf("ACT %s spd:%d +%d -> capped%s", name, spdVal, added, casterStr))
					continue
				}
				copy(raw[rawOff:rawOff+spdLen], encoded)
				modified = true
				e.countModify()
				e.emitLog(fmt.Sprintf("ACT %s spd:%d +%d -> %d%s", name, spdVal, added, target, casterStr))
				continue
			}
		}

		// Diagnostic: a configured skill's real 0x02 cast that we could NOT edit.
		// Distinguish the two root causes so the log says which fix is needed:
		//   truncated/segmented — the speed field can't physically fit in what's
		//     left of this packet, i.e. the cast was split across TCP segments
		//     (needs a carry-over buffer; widening the scanner can't help).
		//   layout-unparsed — the record is fully present but neither the fixed
		//     parser nor the fallback walked it (improve findSpeedSearch).
		if isConfigured && is02 && !spdFound {
			reason := "layout-unparsed"
			if hh.offset+6+16+4 > len(payload) {
				reason = "truncated/segmented"
			}
			if aggressive && isHellfireName(name) {
				if candOff, candLen, candVal, candFloat, candKind, candOK := findRiskySpeedCandidate(payload, hh.offset); candOK {
					post := candOff + candLen
					trailerEnd := post + 16
					if trailerEnd > len(payload) {
						trailerEnd = len(payload)
					}
					trailerHex := ""
					if post < trailerEnd {
						trailerHex = hex.EncodeToString(payload[post:trailerEnd])
					}
					e.emitMiss(fmt.Sprintf("AGGRESSIVE_CANDIDATE %s kind=%s off=%d rel=%d len=%d val=%d float=%t trailer=%s%s", name, candKind, candOff, candOff-hh.offset, candLen, candVal, candFloat, trailerHex, casterStr), payload, hh.offset)
					reason = "risky-candidate"
				}
			}
			e.emitMiss(fmt.Sprintf("MISS %s (%s)%s", name, reason, casterStr), payload, hh.offset)
		}
		if isConfigured && is02 && isHellfireName(name) {
			switch {
			case !spdFound:
				e.auditHellfire(fmt.Sprintf("NO_SPEED name=%q id=%d caster=%d", name, hh.id, caster), payload, hh.offset)
			case spdVal < 10000:
				e.auditHellfire(fmt.Sprintf("LOW_SPEED name=%q id=%d speed=%d float=%t fallback=%t caster=%d", name, hh.id, spdVal, isFloat, spdFallback, caster), payload, hh.offset)
			}
		}

		// Otherwise just log the cast (deduped per skill + action tick).
		tick := byte(0)
		if hh.offset+4 < len(payload) {
			tick = payload[hh.offset+4]
		}
		if e.shouldLogUsed(hh.id, tick) {
			e.emitLog("Used " + name + casterStr)
		}
	}
}

func (e *Engine) countModify() {
	e.fire("capture:count", atomic.AddUint64(&e.modified, 1))
}

func (e *Engine) updateSplitTail(key flowKey, payload []byte, seq uint32) {
	if len(payload) == 0 {
		return
	}
	if e.tails == nil {
		e.tails = map[flowKey]flowTail{}
	}
	prev := e.tails[key]
	if len(prev.data) == 0 || prev.nextSeq != seq {
		prev.data = append(prev.data[:0], payload...)
	} else {
		prev.data = append(prev.data, payload...)
	}
	if len(prev.data) > splitTailMax {
		start := len(prev.data) - splitTailMax
		prev.data = append(prev.data[:0], prev.data[start:]...)
	}
	prev.nextSeq = seq + uint32(len(payload))
	e.tails[key] = prev
	if len(e.tails) > 64 {
		for k := range e.tails {
			if k != key {
				delete(e.tails, k)
				if len(e.tails) <= 64 {
					break
				}
			}
		}
	}
}

// recoverSplitCasts edits a configured cast whose skill id landed in the
// previous TCP segment while its speed field landed in this segment. Because the
// edit is same-length and only touches bytes in the current packet, TCP sequence
// numbers and packet sizes remain unchanged.
func (e *Engine) recoverSplitCasts(raw []byte, payload []byte, payloadOffset int, key flowKey, seq uint32, scanIDs map[uint32]struct{}, scanFB map[byte]struct{}, lookup map[uint32]skillCfg, idToName map[uint32]string, autoMode bool, autoBase, autoPerMs float64, casterFilter uint64, aggressive bool) bool {
	state := e.tails[key]
	tail := state.data
	if len(tail) == 0 || len(payload) == 0 || state.nextSeq != seq {
		return false
	}
	combined := make([]byte, 0, len(tail)+len(payload))
	combined = append(combined, tail...)
	combined = append(combined, payload...)
	hits := findAllSkillIDs(combined, scanIDs, scanFB, 0)
	if len(hits) == 0 {
		return false
	}

	modified := false
	tailLen := len(tail)
	for _, hh := range hits {
		if hh.offset >= tailLen || hh.offset+5 >= len(combined) {
			continue
		}
		if !hh.prefixOK || combined[hh.offset+5] != 0x02 {
			continue
		}
		cfg, isConfigured := lookup[hh.id]
		if !isConfigured {
			continue
		}
		caster := extractEntityKey(combined, hh.offset)
		if e.tracker.isConfigured() && (caster == 0 || !e.tracker.isMine(caster)) {
			continue
		}
		if casterFilter != 0 && caster != 0 && caster != casterFilter {
			continue
		}
		spdOff, spdLen, spdVal, isFloat, spdFound, spdFallback := findAttackSpeedOffset(combined, hh.offset, aggressive)
		if !spdFound || spdVal < 10000 {
			continue
		}
		if spdOff < tailLen || spdOff+spdLen > len(combined) {
			continue
		}
		curOff := spdOff - tailLen
		rawOff := payloadOffset + curOff
		name := idToName[hh.id]
		if name == "" {
			name = cfg.name
		}
		if name == "" {
			name = fmt.Sprintf("ID:%d", hh.id)
		}
		if skipSpeedEditForStability(name, hh.id) {
			if isHellfireName(name) {
				e.auditHellfire(fmt.Sprintf("SPLIT_SKIP_STABILITY name=%q id=%d caster=%d", name, hh.id, caster), combined, hh.offset)
			}
			continue
		}
		suffix := fmt.Sprintf(" [split, caster:%d, id:%d]", caster, hh.id)
		if spdFallback {
			suffix += " (fb)"
		}

		if cfg.brk {
			if isHellfireName(name) {
				e.auditHellfire(fmt.Sprintf("SPLIT_EDIT name=%q id=%d speed=%d target=break float=%t fallback=%t caster=%d", name, hh.id, spdVal, isFloat, spdFallback, caster), combined, hh.offset)
			}
			if isFloat {
				binary.LittleEndian.PutUint32(raw[rawOff:rawOff+4], math.Float32bits(float32(9999999.0/10000.0)))
			} else {
				for j := 0; j < spdLen; j++ {
					raw[rawOff+j] = 0xFF
				}
			}
			modified = true
			e.countModify()
			e.emitLog(fmt.Sprintf("ACT %s spd:%d -> break%s", name, spdVal, suffix))
			continue
		}

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
			e.auditHellfire(fmt.Sprintf("SPLIT_EDIT name=%q id=%d speed=%d add=%d target=%d float=%t fallback=%t caster=%d", name, hh.id, spdVal, added, target, isFloat, spdFallback, caster), combined, hh.offset)
		}
		if isFloat {
			if target > 9999999 {
				target = 9999999
			}
			binary.LittleEndian.PutUint32(raw[rawOff:rawOff+4], math.Float32bits(float32(float64(target)/10000.0)))
			modified = true
			e.countModify()
			e.emitLog(fmt.Sprintf("ACT %s spd:%d +%d -> %d%s", name, spdVal, added, target, suffix))
			continue
		}
		encoded := encodeVarintFixed(target, spdLen)
		if len(encoded) != spdLen {
			maxVal := (uint64(1) << (7 * uint(spdLen))) - 1
			if target > maxVal {
				target = maxVal
			}
			encoded = encodeVarintFixed(target, spdLen)
			e.emitLog(fmt.Sprintf("ACT %s spd:%d +%d -> capped%s", name, spdVal, added, suffix))
		} else {
			e.emitLog(fmt.Sprintf("ACT %s spd:%d +%d -> %d%s", name, spdVal, added, target, suffix))
		}
		copy(raw[rawOff:rawOff+spdLen], encoded)
		modified = true
		e.countModify()
	}
	return modified
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
			"loopback and tcp and !impostor and (((%s) and tcp.PayloadLength > 0) or ((%s) and tcp.PayloadLength >= 4))",
			src, dst)
	}

	const noLoop = "ip.DstAddr != 127.0.0.1 and ip.SrcAddr != 127.0.0.1"
	if len(ports) == 0 {
		return "inbound and tcp and tcp.DstPort > 1024 and tcp.SrcPort > 1024 and " + noLoop + " and tcp.PayloadLength >= 40"
	}
	return fmt.Sprintf(
		"tcp and %s and ((inbound and (%s) and tcp.PayloadLength > 0) or (outbound and (%s) and tcp.PayloadLength >= 4))",
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
