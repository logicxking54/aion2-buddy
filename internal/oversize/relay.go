//go:build windows

// Package oversize implements the "Oversize Network" VPN: a WinTUN userspace
// tunnel that routes Aion 2 through a relay daemon on the user's Taiwan VM so
// the game egresses there (bypassing geo-block, lowering ping). The client
// captures game TCP via a TUN adapter + userspace TCP NAT (tcpnat_windows.go),
// and carries it over an encrypted UDP+ARQ tunnel (internal/oversize/tunnel) to
// the relay (cmd/aion2-relay), which is deployed to the VM over SSH by Setup.
//
// Requires Administrator (WinTUN adapter + route table).
package oversize

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"aion2tmp/internal/oversize/proto"
	"aion2tmp/internal/oversize/tunnel"

	"golang.org/x/crypto/ssh"
)

// relayBinary is the static Linux relay daemon, deployed to the VM by Setup.
//
//go:embed embed/aion2-relay
var relayBinary []byte

const (
	tunAdapterName = "Aion2Buddy"
	tunAddr        = "10.200.0.2"
	tunMask        = "255.255.255.0"
	relayUDPPort   = 443
)

// Relay owns the live VPN (TUN + routes + tunnel) and the SSH deploy flow.
type Relay struct {
	emit func(event string, payload any)

	mu      sync.Mutex
	running bool
	dev     *tunDevice
	rtr     *router
	nat     *tcpNAT
	cli     *tunnel.Client
	cancel  context.CancelFunc
}

// NewRelay creates a relay that reports status/log/ping through emit.
func NewRelay(emit func(event string, payload any)) *Relay { return &Relay{emit: emit} }

func (r *Relay) fire(event string, payload any) {
	if r.emit != nil {
		r.emit(event, payload)
	}
}
func (r *Relay) log(msg string)  { r.fire("oversize:log", msg) }
func (r *Relay) status(s string) { r.fire("oversize:status", s) }

// IsConnected reports whether the VPN is up.
func (r *Relay) IsConnected() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Connect brings up the VPN: dial the relay, create the TUN, install routes,
// and start the TCP NAT. relayIP is the VM's public IP; keyHex is the tunnel key
// from Setup; fullTunnel routes all public traffic (else only the game server
// IPs — knownGameIPs are pre-routed immediately, and more are discovered live).
func (r *Relay) Connect(relayIP, keyHex string, fullTunnel bool, knownGameIPs []string) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	if relayIP == "" || keyHex == "" {
		return errors.New("relay IP and tunnel key are required (deploy the server first)")
	}
	r.status("connecting")
	r.log("Connecting tunnel to " + relayIP + " …")

	cli, err := tunnel.Dial(net.JoinHostPort(relayIP, strconv.Itoa(relayUDPPort)), keyHex,
		func(ms int) { r.fire("oversize:ping", ms) })
	if err != nil {
		r.status("error")
		r.log("Tunnel dial failed: " + err.Error())
		return err
	}

	dev, err := createTUN(tunAdapterName)
	if err != nil {
		cli.Close()
		r.status("error")
		r.log(err.Error())
		return err
	}
	if err := dev.configure(tunAddr, tunMask); err != nil {
		dev.close()
		cli.Close()
		r.status("error")
		r.log("Adapter config failed: " + err.Error())
		return err
	}

	rtr, err := newRouter(dev.ifIndex, r.log)
	if err != nil {
		dev.close()
		cli.Close()
		r.status("error")
		r.log("Routing setup failed: " + err.Error())
		return err
	}
	rtr.exclude([]string{relayIP}) // keep the tunnel + LAN off the TUN

	ctx, cancel := context.WithCancel(context.Background())
	nat := newTCPNAT(dev, cli, r.log)

	r.mu.Lock()
	r.running = true
	r.dev, r.rtr, r.nat, r.cli, r.cancel = dev, rtr, nat, cli, cancel
	r.mu.Unlock()

	if fullTunnel {
		rtr.fullTunnel()
		r.log("Full-tunnel: routing all traffic through the relay.")
		// Still learn game IPs (persisted by the UI) so split-tunnel works later.
		go r.gameRouteWatcher(ctx, rtr, relayIP, false)
	} else {
		nets := 0
		for _, c := range defaultGameNets {
			if err := rtr.routeGameNet(c); err == nil {
				nets++
			}
		}
		ips := 0
		for _, s := range knownGameIPs {
			if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
				rtr.routeGameIP(ip.String())
				ips++
			}
		}
		r.log(fmt.Sprintf("Split-tunnel: pre-routed %d Aion 2 network(s) + %d known IP(s). Launch/relog the game.", nets, ips))
		go r.gameRouteWatcher(ctx, rtr, relayIP, true)
	}

	go nat.run()
	r.status("connected")
	return nil
}

