// Shared capture controller — single owner of the capture engine state and
// orchestration. The CaptureBar (rendered app-wide by App.vue) drives it, and
// it keeps the engine in sync with the shared `config` store, so any menu can
// edit settings and the running capture updates automatically.
import { reactive, ref, watch } from 'vue'
import { config, loadConfig, saveConfig } from './config'
import { i18n } from './i18n'
import { skills, relatedSkillIds } from './projects/pingmaker/skills'
import {
  captureAvailable,
  EventsOn,
  isCapturing,
  setCasterAutoDetect,
  setCasterFilter,
  setCatalog,
  startCapture,
  stopCapture,
  updateCapture,
  type SkillSpeed,
} from './projects/pingmaker/capture'

export type LogKind = 'info' | 'cast' | 'warn'
export interface LogEntry {
  time: string
  msg: string
  kind: LogKind
}

export const running = ref(false)
export const status = ref<'idle' | 'running' | 'error'>('idle')
export const ports = ref<number[]>([])
export const modified = ref(0)
export const ping = ref(0)
export const errorMsg = ref('')
export const gameDetected = ref(false)
export const logs = reactive<LogEntry[]>([])

// Caster auto-detect: true while the engine is counting ACTs to find your caster.
export const casterAutoDetecting = ref(false)
export async function startCasterAutoDetect() {
  if (!captureAvailable()) return
  if (!running.value) await start()
  await setCasterAutoDetect(true).catch(() => {})
  casterAutoDetecting.value = true
}
export async function stopCasterAutoDetect() {
  await setCasterAutoDetect(false).catch(() => {})
  casterAutoDetecting.value = false
}

// Recently used skills (newest first), for the overlay HUD. Each cast is a
// numeric skill id; the UI maps it to a Skill for image/name.
export interface CastEntry {
  id: number
  uid: number
}
export const recentCasts = reactive<CastEntry[]>([])
const MAX_RECENT = 10
let castUid = 1
let lastCastId = 0
let lastCastAt = 0

// Live cast tracking for the stats panel. `casting` = a skill was used recently
// (drives the live/green vs offline look); `castCount` = how many casts in this
// burst, reset after CAST_RESET_MS of no casts.
export const castCount = ref(0)
export const casting = ref(false)
const LIVE_WINDOW_MS = 2000 // no cast for this long → not "live" anymore
const CAST_RESET_MS = 8000 // no cast for this long → clear the count + recent list

function stamp() {
  return new Date().toTimeString().slice(0, 8)
}

export function log(msg: string, kind: LogKind = 'info') {
  logs.push({ time: stamp(), msg, kind })
  if (logs.length > 300) logs.splice(0, logs.length - 300)
}
export function clearLogs() {
  logs.splice(0, logs.length)
}

