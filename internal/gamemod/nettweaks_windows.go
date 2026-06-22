//go:build windows

// This file adds two *reversible* system tweaks ported from the SkyFields
// "Aion2-auto-optimize" PowerShell tool, exposed as Mod-menu toggles:
//
//   - TCP latency  (this file): disables Nagle batching + delayed ACKs and sets
//     a few low-latency TCP registry values, system-wide.
//   - NIC tuning   (nictweaks_windows.go): turns off interrupt moderation,
//     coalescing and power-saving on the physical network adapters.
//
// Both follow the same contract as the intro mod: applying records the ORIGINAL
// state to a JSON backup under the user's config dir, and reverting reads that
// backup to put everything back exactly as it was (deleting values that did not
// exist before). The presence of the backup file is the source of truth for
// whether the tweak is currently applied. Every change is reported through the
// shared Logger so the UI log box shows the user precisely what changed.
package gamemod

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	tcpParamsPath     = `SYSTEM\CurrentControlSet\Services\Tcpip\Parameters`
	tcpInterfacesPath = tcpParamsPath + `\Interfaces`
)

// TweakStatus is the toggle state for a system tweak (TCP or NIC).
type TweakStatus struct {
	Applied bool   `json:"applied"` // tweak currently in effect (our backup file exists)
	Detail  string `json:"detail"`  // short human note, e.g. "3 active network interface(s)"
}

// tcpDword is one DWORD we set to apply the low-latency TCP tweaks. Per-interface
// values live under each configured interface subkey; global ones live under
// Tcpip\Parameters itself.
type tcpDword struct {
	name  string
	value uint32
	perIf bool
}

var tcpTweaks = []tcpDword{
	{name: "TcpAckFrequency", value: 1, perIf: true}, // ACK every packet (no 200ms delayed-ACK wait)
	{name: "TCPNoDelay", value: 1, perIf: true},      // disable Nagle batching of small sends
	{name: "TcpDelAckTicks", value: 0, perIf: true},  // no delayed-ACK timer
	{name: "TcpTimedWaitDelay", value: 30, perIf: false},
}

// regValueBackup records a single registry value's pre-change state so revert can
// restore it exactly (or delete it, if it did not exist before).
type regValueBackup struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Existed bool   `json:"existed"`
	Value   uint32 `json:"value"`
}

type tcpBackup struct {
	Values []regValueBackup `json:"values"`
}

// backupDir is %AppData%\aion2-buddy, where mod rollback data is stored.
func backupDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "aion2-buddy")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

func tcpBackupPath() (string, error) {
	d, err := backupDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "tcp-backup.json"), nil
}

