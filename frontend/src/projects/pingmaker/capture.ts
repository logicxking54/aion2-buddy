// Typed access to the Ping Maker Wails backend; a browser preview has no capture.
import { EventsOn } from '../../../wailsjs/runtime/runtime'

export interface SkillSpeed {
  name: string
  ids: number[]
  primaryIds: number[]
  speedPct: number
  break: boolean
  override: boolean
}

export interface CatalogEntry {
  name: string
  ids: number[]
}

// Mirrors capture.GameBoostState: what the backend did to Aion2.exe.
export interface GameBoostState {
  enabled: boolean
  applied: boolean
  pid: number
  detail: string
}

interface CaptureBackend {
  StartCapture(skills: SkillSpeed[]): Promise<void>
  StopCapture(): Promise<void>
  UpdateCapture(skills: SkillSpeed[]): Promise<void>
  SetCatalog(entries: CatalogEntry[]): Promise<void>
  SetCharacterNames(names: string[]): Promise<void>
  SetCasterFilter(id: number): Promise<void>
  SetGameBoost(enabled: boolean): Promise<GameBoostState>
  IsCapturing(): Promise<boolean>
}

function backend(): CaptureBackend | undefined {
  return (window as any)?.go?.main?.Capture
}

export function captureAvailable(): boolean {
  return !!backend()
}

export async function startCapture(skills: SkillSpeed[]): Promise<void> {
  const b = backend()
  if (!b) throw new Error('backend-unavailable')
  await b.StartCapture(skills)
}

export async function stopCapture(): Promise<void> {
  const b = backend()
  if (b) await b.StopCapture()
}

export async function updateCapture(skills: SkillSpeed[]): Promise<void> {
  const b = backend()
  if (b) await b.UpdateCapture(skills)
}

export async function setCatalog(entries: CatalogEntry[]): Promise<void> {
  const b = backend()
  if (b) await b.SetCatalog(entries)
}

export async function setCharacterNames(names: string[]): Promise<void> {
  const b = backend()
  if (b) await b.SetCharacterNames(names)
}

export async function setCasterFilter(id: number): Promise<void> {
  const b = backend()
  if (b) await b.SetCasterFilter(id)
}

export async function setGameBoost(enabled: boolean): Promise<GameBoostState | undefined> {
  const b = backend()
  return b ? b.SetGameBoost(enabled) : undefined
}

export async function isCapturing(): Promise<boolean> {
  const b = backend()
  return b ? b.IsCapturing() : false
}

export { EventsOn }
