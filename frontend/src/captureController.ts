// Shared capture controller — single owner of the capture engine state and
// orchestration. The CaptureBar (rendered app-wide by App.vue) drives it, and
// it keeps the engine in sync with the shared `config` store, so any menu can
// edit settings and the running capture updates automatically.
import { reactive, ref, watch } from 'vue'
import { config, loadConfig } from './config'
import { i18n } from './i18n'
import { skills, relatedSkillIds } from './projects/pingmaker/skills'
import {
  captureAvailable,
  EventsOn,
  isCapturing,
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

// Caster (entity key) of your most recent own ACT cast, emitted by the engine
// before the engine-level caster filter is applied. Drives the caster filter's
// auto-lock so it can follow you even when other casters are suppressed.
export const lastActCaster = ref(0)
let lastActSeenAt = 0 // ms timestamp the current locked caster was last seen (stickiness)

// Caster filter (entity key as a string; '' = none). Shared between the Ping Maker
// menu (the input field), the global log panel (display filter), and the engine —
// which, when set, ignores every other caster entirely (no log, no cast event, no
// combat-speed edit). Auto-locks onto your own caster (from capture:act / lastActCaster)
// unless the user manually overrides the field (manualFilter); clearing it re-enables
// the auto-lock. The watchers that drive auto-lock + the engine push live in initCapture.
export const casterFilter = ref('')
export const manualFilter = ref(false)
export function onFilterInput(e: Event) {
  manualFilter.value = (e.target as HTMLInputElement).value.trim() !== ''
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

// Build the engine config (SkillSpeed[]) from the persisted rows + skill catalog.
function buildConfig(): SkillSpeed[] {
  const byId = new Map(skills.map((s) => [s.id, s]))
  const out: SkillSpeed[] = []
  for (const r of config.rows) {
    const sk = byId.get(r.id)
    if (sk) {
      // relatedSkillIds expands charge skills (e.g. Hellfire) to cover every
      // charge tier — the charged cast fires under a tier ID, not the base.
      out.push({ name: sk.name, ids: relatedSkillIds(sk), speedPct: r.speedPct || 0, break: r.brk, override: r.overridden })
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

  EventsOn('capture:log', (m: string) => log(String(m), String(m).startsWith('ACT') ? 'cast' : 'info'))
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
  EventsOn('capture:act', (id: number) => {
    const n = Number(id)
    if (!Number.isFinite(n) || n <= 0) return
    const now = Date.now()
    // Sticky lock: keep the current caster unless it's the same id (refresh its
    // freshness) or the current one has gone quiet for a while (a real caster
    // change, e.g. re-entering a dungeon). This stops a single misread key during
    // a spam burst from hijacking the lock away from you and dropping your casts.
    if (n === lastActCaster.value || lastActCaster.value === 0 || now - lastActSeenAt > 4000) {
      lastActCaster.value = n
      lastActSeenAt = now
    }
  })
  EventsOn('game:status', (v: boolean) => {
    gameDetected.value = !!v
  })
  EventsOn('arduino:log', (e: { msg: string; kind: LogKind }) => {
    log(e?.msg ?? '', (e?.kind as LogKind) ?? 'info')
  })
  // Oversize Network log lines share the global (bottom) log.
  EventsOn('oversize:log', (m: string) => log(String(m)))
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

  // Auto-lock the caster filter onto our own caster (from capture:act), unless the
  // user is manually overriding the field. Re-locks when our caster id changes.
  watch(lastActCaster, (id) => {
    if (manualFilter.value) return
    if (id > 0) casterFilter.value = String(id)
  })

  // Push the caster filter to the engine: when set, it ignores every other caster.
  // Re-push when the value changes or capture (re)starts.
  watch(
    [casterFilter, running],
    () => {
      if (!running.value) return // engine isn't capturing; nothing to filter
      const id = Number.parseInt(casterFilter.value.trim(), 10)
      setCasterFilter(Number.isFinite(id) ? id : 0).catch(() => {})
    },
    { immediate: true },
  )
}
