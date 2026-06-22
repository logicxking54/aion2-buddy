//go:build windows

package memread

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This file is a strictly read-only value scanner — the in-app Cheat Engine.
// It walks the game's writable committed memory with VirtualQueryEx and reads
// it with ReadProcessMemory. It NEVER writes, sets breakpoints, or injects, so
// its footprint is smaller than CE (which uses debug APIs for "find what
// accesses"). It supports the classic CE flow: pick a value type, do a first
// scan (a known value/range, OR "unknown initial value"), then narrow with
// increased / decreased / changed / unchanged / exact / between filters.

var (
	modkernel32      = windows.NewLazySystemDLL("kernel32.dll")
	procVirtualQuery = modkernel32.NewProc("VirtualQueryEx")
)

type memBasicInfo struct {
	BaseAddress       uintptr
	AllocationBase    uintptr
	AllocationProtect uint32
	_                 uint32
	RegionSize        uintptr
	State             uint32
	Protect           uint32
	Type              uint32
	_                 uint32
}

const (
	memCommit = 0x1000

	pageNoAccess = 0x01
	pageGuard    = 0x100

	pageReadWrite        = 0x04
	pageWriteCopy        = 0x08
	pageExecuteReadWrite = 0x40
	pageExecuteWriteCopy = 0x80

	userSpaceMax     = uintptr(0x7FFFFFFFFFFF)
	scanChunk        = 1 << 20   // 1 MiB read chunks
	maxCandidates    = 8_000_000 // cap on the address-list result
	maxSnapshotBytes = 2 << 30   // 2 GiB cap on an unknown-scan snapshot
)

func isWritable(protect uint32) bool {
	if protect&pageGuard != 0 || protect&pageNoAccess != 0 {
		return false
	}
	switch protect {
	case pageReadWrite, pageWriteCopy, pageExecuteReadWrite, pageExecuteWriteCopy:
		return true
	}
	return false
}

// --- value types ---

func typeSize(t string) int {
	switch t {
	case "byte":
		return 1
	case "int16", "uint16":
		return 2
	case "int32", "uint32", "float":
		return 4
	case "int64", "uint64", "double":
		return 8
	default:
		return 4
	}
}

// decode reads one typed value from b (len(b) >= typeSize) as a float64 for
// uniform comparison. int64/uint64 above 2^53 lose precision, which is fine for
// the small magnitudes a cooldown uses.
func decode(b []byte, t string) float64 {
	switch t {
	case "byte":
		return float64(b[0])
	case "int16":
		return float64(int16(binary.LittleEndian.Uint16(b)))
	case "uint16":
		return float64(binary.LittleEndian.Uint16(b))
	case "int32":
		return float64(int32(binary.LittleEndian.Uint32(b)))
	case "uint32":
		return float64(binary.LittleEndian.Uint32(b))
	case "float":
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	case "int64":
		return float64(int64(binary.LittleEndian.Uint64(b)))
	case "uint64":
		return float64(binary.LittleEndian.Uint64(b))
	case "double":
		return math.Float64frombits(binary.LittleEndian.Uint64(b))
	default:
		return 0
	}
}

func isFiniteVal(t string, v float64) bool {
	if t == "float" || t == "double" {
		return !math.IsNaN(v) && !math.IsInf(v, 0)
	}
	return true
}

