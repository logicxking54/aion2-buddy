//go:build windows

// Package screen reads the desktop framebuffer to tell whether in-game skill
// slots are lit (ready) or darkened (on cooldown). It only reads the screen —
// it never opens, reads, or writes the game process — so it's invisible to
// anti-cheat. Single-monitor / primary-display coordinates (physical pixels).
package screen

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
)

const (
	srcCopy      = 0x00CC0020
	biRGB        = 0
	dibRGBColors = 0
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// capture grabs a screen rectangle and returns top-down BGRA pixels (4 bytes/px).
func capture(x, y, w, h int) ([]byte, error) {
	if w < 1 || h < 1 {
		return nil, errors.New("empty rectangle")
	}
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return nil, errors.New("GetDC failed")
	}
	defer procReleaseDC.Call(0, hdc)

	memDC, _, _ := procCreateCompatibleDC.Call(hdc)
	if memDC == 0 {
		return nil, errors.New("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memDC)

	bmp, _, _ := procCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, errors.New("CreateCompatibleBitmap failed")
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(memDC, bmp)
	if ret, _, _ := procBitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h), hdc, uintptr(int32(x)), uintptr(int32(y)), srcCopy); ret == 0 {
		procSelectObject.Call(memDC, old)
		return nil, errors.New("BitBlt failed")
	}
	procSelectObject.Call(memDC, old) // deselect before GetDIBits

	bi := bitmapInfo{}
	bi.Header.Size = uint32(unsafe.Sizeof(bi.Header))
	bi.Header.Width = int32(w)
	bi.Header.Height = -int32(h) // negative = top-down rows
	bi.Header.Planes = 1
	bi.Header.BitCount = 32
	bi.Header.Compression = biRGB

	buf := make([]byte, w*h*4)
	ret, _, _ := procGetDIBits.Call(memDC, bmp, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), dibRGBColors)
	if ret == 0 {
		return nil, errors.New("GetDIBits failed")
	}
	return buf, nil
}

// SlotState is one skill's result emitted to the UI. Index is the position of
// the skill's rectangle in the list passed to the monitor.
type SlotState struct {
	Index      int     `json:"i"`
	Brightness float64 `json:"brightness"` // 0..255 average luminance
	Ready      bool    `json:"ready"`      // brightness > threshold
}

// Rect is one skill's screen rectangle (physical pixels, primary monitor).
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// brightnessOf returns the average luminance (0..255) of one rectangle.
func brightnessOf(r Rect) float64 {
	buf, err := capture(r.X, r.Y, r.W, r.H)
	if err != nil || len(buf) == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < len(buf); i += 4 {
		sum += (float64(buf[i]) + float64(buf[i+1]) + float64(buf[i+2])) / 3.0
	}
	return sum / float64(len(buf)/4)
}

// SampleRects returns the average brightness (0..255) of each rectangle.
// One-shot — used by the calibration UI.
func SampleRects(rects []Rect) []float64 {
	out := make([]float64, len(rects))
	for i, r := range rects {
		out[i] = brightnessOf(r)
	}
	return out
}

// CapturePNG returns the rectangle as a base64 data URL, for the calibration
// preview thumbnail so the user can line the box up with the in-game bar.
func CapturePNG(x, y, w, h int) (string, error) {
	buf, err := capture(x, y, w, h)
	if err != nil {
		return "", err
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(buf); i += 4 {
		img.Pix[i] = buf[i+2]   // R <- BGRA.R
		img.Pix[i+1] = buf[i+1] // G
		img.Pix[i+2] = buf[i]   // B
		img.Pix[i+3] = 255
	}
	var bb bytes.Buffer
	if err := png.Encode(&bb, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(bb.Bytes()), nil
}

// Monitor samples a skill bar on a timer and emits per-slot ready states.
type Monitor struct {
	mu      sync.Mutex
	stop    chan struct{}
	running bool
	emit    func(event string, payload any)
}

// NewMonitor wires the result emitter (→ Wails event bus).
func NewMonitor(emit func(event string, payload any)) *Monitor {
	return &Monitor{emit: emit}
}

// Start (re)starts the sampling loop over the given per-skill rectangles.
func (m *Monitor) Start(rects []Rect, threshold float64, intervalMs int) {
	m.Stop()
	if intervalMs < 50 {
		intervalMs = 50
	}
	m.mu.Lock()
	m.stop = make(chan struct{})
	m.running = true
	stop := m.stop
	m.mu.Unlock()

	go func() {
		t := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				vals := SampleRects(rects)
				states := make([]SlotState, len(vals))
				for i, v := range vals {
					states[i] = SlotState{Index: i, Brightness: v, Ready: v > threshold}
				}
				if m.emit != nil {
					m.emit("skillbar:state", states)
				}
			}
		}
	}()
}

// Stop halts the sampling loop (no-op if not running).
func (m *Monitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		close(m.stop)
		m.running = false
	}
}

// IsRunning reports whether the monitor loop is active.
func (m *Monitor) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}
