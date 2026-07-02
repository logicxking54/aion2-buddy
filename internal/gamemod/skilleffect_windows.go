//go:build windows

package gamemod

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The "Disable Skill Effects" mod drops a prebuilt override pak (every class's
// skill-cast VFX disabled) into the game's Content\Paks folder. The pak is too
// big to embed, so it's downloaded once from a static host, checksum-verified,
// cached, and copied in. It's strictly gated to the exact game build it was made
// for — a game patch can change the cooked assets and invalidate it.
const (
	skillEffectVersion = "83" // exact game build (VersionInfo <Version>) this targets
	skillEffectURL     = "https://static.logicxking.com/cbdfe48d-00ea-4fd7-906d-9a585a607ac6.zip"
	skillEffectSHA256  = "34b07741a581b168b660ca4909639b6a3de4f68235f139000454ffbd773c3d43"
)

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
	info := SkillEffectInfo{Supported: skillEffectVersion}
	paks := DetectPaksDir()
	if paks == "" {
		return info
	}
	info.Found = true
	info.PaksDir = paks
	info.Applied = skillEffectInstalled(paks)
	info.GameVersion = gameVersion(paks)
	info.Compatible = info.GameVersion == skillEffectVersion
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
func ensureSkillEffectCache(log Logger, progress ProgressFunc) (string, error) {
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
		if b, _ := os.ReadFile(marker); strings.TrimSpace(string(b)) != skillEffectSHA256 {
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
		if sum, _ := sha256File(zipPath); sum == skillEffectSHA256 {
			needDownload = false
			log.log("Skill-effect mod: cached download already verified")
		}
	}
	if needDownload {
		log.log("Skill-effect mod: downloading ~69 MB…")
		if err := downloadFile(skillEffectURL, zipPath, log, progress); err != nil {
			return "", fmt.Errorf("download failed: %w", err)
		}
		sum, err := sha256File(zipPath)
		if err != nil {
			return "", err
		}
		if sum != skillEffectSHA256 {
			_ = os.Remove(zipPath)
			return "", fmt.Errorf("download corrupted (checksum mismatch): got %s", sum)
		}
		log.log("Skill-effect mod: download verified")
	}

	if err := extractNamed(zipPath, cache, skillEffectPakNames, log); err != nil {
		return "", fmt.Errorf("extract failed: %w", err)
	}
	_ = os.WriteFile(marker, []byte(skillEffectSHA256), 0o644)
	return cache, nil
}

// ApplySkillEffect downloads (first time) and installs the override pak into the
// game's Paks folder. Strictly gated to the supported game build.
func ApplySkillEffect(log Logger, progress ProgressFunc) (SkillEffectInfo, error) {
	info := SkillEffectStatus()
	if !info.Found {
		return info, errors.New("could not find the Aion 2 Paks folder — make sure the game is installed")
	}
	if !info.Compatible {
		return info, fmt.Errorf("this mod is built for game version %s, but the installed game is version %q", skillEffectVersion, info.GameVersion)
	}
	cache, err := ensureSkillEffectCache(log, progress)
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
