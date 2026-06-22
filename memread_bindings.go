//go:build windows

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"aion2tmp/internal/memread"
)

// MemRead is the Wails-bound façade over the read-only memory reader. Exported
// methods are callable from the frontend (window.go.main.MemRead.*). It is
// strictly read-only: it can resolve pointer paths and read values, never
// write or inject. Intended for the cooldown-hunting debug panel.
type MemRead struct {
	ctx    context.Context
	reader *memread.Reader
}

func NewMemRead() *MemRead {
	return &MemRead{reader: memread.NewReader()}
}

func (m *MemRead) setContext(ctx context.Context) { m.ctx = ctx }

// Attached reports whether a read-only handle to the game is currently open.
func (m *MemRead) Attached() bool { return m.reader.Attached() }

// Status actively tries to attach and reports why it can't (process not found,
// admin rights, etc.). The UI polls this for its indicator.
func (m *MemRead) Status() memread.Status { return m.reader.Status() }

// SetProcessName points the reader at a specific client exe (empty = defaults).
func (m *MemRead) SetProcessName(name string) { m.reader.SetProcessName(name) }

// ProcessName returns the active target process name(s).
func (m *MemRead) ProcessName() string { return m.reader.ProcessName() }

// ListAionProcesses lists running processes whose name contains "aion" — used
// to discover the real client exe name when the default isn't found.
func (m *MemRead) ListAionProcesses() []memread.ProcInfo {
	p, err := memread.ListProcesses("aion")
	if err != nil {
		return nil
	}
	return p
}

// Detach closes the process handle (e.g. when leaving the debug panel).
func (m *MemRead) Detach() { m.reader.Close() }

// SpecResult is the structured outcome of reading one pointer-path spec. Errors
// are returned in Error (not as a thrown JS error) so the UI can poll on an
// interval without the call rejecting every tick while the game is closed or a
// pointer is momentarily null (e.g. between zone loads).
type SpecResult struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	Module     string `json:"module"`
	RootOffset string `json:"rootOffset"` // hex, for echo/verification
	Offsets    string `json:"offsets"`    // hex csv, for echo/verification
	memread.Value
}

// ReadSpec parses a Cheat-Engine-style pointer path and reads the value at the
// resolved address. Read-only.
//
// Accepted spec forms (offsets separated by "->", "," or whitespace):
//
//	Aion2.exe+0x01A2B3C4                      (static address, no deref)
//	Aion2.exe+0x01A2B3C4 -> 0x10 -> 0x8 -> 0  (multi-level pointer)
//	+0x01A2B3C4 -> 0x18                        (module defaults to Aion2.exe)
//
// IMPORTANT — offset order: enter offsets in the order they are APPLIED, which
// is the REVERSE of Cheat Engine's pointer view (CE lists the last-applied
// offset on top). So if CE shows, top-to-bottom, offsets 0x8 / 0x10 / 0x18
// under base Aion2.exe+0x1A2B3C4, the spec is:
//
//	Aion2.exe+0x1A2B3C4 -> 0x18 -> 0x10 -> 0x8
func (m *MemRead) ReadSpec(spec string) SpecResult {
	module, root, offsets, err := parsePointerSpec(spec)
	if err != nil {
		return SpecResult{OK: false, Error: err.Error()}
	}
	res := SpecResult{
		Module:     module,
		RootOffset: fmt.Sprintf("0x%X", root),
		Offsets:    formatOffsets(offsets),
	}
	val, err := m.reader.ReadValue(module, root, offsets)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	res.Value = val
	return res
}

// --- read-only value scanner (in-app Cheat Engine replacement) ---

// ScanResult reports the candidate count after a scan step.
type ScanResult struct {
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated,omitempty"` // hit the candidate cap
	Error     string `json:"error,omitempty"`
}

// ScanNew starts a fresh Cheat-Engine-style first scan. vtype is one of
// byte/int16/uint16/int32/uint32/int64/uint64/float/double. op is one of
// "unknown" (unknown initial value), "exact", "between", "bigger", "smaller".
func (m *MemRead) ScanNew(vtype, op string, a, b float64) ScanResult {
	n, trunc, err := m.reader.ScanNew(vtype, op, a, b)
	if err != nil {
		return ScanResult{Error: err.Error()}
	}
	return ScanResult{Count: n, Truncated: trunc}
}

// ScanMode returns "" (no scan), "snapshot" (after an unknown first scan,
// before the first narrow), or "list" (an enumerated candidate set).
func (m *MemRead) ScanMode() string { return m.reader.ScanMode() }

// ScanFloatRange is the auto-locator shortcut (float first scan in [min,max]).
func (m *MemRead) ScanFloatRange(min, max float64) ScanResult {
	n, trunc, err := m.reader.ScanFloatRange(float32(min), float32(max))
	if err != nil {
		return ScanResult{Error: err.Error()}
	}
	return ScanResult{Count: n, Truncated: trunc}
}

// ScanNext refines the candidate set. op: "inc" | "dec" | "changed" |
// "unchanged" | "exact" | "between" | "bigger" | "smaller" | "incby" | "decby"
// (exact/bigger/smaller use a; between uses a,b; incby/decby use a).
func (m *MemRead) ScanNext(op string, a, b float64) ScanResult {
	n, err := m.reader.ScanNext(op, a, b)
	if err != nil {
		return ScanResult{Error: err.Error()}
	}
	return ScanResult{Count: n}
}

