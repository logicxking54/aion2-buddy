// Package tunnel is the client side of the Oversize Network tunnel: it dials the
// relay over UDP, runs the encrypted ARQ session, and presents each tunneled
// game flow as an io.ReadWriteCloser (so the WinTUN/netstack layer can io.Copy
// between a captured game connection and the tunnel). Pure Go / cross-platform —
// the loopback test exercises the full client↔relay path without a TUN.
package tunnel

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"aion2tmp/internal/oversize/arq"
	"aion2tmp/internal/oversize/proto"
)

// sendDup is how many copies of each upstream packet the client transmits, for
// loss resilience on the lossy client→relay leg. The relay already duplicates
// its downstream (AION2_RELAY_DUPLICATE); this makes the upstream — which
// carries latency-critical game inputs — symmetric, so a single lost packet is
// covered by its copy instead of stalling the whole in-order stream for a full
// RTO (~50-250ms). Combat packets are tiny, so the extra bandwidth is negligible.
const sendDup = 2

// Client is a connected tunnel to one relay.
type Client struct {
	conn   *net.UDPConn
	s      *arq.Session
	onPing func(ms int)

	mu      sync.Mutex
	flows   map[proto.ConnId]*Flow
	closed  bool
	stop    chan struct{}
	pingSeq uint64
}

// Dial connects to relayAddr (UDP) using the given hex key, starts the receive,
// retransmit and ping loops, and returns a ready Client. onPing (optional)
// receives round-trip latency samples in ms.
func Dial(relayAddr, keyHex string, onPing func(ms int)) (*Client, error) {
	key, err := proto.KeyFromHex(keyHex)
	if err != nil {
		return nil, err
	}
	ua, err := net.ResolveUDPAddr("udp", relayAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, ua)
	if err != nil {
		return nil, err
	}
	sid, err := proto.NewSessionID()
	if err != nil {
		conn.Close()
		return nil, err
	}
	c := &Client{
		conn:   conn,
		onPing: onPing,
		flows:  map[proto.ConnId]*Flow{},
		stop:   make(chan struct{}),
	}
	c.s = arq.New(key, sid, func(pkt []byte) {
		for i := 0; i < sendDup; i++ {
			conn.Write(pkt)
		}
	}, c.handleMsg)
	go c.recvLoop()
	go c.tickLoop()
	go c.pingLoop()
	return c, nil
}

// Close tears down the tunnel and fails all flows.
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.stop)
	flows := make([]*Flow, 0, len(c.flows))
	for _, f := range c.flows {
		flows = append(flows, f)
	}
	c.flows = map[proto.ConnId]*Flow{}
	c.mu.Unlock()
	for _, f := range flows {
		f.closeRead()
	}
	c.conn.Close()
}

// Open starts a tunneled TCP flow to conn.DstAddr and blocks until the relay
// confirms it (or the timeout elapses / it fails).
func (c *Client) Open(conn proto.ConnId, timeout time.Duration) (*Flow, error) {
	f := &Flow{c: c, conn: conn, in: make(chan []byte, 1024), connected: make(chan struct{}), failed: make(chan string, 1)}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("tunnel closed")
	}
	c.flows[conn] = f
	c.mu.Unlock()

	c.s.SendReliable(proto.Msg{Type: proto.MsgConnect, Conn: conn})

	select {
	case <-f.connected:
		return f, nil
	case reason := <-f.failed:
		c.removeFlow(conn)
		return nil, errors.New("relay connect failed: " + reason)
	case <-time.After(timeout):
		c.removeFlow(conn)
		return nil, errors.New("relay connect timeout")
	case <-c.stop:
		return nil, errors.New("tunnel closed")
	}
}

func (c *Client) removeFlow(conn proto.ConnId) {
	c.mu.Lock()
	delete(c.flows, conn)
	c.mu.Unlock()
}

func (c *Client) recvLoop() {
	buf := make([]byte, 65535)
	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			return
		}
		c.s.OnPacket(append([]byte(nil), buf[:n]...))
	}
}

func (c *Client) tickLoop() {
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			c.s.Tick()
		}
	}
}

func (c *Client) pingLoop() {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			c.mu.Lock()
			c.pingSeq++
			seq := c.pingSeq
			c.mu.Unlock()
			c.s.SendUnreliable(proto.Msg{Type: proto.MsgPing, Seq: seq, TS: arq.NowMs()})
		}
	}
}

func (c *Client) handleMsg(m proto.Msg) {
	switch m.Type {
	case proto.MsgConnected:
		if f := c.flow(m.Conn); f != nil {
			f.markConnected()
		}
	case proto.MsgConnectFailed:
		if f := c.flow(m.Conn); f != nil {
			select {
			case f.failed <- string(m.Payload):
			default:
			}
		}
	case proto.MsgData:
		if f := c.flow(m.Conn); f != nil {
			f.deliver(m.Payload)
		}
	case proto.MsgShutdown, proto.MsgReset:
		if f := c.flow(m.Conn); f != nil {
			f.closeRead()
		}
	case proto.MsgPong:
		if c.onPing != nil {
			c.onPing(int(arq.NowMs() - m.TS))
		}
	}
}

func (c *Client) flow(conn proto.ConnId) *Flow {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flows[conn]
}

// Flow is one tunneled TCP connection, presented as an io.ReadWriteCloser.
type Flow struct {
	c    *Client
	conn proto.ConnId

	in        chan []byte
	connected chan struct{}
	failed    chan string

	rbuf []byte

	once     sync.Once
	connOnce sync.Once
}

func (f *Flow) markConnected() { f.connOnce.Do(func() { close(f.connected) }) }

func (f *Flow) deliver(p []byte) {
	select {
	case f.in <- p:
	case <-f.c.stop:
	}
}

// closeRead signals EOF to readers (relay closed its side or tunnel went away).
func (f *Flow) closeRead() { f.once.Do(func() { close(f.in) }) }

// Read returns tunneled bytes from the relay; io.EOF after the relay closes.
func (f *Flow) Read(p []byte) (int, error) {
	for len(f.rbuf) == 0 {
		b, ok := <-f.in
		if !ok {
			return 0, io.EOF
		}
		f.rbuf = b
	}
	n := copy(p, f.rbuf)
	f.rbuf = f.rbuf[n:]
	return n, nil
}

// Write sends bytes through the tunnel to the game server.
func (f *Flow) Write(p []byte) (int, error) {
	// Chunk to keep each datagram under the MTU-safe payload size.
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > proto.UDPSafePayload {
			n = proto.UDPSafePayload
		}
		f.c.s.SendReliable(proto.Msg{Type: proto.MsgData, Conn: f.conn, Payload: append([]byte(nil), p[:n]...)})
		p = p[n:]
		total += n
	}
	return total, nil
}

// Close tells the relay to close this flow (TCP FIN) and stops local reads.
func (f *Flow) Close() error {
	f.c.s.SendReliable(proto.Msg{Type: proto.MsgShutdown, Conn: f.conn})
	f.c.removeFlow(f.conn)
	f.closeRead()
	return nil
}
