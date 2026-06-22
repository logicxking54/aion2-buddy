//go:build windows

package oversize

import (
	"context"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// excludePorts are non-game services we never treat as the Aion 2 server
// endpoint (mirrors internal/capture/ports.go).
var excludePorts = map[int]struct{}{80: {}, 443: {}, 8080: {}, 8443: {}, 53: {}, 853: {}}

// discoverServerIP finds the Aion 2 game server's IP from the game process's
// established, non-loopback TCP connections (same technique as the capture
// package's portTracker). Returns the first plausible server IP, if any.
func discoverServerIP(ctx context.Context) (net.IP, bool) {
	ips := discoverServerIPs(ctx)
	if len(ips) == 0 {
		return nil, false
	}
	return ips[0], true
}

// discoverServerIPs returns ALL distinct game-server IPs reachable through the
// game (login + world servers may be on different IPs — and the geo-check is
// usually at login, so every one must be routed through the relay).
//
// It follows both the Aion 2 process AND any running network booster/proxy
// (ExitLag, WTFast, …): those tools run the game through a LOCAL proxy, so the
// Aion 2 process only ever connects to 127.0.0.1 and the real outbound sockets
// to the game servers belong to the booster process instead. Without this,
// split-tunnel discovery finds nothing whenever a booster is active.
func discoverServerIPs(ctx context.Context) []net.IP {
	pids := gameAndBoosterPIDs(ctx)
	if len(pids) == 0 {
		return nil
	}
	netstat := runHidden(ctx, "netstat", "-ano")
	if netstat == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var out []net.IP
	for _, line := range strings.Split(netstat, "\n") {
		if !strings.Contains(line, "ESTABLISHED") || !strings.Contains(line, "TCP") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 5 {
			continue
		}
		if _, ok := pids[parts[len(parts)-1]]; !ok {
			continue
		}
		host, portStr, ok := splitHostPort(parts[2]) // foreign address
		if !ok {
			continue
		}
		if strings.HasPrefix(host, "127.") || host == "[::1]" {
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}
		if _, ex := excludePorts[port]; ex {
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil || ip.To4() == nil {
			continue
		}
		if _, dup := seen[host]; dup {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, ip)
	}
	return out
}

// boosterNameLikes are PowerShell -like patterns for network boosters/proxies
// that front the game with a local proxy. When one of these runs, the game's
// real server sockets live in the booster process, not Aion 2, so split-tunnel
// discovery must follow it too. Patterns are case-insensitive.
var boosterNameLikes = []string{
	"*exitlag*", // ExitLag (+ ExitLagPmService)
	"*wtfast*",  // WTFast
	"*noping*",  // NoPing
	"*mudfish*", // Mudfish
}

// gameAndBoosterPIDs returns the PIDs of the Aion 2 process and any running
// network booster/proxy. Non-game booster sockets (license/update on 80/443,
// the relay itself) are filtered out downstream by excludePorts and the relay
// exclusion, so over-matching here is harmless.
func gameAndBoosterPIDs(ctx context.Context) map[string]struct{} {
	// Match any process whose name contains "aion" (Aion2, aion2client, launchers…),
	// not just an exact "Aion2", plus the known boosters, so split-tunnel reliably
	// finds the game's real outbound PIDs whether or not a booster is in the path.
	likes := append([]string{"*aion*"}, boosterNameLikes...)
	clauses := make([]string, len(likes))
	for i, p := range likes {
		clauses[i] = "$_.ProcessName -like '" + p + "'"
	}
	filter := strings.Join(clauses, " -or ")
	out := runHidden(ctx, "powershell", "-NoProfile", "-Command",
		"Get-Process | Where-Object { "+filter+" } | Select-Object -ExpandProperty Id")
	pids := map[string]struct{}{}
	for _, line := range strings.Split(out, "\n") {
		s := strings.TrimSpace(line)
		if s != "" && isDigits(s) {
			pids[s] = struct{}{}
		}
	}
	return pids
}

// runHidden runs a console command without flashing a window and returns stdout.
func runHidden(ctx context.Context, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, _ := cmd.Output()
	return string(out)
}

func splitHostPort(s string) (host, port string, ok bool) {
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
