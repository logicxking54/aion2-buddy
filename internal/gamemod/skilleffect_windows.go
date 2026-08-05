//go:build windows

package gamemod

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The "Disable Skill Effects" mod drops a prebuilt override pak (every class's
// skill-cast VFX disabled) into the game's Content\Paks folder. The pak is too
// big to embed, so it's downloaded once from a static host, checksum-verified,
// cached, and copied in. It's strictly gated to the exact game build it was made
// for — a game patch can rewrite the cooked FX assets (build 84 did), and an
// override pak carrying the previous build's assets would mask the new ones.
//
// Which pak serves which game build is looked up in a remote manifest first, so a
// game patch is covered by editing that JSON server-side with no app release (see
// tools/effect-mod/README.md for how it's published). The baked-in table below is
// the offline fallback, and only needs a new entry when we happen to be cutting a
// release anyway — the manifest is what actually ships a build to users.
// var, not const, so tests can point it at a local server.
var skillEffectManifestURL = "https://static.logicxking.com/skilleffect-manifest.json"

// skillEffectRelease is one cooked pak: which zip to fetch and what it must hash to.
type skillEffectRelease struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	SizeMB int    `json:"sizeMB"`
}

// fallbackBuilds maps game build -> the pak cooked for it, baked into the binary.
// Mirrors the manifest's shape, and is consulted whenever the manifest doesn't
// list a build (including when it's stale or unreachable).
//
// Keep older builds listed: a player who hasn't patched yet still needs their
// match, and dropping an entry silently makes the mod unavailable for them.
//
// Each entry's URL/SHA256 MUST be the pak cooked from that build's own FX. Two
// builds may share one zip only when their extracted FX are byte-identical —
// verify with the hash-diff in tools/effect-mod/README.md, never assume.
var fallbackBuilds = map[string]skillEffectRelease{
	// 93 rebalanced classes and rewrote 84 player FX assets (Templar, Cleric,
	// Chanter, Assassin, Elementalist), so it needed its own pak.
	"93": {URL: "https://static.logicxking.com/8b4b6c14-6364-4a67-a6f9-d64173f42fdf.zip", SHA256: "6aaa3de8cd7b203c16bb75afe186ca057b0129f521eb6becc3e6b5e990cc3083", SizeMB: 63},
	// 91 was a large FX patch (318 assets removed, ~5600 rewritten) and needed its
	// own pak; 92 left FX byte-identical to it, so the two share one.
	"92": {URL: "https://static.logicxking.com/bde7f4c0-c613-4c3e-896e-f7389a1f7643.zip", SHA256: "bb6e0c31cd32a7f9caebb2c4f19251582b0c9b02a30f9c759af8dee45fdf9209", SizeMB: 63},
	"91": {URL: "https://static.logicxking.com/bde7f4c0-c613-4c3e-896e-f7389a1f7643.zip", SHA256: "bb6e0c31cd32a7f9caebb2c4f19251582b0c9b02a30f9c759af8dee45fdf9209", SizeMB: 63},
	// 88 rewrote P_AB_Buff_Cast_001/002/003 and needed its own pak; 89 then left FX
	// byte-identical to it, so the two share one.
	"89": {URL: "https://static.logicxking.com/4934280b-270f-4c41-af7c-da5b06798f9c.zip", SHA256: "519c824a28c0da723ed5541b606114c7d09c91cae2cdb43a3a8787a4971e9635", SizeMB: 65},
	"88": {URL: "https://static.logicxking.com/4934280b-270f-4c41-af7c-da5b06798f9c.zip", SHA256: "519c824a28c0da723ed5541b606114c7d09c91cae2cdb43a3a8787a4971e9635", SizeMB: 65},
	// 86 and 87 left player FX byte-identical to 85 (verified each time: 0 new /
	// 0 removed / 0 changed), so all three are served by the pak cooked from 85.
	"87": {URL: "https://static.logicxking.com/3db28081-dc3a-4ee1-8808-ce2220539fbd.zip", SHA256: "c17c4c8b45ff55403f40a44a4a0be492b49739243c03faf1ffe0e956725cee90", SizeMB: 65},
	"86": {URL: "https://static.logicxking.com/3db28081-dc3a-4ee1-8808-ce2220539fbd.zip", SHA256: "c17c4c8b45ff55403f40a44a4a0be492b49739243c03faf1ffe0e956725cee90", SizeMB: 65},
	"85": {URL: "https://static.logicxking.com/3db28081-dc3a-4ee1-8808-ce2220539fbd.zip", SHA256: "c17c4c8b45ff55403f40a44a4a0be492b49739243c03faf1ffe0e956725cee90", SizeMB: 65},
	"84": {URL: "https://static.logicxking.com/4ff9745b-0bbc-44c0-a5a1-443c6380b61d.zip", SHA256: "7a32f6489f6100b01a7bda34ce13f979398f4fb8568987a6ad709b1df9297907", SizeMB: 69},
	"83": {URL: "https://static.logicxking.com/cbdfe48d-00ea-4fd7-906d-9a585a607ac6.zip", SHA256: "34b07741a581b168b660ca4909639b6a3de4f68235f139000454ffbd773c3d43", SizeMB: 69},
}

