//go:build windows

// Package gamemod implements file-level game mods. The first one removes the
// NCSOFT intro movie (CI_Type_A.bk2) by swapping it for a tiny no-op stub,
// keeping the original as a .bak so it can be restored. The game folder is
// detected automatically (running process path, then a scan of common install
// roots) so the user never has to type a path.
package gamemod

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// introFile is the NC intro movie under <game>\Aion2\Content\Movies.
const introFile = "CI_Type_A.bk2"

// stub is a 76-byte no-op .bk2 (from the RndUser "Remove NC intro" mod) the game
// accepts in place of the real intro, so nothing plays. Written over the original.
//
//go:embed CI_Type_A.bk2
var stub []byte

// Status describes where the game is and whether the intro is currently removed.
type Status struct {
	MoviesDir string `json:"moviesDir"` // detected ...\Aion2\Content\Movies, or "" if not found
	Found     bool   `json:"found"`     // whether the game folder was located
	Removed   bool   `json:"removed"`   // whether the intro is currently replaced (a .bak exists)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// DetectMoviesDir locates <game>\Aion2\Content\Movies, or "" if it can't be found.
// Detection does NOT require the game to be running — the mod is meant to be
// applied beforehand. Order: (1) the running Aion2.exe's folder if it happens to
// be up, (2) a fast probe of common install roots on every fixed drive, (3) a
// bounded depth-limited walk of each drive as a last resort for unusual layouts.
func DetectMoviesDir() string {
	for _, d := range candidateMoviesDirs() {
		if hasIntro(d) {
			return d
		}
	}
	return scanForAion2TW()
}

// hasIntro reports whether dir contains the intro (or its backup).
func hasIntro(dir string) bool {
	return fileExists(filepath.Join(dir, introFile)) || fileExists(filepath.Join(dir, introFile+".bak"))
}

func candidateMoviesDirs() []string {
	var out []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" || seen[strings.ToLower(d)] {
			return
		}
		seen[strings.ToLower(d)] = true
		out = append(out, d)
	}

	// 1) Derived from the running Aion2.exe — most reliable when the game is up.
	add(moviesFromProcess())

	// 2) Scan common install roots. The game lives at <root>\AION2_TW\Aion2.
	rel := filepath.Join("AION2_TW", "Aion2", "Content", "Movies")
	for _, drive := range fixedDrives() {
		add(filepath.Join(drive, rel))
		add(filepath.Join(drive, "Program Files (x86)", "NCSOFT", rel))
		add(filepath.Join(drive, "Program Files", "NCSOFT", rel))
		add(filepath.Join(drive, "NCSOFT", rel))
		add(filepath.Join(drive, "Games", rel))
		add(filepath.Join(drive, "Games", "NCSOFT", rel))
	}
	return out
}

// fixedDrives returns the roots ("C:\", "D:\", …) of all fixed (non-removable,
// non-network) drives.
func fixedDrives() []string {
	var out []string
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(p) == windows.DRIVE_FIXED {
			out = append(out, root)
		}
	}
	return out
}