// Stop tears the VPN down and restores routing.
func (r *Relay) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	dev, rtr, nat, cli, cancel := r.dev, r.rtr, r.nat, r.cli, r.cancel
	r.dev, r.rtr, r.nat, r.cli, r.cancel = nil, nil, nil, nil, nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if nat != nil {
		nat.stopNAT()
	}
	if rtr != nil {
		rtr.teardown()
	}
	if dev != nil {
		dev.close()
	}
	if cli != nil {
		cli.Close()
	}
	r.status("disconnected")
	r.log("Disconnected.")
}

// gameRouteWatcher polls for Aion 2 server IPs, emits each new one (the UI
// persists them for next time), and — when routeIntoTun is set (split-tunnel) —
// routes it into the TUN. The relay IP is never routed in (loop prevention).
func (r *Relay) gameRouteWatcher(ctx context.Context, rtr *router, relayIP string, routeIntoTun bool) {
	relaySet := map[[4]byte]struct{}{}
	if ip := net.ParseIP(relayIP); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			relaySet[[4]byte{v4[0], v4[1], v4[2], v4[3]}] = struct{}{}
		}
	}
	seen := map[string]struct{}{}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, ip := range discoverServerIPs(ctx) {
				v4 := ip.To4()
				if v4 == nil {
					continue
				}
				var arr [4]byte
				copy(arr[:], v4)
				if isExcludedGameIP(arr, relaySet) {
					continue
				}
				s := ip.String()
				if _, dup := seen[s]; dup {
					continue
				}
				seen[s] = struct{}{}
				r.fire("oversize:server", s) // UI persists it to config.oversize.gameIPs
				if routeIntoTun {
					rtr.routeGameIP(s)
					r.log("Routing Aion 2 server " + s + " through the relay.")
				} else {
					r.log("Learned Aion 2 server " + s + " (saved for split-tunnel).")
				}
			}
		}
	}
}

// ── Server deployment (SSH) ───────────────────────────────────

const relaySystemdUnit = `[Unit]
Description=Aion2 Buddy Relay
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/aion2-buddy-relay
Environment=LISTEN_ADDR=0.0.0.0:443
Environment=KEY_FILE=/etc/aion2-buddy-relay/app.key
Environment=AION2_RELAY_DUPLICATE=2
Restart=always
RestartSec=2
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`

