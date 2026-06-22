//go:build windows

// NIC tuning mod — disables interrupt moderation, packet coalescing, offloads and
// power-saving on the physical network adapters to cut jitter. These advanced
// adapter properties are applied through PowerShell's Set-NetAdapterAdvancedProperty
// (the same mechanism the original tool uses), which applies them live. Applying
// records each property's ORIGINAL value so revert can put every adapter back
// exactly as it was. See nettweaks_windows.go for the shared backup contract.
package gamemod

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// nicKeywords are the adapter advanced-property registry keywords we turn off.
// Not every NIC exposes every keyword; missing ones are simply skipped.
var nicKeywords = []string{
	"*InterruptModeration",            // batches interrupts → adds latency
	"*RscIPv4", "*RscIPv6",            // receive segment coalescing
	"*LsoV2IPv4", "*LsoV2IPv6",        // large send offload
	"*FlowControl",                    // pause frames
	"*EEE",                            // energy-efficient ethernet
	"EnableGreenEthernet",             // green ethernet power saving
	"*WakeOnMagicPacket", "*WakeOnPattern", // wake-on-LAN monitoring
}

// nicChange records one adapter property's original value, for rollback.
type nicChange struct {
	Adapter string `json:"adapter"`
	Keyword string `json:"keyword"`
	Value   string `json:"value"`   // original RegistryValue
	Display string `json:"display"` // original DisplayValue (for human logs)
}

func nicBackupPath() (string, error) {
	d, err := backupDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "nic-backup.json"), nil
}

// NICStatus reports whether the NIC tuning tweak is applied.
func NICStatus() TweakStatus {
	p, err := nicBackupPath()
	applied := err == nil && fileExists(p)
	detail := ""
	if changes, e := loadNICBackup(); e == nil {
		names := map[string]bool{}
		for _, c := range changes {
			names[c.Adapter] = true
		}
		detail = fmt.Sprintf("%d adapter(s) tuned", len(names))
	}
	return TweakStatus{Applied: applied, Detail: detail}
}

// ApplyNIC disables the latency-adding adapter features on every physical, up
// adapter, backing up each original value first. The adapter(s) reset briefly.
func ApplyNIC(log Logger) (TweakStatus, error) {
	out, err := runPowerShell(nicApplyScript())
	if err != nil {
		return TweakStatus{}, fmt.Errorf("network adapter tuning failed (run as Administrator): %w", err)
	}
	var changes []nicChange
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var c nicChange
		if json.Unmarshal([]byte(line), &c) == nil && c.Keyword != "" {
			changes = append(changes, c)
			log.log("NIC: %s — %s: %q → Disabled", c.Adapter, friendlyKeyword(c.Keyword), c.Display)
		}
	}
	if len(changes) == 0 {
		log.log("NIC: no matching adapter settings found to change.")
	}
	if err := saveNICBackup(changes); err != nil {
		log.log("NIC: warning — could not save rollback data: %v", err)
	}
	log.log("NIC: done — changed %d setting(s); the adapter(s) reset briefly.", len(changes))
	return NICStatus(), nil
}

// RevertNIC restores every adapter property recorded in the backup to its
// original value.
func RevertNIC(log Logger) (TweakStatus, error) {
	changes, err := loadNICBackup()
	if err != nil {
		log.log("NIC: no saved rollback data — nothing to revert.")
		clearNICBackup()
		return NICStatus(), nil
	}
	var sb strings.Builder
	sb.WriteString("$ErrorActionPreference='SilentlyContinue'\n")
	for _, c := range changes {
		fmt.Fprintf(&sb, "Set-NetAdapterAdvancedProperty -Name '%s' -RegistryKeyword '%s' -RegistryValue '%s'\n",
			psQuote(c.Adapter), psQuote(c.Keyword), psQuote(c.Value))
		log.log("NIC: restore %s — %s → %q", c.Adapter, friendlyKeyword(c.Keyword), c.Display)
	}
	if _, err := runPowerShell(sb.String()); err != nil {
		return TweakStatus{}, fmt.Errorf("restore failed (run as Administrator): %w", err)
	}
	clearNICBackup()
	log.log("NIC: done — original adapter settings restored.")
	return NICStatus(), nil
}

// nicApplyScript builds a PowerShell script that, for every physical up adapter,
// reads each tweak keyword's current value, prints it as one JSON line (for our
// backup), then sets it to 0 (disabled).
func nicApplyScript() string {
	kws := "'" + strings.Join(nicKeywords, "','") + "'"
	return `$ErrorActionPreference='SilentlyContinue'
$kw=@(` + kws + `)
foreach($a in (Get-NetAdapter -Physical | Where-Object {$_.Status -eq 'Up'})){
 foreach($k in $kw){
  $p=Get-NetAdapterAdvancedProperty -Name $a.Name -RegistryKeyword $k
  if($p -ne $null){
   [pscustomobject]@{adapter=$a.Name;keyword=$k;value=[string]($p.RegistryValue[0]);display=[string]$p.DisplayValue}|ConvertTo-Json -Compress
   Set-NetAdapterAdvancedProperty -Name $a.Name -RegistryKeyword $k -RegistryValue 0
  }
 }
}`
}

func runPowerShell(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// psQuote escapes a string for use inside a single-quoted PowerShell literal.
func psQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func friendlyKeyword(kw string) string {
	switch kw {
	case "*InterruptModeration":
		return "Interrupt Moderation"
	case "*RscIPv4", "*RscIPv6":
		return "Receive Segment Coalescing"
	case "*LsoV2IPv4", "*LsoV2IPv6":
		return "Large Send Offload"
	case "*FlowControl":
		return "Flow Control"
	case "*EEE":
		return "Energy-Efficient Ethernet"
	case "EnableGreenEthernet":
		return "Green Ethernet"
	case "*WakeOnMagicPacket", "*WakeOnPattern":
		return "Wake-on-LAN"
	default:
		return kw
	}
}

func saveNICBackup(c []nicChange) error {
	p, err := nicBackupPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func loadNICBackup() ([]nicChange, error) {
	p, err := nicBackupPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c []nicChange
	return c, json.Unmarshal(data, &c)
}

func clearNICBackup() {
	if p, err := nicBackupPath(); err == nil {
		_ = os.Remove(p)
	}
}
