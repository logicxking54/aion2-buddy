<script lang="ts" setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { classes, skills, skillByNumericId, skillColor, type Skill, type SkillClass } from '../pingmaker/skills'
import { EventsOn } from '../pingmaker/capture'
import errorImage from '../../assets/images/skill-error.svg'
import { config, loadConfig, saveConfig } from '../../config'
import { log } from '../../captureController'
import { autoSelectPort, boardConnected, connectBoard, currentPort, disconnectBoard, listSerialPorts, sendKey } from './arduino'
import { captureRectPreview, sampleRects, startSkillbar, stopSkillbar, type BarRect, type SlotState } from './screen'

const { t } = useI18n()
const byId = new Map(skills.map((s) => [s.id, s]))

function classLabel(c: string) {
  const key = `ping.classes.${c}`
  const translated = t(key)
  return translated === key ? c : translated
}
function skillImage(s: Skill) {
  return s.image_url && s.image_url.trim() ? s.image_url : errorImage
}
function onImgError(e: Event) {
  ;(e.target as HTMLImageElement).src = errorImage
}

// ── Arduino board ───────────────────────────────────────────────────────
const ports = ref<string[]>([])
const board = ref('')
const connected = ref(false)
const connecting = ref(false)
const connError = ref('')

async function refreshPorts() {
  ports.value = await listSerialPorts()
}
function applyBoard() {
  writeConfig()
}
async function connect() {
  if (!board.value) return
  connError.value = ''
  connecting.value = true
  try {
    await connectBoard(board.value)
  } catch (e: any) {
    connError.value = String(e?.message ?? e)
  } finally {
    connecting.value = false
  }
}
async function disconnect() {
  await disconnectBoard()
}
async function autoSelect() {
  connError.value = ''
  const p = await autoSelectPort() // backend logs what it found to the app log
  if (!p) {
    connError.value = t('ah.noBoardFound')
    return
  }
  await refreshPorts()
  board.value = p
  applyBoard()
}
async function testKey(token: string) {
  const k = token.trim()
  if (k) await sendKey(k).catch(() => {})
}
const testResult = ref<'' | 'ok' | 'fail'>('')
async function testBoard() {
  if (!connected.value) return
  log('Board test: typing "test" — focus a text field to watch it', 'info')
  let ok = true
  for (const k of ['t', 'e', 's', 't']) {
    try {
      await sendKey(k)
    } catch {
      ok = false
      break
    }
    await new Promise((r) => setTimeout(r, 70))
  }
  testResult.value = ok ? 'ok' : 'fail'
}

// ── Left: skill picker (draggable) ──────────────────────────────────────
const activeClass = ref<SkillClass | 'all'>('all')
const search = ref('')

const filteredSkills = computed(() => {
  const q = search.value.trim().toLowerCase()
  return skills.filter((s) => {
    const matchesClass = activeClass.value === 'all' || s.class === activeClass.value
    const matchesText = q === '' || s.name.toLowerCase().includes(q)
    return matchesClass && matchesText
  })
})

function onDragStart(e: DragEvent, s: Skill) {
  e.dataTransfer?.setData('text/skill-id', s.id)
}
function draggedSkill(e: DragEvent): Skill | undefined {
  const id = e.dataTransfer?.getData('text/skill-id')
  return id ? byId.get(id) : undefined
}

// ── Right: sequences ────────────────────────────────────────────────────
interface AutoSkill {
  uid: number
  skill: Skill
  key: string
  delay: number
}
interface Sequence {
  uid: number
  trigger: Skill | null
  triggerRect: BarRect | null // where this trigger skill's icon sits on screen
  autos: AutoSkill[]
}

let nextUid = 1
const sequences = reactive<Sequence[]>([])

