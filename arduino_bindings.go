//go:build windows

package main

import (
	"context"

	"aion2tmp/internal/arduino"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const arduinoBaud = 115200

// Arduino is the Wails-bound façade over the serial board connection.
type Arduino struct {
	ctx   context.Context
	board *arduino.Board
}

func NewArduino() *Arduino {
	return &Arduino{board: arduino.New()}
}

func (a *Arduino) setContext(ctx context.Context) { a.ctx = ctx }

func (a *Arduino) emitStatus() {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "arduino:status", map[string]interface{}{
			"connected": a.board.IsOpen(),
			"port":      a.board.Port(),
		})
	}
}

// emitLog pushes an Arduino-related line to the app's shared log box.
func (a *Arduino) emitLog(msg, kind string) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "arduino:log", map[string]interface{}{"msg": msg, "kind": kind})
	}
}

// DetectPort returns the auto-detected Arduino COM port, or "".
func (a *Arduino) DetectPort() string {
	return arduino.DetectPort()
}

// AutoSelect scans for the Arduino, logs what it found to the app log, and
// returns the chosen (present) COM port, or "".
func (a *Arduino) AutoSelect() string {
	a.emitLog("Auto-select: scanning for Arduino…", "info")
	port, info := arduino.Detect()
	for _, line := range info {
		a.emitLog(line, "info")
	}
	return port
}

// CurrentPort returns the currently connected port, or "".
func (a *Arduino) CurrentPort() string {
	return a.board.Port()
}

// ConnectBoard opens the given COM port.
func (a *Arduino) ConnectBoard(port string) error {
	err := a.board.Open(port, arduinoBaud)
	a.emitStatus()
	if err != nil {
		a.emitLog("Board connect "+port+" failed: "+err.Error(), "warn")
	} else {
		a.emitLog("Board connected — "+port, "info")
	}
	return err
}

// DisconnectBoard closes the connection.
func (a *Arduino) DisconnectBoard() {
	a.board.Close()
	a.emitStatus()
	a.emitLog("Board disconnected", "info")
}

// BoardConnected reports whether the board is currently open.
func (a *Arduino) BoardConnected() bool {
	return a.board.IsOpen()
}

// SendKey sends one key token to the board (see the Arduino sketch's protocol).
func (a *Arduino) SendKey(token string) error {
	err := a.board.SendKey(token)
	if err != nil {
		a.emitStatus() // a failed write drops the connection
		a.emitLog("Key "+token+" failed: "+err.Error(), "warn")
	} else {
		a.emitLog("Key "+token, "cast")
	}
	return err
}