const casterLogPattern = /\[caster:(\d+),/

function captureLogAllowed(msg: string): boolean {
  if (msg.startsWith('ACT')) return true
  const caster = config.casterRecord
  if (!Number.isFinite(caster) || caster <= 0) return true
  return Number(msg.match(casterLogPattern)?.[1] ?? 0) === caster
}

function pruneCaptureLogs() {
  for (let i = logs.length - 1; i >= 0; i--) {
    if (!captureLogAllowed(logs[i].msg)) logs.splice(i, 1)
  }
}

// Build the engine config (SkillSpeed[]) from the persisted rows + skill catalog.
function buildConfig(): SkillSpeed[] {
  const byId = new Map(skills.map((s) => [s.id, s]))
  const out: SkillSpeed[] = []
  for (const r of config.rows) {
    const sk = byId.get(r.id)
    if (sk) {
      // relatedSkillIds expands charge skills (e.g. Hellfire) to cover every
      // charge tier — the charged cast fires under a tier ID, not the base.
      // primaryIds = this row's own tier ids, so the engine lets an explicit
      // tier row (e.g. Hellfire - Max) override the expanded fallback.
      out.push({ name: sk.name, ids: relatedSkillIds(sk), primaryIds: sk.skill_ids, speedPct: r.speedPct || 0, break: r.brk, override: r.overridden })
    }
  }
  return out
}

export async function start() {
  errorMsg.value = ''
  if (!captureAvailable()) {
    errorMsg.value = i18n.global.t('ping.backendUnavailable')
    return
  }
  try {
    await startCapture(buildConfig())
  } catch (e: any) {
    const m = String(e?.message ?? e)
    errorMsg.value = m === 'backend-unavailable' ? i18n.global.t('ping.backendUnavailable') : m
    status.value = 'error'
  }
}
export async function stop() {
  await stopCapture()
}

let inited = false

// initCapture wires events, pushes the saved config to the engine, and sets up
// watchers that hot-reload the engine on config edits. Called once (CaptureBar).
export async function initCapture() {
  if (inited) return
  inited = true

  EventsOn('capture:log', (m: string) => {
    const msg = String(m)
    if (captureLogAllowed(msg)) log(msg, msg.startsWith('ACT') ? 'cast' : 'info')
  })
  EventsOn('capture:status', (s: string) => {
    status.value = s as 'idle' | 'running' | 'error'
    running.value = s === 'running'
  })
  EventsOn('capture:ports', (p: number[]) => {
    ports.value = p ?? []
  })
  EventsOn('capture:count', (c: number) => {
    modified.value = c ?? 0
  })
  EventsOn('capture:error', (m: string) => {
    errorMsg.value = m
    status.value = 'error'
    running.value = false
    log(m, 'warn')
  })
  EventsOn('capture:ping', (p: number) => {
    ping.value = p ?? 0
  })
  // Caster auto-detect found your caster: drop it into the filter field (the
  // casterRecord watcher pushes it to the engine + persists), then stop.
  EventsOn('capture:caster-auto', (caster: number) => {
    if (!Number.isFinite(caster) || caster <= 0) return
    config.casterRecord = caster
    saveConfig()
    casterAutoDetecting.value = false
    log('Auto-detected caster ' + caster, 'info')
  })
  EventsOn('capture:cast', (c: { id: number }) => {
    if (!c || typeof c.id !== 'number') return
    const now = Date.now()
    if (c.id === lastCastId && now - lastCastAt < 250) return // drop retransmit duplicate
    lastCastId = c.id
    lastCastAt = now
    recentCasts.unshift({ id: c.id, uid: castUid++ })
    if (recentCasts.length > MAX_RECENT) recentCasts.splice(MAX_RECENT)
    castCount.value++
    casting.value = true
  })
  EventsOn('game:status', (v: boolean) => {
    gameDetected.value = !!v
  })
  // Mod menu file-operation lines (e.g. intro remove/restore) share the log box.
  EventsOn('mod:log', (m: string) => log(String(m)))

  // Drive the live indicator + idle reset for the stats panel.
  setInterval(() => {
    if (lastCastAt === 0) return
    const idle = Date.now() - lastCastAt
    if (idle > LIVE_WINDOW_MS) casting.value = false
    if (idle > CAST_RESET_MS) {
      castCount.value = 0
      casting.value = false
      recentCasts.splice(0)
      lastCastAt = 0
    }
  }, 500)

  await loadConfig()
  if (captureAvailable()) {
    setCatalog(skills.map((s) => ({ name: s.name, ids: s.skill_ids }))).catch(() => {})
    running.value = await isCapturing()
    if (running.value) status.value = 'running'
  }

  // Keep the engine in sync with config edits made from any menu.
  watch(
    () => config.rows,
    () => {
      if (running.value) updateCapture(buildConfig()).catch(() => {})
    },
    { deep: true },
  )

  // Manual caster filter: the user types the caster entity key (0/blank = all).
  // Pushes the value to the engine (only that caster is edited/logged) and
  // re-applies the log filter so stale lines from other casters are dropped.
  watch(
    [() => config.casterRecord, running],
    () => {
      const c = Number(config.casterRecord)
      const id = Number.isFinite(c) && c > 0 ? c : 0
      if (captureAvailable()) setCasterFilter(id).catch(() => {})
      pruneCaptureLogs()
    },
    { immediate: true },
  )
}
