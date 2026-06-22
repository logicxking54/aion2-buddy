<script lang="ts" setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { main, memread } from '../../../wailsjs/go/models'
import { EventsOn } from '../../../wailsjs/runtime/runtime'

const { t } = useI18n()

// Defensive access to the read-only Wails backend (absent in a plain browser).
interface MemReadBackend {
  Status(): Promise<memread.Status>
  SetProcessName(name: string): Promise<void>
  ProcessName(): Promise<string>
  ListAionProcesses(): Promise<memread.ProcInfo[]>
  Detach(): Promise<void>
  ReadSpec(spec: string): Promise<main.SpecResult>
  ScanNew(vtype: string, op: string, a: number, b: number): Promise<main.ScanResult>
  ScanNext(op: string, a: number, b: number): Promise<main.ScanResult>
  ScanFloatRange(min: number, max: number): Promise<main.ScanResult>
  ScanList(limit: number): Promise<memread.ScanHit[]>
  ScanMode(): Promise<string>
  ScanReset(): Promise<void>
  ScanRemove(addr: string): Promise<number>
  ReadTypedAt(addr: string, vtype: string): Promise<number>
  ReadBlock(addr: string, rows: number): Promise<main.BlockResult>
}
function backend(): MemReadBackend | undefined {
  return (window as any)?.go?.main?.MemRead
}
const available = !!backend()

// ---------------------------------------------------------------------------
// Attach status + diagnostics
// ---------------------------------------------------------------------------
const status = reactive({ running: false, attached: false, pid: 0, error: '' })
const attached = computed(() => status.attached)
const procName = ref('')
const aionProcs = ref<memread.ProcInfo[]>([])
const showDiag = ref(false)
async function diagnose() {
  const b = backend()
  if (!b) return
  showDiag.value = true
  aionProcs.value = (await b.ListAionProcesses()) ?? []
  procName.value = await b.ProcessName()
}
async function applyProcName(name: string) {
  const b = backend()
  if (!b) return
  await b.SetProcessName(name)
  procName.value = await b.ProcessName()
}

// ---------------------------------------------------------------------------
// Shared "locked" address — live readout of a confirmed candidate
// ---------------------------------------------------------------------------
const locked = ref<{ address: string; vtype: string; value: number } | null>(null)
function lockAt(addr: number, vtype: string) {
  locked.value = { address: '0x' + addr.toString(16).toUpperCase(), vtype, value: NaN }
}

// ---------------------------------------------------------------------------
// Cheat-Engine-style scanner
// ---------------------------------------------------------------------------
const TYPES = [
  { v: 'int32', label: '4 Bytes (int)' },
  { v: 'float', label: 'Float' },
  { v: 'int64', label: '8 Bytes (int)' },
  { v: 'double', label: 'Double' },
  { v: 'int16', label: '2 Bytes (int)' },
  { v: 'byte', label: 'Byte' },
  { v: 'uint32', label: '4 Bytes (unsigned)' },
]
const NEXT_OPS = [
  { v: 'dec', label: 'Decreased value' },
  { v: 'inc', label: 'Increased value' },
  { v: 'unchanged', label: 'Unchanged value' },
  { v: 'changed', label: 'Changed value' },
  { v: 'decby', label: 'Decreased value by …', arg: true },
  { v: 'incby', label: 'Increased value by …', arg: true },
  { v: 'exact', label: 'Exact value …', arg: true },
  { v: 'between', label: 'Value between …', arg: true, arg2: true },
  { v: 'bigger', label: 'Bigger than …', arg: true },
  { v: 'smaller', label: 'Smaller than …', arg: true },
]

const ceType = ref('int32')
const ceFirstMode = ref<'unknown' | 'exact'>('unknown')
const ceExact = ref(0)
const ceNextOp = ref('dec')
const ceArg = ref(0)
const ceArg2 = ref(0)
const ceActive = ref(false) // a scan is in progress (first scan done)
const ceMode = ref('') // 'snapshot' | 'list' | ''
const ceCount = ref(0)
const ceTrunc = ref(false)
const ceNote = ref('')
const ceHits = ref<Hit[]>([])
const ceBusy = ref(false)

const curOp = computed(() => NEXT_OPS.find((o) => o.v === ceNextOp.value))
const LIST_LIMIT = 200
const ceLive = ref(true) // live-refresh the displayed candidate values

// enrich tags each hit with the direction its value moved since the last read,
// so the list can colour values rising/falling during live analysis.
type Hit = memread.ScanHit & { dir: 'up' | 'down' | '' }
const prevVals = new Map<number, number>()
function enrich(list: memread.ScanHit[]): Hit[] {
  return list.map((h) => {
    const prev = prevVals.get(h.address)
    let dir: 'up' | 'down' | '' = ''
    if (prev !== undefined && prev !== h.value) dir = h.value > prev ? 'up' : 'down'
    prevVals.set(h.address, h.value)
    return { ...h, dir }
  })
}

async function refreshList(b: MemReadBackend) {
  if (ceMode.value === 'list' && ceCount.value > 0 && ceCount.value <= LIST_LIMIT) {
    prevVals.clear()
    ceHits.value = enrich(await b.ScanList(LIST_LIMIT))
  } else {
    ceHits.value = []
  }
}

