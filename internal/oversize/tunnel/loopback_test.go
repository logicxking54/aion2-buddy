package tunnel_test

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"aion2tmp/internal/oversize/proto"
	"aion2tmp/internal/oversize/relayd"
	"aion2tmp/internal/oversize/tunnel"
)

// TestLoopbackEndToEnd runs the whole tunnel data path on localhost without a
// TUN: a TCP echo server (stands in for the game server), the relay daemon, and
// the client tunnel. It validates proto + crypto + ARQ + relayd + tunnel
// together — the parts that can't be tested on the live VM from here.
func TestLoopbackEndToEnd(t *testing.T) {
	// 1. Echo server (the "game server").
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go io.Copy(c, c) // echo back
		}
	}()
	echoAddr := echo.Addr().(*net.TCPAddr)

	// 2. Relay daemon on a random UDP port with a fresh key.
	keyHex, err := proto.GenerateKeyHex()
	if err != nil {
		t.Fatal(err)
	}
	relayConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	relayAddr := relayConn.LocalAddr().String()
	relayConn.Close() // free the port for relayd to bind (small race, fine for test)
	go func() {
		_ = relayd.Run(relayAddr, keyHex, 2, nil) // dup=2 exercises dedup
	}()
	time.Sleep(100 * time.Millisecond) // let it bind

	// 3. Client tunnel.
	pingCh := make(chan int, 4)
	cli, err := tunnel.Dial(relayAddr, keyHex, func(ms int) {
		select {
		case pingCh <- ms:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	// 4. Open a tunneled flow to the echo server and round-trip data.
	conn := proto.ConnId{
		SrcIP:   [4]byte{10, 200, 0, 2},
		SrcPort: 50000,
		DstIP:   [4]byte{127, 0, 0, 1},
		DstPort: uint16(echoAddr.Port),
	}
	flow, err := cli.Open(conn, 5*time.Second)
	if err != nil {
		t.Fatalf("open flow: %v", err)
	}

	msg := []byte("hello through the tunnel")
	if _, err := flow.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := make([]byte, len(msg))
	if err := readFull(flow, got, 5*time.Second); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("echo mismatch: got %q want %q", got, msg)
	}

	// A larger payload exercises Data chunking + ARQ ordering.
	big := bytes.Repeat([]byte("ABCDEFGH"), 4000) // 32 KB > UDPSafePayload
	if _, err := flow.Write(big); err != nil {
		t.Fatalf("write big: %v", err)
	}
	gotBig := make([]byte, len(big))
	if err := readFull(flow, gotBig, 10*time.Second); err != nil {
		t.Fatalf("read big: %v", err)
	}
	if !bytes.Equal(gotBig, big) {
		t.Fatalf("big echo mismatch (len got=%d want=%d)", len(gotBig), len(big))
	}

	flow.Close()
}

// readFull reads exactly len(buf) bytes from f or fails after the deadline.
func readFull(f *tunnel.Flow, buf []byte, timeout time.Duration) error {
	type res struct {
		n   int
		err error
	}
	done := make(chan res, 1)
	go func() {
		n, err := io.ReadFull(f, buf)
		done <- res{n, err}
	}()
	select {
	case r := <-done:
		return r.err
	case <-time.After(timeout):
		return io.ErrNoProgress
	}
}
