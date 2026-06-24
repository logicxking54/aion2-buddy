//go:build windows

package capture

import (
	_ "embed"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// The WinDivert 2.x user-mode DLL and kernel driver are embedded so the app is
// self-contained. They are extracted next to each other at runtime (the DLL
// loads the .sys from its own directory) and that directory is added to the
// DLL search path before any WinDivert call.
//
//go:embed windivert/WinDivert.dll
var winDivertDLLBytes []byte

//go:embed windivert/WinDivert64.sys
var winDivertSysBytes []byte

var driverReady bool

// EnsureDriver extracts the embedded WinDivert driver and registers it on the
// DLL search path. Exported so sibling packages that open their own WinDivert
// handle can reuse the same embedded driver instead of shipping a second copy.
// Safe to call repeatedly.
func EnsureDriver() error { return prepareDriver() }

// prepareDriver extracts the embedded WinDivert files to a temp directory (if
// not already present with the right size) and registers that directory on the
// DLL search path. Safe to call multiple times.
func prepareDriver() error {
	if driverReady {
		return nil
	}
	dir := filepath.Join(os.TempDir(), "aion2-buddy-windivert")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(dir, "WinDivert.dll"), winDivertDLLBytes); err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(dir, "WinDivert64.sys"), winDivertSysBytes); err != nil {
		return err
	}
	// Put the extracted dir on the DLL search path so LazyDLL("WinDivert.dll")
	// resolves here and the DLL finds WinDivert64.sys alongside it.
	if err := windows.SetDllDirectory(dir); err != nil {
		return err
	}
	driverReady = true
	return nil
}

// writeIfChanged writes data to path unless an identically-sized file already
// exists there (avoids rewriting a locked, in-use driver file).
func writeIfChanged(path string, data []byte) error {
	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(data)) {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}