async function ceFirstScan() {
  const b = backend()
  if (!b || ceBusy.value) return
  ceBusy.value = true
  ceNote.value = ceFirstMode.value === 'unknown' ? 'Snapshotting memory…' : 'Scanning…'
  try {
    const op = ceFirstMode.value === 'unknown' ? 'unknown' : 'exact'
    const r = await b.ScanNew(ceType.value, op, ceExact.value, 0)
    if (r.error) {
      ceNote.value = r.error
      return
    }
    ceActive.value = true
    ceCount.value = r.count
    ceTrunc.value = !!r.truncated
    ceMode.value = await b.ScanMode()
    ceNote.value =
      ceFirstMode.value === 'unknown'
        ? `Snapshot taken (${r.count.toLocaleString()} values). Now change the value in game, pick a scan type, and click Next Scan.`
        : `${r.count.toLocaleString()} matches. Change the value in game, then Next Scan.`
    await refreshList(b)
  } catch (e: any) {
    ceNote.value = String(e?.message ?? e)
  } finally {
    ceBusy.value = false
  }
}

async function ceNextScan() {
  const b = backend()
  if (!b || ceBusy.value || !ceActive.value) return
  ceBusy.value = true
  try {
    const r = await b.ScanNext(ceNextOp.value, ceArg.value, ceArg2.value)
    if (r.error) {
      ceNote.value = r.error
      return
    }
    ceCount.value = r.count
    ceMode.value = await b.ScanMode()
    ceNote.value = `${r.count.toLocaleString()} matches left.`
    await refreshList(b)
  } catch (e: any) {
    ceNote.value = String(e?.message ?? e)
  } finally {
    ceBusy.value = false
  }
}

// removeCandidate prunes one address from the backend scan set so it won't
// reappear on the next live refresh or Next Scan. Works for either list.
async function removeCandidate(addr: number) {
  const b = backend()
  if (!b) return
  const hex = '0x' + addr.toString(16).toUpperCase()
  const n = await b.ScanRemove(hex)
  prevVals.delete(addr)
  ceHits.value = ceHits.value.filter((h) => h.address !== addr)
  alHits.value = alHits.value.filter((h) => h.address !== addr)
  if (ceActive.value) ceCount.value = n
  else alCount.value = n
}

function ceReset() {
  ceActive.value = false
  ceCount.value = 0
  ceMode.value = ''
  ceTrunc.value = false
  ceNote.value = ''
  ceHits.value = []
  prevVals.clear()
  backend()?.ScanReset()
}

// ---------------------------------------------------------------------------
// Cast-correlated auto-locator (one-click shortcut for a float cooldown)
// ---------------------------------------------------------------------------
type Stage = 'idle' | 'armed' | 'narrowing'
const stage = ref<Stage>('idle')
const alNote = ref('')
const alCount = ref(0)
const alHits = ref<Hit[]>([])
const alBusy = ref(false)
const delay = (ms: number) => new Promise((r) => setTimeout(r, ms))

