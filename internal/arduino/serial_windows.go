//go:build windows

// Package arduino drives the Aion 2 Buddy keyboard sketch over a native
// Windows serial port. The sketch exposes a small line-oriented protocol at
// 115200 baud and emits real USB HID keyboard reports.
package arduino

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const firmwareID = "AION2_KEYBOARD_V2"

// Known Arduino-family USB vendor IDs (Leonardo and common ATmega32u4 clones).
var arduinoVIDs = []string{"VID_2341", "VID_2A03", "VID_1B4F", "VID_239A", "VID_2886"}

// livePorts returns the currently-present COM ports, upper-cased.
func livePorts() map[string]bool {
	ports := map[string]bool{}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.READ)
	if err != nil {
		return ports
	}
	defer key.Close()

	names, err := key.ReadValueNames(0)
	if err != nil {
		return ports
	}
	for _, name := range names {
		if value, _, readErr := key.GetStringValue(name); readErr == nil && value != "" {
			ports[strings.ToUpper(value)] = true
		}
	}
	return ports
}

// Detect scans the USB device tree and returns the first live Arduino COM port
// together with diagnostics suitable for an error message.
func Detect() (string, []string) {
	info := []string{}
	live := livePorts()
	liveList := make([]string, 0, len(live))
	for port := range live {
		liveList = append(liveList, port)
	}
	sort.Strings(liveList)
	if len(liveList) == 0 {
		info = append(info, "Live COM ports: (none)")
	} else {
		info = append(info, "Live COM ports: "+strings.Join(liveList, ", "))
	}

	root, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\USB`, registry.READ)
	if err != nil {
		return "", append(info, "Cannot read the USB device tree")
	}
	defer root.Close()

	type candidate struct {
		port  string
		label string
	}
	candidates := []candidate{}
	devices, _ := root.ReadSubKeyNames(-1)
	for _, device := range devices {
		upper := strings.ToUpper(device)
		isArduino := false
		for _, vid := range arduinoVIDs {
			if strings.Contains(upper, vid) {
				isArduino = true
				break
			}
		}
		if !isArduino {
			continue
		}

		deviceKey, openErr := registry.OpenKey(root, device, registry.READ)
		if openErr != nil {
			continue
		}
		instances, _ := deviceKey.ReadSubKeyNames(-1)
		for _, instance := range instances {
			instanceKey, instanceErr := registry.OpenKey(deviceKey, instance, registry.READ)
			if instanceErr != nil {
				continue
			}
			friendly, _, _ := instanceKey.GetStringValue("FriendlyName")
			port := ""
			if parameters, parameterErr := registry.OpenKey(instanceKey, `Device Parameters`, registry.READ); parameterErr == nil {
				port, _, _ = parameters.GetStringValue("PortName")
				parameters.Close()
			}
			instanceKey.Close()
			if port == "" {
				continue
			}

			label := friendly
			if label == "" {
				label = device
			}
			state := "stale"
			if live[strings.ToUpper(port)] {
				state = "present"
				candidates = append(candidates, candidate{port: strings.ToUpper(port), label: label})
			}
			info = append(info, fmt.Sprintf("Found %s -> %s (%s)", label, port, state))
		}
		deviceKey.Close()
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].port < candidates[j].port })
	if len(candidates) == 0 {
		return "", append(info, "No connected Arduino keyboard board found")
	}
	return candidates[0].port, append(info, "Selected: "+candidates[0].port)
}

// DCB mirrors the Win32 device control block fields needed for 8-N-1 serial.
type dcb struct {
	DCBlength  uint32
	BaudRate   uint32
	flags      uint32
	wReserved  uint16
	XonLim     uint16
	XoffLim    uint16
	ByteSize   byte
	Parity     byte
	StopBits   byte
	XonChar    byte
	XoffChar   byte
	ErrorChar  byte
	EofChar    byte
	EvtChar    byte
	wReserved1 uint16
}

type commTimeouts struct {
	ReadIntervalTimeout         uint32
	ReadTotalTimeoutMultiplier  uint32
	ReadTotalTimeoutConstant    uint32
	WriteTotalTimeoutMultiplier uint32
	WriteTotalTimeoutConstant   uint32
}

var (
	kernel32            = windows.NewLazyDLL("kernel32.dll")
	procSetCommState    = kernel32.NewProc("SetCommState")
	procSetCommTimeouts = kernel32.NewProc("SetCommTimeouts")
)

// Board is a connection to the Arduino. It is safe for concurrent use.
type Board struct {
	mu     sync.Mutex
	handle windows.Handle
	port   string
	open   bool
}

func New() *Board {
	return &Board{handle: windows.InvalidHandle}
}

// Open connects to a COM port at the requested baud rate.
func (b *Board) Open(port string, baud int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked()

	name, err := windows.UTF16PtrFromString(`\\.\` + strings.ToUpper(strings.TrimSpace(port)))
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", port, err)
	}

	state := dcb{
		DCBlength: uint32(unsafe.Sizeof(dcb{})),
		BaudRate:  uint32(baud),
		flags:     0x1011, // binary, DTR enabled, RTS enabled
		ByteSize:  8,
		Parity:    0,
		StopBits:  0,
	}
	if result, _, callErr := procSetCommState.Call(uintptr(handle), uintptr(unsafe.Pointer(&state))); result == 0 {
		windows.CloseHandle(handle)
		return fmt.Errorf("configure %s: %w", port, callErr)
	}

	timeouts := commTimeouts{
		ReadIntervalTimeout:       0xFFFFFFFF,
		WriteTotalTimeoutConstant: 200,
	}
	if result, _, callErr := procSetCommTimeouts.Call(uintptr(handle), uintptr(unsafe.Pointer(&timeouts))); result == 0 {
		windows.CloseHandle(handle)
		return fmt.Errorf("configure timeouts for %s: %w", port, callErr)
	}

	b.handle = handle
	b.port = strings.ToUpper(strings.TrimSpace(port))
	b.open = true
	return nil
}

func (b *Board) closeLocked() {
	if !b.open {
		return
	}
	windows.CloseHandle(b.handle)
	b.handle = windows.InvalidHandle
	b.open = false
}

// Close disconnects the board.
func (b *Board) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked()
}

func (b *Board) IsOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open
}

func (b *Board) Port() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.port
}

func (b *Board) writeCommandLocked(command string) error {
	if !b.open {
		return fmt.Errorf("board not connected")
	}
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "\r\n") {
		return fmt.Errorf("invalid Arduino command")
	}
	data := []byte(command + "\n")
	var written uint32
	if err := windows.WriteFile(b.handle, data, &written, nil); err != nil {
		b.closeLocked()
		return err
	}
	if written != uint32(len(data)) {
		return fmt.Errorf("short serial write: wrote %d of %d bytes", written, len(data))
	}
	return nil
}

// SendCommand writes one newline-terminated protocol command.
func (b *Board) SendCommand(command string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writeCommandLocked(command)
}

func (b *Board) KeyDown(token string) error { return b.SendCommand("down:" + token) }
func (b *Board) KeyUp(token string) error   { return b.SendCommand("up:" + token) }
func (b *Board) KeepAlive() error           { return b.SendCommand("keepalive") }
func (b *Board) ReleaseAll() error          { return b.SendCommand("releaseall") }

// Handshake verifies that the board is running the hold-capable V2 firmware.
// Ping is retried because opening a native USB serial port may briefly reset
// some ATmega32u4 boards.
func (b *Board) Handshake(timeout time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open {
		return fmt.Errorf("board not connected")
	}

	deadline := time.Now().Add(timeout)
	nextPing := time.Time{}
	line := make([]byte, 0, 64)
	buffer := make([]byte, 64)
	for time.Now().Before(deadline) {
		if nextPing.IsZero() || time.Now().After(nextPing) {
			if err := b.writeCommandLocked("ping"); err != nil {
				return err
			}
			nextPing = time.Now().Add(250 * time.Millisecond)
		}

		var read uint32
		if err := windows.ReadFile(b.handle, buffer, &read, nil); err != nil {
			b.closeLocked()
			return err
		}
		for _, value := range buffer[:read] {
			if value == '\n' || value == '\r' {
				if strings.TrimSpace(string(line)) == firmwareID {
					return nil
				}
				line = line[:0]
				continue
			}
			if len(line) < 128 {
				line = append(line, value)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("Arduino on %s did not answer %s; upload the V2 keyboard sketch", b.port, firmwareID)
}
