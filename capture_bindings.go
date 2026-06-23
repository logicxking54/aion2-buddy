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
}

// NewCapture wires the engine's event emitter to runtime.EventsEmit.
func NewCapture() *Capture {
	c := &Capture{}
	c.engine = capture.NewEngine(func(event string, payload any) {
		if c.ctx != nil {
			runtime.EventsEmit(c.ctx, event, payload)
		}
	})
	return c
}

func (c *Capture) setContext(ctx context.Context) {
	c.ctx = ctx
	go c.watchGame()
}

// watchGame polls for the Aion 2 process and pushes a game:status event to the
// UI so the sidebar can show whether the game is detected.
func (c *Capture) watchGame() {
	for {
		if c.ctx != nil {
			runtime.EventsEmit(c.ctx, "game:status", capture.GameRunning())
		}
		time.Sleep(3 * time.Second)
	}
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

// SetAuto toggles auto-mode and sets the ping→add curve (add = base + perMs*ping).
func (c *Capture) SetAuto(enabled bool, base float64, perMs float64) {
	c.engine.SetAuto(enabled, base, perMs)
}

// SetInspect toggles the packet inspector (all=true dumps every inbound packet,
// else only skill-cast packets).
func (c *Capture) SetInspect(on bool, all bool) {
	c.engine.SetInspect(on, all)
}

// SetComboTest toggles the experimental combo-chain next-id rewrite
// (Burst→Ice Chain→Pyroclasm).
func (c *Capture) SetComboTest(on bool) {
	c.engine.SetComboTest(on)
}

// SetDecode toggles full per-cast field decoding to the log.
func (c *Capture) SetDecode(on bool) {
	c.engine.SetDecode(on)
}

// SetAggressiveParser toggles compact/framed fallback speed matching.
func (c *Capture) SetAggressiveParser(on bool) {
	c.engine.SetAggressiveParser(on)
}

// SetCasterMask toggles the FPS-saver mask: the engine rewrites every OTHER
// player's cast skill_id to a no-VFX Dodge skill so the client renders nothing
// heavy for them. keepCaster is your detected caster (0 = not yet known → no-op);
// dodgeID is the Dodge skill id to write.
func (c *Capture) SetCasterMask(on bool, keepCaster uint64, dodgeID uint32) {
	c.engine.SetCasterMask(on, keepCaster, dodgeID)
}

// SetCasterFilter sets the engine-level caster filter: when id != 0, only that
// caster's casts are processed (logged + speed-modified) and every other caster
// is ignored. id == 0 clears it. The frontend pushes the (auto/manual) field value.
func (c *Capture) SetCasterFilter(id uint64) {
	c.engine.SetCasterFilter(id)
}

// IsCapturing reports whether capture is currently running.
func (c *Capture) IsCapturing() bool {
	return c.engine.IsRunning()
}
