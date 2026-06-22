package main

import (
	"sort"

	"golang.org/x/sys/windows/registry"
)

// ListSerialPorts returns the available serial (COM) port names, e.g.
// ["COM3", "COM5"], read from the SERIALCOMM device map. Used by the Auto
// Hotkey menu to pick the Arduino board.
func (a *App) ListSerialPorts() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.READ)
	if err != nil {
		return []string{}
	}
	defer k.Close()

	names, err := k.ReadValueNames(0)
	if err != nil {
		return []string{}
	}
	ports := make([]string, 0, len(names))
	for _, n := range names {
		if v, _, err := k.GetStringValue(n); err == nil && v != "" {
			ports = append(ports, v)
		}
	}
	sort.Strings(ports)
	return ports
}
