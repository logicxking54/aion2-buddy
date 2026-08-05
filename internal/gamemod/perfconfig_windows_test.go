//go:build windows

package gamemod

import (
	"strings"
	"testing"
)

// A stand-in for what the game writes: real sections that must survive our
// apply/revert.
const gameIni = "[Core.System]\r\nPaths=../../../Engine/Content\r\n\r\n[/Script/Engine.RendererSettings]\r\nr.ngx.dlss.enable=1\r\n"

func TestPerfApplyAppendsSettingsAfterGameContent(t *testing.T) {
	out := perfApplyToContent(gameIni)
	if !strings.HasPrefix(out, "[Core.System]") {
		t.Fatal("game content must stay at the top")
	}
	if !strings.Contains(out, "r.ngx.dlss.enable=1") {
		t.Fatal("the game's own settings must survive apply")
	}
	set, dupes := perfCountApplied(out)
	if set != len(perfCvarKeys()) {
		t.Fatalf("applied %d of %d settings", set, len(perfCvarKeys()))
	}
	if dupes != 0 {
		t.Fatalf("fresh apply produced %d duplicates", dupes)
	}
}

func TestPerfApplyIsIdempotent(t *testing.T) {
	once := perfApplyToContent(gameIni)
	twice := perfApplyToContent(once)
	if once != twice {
		t.Fatal("applying twice must produce identical content")
	}
	if _, dupes := perfCountApplied(twice); dupes != 0 {
		t.Fatalf("second apply produced %d duplicate lines", dupes)
	}
}

func TestPerfRevertRestoresGameContent(t *testing.T) {
	applied := perfApplyToContent(gameIni)
	reverted := perfRemoveFromContent(applied)
	if set, _ := perfCountApplied(reverted); set != 0 {
		t.Fatalf("revert left %d of our settings behind:\n%s", set, reverted)
	}
	if !strings.Contains(reverted, "[Core.System]") || !strings.Contains(reverted, "r.ngx.dlss.enable=1") {
		t.Fatalf("revert lost game content:\n%s", reverted)
	}
	if strings.Contains(reverted, perfBanner) {
		t.Fatal("revert should remove our banner")
	}
}

func TestPerfRevertWithoutOurSettingsIsNoop(t *testing.T) {
	if got := perfRemoveFromContent(gameIni); strings.TrimRight(got, "\r\n") != strings.TrimRight(gameIni, "\r\n") {
		t.Fatalf("content without our settings changed:\n%q", got)
	}
}

// The field failure this file's design exists to prevent: UE rewrites Engine.ini
// on exit and strips comments, so a marker-comment-based implementation lost track
// of its own block. Every re-enable then appended another copy — a real machine
// ended up with 7 duplicates of every line — and revert could never remove them.
func TestPerfHandlesCommentStrippedDuplicates(t *testing.T) {
	// What UE left behind: no banner, our settings duplicated 7 times.
	var b strings.Builder
	b.WriteString(gameIni)
	b.WriteString("\r\n[SystemSettings]\r\n")
	for i := 0; i < 7; i++ {
		for _, l := range perfCvars {
			if strings.HasPrefix(l, ";") {
				continue // comments already stripped by UE
			}
			b.WriteString(l + "\r\n")
		}
	}
	mangled := b.String()

	if _, dupes := perfCountApplied(mangled); dupes == 0 {
		t.Fatal("test setup should contain duplicates")
	}
	if !PerfStatusFromContent(mangled) {
		t.Fatal("status must detect settings even with the banner stripped")
	}

	healed := perfApplyToContent(mangled)
	if _, dupes := perfCountApplied(healed); dupes != 0 {
		t.Fatalf("apply must collapse duplicates, still have %d", dupes)
	}

	clean := perfRemoveFromContent(mangled)
	if set, _ := perfCountApplied(clean); set != 0 {
		t.Fatalf("revert must remove comment-stripped copies, %d left:\n%s", set, clean)
	}
	if !strings.Contains(clean, "r.ngx.dlss.enable=1") {
		t.Fatal("game content lost while cleaning duplicates")
	}
}

// A banner written by an older version of this mod (different wording, and the
// original `; ====` markers) must still be recognised as ours and removed —
// otherwise upgrading the app leaves stale comment lines accumulating.
func TestPerfRemovesOlderBanners(t *testing.T) {
	old := gameIni +
		"\r\n; ==== aion2-buddy performance config BEGIN (auto-generated, do not edit) ====\r\n" +
		"[SystemSettings]\r\nr.ShadowQuality=0\r\n" +
		"; ==== aion2-buddy performance config END ====\r\n" +
		"; aion2-buddy performance config \u2014 toggle it off in the app to remove\r\n"

	clean := perfRemoveFromContent(old)
	if strings.Contains(clean, "aion2-buddy") {
		t.Fatalf("older banner/marker lines survived:\n%s", clean)
	}
	if set, _ := perfCountApplied(clean); set != 0 {
		t.Fatalf("settings from the old block survived: %d", set)
	}

	applied := perfApplyToContent(old)
	if n := strings.Count(applied, perfBannerPrefix); n != 1 {
		t.Fatalf("want exactly one banner after apply, got %d:\n%s", n, applied)
	}
}

// A key we manage that the GAME set in one of its own sections must be left alone
// — we only own what's under [SystemSettings].
func TestPerfLeavesGameOwnedSectionsAlone(t *testing.T) {
	ini := "[/Script/Engine.RendererSettings]\r\nr.ShadowQuality=3\r\n"
	if got := perfRemoveFromContent(ini); !strings.Contains(got, "r.ShadowQuality=3") {
		t.Fatalf("removed a setting from the game's own section:\n%s", got)
	}
}

func TestPerfApplyOnEmptyFile(t *testing.T) {
	out := perfApplyToContent("")
	if set, _ := perfCountApplied(out); set != len(perfCvarKeys()) {
		t.Fatal("empty file should get the full set")
	}
	if got := perfRemoveFromContent(out); strings.TrimSpace(got) != "" {
		t.Fatalf("reverting a settings-only file should leave it empty, got:\n%q", got)
	}
}

// Repeated cycles must not accumulate bare [SystemSettings] headers.
func TestPerfCyclesDoNotAccumulateHeaders(t *testing.T) {
	c := gameIni
	for i := 0; i < 5; i++ {
		c = perfApplyToContent(c)
		c = perfRemoveFromContent(c)
	}
	if n := strings.Count(c, perfSectionHeader); n != 0 {
		t.Fatalf("after 5 cycles the file has %d leftover %s headers:\n%s", n, perfSectionHeader, c)
	}
}

// The safety exception documented in perfCvars: Niagara quality must stay ≥1 so
// scalability culling can't hide the boss telegraphs the skill-effect pak keeps.
func TestPerfKeepsNiagaraQualityAtOne(t *testing.T) {
	block := perfBlock()
	if strings.Contains(block, "fx.Niagara.QualityLevel=0") {
		t.Fatal("fx.Niagara.QualityLevel must not be 0 — culls boss telegraph emitters")
	}
	if !strings.Contains(block, "fx.Niagara.QualityLevel=1") {
		t.Fatal("expected fx.Niagara.QualityLevel=1 in the preset")
	}
}
