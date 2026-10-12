/*
  Aion 2 Buddy Arduino keyboard bridge V2
  Board: Leonardo / Leonardo ETH / Micro / Pro Micro (ATmega32u4)

  Serial protocol: 115200 baud, one command per line.

    ping          identify this firmware
    down:r        hold R
    keepalive     refresh the hold watchdog
    up:r          release R
    releaseall    release every held key
    r             backward-compatible 20 ms tap

  The watchdog releases every key if the host disappears while a key is held.
*/

#include <Keyboard.h>

const unsigned long BAUD = 115200;
const unsigned int TAP_HOLD_MS = 20;
const unsigned long HOLD_WATCHDOG_MS = 2000;
const uint8_t MAX_HELD_KEYS = 8;

String inputBuffer;
uint8_t heldKeys[MAX_HELD_KEYS];
uint8_t heldCount = 0;
unsigned long lastHostContact = 0;

void setup() {
  Serial.begin(BAUD);
  Keyboard.begin();
  Keyboard.releaseAll();
  lastHostContact = millis();
}

void loop() {
  while (Serial.available() > 0) {
    char value = (char)Serial.read();
    if (value == '\n' || value == '\r') {
      if (inputBuffer.length() > 0) {
        lastHostContact = millis();
        handleLine(inputBuffer);
        inputBuffer = "";
      }
    } else {
      inputBuffer += value;
      if (inputBuffer.length() > 48) inputBuffer = "";
    }
  }

  if (heldCount > 0 && millis() - lastHostContact > HOLD_WATCHDOG_MS) {
    releaseEveryKey();
  }
}

uint8_t namedKey(const String& token) {
  if (token == "space") return ' ';
  if (token == "enter" || token == "return") return KEY_RETURN;
  if (token == "tab") return KEY_TAB;
  if (token == "esc" || token == "escape") return KEY_ESC;
  if (token == "backspace") return KEY_BACKSPACE;
  if (token == "delete" || token == "del") return KEY_DELETE;
  if (token == "up") return KEY_UP_ARROW;
  if (token == "down") return KEY_DOWN_ARROW;
  if (token == "left") return KEY_LEFT_ARROW;
  if (token == "right") return KEY_RIGHT_ARROW;
  if (token == "shift") return KEY_LEFT_SHIFT;
  if (token == "ctrl" || token == "control") return KEY_LEFT_CTRL;
  if (token == "alt") return KEY_LEFT_ALT;
  if (token.length() >= 2 && token[0] == 'f') {
    int number = token.substring(1).toInt();
    if (number >= 1 && number <= 12) return KEY_F1 + (number - 1);
  }
  return 0;
}

uint8_t resolveKey(String token) {
  token.trim();
  token.toLowerCase();
  if (token.length() == 0) return 0;
  if (token.length() == 1) return (uint8_t)token[0];
  return namedKey(token);
}

void rememberHeld(uint8_t key) {
  for (uint8_t index = 0; index < heldCount; index++) {
    if (heldKeys[index] == key) return;
  }
  if (heldCount < MAX_HELD_KEYS) heldKeys[heldCount++] = key;
}

void forgetHeld(uint8_t key) {
  for (uint8_t index = 0; index < heldCount; index++) {
    if (heldKeys[index] != key) continue;
    for (uint8_t next = index + 1; next < heldCount; next++) {
      heldKeys[next - 1] = heldKeys[next];
    }
    heldCount--;
    return;
  }
}

void releaseEveryKey() {
  Keyboard.releaseAll();
  heldCount = 0;
}

void tapKey(uint8_t key) {
  if (key == 0) return;
  Keyboard.press(key);
  delay(TAP_HOLD_MS);
  Keyboard.release(key);
}

void handleTap(String token) {
  uint8_t modifier = 0;
  String keyToken = token;
  int plus = token.indexOf('+');
  if (plus > 0) {
    String modifierToken = token.substring(0, plus);
    keyToken = token.substring(plus + 1);
    modifierToken.trim();
    modifierToken.toLowerCase();
    if (modifierToken == "ctrl" || modifierToken == "control") modifier = KEY_LEFT_CTRL;
    else if (modifierToken == "shift") modifier = KEY_LEFT_SHIFT;
    else if (modifierToken == "alt") modifier = KEY_LEFT_ALT;
  }

  uint8_t key = resolveKey(keyToken);
  if (key == 0) return;
  if (modifier != 0) Keyboard.press(modifier);
  tapKey(key);
  if (modifier != 0) Keyboard.release(modifier);
}

void handleLine(String line) {
  line.trim();
  line.toLowerCase();
  if (line.length() == 0) return;

  if (line == "ping") {
    Serial.println("AION2_KEYBOARD_V2");
    return;
  }
  if (line == "keepalive") return;
  if (line == "releaseall") {
    releaseEveryKey();
    return;
  }
  if (line.startsWith("down:")) {
    uint8_t key = resolveKey(line.substring(5));
    if (key != 0) {
      Keyboard.press(key);
      rememberHeld(key);
    }
    return;
  }
  if (line.startsWith("up:")) {
    uint8_t key = resolveKey(line.substring(3));
    if (key != 0) {
      Keyboard.release(key);
      forgetHeld(key);
    }
    return;
  }

  handleTap(line);
}
