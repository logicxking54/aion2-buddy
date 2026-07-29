//go:build windows

package gamemod

import (
	"strings"
	"testing"
)

// A stand-in for what the game's updater writes: real sections that must survive
// our apply/revert byte-for-byte.
const gameIni = "[Core.System]\r\nPaths=../../../Engine/Content\r\n\r\n[/Script/Engine.RendererSettings]\r\nr.ngx.dlss.enable=1\r\n"

func TestPerfApplyAppendsBlockAfterGameContent(t *testing.T) {
	out := perfApplyToContent(gameIni)
	if !strings.HasPrefix(out, "[Core.System]") {
		t.Fatal("game content must stay at the top")
	}
	if !strings.Contains(out, perfBeginMarker) || !strings.Contains(out, perfEndMarker) {
		t.Fatal("applied content must contain the marker block")
	}
	if !strings.Contains(out, "r.ngx.dlss.enable=1") {
		t.Fatal("the game's own settings must survive apply")
	}
	if strings.Index(out, perfBeginMarker) < strings.Index(out, "r.ngx.dlss.enable=1") {
		t.Fatal("our block must come after the game's sections so its values win")
	}
}

func TestPerfApplyIsIdempotent(t *testing.T) {
	once := perfApplyToContent(gameIni)
	twice := perfApplyToContent(once)
	if once != twice {
		t.Fatal("applying twice must not duplicate the block")
	}
	if strings.Count(twice, perfBeginMarker) != 1 {
		t.Fatalf("want exactly one block, got %d", strings.Count(twice, perfBeginMarker))
	}
}

func TestPerfRevertRestoresGameContent(t *testing.T) {
	applied := perfApplyToContent(gameIni)
	reverted := perfRemoveFromContent(applied)
	if strings.Contains(reverted, perfBeginMarker) {
		t.Fatal("revert must remove the block")
	}
	// The game's own content must be preserved (modulo trailing newline trimming).
	if !strings.Contains(reverted, "[Core.System]") || !strings.Contains(reverted, "r.ngx.dlss.enable=1") {
		t.Fatalf("revert lost game content:\n%s", reverted)
	}
}

func TestPerfRevertWithoutBlockIsNoop(t *testing.T) {
	if got := perfRemoveFromContent(gameIni); got != gameIni {
		t.Fatal("content without our block must pass through unchanged")
	}
}

func TestPerfApplyOnEmptyFile(t *testing.T) {
	out := perfApplyToContent("")
	if !strings.HasPrefix(out, perfBeginMarker) {
		t.Fatal("empty file should become just our block")
	}
	if perfRemoveFromContent(out) != "" {
		t.Fatal("reverting a block-only file should leave it empty")
	}
}

// A torn block (updater truncated the file mid-block) must not survive apply.
func TestPerfApplyHealsTornBlock(t *testing.T) {
	torn := gameIni + "\r\n" + perfBeginMarker + "\r\n[SystemSettings]\r\nr.ShadowQuality=0\r\n" // no END
	out := perfApplyToContent(torn)
	if strings.Count(out, perfBeginMarker) != 1 || strings.Count(out, perfEndMarker) != 1 {
		t.Fatalf("torn block not healed:\n%s", out)
	}
	if !strings.Contains(out, "[Core.System]") {
		t.Fatal("game content lost while healing")
	}
}

// The safety exception documented in perfCvars: Niagara quality must stay at 1 so
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
