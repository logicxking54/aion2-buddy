// Single source of truth for all persisted app settings, saved as one JSON
// file via the Go backend (App.SaveConfig/LoadConfig → %APPDATA%\aion2-buddy
// \skills-config.json). Components read/write `config` and call saveConfig().
import { reactive } from 'vue'

export interface SavedRow {
  id: string
  speedPct: number
  overridden: boolean
  brk: boolean
}

export interface AppConfig {
  language: 'th' | 'en'
  character: string
  defaultSpeed: number
  casterRecord: number // manual caster-filter entity key (0 = all casters)
  panelHeight: number
  overlay: boolean // overlay mode: window pinned above the game; UI adapts when on
  devMode: boolean // show developer-only menus (e.g. Packet Inspector)
  casterMask: boolean // FPS mask: rewrite other players' casts to Dodge (dev-only)
  rows: SavedRow[]
}

// Seed defaults from the legacy localStorage keys so existing settings migrate
// into the file on first run.
function initialLanguage(): 'th' | 'en' {
  const v = localStorage.getItem('locale')
  return v === 'en' || v === 'th' ? v : 'th'
}

export const config = reactive<AppConfig>({
  language: initialLanguage(),
  character: localStorage.getItem('aion2-character') ?? '',
  defaultSpeed: 250,
  casterRecord: 0,
  panelHeight: 300,
  overlay: false,
  devMode: false,
  casterMask: false,
  rows: [],
})

interface AppBackend {
  SaveConfig(data: string): Promise<void>
  LoadConfig(): Promise<string>
}
function appBackend(): AppBackend | undefined {
  return (window as any)?.go?.main?.App
}

let ready = false
let loadPromise: Promise<void> | null = null

// loadConfig reads the file once; concurrent callers share the same promise.
export function loadConfig(): Promise<void> {
  if (!loadPromise) loadPromise = doLoad()
  return loadPromise
}

async function doLoad(): Promise<void> {
  const a = appBackend()
  if (a) {
    try {
      const raw = await a.LoadConfig()
      if (raw) {
        const data = JSON.parse(raw)
        if (data && typeof data === 'object') Object.assign(config, data)
        if (typeof config.casterRecord !== 'number') config.casterRecord = 0
      }
    } catch {
      // ignore malformed config
    }
  }
  ready = true
}

let saveTimer: ReturnType<typeof setTimeout> | undefined

// saveConfig writes the whole config (debounced). No-op until the initial load
// has completed, so we never clobber the file with defaults.
export function saveConfig(): void {
  if (!ready) return
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = setTimeout(() => {
    const a = appBackend()
    if (a) a.SaveConfig(JSON.stringify(config)).catch(() => {})
  }, 300)
}