// ScanCount returns the current candidate count.
func (m *MemRead) ScanCount() int { return m.reader.ScanCount() }

// ScanList returns up to limit candidates (address + current float value).
func (m *MemRead) ScanList(limit int) []memread.ScanHit {
	hits, err := m.reader.ScanList(limit)
	if err != nil {
		return nil
	}
	return hits
}

// ScanReset clears the candidate set.
func (m *MemRead) ScanReset() { m.reader.ScanReset() }

// ScanRemove prunes one candidate address (decimal or 0xHEX) from the list and
// returns the new candidate count.
func (m *MemRead) ScanRemove(addr string) int {
	v, err := parseAddr(addr)
	if err != nil {
		return m.reader.ScanCount()
	}
	return m.reader.ScanRemove(uintptr(v))
}

// BlockResult is a memory-view dump (errors carried in-band so the UI can poll
// without the call rejecting each tick).
type BlockResult struct {
	Base  string           `json:"base"`
	Rows  []memread.MemRow `json:"rows"`
	Error string           `json:"error,omitempty"`
}

// ReadBlock dumps rows*4 bytes starting at addr (decimal or 0xHEX) as decoded
// 4-byte slots, for the live memory viewer. Read-only.
func (m *MemRead) ReadBlock(addr string, rows int) BlockResult {
	v, err := parseAddr(addr)
	if err != nil {
		return BlockResult{Error: fmt.Sprintf("bad address %q", addr)}
	}
	out, err := m.reader.ReadBlock(uintptr(v), rows)
	if err != nil {
		return BlockResult{Base: fmt.Sprintf("0x%X", v), Error: err.Error()}
	}
	return BlockResult{Base: fmt.Sprintf("0x%X", v), Rows: out}
}

// parseAddr parses an address string: "0x..." as hex, otherwise decimal.
func parseAddr(addr string) (uint64, error) {
	s := strings.TrimSpace(addr)
	if strings.HasPrefix(strings.ToLower(s), "0x") {
		return parseHexInt(s)
	}
	return strconv.ParseUint(s, 10, 64)
}

// ReadFloatAt live-polls a single locked candidate address (decimal or 0xHEX
// string, since JS numbers can't safely hold a full 64-bit address).
func (m *MemRead) ReadFloatAt(addr string) (float32, error) {
	v, err := parseAddr(addr)
	if err != nil {
		return 0, fmt.Errorf("bad address %q: %w", addr, err)
	}
	return m.reader.ReadFloatAt(uintptr(v))
}

// ReadTypedAt live-polls a locked candidate address with an explicit value type.
func (m *MemRead) ReadTypedAt(addr, vtype string) (float64, error) {
	v, err := parseAddr(addr)
	if err != nil {
		return 0, fmt.Errorf("bad address %q: %w", addr, err)
	}
	return m.reader.ReadTypedAt(uintptr(v), vtype)
}

func formatOffsets(offs []uintptr) string {
	if len(offs) == 0 {
		return ""
	}
	parts := make([]string, len(offs))
	for i, o := range offs {
		parts[i] = fmt.Sprintf("0x%X", o)
	}
	return strings.Join(parts, ", ")
}

// parsePointerSpec turns a spec string into (module, rootOffset, offsets).
func parsePointerSpec(spec string) (module string, root uintptr, offsets []uintptr, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", 0, nil, fmt.Errorf("empty pointer spec")
	}
	// Normalise all separators to a single delimiter.
	norm := strings.NewReplacer("->", "\x00", ",", "\x00", "\n", "\x00", "\t", "\x00", " ", "\x00").Replace(spec)
	tokens := make([]string, 0, 8)
	for _, t := range strings.Split(norm, "\x00") {
		if t = strings.TrimSpace(t); t != "" {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		return "", 0, nil, fmt.Errorf("no tokens in spec")
	}

	// First token holds the module and root offset.
	module, root, err = parseBase(tokens[0])
	if err != nil {
		return "", 0, nil, err
	}
	for _, t := range tokens[1:] {
		off, err := parseHexInt(t)
		if err != nil {
			return "", 0, nil, fmt.Errorf("bad offset %q: %w", t, err)
		}
		offsets = append(offsets, uintptr(off))
	}
	return module, root, offsets, nil
}

// parseBase parses "Module+0xHEX", "+0xHEX", "0xHEX" or "Module".
func parseBase(tok string) (module string, root uintptr, err error) {
	if i := strings.IndexByte(tok, '+'); i >= 0 {
		module = strings.TrimSpace(tok[:i])
		off, err := parseHexInt(strings.TrimSpace(tok[i+1:]))
		if err != nil {
			return "", 0, fmt.Errorf("bad base offset in %q: %w", tok, err)
		}
		if module == "" {
			module = "Aion2.exe"
		}
		return module, uintptr(off), nil
	}
	// No '+': either a bare module name or a bare hex offset.
	if looksHex(tok) {
		off, err := parseHexInt(tok)
		if err != nil {
			return "", 0, err
		}
		return "Aion2.exe", uintptr(off), nil
	}
	return tok, 0, nil // bare module, static base offset 0
}

func looksHex(s string) bool {
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func parseHexInt(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	return strconv.ParseUint(s, 16, 64)
}
