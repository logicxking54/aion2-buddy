//go:build windows

package capture

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GameRunning reports whether the Aion 2 client process is running. Uses a
// process snapshot (no PowerShell), cheap enough to poll periodically.
func GameRunning() bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)

	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return false
	}
	for {
		name := windows.UTF16ToString(e.ExeFile[:])
		if strings.EqualFold(name, "Aion2.exe") || strings.EqualFold(name, "Aion2") {
			return true
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			return false
		}
	}
}
