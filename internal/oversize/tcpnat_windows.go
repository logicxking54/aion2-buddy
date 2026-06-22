//go:build windows

package oversize

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"

	"aion2tmp/internal/oversize/ipstack"
	"aion2tmp/internal/oversize/proto"
	"aion2tmp/internal/oversize/tunnel"
)

// tcpNAT is the userspace TCP endpoint behind the TUN. The OS believes it is
// talking directly to each game server; we synthesize the server side (SYN-ACK,
// ACKs, FIN) and bridge the byte stream through the encrypted tunnel. Ported
// from the reference aion2-proxy/src/tun_windows.rs.
//
// The TUN↔OS leg is local and lossless, so this NAT needs no retransmission of
// its own; the loss-prone client↔relay leg is handled by the tunnel's ARQ.
type tcpNAT struct {
	dev *tunDevice
	cli *tunnel.Client
	log func(string)

	mu    sync.Mutex
	conns map[proto.ConnId]*natEntry
	stop  chan struct{}
}

const natWindow = 65535

type natEntry struct {
	mu          sync.Mutex
	conn        proto.ConnId // client->server orientation
	ourSeq      uint32       // next seq we (the "server") will send
	clientNext  uint32       // next seq we expect from the client
	connected   bool
	established bool
	closed      bool
	pending     [][]byte // client->server data buffered until the tunnel connects
	flow        *tunnel.Flow
}

func newTCPNAT(dev *tunDevice, cli *tunnel.Client, log func(string)) *tcpNAT {
	return &tcpNAT{dev: dev, cli: cli, log: log, conns: map[proto.ConnId]*natEntry{}, stop: make(chan struct{})}
}

func (n *tcpNAT) run() {
	for {
		select {
		case <-n.stop:
			return
		default:
		}
		pkt, err := n.dev.readPacket()
		if err != nil {
			return // session ended
		}
		n.handle(pkt)
	}
}

func (n *tcpNAT) stopNAT() {
	close(n.stop)
	n.mu.Lock()
	conns := n.conns
	n.conns = map[proto.ConnId]*natEntry{}
	n.mu.Unlock()
	for _, e := range conns {
		if e.flow != nil {
			e.flow.Close()
		}
	}
}

func (n *tcpNAT) handle(pkt []byte) {
	t, ok := ipstack.ParseTCP(pkt)
	if !ok {
		return // only TCP is tunneled; drop the rest
	}
	conn := proto.ConnId{SrcIP: t.SrcIP, SrcPort: t.SrcPort, DstIP: t.DstIP, DstPort: t.DstPort}

	n.mu.Lock()
	e := n.conns[conn]
	n.mu.Unlock()

	syn := t.Flags&ipstack.FlagSYN != 0
	ack := t.Flags&ipstack.FlagACK != 0

	if syn && !ack {
		if e == nil {
			e = n.openConn(conn, t.Seq)
		} else {
			e.mu.Lock()
			n.sendSynAck(e) // retransmitted SYN
			e.mu.Unlock()
		}
		return
	}
	if e == nil {
		return // unknown flow
	}
	if t.Flags&ipstack.FlagRST != 0 {
		n.drop(conn, e, false)
		return
	}
	if t.Flags&ipstack.FlagFIN != 0 {
		e.mu.Lock()
		e.clientNext = t.Seq + uint32(len(t.Payload)) + 1
		n.sendAck(e)
		flow := e.flow
		e.mu.Unlock()
		if flow != nil {
			flow.Close() // half-close toward the relay
		}
		return
	}

	if ack && !e.established {
		e.mu.Lock()
		e.established = true
		e.mu.Unlock()
	}

	if len(t.Payload) > 0 {
		e.mu.Lock()
		switch {
		case t.Seq == e.clientNext:
			e.clientNext += uint32(len(t.Payload))
			data := append([]byte(nil), t.Payload...)
			n.sendAck(e)
			if e.connected && e.flow != nil {
				flow := e.flow
				e.mu.Unlock()
				flow.Write(data)
			} else {
				e.pending = append(e.pending, data)
				e.mu.Unlock()
			}
		default:
			// Retransmit or out-of-order: re-ACK what we have, drop the data.
			n.sendAck(e)
			e.mu.Unlock()
		}
	}
}

