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
  language: 'th' | 'en' | 'zh'
  character: string
  defaultSpeed: number
  casterRecord: number // auto-detected caster-filter entity key — RUNTIME ONLY, never persisted (changes every session)
  panelHeight: number
  devMode: boolean // show developer-only menus (e.g. Packet Inspector)
  casterMask: boolean // FPS mask: rewrite other players' casts to Dodge (dev-only)
  animMask: boolean // disable skill anims (except mine): rewrite others' non-edit-list casts to a no-anim skill
  rows: SavedRow[]
}

// Seed defaults from the legacy localStorage keys so existing settings migrate
// into the file on first run.
function initialLanguage(): 'th' | 'en' | 'zh' {
  const v = localStorage.getItem('locale')
  return v === 'en' || v === 'th' || v === 'zh' ? v : 'th'
}

export const config = reactive<AppConfig>({
  language: initialLanguage(),
  character: localStorage.getItem('aion2-character') ?? '',
  defaultSpeed: 250,
  casterRecord: 0,
  panelHeight: 300,
  devMode: false,
  casterMask: false,
  animMask: false,
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
        config.casterRecord = 0 // never restore a persisted caster; it's per-session
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
    // casterRecord is auto-detected per session — strip it so it's never persisted.
    const { casterRecord: _drop, ...persist } = config
    if (a) a.SaveConfig(JSON.stringify(persist)).catch(() => {})
  }, 300)
}
