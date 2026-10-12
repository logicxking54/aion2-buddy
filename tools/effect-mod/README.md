# Disable Skill Effects — mod pipeline

Re-cook the override pak that turns off every class's skill-cast VFX. **A game patch
can rewrite the cooked FX assets** (build 84 rewrote 20 Fighter/GodStone ones), so an
override pak carrying the previous build's assets would mask the new ones — each new
build needs its own pak. Never map a new build to an old pak.

Since the manifest change, a game patch needs **no app release**: re-cook, upload the
zip, add one entry to `skilleffect-manifest.json`, re-upload the manifest. Done.

## Selective local experiments

### Build 122 Sorcerer timing trial (local only)

`C:\aion_fx\cast-window-v122-20261010\endure-test-v1` contains the separate
`pakchunk99998-Windows_1_P` trio and `manage-experiment.ps1` (`Status`, `Install`,
`Disable`). This is NOT an effect-disabling mod. It reduces one `EndureEnd`
activation time by 10% in six Sorcerer Design assets for five skills. Input-unlock
semantics and gameplay improvement remain unverified; do not claim 10% faster casts.
The probe, per-asset reports, readback evidence, and install receipts are in that
scratch tree. Original archives and the selected Speeder macro are not replaced.

On a game update, close Aion2 and run the helper with `-Action Disable` before
testing the new build. It moves only the hash-verified experimental trio out of
Paks. Its build check prevents a new installation on another build, but cannot
automatically remove an already-installed trial. Never carry this trial forward,
combine it into the default effect mod, or publish it through the manifest without
fresh review and explicit authorization.

### Selective effect audit

The existing default disabler remains unchanged. For a separate conservative trial,
the disabler also has an audit/build mode. Do not publish an experimental selection
through the global manifest without an explicit request and in-game validation.

```powershell
# Capture package-store imports, then extract FX and Act to a NEW scratch tree.
# Retoc filters are substring matches; these may also include nested FX/Act folders.
retoc.exe -a <AES> list --path --package --store <game-paks> > <scratch>/package-store.txt
retoc.exe -a <AES> to-legacy --filter FX/ --no-shaders --no-script-objects --version UE5_3 <game-paks> <scratch>/original
retoc.exe -a <AES> to-legacy --filter Act/ --no-shaders --no-script-objects --version UE5_3 <game-paks> <scratch>/original

dotnet build tools/effect-mod/disabler -c Release
disabler.exe --audit <scratch>/original <scratch>/package-store.txt <scratch>/selection.json
disabler.exe --selective <usmap> <scratch>/selection.json <scratch>/modified <scratch>/build-report.json

# Unit checks; optional arguments additionally test real extracted Niagara fixtures.
dotnet run --project tools/effect-mod/disabler-tests -c Release
dotnet run --project tools/effect-mod/disabler-tests -c Release -- <scratch>/original <usmap>
```

Run the two audits (and the builds that follow) **one after another, never two at
once on the same tree**: the disabler opens assets without share-read, so a second
concurrent reader fails hundreds of headers with "being used by another process" and
the audit then approves nothing. Extract from a hard-linked view of the official
archives only (no `pakchunk9999*` overrides) so an installed trial pak cannot leak
into the baseline. The bash/python versions of the batch runner and cook preparation
used for builds 112–122 live beside each scratch tree (`build-root-only.sh`,
`prepare_cook.py`); the PowerShell originals need PowerShell 7, which this machine
does not have.

The audit considers a narrow player/cosmetic path allowlist and rejects transitive
references from protected boss, NPC, map, environment, healer, status, and shared
module roots. It combines package-store hard imports with `/Game` paths in extracted
name tables. This is NOT a complete dynamic-reference graph or a proof that every
boss mechanic stays visible. Never blanket-disable a folder merely because its name
sounds decorative. Cleric, Chanter, Templar, shared status cues, and environment
particles are intentionally held out of this initial policy.
Both extracted `.uasset` and `.umap` headers are scanned; map soft references must
not silently disappear merely because maps use a different extension.

