//go:build windows

package oversize

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
)

const tunGateway = "10.200.0.1" // the TUN's "other side" (route next-hop)

// defaultGameNets are the known Aion 2 server networks (HiNet, Taiwan). They are
// pre-routed into the TUN in split-tunnel mode so the geo-blocked login/world
// handshake works on the very first run — before any IP has been learned — and so
// world-server IPs that rotate within the subnet are covered without a discovery
// race. Discovery still adds any server IPs outside these ranges live.
var defaultGameNets = []string{"210.242.123.0/24"}

// routeSpec identifies a route by destination + mask (enough to delete it).
type routeSpec struct {
	dest string
	mask string
}

// router installs and removes the routes that steer game traffic into the TUN
// while keeping the relay link, LAN, and (in split mode) everything else on the
// real gateway. It records every route it adds so teardown is exact, and so a
// crash can't permanently strand the user's connectivity.
type router struct {
	ifIndex    uint32
	origGW     string
	mu         sync.Mutex
	added      []routeSpec
	log        func(string)
}

func newRouter(ifIndex uint32, log func(string)) (*router, error) {
	gw, err := defaultGateway()
	if err != nil {
		return nil, err
	}
	return &router{ifIndex: ifIndex, origGW: gw, log: log}, nil
}

// excludeViaGateway pins host/networks to the original gateway so they never
// enter the tunnel (relay IP — loop prevention; RFC1918 + link-local — LAN/DNS).
func (r *router) exclude(relayIPs []string) {
	for _, ip := range relayIPs {
		r.add(ip, "255.255.255.255", r.origGW, false)
	}
	r.add("10.0.0.0", "255.0.0.0", r.origGW, false)
	r.add("172.16.0.0", "255.240.0.0", r.origGW, false)
	r.add("192.168.0.0", "255.255.0.0", r.origGW, false)
	r.add("169.254.0.0", "255.255.0.0", r.origGW, false)
}

// fullTunnel shadows the default route with two /1 routes via the TUN (covers
// 0.0.0.0/0 without replacing the original default).
func (r *router) fullTunnel() {
	r.add("0.0.0.0", "128.0.0.0", tunGateway, true)
	r.add("128.0.0.0", "128.0.0.0", tunGateway, true)
}

// routeGameIP sends one game-server IP into the TUN (split-tunnel mode).
func (r *router) routeGameIP(ip string) {
	r.add(ip, "255.255.255.255", tunGateway, true)
}

// routeGameNet sends a whole game-server network (CIDR) into the TUN. Used to
// pre-route the known Aion 2 subnets so split-tunnel works on a first run.
func (r *router) routeGameNet(cidr string) error {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	mask := net.IP(ipnet.Mask).To4()
	if mask == nil {
		return fmt.Errorf("only IPv4 nets supported: %s", cidr)
	}
	r.add(ipnet.IP.String(), mask.String(), tunGateway, true)
	return nil
}

// add installs a route and records it. viaTUN selects the TUN interface.
func (r *router) add(dest, mask, gw string, viaTUN bool) {
	args := []string{"add", dest, "mask", mask, gw, "metric", "5"}
	if viaTUN {
		args = append(args, "IF", strconv.Itoa(int(r.ifIndex)))
	}
	if _, err := runHidden2("route", args...); err != nil && r.log != nil {
		r.log(fmt.Sprintf("route add %s/%s failed: %v", dest, mask, err))
	}
	r.mu.Lock()
	r.added = append(r.added, routeSpec{dest, mask})
	r.mu.Unlock()
}

// teardown removes every route this router added. Idempotent.
func (r *router) teardown() {
	r.mu.Lock()
	added := r.added
	r.added = nil
	r.mu.Unlock()
	for _, rt := range added {
		runHidden2("route", "delete", rt.dest, "mask", rt.mask)
	}
}

// defaultGateway reads the current IPv4 default gateway from `route print`.
func defaultGateway() (string, error) {
	out, err := runHidden2("route", "print", "0.0.0.0")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == "0.0.0.0" && f[1] == "0.0.0.0" {
			return f[2], nil
		}
	}
	return "", fmt.Errorf("could not determine default gateway")
}

// isExcludedGameIP reports whether ip should never be pulled into the tunnel
// (loopback / private / link-local / multicast / the relay itself).
func isExcludedGameIP(ip [4]byte, relay map[[4]byte]struct{}) bool {
	if _, ok := relay[ip]; ok {
		return true
	}
	switch {
	case ip[0] == 127, ip[0] == 10, ip[0] == 0:
		return true
	case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31:
		return true
	case ip[0] == 192 && ip[1] == 168:
		return true
	case ip[0] == 169 && ip[1] == 254:
		return true
	case ip[0] >= 224: // multicast / reserved
		return true
	}
	return false
}
