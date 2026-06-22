package proto

import (
	"encoding/binary"
	"errors"
	"sort"
)

// Selective-repeat ARQ over UDP, ported from the reference
// aion2-common/src/reliable.rs. Both ends are ours, so the semantics are made
// self-consistent rather than bug-for-bug identical:
//   - Data.Seq is 0-based and increasing.
//   - Ack.Ack is the next expected seq (lowest not-yet-received); seqs [0, Ack)
//     are cumulatively acknowledged.
//   - Ranges lists additional buffered out-of-order runs (inclusive) at/after Ack.
//
// The layer is transport-agnostic: the caller supplies a monotonic now-ms clock
// and moves the encoded frames over the (encrypted) UDP socket itself.

// FrameType tags an ARQ frame.
type FrameType byte

const (
	FrameData       FrameType = 1
	FrameAck        FrameType = 2
	FrameUnreliable FrameType = 3
)

// Frame is one ARQ unit. Data/Unreliable carry a payload; Ack carries the
// cumulative ack plus selective ranges.
type Frame struct {
	Type    FrameType
	Seq     uint64       // Data
	Payload []byte       // Data / Unreliable
	Ack     uint64       // Ack
	Ranges  [][2]uint64  // Ack
}

// EncodeFrame serializes a Frame.
func EncodeFrame(f *Frame) []byte {
	switch f.Type {
	case FrameData:
		out := make([]byte, 1+8+len(f.Payload))
		out[0] = byte(FrameData)
		binary.BigEndian.PutUint64(out[1:9], f.Seq)
		copy(out[9:], f.Payload)
		return out
	case FrameUnreliable:
		out := make([]byte, 1+len(f.Payload))
		out[0] = byte(FrameUnreliable)
		copy(out[1:], f.Payload)
		return out
	case FrameAck:
		out := make([]byte, 1+8+2+len(f.Ranges)*16)
		out[0] = byte(FrameAck)
		binary.BigEndian.PutUint64(out[1:9], f.Ack)
		binary.BigEndian.PutUint16(out[9:11], uint16(len(f.Ranges)))
		off := 11
		for _, r := range f.Ranges {
			binary.BigEndian.PutUint64(out[off:off+8], r[0])
			binary.BigEndian.PutUint64(out[off+8:off+16], r[1])
			off += 16
		}
		return out
	}
	return nil
}

// DecodeFrame parses a Frame produced by EncodeFrame.
func DecodeFrame(b []byte) (Frame, error) {
	if len(b) < 1 {
		return Frame{}, errShort
	}
	f := Frame{Type: FrameType(b[0])}
	rest := b[1:]
	switch f.Type {
	case FrameData:
		if len(rest) < 8 {
			return Frame{}, errShort
		}
		f.Seq = binary.BigEndian.Uint64(rest[:8])
		if len(rest) > 8 {
			f.Payload = append([]byte(nil), rest[8:]...)
		}
	case FrameUnreliable:
		if len(rest) > 0 {
			f.Payload = append([]byte(nil), rest...)
		}
	case FrameAck:
		if len(rest) < 10 {
			return Frame{}, errShort
		}
		f.Ack = binary.BigEndian.Uint64(rest[:8])
		n := int(binary.BigEndian.Uint16(rest[8:10]))
		off := 10
		if len(rest) < off+n*16 {
			return Frame{}, errShort
		}
		f.Ranges = make([][2]uint64, n)
		for i := 0; i < n; i++ {
			f.Ranges[i][0] = binary.BigEndian.Uint64(rest[off : off+8])
			f.Ranges[i][1] = binary.BigEndian.Uint64(rest[off+8 : off+16])
			off += 16
		}
	default:
		return Frame{}, errors.New("proto: unknown frame type")
	}
	return f, nil
}

// ── Sender ────────────────────────────────────────────────────

const (
	minRTOms        = 50
	maxRTOms        = 2000
	defaultRTOms    = 250
	defaultMaxTries = 30
	// fastRtxGapMs rate-limits fast-retransmit so a persistent gap re-sends a
	// frame at most ~once per this interval (well under one RTT) rather than on
	// every duplicate ack.
	fastRtxGapMs = 15
)

type unacked struct {
	payload     []byte
	firstSentMs uint64
	lastSentMs  uint64
	tries       uint32
}

// Tx is the reliable sender: assigns sequence numbers, tracks un-acked frames,
// adapts the RTO from RTT samples (RFC 6298 + Karn), and retransmits.
type Tx struct {
	nextSeq  uint64
	unacked  map[uint64]*unacked
	rtoMs    uint64
	srtt     float64
	rttvar   float64
	hasSRTT  bool
	maxTries uint32
}

// NewTx creates a sender with default RTO/limits.
func NewTx() *Tx {
	return &Tx{unacked: map[uint64]*unacked{}, rtoMs: defaultRTOms, maxTries: defaultMaxTries}
}

// Send assigns the next seq, records the frame for retransmit, and returns the
// Data frame to transmit.
func (t *Tx) Send(payload []byte, nowMs uint64) Frame {
	seq := t.nextSeq
	t.nextSeq++
	t.unacked[seq] = &unacked{payload: payload, firstSentMs: nowMs, lastSentMs: nowMs, tries: 1}
	return Frame{Type: FrameData, Seq: seq, Payload: payload}
}

