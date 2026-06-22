// Package proto is the shared tunnel protocol for Oversize Network: the message
// types exchanged between the Windows VPN client (internal/oversize) and the
// Linux relay daemon (cmd/aion2-relay), plus AEAD framing (crypto.go) and the
// selective-repeat ARQ over UDP (reliable.go).
//
// Both ends are ours, so the wire format is a compact hand-rolled binary
// encoding (no bincode/gob compatibility needed). It mirrors the semantics of
// the reference's aion2-common/src/protocol.rs.
package proto

import (
	"encoding/binary"
	"errors"
	"net"
)

// Payload size caps (mirror the reference). Reads are chunked to UDPSafePayload
// so the sealed datagram stays under typical path MTU and avoids IP fragmentation.
const (
	MaxPayload     = 1280
	UDPSafePayload = 1100
)

// ConnId identifies one proxied TCP flow: (srcIP:srcPort) -> (dstIP:dstPort).
// The relay keys real outbound connections by (sessionId, ConnId).
type ConnId struct {
	SrcIP   [4]byte
	SrcPort uint16
	DstIP   [4]byte
	DstPort uint16
}

// connIdSize is the fixed wire size of a ConnId.
const connIdSize = 12

func (c ConnId) encode(b []byte) {
	copy(b[0:4], c.SrcIP[:])
	binary.BigEndian.PutUint16(b[4:6], c.SrcPort)
	copy(b[6:10], c.DstIP[:])
	binary.BigEndian.PutUint16(b[10:12], c.DstPort)
}

func decodeConnId(b []byte) ConnId {
	var c ConnId
	copy(c.SrcIP[:], b[0:4])
	c.SrcPort = binary.BigEndian.Uint16(b[4:6])
	copy(c.DstIP[:], b[6:10])
	c.DstPort = binary.BigEndian.Uint16(b[10:12])
	return c
}

// DstAddr returns the flow's destination as a net.TCPAddr (the real game server).
func (c ConnId) DstAddr() *net.TCPAddr {
	return &net.TCPAddr{IP: net.IP(c.DstIP[:]).To4(), Port: int(c.DstPort)}
}

// MsgType tags a TunnelMessage.
type MsgType byte

const (
	MsgConnect       MsgType = 1 // client -> relay: open a TCP connection to Conn.DstAddr
	MsgConnected     MsgType = 2 // relay -> client: connection established
	MsgConnectFailed MsgType = 3 // relay -> client: connect failed (Payload = reason)
	MsgData          MsgType = 4 // both: forward TCP payload (Payload)
	MsgShutdown      MsgType = 5 // both: half-close (TCP FIN)
	MsgReset         MsgType = 6 // both: force-close (TCP RST)
	MsgPing          MsgType = 7 // client -> relay: keepalive/latency (Seq, TS)
	MsgPong          MsgType = 8 // relay -> client: echo (Seq, TS)
)

// Msg is the decoded tunnel message. Only the fields relevant to Type are set.
type Msg struct {
	Type    MsgType
	Conn    ConnId
	Payload []byte // Data payload, or ConnectFailed reason
	Seq     uint64 // Ping/Pong
	TS      uint64 // Ping/Pong timestamp (ms)
}

var errShort = errors.New("proto: short buffer")

// EncodeMsg serializes a Msg. Layout: [type:1][body...].
//   - Connect/Connected/Shutdown/Reset: [ConnId:12]
//   - Data/ConnectFailed:               [ConnId:12][payload...]
//   - Ping/Pong:                        [Seq:8][TS:8]
func EncodeMsg(m *Msg) []byte {
	switch m.Type {
	case MsgPing, MsgPong:
		out := make([]byte, 1+16)
		out[0] = byte(m.Type)
		binary.BigEndian.PutUint64(out[1:9], m.Seq)
		binary.BigEndian.PutUint64(out[9:17], m.TS)
		return out
	case MsgData, MsgConnectFailed:
		out := make([]byte, 1+connIdSize+len(m.Payload))
		out[0] = byte(m.Type)
		m.Conn.encode(out[1 : 1+connIdSize])
		copy(out[1+connIdSize:], m.Payload)
		return out
	default: // Connect, Connected, Shutdown, Reset
		out := make([]byte, 1+connIdSize)
		out[0] = byte(m.Type)
		m.Conn.encode(out[1 : 1+connIdSize])
		return out
	}
}

// DecodeMsg parses a Msg produced by EncodeMsg.
func DecodeMsg(b []byte) (Msg, error) {
	if len(b) < 1 {
		return Msg{}, errShort
	}
	m := Msg{Type: MsgType(b[0])}
	rest := b[1:]
	switch m.Type {
	case MsgPing, MsgPong:
		if len(rest) < 16 {
			return Msg{}, errShort
		}
		m.Seq = binary.BigEndian.Uint64(rest[0:8])
		m.TS = binary.BigEndian.Uint64(rest[8:16])
	case MsgData, MsgConnectFailed:
		if len(rest) < connIdSize {
			return Msg{}, errShort
		}
		m.Conn = decodeConnId(rest[:connIdSize])
		if len(rest) > connIdSize {
			m.Payload = append([]byte(nil), rest[connIdSize:]...)
		}
	case MsgConnect, MsgConnected, MsgShutdown, MsgReset:
		if len(rest) < connIdSize {
			return Msg{}, errShort
		}
		m.Conn = decodeConnId(rest[:connIdSize])
	default:
		return Msg{}, errors.New("proto: unknown message type")
	}
	return m, nil
}
