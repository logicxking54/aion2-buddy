package proto

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []Frame{
		{Type: FrameData, Seq: 42, Payload: []byte("data")},
		{Type: FrameUnreliable, Payload: []byte("ping")},
		{Type: FrameAck, Ack: 5, Ranges: [][2]uint64{{7, 9}, {11, 11}}},
	}
	for _, in := range cases {
		got, err := DecodeFrame(EncodeFrame(&in))
		if err != nil {
			t.Fatalf("decode %v: %v", in.Type, err)
		}
		if got.Type != in.Type || got.Seq != in.Seq || got.Ack != in.Ack {
			t.Fatalf("mismatch in=%+v got=%+v", in, got)
		}
		if !bytes.Equal(got.Payload, in.Payload) {
			t.Fatalf("payload mismatch %q vs %q", got.Payload, in.Payload)
		}
		if len(got.Ranges) != len(in.Ranges) {
			t.Fatalf("ranges len mismatch")
		}
		for i := range in.Ranges {
			if got.Ranges[i] != in.Ranges[i] {
				t.Fatalf("range mismatch")
			}
		}
	}
}

// Rx delivers in order, buffers out-of-order, and dedups.
func TestRxReorderAndDedup(t *testing.T) {
	rx := NewRx()

	if d := rx.Recv(0, []byte("a")); len(d) != 1 || string(d[0]) != "a" {
		t.Fatalf("seq0 should deliver immediately, got %v", d)
	}
	// Out-of-order: 2 before 1 — nothing delivered yet.
	if d := rx.Recv(2, []byte("c")); d != nil {
		t.Fatalf("seq2 should buffer, got %v", d)
	}
	// Duplicate of an already-delivered seq.
	if d := rx.Recv(0, []byte("a")); d != nil {
		t.Fatalf("dup seq0 should be dropped, got %v", d)
	}
	// Gap fills: delivers 1 then buffered 2.
	d := rx.Recv(1, []byte("b"))
	if len(d) != 2 || string(d[0]) != "b" || string(d[1]) != "c" {
		t.Fatalf("gap fill should deliver b,c, got %v", d)
	}
	// Duplicate-path copy of seq2 after delivery.
	if d := rx.Recv(2, []byte("c")); d != nil {
		t.Fatalf("dup-path seq2 should be dropped, got %v", d)
	}

	ack, ranges := rx.Ack()
	if ack != 3 || len(ranges) != 0 {
		t.Fatalf("ack should be 3 with no ranges, got ack=%d ranges=%v", ack, ranges)
	}
}

func TestRxAckRanges(t *testing.T) {
	rx := NewRx()
	rx.Recv(0, []byte("a")) // delivered, nextExpected=1
	rx.Recv(2, []byte("c")) // buffered
	rx.Recv(3, []byte("d")) // buffered (contiguous with 2)
	rx.Recv(5, []byte("f")) // buffered (separate run)
	ack, ranges := rx.Ack()
	if ack != 1 {
		t.Fatalf("ack should be 1, got %d", ack)
	}
	want := [][2]uint64{{2, 3}, {5, 5}}
	if len(ranges) != len(want) {
		t.Fatalf("ranges %v != %v", ranges, want)
	}
	for i := range want {
		if ranges[i] != want[i] {
			t.Fatalf("ranges %v != %v", ranges, want)
		}
	}
}

// Tx retransmits after RTO and stops once acked.
func TestTxRetransmitAndAck(t *testing.T) {
	tx := NewTx()
	now := uint64(1000)
	f := tx.Send([]byte("x"), now)
	if f.Seq != 0 || tx.Pending() != 1 {
		t.Fatalf("first send seq0/pending1, got seq=%d pending=%d", f.Seq, tx.Pending())
	}
	// Before RTO: no retransmit.
	if rs := tx.PollRetransmit(now + 10); len(rs) != 0 {
		t.Fatalf("should not retransmit before RTO")
	}
	// After RTO: retransmit seq0.
	rs := tx.PollRetransmit(now + defaultRTOms + 1)
	if len(rs) != 1 || rs[0].Seq != 0 {
		t.Fatalf("should retransmit seq0, got %v", rs)
	}
	// Ack clears it.
	tx.OnAck(1, nil, now+defaultRTOms+5) // ack=1 => seq0 acknowledged
	if tx.Pending() != 0 {
		t.Fatalf("ack should clear seq0, pending=%d", tx.Pending())
	}
}

func TestTxPurgeDead(t *testing.T) {
	tx := NewTx()
	now := uint64(0)
	tx.Send([]byte("x"), now)
	// Force many retransmits past maxTries.
	for i := 0; i < defaultMaxTries+5; i++ {
		now += maxRTOms + 1
		tx.PollRetransmit(now)
	}
	if dropped := tx.PurgeDead(); dropped != 1 {
		t.Fatalf("expected 1 dead frame purged, got %d", dropped)
	}
	if tx.Pending() != 0 {
		t.Fatalf("dead frame should be gone, pending=%d", tx.Pending())
	}
}
