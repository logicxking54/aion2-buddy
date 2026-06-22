//go:build windows

package main

import (
	"context"

	"aion2tmp/internal/screen"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Screen is the Wails-bound façade over the screen-reading skill-bar monitor.
// It only reads the desktop framebuffer (never the game process), so it is
// invisible to anti-cheat. Methods are callable from window.go.main.Screen.*.
type Screen struct {
	ctx context.Context
	mon *screen.Monitor
}

// NewScreen wires the monitor's emitter to runtime.EventsEmit.
func NewScreen() *Screen {
	s := &Screen{}
	s.mon = screen.NewMonitor(func(event string, payload any) {
		if s.ctx != nil {
			runtime.EventsEmit(s.ctx, event, payload)
		}
	})
	return s
}

func (s *Screen) setContext(ctx context.Context) { s.ctx = ctx }

// SampleRects returns the average brightness (0..255) of each skill rectangle.
// One-shot — used by the calibration UI.
func (s *Screen) SampleRects(rects []screen.Rect) []float64 {
	return screen.SampleRects(rects)
}

// CaptureRectPreview returns a skill rectangle as a base64 PNG data URL so the
// calibration UI can show what the app is capturing.
func (s *Screen) CaptureRectPreview(x, y, w, h int) (string, error) {
	return screen.CapturePNG(x, y, w, h)
}

// StartSkillbar begins the sampling loop over the given per-skill rectangles;
// it emits skillbar:state events with per-skill {i, brightness, ready}, where
// i is the rectangle's position in the list. ready = brightness > threshold.
func (s *Screen) StartSkillbar(rects []screen.Rect, threshold float64, intervalMs int) {
	s.mon.Start(rects, threshold, intervalMs)
}

// StopSkillbar halts the sampling loop.
func (s *Screen) StopSkillbar() { s.mon.Stop() }

// SkillbarRunning reports whether the monitor loop is active.
func (s *Screen) SkillbarRunning() bool { return s.mon.IsRunning() }
