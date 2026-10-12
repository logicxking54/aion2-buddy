# AGENTS.md

## Priority 1: Verified Outcomes and Accountability

The user reports that previous projects did not achieve their goals and considers
this project the final evaluation of whether to continue paying approximately
THB 7,000/month. Treat this as a requirement for accountable work, not a reason to
promise success or overstate evidence.

- Judge progress by the user's actual goal and agreed acceptance criteria, not by
  files edited, versions created, time spent, or explanations given.
- Never claim a gameplay improvement from static checks alone. Separate implemented,
  locally verified, and confirmed in-game outcomes; state remaining uncertainty.
- Preserve the measured best baseline. Make evidence-based changes and compare
  results before stacking more tuning; do not repeatedly degrade a working setup.
- Own the investigation and use available evidence before asking the user to repeat
  information or tests. When user action is necessary, state the exact next action
  and what its result will establish. Keep Thai responses short and direct.

Guidance for working in this repo. The `memory/` notes (loaded via MEMORY.md) carry the
deeper packet-protocol findings; this file is the map.

## What this is

**Aion 2 Buddy** — a Wails v2 desktop app (Go backend + Vue 3/TypeScript frontend) for the
MMO *Aion 2*. Go module: `aion2tmp`. Windows-only.

The app contains only **Ping Maker**, rendered directly from `App.vue`:

- **Ping Maker** — WinDivert pulls the game's TCP packets out of the
  stack and rewrites the combat-speed field of configured skills so casts play faster.
  **Requires admin** (kernel driver).
- Language selection, Start/Stop, caster selection, skill settings, and the capture
  log remain on this one screen. There is no menu registry or separate Settings page.

### Tier Mode — built, measured in-game, removed (2026-10)

Tier Mode read the resting combat-speed stat (0x011a) from LZ4 stat-recalc
bundles and could overwrite it. It was removed at the user's request after
one live Sorcerer session established:

- The stat readout was correct against live packets (~106% real).
- **No tiers above ~94.4**: 94.4 ≈ 106 cast pace. Combat-speed gear/buffs
  beyond ~94.4 are wasted stats — the readout's real value was gear tuning.
  (`[51.2, 70.2, 71.5, 85.0, 94.4]` breakpoints remain Korean-community
  numbers; only the region above 94.4 was user-checked.)
- **0x011a writes are a dead end**: the client does NOT change cast pace from
  the resting stat we write — it honours the per-cast speed attached to each
  ACT packet, i.e. Ping Maker's existing lever. Do not rebuild a stat-writing
  mode expecting speed gains.

Legacy `tierMode`/`tierTargetPct`/`tierThresholds` keys may exist in old
config files; the loader preserves them as inert rollback data only.
- Mod, Packet Inspector, Latency Lab, Rotation Lab, and Macro Optimizer are removed
  from the app, including their Wails bindings. Do not reintroduce them implicitly.
- `internal/gamemod` and `tools/effect-mod` remain for external/offline maintenance;
  they are not linked into the app. Existing game mods and external Speeder macros
  are independent and must not be removed as part of app cleanup. Re-cook runbook:
  `tools/effect-mod/README.md`. Keep `fx.Niagara.QualityLevel` >=1 in any external
  performance config: 0 can hide boss telegraphs.

## Build / run / test

The shell here is **PowerShell**; a Bash tool may or may not be present, so prefer
PowerShell-native commands. NSIS is not on PATH by default.

```powershell
go build ./...
go vet ./...
go test ./... -count=1
gofmt -w internal/capture/*.go *.go

Push-Location frontend
npm test                           # Ping Maker-only and config migration checks
npm run build                      # Vue typecheck + production frontend build
Pop-Location

# Full app + Windows installer
$env:PATH += ";D:\Program Files (x86)\NSIS"
wails build -nsis
#   -> build/bin/aion2-buddy.exe  and  build/bin/aion2-buddy-amd64-installer.exe
```

Go/npm caches are redirected to `F:\dev-cache` (`go env -w GOCACHE/GOMODCACHE`,
`npm config set cache`) because `D:` fills up. Set these with the tool's own config, not
environment variables — env vars set from a shell don't survive into later sessions.

Most `internal/capture` files are `//go:build windows`.

**PowerShell traps that have bitten this repo:**
- `Set-Content -Encoding utf8` writes a **BOM**. It corrupted `wails.json` (wails then
  fails with `invalid character 'ï'`) and leaked into a commit subject. Write files with
  the Write tool, or `[IO.File]::WriteAllText($p, $s, (New-Object Text.UTF8Encoding $false))`.
- Passing a multi-line commit message inline mangles embedded quotes — use `git commit -F <file>`.
- Don't wipe `$env:LOCALAPPDATA\Temp` wholesale; the harness keeps background-task output
  under `Temp\Codex`.

## Architecture

