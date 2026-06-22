// Package arq wraps the proto ARQ (Tx/Rx) + AEAD into a duplex reliable
// session shared by the VPN client (internal/oversize/tunnel) and the relay
// daemon (internal/oversize/relayd). It is transport-neutral: the owner
// supplies a send func (write one sealed wire packet) and feeds inbound packets
// via OnPacket; the session delivers in-order decoded messages to onMessage.
package arq

import (
	"sync"
	"time"

	"aion2tmp/internal/oversize/proto"
)

var start = time.Now()

// NowMs is the monotonic clock (ms) the ARQ uses for RTO timing.
func NowMs() uint64 { return uint64(time.Since(start).Milliseconds()) }

// Session is one encrypted, reliable, message-multiplexed channel with a peer.
type Session struct {
	key       *proto.Key
	sid       proto.SessionID
	send      func([]byte)
	onMessage func(proto.Msg)

	mu sync.Mutex
	tx *proto.Tx
	rx *proto.Rx
}

// New creates a session. send writes one sealed packet to the peer; onMessage
// receives each in-order decoded TunnelMessage.
func New(key *proto.Key, sid proto.SessionID, send func([]byte), onMessage func(proto.Msg)) *Session {
	return &Session{key: key, sid: sid, send: send, onMessage: onMessage, tx: proto.NewTx(), rx: proto.NewRx()}
}

// SessionID returns the session id.
func (s *Session) SessionID() proto.SessionID { return s.sid }

func (s *Session) sealSend(format byte, body []byte) {
	if pkt, err := s.key.Seal(s.sid, format, body); err == nil {
		s.send(pkt)
	}
}

// SendReliable queues a message for reliable, ordered delivery.
func (s *Session) SendReliable(m proto.Msg) {
	body := proto.EncodeMsg(&m)
	s.mu.Lock()
	f := s.tx.Send(body, NowMs())
	s.mu.Unlock()
	s.sealSend(proto.FormatFrame, proto.EncodeFrame(&f))
}

// SendUnreliable sends a message without ARQ (e.g. Ping/Pong).
func (s *Session) SendUnreliable(m proto.Msg) {
	f := proto.Frame{Type: proto.FrameUnreliable, Payload: proto.EncodeMsg(&m)}
	s.sealSend(proto.FormatFrame, proto.EncodeFrame(&f))
}

// OnPacket decrypts and processes one inbound wire packet.
func (s *Session) OnPacket(packet []byte) {
	format, body, err := s.key.Open(packet)
	if err != nil || format != proto.FormatFrame {
		return
	}
	fr, err := proto.DecodeFrame(body)
	if err != nil {
		return
	}
	switch fr.Type {
	case proto.FrameData:
		s.mu.Lock()
		delivered := s.rx.Recv(fr.Seq, fr.Payload)
		ack, ranges := s.rx.Ack()
		s.mu.Unlock()
		af := proto.Frame{Type: proto.FrameAck, Ack: ack, Ranges: ranges}
		s.sealSend(proto.FormatFrame, proto.EncodeFrame(&af))
		for _, b := range delivered {
			if m, err := proto.DecodeMsg(b); err == nil {
				s.onMessage(m)
			}
		}
	case proto.FrameAck:
		now := NowMs()
		s.mu.Lock()
		s.tx.OnAck(fr.Ack, fr.Ranges, now)
		rtx := s.tx.FastRetransmit(fr.Ranges, now)
		s.mu.Unlock()
		for i := range rtx {
			s.sealSend(proto.FormatFrame, proto.EncodeFrame(&rtx[i]))
		}
	case proto.FrameUnreliable:
		if m, err := proto.DecodeMsg(fr.Payload); err == nil {
			s.onMessage(m)
		}
	}
}

// Tick drives retransmission; call it on a fixed interval (e.g. 25ms).
func (s *Session) Tick() {
	s.mu.Lock()
	frames := s.tx.PollRetransmit(NowMs())
	s.tx.PurgeDead()
	s.mu.Unlock()
	for i := range frames {
		s.sealSend(proto.FormatFrame, proto.EncodeFrame(&frames[i]))
	}
}
