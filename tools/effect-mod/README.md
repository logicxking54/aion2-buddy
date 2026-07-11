# Disable Skill Effects — mod pipeline

Re-cook the override pak that turns off every class's skill-cast VFX. **A game patch
can rewrite the cooked FX assets** (build 84 rewrote 20 Fighter/GodStone ones), so an
override pak carrying the previous build's assets would mask the new ones — each new
build needs its own pak. Never map a new build to an old pak.

Since the manifest change, a game patch needs **no app release**: re-cook, upload the
zip, add one entry to `skilleffect-manifest.json`, re-upload the manifest. Done.

## Toolchain

| Thing | Where |
|---|---|
| retoc | `F:\retoc_cli-x86_64-pc-windows-msvc\retoc.exe` (+ `oo2core_9_win64.dll` beside it) |
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

1. Upload the zip → get its URL.
2. Add an entry to `skilleffect-manifest.json` (newest build first) with the URL, the
   zip's `sha256`, and its size in MB. Keep the previous build's entry — users who
   haven't patched yet still need it.
3. Upload the manifest to `https://static.logicxking.com/skilleffect-manifest.json`.

The app reads that manifest at runtime and picks the pak matching the player's build.
A build with no entry is reported as unsupported — it never falls back to another
build's pak.

### The baked-in fallback

`internal/gamemod/skilleffect_windows.go` also carries `fallbackSkillEffectVersion` /
`URL` / `SHA256` — used only when the manifest host is unreachable. **All three must
describe the same pak.** They only need bumping if you want offline users to get a
newer build; the manifest is what normally drives everything.
