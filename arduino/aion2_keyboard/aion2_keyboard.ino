/*
  Aion 2 Buddy — Arduino keyboard bridge
  Board: Leonardo / Micro / Pro Micro (any ATmega32u4 — native USB HID)

  Acts as a USB keyboard. The PC app opens the board's COM port and sends one
  key token per line; the board "taps" (press + release) that key. The app
  handles all timing/delays between keys, so the board stays simple.

  Serial: 115200 baud, newline ('\n') terminated.

  Token formats (case-insensitive):
    1            tap the '1' key
    q            tap the 'q' key
    space        spacebar
    enter        Return
    tab esc backspace delete
    up down left right
    f1 .. f12
    ctrl shift alt          (tap a modifier alone)
    ctrl+1  shift+q  alt+e  (hold modifier, tap key)

  Examples the app might send:
    "1\n"      -> presses 1
    "shift+2\n"-> presses Shift+2

  Test from the Arduino Serial Monitor: set line ending to "Newline", type
  "1" and Enter — it should type 1 wherever your cursor is. (Be careful: it
  types into the focused window.)
*/

#include <Keyboard.h>

const unsigned long BAUD = 115200;
const unsigned int TAP_HOLD_MS = 20; // how long a key is held during a tap

String buf;

void setup() {
  Serial.begin(BAUD);
  Keyboard.begin();
}

void loop() {
  while (Serial.available() > 0) {
    char c = (char)Serial.read();
    if (c == '\n' || c == '\r') {
      if (buf.length() > 0) {
        handleLine(buf);
        buf = "";
      }
    } else {
      buf += c;
      if (buf.length() > 32) buf = ""; // overflow guard
    }
  }
}

// Map a named token (already lowercased) to a Keyboard key code, or 0 if none.
uint8_t namedKey(const String& s) {
  if (s == "space") return ' ';
  if (s == "enter" || s == "return") return KEY_RETURN;
  if (s == "tab") return KEY_TAB;
  if (s == "esc" || s == "escape") return KEY_ESC;
  if (s == "backspace") return KEY_BACKSPACE;
  if (s == "delete" || s == "del") return KEY_DELETE;
  if (s == "up") return KEY_UP_ARROW;
  if (s == "down") return KEY_DOWN_ARROW;
  if (s == "left") return KEY_LEFT_ARROW;
  if (s == "right") return KEY_RIGHT_ARROW;
  if (s == "shift") return KEY_LEFT_SHIFT;
  if (s == "ctrl" || s == "control") return KEY_LEFT_CTRL;
  if (s == "alt") return KEY_LEFT_ALT;
  if (s.length() >= 2 && s[0] == 'f') {
    int n = s.substring(1).toInt();
    if (n >= 1 && n <= 12) return KEY_F1 + (n - 1);
  }
  return 0;
}

// Resolve a token (single char or named) to a key code.
uint8_t resolveKey(String s) {
  s.trim();
  s.toLowerCase();
  if (s.length() == 0) return 0;
  if (s.length() == 1) return (uint8_t)s[0]; // printable character
  return namedKey(s);
}

void tapKey(uint8_t k) {
  if (k == 0) return;
  Keyboard.press(k);
  delay(TAP_HOLD_MS);
  Keyboard.release(k);
}

void handleLine(String line) {
  line.trim();
  if (line.length() == 0) return;

  // Optional modifier prefix, e.g. "ctrl+1".
  uint8_t mod = 0;
  String keyTok = line;
  int plus = line.indexOf('+');
  if (plus > 0) {
    String modTok = line.substring(0, plus);
    keyTok = line.substring(plus + 1);
    modTok.trim();
    modTok.toLowerCase();
    if (modTok == "ctrl" || modTok == "control") mod = KEY_LEFT_CTRL;
    else if (modTok == "shift") mod = KEY_LEFT_SHIFT;
    else if (modTok == "alt") mod = KEY_LEFT_ALT;
  }

  uint8_t k = resolveKey(keyTok);
  if (k == 0) return;

  if (mod) Keyboard.press(mod);
  tapKey(k);
  if (mod) Keyboard.release(mod);
}