- **Go backend** exposes only `Capture` (`capture_bindings.go`) and `App` (`app.go`)
  via Wails. The capture facade wraps `internal/capture`; App handles config storage
  and game detection. Generated TS lands in `frontend/wailsjs/` (regenerated by
  `wails dev`/`build` — don't hand-edit).
- **Frontend** (`frontend/src`): `App.vue` + `captureController.ts` (single owner of
  capture state, wires engine events) + `projects/pingmaker/PingMaker.vue`.
  Persisted settings in `config.ts` → `%APPDATA%\aion2-buddy\skills-config.json`.
  Only Ping Maker settings become live state. Unknown legacy keys are preserved as
  inert data for rollback, never used to activate removed features or masking.
- **Capture engine** (`internal/capture/engine.go`, `protocol.go`): a single-threaded
  `recv → processPacket → reinject` loop. It is **in-line** — time in `processPacket` is
  added directly to packet latency (~3–6.5µs/packet, negligible vs network ping).

## Packet protocol — the critical domain knowledge

- Traffic is **asymmetric**: **server→client is PLAINTEXT** (varint-framed), which is why
  casts can be read and edited. **Client→server is ENCRYPTED** — don't try to decode it.
  Only its size and timing are observable, and see the warning below about how little that
  turned out to be worth.
- A cast packet: `38 <caster varint> <flag 00|01> <skill_id u32 LE> <tick> <pkt_type>
  <entity varint> <4× float32 position> <attack_speed> <marker> <trailing record>`.
  `pkt_type` 0x02 = ACT cast (carries position+speed, editable), 0x00 = self-buff,
  0x03 = effect. `attack_speed` is a varint OR a float32 holding `value/10000`.
- **Party packets are LZ4-block-compressed.** The engine decompresses with provenance,
  finds the speed in the clean stream, and writes the new bytes back into the block
  **literals**. Casts whose speed bytes are a back-reference *match* are unreachable this
  way — ~15–17% in 10-person dungeons, near 0% solo.
- **Edits MUST be same-length** so TCP sequence numbers stay valid. Never change a packet's
  length. *Do not re-attempt variable-length "reseg"* — it was built, validated offline,
  and reverted; see `memory/aion2-reseg-attempt.md`.
- **Caster filter** (engine `casterFilter`): only the selected caster's casts are edited or
  logged. Picker options come from `recordActCaster` and **expire after 90s of silence**,
  which is what makes re-entering an instance re-lock by itself (entity keys are
  re-assigned on entry, and a stale key used to hold the sticky selection forever).
  Runtime-only; never persisted.
- The user's character is **Maliwan**, not **Mali茉莉**. The latter remains in captured
  protocol fixtures as a different character, not evidence of the user's identity.
  When a character name is configured, require an exact normalized name match; do not
  substitute the first matching-skill caster or a new caster after a quiet gap. A
  manual caster selection remains available if capture missed the name record.

### Measured dead end — don't rebuild this

Identifying the local player by correlating inbound casts with our own outbound packets
**does not work**, despite an old note claiming a `len=28` packet precedes each cast. Over a
recorded session with two Sorcerers in the party, no outbound size preceded more than 59%
of any one caster's casts against a 64% chance level, and the pairing crowned the wrong
caster. `TestReplaySessionPairing` is kept as the measuring stick — run any future
auto-detect idea through it *before* touching the engine:

```powershell
$env:PAIRTEST="<session>.jsonl"; go test ./internal/capture/ -run Replay -v
```

Details and numbers: `memory/aion2-caster-autodetect.md`.

## Features present but switched off

Kept in the tree deliberately — don't "clean them up", and don't rebuild them from scratch
either.

| Flag | Where | State |
|---|---|---|
| `logUsedCasts` | `internal/capture/engine.go` | **off** — suppresses the `Used <skill>` console line for casts we don't edit. The console is meant to show only the `ACT` lines (casts the engine actually changed on your caster); `Used` buried those under every buff tick and every party member's catalogued skill. `shouldLogUsed` and `recentUsed` exist only to serve it. Flip the const to bring it back. |

## Speeder macro tuning

- Working Speeder macros live outside this repo under `F:\aion2-speeder\speeder`. Preserve
  every measured baseline and make a separate selected test file for behavioral changes.
- Before delivering a conditional priority change, trace both its true and false paths.
  A gate added ahead of a high-priority skill must have an immediate fallback for that
  skill unless the user explicitly requests waiting; otherwise a failed condition can
  silently starve the skill for the entire rotation.
- Validate contiguous `keys` numbering, forced key-release paths, UTF-8 without BOM, and
  config line 5. Keep the last known-good baseline hash unchanged and evaluate tuning
  with server-confirmed ACT/rotation logs rather than client input attempts alone.

## Conventions

- **Commits/tags/push: only when the user explicitly asks.** End commit messages with
  `Co-Authored-By: Codex Opus 4.8 <noreply@anthropic.com>`. Work happens on `develop`.
- **Release flow**: bump `wails.json` `info.productVersion` (drives both the in-app
  `__APP_VERSION__` badge and installer metadata) → add an entry at the TOP of `releases`
  in `frontend/src/components/ChangelogModal.vue` (`en` + `th` + `zh`, retained release
  history but not mounted in the Ping Maker-only UI) → `wails build -nsis`
  → commit `Release vX.Y.Z`.
  A game patch alone is **not** a reason to release — publishing the skill-effect manifest
  is what ships a new build to users.
- **i18n**: every UI string goes in `frontend/src/i18n/en.ts`, `th.ts` and `zh.ts`.
- The app covers the WinDivert packet path (`internal/capture`). Legacy mod utilities
  (`internal/gamemod`) are offline-only. Memory-reading / kernel work belongs to a
  separate project.
- Don't commit `build/bin/*.exe` or `frontend/dist` (gitignored). The `LF→CRLF` git
  warnings are harmless.
