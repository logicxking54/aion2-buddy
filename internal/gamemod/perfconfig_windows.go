//go:build windows

// "Performance Config" Mod-menu toggle: writes an aggressive max-FPS cvar block
// into the game's user Engine.ini. Pure config-file edit — no game files touched.
//
// Mechanism: UE reads `[SystemSettings]` from
// %LOCALAPPDATA%\AION2\Saved_TW\Config\Windows\Engine.ini at startup (proven —
// the game itself stores its DLSS flag there). Our block is delimited by marker
// comments so apply is idempotent and revert removes exactly what we added,
// leaving everything the game wrote untouched. No backup file needed: the marker
// block IS the change, and its presence is the applied-state source of truth.
//
// The game's updater regenerates these inis on every patch (observed), which
// wipes the block — the toggle then simply reads as OFF and the user re-enables
// it. Same re-apply story as the skill-effect pak.
package gamemod

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	perfBeginMarker = "; ==== aion2-buddy performance config BEGIN (auto-generated, do not edit) ===="
	perfEndMarker   = "; ==== aion2-buddy performance config END ===="
)

// perfCvars is the max-FPS preset. One deliberate exception to "FPS over looks":
// fx.Niagara.QualityLevel stays at 1 (not 0) because quality 0 can cull whole
// emitters, and the FX we ship intact in the skill-effect pak — boss ground
// telegraphs, raid mechanic orbs — must stay visible. Everything else is free to
// look terrible.
var perfCvars = []string{
	"; --- DLSS Frame Generation (RTX 40xx+; needs Windows HAGS enabled) ---",
	"r.Streamline.DLSSG.Enable=1",
	"; --- Lumen GI / reflections off (single biggest raster win in UE5) ---",
	"r.DynamicGlobalIlluminationMethod=0",
	"r.ReflectionMethod=0",
	"r.Lumen.DiffuseIndirect.Allow=0",
	"r.SSR.Quality=0",
	"r.AmbientOcclusionLevels=0",
	"; --- shadows off ---",
	"r.ShadowQuality=0",
	"r.ContactShadows=0",
	"r.CapsuleShadows=0",
	"r.DistanceFieldShadowing=0",
	"; --- remaining FX cheaper (1, not 0 — 0 can cull boss telegraphs) ---",
	"fx.Niagara.QualityLevel=1",
	"fx.MaxCPUParticlesPerEmitter=200",
	"; --- crowd detail (NCSoft scalability group) ---",
	"sg.MassiveGameQuality=0",
	"; --- post-processing off ---",
	"r.MotionBlurQuality=0",
	"r.BloomQuality=0",
	"r.LensFlareQuality=0",
	"r.DepthOfFieldQuality=0",
	"r.SceneColorFringeQuality=0",
	"r.LightShaftQuality=0",
	"r.Tonemapper.GrainQuantization=0",
	"r.VolumetricFog=0",
	"r.VolumetricCloud=0",
	"; --- world geometry ---",
	"foliage.DensityScale=0.2",
	"grass.DensityScale=0",
	"r.SkeletalMeshLODBias=2",
	"r.ViewDistanceScale=0.6",
	"; --- texture streaming (8GB-VRAM friendly) ---",
	"r.Streaming.PoolSize=5000",
	"r.Streaming.MipBias=1",
}

// perfBlock renders the full marker-delimited ini block we append.
func perfBlock() string {
	var b strings.Builder
	b.WriteString(perfBeginMarker + "\r\n")
	b.WriteString("[SystemSettings]\r\n")
	for _, l := range perfCvars {
		b.WriteString(l + "\r\n")
	}
	b.WriteString(perfEndMarker + "\r\n")
	return b.String()
}

