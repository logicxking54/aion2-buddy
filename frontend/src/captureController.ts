// Shared capture controller — single owner of the capture engine state and
// orchestration. The CaptureBar (rendered app-wide by App.vue) drives it, and
// it keeps the engine in sync with the shared `config` store, so any menu can
// edit settings and the running capture updates automatically.
import { computed, reactive, ref, watch } from 'vue'
import { config, loadConfig } from './config'
import { i18n } from './i18n'
import { skills, relatedSkillIds } from './projects/pingmaker/skills'
import {
  captureAvailable,
  EventsOn,
  isCapturing,
  setCasterFilter,
  setCatalog,
  setCharacterNames,
  setGameBoost,
  startCapture,
  stopCapture,
  updateCapture,
  type GameBoostState,
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
export interface LocalCaster {
  id: number
  name: string
  manualOverrideCleared?: boolean
}
export const localCaster = ref<LocalCaster>({ id: 0, name: '' })
// What the backend last did to Aion2.exe for the game boost toggle.
export const gameBoost = ref<GameBoostState>({ enabled: false, applied: false, pid: 0, detail: '' })
export const effectiveCaster = computed(() => {
  const manual = Number(config.casterRecord)
  return Number.isFinite(manual) && manual > 0 ? manual : Number(localCaster.value.id) || 0
})

// Pick your own caster only as a manual override; 0 returns to auto identity.
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
  const caster = effectiveCaster.value
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

// Wails calls can run concurrently. Keep skill-list and lifecycle writes
// ordered so a delayed older request cannot become the final configuration.
let captureOperations: Promise<void> = Promise.resolve()
function queueCaptureOperation(operation: () => Promise<void>): Promise<void> {
  const next = captureOperations.then(operation)
  captureOperations = next.catch(() => {})
  return next
}

export async function start() {
  errorMsg.value = ''
  if (!captureAvailable()) {
    errorMsg.value = i18n.global.t('ping.backendUnavailable')
    return
  }
  try {
    await queueCaptureOperation(async () => {
      const character = config.character.trim()
      await setCharacterNames(character ? [character] : [])
      await startCapture(buildConfig())
    })
  } catch (e: any) {
    const m = String(e?.message ?? e)
    errorMsg.value = m === 'backend-unavailable' ? i18n.global.t('ping.backendUnavailable') : m
    status.value = 'error'
  }
}
export async function stop() {
  await queueCaptureOperation(() => stopCapture())
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
    if (s === 'idle') {
      localCaster.value = { id: 0, name: '' }
      actCasters.value = []
      config.casterRecord = 0
    }
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
  // 33 36 is the server's authoritative self-identity record. It arrives on
  // login/instance entry, before a combat ACT, and remains unambiguous when the
  // party contains another player of the same class.
  EventsOn('capture:local-caster', (value: LocalCaster) => {
    const previousID = Number(localCaster.value.id) || 0
    const next: LocalCaster = {
      id: Number(value?.id) || 0,
      name: String(value?.name ?? ''),
      manualOverrideCleared: !!value?.manualOverrideCleared,
    }
    if (next.id === 0 || next.manualOverrideCleared || (previousID > 0 && next.id !== previousID)) {
      config.casterRecord = 0
    }
    localCaster.value = next
  })

  // Configured-skill ACT casters populate the manual fallback choices. The
  // backend may also use the first unseen caster after an instance-transition
  // quiet gap when the authoritative identity record was absent.
  EventsOn('capture:act-casters', (list: ActCaster[]) => {
    actCasters.value = Array.isArray(list) ? list : []
  })
  EventsOn('game:status', (v: boolean) => {
    gameDetected.value = !!v
  })
  EventsOn('game:boost', (s: GameBoostState) => {
    gameBoost.value = {
      enabled: !!s?.enabled,
      applied: !!s?.applied,
      pid: Number(s?.pid) || 0,
      detail: String(s?.detail ?? ''),
    }
  })

  await loadConfig()
  if (captureAvailable()) {
    const character = config.character.trim()
    await setCharacterNames(character ? [character] : [])
    setCatalog(skills.map((s) => ({ name: s.name, ids: s.skill_ids }))).catch(() => {})
    running.value = await isCapturing()
    if (running.value) status.value = 'running'
  }

  // Keep the engine in sync with skill-list edits. Queue so a delayed older
  // write cannot become the final configuration.
  watch(
    () => config.rows,
    () => {
      queueCaptureOperation(async () => {
        if (captureAvailable() && running.value) await updateCapture(buildConfig())
      }).catch(() => {})
    },
    { deep: true },
  )

  // The exact character name is a second trusted identity source when capture
  // starts too late to see the server's 33 36 self record. Push it even while
  // idle so the tracker is armed before the first packet after Start.
  watch(
    () => config.character,
    (value) => {
      const character = String(value ?? '').trim()
      if (captureAvailable()) {
        queueCaptureOperation(() => setCharacterNames(character ? [character] : [])).catch(() => {})
      }
    },
  )

  // The game boost follows the saved toggle; the backend applies it whenever
  // Aion2.exe is running and restores the process when it is switched off.
  watch(
    () => config.gameBoost,
    (on) => {
      if (!captureAvailable()) return
      setGameBoost(!!on)
        .then((s) => {
          if (s) gameBoost.value = s
        })
        .catch(() => {})
    },
    { immediate: true },
  )

  // Manual override only. Zero tells the backend to use its auto-detected local
  // identity; a selected ACT caster always takes precedence over auto.
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