// scanForAion2TW is the last-resort fallback: a bounded, depth-limited walk of
// each fixed drive looking for an "AION2_TW" folder that holds the intro. Depth
// and a visited-dir cap keep it fast (seconds, not minutes), and obvious
// system/junk folders are skipped. Returns "" if nothing is found.
func scanForAion2TW() string {
	const maxDepth = 5      // levels below the drive root
	const maxDirs = 60000   // hard cap on directories visited (across all drives)
	skip := map[string]bool{
		"windows": true, "$recycle.bin": true, "system volume information": true,
		"users": true, "windows.old": true, "perflogs": true, "msocache": true,
	}
	visited := 0
	for _, root := range fixedDrives() {
		var found string
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil //nolint:nilerr // unreadable dirs are skipped, not fatal
			}
			if visited++; visited > maxDirs {
				return filepath.SkipAll
			}
			name := strings.ToLower(d.Name())
			// Depth limit relative to the drive root.
			rel, _ := filepath.Rel(root, path)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) >= maxDepth {
				return filepath.SkipDir
			}
			if path != root && skip[name] {
				return filepath.SkipDir
			}
			if name == "aion2_tw" {
				m := filepath.Join(path, "Aion2", "Content", "Movies")
				if hasIntro(m) {
					found = m
					return filepath.SkipAll
				}
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// moviesFromProcess finds the Movies dir by reading the running Aion2.exe's full
// path and walking up its ancestors for a Content\Movies\<introFile>.
func moviesFromProcess() string {
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
		m := filepath.Join(dir, "Content", "Movies")
		if fileExists(filepath.Join(m, introFile)) || fileExists(filepath.Join(m, introFile+".bak")) {
			return m
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// aion2PID returns the PID of the running Aion2 client, or 0 if not running.
func aion2PID() uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)

	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return 0
	}
	for {
		name := windows.UTF16ToString(e.ExeFile[:])
		if strings.EqualFold(name, "Aion2.exe") || strings.EqualFold(name, "Aion2") {
			return e.ProcessID
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			return 0
		}
	}
}

// exePath returns the full image path of the given PID, or "" on failure.
func exePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// GetStatus reports the current game location and intro state.
func GetStatus() Status {
	m := DetectMoviesDir()
	if m == "" {
		return Status{}
	}
	return Status{
		MoviesDir: m,
		Found:     true,
		Removed:   fileExists(filepath.Join(m, introFile+".bak")),
	}
}

// Logger receives a human-readable line describing each file operation, so the
// UI can show the user exactly what is being changed on their PC. May be nil.
type Logger func(string)

func (l Logger) log(format string, a ...any) {
	if l != nil {
		l(fmt.Sprintf(format, a...))
	}
}

// RemoveIntro backs up the real intro to <introFile>.bak and writes the no-op
// stub in its place. Idempotent: if a .bak already exists it just ensures the
// stub is in place. Each step is reported through log (if non-nil).
func RemoveIntro(log Logger) (Status, error) {
	m := DetectMoviesDir()
	if m == "" {
		return Status{}, errors.New("could not find the Aion 2 game folder — make sure the game is installed (try launching it once)")
	}
	orig := filepath.Join(m, introFile)
	bak := orig + ".bak"
	log.log("Remove Intro: game folder detected at %s", m)

	if !fileExists(bak) {
		if !fileExists(orig) {
			return Status{}, fmt.Errorf("intro file not found: %s", orig)
		}
		log.log("Backing up original intro: %s → %s", orig, bak)
		if err := copyFile(orig, bak); err != nil {
			return Status{}, fmt.Errorf("backup failed: %w", err)
		}
	} else {
		log.log("Backup already exists, keeping it: %s", bak)
	}
	log.log("Writing %d-byte no-op stub over %s", len(stub), orig)
	if err := os.WriteFile(orig, stub, 0o644); err != nil {
		return Status{}, fmt.Errorf("writing stub failed (try running as Administrator): %w", err)
	}
	log.log("Remove Intro: done — the NC intro will be skipped.")
	return GetStatus(), nil
}

// RestoreIntro puts the original intro back from the .bak and removes the backup.
// Idempotent: if there's no .bak it's a no-op. Each step is reported through log.
func RestoreIntro(log Logger) (Status, error) {
	m := DetectMoviesDir()
	if m == "" {
		return Status{}, errors.New("could not find the Aion 2 game folder")
	}
	orig := filepath.Join(m, introFile)
	bak := orig + ".bak"
	if !fileExists(bak) {
		log.log("Restore Intro: no backup found, nothing to restore.")
		return GetStatus(), nil // nothing to restore
	}
	log.log("Restore Intro: game folder detected at %s", m)
	log.log("Restoring original intro: %s → %s", bak, orig)
	if err := copyFile(bak, orig); err != nil {
		return Status{}, fmt.Errorf("restore failed (try running as Administrator): %w", err)
	}
	log.log("Deleting backup: %s", bak)
	_ = os.Remove(bak)
	log.log("Restore Intro: done — the original intro is back.")
	return GetStatus(), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
