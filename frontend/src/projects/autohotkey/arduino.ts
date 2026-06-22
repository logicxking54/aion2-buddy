// Defensive wrappers around the Go App / Arduino bindings.
function app() {
  return (window as any)?.go?.main?.App
}
function arduino() {
  return (window as any)?.go?.main?.Arduino
}

export async function listSerialPorts(): Promise<string[]> {
  const a = app()
  if (!a?.ListSerialPorts) return []
  try {
    return (await a.ListSerialPorts()) ?? []
  } catch {
    return []
  }
}

export async function connectBoard(port: string): Promise<void> {
  const a = arduino()
  if (!a) throw new Error('backend-unavailable')
  await a.ConnectBoard(port)
}

export async function disconnectBoard(): Promise<void> {
  const a = arduino()
  if (a) await a.DisconnectBoard()
}

export async function boardConnected(): Promise<boolean> {
  const a = arduino()
  return a ? a.BoardConnected() : false
}

export async function detectPort(): Promise<string> {
  const a = arduino()
  if (!a?.DetectPort) return ''
  try {
    return (await a.DetectPort()) ?? ''
  } catch {
    return ''
  }
}

// autoSelectPort scans for the board (logging diagnostics to the app log) and
// returns the chosen COM port, or ''.
export async function autoSelectPort(): Promise<string> {
  const a = arduino()
  if (!a?.AutoSelect) return ''
  try {
    return (await a.AutoSelect()) ?? ''
  } catch {
    return ''
  }
}

export async function currentPort(): Promise<string> {
  const a = arduino()
  if (!a?.CurrentPort) return ''
  try {
    return (await a.CurrentPort()) ?? ''
  } catch {
    return ''
  }
}

export async function sendKey(token: string): Promise<void> {
  const a = arduino()
  if (a) await a.SendKey(token)
}
