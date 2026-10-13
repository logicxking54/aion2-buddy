//go:build windows

package main

import (
	"context"
	"time"

	"aion2tmp/internal/capture"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Capture is the Wails-bound façade over the packet-capture engine. Its
// exported methods are callable from the frontend (window.go.main.Capture.*),
// and engine events are forwarded to the frontend via the Wails event bus.
type Capture struct {
	ctx    context.Context
	engine *capture.Engine
	boost  *capture.GameBoost
}

// NewCapture wires the engine's event emitter to runtime.EventsEmit.
func NewCapture() *Capture {
	c := &Capture{boost: capture.NewGameBoost()}
	c.engine = capture.NewEngine(func(event string, payload any) {
		if c.ctx != nil {
			runtime.EventsEmit(c.ctx, event, payload)
		}
	})
	return c
}

func (c *Capture) setContext(ctx context.Context) {
	c.ctx = ctx
	_ = capture.BoostOwnProcess() // best effort; the packet loop is latency-critical
	go c.watchGame()
}

// watchGame polls for the Aion 2 process, pushes a game:status event to the UI
// so it can show whether the game is detected, and lets the game boost follow
// the process (apply on launch, forget on exit). game:boost is only sent when
// the boost state actually changes.
func (c *Capture) watchGame() {
	var last capture.GameBoostState
	for {
		if c.ctx != nil {
			pid := capture.GamePID()
			runtime.EventsEmit(c.ctx, "game:status", pid != 0)
			if st := c.boost.Tick(pid); st != last {
				last = st
				runtime.EventsEmit(c.ctx, "game:boost", st)
			}
		}
		time.Sleep(3 * time.Second)
	}
}

// SetAnimMask turns the "hide other players' skill animations" packet rewrite
// on or off. It only acts while capture is running and your caster is known.
func (c *Capture) SetAnimMask(on bool) {
	c.engine.SetAnimMask(on)
}

// SetGameBoost turns the game-process boost on or off: High priority and
// performance-core affinity for Aion2.exe while it runs. Off restores whatever
// the game had before this app changed it.
func (c *Capture) SetGameBoost(enabled bool) capture.GameBoostState {
	st := c.boost.SetEnabled(enabled, capture.GamePID())
	if c.ctx != nil {
		runtime.EventsEmit(c.ctx, "game:boost", st)
	}
	return st
}

// SetGameThreadIsolation is the boost's sub-switch: keep the game's main thread
// alone on one core, and every other game thread (plus this app) off that
// core, while the boost is applied. Off puts the threads back on the process's
// own cores.
func (c *Capture) SetGameThreadIsolation(on bool) capture.GameBoostState {
	st := c.boost.SetThreadIsolation(on, capture.GamePID())
	if c.ctx != nil {
		runtime.EventsEmit(c.ctx, "game:boost", st)
	}
	return st
}

// StartCapture begins interception with the given per-skill combat-speed config.
// Returns an error (rejected promise on the JS side) if startup fails.
func (c *Capture) StartCapture(skills []capture.SkillSpeed) error {
	return c.engine.Start(skills)
}

// StopCapture stops interception.
func (c *Capture) StopCapture() {
	c.engine.Stop()
}

// UpdateCapture hot-reloads the config without stopping capture.
func (c *Capture) UpdateCapture(skills []capture.SkillSpeed) {
	c.engine.SetConfig(skills)
}

// SetCatalog loads the full known-skill list so every cast can be logged,
// not just configured skills.
func (c *Capture) SetCatalog(entries []capture.CatalogEntry) {
	c.engine.SetCatalog(entries)
}

// SetCharacterNames restricts capture to your character(s); empty = show all.
func (c *Capture) SetCharacterNames(names []string) {
	c.engine.SetCharacterNames(names)
}

// SetCasterFilter sets the manual caster override. id == 0 returns to the
// backend's authoritative auto-detected local caster.
func (c *Capture) SetCasterFilter(id uint64) {
	c.engine.SetCasterFilter(id)
}

// IsCapturing reports whether capture is currently running.
func (c *Capture) IsCapturing() bool {
	return c.engine.IsRunning()
}
