//go:build windows

package capture

import (
	"context"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// portTracker discovers the Aion 2 server ports from the game process's
// connections. Two modes:
//   - direct:   game connects straight to the server → capture inbound on those
//     remote ports.
//   - loopback: game connects to a local proxy/VPN (ExitLag, GearUp) on
//     127.0.0.1 → the server data flows over loopback, so we capture loopback
//     traffic from the proxy's local port instead.
//
// A short memory keeps a port for ~30s after it disappears so the capture
// handle doesn't churn.
type portTracker struct {
	mu       sync.Mutex
	active   map[int]struct{}
	lastSeen map[int]time.Time
	loopback bool
	onChange func(ports []int, loopback bool)
	stop     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

var excludePorts = map[int]struct{}{80: {}, 443: {}, 8080: {}, 8443: {}, 53: {}, 853: {}}

const portMemory = 30 * time.Second

func newPortTracker() *portTracker {
	return &portTracker{active: map[int]struct{}{}, lastSeen: map[int]time.Time{}}
}

func (pt *portTracker) start(onChange func(ports []int, loopback bool)) {
	pt.onChange = onChange
	pt.stop = make(chan struct{})
	pt.ctx, pt.cancel = context.WithCancel(context.Background())
	pt.refresh()
	pt.wg.Add(1)
	go func() {
		defer pt.wg.Done()
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-pt.stop:
				return
			case <-t.C:
				pt.refresh()
			}
		}
	}()
}

func (pt *portTracker) stopTracking() {
	if pt.cancel != nil {
		pt.cancel() // kill any in-flight netstat/powershell so we don't block
	}
	if pt.stop != nil {
		close(pt.stop)
		pt.stop = nil
	}
	pt.wg.Wait()
}

// get returns the active ports and whether we're in loopback (proxy/VPN) mode.
func (pt *portTracker) get() ([]int, bool) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return sortedKeys(pt.active), pt.loopback
}

func (pt *portTracker) refresh() {
	pids := getAionPIDs(pt.ctx)
	if len(pids) == 0 {
		return
	}
	netstat := runHidden(pt.ctx, "netstat", "-ano")
	if netstat == "" {
		return
	}

	// Direct game-server connections take priority; otherwise fall back to a
	// loopback proxy (ExitLag/GearUp) the game talks to on 127.0.0.1.
	detected := findDirectPorts(netstat, pids)
	loopback := false
	if len(detected) == 0 {
		if proxy := findProxyPorts(netstat, pids); len(proxy) > 0 {
			detected = proxy
			loopback = true
		}
	}

	now := time.Now()
	pt.mu.Lock()
	// On a mode flip, drop the memory so direct/loopback ports never mix.
	if loopback != pt.loopback {
		pt.lastSeen = map[int]time.Time{}
		pt.active = map[int]struct{}{}
		pt.loopback = loopback
	}
	for p := range detected {
		pt.lastSeen[p] = now
	}
	effective := map[int]struct{}{}
	for p := range detected {
		effective[p] = struct{}{}
	}
	for p, ts := range pt.lastSeen {
		if now.Sub(ts) <= portMemory {
			effective[p] = struct{}{}
		} else {
			delete(pt.lastSeen, p)
		}
	}
	changed := !sameSet(pt.active, effective)
	pt.active = effective
	cb := pt.onChange
	lb := pt.loopback
	pt.mu.Unlock()

	if changed && cb != nil {
		cb(sortedKeys(effective), lb)
	}
}

func getAionPIDs(ctx context.Context) map[string]struct{} {
	out := runHidden(ctx, "powershell", "-NoProfile", "-Command",
		"Get-Process -Name 'Aion2' -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id")
	pids := map[string]struct{}{}
	for _, line := range strings.Split(out, "\n") {
		s := strings.TrimSpace(line)
		if s != "" && isDigits(s) {
			pids[s] = struct{}{}
		}
	}
	return pids
}

// findDirectPorts: ESTABLISHED non-loopback remote ports owned by the game.
func findDirectPorts(netstat string, pids map[string]struct{}) map[int]struct{} {
	ports := map[int]struct{}{}
	forEachConn(netstat, pids, func(host string, port int) {
		if strings.HasPrefix(host, "127.") || host == "[::1]" {
			return
		}
		if _, ex := excludePorts[port]; ex {
			return
		}
		ports[port] = struct{}{}
	})
	return ports
}

// findProxyPorts: the local (loopback) remote ports the game connects to — i.e.
// the proxy/VPN's listen ports carrying the forwarded server stream.
func findProxyPorts(netstat string, pids map[string]struct{}) map[int]struct{} {
	ports := map[int]struct{}{}
	forEachConn(netstat, pids, func(host string, port int) {
		if strings.HasPrefix(host, "127.") || host == "[::1]" {
			ports[port] = struct{}{}
		}
	})
	return ports
}

// forEachConn invokes fn(remoteHost, remotePort) for each ESTABLISHED TCP line
// owned by one of pids.
func forEachConn(netstat string, pids map[string]struct{}, fn func(host string, port int)) {
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
		if port, err := strconv.Atoi(portStr); err == nil {
			fn(host, port)
		}
	}
}

// runHidden runs a console command without flashing a window and returns stdout.
// Bound to ctx so it can be cancelled (killed) when capture stops.
func runHidden(ctx context.Context, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, _ := cmd.Output()
	return string(out)
}

// ── small helpers ─────────────────────────────────────────────

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

func sortedKeys(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func sameSet(a, b map[int]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
