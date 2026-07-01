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
  setSkillSwap,
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

// Recent casters of your configured skills (last ~20 ACTs), for the caster picker.
export interface ActCaster {
  id: number
  name: string
}
export const actCasters = ref<ActCaster[]>([])
// Pick your own caster from the dropdown (same-class party case); 0 = all casters.
export function pickCaster(id: number) {
  config.casterRecord = Number(id) || 0
}

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

// Build the skill-override map from rows that picked a "render as" skill: every
// variant id of the row's skill maps to the target skill's base id, so the client
// draws the target's animation + VFX for that cast (server outcome unchanged).
function buildSwap(): { from: number[]; to: number[] } {
  const byId = new Map(skills.map((s) => [s.id, s]))
  const from: number[] = []
  const to: number[] = []
  for (const r of config.rows) {
    if (!r.swapId) continue
    const src = byId.get(r.id)
    const dst = byId.get(r.swapId)
    if (!src || !dst || dst.skill_ids.length === 0) continue
    const target = dst.skill_ids[0]
    for (const id of src.skill_ids) {
      from.push(id)
      to.push(target)
    }
  }
  return { from, to }
}

// Push the current override map to the engine. Safe to call anytime — the engine
// holds the map and only applies it while capturing. from.length 0 clears it.
function pushSwap() {
  if (!captureAvailable()) return
  const { from, to } = buildSwap()
  setSkillSwap(from.length > 0, from, to).catch(() => {})
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
  // Caster picker: the engine emits every distinct caster of YOUR configured skills
  // seen this session (with character names) as selectable options. Your selection
  // is STICKY — once a caster is chosen (auto or manual) it's never replaced while
  // it's still a known caster; other players casting your skills only ever get added
  // as options, they can't flip the filter. Auto-lock happens only when there's no
  // valid pick yet: a single caster locks automatically, several wait for a manual
  // pick. A stale pick from a previous session (its id absent from the fresh list
  // after the engine resets on a session change) is dropped and re-locked.
  EventsOn('capture:act-casters', (list: ActCaster[]) => {
    actCasters.value = Array.isArray(list) ? list : []
    const cur = Number(config.casterRecord)
    if (cur > 0 && actCasters.value.some((c) => c.id === cur)) return // keep your pick
    config.casterRecord = actCasters.value.length === 1 ? actCasters.value[0].id : 0
  })
  EventsOn('game:status', (v: boolean) => {
    gameDetected.value = !!v
  })
  // Mod menu file-operation lines (e.g. intro remove/restore) share the log box.
  EventsOn('mod:log', (m: string) => log(String(m)))

  await loadConfig()
  if (captureAvailable()) {
    setCatalog(skills.map((s) => ({ name: s.name, ids: s.skill_ids }))).catch(() => {})
    running.value = await isCapturing()
    if (running.value) status.value = 'running'
  }

  // Keep the engine in sync with config edits made from any menu: combat-speed
  // rows (only while running) and the per-row skill override map (safe anytime;
  // also re-pushed when capture starts so the override is restored).
  watch(
    [() => config.rows, running],
    () => {
      if (running.value) updateCapture(buildConfig()).catch(() => {})
      pushSwap()
    },
    { deep: true, immediate: true },
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