function addSequence() {
  sequences.push({ uid: nextUid++, trigger: null, triggerRect: null, autos: [] })
  writeConfig()
}
function removeSequence(uid: number) {
  const i = sequences.findIndex((s) => s.uid === uid)
  if (i !== -1) sequences.splice(i, 1)
  writeConfig()
}
function dropOnTrigger(e: DragEvent, seq: Sequence) {
  const s = draggedSkill(e)
  if (!s) return
  seq.trigger = s
  writeConfig()
}
function removeTrigger(seq: Sequence) {
  seq.trigger = null
  writeConfig()
}
function dropOnAuto(e: DragEvent, seq: Sequence) {
  const s = draggedSkill(e)
  if (!s) return
  seq.autos.push({ uid: nextUid++, skill: s, key: '', delay: 0 })
  writeConfig()
}
function removeAuto(seq: Sequence, uid: number) {
  const i = seq.autos.findIndex((a) => a.uid === uid)
  if (i !== -1) seq.autos.splice(i, 1)
  writeConfig()
}
function setKey(a: AutoSkill, v: string) {
  a.key = v
  writeConfig()
}
function setDelay(a: AutoSkill, v: string) {
  a.delay = v === '' ? 0 : Number(v)
  writeConfig()
}

// ── Skill-bar screen monitor ────────────────────────────────────────────
// Each detection skill has its own screen rectangle; the skill is "ready"
// (off cooldown) when that rectangle's average brightness exceeds `threshold`.
const threshold = ref(60)
const monitoring = ref(false)
const previews = reactive<Record<number, string>>({}) // seq.uid → PNG data URL
const sampleVals = reactive<Record<number, number>>({}) // seq.uid → brightness
const slotStates = reactive<SlotState[]>([]) // live states from the monitor

// Detection skills (one per sequence with a trigger), in the order sent to the
// monitor — slotStates[idx] corresponds to detectionSkills[idx].
const detectionSkills = computed(() => sequences.filter((s) => s.trigger))

function setThreshold(v: string) {
  threshold.value = v === '' ? 0 : Number(v)
  writeConfig()
  if (monitoring.value) restartMonitor()
}
function ensureRect(seq: Sequence): BarRect {
  if (!seq.triggerRect) seq.triggerRect = { x: 0, y: 0, w: 48, h: 48 }
  return seq.triggerRect
}
function setRect(seq: Sequence, field: keyof BarRect, v: string) {
  ensureRect(seq)[field] = v === '' ? 0 : Number(v)
  writeConfig()
  if (monitoring.value) restartMonitor()
}
async function sampleOne(seq: Sequence) {
  const r = ensureRect(seq)
  const vals = await sampleRects([r])
  sampleVals[seq.uid] = vals[0] ?? 0
  previews[seq.uid] = await captureRectPreview(r)
}
function monitorRects(): BarRect[] {
  return detectionSkills.value.map((s) => s.triggerRect ?? { x: 0, y: 0, w: 0, h: 0 })
}
async function restartMonitor() {
  await startSkillbar(monitorRects(), threshold.value, 200)
}
async function toggleMonitor() {
  if (monitoring.value) {
    await stopSkillbar()
    monitoring.value = false
    return
  }
  await restartMonitor()
  monitoring.value = true
}
// live brightness/ready for the detection skill at the given list position.
function slotState(index: number): SlotState | undefined {
  return slotStates.find((s) => s.i === index)
}

const unsubBar = EventsOn('skillbar:state', (states: SlotState[]) => {
  slotStates.splice(0, slotStates.length, ...(states ?? []))
})

// ── Transform cycles + auto-fire (packet-driven) ─────────────────────────
// Transform skills (e.g. Flame Arrow → Burst → Pyroclasm) share one slot and
// cycle on each use. The capture engine emits capture:cast {id, next}; we learn
// the chain from the "next" pointers and track which form is currently loaded.
const autoFire = ref(false)
const transformNext = reactive<Record<number, number>>({}) // skill id → next-form id
const loadedByStart = reactive<Record<number, number>>({}) // chain start id → loaded form id
const revertTimers: Record<number, ReturnType<typeof setTimeout>> = {} // chain start → revert timer
// A transform slot drops back to its base form if you don't keep casting from
// it. We couldn't find a server "revert" packet, so fall back to this timeout.
const REVERT_MS = 3400

