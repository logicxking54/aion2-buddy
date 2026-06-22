//go:build windows

package gamemod

import "testing"

// TestDetectLive is a manual smoke test: it prints what detection finds on this
// machine. Not an assertion (CI has no game install) — run with:
//   go test ./internal/gamemod/ -run TestDetectLive -v
func TestDetectLive(t *testing.T) {
	st := GetStatus()
	t.Logf("found=%v removed=%v moviesDir=%q", st.Found, st.Removed, st.MoviesDir)
}
