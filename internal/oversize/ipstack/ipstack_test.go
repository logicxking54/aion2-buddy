package ipstack

import (
	"bytes"
	"testing"
)

func TestBuildParseRoundTrip(t *testing.T) {
	src := [4]byte{10, 200, 0, 1}
	dst := [4]byte{10, 200, 0, 2}
	payload := []byte("payload bytes here")

	pkt := BuildTCP(src, dst, 443, 51000, 0x11223344, 0x55667788, FlagACK|FlagPSH, 65535, payload, false)

	if !VerifyChecksums(pkt) {
		t.Fatalf("checksums invalid")
	}
	got, ok := ParseTCP(pkt)
	if !ok {
		t.Fatalf("parse failed")
	}
	if got.SrcIP != src || got.DstIP != dst || got.SrcPort != 443 || got.DstPort != 51000 {
		t.Fatalf("addr/port mismatch: %+v", got)
	}
	if got.Seq != 0x11223344 || got.Ack != 0x55667788 || got.Flags != (FlagACK|FlagPSH) {
		t.Fatalf("seq/ack/flags mismatch: %+v", got)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatalf("payload mismatch: %q", got.Payload)
	}
}

func TestBuildWithMSSChecksum(t *testing.T) {
	src := [4]byte{1, 2, 3, 4}
	dst := [4]byte{5, 6, 7, 8}
	// SYN-ACK with MSS option, no payload.
	pkt := BuildTCP(src, dst, 80, 12345, 1000, 2000, FlagSYN|FlagACK, 64240, nil, true)
	if !VerifyChecksums(pkt) {
		t.Fatalf("SYN-ACK checksums invalid")
	}
	got, ok := ParseTCP(pkt)
	if !ok || got.Flags != (FlagSYN|FlagACK) || len(got.Payload) != 0 {
		t.Fatalf("parse SYN-ACK mismatch: %+v ok=%v", got, ok)
	}
}

func TestParseRejectsNonTCP(t *testing.T) {
	// IPv4 UDP packet (protocol 17) must be rejected.
	pkt := make([]byte, 28)
	pkt[0] = 0x45
	pkt[9] = 17
	if _, ok := ParseTCP(pkt); ok {
		t.Fatalf("should reject non-TCP")
	}
	// Too short.
	if _, ok := ParseTCP([]byte{0x45, 0, 0}); ok {
		t.Fatalf("should reject short packet")
	}
}