interface TransformChain {
  start: number // base form id (no incoming edge)
  ids: number[] // ordered cycle
  loaded: number // form currently loaded (what fires on the next press)
}
// Build linear chains from the learned next-pointers (start = no incoming edge).
function buildChains(): { start: number; ids: number[] }[] {
  const keys = Object.keys(transformNext).map(Number)
  if (keys.length === 0) return []
  const incoming = new Set(keys.map((k) => transformNext[k]))
  const out: { start: number; ids: number[] }[] = []
  for (const start of keys.filter((k) => !incoming.has(k))) {
    const ids: number[] = []
    const guard = new Set<number>()
    let cur: number | undefined = start
    while (cur != null && !guard.has(cur)) {
      ids.push(cur)
      guard.add(cur)
      cur = transformNext[cur]
    }
    out.push({ start, ids })
  }
  return out
}
const transformChains = computed<TransformChain[]>(() =>
  buildChains().map(({ start, ids }) => ({ start, ids, loaded: loadedByStart[start] ?? start })),
)

function skillOf(id: number): Skill | undefined {
  return skillByNumericId(id)
}

// Advance a transform slot's loaded form. Only a cast of a skill *in the chain*
// changes it — unrelated casts are ignored. Casting the loaded form again moves
// to the next form; a terminal cast loops back to the base. After a non-base
// form loads, a timer reverts the slot to base if nothing in the chain is cast.
function trackTransform(castId: number) {
  const chain = buildChains().find((c) => c.ids.includes(castId))
  if (!chain) return // not (yet) a known transform member
  const start = chain.start
  const loaded = transformNext[castId] ?? start // next form, or base if terminal
  loadedByStart[start] = loaded
  if (revertTimers[start]) clearTimeout(revertTimers[start])
  delete revertTimers[start]
  if (loaded !== start) {
    revertTimers[start] = setTimeout(() => {
      loadedByStart[start] = start
      delete revertTimers[start]
    }, REVERT_MS)
  }
}

// Auto-fire timing. The game sometimes drops board keystrokes, so we re-press
// the key every auto-skill's `delay` ms until the cast packet confirms the skill
// went off, giving up after SPAM_TIMEOUT_MS (e.g. the skill is out of resources).
const lastCastAt = reactive<Record<number, number>>({}) // skill id → last cast time (ms)
const firing = new Set<number>() // sequence uids currently auto-firing
const SPAM_INTERVAL_MS = 120 // fallback press rate when an auto-skill's delay is 0
const SPAM_TIMEOUT_MS = 2500
const SPAM_MAX_PRESSES = 5 // stop after this many tries (e.g. target already dead)

function sleep(ms: number) {
  return new Promise<void>((r) => setTimeout(r, ms))
}

// Run one sequence: for each auto-press skill, wait its delay, then spam the key
// until that skill's cast is confirmed (or timeout). Steps run in order.
async function runSequence(seq: Sequence) {
  if (firing.has(seq.uid)) return // already running for this trigger
  firing.add(seq.uid)
  try {
    log(`Auto-fire: ${seq.trigger!.name} detected`, 'cast')
    for (const a of seq.autos) {
      const key = a.key.trim()
      if (!key) continue
      const interval = a.delay > 0 ? a.delay : SPAM_INTERVAL_MS // press rate from the skill's delay field
      const start = Date.now()
      const confirmed = () => a.skill.skill_ids.some((id) => (lastCastAt[id] ?? 0) >= start)
      let presses = 0
      while (Date.now() - start < SPAM_TIMEOUT_MS && presses < SPAM_MAX_PRESSES && !confirmed()) {
        if (!autoFire.value || !connected.value) break // stopped mid-spam
        await sendKey(key).catch(() => {})
        presses++
        await sleep(interval)
      }
      if (confirmed()) log(`  ↳ '${key}' → ${a.skill.name} cast (${presses}×)`, 'info')
      else log(`  ↳ '${key}' → ${a.skill.name} not confirmed after ${presses}×`, 'warn')
    }
  } finally {
    firing.delete(seq.uid)
  }
}

// Auto-fire every sequence whose trigger matches this cast.
function fireSequencesFor(castId: number) {
  const matched = sequences.filter((s) => s.trigger && s.trigger.skill_ids.includes(castId))
  if (matched.length === 0) return // this cast isn't a trigger for any sequence
  if (!autoFire.value) return // auto-fire toggle is off
  if (!connected.value) {
    log('Auto-fire: board not connected — skipped', 'warn')
    return
  }
  for (const seq of matched) void runSequence(seq)
}

