// Package ipstack holds the pure (OS-independent) IPv4/TCP packet helpers used
// by the WinTUN userspace TCP NAT: header parsing, checksum computation, and
// packet construction. Kept separate from the Windows-only TUN wiring so this
// logic is unit-testable on any platform. Ported from the reference
// aion2-network-accelerator/crates/aion2-proxy/src/tun_windows.rs.
package ipstack

import "encoding/binary"

// TCP flag bits.
const (
	FlagFIN = 0x01
	FlagSYN = 0x02
	FlagRST = 0x04
	FlagPSH = 0x08
	FlagACK = 0x10
)

// TCP holds the parsed fields of an IPv4+TCP packet we care about.
type TCP struct {
	SrcIP   [4]byte
	DstIP   [4]byte
	SrcPort uint16
	DstPort uint16
	Seq     uint32
	Ack     uint32
	Flags   byte
	Window  uint16
	Payload []byte
}

// ParseTCP parses an IPv4 TCP packet. Returns ok=false for non-IPv4, non-TCP,
// or truncated packets.
func ParseTCP(pkt []byte) (TCP, bool) {
	var t TCP
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return t, false
	}
	ihl := int(pkt[0]&0x0F) * 4
	if ihl < 20 || len(pkt) < ihl+20 {
		return t, false
	}
	if pkt[9] != 6 { // protocol != TCP
		return t, false
	}
	copy(t.SrcIP[:], pkt[12:16])
	copy(t.DstIP[:], pkt[16:20])
	tcp := pkt[ihl:]
	t.SrcPort = binary.BigEndian.Uint16(tcp[0:2])
	t.DstPort = binary.BigEndian.Uint16(tcp[2:4])
	t.Seq = binary.BigEndian.Uint32(tcp[4:8])
	t.Ack = binary.BigEndian.Uint32(tcp[8:12])
	dataOff := int(tcp[12]>>4) * 4
	if dataOff < 20 || len(tcp) < dataOff {
		return t, false
	}
	t.Flags = tcp[13]
	t.Window = binary.BigEndian.Uint16(tcp[14:16])
	if len(tcp) > dataOff {
		t.Payload = tcp[dataOff:]
	}
	return t, true
}

// checksum16 computes the 16-bit one's-complement sum (RFC 1071) over b.
func checksum16(b []byte) uint32 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	return sum
}

func fold(sum uint32) uint16 {
	for sum>>16 != 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	return ^uint16(sum)
}

// BuildTCP constructs a complete IPv4+TCP packet with correct checksums. If
// withMSS is true a 4-byte MSS option (1360) is added (used on SYN/SYN-ACK).
func BuildTCP(srcIP, dstIP [4]byte, srcPort, dstPort uint16, seq, ack uint32, flags byte, window uint16, payload []byte, withMSS bool) []byte {
	tcpHdr := 20
	var opts []byte
	if withMSS {
		opts = []byte{0x02, 0x04, 0x05, 0x50} // MSS = 1360
		tcpHdr += len(opts)
	}
	ipHdr := 20
	total := ipHdr + tcpHdr + len(payload)
	pkt := make([]byte, total)

	// IPv4 header.
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(total))
	pkt[8] = 64 // TTL
	pkt[9] = 6  // TCP
	copy(pkt[12:16], srcIP[:])
	copy(pkt[16:20], dstIP[:])
	binary.BigEndian.PutUint16(pkt[10:12], fold(checksum16(pkt[:ipHdr])))

	// TCP header.
	t := pkt[ipHdr:]
	binary.BigEndian.PutUint16(t[0:2], srcPort)
	binary.BigEndian.PutUint16(t[2:4], dstPort)
	binary.BigEndian.PutUint32(t[4:8], seq)
	binary.BigEndian.PutUint32(t[8:12], ack)
	t[12] = byte((tcpHdr / 4) << 4) // data offset
	t[13] = flags
	binary.BigEndian.PutUint16(t[14:16], window)
	copy(t[20:], opts)
	copy(t[tcpHdr:], payload)

	// TCP checksum over pseudo-header + TCP segment.
	pseudo := make([]byte, 12)
	copy(pseudo[0:4], srcIP[:])
	copy(pseudo[4:8], dstIP[:])
	pseudo[9] = 6
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(tcpHdr+len(payload)))
	sum := checksum16(pseudo) + checksum16(t[:tcpHdr+len(payload)])
	binary.BigEndian.PutUint16(t[16:18], fold(sum))
	return pkt
}

// VerifyChecksums returns true if the IPv4 and TCP checksums in pkt are valid
// (a folded one's-complement sum over the relevant bytes yields 0). Used in
// tests.
func VerifyChecksums(pkt []byte) bool {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return false
	}
	ihl := int(pkt[0]&0x0F) * 4
	if fold(checksum16(pkt[:ihl])) != 0 {
		return false
	}
	tcp := pkt[ihl:]
	pseudo := make([]byte, 12)
	copy(pseudo[0:4], pkt[12:16])
	copy(pseudo[4:8], pkt[16:20])
	pseudo[9] = 6
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(tcp)))
	return fold(checksum16(pseudo)+checksum16(tcp)) == 0
}