The selective builder verifies baseline hashes, rejects all RawExport assets and
unsupported bulk sidecars, requires an exact unchanged serialization roundtrip,
then disables emitter handles and reopens the output to verify every handle is off.
Skipped assets have explicit reasons in the build report; `errors=0` in the legacy
tool is NOT evidence of full VFX coverage. Candidate output and roundtrip scratch
must be new directories separate from the input. Only the `modified` tree, never
the `modified-roundtrip` tree, is eligible for cooking.
The JSON report must also remain outside the source, output, and roundtrip trees.

Before installing, compare the cooked directory index with exactly the report's
`written` packages, convert it back and recheck the disabled handles. Verify the
installed game build has not changed, keep the existing override as a complete
three-file backup, and close Aion2 before replacing any live pak files. Do not
reuse a trial after a game update or claim FPS improvement without measurement.
For cooked-pak readback, use a separate read-only view of the matching official
archives plus copies of the three test pak files. Global files alone can leave
external imports unresolved as `UnknownPackage`/`UnknownExport`, even if extraction
succeeds. Compare complete resolved name/import tables and export payloads, not
just enable flags. Do not ship the dependency archives. Check extraction totals:
retoc can return exit code zero even when all asset conversions failed.

### Player-only FX actions

When a particle is shared with NPCs, do not modify that shared particle. A separate
mode can gate visual task links in exclusively player-called `FX/Act/Player` assets:

```powershell
disabler.exe --audit-actions <scratch>/original <scratch>/package-store.txt <scratch>/action-selection.json
disabler.exe --player-actions <usmap> <scratch>/action-selection.json <scratch>/actions <scratch>/action-report.json
```

This mode retains external package callers and common-directory support-class roots
in the reference graph. Unknown callers and protected transitive references block
selection. Only a single `AionAct` root explicitly marked `Fx` is accepted. Only
local `AionActTask_Particle`/`AionActTask_Streak` links with an explicit `bEnabled`
are disabled; other task types, timings, and references remain unchanged.

The root-only reader uses UAssetAPI `SkipParsingExports` and
`SkipPreloadDependencyLoading`, then parses the one FX root. All ancillary exports
remain opaque. Full task parsing caused fatal `FSoftObjectPath` stack overflows on
build 110; the two known Gladiator Skill032 inputs remain quarantined. Do not retry
them or depend on `catch(Exception)` to recover a native stack overflow. Use bounded
child processes and stop on incomplete reports. A child-only heap limit controls
memory use but is not a stack-overflow fix.

Both unchanged serialization and restoration of only the edited flags must reproduce
the exact original `.uasset`/`.uexp` pair. Cook only completed `written` outputs
confirmed against the reviewed audit, never incomplete batches or check directories.
Opaque export readback avoids unsupported schemas during final validation, but must
also compare resolved names and imports. These checks do not replace an in-game
boss-mechanics visibility test.

## Toolchain

| Thing | Where |
|---|---|
| retoc | `G:\tools\retoc-0.1.5\bin\retoc.exe` (+ `oo2core_9_win64.dll` beside it) |
| usmap | `D:\Users\Singh\Downloads\AION2-5.3.2.0-1.0.0.0.usmap` (UE5_3) |
| AES | `0x06038EF544B6007614F8574F1B7C2A3F0D565F74CDCC1B366B4EA1A17B97CBFF` |
| disabler | `tools/effect-mod/disabler` (net8.0 + UAssetAPI 1.1.0) |
| game Paks | `C:\AION2_TW\Aion2\Content\Paks` |
| build number | `<Version>` in `C:\AION2_TW\VersionInfo_A2_TW_L_GA_PURPLE.xml` |

Scratch on **C:\** (D:\ is nearly full). Budget ~1.5 GB. Close the game first — a running
`Aion2.exe` mmap-locks the `.ucas`.

## Steps (NN = new build number)

