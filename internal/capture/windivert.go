//go:build windows

// Package capture ports the pingmaker latency-compensation engine: it uses
// WinDivert to intercept inbound Aion 2 server packets and rewrite the
// combat-speed varint inside skill packets.
//
// windivert.go is a minimal pure-Go binding to WinDivert 2.x (no cgo), built
// on golang.org/x/sys/windows LazyDLL. Only the handful of functions the
// engine needs are bound.
package capture

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinDivert layers / params / flags (subset).
const (
	layerNetwork = 0

	paramQueueLength = 0
	paramQueueTime   = 1
	paramQueueSize   = 2

	flagSniff = 0x0001 // read-only capture (no re-injection required)

	invalidHandle = ^uintptr(0) // INVALID_HANDLE_VALUE
)

const shutdownBoth = 3 // WINDIVERT_SHUTDOWN_BOTH

var (
	winDivertDLL      = windows.NewLazyDLL("WinDivert.dll")
	procOpen          = winDivertDLL.NewProc("WinDivertOpen")
	procRecv          = winDivertDLL.NewProc("WinDivertRecv")
	procSend          = winDivertDLL.NewProc("WinDivertSend")
	procShutdown      = winDivertDLL.NewProc("WinDivertShutdown")
	procClose         = winDivertDLL.NewProc("WinDivertClose")
	procSetParam      = winDivertDLL.NewProc("WinDivertSetParam")
	procCalcChecksums = winDivertDLL.NewProc("WinDivertHelperCalcChecksums")
)

// Address mirrors WINDIVERT_ADDRESS (2.x) — 80 bytes. The bitfield block is
// packed into Flags; we only read a couple of bits and otherwise pass the
// struct straight back on send.
type Address struct {
	Timestamp int64
	Flags     uint32 // Layer:8, Event:8, Sniffed:1, Outbound:1, Loopback:1, Impostor:1, IPv6:1, IPChecksum:1, TCPChecksum:1, UDPChecksum:1, Reserved1:8
	Reserved2 uint32
	Union     [64]byte
}

// handle wraps a WinDivert HANDLE.
type handle uintptr

// openHandle opens a WinDivert NETWORK handle with the given filter.
// priority 0, the supplied flags (0 = intercept, flagSniff = read-only).
func openHandle(filter string, flags uint64) (handle, error) {
	f, err := windows.BytePtrFromString(filter)
	if err != nil {
		return 0, err
	}
	r, _, e := procOpen.Call(
		uintptr(unsafe.Pointer(f)),
		uintptr(layerNetwork),
		uintptr(0), // priority
		uintptr(flags),
	)
	if r == invalidHandle {
		return 0, fmt.Errorf("WinDivertOpen failed: %w", e)
	}
	return handle(r), nil
}

// recv blocks until a packet is captured, copying it into buf. Returns the
// number of bytes read and the WinDivert address.
func (h handle) recv(buf []byte) (int, Address, error) {
	var recvLen uint32
	var addr Address
	r, _, e := procRecv.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&recvLen)),
		uintptr(unsafe.Pointer(&addr)),
	)
	if r == 0 {
		return 0, addr, e
	}
	return int(recvLen), addr, nil
}

// send re-injects a packet (modified or not) using its original address.
func (h handle) send(packet []byte, addr *Address) error {
	var sendLen uint32
	r, _, e := procSend.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&packet[0])),
		uintptr(len(packet)),
		uintptr(unsafe.Pointer(&sendLen)),
		uintptr(unsafe.Pointer(addr)),
	)
	if r == 0 {
		return e
	}
	return nil
}

// shutdown stops queuing and unblocks any pending recv (returns ERROR_NO_DATA).
// Required on WinDivert 2.x — close() alone does not cancel a blocked recv.
func (h handle) shutdown() {
	procShutdown.Call(uintptr(h), uintptr(shutdownBoth))
}

// close shuts the handle. Call shutdown() first to unblock a waiting recv.
func (h handle) close() {
	procClose.Call(uintptr(h))
}

// setParam tunes a kernel queue parameter (best-effort).
func (h handle) setParam(param int, value uint64) {
	procSetParam.Call(uintptr(h), uintptr(param), uintptr(value))
}

// calcChecksums recomputes IP/TCP checksums after the payload was modified.
func calcChecksums(packet []byte, addr *Address) {
	procCalcChecksums.Call(
		uintptr(unsafe.Pointer(&packet[0])),
		uintptr(len(packet)),
		uintptr(unsafe.Pointer(addr)),
		uintptr(0), // flags 0 = recompute all
	)
}

// tune maximizes the kernel queue buffers (mirrors pingmaker's _tune_handle).
func (h handle) tune() {
	h.setParam(paramQueueLength, 16384)
	h.setParam(paramQueueSize, 33554432) // 32 MB
	h.setParam(paramQueueTime, 16000)    // 16 s (max)
}