// perfRemoveFromContent strips our marker block (with surrounding blank padding)
// from ini content, returning the content unchanged if no block is present.
// Handles a torn block (BEGIN without END) by cutting to end-of-file rather than
// leaving half a block behind.
func perfRemoveFromContent(content string) string {
	begin := strings.Index(content, perfBeginMarker)
	if begin < 0 {
		return content
	}
	rest := content[begin:]
	end := strings.Index(rest, perfEndMarker)
	var after string
	if end < 0 {
		after = "" // torn block — drop to EOF
	} else {
		after = rest[end+len(perfEndMarker):]
		after = strings.TrimLeft(after, "\r\n")
	}
	before := strings.TrimRight(content[:begin], "\r\n")
	switch {
	case before == "":
		return after
	case after == "":
		return before + "\r\n"
	default:
		return before + "\r\n\r\n" + after
	}
}

// perfApplyToContent returns ini content with exactly one fresh copy of our block
// appended (any previous copy replaced, so upgrades that change perfCvars win).
func perfApplyToContent(content string) string {
	content = perfRemoveFromContent(content)
	content = strings.TrimRight(content, "\r\n")
	if content == "" {
		return perfBlock()
	}
	return content + "\r\n\r\n" + perfBlock()
}

// perfConfigDir locates the game's user config dir. The variant suffix differs
// per region (Saved_TW here), so glob for any Saved* that actually contains the
// Windows config folder with a GameUserSettings.ini.
func perfConfigDir() string {
	local, err := os.UserCacheDir() // %LOCALAPPDATA% (UserCacheDir maps there on Windows)
	if err != nil {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(local, "AION2", "Saved*", "Config", "Windows"))
	for _, m := range matches {
		if fileExists(filepath.Join(m, "GameUserSettings.ini")) || fileExists(filepath.Join(m, "Engine.ini")) {
			return m
		}
	}
	return ""
}

func perfEngineIniPath() string {
	d := perfConfigDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "Engine.ini")
}

// PerfStatus reports whether the performance block is currently in Engine.ini.
func PerfStatus() TweakStatus {
	p := perfEngineIniPath()
	if p == "" {
		return TweakStatus{Applied: false, Detail: "config not found"}
	}
	data, err := os.ReadFile(p)
	applied := err == nil && strings.Contains(string(data), perfBeginMarker)
	// Show which Saved_* variant we found, so the user can see it hit the right one.
	return TweakStatus{Applied: applied, Detail: filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(p))))}
}

// ApplyPerf writes the max-FPS block into Engine.ini. Takes effect on next game
// launch. Re-run any time — replaces its own previous block.
func ApplyPerf(log Logger) (TweakStatus, error) {
	p := perfEngineIniPath()
	if p == "" {
		return PerfStatus(), fmt.Errorf("could not find the game's config folder (%%LOCALAPPDATA%%\\AION2\\Saved*\\Config\\Windows) — run the game once first")
	}
	data, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return PerfStatus(), fmt.Errorf("reading Engine.ini failed: %w", err)
	}
	out := perfApplyToContent(string(data))
	// Plain ASCII, no BOM — a BOM at the top of an ini makes UE reject the first line.
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		return PerfStatus(), fmt.Errorf("writing Engine.ini failed: %w", err)
	}
	log.log("Perf: max-FPS config written to %s (%d settings)", p, len(perfCvars))
	log.log("Perf: takes effect the next time the game starts. Frame Generation additionally needs Windows HAGS on + an RTX 40-series GPU.")
	log.log("Perf: a game patch resets this file — just toggle it on again afterwards.")
	return PerfStatus(), nil
}

// RevertPerf removes our block, leaving the rest of Engine.ini exactly as the
// game wrote it.
func RevertPerf(log Logger) (TweakStatus, error) {
	p := perfEngineIniPath()
	if p == "" {
		return PerfStatus(), nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return PerfStatus(), nil
		}
		return PerfStatus(), fmt.Errorf("reading Engine.ini failed: %w", err)
	}
	out := perfRemoveFromContent(string(data))
	if out == string(data) {
		log.log("Perf: nothing to remove — config was not applied")
		return PerfStatus(), nil
	}
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		return PerfStatus(), fmt.Errorf("writing Engine.ini failed: %w", err)
	}
	log.log("Perf: performance config removed — game visuals return to your in-game settings on next launch")
	return PerfStatus(), nil
}