// The cooldown is stored as ELAPSED-since-cast (float, counts UP, resets to ~0
// on cast). So: snapshot before the cast, then on cast keep values that
// DECREASED (the reset big→0), then keep values that INCREASE (counting up).
function alArm() {
  const b = backend()
  if (!b) return
  stage.value = 'armed'
  alCount.value = 0
  alHits.value = []
  alBusy.value = true
  alNote.value = 'Snapshotting memory…'
  b.ScanNew('float', 'unknown', 0, 0)
    .then((r) => {
      alNote.value = `Armed (${(r.count || 0).toLocaleString()} floats). Now CAST the skill ONCE.`
    })
    .finally(() => {
      alBusy.value = false
    })
}
async function onCast(id: number) {
  const b = backend()
  if (!b) return
  if (stage.value === 'armed') {
    stage.value = 'narrowing'
    alBusy.value = true
    alNote.value = `Cast detected (id ${id}). Locating the timer…`
    try {
      await b.ScanNext('dec', 0, 0) // reset: value was large, dropped to ~0
      await b.ScanNext('between', 0, 5) // ...and is now near zero — strong filter
      await delay(1500)
      await b.ScanNext('inc', 0, 0) // now counting up
      await delay(1500)
      let r = await b.ScanNext('inc', 0, 0)
      // Auto-trim a couple more times if it's still crowded (steady up-counters).
      for (let k = 0; k < 2 && r.count > 40; k++) {
        await delay(1500)
        r = await b.ScanNext('inc', 0, 0)
      }
      alCount.value = r.count
      prevVals.clear()
      alHits.value = r.count > 0 && r.count <= 40 ? enrich(await b.ScanList(40)) : []
      alNote.value =
        r.count <= 40
          ? `${r.count} candidates — pick the one ≈ seconds since you cast.`
          : `${r.count} left — click “↑ still counting” a few more times.`
    } finally {
      alBusy.value = false
    }
  } else if (stage.value === 'narrowing') {
    await alNarrow('dec', 'reset on re-cast')
  }
}
async function alNarrow(op: 'dec' | 'inc', label: string) {
  const b = backend()
  if (!b || alBusy.value) return
  alBusy.value = true
  try {
    const r = await b.ScanNext(op, 0, 0)
    alCount.value = r.count
    alNote.value = `${r.count} left (${label}).`
    prevVals.clear()
    alHits.value = r.count > 0 && r.count <= 40 ? enrich(await b.ScanList(40)) : []
  } finally {
    alBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// Memory view (dissect the struct around an address, live)
// ---------------------------------------------------------------------------
type MvRow = memread.MemRow & { chg: boolean }
// Master on/off for the whole Memory view. Off by default (no polling, table
// unmounted) to save CPU; the choice is remembered across launches.
const mvEnabled = ref(localStorage.getItem('aion2.memprobe.enabled') === '1')
watch(mvEnabled, (v) => localStorage.setItem('aion2.memprobe.enabled', v ? '1' : '0'))
const mvAddr = ref('0x7A66B270')
const mvRows = ref(48)
const mvLive = ref(true)
const mvData = ref<MvRow[]>([])
const mvBase = ref('')
const mvError = ref('')
const mvOnlyChanged = ref(true) // export/show only slots that moved
const MV_FULL_LIMIT = 400 // above this, the live table shows only moving slots
const mvPrev = new Map<number, number>() // offset -> last uint32, for change flags
const everChanged = new Set<number>() // offsets that changed at least once

async function mvRead() {
  const b = backend()
  if (!b || !mvAddr.value.trim()) return
  try {
    const r = await b.ReadBlock(mvAddr.value.trim(), mvRows.value || 48)
    const newBase = r.base ?? ''
    if (newBase !== mvBase.value) {
      mvPrev.clear()
      everChanged.clear() // offsets map to new addresses — old change history is moot
    }
    mvBase.value = newBase
    mvError.value = r.error ?? ''
    mvData.value = (r.rows ?? []).map((row) => {
      const prev = mvPrev.get(row.offset)
      const chg = prev !== undefined && prev !== row.uint32
      if (chg) everChanged.add(row.offset)
      mvPrev.set(row.offset, row.uint32)
      return { ...row, chg }
    })
  } catch (e: any) {
    mvError.value = String(e?.message ?? e)
  }
}

// What the table renders. For big regions with "only changed" on, show just the
// moving slots (cooldown stands out, stays responsive) — but if nothing has
// moved yet, fall back to showing all so the table is never mysteriously blank.
// Untick "only changed" to always see every slot (capped for responsiveness).
const MV_RENDER_CAP = 1200
const mvVisible = computed(() => {
  const all = mvData.value
  if (all.length > MV_FULL_LIMIT && mvOnlyChanged.value) {
    const moved = all.filter((r) => everChanged.has(r.offset))
    if (moved.length) return moved
  }
  return all.length > MV_RENDER_CAP ? all.slice(0, MV_RENDER_CAP) : all
})

function mvJump(deltaHex: number) {
  // Re-base relative to the resolved base so jumps are stable while polling.
  const cur = mvBase.value ? parseInt(mvBase.value, 16) : parseInt(mvAddr.value.replace(/^0x/i, ''), 16)
  if (!isFinite(cur)) return
  mvPrev.clear()
  everChanged.clear()
  mvAddr.value = '0x' + (cur + deltaHex).toString(16).toUpperCase()
  mvRead()
}

// Recorder: snapshot the region once per second so the time series can be
// analysed (a cooldown is the slot that steps down ~1/sec).
const mvRecording = ref(false)
const mvSamples = ref<MvRow[][]>([])
const mvCopied = ref(false)
let mvRecTimer: number | undefined

function mvToggleRecord() {
  mvRecording.value = !mvRecording.value
  if (mvRecording.value) {
    mvSamples.value = []
    everChanged.clear() // track only what moves during this recording window
    const tick = async () => {
      await mvRead()
      mvSamples.value.push(mvData.value.map((r) => ({ ...r })))
      if (mvRecording.value) mvRecTimer = window.setTimeout(tick, 1000)
    }
    tick()
  } else if (mvRecTimer) {
    window.clearTimeout(mvRecTimer)
  }
}

// classify flags an offset's int32 series as a likely countdown to help triage.
function classify(series: number[]): string {
  if (series.length < 3) return ''
  const diffs: number[] = []
  for (let i = 1; i < series.length; i++) diffs.push(series[i] - series[i - 1])
  const allNonPos = diffs.every((d) => d <= 0)
  const someNeg = diffs.some((d) => d < 0)
  if (!allNonPos || !someNeg) return ''
  const drops = diffs.filter((d) => d < 0).map((d) => -d)
  const avg = drops.reduce((a, b) => a + b, 0) / drops.length
  if (avg >= 0.5 && avg <= 2) return '⏱ sec?'
  if (avg >= 500 && avg <= 2000) return '⏱ ms?'
  return '↓'
}

function buildAnalysisText(): string {
  const s = mvSamples.value
  if (!s.length) return '(no samples)'
  const offs = s[0]
  // Only include slots that moved during the recording (unless toggled off, or
  // nothing moved at all). This keeps a big-region paste small and on-point.
  let idx = offs.map((_, i) => i)
  if (mvOnlyChanged.value) {
    const moved = idx.filter((i) => everChanged.has(offs[i].offset))
    if (moved.length) idx = moved
  }
  let out = `MemProbe region recording — base ${mvBase.value}, ${s.length} samples @ ~1s\n`
  out += `slots: ${idx.length} shown of ${offs.length} (${mvOnlyChanged.value ? 'changed only' : 'all'})\n`
  out += `format: +off addr | int32: t0,t1,... | float: t0,t1,...   (flag = countdown heuristic)\n`
  for (const i of idx) {
    const ints = s.map((smp) => smp[i]?.int32 ?? 0)
    const flts = s.map((smp) => fmtNum(smp[i]?.float32))
    const flag = classify(ints)
    out += `+${offs[i].offset.toString(16)} 0x${offs[i].address.toString(16).toUpperCase()} | i:${ints.join(',')} | f:${flts.join(',')}${flag ? ' | ' + flag : ''}\n`
  }
  return out
}

async function mvCopy() {
  const text = buildAnalysisText()
  try {
    await navigator.clipboard.writeText(text)
    mvCopied.value = true
    window.setTimeout(() => (mvCopied.value = false), 1500)
  } catch {
    // Fallback: native save dialog via the App binding.
    const app = (window as any)?.go?.main?.App
    if (app?.ExportPackets) await app.ExportPackets('memprobe-recording.txt', text)
  }
}

// ---------------------------------------------------------------------------
// Manual pointer watch (verify a raw address or CE pointer path)
// ---------------------------------------------------------------------------
interface Watch {
  id: number
  label: string
  spec: string
  result?: main.SpecResult
}
const STORAGE_KEY = 'aion2.memprobe.watches'
let nextId = 1
function loadWatches(): Watch[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) {
      const arr = JSON.parse(raw) as Watch[]
      if (Array.isArray(arr) && arr.length) {
        nextId = Math.max(...arr.map((w) => w.id)) + 1
        return arr.map((w) => ({ id: w.id, label: w.label, spec: w.spec }))
      }
    }
  } catch {
    /* ignore */
  }
  return []
}
const watches = reactive<Watch[]>(loadWatches())
function persist() {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(watches.map((w) => ({ id: w.id, label: w.label, spec: w.spec }))))
}
function addWatch() {
  watches.push({ id: nextId++, label: '', spec: 'Aion2.exe+0x' })
  persist()
}
function removeWatch(id: number) {
  const i = watches.findIndex((w) => w.id === id)
  if (i >= 0) watches.splice(i, 1)
  persist()
}
async function readWatch(w: Watch) {
  const b = backend()
  if (!b || !w.spec.trim()) return
  try {
    w.result = await b.ReadSpec(w.spec)
  } catch (e: any) {
    w.result = { ok: false, error: String(e?.message ?? e) } as main.SpecResult
  }
}

