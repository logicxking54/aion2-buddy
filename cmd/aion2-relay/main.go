// Command aion2-relay is the Oversize Network relay daemon deployed to the
// Taiwan VM. It listens for the encrypted UDP tunnel and forwards each flow to
// the real game server. Build a static Linux binary for deployment with:
//
//	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o aion2-relay ./cmd/aion2-relay
//
// Config via env / args:
//
//	LISTEN_ADDR             listen address (default 0.0.0.0:443)
//	AION2_RELAY_DUPLICATE   downstream datagram copies (default 1)
//	arg[1] or KEY_FILE      path to the 64-hex key file (default /etc/aion2-buddy-relay/app.key)
//	KEY                     the key hex directly (overrides the file)
package main

import (
	"log"
	"os"
	"strconv"
	"strings"

	"aion2tmp/internal/oversize/relayd"
)

func main() {
	listen := getenv("LISTEN_ADDR", "0.0.0.0:443")

	dup := 1
	if v := os.Getenv("AION2_RELAY_DUPLICATE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dup = n
		}
	}

	keyHex := strings.TrimSpace(os.Getenv("KEY"))
	if keyHex == "" {
		path := getenv("KEY_FILE", "/etc/aion2-buddy-relay/app.key")
		if len(os.Args) > 1 {
			path = os.Args[1]
		}
		b, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("read key file %s: %v", path, err)
		}
		keyHex = strings.TrimSpace(string(b))
	}

	logf := func(format string, args ...any) { log.Printf(format, args...) }
	if err := relayd.Run(listen, keyHex, dup, logf); err != nil {
		log.Fatalf("relay exited: %v", err)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
