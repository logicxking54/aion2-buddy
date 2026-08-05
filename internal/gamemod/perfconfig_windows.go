//go:build windows

// "Performance Config" Mod-menu toggle: writes an aggressive max-FPS cvar block
// into the game's user Engine.ini. Pure config-file edit - no game files touched.
//
// Mechanism: UE reads `[SystemSettings]` from
// %LOCALAPPDATA%\AION2\Saved_TW\Config\Windows\Engine.ini at startup (proven -
// the game itself stores its DLSS flag there).
//
// Identity is by CVAR KEY, not by marker comments. The first version of this used
// `; ====` markers to delimit the block, which broke in the field: UE rewrites
// its config files on exit and STRIPS COMMENTS, so the markers vanished while the
// settings stayed. Status then read OFF, every re-enable appended another copy
// (a real Engine.ini ended up with 7 duplicates of every line), and revert could
// never find the block to remove - the settings were stuck on permanently.
//
// So: apply removes every line assigning any key we manage, anywhere in the file,
// then appends one clean block. That makes apply idempotent, self-healing against
// duplicates, and revert exact - all without depending on anything UE might strip.
//
// The game's updater regenerates these inis on every patch (observed), which wipes
// our settings - the toggle then reads OFF and the user re-enables it. Same
// re-apply story as the skill-effect pak.
package gamemod

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	perfSectionHeader = "[SystemSettings]"
	// Written for humans reading the ini; never parsed, since UE strips comments.
	perfBanner = "; aion2-buddy performance config - toggle it off in the app to remove"
	// Matched by prefix when removing, so a banner written by an older version
	// (whose wording differed) is still recognised as ours and cleaned up.
	perfBannerPrefix = "; aion2-buddy"
)

// perfCvars is the max-FPS preset. One deliberate exception to "FPS over looks":
// fx.Niagara.QualityLevel stays at 1 (not 0) because quality 0 can cull whole
// emitters, and the FX we ship intact in the skill-effect pak - boss ground
// telegraphs, raid mechanic orbs - must stay visible. Everything else is free to
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
	"; --- remaining FX cheaper (1, not 0 - 0 can cull boss telegraphs) ---",
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
	// The cheap way to get what "strip the scenery" mods do by emptying assets:
	// DetailMode culls props the artists marked as decoration, MaterialQualityLevel
	// drops every surface to its simplest shader, the LOD biases pick coarser
	// meshes, and HLOD swaps distant clusters for a single proxy. All revert in one
	// click and survive game patches, which asset edits do not.
	"; --- world geometry and materials ---",
	"r.DetailMode=0",
	"r.MaterialQualityLevel=0",
	"r.StaticMeshLODBias=2",
	"r.SkeletalMeshLODBias=2",
	"r.HLOD=1",
	"foliage.DensityScale=0.2",
	"foliage.LODDistanceScale=0.5",
	"grass.DensityScale=0",
	"r.ViewDistanceScale=0.6",
	// Sized for an 8GB card, which is what this preset's Frame Generation target
	// (a 4060 Ti) usually is. DLSS-G itself costs roughly 1-1.5GB on top of the
	// render targets, so a 5GB texture pool left too little headroom and traded
	// stutter for sharpness in exactly the crowded fights we're optimising for.
	"; --- texture streaming (sized for 8GB VRAM alongside Frame Generation) ---",
	"r.Streaming.PoolSize=3500",
	"r.Streaming.MipBias=1",
	"r.Streaming.LimitPoolSizeToVRAM=1",
}

// perfCvarKeys returns the setting names we manage (the part before '='), skipping
// the comment lines in perfCvars.
func perfCvarKeys() map[string]bool {
	keys := map[string]bool{}
	for _, l := range perfCvars {
		if i := strings.Index(l, "="); i > 0 {
			keys[strings.ToLower(strings.TrimSpace(l[:i]))] = true
		}
	}
	return keys
}

// perfBlock renders the ini block we append.
func perfBlock() string {
	var b strings.Builder
	b.WriteString(perfBanner + "\r\n")
	b.WriteString(perfSectionHeader + "\r\n")
	for _, l := range perfCvars {
		b.WriteString(l + "\r\n")
	}
	return b.String()
}

