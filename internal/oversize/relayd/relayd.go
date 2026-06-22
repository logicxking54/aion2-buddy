// Package relayd is the Oversize Network relay engine: it terminates the
// encrypted UDP tunnel from the client, and for each tunneled flow opens a real
// TCP connection to the requested destination (the game server) and relays
// bytes both ways. It runs on the Taiwan VM via cmd/aion2-relay, but is pure Go
// and so also runs on Windows for the loopback integration test.
package relayd

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"aion2tmp/internal/oversize/arq"
	"aion2tmp/internal/oversize/proto"
)

// Logf is an optional logger.
type Logf func(format string, args ...any)

// Server is the relay. One shared UDP socket serves all client sessions.
type Server struct {
	conn *net.UDPConn
	key  *proto.Key
	dup  int
	logf Logf

	mu       sync.Mutex
	sessions map[proto.SessionID]*clientSession
}

// Run binds listenAddr (e.g. "0.0.0.0:443"), loads the 32-byte hex key, and
// serves until the socket errors. dup>1 sends each downstream datagram dup times
// (loss resilience). It blocks.
func Run(listenAddr, keyHex string, dup int, logf Logf) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	key, err := proto.KeyFromHex(keyHex)
	if err != nil {
		return fmt.Errorf("relay key: %w", err)
	}
	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	if dup < 1 {
		dup = 1
	}
	s := &Server{conn: conn, key: key, dup: dup, logf: logf, sessions: map[proto.SessionID]*clientSession{}}
	logf("relay listening on %s (dup=%d)", listenAddr, dup)
	go s.retransmitLoop()
	return s.serve()
}

func (s *Server) serve() error {
	buf := make([]byte, 65535)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return err
		}
		packet := append([]byte(nil), buf[:n]...)
		s.dispatch(packet, from)
	}
}

func (s *Server) dispatch(packet []byte, from *net.UDPAddr) {
	keyID, ok := proto.PeekKeyID(packet)
	if !ok || keyID != s.key.ID {
		return // not for us / unknown key
	}
	sid, ok := proto.PeekSessionID(packet)
	if !ok {
		return
	}
	cs := s.session(sid, from)
	cs.setAddr(from)
	cs.s.OnPacket(packet)
}

func (s *Server) session(sid proto.SessionID, from *net.UDPAddr) *clientSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cs, ok := s.sessions[sid]; ok {
		return cs
	}
	cs := &clientSession{srv: s, addr: from, conns: map[proto.ConnId]*relayConn{}}
	cs.s = arq.New(s.key, sid, cs.sendPacket, cs.handleMsg)
	s.sessions[sid] = cs
	s.logf("new session %x from %s", sid[:], from)
	return cs
}

func (s *Server) retransmitLoop() {
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		sessions := make([]*clientSession, 0, len(s.sessions))
		for _, cs := range s.sessions {
			sessions = append(sessions, cs)
		}
		s.mu.Unlock()
		for _, cs := range sessions {
			cs.s.Tick()
		}
	}
}

// clientSession holds one client's ARQ session and its live TCP connections.
type clientSession struct {
	srv *Server
	s   *arq.Session

	addrMu sync.Mutex
	addr   *net.UDPAddr

	mu    sync.Mutex
	conns map[proto.ConnId]*relayConn
}

func (cs *clientSession) setAddr(a *net.UDPAddr) {
	cs.addrMu.Lock()
	cs.addr = a
	cs.addrMu.Unlock()
}

// sendPacket writes a sealed packet to the client's latest address, dup times.
func (cs *clientSession) sendPacket(pkt []byte) {
	cs.addrMu.Lock()
	addr := cs.addr
	cs.addrMu.Unlock()
	if addr == nil {
		return
	}
	for i := 0; i < cs.srv.dup; i++ {
		cs.srv.conn.WriteToUDP(pkt, addr)
	}
}

func (cs *clientSession) handleMsg(m proto.Msg) {
	switch m.Type {
	case proto.MsgConnect:
		go cs.dial(m.Conn)
	case proto.MsgData:
		cs.mu.Lock()
		rc := cs.conns[m.Conn]
		cs.mu.Unlock()
		if rc != nil {
			rc.write(m.Payload)
		}
	case proto.MsgShutdown, proto.MsgReset:
		cs.mu.Lock()
		rc := cs.conns[m.Conn]
		cs.mu.Unlock()
		if rc != nil {
			rc.close()
		}
	case proto.MsgPing:
		cs.s.SendUnreliable(proto.Msg{Type: proto.MsgPong, Seq: m.Seq, TS: m.TS})
	}
}

func (cs *clientSession) dial(conn proto.ConnId) {
	dst := conn.DstAddr().String()
	cs.srv.logf("flow -> %s", dst)
	tcp, err := net.DialTimeout("tcp", dst, 10*time.Second)
	if err != nil {
		cs.srv.logf("flow -> %s FAILED: %v", dst, err)
		cs.s.SendReliable(proto.Msg{Type: proto.MsgConnectFailed, Conn: conn, Payload: []byte(err.Error())})
		return
	}
	if t, ok := tcp.(*net.TCPConn); ok {
		t.SetNoDelay(true)
	}
	rc := &relayConn{tcp: tcp, out: make(chan []byte, 256), done: make(chan struct{})}
	cs.mu.Lock()
	cs.conns[conn] = rc
	cs.mu.Unlock()

	cs.s.SendReliable(proto.Msg{Type: proto.MsgConnected, Conn: conn})
	go rc.writer()
	go cs.readFromGame(conn, rc)
}

// readFromGame pumps game-server bytes back to the client as Data messages.
func (cs *clientSession) readFromGame(conn proto.ConnId, rc *relayConn) {
	buf := make([]byte, proto.UDPSafePayload)
	for {
		n, err := rc.tcp.Read(buf)
		if n > 0 {
			cs.s.SendReliable(proto.Msg{Type: proto.MsgData, Conn: conn, Payload: append([]byte(nil), buf[:n]...)})
		}
		if err != nil {
			if isEOF(err) {
				cs.s.SendReliable(proto.Msg{Type: proto.MsgShutdown, Conn: conn})
			} else {
				cs.s.SendReliable(proto.Msg{Type: proto.MsgReset, Conn: conn})
			}
			break
		}
	}
	rc.close()
	cs.mu.Lock()
	delete(cs.conns, conn)
	cs.mu.Unlock()
}

// relayConn is one real TCP connection to a game server, with a serialized
// write queue so the receive loop never blocks on a slow socket.
type relayConn struct {
	tcp       net.Conn
	out       chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func (rc *relayConn) write(p []byte) {
	select {
	case rc.out <- p:
	case <-rc.done:
	}
}

func (rc *relayConn) writer() {
	for {
		select {
		case p := <-rc.out:
			if _, err := rc.tcp.Write(p); err != nil {
				rc.close()
				return
			}
		case <-rc.done:
			return
		}
	}
}

func (rc *relayConn) close() {
	rc.closeOnce.Do(func() {
		close(rc.done)
		rc.tcp.Close()
	})
}

func isEOF(err error) bool {
	return errors.Is(err, io.EOF)
}
