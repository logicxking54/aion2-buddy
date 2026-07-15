//go:build windows

package gamemod

import (
	"net/http"
	"net/http/httptest"
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
	// A build the manifest doesn't list, and that isn't the baked fallback, must NOT
	// silently resolve to another build's pak — that would mask the FX the patch
	// rewrote. (Use a build far from fallbackSkillEffectVersion.)
	if rel := releaseFor("99999", nil); rel != nil {
		t.Fatalf("unlisted non-fallback build should be unsupported, got %+v", rel)
	}
	// supportedBuilds unions the manifest with the baked fallback, newest-first.
	if got := supportedBuilds(nil); got != "84, 83" && got != "85, 84, 83" {
		t.Fatalf("unexpected supportedBuilds: %q", got)
	}
}

// A build the live manifest hasn't caught up to yet must still resolve — via the
// baked fallback — as long as it's the exact build the fallback was cooked for.
// This is the case that matters when the fixed-name manifest can't be overwritten.
func TestReleaseForFallsBackWhenManifestStale(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Manifest is reachable but lists only OLD builds, not the fallback's.
		w.Write([]byte(`{"builds":{"1":{"url":"https://example.com/old.zip","sha256":"old"}}}`))
	}))
	defer srv.Close()
	resetManifestCache(t, srv.URL)

	rel := releaseFor(fallbackSkillEffectVersion, nil)
	if rel == nil {
		t.Fatal("the baked build must resolve even when the live manifest omits it")
	}
	if rel.URL != fallbackSkillEffectURL || rel.SHA256 != fallbackSkillEffectSHA256 {
		t.Fatalf("stale-manifest fallback must match baked constants: %+v", rel)
	}
	// But a different unlisted build still gets nothing.
	if rel := releaseFor("2", nil); rel != nil {
		t.Fatalf("unlisted non-fallback build should stay unsupported, got %+v", rel)
	}
}

// When the manifest host is down, the app must still serve the one build baked
// into the binary — and refuse every other build.
func TestReleaseForFallsBackWhenManifestUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	resetManifestCache(t, srv.URL)

	rel := releaseFor(fallbackSkillEffectVersion, nil)
	if rel == nil {
		t.Fatal("baked-in build should resolve when the manifest is unreachable")
	}
	if rel.URL != fallbackSkillEffectURL || rel.SHA256 != fallbackSkillEffectSHA256 {
		t.Fatalf("fallback release must match the baked-in constants: %+v", rel)
	}
	if rel := releaseFor("999", nil); rel != nil {
		t.Fatalf("non-baked build must be unsupported without a manifest, got %+v", rel)
	}
}

// The baked-in fallback triple must be self-consistent: shipping a version that
// disagrees with its URL/SHA would hand an older build's pak to a newer client.
func TestFallbackTripleIsConsistent(t *testing.T) {
	if fallbackSkillEffectVersion == "" || fallbackSkillEffectURL == "" || fallbackSkillEffectSHA256 == "" {
		t.Fatal("fallback constants must all be set")
	}
	if len(fallbackSkillEffectSHA256) != 64 {
		t.Fatalf("fallback SHA256 should be 64 hex chars, got %d", len(fallbackSkillEffectSHA256))
	}
}
