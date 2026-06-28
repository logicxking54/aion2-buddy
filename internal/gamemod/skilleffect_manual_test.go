//go:build windows

package gamemod

import (
	"os"
	"testing"
)

// Manual end-to-end check of the skill-effect mod. Gated by the FXTEST env var so
// it never runs in a normal `go test` (it hits the real download URL and writes to
// the real game install):
//
//	FXTEST=status  print detection/status only
//	FXTEST=revert  remove the installed override paks
//	FXTEST=apply   download (if needed) + verify + install
func TestSkillEffectManual(t *testing.T) {
	mode := os.Getenv("FXTEST")
	if mode == "" {
		t.Skip("set FXTEST=status|revert|apply to run")
	}
	log := Logger(func(s string) { t.Log(s) })

	st := SkillEffectStatus()
	t.Logf("STATUS found=%v applied=%v compatible=%v version=%q supported=%q\n  paks=%q",
		st.Found, st.Applied, st.Compatible, st.GameVersion, st.Supported, st.PaksDir)

	switch mode {
	case "status":
	case "revert":
		info, err := RevertSkillEffect(log)
		if err != nil {
			t.Fatalf("revert: %v", err)
		}
		t.Logf("AFTER REVERT applied=%v", info.Applied)
	case "apply":
		info, err := ApplySkillEffect(log, func(p int) {
			if p%10 == 0 {
				t.Logf("progress %d%%", p)
			}
		})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		t.Logf("AFTER APPLY applied=%v", info.Applied)
	default:
		t.Fatalf("unknown FXTEST=%q", mode)
	}
}
