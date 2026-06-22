# Aion 2 Buddy — Arduino keyboard bridge

Turns a **Leonardo / Micro / Pro Micro** (ATmega32u4) into a USB keyboard that
the app drives over serial. Used by the **Auto Hotkey** menu to physically press
your combo keys when a trigger skill is detected.

## Upload

1. Open `aion2_keyboard/aion2_keyboard.ino` in the Arduino IDE.
2. Select **Tools → Board → Arduino Leonardo** (or your 32u4 board) and the
   correct **Port**.
3. Upload. The board now enumerates as a keyboard.

> The `Keyboard` library is built into the Arduino IDE — no extra install.

## Protocol

- **115200 baud**, one **key token per line** (`\n`).
- The board *taps* (press + release) the key for each line. The PC app handles
  all timing/delays between keys.

Tokens (case-insensitive):

| Token | Key |
|-------|-----|
| `1`, `q`, `e` … | that character |
| `space` | Spacebar |
| `enter` / `return` | Return |
| `tab`, `esc`, `backspace`, `delete` | as named |
| `up` `down` `left` `right` | arrows |
| `f1` … `f12` | function keys |
| `ctrl` `shift` `alt` | modifier alone |
| `ctrl+1`, `shift+q` | hold modifier, tap key |

## Test

In the Arduino **Serial Monitor**, set line ending to **Newline**, type `1`,
press Enter — it types `1` into whatever window is focused.
