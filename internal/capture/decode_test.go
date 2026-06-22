//go:build windows

package capture

import "testing"

func TestLZ4DecompressBlock(t *testing.T) {
	cases := []struct {
		name   string
		src    []byte
		dstLen int
		want   string
	}{
		// token 0x40 = 4 literals, 0 match; then the 4 literal bytes.
		{"literals-only", []byte{0x40, 'A', 'B', 'C', 'D'}, 4, "ABCD"},
		// literals "ABCD", then match offset=4 len=4 (token low nibble 0 → +4).
		{"with-match", []byte{0x40, 'A', 'B', 'C', 'D', 0x04, 0x00}, 8, "ABCDABCD"},
	}
	for _, c := range cases {
		got, ok := lz4DecompressBlock(c.src, c.dstLen)
		if !ok {
			t.Fatalf("%s: decompress failed", c.name)
		}
		if string(got) != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestLZ4DecompressBlockRejectsGarbage(t *testing.T) {
	// A match offset pointing before the output start must fail, not panic.
	if _, ok := lz4DecompressBlock([]byte{0x0f, 0xff, 0x00}, 64); ok {
		t.Fatal("expected malformed block to be rejected")
	}
}

func TestDecodeMessagesNoCrash(t *testing.T) {
	// Garbage / partial frames must not panic and must terminate.
	inputs := [][]byte{
		nil,
		{0x00, 0x00, 0x00},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		{0x05, 0x04, 0x38, 0x01, 0x02}, // a too-short "damage"-looking frame
	}
	for i, in := range inputs {
		_ = decodeMessages(in, map[uint32]string{}) // must return without panic
		_ = i
	}
}
