// Thin typed wrapper around the Wails-bound Capture backend + event bus.
// The bound methods live on window.go.main.Capture at runtime; we access them
// defensively so the UI still type-checks (and degrades gracefully in a plain
// browser where the backend is absent).
import { EventsOn } from '../../../wailsjs/runtime/runtime'

export interface SkillSpeed {
  name: string
  ids: number[]
  primaryIds: number[] // this row's own tier ids; override the expanded fallback
  speedPct: number
  break: boolean
  override: boolean
}

export interface CatalogEntry {
  name: string
  ids: number[]
}

interface CaptureBackend {
  StartCapture(skills: SkillSpeed[]): Promise<void>
  StopCapture(): Promise<void>
  UpdateCapture(skills: SkillSpeed[]): Promise<void>
  SetCatalog(entries: CatalogEntry[]): Promise<void>
  SetCharacterNames(names: string[]): Promise<void>
  SetInspect(on: boolean, all: boolean): Promise<void>
  SetDecode(on: boolean): Promise<void>
  SetSessionRecord(on: boolean): Promise<string>
  SetCasterMask(on: boolean, keepCaster: number, dodgeID: number): Promise<void>
  SetCasterFilter(id: number): Promise<void>
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

export async function setInspect(on: boolean, all: boolean): Promise<void> {
  const b = backend()
  if (b) await b.SetInspect(on, all)
}

export async function setDecode(on: boolean): Promise<void> {
  const b = backend()
  if (b) await b.SetDecode(on)
}

// setSessionRecord toggles raw-packet recording of the whole session to a JSONL
// file. Returns the file path when starting (empty string otherwise).
export async function setSessionRecord(on: boolean): Promise<string> {
  const b = backend()
  if (!b) return ''
  return await b.SetSessionRecord(on)
}

export async function setCasterMask(on: boolean, keepCaster: number, dodgeID: number): Promise<void> {
  const b = backend()
  if (b) await b.SetCasterMask(on, keepCaster, dodgeID)
}

export async function setCasterFilter(id: number): Promise<void> {
  const b = backend()
  if (b) await b.SetCasterFilter(id)
}

export async function isCapturing(): Promise<boolean> {
  const b = backend()
  return b ? b.IsCapturing() : false
}

export { EventsOn }
