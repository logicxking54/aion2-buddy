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

// Recent casters of your configured skills, for the caster picker.
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
  // Caster picker: the engine emits the recent casters of YOUR configured skills as
  // selectable options. Your selection is STICKY while it's still listed, so nobody
  // else casting your skills can steal the filter; a lone caster locks in on its own,
  // and anything more ambiguous waits for you to pick.
  //
  // Re-locking after re-entering an instance (where entity keys are re-assigned)
  // comes from options expiring engine-side: the previous run's key stops casting,
  // ages out, and the branch below runs again for the new one.
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
