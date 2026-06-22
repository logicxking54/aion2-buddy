package proto

import (
	"bytes"
	"testing"
)

func TestMsgRoundTrip(t *testing.T) {
	conn := ConnId{SrcIP: [4]byte{10, 0, 0, 2}, SrcPort: 54321, DstIP: [4]byte{1, 2, 3, 4}, DstPort: 443}
	cases := []Msg{
		{Type: MsgConnect, Conn: conn},
		{Type: MsgConnected, Conn: conn},
		{Type: MsgShutdown, Conn: conn},
		{Type: MsgReset, Conn: conn},
		{Type: MsgData, Conn: conn, Payload: []byte("hello world")},
		{Type: MsgConnectFailed, Conn: conn, Payload: []byte("refused")},
		{Type: MsgPing, Seq: 7, TS: 123456},
		{Type: MsgPong, Seq: 7, TS: 123456},
	}
	for _, in := range cases {
		got, err := DecodeMsg(EncodeMsg(&in))
		if err != nil {
			t.Fatalf("decode %v: %v", in.Type, err)
		}
		if got.Type != in.Type || got.Conn != in.Conn || got.Seq != in.Seq || got.TS != in.TS {
			t.Fatalf("mismatch: in=%+v got=%+v", in, got)
		}
		if !bytes.Equal(got.Payload, in.Payload) {
			t.Fatalf("payload mismatch for %v: %q vs %q", in.Type, got.Payload, in.Payload)
		}
	}
}

func TestCryptoRoundTrip(t *testing.T) {
	hexKey, err := GenerateKeyHex()
	if err != nil {
		t.Fatal(err)
	}
	k, err := KeyFromHex(hexKey)
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := NewSessionID()
	body := []byte("the quick brown fox")

	pkt, err := k.Seal(sess, FormatFrame, body)
	if err != nil {
		t.Fatal(err)
	}
	// Header fields must be readable in the clear for relay routing.
	if id, ok := PeekKeyID(pkt); !ok || id != k.ID {
		t.Fatalf("key id peek mismatch")
	}
	if s, ok := PeekSessionID(pkt); !ok || s != sess {
		t.Fatalf("session id peek mismatch")
	}
	format, got, err := k.Open(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatFrame || !bytes.Equal(got, body) {
		t.Fatalf("open mismatch: format=%d body=%q", format, got)
	}

	// Wrong key must fail to open.
	other, _ := KeyFromHex("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	if _, _, err := other.Open(pkt); err == nil {
		t.Fatalf("expected open failure with wrong key")
	}
}

func TestKeyIDStable(t *testing.T) {
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(i)
	}
	k1, _ := NewKey(raw)
	k2, _ := NewKey(raw)
	if k1.ID != k2.ID {
		t.Fatalf("key id not stable")
	}
}
