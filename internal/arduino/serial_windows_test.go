//go:build windows

package arduino

import (
	"os"
	"testing"
	"time"
)

func TestHardwareHandshake(t *testing.T) {
	if os.Getenv("ARDUINO_HARDWARE_TEST") != "1" {
		t.Skip("set ARDUINO_HARDWARE_TEST=1 to use the connected keyboard board")
	}
	port, diagnostics := Detect()
	if port == "" {
		t.Fatalf("board not found: %v", diagnostics)
	}
	board := New()
	if err := board.Open(port, 115200); err != nil {
		t.Fatal(err)
	}
	defer board.Close()
	if err := board.Handshake(6 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := board.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("handshake succeeded on %s", port)
}