// safeF64 / safeF32 replace NaN/Inf with 0 so values can be JSON-encoded for
// the frontend (Go's encoding/json — used by Wails — rejects NaN/Inf and would
// otherwise crash the app on the next read).
func safeF64(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func safeF32(v float32) float32 {
	if v != v || math.IsInf(float64(v), 0) {
		return 0
	}
	return v
}

// match reports whether a value passes op. value-vs-constant ops use a,b;
// value-vs-previous ops use prev (only valid when hasPrev).
func match(op string, prev, cur, a, b float64, hasPrev bool) bool {
	switch op {
	case "exact":
		return cur == a
	case "between":
		return cur >= a && cur <= b
	case "bigger":
		return cur > a
	case "smaller":
		return cur < a
	case "changed":
		return hasPrev && cur != prev
	case "unchanged":
		return hasPrev && cur == prev
	case "inc":
		return hasPrev && cur > prev
	case "dec":
		return hasPrev && cur < prev
	case "incby":
		return hasPrev && cur == prev+a
	case "decby":
		return hasPrev && cur == prev-a
	case "range": // alias of between (auto-locator)
		return cur >= a && cur <= b
	default:
		return false
	}
}

// --- scan state ---

// block is a contiguous run of bytes successfully read from the target at
// scan time. Unknown-initial scans keep blocks so later inc/dec passes can
// compare against the snapshot.
type block struct {
	base uintptr
	data []byte
}

type scanState struct {
	vtype  string
	mode   string // "list" | "snapshot"
	addrs  []uintptr
	vals   []float64 // previous typed value at addr (list mode)
	blocks []block   // snapshot mode
}

// ScanHit is one candidate returned to the UI.
type ScanHit struct {
	Address uint64  `json:"address"`
	Value   float64 `json:"value"`
	Display string  `json:"display"`
}

func formatVal(t string, v float64) string {
	switch t {
	case "float", "double":
		if math.Abs(v) < 1e-4 || math.Abs(v) > 1e9 {
			return fmt.Sprintf("%.3e", v)
		}
		return fmt.Sprintf("%.3f", v)
	default:
		return fmt.Sprintf("%d", int64(v))
	}
}

// --- region walking ---

// eachWritableChunk calls fn(base, data) for every readable chunk of every
// committed writable region. data aliases a shared buffer — copy if retaining.
func (r *Reader) eachWritableChunk(buf []byte, fn func(base uintptr, data []byte) bool) {
	addr := uintptr(0)
	for addr < userSpaceMax {
		var mbi memBasicInfo
		ret, _, _ := procVirtualQuery.Call(uintptr(r.handle), addr,
			uintptr(unsafe.Pointer(&mbi)), unsafe.Sizeof(mbi))
		if ret == 0 {
			break
		}
		next := mbi.BaseAddress + mbi.RegionSize
		if next <= addr {
			break
		}
		if mbi.State == memCommit && isWritable(mbi.Protect) {
			for off := uintptr(0); off < mbi.RegionSize; off += scanChunk {
				n := scanChunk
				if rem := mbi.RegionSize - off; rem < uintptr(n) {
					n = int(rem)
				}
				var got uintptr
				err := windows.ReadProcessMemory(r.handle, mbi.BaseAddress+off, &buf[0], uintptr(n), &got)
				if err != nil || got < 8 {
					continue
				}
				if !fn(mbi.BaseAddress+off, buf[:got]) {
					return
				}
			}
		}
		addr = next
	}
}

// ScanNew starts a fresh scan with value type vtype and first-scan op:
//
//	"unknown"  unknown initial value — snapshot memory for later inc/dec
//	"exact"    value == a
//	"between"  a <= value <= b
//	"bigger"   value > a
//	"smaller"  value < a
//
// For "unknown" the returned count is the number of values snapshotted (a
// rough size, not a candidate list). For the others it's the candidate count.
func (r *Reader) ScanNew(vtype, op string, a, b float64) (count int, truncated bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.open(); err != nil {
		return 0, false, err
	}
	size := typeSize(vtype)
	st := &scanState{vtype: vtype}
	buf := make([]byte, scanChunk)

	if op == "unknown" {
		st.mode = "snapshot"
		total := 0
		values := 0
		trunc := false
		r.eachWritableChunk(buf, func(base uintptr, data []byte) bool {
			cp := make([]byte, len(data))
			copy(cp, data)
			st.blocks = append(st.blocks, block{base: base, data: cp})
			total += len(cp)
			values += len(cp) / size
			if total >= maxSnapshotBytes {
				trunc = true
				return false
			}
			return true
		})
		r.scan = st
		return values, trunc, nil
	}

	st.mode = "list"
	trunc := false
	r.eachWritableChunk(buf, func(base uintptr, data []byte) bool {
		for i := 0; i+size <= len(data); i += size {
			cur := decode(data[i:], vtype)
			if !isFiniteVal(vtype, cur) {
				continue
			}
			if match(op, 0, cur, a, b, false) {
				st.addrs = append(st.addrs, base+uintptr(i))
				st.vals = append(st.vals, cur)
				if len(st.addrs) >= maxCandidates {
					trunc = true
					return false
				}
			}
		}
		return true
	})
	r.scan = st
	return len(st.addrs), trunc, nil
}

// readChunked decodes the typed value at each address, coalescing reads into
// 64 KiB chunks to slash syscalls. Candidate addresses are kept in ascending
// order, so clustered candidates (a struct, an array) are served from one read.
// ok[i] is false when address i couldn't be read (it's then dropped by callers).
func (r *Reader) readChunked(addrs []uintptr, vtype string) (vals []float64, ok []bool) {
	const chunkBytes = 64 * 1024
	size := typeSize(vtype)
	vals = make([]float64, len(addrs))
	ok = make([]bool, len(addrs))
	chunk := make([]byte, chunkBytes)
	var bufBase uintptr
	var bufLen int // bytes valid in chunk starting at bufBase (0 = none)
	for i, ad := range addrs {
		if bufLen > 0 && ad >= bufBase && ad+uintptr(size) <= bufBase+uintptr(bufLen) {
			vals[i] = decode(chunk[ad-bufBase:], vtype)
			ok[i] = true
			continue
		}
		// Read a fresh chunk starting at this address. ReadProcessMemory sets
		// got even on a partial copy (chunk crossing an unmapped page), so we
		// use whatever it returned.
		var got uintptr
		windows.ReadProcessMemory(r.handle, ad, &chunk[0], uintptr(chunkBytes), &got)
		if int(got) < size {
			bufLen = 0
			continue
		}
		bufBase = ad
		bufLen = int(got)
		vals[i] = decode(chunk, vtype)
		ok[i] = true
	}
	return vals, ok
}

// ScanNext narrows the current candidates by op (see match). Works whether the
// current scan is a snapshot (first narrow after "unknown") or an address list.
// After any narrow the result is always an address list.
func (r *Reader) ScanNext(op string, a, b float64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scan == nil {
		return 0, fmt.Errorf("no active scan — run a first scan first")
	}
	if err := r.open(); err != nil {
		return 0, err
	}
	st := r.scan
	size := typeSize(st.vtype)
	keep := &scanState{vtype: st.vtype, mode: "list"}

	if st.mode == "snapshot" {
		buf := make([]byte, scanChunk)
		for _, blk := range st.blocks {
			var got uintptr
			n := len(blk.data)
			if err := windows.ReadProcessMemory(r.handle, blk.base, &buf[0], uintptr(n), &got); err != nil || int(got) < n {
				continue // block no longer fully readable — skip
			}
			for i := 0; i+size <= n; i += size {
				prev := decode(blk.data[i:], st.vtype)
				cur := decode(buf[i:], st.vtype)
				if !isFiniteVal(st.vtype, cur) {
					continue
				}
				if match(op, prev, cur, a, b, true) {
					keep.addrs = append(keep.addrs, blk.base+uintptr(i))
					keep.vals = append(keep.vals, cur)
					if len(keep.addrs) >= maxCandidates {
						r.scan = keep
						return len(keep.addrs), nil
					}
				}
			}
		}
		r.scan = keep
		return len(keep.addrs), nil
	}

	// list mode — chunked reads over the (ascending) candidate addresses.
	vals, ok := r.readChunked(st.addrs, st.vtype)
	for i, ad := range st.addrs {
		if !ok[i] {
			continue
		}
		cur := vals[i]
		if !isFiniteVal(st.vtype, cur) {
			continue
		}
		if match(op, st.vals[i], cur, a, b, true) {
			keep.addrs = append(keep.addrs, ad)
			keep.vals = append(keep.vals, cur)
		}
	}
	r.scan = keep
	return len(keep.addrs), nil
}

// ScanFloatRange is the auto-locator shortcut: a float first scan in [min,max].
func (r *Reader) ScanFloatRange(min, max float32) (int, bool, error) {
	return r.ScanNew("float", "between", float64(min), float64(max))
}

// ScanCount returns the current candidate count (0 if none; snapshot mode
// reports 0 since it isn't an enumerated list yet).
func (r *Reader) ScanCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scan == nil || r.scan.mode == "snapshot" {
		return 0
	}
	return len(r.scan.addrs)
}

