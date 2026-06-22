// Defensive wrappers around the Go Screen binding (per-skill screen reader).
function screen() {
  return (window as any)?.go?.main?.Screen
}

export interface SlotState {
  i: number
  brightness: number // 0..255 average luminance
  ready: boolean
}

export interface BarRect {
  x: number
  y: number
  w: number
  h: number
}

// sampleRects returns the average brightness (0..255) of each skill rectangle.
export async function sampleRects(rects: BarRect[]): Promise<number[]> {
  const s = screen()
  if (!s?.SampleRects) return []
  try {
    return (await s.SampleRects(rects)) ?? []
  } catch {
    return []
  }
}

// captureRectPreview returns a base64 PNG data URL of one skill rectangle.
export async function captureRectPreview(r: BarRect): Promise<string> {
  const s = screen()
  if (!s?.CaptureRectPreview) return ''
  try {
    return (await s.CaptureRectPreview(r.x, r.y, r.w, r.h)) ?? ''
  } catch {
    return ''
  }
}

export async function startSkillbar(rects: BarRect[], threshold: number, intervalMs: number): Promise<void> {
  const s = screen()
  if (s?.StartSkillbar) await s.StartSkillbar(rects, threshold, intervalMs)
}

export async function stopSkillbar(): Promise<void> {
  const s = screen()
  if (s?.StopSkillbar) await s.StopSkillbar()
}

export async function skillbarRunning(): Promise<boolean> {
  const s = screen()
  return s?.SkillbarRunning ? s.SkillbarRunning() : false
}
