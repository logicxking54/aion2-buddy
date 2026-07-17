//go:build windows

package gamemod

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// resetManifestCache clears the process-wide manifest cache so each case starts clean.
func resetManifestCache(t *testing.T, url string) {
	t.Helper()
	orig := skillEffectManifestURL
	manifestMu.Lock()
	manifestCached = nil
	manifestMu.Unlock()
	skillEffectManifestURL = url
	t.Cleanup(func() {
		skillEffectManifestURL = orig
		manifestMu.Lock()
		manifestCached = nil
		manifestMu.Unlock()
	})
}

func TestReleaseForUsesManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"builds":{
			"84":{"url":"https://example.com/v84.zip","sha256":"aa84","sizeMB":69},
			"83":{"url":"https://example.com/v83.zip","sha256":"bb83","sizeMB":69}}}`))
	}))
	defer srv.Close()
	resetManifestCache(t, srv.URL)

	rel := releaseFor("84", nil)
	if rel == nil {
		t.Fatal("build 84 should resolve from the manifest")
	}
	if rel.URL != "https://example.com/v84.zip" || rel.SHA256 != "aa84" {
		t.Fatalf("wrong release for 84: %+v", rel)
	}
	if rel := releaseFor("83", nil); rel == nil || rel.SHA256 != "bb83" {
		t.Fatalf("build 83 should resolve to its own pak, got %+v", rel)
	}
	// A build listed in neither the manifest nor the baked table must NOT silently
	// resolve to another build's pak — that would mask the FX a patch rewrote.
	if rel := releaseFor("99999", nil); rel != nil {
		t.Fatalf("wholly unknown build should be unsupported, got %+v", rel)
	}
	// supportedBuilds unions the manifest with the baked table, newest-first.
	got := supportedBuilds(nil)
	for _, want := range []string{"84", "83"} {
		if !strings.Contains(got, want) {
			t.Fatalf("supportedBuilds %q should contain manifest build %s", got, want)
		}
	}
	for b := range fallbackBuilds {
		if !strings.Contains(got, b) {
			t.Fatalf("supportedBuilds %q should contain baked build %s", got, b)
		}
	}
}

// A build the live manifest hasn't caught up to must still resolve via the baked
// table. This is the case that matters in practice: the fixed-name manifest can't
// be overwritten by our upload API, so it lags every newly cooked build.
func TestReleaseForFallsBackWhenManifestStale(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reachable, but lists only a build we don't otherwise know about.
		w.Write([]byte(`{"builds":{"1":{"url":"https://example.com/old.zip","sha256":"old"}}}`))
	}))
	defer srv.Close()
	resetManifestCache(t, srv.URL)

	for b, want := range fallbackBuilds {
		rel := releaseFor(b, nil)
		if rel == nil {
			t.Fatalf("baked build %s must resolve even when the manifest omits it", b)
		}
		if rel.URL != want.URL || rel.SHA256 != want.SHA256 {
			t.Fatalf("build %s: stale-manifest fallback must match the baked entry: %+v", b, rel)
		}
	}
	if rel := releaseFor("2", nil); rel != nil {
		t.Fatalf("unknown build should stay unsupported, got %+v", rel)
	}
}

// When the manifest host is down, the app must still serve every build baked into
// the binary — and refuse the ones it doesn't know.
func TestReleaseForFallsBackWhenManifestUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	resetManifestCache(t, srv.URL)

	for b, want := range fallbackBuilds {
		rel := releaseFor(b, nil)
		if rel == nil {
			t.Fatalf("baked build %s should resolve when the manifest is unreachable", b)
		}
		if rel.URL != want.URL || rel.SHA256 != want.SHA256 {
			t.Fatalf("build %s: fallback must match the baked entry: %+v", b, rel)
		}
	}
	if rel := releaseFor("999", nil); rel != nil {
		t.Fatalf("non-baked build must be unsupported without a manifest, got %+v", rel)
	}
}

// Every baked entry must be complete: a build pointing at a blank or malformed
// URL/SHA would fail the download or, worse, skip verification.
func TestFallbackBuildsAreWellFormed(t *testing.T) {
	if len(fallbackBuilds) == 0 {
		t.Fatal("fallbackBuilds must not be empty")
	}
	for b, r := range fallbackBuilds {
		if _, err := strconv.Atoi(b); err != nil {
			t.Fatalf("build key %q should be a numeric game build", b)
		}
		if !strings.HasPrefix(r.URL, "https://") {
			t.Fatalf("build %s: URL must be https, got %q", b, r.URL)
		}
		if len(r.SHA256) != 64 {
			t.Fatalf("build %s: SHA256 should be 64 hex chars, got %d", b, len(r.SHA256))
		}
		if r.SizeMB <= 0 {
			t.Fatalf("build %s: SizeMB should be positive, got %d", b, r.SizeMB)
		}
	}
}
