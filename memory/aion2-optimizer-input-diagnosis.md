# Overnight optimizer input diagnosis

Last updated: 2026-08-28 (Asia/Bangkok)

## Pause state

- The `aion2-overnight-macro-tuner` heartbeat is paused.
- The latest optimizer session is stopped:
  `D:\Users\Singh\AppData\Roaming\aion2-buddy\optimizer\20260828-022913`
- No production macro, `config.txt`, or candidate was changed during the final diagnosis.
- Resume from this note before creating more candidates.

## Reproduced symptom

When Aion2 Buddy drives virtual key 82 (R) through the Arduino, the character casts one
or two skills and then appears to stall or cast intermittently. Holding the equivalent
Logitech mouse macro works normally.

The optimizer input log showed one successful Arduino R key-down, successful watchdog
keepalives about every 500 ms, and one R key-up at the end. There was no intermediate
release or foreground-focus failure. The firmware's `keepalive` command refreshes only
the board watchdog; it does not emit another keyboard HID report or repeated R events.

## Measured runs

Session baseline (`run-001-77b251d5c930-trial-01.json`):

- 40 server-confirmed ACT casts
- 0 Hellfire casts
- ACT gaps: P50 1648 ms, P95 2034 ms, max 3299 ms
- 32 idle gaps

Five-second Hellfire timeout candidate (`run-002-d049cf7f9a4c-trial-01.json`):

- Changed only both Hellfire `wfa` timeouts from `1500T` to `5000T`
- 41 server-confirmed ACT casts
- 0 Hellfire casts
- ACT gaps: P50 1605 ms, P95 1966.7 ms, max 3300 ms
- 33 idle gaps

Conclusion: the intermittent behavior and missing Hellfire are not explained by the
1500 ms Hellfire timeout alone. Do not continue producing timeout-only variants.

## Strongest current hypothesis

The automation uses the wrong trigger lifecycle for this hardware/input path:

- Current runtime conversion forces trigger section `[82]` to `repeat=1`.
- `runTrial` sends one persistent Arduino key-down and relies on Speeder observing the
  held state for the full run.
- The Logitech macro likely emits a different key-event pattern, which explains why it
  works while the persistent Arduino hold does not.
- Speeder's current official examples define `repeat=1` as active only while the trigger
  key is held and `repeat=2` as press once to toggle on/off.

The code already contains unused `startToggle` and `stopToggle` helpers, strongly
indicating that toggle operation was anticipated but the trial path was later wired to
held-key operation.

## Exact next implementation

1. Replace the runtime-only `optimizerHeldMacro` conversion with a toggle conversion
   that changes only `[triggerKey]` to `repeat=2`; preserve all other bytes/sections.
2. In `runTrial`, tap R once with `startToggle` before waiting for the first ACT, and tap
   R once with `stopToggle` after the measured window.
3. Remove held-key watchdog calls from the measurement loop. Keep `releaseKey` as an
   emergency cleanup path.
4. On every error/cancel path, call `stopToggle` before restoring the original macro.
5. Update messages from "held/released" to "toggled on/off" and update unit tests.
6. Run `go test ./internal/capture -count=1` and `go test ./... -count=1`.
7. Build and overwrite `build\bin\aion2-buddy.exe`, then perform one short validation
   run before restarting overnight optimization.

Important: runtime candidates may use `repeat=2`, but the original active macro and
`config.txt` must remain unchanged and be restored after every run.

## Secondary validation still needed

Confirm that Speeder reloads the rewritten macro file before each trial. The optimizer
currently waits 750 ms after writing it, but config line 6 is blank. If candidate hashes
continue producing identical behavior after the toggle fix, verify reload semantics
before changing rotation logic; otherwise the optimizer may be measuring the same macro
for every candidate.

Official reference used for repeat behavior:
https://speedercheats.vip/aion-2-speeder-guide/example-macros/