// ScanMode returns "", "list", or "snapshot".
func (r *Reader) ScanMode() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scan == nil {
		return ""
	}
	return r.scan.mode
}

// ScanList returns up to limit current candidates with freshly read values.
func (r *Reader) ScanList(limit int) ([]ScanHit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scan == nil || r.scan.mode != "list" {
		return nil, nil
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	n := len(r.scan.addrs)
	if limit > 0 && n > limit {
		n = limit
	}
	addrs := r.scan.addrs[:n]
	vals, ok := r.readChunked(addrs, r.scan.vtype)
	hits := make([]ScanHit, 0, n)
	for i := 0; i < n; i++ {
		v := r.scan.vals[i]
		if ok[i] {
			v = vals[i]
		}
		hits = append(hits, ScanHit{Address: uint64(addrs[i]), Value: safeF64(v), Display: formatVal(r.scan.vtype, v)})
	}
	return hits, nil
}

// ScanReset clears the candidate set.
func (r *Reader) ScanReset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scan = nil
}

// ScanRemove drops one address from the candidate list (manual pruning during
// analysis). Returns the new candidate count. No-op unless in list mode.
func (r *Reader) ScanRemove(addr uintptr) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scan == nil || r.scan.mode != "list" {
		return 0
	}
	for i, a := range r.scan.addrs {
		if a == addr {
			r.scan.addrs = append(r.scan.addrs[:i], r.scan.addrs[i+1:]...)
			r.scan.vals = append(r.scan.vals[:i], r.scan.vals[i+1:]...)
			break
		}
	}
	return len(r.scan.addrs)
}