// OnAck clears acknowledged frames (cumulative < ack, plus selective ranges)
// and samples RTT from frames acked on their first transmission (Karn).
func (t *Tx) OnAck(ack uint64, ranges [][2]uint64, nowMs uint64) {
	ackSeq := func(seq uint64) {
		if u, ok := t.unacked[seq]; ok {
			if u.tries == 1 {
				t.sampleRTT(float64(nowMs - u.firstSentMs))
			}
			delete(t.unacked, seq)
		}
	}
	for seq := range t.unacked {
		if seq < ack {
			ackSeq(seq)
		}
	}
	for _, r := range ranges {
		for seq := r[0]; seq <= r[1]; seq++ {
			ackSeq(seq)
		}
	}
}

// PollRetransmit returns frames whose RTO has elapsed (and bumps their tries).
// On any retransmit the RTO is backed off (Karn); a clean RTT sample resets it.
func (t *Tx) PollRetransmit(nowMs uint64) []Frame {
	var out []Frame
	backedOff := false
	seqs := t.sortedSeqs()
	for _, seq := range seqs {
		u := t.unacked[seq]
		if u.tries <= t.maxTries && nowMs-u.lastSentMs >= t.rtoMs {
			u.lastSentMs = nowMs
			u.tries++
			out = append(out, Frame{Type: FrameData, Seq: seq, Payload: u.payload})
			backedOff = true
		}
	}
	if backedOff {
		t.rtoMs *= 2
		if t.rtoMs > maxRTOms {
			t.rtoMs = maxRTOms
		}
	}
	return out
}

// FastRetransmit returns unacked frames whose seq is below a selectively-acked
// seq — i.e. the receiver has already buffered a later frame, so this one is a
// gap (presumed lost). It resends immediately instead of waiting for the RTO,
// the way TCP fast-retransmit reacts to SACK/dup-acks. Rate-limited per frame
// (fastRtxGapMs) so reordering or repeated acks can't cause a retransmit storm.
// It does NOT back off the RTO (that stays a timer-based backstop); a spurious
// resend from reordering just costs one duplicate, which the receiver dedups.
func (t *Tx) FastRetransmit(ranges [][2]uint64, nowMs uint64) []Frame {
	if len(ranges) == 0 {
		return nil
	}
	var hi uint64
	for _, r := range ranges {
		if r[1] > hi {
			hi = r[1]
		}
	}
	var out []Frame
	for _, seq := range t.sortedSeqs() {
		if seq >= hi {
			break
		}
		u := t.unacked[seq]
		if u.tries <= t.maxTries && nowMs-u.lastSentMs >= fastRtxGapMs {
			u.lastSentMs = nowMs
			u.tries++
			out = append(out, Frame{Type: FrameData, Seq: seq, Payload: u.payload})
		}
	}
	return out
}

// PurgeDead drops frames that exceeded maxTries (dead-path backstop) and returns
// how many were dropped.
func (t *Tx) PurgeDead() int {
	n := 0
	for seq, u := range t.unacked {
		if u.tries > t.maxTries {
			delete(t.unacked, seq)
			n++
		}
	}
	return n
}

// Pending reports how many frames are awaiting ack.
func (t *Tx) Pending() int { return len(t.unacked) }

func (t *Tx) sortedSeqs() []uint64 {
	seqs := make([]uint64, 0, len(t.unacked))
	for s := range t.unacked {
		seqs = append(seqs, s)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs
}

func (t *Tx) sampleRTT(r float64) {
	if !t.hasSRTT {
		t.srtt = r
		t.rttvar = r / 2
		t.hasSRTT = true
	} else {
		t.rttvar = 0.75*t.rttvar + 0.25*abs(t.srtt-r)
		t.srtt = 0.875*t.srtt + 0.125*r
	}
	rto := t.srtt + max64(4*t.rttvar, 1.0)
	v := uint64(rto + 0.5)
	if v < minRTOms {
		v = minRTOms
	}
	if v > maxRTOms {
		v = maxRTOms
	}
	t.rtoMs = v
}

// ── Receiver ──────────────────────────────────────────────────

// Rx is the reliable receiver: delivers payloads in order, buffers
// out-of-order frames, and dedups retransmits / duplicate-path copies.
type Rx struct {
	nextExpected uint64
	buffer       map[uint64][]byte
}

// NewRx creates a receiver.
func NewRx() *Rx { return &Rx{buffer: map[uint64][]byte{}} }

// Recv ingests a Data frame and returns any newly in-order payloads (possibly
// several when a gap fills). Duplicates/old frames return nil.
func (r *Rx) Recv(seq uint64, payload []byte) [][]byte {
	if seq < r.nextExpected {
		return nil
	}
	if _, ok := r.buffer[seq]; ok {
		return nil
	}
	r.buffer[seq] = payload
	var delivered [][]byte
	for {
		p, ok := r.buffer[r.nextExpected]
		if !ok {
			break
		}
		delivered = append(delivered, p)
		delete(r.buffer, r.nextExpected)
		r.nextExpected++
	}
	return delivered
}

// Ack returns the cumulative ack (next expected seq) and the selective ranges of
// buffered out-of-order seqs.
func (r *Rx) Ack() (uint64, [][2]uint64) {
	if len(r.buffer) == 0 {
		return r.nextExpected, nil
	}
	seqs := make([]uint64, 0, len(r.buffer))
	for s := range r.buffer {
		seqs = append(seqs, s)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	var ranges [][2]uint64
	start, end := seqs[0], seqs[0]
	for _, s := range seqs[1:] {
		if s == end+1 {
			end = s
		} else {
			ranges = append(ranges, [2]uint64{start, end})
			start, end = s, s
		}
	}
	ranges = append(ranges, [2]uint64{start, end})
	return r.nextExpected, ranges
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
