//go:build windows

package main

import (
	"context"

	"aion2tmp/internal/oversize"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Oversize is the Wails-bound façade over the Oversize Network VPN. Exported
// methods are callable from the frontend (window.go.main.Oversize.*); relay
// events are forwarded to the frontend (oversize:status|log|ping|server).
type Oversize struct {
	ctx   context.Context
	relay *oversize.Relay
}

// NewOversize wires the relay's event emitter to runtime.EventsEmit.
func NewOversize() *Oversize {
	o := &Oversize{}
	o.relay = oversize.NewRelay(func(event string, payload any) {
		if o.ctx != nil {
			runtime.EventsEmit(o.ctx, event, payload)
		}
	})
	return o
}

func (o *Oversize) setContext(ctx context.Context) { o.ctx = ctx }

// Connect brings up the VPN to relayIP using the tunnel key from SetupServer.
// fullTunnel routes all traffic (else only Aion 2 servers); knownGameIPs are the
// previously-learned game server IPs, pre-routed immediately in split mode.
func (o *Oversize) Connect(relayIP, key string, fullTunnel bool, knownGameIPs []string) error {
	return o.relay.Connect(relayIP, key, fullTunnel, knownGameIPs)
}

// Disconnect tears the VPN down and restores normal routing.
func (o *Oversize) Disconnect() {
	o.relay.Stop()
}

// IsConnected reports whether the VPN is up.
func (o *Oversize) IsConnected() bool {
	return o.relay.IsConnected()
}

// SetupServer SSHes into the VM, deploys the relay daemon, and returns the
// tunnel key (hex) to store in config and pass to Connect.
func (o *Oversize) SetupServer(ip string, port int, user, pass string) (string, error) {
	return o.relay.Setup(ip, port, user, pass)
}