// perfRemoveFromContent strips every line that assigns one of our keys, plus our
// banner and any [SystemSettings] header left with nothing under it. Lines the
// game owns are untouched - including a key we manage appearing in some other
// section, since UE's [SystemSettings] is the only place these take effect and
// leaving a stray elsewhere is safer than deleting something we didn't write.
func perfRemoveFromContent(content string) string {
	keys := perfCvarKeys()
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	kept := make([]string, 0, len(lines))
	inOurSection := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, perfBannerPrefix) || strings.HasPrefix(t, "; ==== aion2-buddy") {
			continue // our banner, any version, including the old marker comments
		}
		if strings.HasPrefix(t, "[") {
			inOurSection = strings.EqualFold(t, perfSectionHeader)
			kept = append(kept, line)
			continue
		}
		if inOurSection {
			if i := strings.Index(t, "="); i > 0 && keys[strings.ToLower(strings.TrimSpace(t[:i]))] {
				continue
			}
			// Our own explanatory comments inside the block.
			if strings.HasPrefix(t, "; ---") {
				continue
			}
		}
		kept = append(kept, line)
	}

	// Drop a [SystemSettings] header we emptied out, so repeated apply/revert
	// cycles don't leave a stack of bare headers behind.
	pruned := make([]string, 0, len(kept))
	for i := 0; i < len(kept); i++ {
		if strings.EqualFold(strings.TrimSpace(kept[i]), perfSectionHeader) {
			hasBody := false
			for j := i + 1; j < len(kept); j++ {
				t := strings.TrimSpace(kept[j])
				if t == "" {
					continue
				}
				if strings.HasPrefix(t, "[") {
					break
				}
				hasBody = true
				break
			}
			if !hasBody {
				continue
			}
		}
		pruned = append(pruned, kept[i])
	}

	out := strings.Join(pruned, "\r\n")
	out = strings.TrimRight(out, "\r\n")
	if out != "" {
		out += "\r\n"
	}
	return out
}

// perfApplyToContent returns ini content with exactly one clean copy of our
// settings appended, having first removed any earlier copy (however many times it
// was written, and whether or not the banner survived UE's comment stripping).
func perfApplyToContent(content string) string {
	content = perfRemoveFromContent(content)
	content = strings.TrimRight(content, "\r\n")
	if content == "" {
		return perfBlock()
	}
	return content + "\r\n\r\n" + perfBlock()
}

// perfCountApplied reports how many of our keys are currently set in the file -
// used for status (any > 0 means applied) and to spot duplicate build-up.
func perfCountApplied(content string) (set int, duplicates int) {
	keys := perfCvarKeys()
	seen := map[string]int{}
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		i := strings.Index(t, "=")
		if i <= 0 || strings.HasPrefix(t, ";") {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(t[:i]))
		if keys[k] {
			seen[k]++
		}
	}
	for _, n := range seen {
		set++
		if n > 1 {
			duplicates += n - 1
		}
	}
	return set, duplicates
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

// PerfStatusFromContent reports whether ini content carries our settings. Split
// out so the detection rule can be tested against real-world mangled files.
func PerfStatusFromContent(content string) bool {
	set, _ := perfCountApplied(content)
	return set > 0
}

// PerfStatus reports whether our settings are currently in Engine.ini. Detection
// is by cvar key (see the file header): a comment-based marker does not survive
// UE rewriting the file.
func PerfStatus() TweakStatus {
	p := perfEngineIniPath()
	if p == "" {
		return TweakStatus{Applied: false, Detail: "config not found"}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return TweakStatus{Applied: false, Detail: "config not readable"}
	}
	set, _ := perfCountApplied(string(data))
	// Show which Saved_* variant we found, so the user can see it hit the right one.
	variant := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(p))))
	return TweakStatus{Applied: set > 0, Detail: variant}
}

// ApplyPerf writes the max-FPS block into Engine.ini. Takes effect on next game
// launch. Re-run any time - replaces its own previous block.
func ApplyPerf(log Logger) (TweakStatus, error) {
	p := perfEngineIniPath()
	if p == "" {
		return PerfStatus(), fmt.Errorf("could not find the game's config folder (%%LOCALAPPDATA%%\\AION2\\Saved*\\Config\\Windows) - run the game once first")
	}
	data, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return PerfStatus(), fmt.Errorf("reading Engine.ini failed: %w", err)
	}
	if _, dupes := perfCountApplied(string(data)); dupes > 0 {
		log.log("Perf: cleaning up %d duplicate setting(s) left in Engine.ini", dupes)
	}
	out := perfApplyToContent(string(data))
	// Plain ASCII, no BOM - a BOM at the top of an ini makes UE reject the first line.
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		return PerfStatus(), fmt.Errorf("writing Engine.ini failed: %w", err)
	}
	log.log("Perf: max-FPS config written to %s (%d settings)", p, len(perfCvarKeys()))
	log.log("Perf: takes effect the next time the game starts. Frame Generation additionally needs Windows HAGS on + an RTX 40-series GPU.")
	log.log("Perf: a game patch resets this file - just toggle it on again afterwards.")
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
		log.log("Perf: nothing to remove - config was not applied")
		return PerfStatus(), nil
	}
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		return PerfStatus(), fmt.Errorf("writing Engine.ini failed: %w", err)
	}
	log.log("Perf: performance config removed - game visuals return to your in-game settings on next launch")
	return PerfStatus(), nil
}