```bash
# 1. Extract player FX  (~13 s → 11383 assets, ~772 MB)
retoc.exe -a <AES> to-legacy --filter FX/Particle/PC --version UE5_3 \
  "C:/AION2_TW/Aion2/Content/Paks" C:/aion_fx/vNN_mod

# 2. Disable every Niagara emitter  (~12 min — run in BACKGROUND, a foreground
#    Bash call dies at the 10-min cap).  Expect: edited=4663 emitters=9051 errors=0
disabler.exe <usmap> C:/aion_fx/vNN_mod

# 3. Did the FX actually change vs the last build?  (decides re-cook vs reuse)
(cd v<PREV>_mod && find . -type f -name 'P_*' | sort | xargs sha256sum) > /tmp/prev.txt
(cd vNN_mod    && find . -type f -name 'P_*' | sort | xargs sha256sum) > /tmp/new.txt
diff /tmp/prev.txt /tmp/new.txt | grep '^>' | awk '{print $3}'   # empty ⇒ identical

# 4. Slim to P_ only  (5640 assets / 11280 files / 324 MB; MI_*/textures are dead
#    weight once the systems are disabled — cuts the pak 739 MB → 301 MB)
cd vNN_mod && find AION2 -type f -name 'P_*' | tar -cf - -T - | tar -xf - -C ../vNN_slim

# 5. Cook  (~1 s).  Name MUST be pakchunk99999-Windows_1_P: patch index _1_P outranks
#    the base _0_P, chunk 99999 is a high sort tiebreak.  A 7-digit chunk CRASHES.
retoc.exe to-zen --version UE5_3 C:/aion_fx/vNN_slim \
  C:/aion_fx/vNN_out/pakchunk99999-Windows_1_P.utoc

# 6. Install + test: copy the 3 files (.pak/.ucas/.utoc) into the game's Paks dir,
#    restart the game, confirm cast VFX are gone for every class and it doesn't crash.

# 7. Ship: zip the 3 files FLAT (basenames must match skillEffectPakNames), sha256 it.
```

## Publishing (no app release needed)

The static host is a box we have root on. Files served at `static.logicxking.com/<name>`
live in `/root/core/static/storage/` on `167.172.74.106` (ssh key `~/.ssh/me`).

The public upload API (`POST chatnklaos-api.logicxking.com/public/upload`, field `file`)
renames every upload to a random UUID — fine for pak zips, useless for the manifest,
which must keep its exact name. So: **upload zips via the API, edit the manifest over ssh.**

1. Upload the zip (only if the FX actually changed — step 3 above tells you):
   ```bash
   curl.exe -X POST "https://chatnklaos-api.logicxking.com/public/upload" -F "file=@C:\aion_fx\skilleffect-vNN.zip"
   # response: {"url":"https://static.xname2.com/<uuid>.zip"}
   # xname2.com is an expired domain for the same storage — rewrite it to logicxking.com
   ```
2. Add an entry to `skilleffect-manifest.json` (newest build first): the rewritten URL,
   the zip's sha256, size in MB. **Keep older builds** — players who haven't patched
   still need their match. If the FX were unchanged, point the new build at the existing
   zip rather than cooking a new one.
3. Publish it:
   ```bash
   scp -i ~/.ssh/me tools/effect-mod/skilleffect-manifest.json \
     root@167.172.74.106:/root/core/static/storage/skilleffect-manifest.json
   curl -s https://static.logicxking.com/skilleffect-manifest.json   # verify
   ```

The app reads that manifest at runtime and picks the pak matching the player's build,
so a new build reaches everyone with no app update. A build listed in neither the
manifest nor the baked table is reported unsupported — it never falls back to another
build's pak.

### The baked-in fallback

`internal/gamemod/skilleffect_windows.go` carries `fallbackBuilds`, a build → pak table
used only when the manifest doesn't list a build (stale or host down). Add the new build
there too if you're cutting a release anyway, but it is **not** a reason to release —
publishing the manifest is what ships a build.