// ---------------------------------------------------------------------------
// Polling loops
// ---------------------------------------------------------------------------
let lockTimer: number | undefined
let attachTimer: number | undefined
let listTimer: number | undefined
let mvTimer: number | undefined
let offCast: (() => void) | undefined

onMounted(() => {
  const b = backend()
  if (!b) return
  offCast = EventsOn('capture:cast', (c: { id: number }) => onCast(c?.id ?? -1))

  const checkAttach = async () => {
    try {
      const s = await b.Status()
      status.running = !!s.running
      status.attached = !!s.attached
      status.pid = s.pid ?? 0
      status.error = s.error ?? ''
    } catch (e: any) {
      status.attached = false
      status.error = String(e?.message ?? e)
    }
    attachTimer = window.setTimeout(checkAttach, 1500)
  }
  checkAttach()

  const pollLock = async () => {
    if (locked.value) {
      try {
        locked.value.value = await b.ReadTypedAt(locked.value.address, locked.value.vtype)
      } catch {
        /* ignore */
      }
    }
    lockTimer = window.setTimeout(pollLock, 200)
  }
  pollLock()

  // Live-refresh whichever candidate list is on screen, so values move in
  // real time during analysis. Re-reads addresses only (cheap); skipped while
  // a scan is running so the set isn't changing under us.
  const pollList = async () => {
    try {
      if (ceLive.value && !ceBusy.value && ceHits.value.length && ceMode.value === 'list' && ceCount.value <= LIST_LIMIT) {
        ceHits.value = enrich(await b.ScanList(LIST_LIMIT))
      } else if (ceLive.value && !alBusy.value && alHits.value.length) {
        alHits.value = enrich(await b.ScanList(30))
      }
    } catch {
      /* ignore */
    }
    listTimer = window.setTimeout(pollList, 350)
  }
  pollList()

  const pollView = async () => {
    // Not gated on status.attached: ReadBlock re-attaches on demand and reports
    // read errors in-band (mvError), so this stays live and surfaces the real
    // reason if a read fails — rather than silently freezing. Gated on the
    // master On/Off (mvEnabled) so it costs nothing when turned off.
    if (mvEnabled.value && mvLive.value) await mvRead()
    mvTimer = window.setTimeout(pollView, 300)
  }
  pollView()
})

onUnmounted(() => {
  if (offCast) offCast()
  if (lockTimer) window.clearTimeout(lockTimer)
  if (attachTimer) window.clearTimeout(attachTimer)
  if (listTimer) window.clearTimeout(listTimer)
  if (mvTimer) window.clearTimeout(mvTimer)
  if (mvRecTimer) window.clearTimeout(mvRecTimer)
  backend()?.Detach()
})

function fmtNum(n: number | undefined) {
  if (n === undefined || !isFinite(n)) return '—'
  if (Number.isInteger(n)) return String(n)
  return Math.abs(n) < 1e-4 || Math.abs(n) > 1e9 ? n.toExponential(3) : n.toFixed(3)
}
</script>

