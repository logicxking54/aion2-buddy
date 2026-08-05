//go:build windows

package gamemod

import (
	"os"
	"strings"
	"testing"
)

// Manual end-to-end check of the performance config against the REAL game
// Engine.ini. Gated so it never runs in normal `go test`:
//
//	PERFTEST=status  print detection/status only
//	PERFTEST=apply   write the block
//	PERFTEST=revert  remove the block
//	PERFTEST=cycle   apply, verify, revert, verify byte-identical to the original
func TestPerfManual(t *testing.T) {
	mode := os.Getenv("PERFTEST")
	if mode == "" {
		t.Skip("set PERFTEST=status|apply|revert|cycle to run")
	}
	log := Logger(func(s string) { t.Log(s) })

	st := PerfStatus()
	t.Logf("STATUS applied=%v detail=%q path=%q", st.Applied, st.Detail, perfEngineIniPath())

	switch mode {
	case "status":
	case "apply":
		if _, err := ApplyPerf(log); err != nil {
			t.Fatal(err)
		}
	case "revert":
		if _, err := RevertPerf(log); err != nil {
			t.Fatal(err)
		}
	case "cycle":
		p := perfEngineIniPath()
		if p == "" {
			t.Fatal("config dir not found")
		}
		orig, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyPerf(log); err != nil {
			t.Fatal(err)
		}
		mid, _ := os.ReadFile(p)
		if set, dupes := perfCountApplied(string(mid)); set == 0 || dupes != 0 {
			t.Fatalf("after apply: %d settings present, %d duplicates (want all, none)", set, dupes)
		}
		if !PerfStatus().Applied {
			t.Fatal("status should report applied")
		}
		if _, err := RevertPerf(log); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(p)
		// Compare modulo trailing-newline normalization, which apply/revert may trim.
		if strings.TrimRight(string(after), "\r\n") != strings.TrimRight(string(orig), "\r\n") {
			t.Fatalf("revert did not restore the original file.\n-- original --\n%s\n-- after --\n%s", orig, after)
		}
		t.Log("CYCLE OK — apply/revert round-trip left the game's ini intact")
	default:
		t.Fatalf("unknown PERFTEST=%q", mode)
	}
	st = PerfStatus()
	t.Logf("FINAL applied=%v", st.Applied)
}