// configuredInterfaces returns the registry paths of TCP interfaces that have an
// IP address (i.e. real, in-use adapters) — the ones worth tweaking.
func configuredInterfaces() ([]string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, tcpInterfacesPath, registry.READ)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		sub := tcpInterfacesPath + `\` + n
		ik, err := registry.OpenKey(registry.LOCAL_MACHINE, sub, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		hasIP := false
		if v, _, err := ik.GetStringValue("DhcpIPAddress"); err == nil && v != "" && v != "0.0.0.0" {
			hasIP = true
		}
		if !hasIP {
			if vs, _, err := ik.GetStringsValue("IPAddress"); err == nil {
				for _, v := range vs {
					if v != "" && v != "0.0.0.0" {
						hasIP = true
						break
					}
				}
			}
		}
		ik.Close()
		if hasIP {
			out = append(out, sub)
		}
	}
	return out, nil
}

// TCPStatus reports whether the TCP latency tweak is applied.
func TCPStatus() TweakStatus {
	p, err := tcpBackupPath()
	applied := err == nil && fileExists(p)
	detail := ""
	if ifaces, e := configuredInterfaces(); e == nil {
		detail = fmt.Sprintf("%d active network interface(s)", len(ifaces))
	}
	return TweakStatus{Applied: applied, Detail: detail}
}

// ApplyTCP writes the low-latency TCP values (backing up each original first) and
// disables ECN. Needs Administrator. Reports every change through log.
func ApplyTCP(log Logger) (TweakStatus, error) {
	ifaces, err := configuredInterfaces()
	if err != nil {
		return TweakStatus{}, fmt.Errorf("reading network interfaces failed (run as Administrator): %w", err)
	}
	var backup tcpBackup
	set := func(path string, t tcpDword) error {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer k.Close()
		b := regValueBackup{Path: path, Name: t.name}
		if cur, _, err := k.GetIntegerValue(t.name); err == nil {
			b.Existed = true
			b.Value = uint32(cur)
		}
		backup.Values = append(backup.Values, b)
		log.log("TCP: set %s = %d  [%s]", t.name, t.value, shortPath(path))
		return k.SetDWordValue(t.name, t.value)
	}

	for _, t := range tcpTweaks {
		if t.perIf {
			for _, ifc := range ifaces {
				if err := set(ifc, t); err != nil {
					log.log("TCP: warning — could not set %s on %s: %v", t.name, shortPath(ifc), err)
				}
			}
			continue
		}
		if err := set(tcpParamsPath, t); err != nil {
			return TweakStatus{}, fmt.Errorf("setting %s failed (run as Administrator): %w", t.name, err)
		}
	}
	setECN(log, "disabled")

	if err := saveTCPBackup(backup); err != nil {
		log.log("TCP: warning — could not save rollback data: %v", err)
	}
	log.log("TCP: done — applied to %d interface(s). No reboot needed.", len(ifaces))
	return TCPStatus(), nil
}

// RevertTCP restores the original TCP values from the backup (deleting any value
// that did not exist before) and resets ECN to the Windows default.
func RevertTCP(log Logger) (TweakStatus, error) {
	backup, err := loadTCPBackup()
	if err != nil {
		// No backup: best-effort — delete the values we would have set so behavior
		// returns to Windows defaults.
		log.log("TCP: no saved rollback data — removing the tweak values to restore defaults.")
		ifaces, _ := configuredInterfaces()
		for _, ifc := range ifaces {
			for _, t := range tcpTweaks {
				if t.perIf {
					deleteRegValue(log, ifc, t.name)
				}
			}
		}
		deleteRegValue(log, tcpParamsPath, "TcpTimedWaitDelay")
		setECN(log, "default")
		clearTCPBackup()
		return TCPStatus(), nil
	}
	for _, b := range backup.Values {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, b.Path, registry.SET_VALUE)
		if err != nil {
			log.log("TCP: warning — could not open %s to restore %s: %v", shortPath(b.Path), b.Name, err)
			continue
		}
		if b.Existed {
			log.log("TCP: restore %s = %d  [%s]", b.Name, b.Value, shortPath(b.Path))
			_ = k.SetDWordValue(b.Name, b.Value)
		} else {
			log.log("TCP: remove %s (was not set originally)  [%s]", b.Name, shortPath(b.Path))
			_ = k.DeleteValue(b.Name)
		}
		k.Close()
	}
	setECN(log, "default")
	clearTCPBackup()
	log.log("TCP: done — original settings restored.")
	return TCPStatus(), nil
}

func deleteRegValue(log Logger, path, name string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if err := k.DeleteValue(name); err == nil {
		log.log("TCP: remove %s  [%s]", name, shortPath(path))
	}
}

// setECN toggles TCP ECN capability via netsh ("disabled" or "default").
func setECN(log Logger, mode string) {
	cmd := exec.Command("netsh", "int", "tcp", "set", "global", "ecncapability="+mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		log.log("TCP: warning — ECN change failed: %v (%s)", err, strings.TrimSpace(string(out)))
		return
	}
	log.log("TCP: ECN capability → %s", mode)
}

func saveTCPBackup(b tcpBackup) error {
	p, err := tcpBackupPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func loadTCPBackup() (tcpBackup, error) {
	var b tcpBackup
	p, err := tcpBackupPath()
	if err != nil {
		return b, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return b, err
	}
	return b, json.Unmarshal(data, &b)
}

func clearTCPBackup() {
	if p, err := tcpBackupPath(); err == nil {
		_ = os.Remove(p)
	}
}

// shortPath returns the last path segment (e.g. an interface GUID) for tidy logs.
func shortPath(p string) string {
	if i := strings.LastIndex(p, `\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
