# Arduino keyboard bridge

The rotation optimizer uses an Arduino Leonardo-class ATmega32u4 board as a
real USB HID keyboard. This avoids game-side rejection of Windows `SendInput`.

## Supported board

The current machine uses an Arduino Leonardo ETH on `COM7` with FQBN
`arduino:avr:leonardoeth`. Leonardo, Micro, and compatible Pro Micro boards also
work when the correct FQBN is selected.

## Upload

Open `aion2_keyboard/aion2_keyboard.ino` in Arduino IDE, select the board and
port, then upload it. The built-in `Keyboard` library is the only dependency.

The optimizer requires the V2 handshake and will fail clearly when an old
tap-only sketch is installed.

## Protocol

The serial port runs at 115200 baud with one command per line:

| Command | Action |
|---|---|
| `ping` | Replies with `AION2_KEYBOARD_V2` |
| `down:r` | Holds R through USB HID |
| `keepalive` | Keeps the held-key watchdog alive |
| `up:r` | Releases R |
| `releaseall` | Releases every key |
| `r` | Backward-compatible 20 ms tap |

If keepalives stop for two seconds, the firmware releases all held keys. This
prevents a stuck key if the app or serial connection exits unexpectedly.
