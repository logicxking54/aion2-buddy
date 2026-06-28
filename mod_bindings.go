//go:build windows

package main

import (
	"context"

	"aion2tmp/internal/gamemod"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Mod is the Wails-bound façade over file-level game mods (currently the NC
// intro remover). Exported methods are callable from the frontend
// (window.go.main.Mod.*).
type Mod struct {
	ctx context.Context
}

// NewMod creates the Mod binding.
func NewMod() *Mod {
	return &Mod{}
}

func (m *Mod) setContext(ctx context.Context) {
	m.ctx = ctx
}

// DetectGamePath returns the auto-detected <game>\Aion2\Content\Movies folder,
// or "" if the game couldn't be located.
func (m *Mod) DetectGamePath() string {
	return gamemod.DetectMoviesDir()
}

// IntroStatus reports where the game is and whether the intro is currently removed.
func (m *Mod) IntroStatus() gamemod.Status {
	return gamemod.GetStatus()
}

// emitLog forwards a mod file-operation line to the frontend log box.
func (m *Mod) emitLog(s string) {
	if m.ctx != nil {
		runtime.EventsEmit(m.ctx, "mod:log", s)
	}
}

// RemoveIntro swaps the NC intro movie for a no-op stub (backing up the original).
// Each file operation is logged to the UI log box via the "mod:log" event.
func (m *Mod) RemoveIntro() (gamemod.Status, error) {
	return gamemod.RemoveIntro(m.emitLog)
}

// RestoreIntro restores the original intro movie from the backup.
func (m *Mod) RestoreIntro() (gamemod.Status, error) {
	return gamemod.RestoreIntro(m.emitLog)
}

// --- System tweaks (ported from the SkyFields auto-optimizer) ---------------
// Each is a reversible toggle: Apply* records the original state and changes it;
// Revert* restores it. *Status reports whether it's currently applied. File
// operations are logged to the UI via the "mod:log" event.

// TCPStatus reports whether the low-latency TCP tweaks are applied.
func (m *Mod) TCPStatus() gamemod.TweakStatus {
	return gamemod.TCPStatus()
}

// ApplyTCP disables Nagle/delayed-ACK and sets the low-latency TCP values.
func (m *Mod) ApplyTCP() (gamemod.TweakStatus, error) {
	return gamemod.ApplyTCP(m.emitLog)
}

// RevertTCP restores the original TCP settings from the backup.
func (m *Mod) RevertTCP() (gamemod.TweakStatus, error) {
	return gamemod.RevertTCP(m.emitLog)
}

// NICStatus reports whether the network-adapter tuning is applied.
func (m *Mod) NICStatus() gamemod.TweakStatus {
	return gamemod.NICStatus()
}

// ApplyNIC turns off interrupt moderation, coalescing and power-saving on the
// physical adapters.
func (m *Mod) ApplyNIC() (gamemod.TweakStatus, error) {
	return gamemod.ApplyNIC(m.emitLog)
}

// RevertNIC restores the original adapter settings from the backup.
func (m *Mod) RevertNIC() (gamemod.TweakStatus, error) {
	return gamemod.RevertNIC(m.emitLog)
}

// --- Disable Skill Effects (override .pak in Content\Paks) ------------------
// A prebuilt combined override pak (every class's skill VFX disabled) is
// downloaded on first enable and dropped into the game's Paks folder. Strictly
// gated to the exact game build it was made for (SkillEffectInfo.Compatible).

// emitFxProgress forwards skill-effect-mod download progress (0–100) to the UI.
func (m *Mod) emitFxProgress(pct int) {
	if m.ctx != nil {
		runtime.EventsEmit(m.ctx, "mod:fxprogress", pct)
	}
}

// SkillEffectStatus reports whether the mod is found/compatible/applied.
func (m *Mod) SkillEffectStatus() gamemod.SkillEffectInfo {
	return gamemod.SkillEffectStatus()
}

// ApplySkillEffect downloads (first time) and installs the override pak.
func (m *Mod) ApplySkillEffect() (gamemod.SkillEffectInfo, error) {
	return gamemod.ApplySkillEffect(m.emitLog, m.emitFxProgress)
}

// RevertSkillEffect removes the override pak from the Paks folder.
func (m *Mod) RevertSkillEffect() (gamemod.SkillEffectInfo, error) {
	return gamemod.RevertSkillEffect(m.emitLog)
}