<template>
  <div class="flex h-full flex-col gap-4 overflow-y-auto p-6 text-slate-200">
    <!-- Header -->
    <div class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-lg font-extrabold tracking-wide text-white">{{ t('menus.memread.label') }}</h1>
        <p class="mt-1 max-w-2xl text-xs text-slate-400">{{ t('menus.memread.description') }}</p>
      </div>
      <div
        class="flex items-center gap-2 rounded-lg px-3 py-1.5 text-xs ring-1"
        :class="attached ? 'bg-emerald-500/15 text-emerald-300 ring-emerald-400/30' : 'bg-white/5 text-slate-400 ring-white/10'"
      >
        <span class="h-2 w-2 rounded-full" :class="attached ? 'bg-emerald-400' : 'bg-slate-500'" />
        {{ attached ? `attached · pid ${status.pid}` : status.running ? 'found, not attached' : 'not attached' }}
      </div>
    </div>

    <div v-if="!available" class="rounded-lg bg-rose-500/10 px-4 py-3 text-xs text-rose-300">
      Memory backend not available — run inside the desktop app, as Administrator.
    </div>

    <!-- Attach trouble -->
    <div v-else-if="!attached" class="rounded-lg border border-amber-400/20 bg-amber-500/5 px-4 py-3 text-xs">
      <div class="flex items-center justify-between gap-3">
        <p class="text-amber-200/90">
          <b>Can't read memory yet.</b>
          <span v-if="status.error" class="text-amber-200/70"> {{ status.error }}</span>
        </p>
        <button type="button" @click="diagnose" class="shrink-0 rounded bg-white/5 px-3 py-1.5 font-semibold text-slate-200 ring-1 ring-white/10 hover:bg-white/10">Diagnose</button>
      </div>
      <ul class="mt-2 list-disc space-y-0.5 pl-5 text-[11px] text-amber-200/70">
        <li><b>Run as Administrator</b> — required for OpenProcess.</li>
        <li>Make sure the game is running (in-world, not just the launcher).</li>
        <li>If the client exe isn't <code>Aion2.exe</code>, click Diagnose and pick it.</li>
      </ul>
      <div v-if="showDiag" class="mt-3 rounded bg-ink-900/60 p-3">
        <div class="text-[10px] uppercase tracking-wide text-slate-500">
          target: <span class="text-slate-300">{{ procName || '—' }}</span> · processes matching “aion”
        </div>
        <div v-if="aionProcs.length" class="mt-1 space-y-1">
          <button v-for="p in aionProcs" :key="p.pid" type="button" @click="applyProcName(p.name)"
            class="flex w-full items-center justify-between rounded bg-ink-900 px-3 py-1.5 font-mono text-xs ring-1 ring-white/10 hover:ring-accent/40">
            <span class="text-slate-300">{{ p.name }}</span>
            <span class="text-slate-500">pid {{ p.pid }} · click to target</span>
          </button>
        </div>
        <p v-else class="mt-1 text-[11px] text-slate-500">No “aion” process found. Is the game running?</p>
        <button type="button" @click="applyProcName('')" class="mt-2 text-[11px] text-slate-500 underline hover:text-slate-300">reset to default</button>
      </div>
    </div>

    <!-- Cheat-Engine-style scanner -->
    <section class="rounded-xl border border-white/5 bg-ink-900/60 p-4">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-bold text-white">🔎 Memory scanner <span class="text-[10px] font-normal text-slate-500">(Cheat Engine style)</span></h2>
        <span class="text-[10px] uppercase tracking-wide text-slate-500">read-only</span>
      </div>

      <!-- value type + first scan -->
      <div class="mt-3 flex flex-wrap items-end gap-3">
        <label class="flex flex-col gap-1 text-[11px] text-slate-400">
          Value type
          <select v-model="ceType" :disabled="ceActive" class="rounded bg-ink-900 px-2 py-1.5 text-xs text-slate-200 ring-1 ring-white/10 disabled:opacity-50">
            <option v-for="ty in TYPES" :key="ty.v" :value="ty.v">{{ ty.label }}</option>
          </select>
        </label>

        <template v-if="!ceActive">
          <label class="flex flex-col gap-1 text-[11px] text-slate-400">
            First scan
            <select v-model="ceFirstMode" class="rounded bg-ink-900 px-2 py-1.5 text-xs text-slate-200 ring-1 ring-white/10">
              <option value="unknown">Unknown initial value</option>
              <option value="exact">Exact value</option>
            </select>
          </label>
          <label v-if="ceFirstMode === 'exact'" class="flex flex-col gap-1 text-[11px] text-slate-400">
            Value
            <input v-model.number="ceExact" type="number" step="any" class="w-28 rounded bg-ink-900 px-2 py-1.5 text-xs text-slate-200 ring-1 ring-white/10" />
          </label>
          <button type="button" :disabled="!attached || ceBusy" @click="ceFirstScan"
            class="rounded-lg bg-accent/15 px-4 py-2 text-sm font-semibold text-accent ring-1 ring-accent/30 transition hover:bg-accent/25 disabled:opacity-40">
            First Scan
          </button>
        </template>

        <!-- next scan controls -->
        <template v-else>
          <label class="flex flex-col gap-1 text-[11px] text-slate-400">
            Scan type
            <select v-model="ceNextOp" class="rounded bg-ink-900 px-2 py-1.5 text-xs text-slate-200 ring-1 ring-white/10">
              <option v-for="o in NEXT_OPS" :key="o.v" :value="o.v">{{ o.label }}</option>
            </select>
          </label>
          <label v-if="curOp?.arg" class="flex flex-col gap-1 text-[11px] text-slate-400">
            {{ curOp?.arg2 ? 'From' : 'Value' }}
            <input v-model.number="ceArg" type="number" step="any" class="w-24 rounded bg-ink-900 px-2 py-1.5 text-xs text-slate-200 ring-1 ring-white/10" />
          </label>
          <label v-if="curOp?.arg2" class="flex flex-col gap-1 text-[11px] text-slate-400">
            To
            <input v-model.number="ceArg2" type="number" step="any" class="w-24 rounded bg-ink-900 px-2 py-1.5 text-xs text-slate-200 ring-1 ring-white/10" />
          </label>
          <button type="button" :disabled="!attached || ceBusy" @click="ceNextScan"
            class="rounded-lg bg-emerald-500/15 px-4 py-2 text-sm font-semibold text-emerald-200 ring-1 ring-emerald-400/30 transition hover:bg-emerald-500/25 disabled:opacity-40">
            Next Scan
          </button>
          <button type="button" @click="ceReset" class="rounded-lg bg-white/5 px-3 py-2 text-xs font-semibold text-slate-300 ring-1 ring-white/10 hover:bg-white/10">New Scan</button>
        </template>

        <span v-if="ceActive" class="ml-auto rounded bg-white/5 px-2 py-1 font-mono text-xs text-slate-300">
          {{ ceMode === 'snapshot' ? 'unknown' : (ceTrunc ? ceCount.toLocaleString() + '+' : ceCount.toLocaleString()) }} found
        </span>
      </div>

      <p v-if="ceNote" class="mt-3 rounded bg-white/5 px-3 py-2 text-[11px] text-slate-300">{{ ceNote }}</p>
      <p class="mt-2 text-[10px] leading-relaxed text-slate-500">
        Flow: pick the type → <b>Unknown initial value</b> → First Scan. Change the value in game (e.g. cast so the cooldown starts),
        then choose <b>Decreased value</b> and click <b>Next Scan</b> repeatedly while it counts down. The list narrows to the address.
        Tip: if it counts down, “Decreased value”; if it counts up, “Increased value”.
      </p>

      <!-- candidate list -->
      <div v-if="ceHits.length" class="mt-3 space-y-1">
        <div class="flex items-center justify-between text-[10px] uppercase tracking-wide text-slate-500">
          <span>candidates — click the one matching your in-game value to lock + live-watch</span>
          <label class="flex items-center gap-1.5 normal-case text-slate-400">
            <input v-model="ceLive" type="checkbox" class="accent-emerald-400" />
            live
            <span class="h-1.5 w-1.5 rounded-full" :class="ceLive ? 'animate-pulse bg-emerald-400' : 'bg-slate-600'" />
          </label>
        </div>
        <div v-for="h in ceHits" :key="h.address" class="flex items-center gap-1">
          <button type="button" @click="lockAt(h.address, ceType)"
            class="flex flex-1 items-center justify-between rounded bg-ink-900 px-3 py-1.5 font-mono text-xs ring-1 ring-white/10 hover:ring-accent/40">
            <span class="text-slate-400">0x{{ h.address.toString(16).toUpperCase() }}</span>
            <span class="flex items-center gap-1.5">
              <span :class="h.dir === 'down' ? 'text-emerald-300' : h.dir === 'up' ? 'text-rose-300' : 'text-accent'">{{ h.display }}</span>
              <span class="w-2 text-[10px]" :class="h.dir === 'down' ? 'text-emerald-400' : 'text-rose-400'">{{ h.dir === 'down' ? '▼' : h.dir === 'up' ? '▲' : '' }}</span>
            </span>
          </button>
          <button type="button" @click="removeCandidate(h.address)" title="remove from list"
            class="shrink-0 rounded px-2 py-1.5 text-xs text-slate-500 ring-1 ring-white/10 hover:bg-rose-500/10 hover:text-rose-300">✕</button>
        </div>
      </div>
      <p v-else-if="ceActive && ceMode === 'list' && ceCount > LIST_LIMIT" class="mt-3 text-[11px] text-slate-500">
        {{ ceCount.toLocaleString() }} matches — keep narrowing (Next Scan) until ≤ {{ LIST_LIMIT }} to see the list.
      </p>
    </section>

    <!-- Locked live readout (shared) -->
    <div v-if="locked" class="flex items-center justify-between rounded-xl border border-accent/30 bg-accent/10 px-4 py-3">
      <div>
        <div class="text-[10px] uppercase tracking-wide text-accent/70">locked · live ({{ locked.vtype }})</div>
        <div class="font-mono text-xs text-slate-400">{{ locked.address }}</div>
      </div>
      <div class="flex items-center gap-3">
        <div class="font-mono text-2xl font-bold text-accent">{{ fmtNum(locked.value) }}</div>
        <button type="button" @click="locked = null" class="rounded px-2 py-1 text-xs text-slate-500 hover:text-rose-300">✕</button>
      </div>
    </div>

    <!-- Memory view (dissect) -->
    <section class="rounded-xl border border-white/5 bg-ink-900/60 p-4">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-bold text-white">🧬 Memory view <span class="text-[10px] font-normal text-slate-500">(dissect a region, live)</span></h2>
        <div class="flex items-center gap-3">
          <label v-if="mvEnabled" class="flex items-center gap-1.5 text-[11px] text-slate-400">
            <input v-model="mvLive" type="checkbox" class="accent-emerald-400" /> live
            <span class="h-1.5 w-1.5 rounded-full" :class="mvLive ? 'animate-pulse bg-emerald-400' : 'bg-slate-600'" />
          </label>
          <button type="button" @click="mvEnabled = !mvEnabled"
            class="rounded-lg px-3 py-1.5 text-xs font-bold ring-1 transition"
            :class="mvEnabled ? 'bg-emerald-500/15 text-emerald-200 ring-emerald-400/30 hover:bg-emerald-500/25' : 'bg-white/5 text-slate-400 ring-white/10 hover:bg-white/10'">
            {{ mvEnabled ? 'On' : 'Off' }}
          </button>
        </div>
      </div>
      <p v-if="!mvEnabled" class="mt-2 text-[11px] text-slate-500">
        Off to save CPU — no memory reads or table rendering. Click <b>Off → On</b> to use the live region dissector.
      </p>

      <template v-if="mvEnabled">
      <div class="mt-3 flex flex-wrap items-end gap-3">
        <label class="flex flex-col gap-1 text-[11px] text-slate-400">
          Address
          <input v-model="mvAddr" spellcheck="false" placeholder="0x7A66B270" @keyup.enter="mvRead"
            class="w-40 rounded bg-ink-900 px-2 py-1.5 font-mono text-xs text-slate-200 ring-1 ring-white/10" />
        </label>
        <label class="flex flex-col gap-1 text-[11px] text-slate-400">
          Rows (×4 bytes)
          <input v-model.number="mvRows" type="number" min="1" max="16384" class="w-24 rounded bg-ink-900 px-2 py-1.5 text-xs text-slate-200 ring-1 ring-white/10" />
        </label>
        <div class="flex items-center gap-1">
          <button type="button" @click="mvJump(-0x100)" class="rounded bg-white/5 px-2 py-1.5 font-mono text-xs text-slate-300 ring-1 ring-white/10 hover:bg-white/10">−100</button>
          <button type="button" @click="mvJump(-0x40)" class="rounded bg-white/5 px-2 py-1.5 font-mono text-xs text-slate-300 ring-1 ring-white/10 hover:bg-white/10">−40</button>
          <button type="button" @click="mvJump(0x40)" class="rounded bg-white/5 px-2 py-1.5 font-mono text-xs text-slate-300 ring-1 ring-white/10 hover:bg-white/10">+40</button>
          <button type="button" @click="mvJump(0x100)" class="rounded bg-white/5 px-2 py-1.5 font-mono text-xs text-slate-300 ring-1 ring-white/10 hover:bg-white/10">+100</button>
        </div>
        <button type="button" :disabled="!attached" @click="mvRead" class="rounded-lg bg-accent/15 px-4 py-2 text-sm font-semibold text-accent ring-1 ring-accent/30 hover:bg-accent/25 disabled:opacity-40">Read</button>

        <div class="ml-auto flex items-center gap-2">
          <button type="button" :disabled="!attached" @click="mvToggleRecord"
            class="rounded-lg px-4 py-2 text-sm font-semibold ring-1 transition disabled:opacity-40"
            :class="mvRecording ? 'bg-rose-500/15 text-rose-200 ring-rose-400/30 hover:bg-rose-500/25' : 'bg-white/5 text-slate-200 ring-white/10 hover:bg-white/10'">
            {{ mvRecording ? '■ Stop' : '● Record (1s)' }}
          </button>
          <span v-if="mvSamples.length" class="rounded bg-white/5 px-2 py-1 font-mono text-xs text-slate-300">{{ mvSamples.length }} samples</span>
          <button v-if="mvSamples.length && !mvRecording" type="button" @click="mvCopy"
            class="rounded-lg bg-emerald-500/15 px-3 py-2 text-xs font-semibold text-emerald-200 ring-1 ring-emerald-400/30 hover:bg-emerald-500/25">
            {{ mvCopied ? '✓ copied' : 'Copy for analysis' }}
          </button>
        </div>
      </div>

      <p v-if="mvError" class="mt-2 rounded bg-rose-500/10 px-3 py-1.5 text-[11px] text-rose-300">{{ mvError }}</p>
      <div class="mt-2 flex flex-wrap items-center justify-between gap-2 text-[10px] text-slate-500">
        <span>
          Each row is 4 bytes. Click a <span class="text-accent">float</span> or <span class="text-slate-300">int</span> to lock + live-watch.
          Changed slots flash amber — cast and watch which row counts down.
        </span>
        <label class="flex items-center gap-1.5 text-slate-400">
          <input v-model="mvOnlyChanged" type="checkbox" class="accent-emerald-400" /> only changed
        </label>
      </div>
      <p v-if="mvData.length > MV_FULL_LIMIT && mvOnlyChanged" class="mt-1 text-[10px] text-amber-300/80">
        Large region: showing {{ mvVisible.length }} moving of {{ mvData.length }} slots. Untick “only changed” to show all. Recording still captures everything.
      </p>

      <div v-if="mvVisible.length" class="mt-3 overflow-x-auto">
        <table class="w-full border-collapse font-mono text-xs">
          <thead>
            <tr class="text-left text-[10px] uppercase tracking-wide text-slate-500">
              <th class="px-2 py-1">off</th>
              <th class="px-2 py-1">address</th>
              <th class="px-2 py-1">bytes</th>
              <th class="px-2 py-1 text-right">int32</th>
              <th class="px-2 py-1 text-right">uint32</th>
              <th class="px-2 py-1 text-right">float</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in mvVisible" :key="row.offset" class="border-t border-white/5 transition-colors" :class="row.chg ? 'bg-amber-400/10' : ''">
              <td class="px-2 py-1 text-slate-500">+{{ row.offset.toString(16).toUpperCase() }}</td>
              <td class="px-2 py-1 text-slate-500">0x{{ row.address.toString(16).toUpperCase() }}</td>
              <td class="px-2 py-1 text-slate-400">{{ row.hex }}</td>
              <td class="px-2 py-1 text-right">
                <button type="button" @click="lockAt(row.address, 'int32')" class="text-slate-200 hover:text-accent">{{ row.int32 }}</button>
              </td>
              <td class="px-2 py-1 text-right text-slate-400">{{ row.uint32 }}</td>
              <td class="px-2 py-1 text-right">
                <button type="button" @click="lockAt(row.address, 'float')" class="text-accent hover:underline">{{ fmtNum(row.float32) }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      </template>
    </section>

    <!-- Cast-correlated auto-locator (shortcut) -->
    <section class="rounded-xl border border-white/5 bg-ink-900/40 p-4">
      <h2 class="text-sm font-bold text-white">🎯 Auto-locate cooldown</h2>
      <p class="mt-1 text-[11px] text-slate-400">
        Finds the skill's <b>elapsed-since-cast</b> float (resets to ~0 on cast, then counts up). Click <b>Arm</b>,
        wait for "now cast", then <b>cast the skill once</b> — it narrows automatically.
      </p>
      <div class="mt-3 flex flex-wrap items-center gap-3">
        <button v-if="stage === 'idle'" type="button" :disabled="!attached || alBusy" @click="alArm"
          class="rounded-lg bg-accent/15 px-4 py-2 text-sm font-semibold text-accent ring-1 ring-accent/30 hover:bg-accent/25 disabled:opacity-40">▶ Arm</button>
        <span v-if="stage === 'armed'" class="rounded-lg bg-amber-500/15 px-4 py-2 text-sm font-semibold text-amber-200 ring-1 ring-amber-400/30">now cast the skill…</span>
        <template v-if="stage === 'narrowing'">
          <button type="button" :disabled="alBusy" @click="alNarrow('inc', 'still counting up')"
            class="rounded-lg bg-emerald-500/15 px-4 py-2 text-sm font-semibold text-emerald-200 ring-1 ring-emerald-400/30 hover:bg-emerald-500/25 disabled:opacity-40">↑ still counting</button>
        </template>
        <button v-if="stage !== 'idle'" type="button" @click="stage = 'idle'; alNote = ''; alHits = []; backend()?.ScanReset()"
          class="rounded-lg px-3 py-2 text-xs text-slate-500 ring-1 ring-white/10 hover:text-rose-300">reset</button>
        <span v-if="alCount > 0" class="ml-auto rounded bg-white/5 px-2 py-1 font-mono text-xs text-slate-300">{{ alCount }} candidates</span>
      </div>
      <p v-if="alNote" class="mt-3 rounded bg-white/5 px-3 py-2 text-[11px] text-slate-300">{{ alNote }}</p>
      <div v-if="alHits.length" class="mt-3 space-y-1">
        <div v-for="h in alHits" :key="h.address" class="flex items-center gap-1">
          <button type="button" @click="lockAt(h.address, 'float')"
            class="flex flex-1 items-center justify-between rounded bg-ink-900 px-3 py-1.5 font-mono text-xs ring-1 ring-white/10 hover:ring-accent/40">
            <span class="text-slate-400">0x{{ h.address.toString(16).toUpperCase() }}</span>
            <span class="text-accent">{{ h.display }}</span>
          </button>
          <button type="button" @click="removeCandidate(h.address)" title="remove from list"
            class="shrink-0 rounded px-2 py-1.5 text-xs text-slate-500 ring-1 ring-white/10 hover:bg-rose-500/10 hover:text-rose-300">✕</button>
        </div>
      </div>
    </section>

    <!-- Manual pointer watch -->
    <section class="rounded-xl border border-white/5 bg-ink-900/40 p-4">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-bold text-white">🔧 Manual pointer watch</h2>
        <button type="button" @click="addWatch" class="rounded bg-white/5 px-3 py-1.5 text-xs font-semibold text-slate-300 ring-1 ring-white/10 hover:bg-white/10">+ watch</button>
      </div>
      <p class="mt-1 text-[11px] text-slate-400">
        Verify a raw address or CE pointer path: <code class="text-slate-300">Aion2.exe+0xBASE -&gt; 0x18 -&gt; 0x10</code>
      </p>
      <div class="mt-3 space-y-2">
        <div v-for="w in watches" :key="w.id" class="rounded-lg bg-ink-900/60 p-3">
          <div class="flex items-center gap-2">
            <input v-model="w.label" @change="persist" placeholder="label" class="w-28 shrink-0 rounded bg-ink-900 px-2 py-1.5 text-xs font-semibold text-white ring-1 ring-white/10" />
            <input v-model="w.spec" @change="persist" spellcheck="false" placeholder="Aion2.exe+0xBASE -> 0x10" class="flex-1 rounded bg-ink-900 px-2 py-1.5 font-mono text-xs text-slate-200 ring-1 ring-white/10" />
            <button type="button" @click="readWatch(w)" class="rounded bg-white/5 px-3 py-1.5 text-xs font-semibold text-slate-300 ring-1 ring-white/10 hover:bg-white/10">read</button>
            <button type="button" @click="removeWatch(w.id)" class="rounded px-2 py-1.5 text-xs text-slate-500 hover:text-rose-300">✕</button>
          </div>
          <div v-if="w.result" class="mt-2 font-mono text-xs">
            <div v-if="w.result.error" class="rounded bg-rose-500/10 px-3 py-1.5 text-rose-300">{{ w.result.error }}</div>
            <div v-else class="flex flex-wrap gap-x-5 gap-y-1">
              <span class="text-slate-500">addr <span class="text-slate-300">0x{{ w.result.address?.toString(16).toUpperCase() }}</span></span>
              <span class="text-accent">f32 {{ fmtNum(w.result.float32) }}</span>
              <span class="text-slate-300">i32 {{ w.result.int32 }}</span>
              <span class="text-slate-300">u32 {{ w.result.uint32 }}</span>
              <span class="text-slate-500">{{ w.result.hexLE }}</span>
            </div>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>