// Setup SSHes into the VM, deploys/updates the relay daemon (binary + systemd +
// firewall), ensures a tunnel key, and returns that key (hex). Streams progress
// to the UI log. Idempotent: reuses an existing key.
func (r *Relay) Setup(ip string, port int, user, pass string) (string, error) {
	if user == "" || ip == "" {
		return "", errors.New("relay IP and username are required")
	}
	if port <= 0 {
		port = 22
	}
	r.log("Deploy: connecting to " + ip + ":" + strconv.Itoa(port) + " …")
	client, err := ssh.Dial("tcp", net.JoinHostPort(ip, strconv.Itoa(port)), &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	})
	if err != nil {
		r.log("Deploy: SSH connect failed: " + err.Error())
		return "", err
	}
	defer client.Close()

	// Ensure key dir + tunnel key (reuse existing if valid).
	if _, err := sshExec(client, "mkdir -p /etc/aion2-buddy-relay && chmod 700 /etc/aion2-buddy-relay"); err != nil {
		return "", err
	}
	existing, _ := sshExec(client, "cat /etc/aion2-buddy-relay/app.key 2>/dev/null || true")
	keyHex := strings.TrimSpace(existing)
	if !validKeyHex(keyHex) {
		keyHex, err = proto.GenerateKeyHex()
		if err != nil {
			return "", err
		}
		if err := sshUpload(client, []byte(keyHex), "/etc/aion2-buddy-relay/app.key", "0600"); err != nil {
			return "", fmt.Errorf("upload key: %w", err)
		}
		r.log("Deploy: generated new tunnel key.")
	} else {
		r.log("Deploy: reusing existing tunnel key.")
	}

	// Stop, upload binary + unit, (re)start, open firewall — streamed.
	if _, err := sshExec(client, "systemctl stop aion2-buddy-relay 2>/dev/null || true"); err != nil {
		return "", err
	}
	r.log("Deploy: uploading relay binary (" + strconv.Itoa(len(relayBinary)) + " bytes) …")
	if err := sshUpload(client, relayBinary, "/usr/local/bin/aion2-buddy-relay", "0755"); err != nil {
		return "", fmt.Errorf("upload binary: %w", err)
	}
	if err := sshUpload(client, []byte(relaySystemdUnit), "/etc/systemd/system/aion2-buddy-relay.service", "0644"); err != nil {
		return "", fmt.Errorf("upload unit: %w", err)
	}
	if err := r.sshStream(client, deployScript); err != nil {
		return "", err
	}
	r.log("Deploy: done. Add a GCP VPC rule allowing udp:443 if you haven't.")
	return keyHex, nil
}

// deployScript reloads systemd, opens the firewall (firewalld/ufw/iptables),
// enables + starts the relay, and verifies it is active.
const deployScript = `
echo '[deploy] reloading systemd'
systemctl daemon-reload
command -v restorecon >/dev/null 2>&1 && restorecon -v /usr/local/bin/aion2-buddy-relay 2>/dev/null || true
echo '[deploy] opening firewall for udp/tcp 443'
if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  firewall-cmd --permanent --add-port=443/udp >/dev/null 2>&1
  firewall-cmd --permanent --add-port=443/tcp >/dev/null 2>&1
  firewall-cmd --reload >/dev/null 2>&1
fi
command -v ufw >/dev/null 2>&1 && ufw allow 443/udp 2>/dev/null && ufw allow 443/tcp 2>/dev/null
iptables -C INPUT -p udp --dport 443 -j ACCEPT 2>/dev/null || iptables -I INPUT -p udp --dport 443 -j ACCEPT 2>/dev/null
echo '[deploy] enabling + starting service'
systemctl enable aion2-buddy-relay 2>/dev/null || true
systemctl restart aion2-buddy-relay
sleep 1
if systemctl is-active --quiet aion2-buddy-relay; then
  echo '[deploy] relay is active on udp/tcp 443'
else
  echo '[deploy] WARNING relay not active:'
  journalctl -u aion2-buddy-relay -n 15 --no-pager 2>/dev/null || true
fi
echo '[deploy] done'
`

func validKeyHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// sshExec runs a command and returns trimmed combined stdout.
func sshExec(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	return strings.TrimSpace(string(out)), err
}

// sshUpload writes data to remotePath with the given chmod mode (octal string),
// via a stdin->file copy, atomically replacing any existing file.
func sshUpload(client *ssh.Client, data []byte, remotePath, mode string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdin = bytes.NewReader(data)
	tmp := remotePath + ".tmp"
	cmd := fmt.Sprintf("cat > %q && chmod %s %q && mv -f %q %q", tmp, mode, tmp, tmp, remotePath)
	if out, err := sess.CombinedOutput(cmd); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// sshStream runs a bash script over SSH and streams its output to the UI log.
func (r *Relay) sshStream(client *ssh.Client, script string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	stdout, _ := sess.StdoutPipe()
	stderr, _ := sess.StderrPipe()
	sess.Stdin = strings.NewReader(script)
	var wg sync.WaitGroup
	stream := func(rd io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(rd)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			r.log(sc.Text())
		}
	}
	wg.Add(2)
	go stream(stdout)
	go stream(stderr)
	if err := sess.Start("bash -s"); err != nil {
		return err
	}
	runErr := sess.Wait()
	wg.Wait()
	return runErr
}