// skillEffectManifest maps game build number ("84") -> the pak cooked for it.
// Keeping several builds lets users who haven't patched yet still get their match.
type skillEffectManifest struct {
	Builds map[string]skillEffectRelease `json:"builds"`
}

var (
	manifestMu     sync.Mutex
	manifestCached *skillEffectManifest // nil until a fetch succeeds
)

// fetchManifest returns the remote manifest, or nil if it can't be reached. A
// successful fetch is cached for the process; failures are not, so a transient
// network blip doesn't pin us to the fallback for the whole session.
func fetchManifest(log Logger) *skillEffectManifest {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	if manifestCached != nil {
		return manifestCached
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(skillEffectManifestURL)
	if err != nil {
		log.log("Skill-effect mod: manifest unreachable (%v) — using built-in list", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.log("Skill-effect mod: manifest returned %s — using built-in list", resp.Status)
		return nil
	}

	var m skillEffectManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		log.log("Skill-effect mod: manifest unreadable (%v) — using built-in list", err)
		return nil
	}
	if len(m.Builds) == 0 {
		log.log("Skill-effect mod: manifest listed no builds — using built-in list")
		return nil
	}
	manifestCached = &m
	return manifestCached
}

// releaseFor resolves the pak to use for a given game build, or nil if that build
// isn't supported. The manifest wins when it lists the build (that's how a build can
// be added without an app release); otherwise the baked table answers. Both are
// keyed by exact build, so neither path can serve another build's pak.
func releaseFor(build string, log Logger) *skillEffectRelease {
	if build == "" {
		return nil
	}
	if m := fetchManifest(log); m != nil {
		if r, ok := m.Builds[build]; ok && r.URL != "" && r.SHA256 != "" {
			return &r
		}
		// Reachable but missing this build — fall through to the baked table.
	}
	if r, ok := fallbackBuilds[build]; ok {
		return &r
	}
	return nil
}

// supportedBuilds lists the game builds we have a pak for, newest first — shown to
// the user when their build isn't one of them. Unions the baked table with the
// manifest, matching what releaseFor will actually serve.
func supportedBuilds(log Logger) string {
	set := map[string]bool{}
	for b := range fallbackBuilds {
		set[b] = true
	}
	if m := fetchManifest(log); m != nil {
		for b := range m.Builds {
			set[b] = true
		}
	}
	builds := make([]string, 0, len(set))
	for b := range set {
		builds = append(builds, b)
	}
	sort.Slice(builds, func(i, j int) bool {
		ni, erri := strconv.Atoi(builds[i])
		nj, errj := strconv.Atoi(builds[j])
		if erri == nil && errj == nil {
			return ni > nj
		}
		return builds[i] > builds[j]
	})
	return strings.Join(builds, ", ")
}

// The override pak MUST use patch index ≥1 (_1_P) so it outranks the base game's
// pakchunkN-Windows_0_P content across ALL chunks; a high chunk number (99999) is
// just a sort tiebreak. Three files: the thin .pak + the IoStore .utoc/.ucas.
var skillEffectPakNames = []string{
	"pakchunk99999-Windows_1_P.utoc",
	"pakchunk99999-Windows_1_P.ucas",
	"pakchunk99999-Windows_1_P.pak",
}

// ProgressFunc receives download progress as a whole percentage (0–100). May be nil.
type ProgressFunc func(percent int)

// SkillEffectInfo is the status reported to the frontend (JSON-tagged for Wails).
type SkillEffectInfo struct {
	Found       bool   `json:"found"`       // Paks folder located on disk
	PaksDir     string `json:"paksDir"`     // …\AION2_TW\Aion2\Content\Paks
	Applied     bool   `json:"applied"`     // override pak currently installed
	GameVersion string `json:"gameVersion"` // build read from the VersionInfo XML
	Supported   string `json:"supported"`   // the build this mod was made for
	Compatible  bool   `json:"compatible"`  // GameVersion == Supported
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// isPaksDir reports whether dir is the game's Content\Paks (it holds the base
// game's pakchunk container files).
func isPaksDir(dir string) bool {
	if !dirExists(dir) {
		return false
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "pakchunk*-Windows*.utoc")); len(m) > 0 {
		return true
	}
	m, _ := filepath.Glob(filepath.Join(dir, "*.pak"))
	return len(m) > 0
}

// DetectPaksDir locates <game>\AION2_TW\Aion2\Content\Paks without needing the
// game to be running (mirrors DetectMoviesDir's probe of common install roots).
func DetectPaksDir() string {
	if d := paksFromProcess(); d != "" {
		return d
	}
	rel := filepath.Join("AION2_TW", "Aion2", "Content", "Paks")
	seen := map[string]bool{}
	for _, drive := range fixedDrives() {
		for _, c := range []string{
			filepath.Join(drive, rel),
			filepath.Join(drive, "Program Files (x86)", "NCSOFT", rel),
			filepath.Join(drive, "Program Files", "NCSOFT", rel),
			filepath.Join(drive, "NCSOFT", rel),
			filepath.Join(drive, "Games", rel),
			filepath.Join(drive, "Games", "NCSOFT", rel),
		} {
			if seen[strings.ToLower(c)] {
				continue
			}
			seen[strings.ToLower(c)] = true
			if isPaksDir(c) {
				return c
			}
		}
	}
	return ""
}

// paksFromProcess derives the Paks dir from the running Aion2.exe's path by
// walking up its ancestors for a Content\Paks.
func paksFromProcess() string {
	pid := aion2PID()
	if pid == 0 {
		return ""
	}
	exe := exePath(pid)
	if exe == "" {
		return ""
	}
	dir := filepath.Dir(exe)
	for i := 0; i < 10; i++ {
		p := filepath.Join(dir, "Content", "Paks")
		if isPaksDir(p) {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

var versionTagRe = regexp.MustCompile(`(?i)<Version>\s*([0-9]+)\s*</Version>`)

// gameVersion reads the client build number from the VersionInfo_*.xml that sits a
// few levels above the Paks folder (in the install root), or "" if not found.
func gameVersion(paksDir string) string {
	dir := paksDir
	for i := 0; i < 6 && dir != ""; i++ {
		matches, _ := filepath.Glob(filepath.Join(dir, "VersionInfo_*.xml"))
		for _, m := range matches {
			if v := readVersionXML(m); v != "" {
				return v
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func readVersionXML(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if m := versionTagRe.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

// skillEffectInstalled reports whether the override pak is present in the Paks dir.
func skillEffectInstalled(paksDir string) bool {
	for _, n := range skillEffectPakNames {
		if fileExists(filepath.Join(paksDir, n)) {
			return true
		}
	}
	return false
}

// SkillEffectStatus reports where the game is, whether the mod is installed, and
// whether it's compatible with the installed build.
func SkillEffectStatus() SkillEffectInfo {
	var info SkillEffectInfo
	paks := DetectPaksDir()
	if paks == "" {
		info.Supported = supportedBuilds(nil)
		return info
	}
	info.Found = true
	info.PaksDir = paks
	info.Applied = skillEffectInstalled(paks)
	info.GameVersion = gameVersion(paks)
	info.Supported = supportedBuilds(nil)
	info.Compatible = releaseFor(info.GameVersion, nil) != nil
	return info
}

// skillEffectCacheDir is where the downloaded+extracted pak files are cached so we
// don't re-download every time: %APPDATA%\aion2-buddy\skilleffect.
func skillEffectCacheDir() (string, error) {
	base, err := backupDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "skilleffect"), nil
}

// ensureSkillEffectCache makes sure the extracted pak files are in the cache,
// downloading + checksum-verifying + extracting the zip on first use.
func ensureSkillEffectCache(rel *skillEffectRelease, log Logger, progress ProgressFunc) (string, error) {
	cache, err := skillEffectCacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", err
	}

	// A marker records which build's paks are currently cached. The pak filenames
	// are version-independent, so without this a stale pak from an older game build
	// would short-circuit the (correct) re-download after a version bump.
	marker := filepath.Join(cache, "skilleffect.sha")

	have := true
	for _, n := range skillEffectPakNames {
		if !fileExists(filepath.Join(cache, n)) {
			have = false
			break
		}
	}
	if have {
		if b, _ := os.ReadFile(marker); strings.TrimSpace(string(b)) != rel.SHA256 {
			have = false // cache is for a different build — rebuild it
		}
	}
	if have {
		log.log("Skill-effect mod: using cached files in %s", cache)
		return cache, nil
	}

	zipPath := filepath.Join(cache, "skilleffect.zip")
	needDownload := true
	if fileExists(zipPath) {
		if sum, _ := sha256File(zipPath); sum == rel.SHA256 {
			needDownload = false
			log.log("Skill-effect mod: cached download already verified")
		}
	}
	if needDownload {
		size := rel.SizeMB
		if size == 0 {
			size = 69
		}
		log.log("Skill-effect mod: downloading ~%d MB…", size)
		if err := downloadFile(rel.URL, zipPath, log, progress); err != nil {
			return "", fmt.Errorf("download failed: %w", err)
		}
		sum, err := sha256File(zipPath)
		if err != nil {
			return "", err
		}
		if sum != rel.SHA256 {
			_ = os.Remove(zipPath)
			return "", fmt.Errorf("download corrupted (checksum mismatch): got %s", sum)
		}
		log.log("Skill-effect mod: download verified")
	}

	if err := extractNamed(zipPath, cache, skillEffectPakNames, log); err != nil {
		return "", fmt.Errorf("extract failed: %w", err)
	}
	_ = os.WriteFile(marker, []byte(rel.SHA256), 0o644)
	return cache, nil
}

// ApplySkillEffect downloads (first time) and installs the override pak into the
// game's Paks folder. Strictly gated to the supported game build.
func ApplySkillEffect(log Logger, progress ProgressFunc) (SkillEffectInfo, error) {
	info := SkillEffectStatus()
	if !info.Found {
		return info, errors.New("could not find the Aion 2 Paks folder — make sure the game is installed")
	}
	rel := releaseFor(info.GameVersion, log)
	if rel == nil {
		return info, fmt.Errorf("no pak has been built for game version %q yet (available: %s)", info.GameVersion, supportedBuilds(log))
	}
	cache, err := ensureSkillEffectCache(rel, log, progress)
	if err != nil {
		return info, err
	}
	for _, n := range skillEffectPakNames {
		src := filepath.Join(cache, n)
		dst := filepath.Join(info.PaksDir, n)
		log.log("Installing %s", n)
		if err := copyFile(src, dst); err != nil {
			return SkillEffectStatus(), fmt.Errorf("installing %s failed (try running as Administrator): %w", n, err)
		}
	}
	log.log("Skill-effect mod: installed — restart the game to apply.")
	return SkillEffectStatus(), nil
}

// RevertSkillEffect removes the override pak from the Paks folder (the cached
// download is kept so re-enabling is instant).
func RevertSkillEffect(log Logger) (SkillEffectInfo, error) {
	info := SkillEffectStatus()
	if !info.Found {
		return info, errors.New("could not find the Aion 2 Paks folder")
	}
	for _, n := range skillEffectPakNames {
		p := filepath.Join(info.PaksDir, n)
		if fileExists(p) {
			log.log("Removing %s", n)
			if err := os.Remove(p); err != nil {
				return SkillEffectStatus(), fmt.Errorf("removing %s failed (try running as Administrator): %w", n, err)
			}
		}
	}
	log.log("Skill-effect mod: removed — restart the game to restore effects.")
	return SkillEffectStatus(), nil
}

// downloadFile streams url to dst, reporting progress through the callback.
func downloadFile(url, dst string, log Logger, progress ProgressFunc) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	cw := &countWriter{total: resp.ContentLength, log: log, progress: progress}
	if _, err := io.Copy(io.MultiWriter(f, cw), resp.Body); err != nil {
		return err
	}
	if progress != nil {
		progress(100)
	}
	return nil
}

// countWriter tracks bytes copied and emits progress: a whole-percent value via
// the callback on each change, and a text log line roughly every 16 MB.
type countWriter struct {
	total    int64
	written  int64
	lastPct  int
	lastLog  int64
	log      Logger
	progress ProgressFunc
}

func (c *countWriter) Write(p []byte) (int, error) {
	n := len(p)
	c.written += int64(n)
	if c.total > 0 {
		if pct := int(c.written * 100 / c.total); pct != c.lastPct {
			c.lastPct = pct
			if c.progress != nil {
				c.progress(pct)
			}
		}
	}
	if c.written-c.lastLog >= 16*1024*1024 {
		c.lastLog = c.written
		if c.total > 0 {
			c.log.log("Downloading… %d / %d MB", c.written/(1024*1024), c.total/(1024*1024))
		} else {
			c.log.log("Downloading… %d MB", c.written/(1024*1024))
		}
	}
	return n, nil
}

// sha256File returns the lowercase hex SHA-256 of a file's contents.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractNamed extracts only the named entries (matched by base name) from the
// zip into destDir, flattened and overwriting. Errors if any are missing.
func extractNamed(zipPath, destDir string, names []string, log Logger) error {
	want := map[string]bool{}
	for _, n := range names {
		want[strings.ToLower(n)] = true
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	found := map[string]bool{}
	for _, zf := range zr.File {
		base := filepath.Base(zf.Name)
		if !want[strings.ToLower(base)] {
			continue
		}
		log.log("Extracting %s", base)
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(filepath.Join(destDir, base))
		if err != nil {
			rc.Close()
			return err
		}
		_, cerr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if cerr != nil {
			return cerr
		}
		found[strings.ToLower(base)] = true
	}
	for _, n := range names {
		if !found[strings.ToLower(n)] {
			return fmt.Errorf("zip is missing expected file %s", n)
		}
	}
	return nil
}
