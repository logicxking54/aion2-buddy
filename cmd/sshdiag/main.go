// Command sshdiag measures the hidden relay->game-server hop (RTT, loss, route,
// live-socket retransmits) that the tunnel's client<->relay ping cannot see.
// Use it to vet a candidate relay VPS before deploying: a good host shows a low
// relay->server RTT (~5-15ms to HiNet), not the ~65ms a poorly-peered cloud adds.
//
//	go run ./cmd/sshdiag -host <vm-ip> -user root -pass <password> [-target 210.242.123.181]
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	host := flag.String("host", "", "relay VM IP/host (required)")
	user := flag.String("user", "root", "SSH user")
	pass := flag.String("pass", "", "SSH password (required)")
	port := flag.String("port", "22", "SSH port")
	target := flag.String("target", "210.242.123.181", "game-server IP to measure the hop to")
	flag.Parse()
	if *host == "" || *pass == "" {
		fmt.Println("usage: go run ./cmd/sshdiag -host <ip> -user root -pass <pw> [-target <gameIP>]")
		os.Exit(2)
	}

	client, err := ssh.Dial("tcp", net.JoinHostPort(*host, *port), &ssh.ClientConfig{
		User:            *user,
		Auth:            []ssh.AuthMethod{ssh.Password(*pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	})
	if err != nil {
		fmt.Println("SSH dial failed:", err)
		os.Exit(1)
	}
	defer client.Close()

	t := *target
	script := `
echo "===== LIVE RELAY->SERVER SOCKET (ss -ti) — RTT & retransmits of the real game socket ====="
ss -tinp "dst ` + t + `" 2>/dev/null || echo "(no live socket — be in-game when running this)"
echo
echo "===== PING relay->server (30 pkts) — ICMP often blocked; TCP RTT above is authoritative ====="
ping -c 30 -i 0.2 -W 1 ` + t + ` 2>&1 | tail -4
echo
echo "===== TCP connect RTT to game port (10x) — measures the hop even when ICMP is blocked ====="
for i in $(seq 10); do
  python3 - "$@" <<'PY' 2>/dev/null || break
import socket,time,sys
s=socket.socket(); s.settimeout(2)
t0=time.time()
try:
    s.connect(("` + t + `",13328)); print("connect %.1f ms"%((time.time()-t0)*1000)); s.close()
except Exception as e: print("fail",e)
PY
done
echo
echo "===== TRACEROUTE relay->server ====="
( command -v traceroute >/dev/null && traceroute -n -w1 -q1 -m 20 ` + t + ` 2>&1 | tail -20 ) || echo "traceroute not installed"
`
	sess, err := client.NewSession()
	if err != nil {
		fmt.Println("session failed:", err)
		os.Exit(1)
	}
	defer sess.Close()
	out, _ := sess.CombinedOutput(script)
	fmt.Println(string(out))
}
