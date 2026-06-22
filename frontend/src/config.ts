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

export interface SavedAuto {
  id: string
  key: string
  delay: number
}
// Screen rectangle (physical pixels) for one skill's icon on the in-game bar.
export interface ScreenRect {
  x: number
  y: number
  w: number
  h: number
}
export interface SavedSequence {
  triggerId: string | null
  triggerRect: ScreenRect | null // where the trigger skill's icon sits on screen
  autos: SavedAuto[]
}
// Screen-reading monitor: each skill has its own rectangle; a skill is "ready"
// when its rectangle's average brightness exceeds `threshold`.
export interface SkillbarConfig {
  threshold: number
}
export interface AutoHotkeyConfig {
  board: string
  sequences: SavedSequence[]
  skillbar: SkillbarConfig
}

// Oversize Network: WinTUN VPN settings. The relay daemon is deployed to the VM
// over SSH (ip/port/user/password); `key` is the tunnel key returned by deploy.
export interface OversizeConfig {
  ip: string
  port: number
  user: string
  password: string
  key: string // tunnel key (hex) from "Deploy server"; required to connect
  fullTunnel: boolean // true = route all traffic; false = only Aion 2 servers (split)
  gameIPs: string[] // learned Aion 2 server IPs; pre-routed on connect in split mode
}

export interface AppConfig {
  language: 'th' | 'en'
  character: string
  defaultSpeed: number
  panelHeight: number
  overlay: boolean // overlay mode: window pinned above the game; UI adapts when on
  devMode: boolean // show developer-only menus (e.g. Packet Inspector)
  casterMask: boolean // FPS mask: rewrite other players' casts to Dodge (dev-only)
  rows: SavedRow[]
  autoHotkey: AutoHotkeyConfig
  oversize: OversizeConfig
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
  panelHeight: 300,
  overlay: false,
  devMode: false,
  casterMask: false,
  rows: [],
  autoHotkey: { board: '', sequences: [], skillbar: { threshold: 60 } },
  oversize: { ip: '104.199.243.60', port: 22, user: 'root', password: '123123Zz', key: '', fullTunnel: true, gameIPs: [] },
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
        // Backfill nested oversize defaults: Object.assign overwrites the whole
        // `oversize` object, so configs saved before a field existed would lack
        // it (e.g. gameIPs), crashing the menu. Defaults first, saved values win.
        config.oversize = {
          ip: '104.199.243.60',
          port: 22,
          user: 'root',
          password: '123123Zz',
          key: '',
          fullTunnel: true,
          gameIPs: [],
          ...(config.oversize as Partial<OversizeConfig>),
        }
        if (!Array.isArray(config.oversize.gameIPs)) config.oversize.gameIPs = []
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