function toggleAutoFire() {
  autoFire.value = !autoFire.value
  log(autoFire.value ? 'Auto-fire ON' : 'Auto-fire OFF', 'info')
}

const unsubCast = EventsOn('capture:cast', (c: { id: number; next: number }) => {
  if (!c) return
  lastCastAt[c.id] = Date.now() // record so the spam loop can confirm casts
  if (c.next) transformNext[c.id] = c.next
  trackTransform(c.id)
  fireSequencesFor(c.id)
})

// ── Persistence ─────────────────────────────────────────────────────────
let loading = false
function writeConfig() {
  if (loading) return
  config.autoHotkey = {
    board: board.value,
    sequences: sequences.map((s) => ({
      triggerId: s.trigger ? s.trigger.id : null,
      triggerRect: s.triggerRect ? { ...s.triggerRect } : null,
      autos: s.autos.map((a) => ({ id: a.skill.id, key: a.key, delay: a.delay })),
    })),
    skillbar: { threshold: threshold.value },
  }
  saveConfig()
}

onMounted(async () => {
  loading = true
  await loadConfig()
  const ah = config.autoHotkey ?? { board: '', sequences: [] }
  board.value = ah.board ?? ''
  if (ah.skillbar?.threshold != null) threshold.value = ah.skillbar.threshold
  sequences.splice(0, sequences.length)
  for (const s of ah.sequences ?? []) {
    const trigger = s.triggerId ? byId.get(s.triggerId) ?? null : null
    const autos: AutoSkill[] = []
    for (const a of s.autos ?? []) {
      const sk = byId.get(a.id)
      if (sk) autos.push({ uid: nextUid++, skill: sk, key: a.key ?? '', delay: Number(a.delay) || 0 })
    }
    sequences.push({ uid: nextUid++, trigger, triggerRect: s.triggerRect ?? null, autos })
  }
  loading = false
  refreshPorts()
  connected.value = await boardConnected()
  if (connected.value) {
    const p = await currentPort()
    if (p) board.value = p
  }
})

const unsub = EventsOn('arduino:status', (s: { connected: boolean; port: string }) => {
  connected.value = !!s?.connected
  if (s?.port) board.value = s.port
})
onUnmounted(() => {
  unsub()
  unsubBar()
  unsubCast()
  Object.values(revertTimers).forEach(clearTimeout)
  if (monitoring.value) stopSkillbar().catch(() => {})
})
</script>

