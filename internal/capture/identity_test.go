//go:build windows

package capture

import (
	"encoding/hex"
	"testing"
)

func TestScanLocalIdentityCapturedNameRecord(t *testing.T) {
	// Captured in a two-Sorcerer party. d7 50 is varint 10327, and 37 has
	// the name-present bit set. This is Mali茉莉, not the user's Maliwan.
	data, err := hex.DecodeString("3336d7505fb1fb1a370a4d616c69e88c89e88e89")
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := scanLocalIdentity(data, nil)
	if !ok {
		t.Fatal("self identity was not decoded")
	}
	if binding.actorID != 10327 || binding.name != "Mali茉莉" {
		t.Fatalf("identity = %#v, want actor 10327 / Mali茉莉", binding)
	}
}

func TestScanLocalIdentityUsesMaskBitNotLiteralValue(t *testing.T) {
	data := []byte{
		0x33, 0x36, 0x2a,
		0x00, 0x00, 0x00, 0x00, // mask1
		0x03, // mask2: name bit plus another flag
		0x03, 'B', 'o', 'b',
	}
	binding, ok := scanLocalIdentity(data, nil)
	if !ok || binding.actorID != 42 || binding.name != "Bob" {
		t.Fatalf("identity = %#v, ok=%v", binding, ok)
	}
}

func TestScanLocalIdentityRejectsOtherPlayerRecord(t *testing.T) {
	data := []byte{
		0x45, 0x36, 0x2a,
		0x00, 0x00, 0x00, 0x00,
		0x01, 0x03, 'B', 'o', 'b',
	}
	if binding, ok := scanLocalIdentity(data, nil); ok {
		t.Fatalf("other-player record decoded as local: %#v", binding)
	}
}

func TestLocalIdentityScannerHandlesTCPSplit(t *testing.T) {
	data, _ := hex.DecodeString("3336d7505fb1fb1a370a4d616c69e88c89e88e89")
	var scanner localIdentityScanner
	if binding, ok := scanner.feed(data[:9], nil); ok {
		t.Fatalf("partial record decoded early: %#v", binding)
	}
	binding, ok := scanner.feed(data[9:], nil)
	if !ok || binding.actorID != 10327 || binding.name != "Mali茉莉" {
		t.Fatalf("split identity = %#v, ok=%v", binding, ok)
	}
}

func TestLocalIdentityScannerNoHeapCostAfterWarmup(t *testing.T) {
	payload := make([]byte, 1500)
	for i := range payload {
		payload[i] = 0x55
	}
	var scanner localIdentityScanner
	scanner.feed(payload, nil) // allocate the reusable tail once
	if allocs := testing.AllocsPerRun(1000, func() { scanner.feed(payload, nil) }); allocs != 0 {
		t.Fatalf("identity scan allocated %.1f objects per ordinary packet", allocs)
	}
}