// openConn creates NAT state, SYN-ACKs immediately, and opens the tunnel flow.
func (n *tcpNAT) openConn(conn proto.ConnId, clientISN uint32) *natEntry {
	e := &natEntry{conn: conn, ourSeq: randSeq(), clientNext: clientISN + 1}
	n.mu.Lock()
	n.conns[conn] = e
	n.mu.Unlock()

	e.mu.Lock()
	n.sendSynAck(e)
	e.mu.Unlock()

	go n.connectTunnel(e)
	return e
}

func (n *tcpNAT) connectTunnel(e *natEntry) {
	flow, err := n.cli.Open(e.conn, 10*time.Second)
	if err != nil {
		if n.log != nil {
			n.log("tunnel open failed: " + err.Error())
		}
		n.sendReset(e)
		n.drop(e.conn, e, false)
		return
	}
	e.mu.Lock()
	e.flow = flow
	e.connected = true
	pending := e.pending
	e.pending = nil
	e.mu.Unlock()

	for _, p := range pending {
		flow.Write(p)
	}
	// Pump relay->client bytes back to the OS.
	buf := make([]byte, proto.UDPSafePayload)
	for {
		nr, rerr := flow.Read(buf)
		if nr > 0 {
			e.mu.Lock()
			n.sendData(e, buf[:nr])
			e.mu.Unlock()
		}
		if rerr != nil {
			e.mu.Lock()
			n.sendFin(e)
			e.mu.Unlock()
			n.drop(e.conn, e, true)
			return
		}
	}
}

func (n *tcpNAT) drop(conn proto.ConnId, e *natEntry, keepFlow bool) {
	n.mu.Lock()
	delete(n.conns, conn)
	n.mu.Unlock()
	if !keepFlow && e.flow != nil {
		e.flow.Close()
	}
}

// ── packet senders (caller holds e.mu) ────────────────────────
// Outgoing packets are "from the server" toward the client, so src/dst are the
// reverse of the client->server ConnId.

func (n *tcpNAT) sendSynAck(e *natEntry) {
	pkt := ipstack.BuildTCP(e.conn.DstIP, e.conn.SrcIP, e.conn.DstPort, e.conn.SrcPort,
		e.ourSeq, e.clientNext, ipstack.FlagSYN|ipstack.FlagACK, natWindow, nil, true)
	e.ourSeq++ // SYN consumes one sequence number
	n.dev.writePacket(pkt)
}

func (n *tcpNAT) sendAck(e *natEntry) {
	pkt := ipstack.BuildTCP(e.conn.DstIP, e.conn.SrcIP, e.conn.DstPort, e.conn.SrcPort,
		e.ourSeq, e.clientNext, ipstack.FlagACK, natWindow, nil, false)
	n.dev.writePacket(pkt)
}

func (n *tcpNAT) sendData(e *natEntry, payload []byte) {
	pkt := ipstack.BuildTCP(e.conn.DstIP, e.conn.SrcIP, e.conn.DstPort, e.conn.SrcPort,
		e.ourSeq, e.clientNext, ipstack.FlagACK|ipstack.FlagPSH, natWindow, payload, false)
	e.ourSeq += uint32(len(payload))
	n.dev.writePacket(pkt)
}

func (n *tcpNAT) sendFin(e *natEntry) {
	pkt := ipstack.BuildTCP(e.conn.DstIP, e.conn.SrcIP, e.conn.DstPort, e.conn.SrcPort,
		e.ourSeq, e.clientNext, ipstack.FlagFIN|ipstack.FlagACK, natWindow, nil, false)
	e.ourSeq++
	n.dev.writePacket(pkt)
}

func (n *tcpNAT) sendReset(e *natEntry) {
	pkt := ipstack.BuildTCP(e.conn.DstIP, e.conn.SrcIP, e.conn.DstPort, e.conn.SrcPort,
		e.ourSeq, e.clientNext, ipstack.FlagRST|ipstack.FlagACK, 0, nil, false)
	n.dev.writePacket(pkt)
}

func randSeq() uint32 {
	var b [4]byte
	rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])
}