<template>
  <div class="mx-auto flex h-full max-w-6xl flex-col gap-3">
    <!-- Arduino board selector -->
    <div class="flex flex-col gap-2 rounded-xl border border-white/5 bg-ink-700 px-4 py-2.5">
      <!-- row 1: connection -->
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-xs font-semibold text-slate-300">{{ t('ah.board') }}</span>
        <select
          v-model="board"
          @change="applyBoard"
          class="rounded-md border border-white/10 bg-ink-900 px-2 py-1 text-sm text-white outline-none focus:border-accent/60"
        >
          <option value="">{{ t('ah.selectBoard') }}</option>
          <option v-for="p in ports" :key="p" :value="p">{{ p }}</option>
        </select>
        <button
          type="button"
          @click="refreshPorts"
          class="rounded-md bg-white/5 px-3 py-1 text-xs font-semibold text-slate-300 transition hover:bg-white/10"
        >{{ t('ah.refresh') }}</button>

        <button
          type="button"
          @click="autoSelect"
          class="rounded-md bg-white/5 px-3 py-1 text-xs font-semibold text-slate-300 transition hover:bg-white/10"
        >{{ t('ah.autoSelect') }}</button>

        <button
          v-if="!connected"
          type="button"
          @click="connect"
          :disabled="!board || connecting"
          class="rounded-md bg-accent/15 px-3 py-1 text-xs font-bold text-accent transition hover:bg-accent hover:text-ink-900 disabled:opacity-40"
        >{{ t('ah.connect') }}</button>
        <button
          v-else
          type="button"
          @click="disconnect"
          class="rounded-md bg-red-500/15 px-3 py-1 text-xs font-bold text-red-300 transition hover:bg-red-500 hover:text-white"
        >{{ t('ah.disconnect') }}</button>

        <span class="flex items-center gap-1.5 text-[11px]">
          <span class="h-2 w-2 rounded-full" :class="connected ? 'bg-emerald-400 shadow-[0_0_8px] shadow-emerald-400/60' : 'bg-slate-600'" />
          <span :class="connected ? 'text-emerald-300' : 'text-slate-500'">{{ connected ? t('ah.connected') : t('ah.notConnected') }}</span>
        </span>
        <span v-if="connError" class="text-[11px] text-red-400">{{ connError }}</span>
        <span v-else-if="ports.length === 0" class="text-[11px] text-slate-500">{{ t('ah.noPorts') }}</span>
      </div>

      <!-- row 2: actions -->
      <div class="flex flex-wrap items-center gap-2 border-t border-white/5 pt-2">
        <button
          v-if="connected"
          type="button"
          @click="testBoard"
          class="rounded-md bg-accent/15 px-3 py-1 text-xs font-bold text-accent transition hover:bg-accent hover:text-ink-900"
        >{{ t('ah.testBoard') }}</button>
        <button
          type="button"
          @click="toggleAutoFire"
          :disabled="!connected"
          :title="t('ah.autoFireHint')"
          class="rounded-md px-3 py-1 text-xs font-bold transition disabled:opacity-40"
          :class="autoFire ? 'bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25' : 'bg-white/5 text-slate-300 hover:bg-white/10'"
        >{{ autoFire ? t('ah.autoFireOn') : t('ah.autoFireOff') }}</button>
      </div>
    </div>

    <!-- Skill Bar Monitor (screen-reading: bright = ready, dim = on cooldown) -->
    <div class="rounded-xl border border-white/5 bg-ink-700 p-3">
      <div class="flex flex-wrap items-center gap-2">
        <h2 class="text-sm font-bold uppercase tracking-wider text-slate-400">{{ t('ah.skillbar') }}</h2>
        <button
          type="button"
          @click="toggleMonitor"
          :disabled="detectionSkills.length === 0"
          class="rounded-md px-3 py-1 text-xs font-bold transition disabled:opacity-40"
          :class="monitoring ? 'bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25' : 'bg-accent/15 text-accent hover:bg-accent/25'"
        >{{ monitoring ? '● ' + t('ah.monStop') : '▶ ' + t('ah.monStart') }}</button>
        <label class="flex items-center gap-1 text-[10px] text-slate-500">
          {{ t('ah.threshold') }}
          <input
            :value="threshold"
            @input="setThreshold(($event.target as HTMLInputElement).value)"
            type="number"
            min="0"
            max="255"
            class="w-16 rounded border border-white/10 bg-ink-900 px-1 py-0.5 text-right text-xs text-white outline-none focus:border-accent/60"
          />
        </label>
        <span class="text-[11px] text-slate-500">{{ t('ah.skillbarReq') }}</span>
      </div>

      <p v-if="detectionSkills.length === 0" class="py-3 text-xs text-slate-500">{{ t('ah.skillbarEmpty') }}</p>

      <!-- one row per detection skill: live state + its own screen rectangle -->
      <ul v-else class="mt-2 space-y-2">
        <li
          v-for="(seq, idx) in detectionSkills"
          :key="seq.uid"
          class="flex flex-wrap items-center gap-2 rounded-lg bg-ink-800 p-2"
        >
          <img
            :src="skillImage(seq.trigger!)"
            @error="onImgError"
            alt=""
            class="h-10 w-10 shrink-0 rounded-lg object-cover transition duration-150"
            :class="slotState(idx)?.ready ? 'ring-2 ring-emerald-400/80' : 'brightness-50 grayscale'"
          />
          <span class="w-28 shrink-0 truncate text-xs font-semibold text-white">{{ seq.trigger!.name }}</span>

          <label v-for="f in (['x', 'y', 'w', 'h'] as const)" :key="f" class="flex items-center gap-0.5 text-[10px] text-slate-500">
            {{ f.toUpperCase() }}
            <input
              :value="seq.triggerRect ? seq.triggerRect[f] : 0"
              @input="setRect(seq, f, ($event.target as HTMLInputElement).value)"
              type="number"
              class="w-14 rounded border border-white/10 bg-ink-900 px-1 py-0.5 text-right text-xs text-white outline-none focus:border-accent/60"
            />
          </label>

          <button
            type="button"
            @click="sampleOne(seq)"
            class="rounded-md bg-white/5 px-2 py-1 text-[11px] font-semibold text-slate-300 transition hover:bg-white/10"
          >{{ t('ah.sample') }}</button>

          <img v-if="previews[seq.uid]" :src="previews[seq.uid]" alt="" class="h-10 rounded border border-white/10" />

          <span
            v-if="monitoring && slotState(idx)"
            class="rounded px-1.5 py-0.5 font-mono text-[10px]"
            :class="slotState(idx)!.ready ? 'bg-emerald-500/15 text-emerald-300' : 'bg-white/5 text-slate-400'"
          >{{ Math.round(slotState(idx)!.brightness) }}</span>
          <span
            v-else-if="sampleVals[seq.uid] != null"
            class="rounded px-1.5 py-0.5 font-mono text-[10px]"
            :class="sampleVals[seq.uid] > threshold ? 'bg-emerald-500/15 text-emerald-300' : 'bg-white/5 text-slate-400'"
          >{{ Math.round(sampleVals[seq.uid]) }}</span>
        </li>
      </ul>
    </div>

    <!-- Transform cycles (packet-tracked; highlighted = form loaded now) -->
    <div v-if="transformChains.length" class="rounded-xl border border-white/5 bg-ink-700 p-3">
      <h2 class="text-sm font-bold uppercase tracking-wider text-slate-400">{{ t('ah.transform') }}</h2>
      <p class="mt-1 text-[11px] text-slate-500">{{ t('ah.transformHint') }}</p>
      <div class="mt-2 space-y-2">
        <div v-for="(ch, ci) in transformChains" :key="ci" class="flex flex-wrap items-center gap-1">
          <template v-for="(id, k) in ch.ids" :key="id">
            <span v-if="k > 0" class="text-slate-600">→</span>
            <img
              :src="skillOf(id) ? skillImage(skillOf(id)!) : errorImage"
              @error="onImgError"
              :alt="String(id)"
              :title="skillOf(id)?.name ?? ('ID:' + id)"
              class="h-10 w-10 rounded-lg object-cover transition duration-150"
              :class="id === ch.loaded ? 'ring-2 ring-emerald-400/80' : 'brightness-50 grayscale'"
            />
          </template>
          <span class="ml-2 text-[11px] font-semibold text-emerald-300">
            {{ t('ah.loaded') }}: {{ skillOf(ch.loaded)?.name ?? ch.loaded }}
          </span>
        </div>
      </div>
    </div>

    <div class="grid min-h-0 flex-1 grid-cols-2 gap-4">
      <!-- ============ LEFT: skill picker (drag source) ============ -->
      <div class="flex min-h-0 flex-col rounded-xl border border-white/5 bg-ink-700 p-4">
        <h2 class="text-sm font-bold uppercase tracking-wider text-slate-400">{{ t('ping.skills') }}</h2>

        <div class="mt-2 flex flex-wrap gap-2">
          <button
            type="button"
            @click="activeClass = 'all'"
            class="rounded-lg px-3 py-1.5 text-xs font-semibold transition"
            :class="activeClass === 'all' ? 'bg-accent/15 text-white ring-1 ring-accent/30' : 'text-slate-300 hover:bg-white/5'"
          >{{ t('ping.all') }}</button>
          <button
            v-for="c in classes"
            :key="c.id"
            type="button"
            @click="activeClass = c.id"
            class="flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-semibold transition"
            :class="activeClass === c.id ? 'text-white ring-1' : 'text-slate-300 hover:bg-white/5'"
            :style="activeClass === c.id ? { backgroundColor: c.color + '22', boxShadow: 'inset 0 0 0 1px ' + c.color } : {}"
          >
            <span>{{ c.icon }}</span>{{ classLabel(c.id) }}
          </button>
        </div>

        <input
          v-model="search"
          type="text"
          :placeholder="t('ping.search')"
          class="mt-2 w-full rounded-lg border border-white/10 bg-ink-800 px-3 py-2 text-sm text-white placeholder:text-slate-500 outline-none focus:border-accent/60 focus:ring-2 focus:ring-accent/20"
        />

        <p class="mt-2 text-[11px] text-slate-500">{{ t('ah.dragHint') }}</p>

        <ul class="mt-2 min-h-0 flex-1 space-y-2 overflow-y-auto pr-1">
          <li
            v-for="s in filteredSkills"
            :key="s.id"
            draggable="true"
            @dragstart="onDragStart($event, s)"
            class="flex cursor-grab items-center gap-3 rounded-lg border border-white/5 bg-ink-800 p-2 transition hover:border-white/10 active:cursor-grabbing"
          >
            <img :src="skillImage(s)" draggable="false" @error="onImgError" alt="" class="h-8 w-8 shrink-0 rounded-lg object-cover" />
            <div class="min-w-0 flex-1">
              <div class="truncate text-sm font-semibold text-white">{{ s.name }}</div>
              <div v-if="s.class" class="text-[11px] font-medium" :style="{ color: skillColor(s) }">{{ classLabel(s.class) }}</div>
            </div>
          </li>
          <li v-if="filteredSkills.length === 0" class="py-8 text-center text-sm text-slate-500">{{ t('ping.noMatch') }}</li>
        </ul>
      </div>

      <!-- ============ RIGHT: sequences ============ -->
      <div class="flex min-h-0 flex-col rounded-xl border border-white/5 bg-ink-700 p-4">
        <div class="flex items-center justify-between">
          <h2 class="text-sm font-bold uppercase tracking-wider text-slate-400">
            {{ t('ah.sequence') }} <span class="ml-1 text-slate-500">({{ sequences.length }})</span>
          </h2>
          <button
            type="button"
            @click="addSequence"
            class="rounded-md bg-accent/15 px-3 py-1.5 text-xs font-bold text-accent transition hover:bg-accent hover:text-ink-900"
          >{{ t('ah.addSequence') }}</button>
        </div>

        <!-- Empty state -->
        <div
          v-if="sequences.length === 0"
          class="mt-3 grid flex-1 place-items-center rounded-lg border border-dashed border-white/10 text-center"
        >
          <div>
            <div class="text-4xl">⌨️</div>
            <p class="mt-2 font-semibold text-slate-300">{{ t('ah.empty') }}</p>
            <p class="text-sm text-slate-500">{{ t('ah.emptyHint') }}</p>
          </div>
        </div>

        <!-- Sequences -->
        <ul v-else class="mt-3 min-h-0 flex-1 space-y-3 overflow-y-auto pr-1">
          <li v-for="(seq, i) in sequences" :key="seq.uid" class="rounded-lg border border-white/5 bg-ink-800 p-3">
            <div class="flex items-center justify-between">
              <span class="text-xs font-bold text-slate-300">{{ t('ah.sequence') }} {{ i + 1 }}</span>
              <button
                type="button"
                @click="removeSequence(seq.uid)"
                class="rounded px-1.5 text-xs font-semibold text-slate-500 transition hover:bg-red-500/10 hover:text-red-400"
              >✕</button>
            </div>

            <!-- Trigger (detection) zone -->
            <div class="mt-2 text-[11px] font-semibold uppercase tracking-wide text-slate-500">{{ t('ah.trigger') }}</div>
            <div
              @dragover.prevent
              @drop="dropOnTrigger($event, seq)"
              class="mt-1 flex min-h-[2.75rem] items-center gap-2 rounded-lg border border-dashed border-accent/30 bg-ink-900/40 p-2"
            >
              <template v-if="seq.trigger">
                <img :src="skillImage(seq.trigger)" @error="onImgError" alt="" class="h-8 w-8 shrink-0 rounded-lg object-cover" />
                <span class="min-w-0 flex-1 truncate text-sm font-semibold text-white">{{ seq.trigger.name }}</span>
                <button
                  type="button"
                  @click="removeTrigger(seq)"
                  class="rounded px-1.5 text-xs text-slate-500 transition hover:bg-red-500/10 hover:text-red-400"
                >✕</button>
              </template>
              <span v-else class="px-1 text-xs text-slate-500">{{ t('ah.triggerHint') }}</span>
            </div>

            <div class="my-1 text-center text-[11px] font-semibold text-slate-500">↓ {{ t('ah.auto') }}</div>

            <!-- Auto-press zone -->
            <div
              @dragover.prevent
              @drop="dropOnAuto($event, seq)"
              class="space-y-2 rounded-lg border border-dashed border-white/10 bg-ink-900/40 p-2"
            >
              <div
                v-for="a in seq.autos"
                :key="a.uid"
                class="flex items-center gap-2 rounded-md bg-ink-800 p-1.5"
                :style="{ boxShadow: 'inset 3px 0 0 ' + skillColor(a.skill) }"
              >
                <img :src="skillImage(a.skill)" @error="onImgError" alt="" class="h-7 w-7 shrink-0 rounded object-cover" />
                <span class="min-w-0 flex-1 truncate text-xs font-semibold text-white">{{ a.skill.name }}</span>
                <label class="flex items-center gap-1 text-[10px] text-slate-500">
                  {{ t('ah.key') }}
                  <input
                    :value="a.key"
                    @input="setKey(a, ($event.target as HTMLInputElement).value)"
                    type="text"
                    maxlength="12"
                    class="w-12 rounded border border-white/10 bg-ink-900 px-1 py-0.5 text-center text-xs text-white outline-none focus:border-accent/60"
                  />
                </label>
                <label class="flex items-center gap-1 text-[10px] text-slate-500">
                  {{ t('ah.delay') }}
                  <input
                    :value="a.delay"
                    @input="setDelay(a, ($event.target as HTMLInputElement).value)"
                    type="number"
                    min="0"
                    step="10"
                    class="w-14 rounded border border-white/10 bg-ink-900 px-1 py-0.5 text-right text-xs text-white outline-none focus:border-accent/60"
                  />
                </label>
                <button
                  type="button"
                  @click="testKey(a.key)"
                  :disabled="!connected || !a.key.trim()"
                  :title="t('ah.test')"
                  class="rounded px-1 text-xs text-accent transition hover:text-accent-soft disabled:opacity-30"
                >▶</button>
                <button
                  type="button"
                  @click="removeAuto(seq, a.uid)"
                  class="rounded px-1 text-xs text-slate-500 transition hover:bg-red-500/10 hover:text-red-400"
                >✕</button>
              </div>
              <p v-if="seq.autos.length === 0" class="px-1 py-1 text-xs text-slate-500">{{ t('ah.autoHint') }}</p>
            </div>
          </li>
        </ul>
      </div>
    </div>

    <!-- Board test result modal -->
    <div
      v-if="testResult"
      class="fixed inset-0 z-50 grid place-items-center bg-black/50"
      @click.self="testResult = ''"
    >
      <div class="w-72 rounded-xl border border-white/10 bg-ink-700 p-5 text-center shadow-2xl">
        <div
          class="mx-auto grid h-12 w-12 place-items-center rounded-full text-2xl"
          :class="testResult === 'ok' ? 'bg-emerald-400/15 text-emerald-300' : 'bg-red-500/15 text-red-300'"
        >{{ testResult === 'ok' ? '✓' : '✕' }}</div>
        <p class="mt-3 font-bold text-white">{{ testResult === 'ok' ? t('ah.boardOk') : t('ah.boardFail') }}</p>
        <p class="mt-1 text-xs text-slate-400">{{ testResult === 'ok' ? t('ah.boardOkHint') : t('ah.boardFailHint') }}</p>
        <button
          type="button"
          @click="testResult = ''"
          class="mt-4 w-full rounded-lg bg-accent px-4 py-2 text-sm font-bold text-ink-900 transition hover:bg-accent-soft"
        >{{ t('common.ok') }}</button>
      </div>
    </div>
  </div>
</template>