// ReadTypedAt reads one typed value at an absolute address (live-poll a locked
// candidate). Read-only.
func (r *Reader) ReadTypedAt(addr uintptr, vtype string) (float64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.open(); err != nil {
		return 0, err
	}
	size := typeSize(vtype)
	raw := make([]byte, size)
	var got uintptr
	if err := windows.ReadProcessMemory(r.handle, addr, &raw[0], uintptr(size), &got); err != nil || int(got) < size {
		return 0, fmt.Errorf("read %s at 0x%X: %w", vtype, addr, err)
	}
	return safeF64(decode(raw, vtype)), nil
}

// ReadFloatAt keeps the auto-locator's float live-poll working.
func (r *Reader) ReadFloatAt(addr uintptr) (float32, error) {
	v, err := r.ReadTypedAt(addr, "float")
	return float32(v), err
}

// MemRow is one 4-byte slot of a memory-view dump, decoded several ways so the
// struct around an address can be inspected (like a mini ReClass / CE memory
// viewer).
type MemRow struct {
	Offset  int     `json:"offset"`  // bytes from the block base
	Address uint64  `json:"address"` // absolute address of this slot
	Hex     string  `json:"hex"`     // the 4 raw bytes
	Int32   int32   `json:"int32"`
	UInt32  uint32  `json:"uint32"`
	Float32 float32 `json:"float32"`
}

// ReadBlock dumps rows*4 bytes starting at addr as decoded 4-byte slots.
// Read-only. rows is clamped to [1,16384] (64 KiB max in one read).
func (r *Reader) ReadBlock(addr uintptr, rows int) ([]MemRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.open(); err != nil {
		return nil, err
	}
	if rows <= 0 {
		rows = 32
	}
	if rows > 16384 {
		rows = 16384
	}
	n := rows * 4
	buf := make([]byte, n)
	var got uintptr
	if err := windows.ReadProcessMemory(r.handle, addr, &buf[0], uintptr(n), &got); err != nil {
		return nil, fmt.Errorf("read block at 0x%X: %w", addr, err)
	}
	count := int(got) / 4
	out := make([]MemRow, 0, count)
	for i := 0; i < count; i++ {
		off := i * 4
		u := binary.LittleEndian.Uint32(buf[off:])
		out = append(out, MemRow{
			Offset:  off,
			Address: uint64(addr) + uint64(off),
			Hex:     fmt.Sprintf("%02X %02X %02X %02X", buf[off], buf[off+1], buf[off+2], buf[off+3]),
			Int32:   int32(u),
			UInt32:  u,
			Float32: safeF32(math.Float32frombits(u)),
		})
	}
	return out, nil
}
