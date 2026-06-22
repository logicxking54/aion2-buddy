<script lang="ts" setup>
import { onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { EventsOn, setInspect, setComboTest, setDecode } from '../pingmaker/capture'
import { running, start, stop, log } from '../../captureController'

const { t } = useI18n()

const enabled = ref(false)
const paused = ref(false)

// Experimental: redirect a chain skill's "next form" id so the client's combo
// follow-up button changes (Burst → Ice Chain → Pyroclasm). Persists in the
// engine, so it keeps working after leaving this page; toggle off to stop.
const comboOn = ref(false)
function toggleCombo() {
  comboOn.value = !comboOn.value
  setComboTest(comboOn.value).catch(() => {})
}

// Print every decoded field of each cast (skill, tick, caster, position, speed,
// trailing record) to the log.
const decodeOn = ref(false)
function toggleDecode() {
  decodeOn.value = !decodeOn.value
  setDecode(decodeOn.value).catch(() => {})
}

interface Entry {
  time: string
  label: string
  len: number
  offset: number
  opcodes: string[]
  hex: string
}
const entries = reactive<Entry[]>([])

// Capture mode. all=false → only skill packets (inbound casts + outbound skill
// requests). all=true → EVERY inbound/outbound packet — needed to reverse-engineer
// changed packet formats (the cast/speed framing the modify path no longer matches).
const allMode = ref(false)
function apply() {
  setInspect(enabled.value, allMode.value).catch(() => {})
}
function toggleMode() {
  allMode.value = !allMode.value
  if (enabled.value) apply() // hot-swap the backend filter while inspecting
}
// The single Inspect button also drives capture: enabling it starts the shared
// engine if nothing else did, and disabling it stops the engine again — but only
// if WE started it, so it never kills a capture Ping Maker is using.
let startedHere = false
async function toggle() {
  enabled.value = !enabled.value
  if (enabled.value) {
    entries.splice(0, entries.length)
    if (!running.value) {
      await start()
      startedHere = true
    }
  } else if (startedHere) {
    await stop()
    startedHere = false
  }
  apply()
}
function clearList() {
  entries.splice(0, entries.length)
  if (enabled.value) apply() // also resets the backend cap
}

// Export the captured entries as JSON Lines (oldest-first, one packet per line)
// via a native save dialog. JSONL keeps it machine-readable for offline analysis.
const exporting = ref(false)
async function exportFile() {
  if (entries.length === 0 || exporting.value) return
  const backend = (window as any)?.go?.main?.App
  if (!backend?.ExportPackets) {
    log('Export unavailable (backend not ready)', 'warn')
    return
  }
  // entries are stored newest-first; emit chronological order for analysis.
  const lines = entries
    .slice()
    .reverse()
    .map((e) => JSON.stringify(e))
    .join('\n')
  const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
  exporting.value = true
  try {
    const path = await backend.ExportPackets(`packets-${ts}.jsonl`, lines)
    if (path) log(`Exported ${entries.length} packets → ${path}`, 'info')
  } catch (err: any) {
    log('Export failed: ' + String(err?.message ?? err), 'warn')
  } finally {
    exporting.value = false
  }
}

function stamp() {
  return new Date().toTimeString().slice(0, 8)
}
// "aabbcc" -> "aa bb cc"
function spaced(hex: string) {
  return hex.replace(/(..)/g, '$1 ').trimEnd()
}

const unsub = EventsOn(
  'inspector:packet',
  (p: { label: string; len: number; offset: number; opcodes: string[]; hex: string }) => {
    if (paused.value) return
    entries.unshift({
      time: stamp(),
      label: p.label,
      len: p.len ?? 0,
      offset: p.offset ?? -1,
      opcodes: p.opcodes ?? [],
      hex: p.hex ?? '',
    })
    if (entries.length > 500) entries.splice(500)
  },
)

onUnmounted(() => {
  unsub()
  if (enabled.value) setInspect(false, false).catch(() => {}) // stop dumping when leaving
})
</script>

<template>
  <div class="mx-auto flex h-full max-w-5xl flex-col gap-3">
    <!-- Controls -->
    <div class="flex flex-wrap items-center gap-2 rounded-xl border border-white/5 bg-ink-700 px-4 py-2.5">
      <!-- Single Start: drives both capture and inspection (runs on its own). -->
      <button
        type="button"
        @click="toggle"
        class="rounded-md px-4 py-1.5 text-sm font-bold transition"
        :class="enabled ? 'bg-accent text-ink-900 hover:bg-accent-soft' : 'bg-accent/15 text-accent hover:bg-accent/25'"
      >{{ enabled ? '■ ' + t('insp.stop') : '▶ ' + t('insp.enable') }}</button>

      <!-- Capture-mode filter: skill-only vs every packet. -->
      <button
        type="button"
        @click="toggleMode"
        :title="t('insp.allMode')"
        class="rounded-md px-3 py-1 text-xs font-semibold transition"
        :class="allMode ? 'bg-violet-500/20 text-violet-300 ring-1 ring-violet-400/30' : 'bg-white/5 text-slate-300 hover:bg-white/10'"
      >{{ allMode ? '● ' + t('insp.allMode') : t('insp.skillMode') }}</button>

      <button
        type="button"
        @click="toggleCombo"
        title="Test: redirect chain next-id Burst → Ice Chain → Pyroclasm (changes the combo follow-up button)"
        class="rounded-md px-3 py-1 text-xs font-semibold transition"
        :class="comboOn ? 'bg-amber-500/20 text-amber-300 ring-1 ring-amber-400/30' : 'bg-white/5 text-slate-300 hover:bg-white/10'"
      >{{ comboOn ? '● Combo Test ON' : 'Combo Test' }}</button>

      <button
        type="button"
        @click="toggleDecode"
        title="Print every decoded field of each cast (skill, tick, caster, position, speed, trailing record) to the log"
        class="rounded-md px-3 py-1 text-xs font-semibold transition"
        :class="decodeOn ? 'bg-sky-500/20 text-sky-300 ring-1 ring-sky-400/30' : 'bg-white/5 text-slate-300 hover:bg-white/10'"
      >{{ decodeOn ? '● Decode ON' : 'Decode' }}</button>

      <button
        type="button"
        @click="paused = !paused"
        class="rounded-md bg-white/5 px-3 py-1 text-xs font-semibold text-slate-300 transition hover:bg-white/10"
      >{{ paused ? t('insp.resume') : t('insp.pause') }}</button>
      <button
        type="button"
        @click="clearList"
        class="rounded-md bg-white/5 px-3 py-1 text-xs font-semibold text-slate-300 transition hover:bg-white/10"
      >{{ t('insp.clear') }}</button>
      <button
        type="button"
        @click="exportFile"
        :disabled="entries.length === 0 || exporting"
        class="rounded-md bg-white/5 px-3 py-1 text-xs font-semibold text-slate-300 transition hover:bg-white/10 disabled:opacity-40"
      >{{ exporting ? t('insp.exporting') : t('insp.export') }}</button>

      <span class="ml-auto text-[11px] text-slate-500">{{ entries.length }} {{ t('insp.captured') }}</span>
    </div>

    <p v-if="enabled && !running" class="rounded-lg border border-amber-400/30 bg-amber-400/10 px-3 py-2 text-xs font-semibold text-amber-300">
      {{ t('insp.needCapture') }}
    </p>

    <!-- Message list -->
    <div class="min-h-0 flex-1 overflow-y-auto rounded-xl border border-white/5 bg-ink-900 p-3">
      <p v-if="entries.length === 0" class="text-sm text-slate-600">{{ t('insp.empty') }}</p>
      <div
        v-for="(e, i) in entries"
        :key="i"
        class="mb-2 rounded-lg border border-white/5 bg-ink-800 p-2.5"
      >
        <div class="flex flex-wrap items-center gap-2 text-xs">
          <span class="font-mono text-slate-600">{{ e.time }}</span>
          <span class="font-semibold text-accent-soft">{{ e.label }}</span>
          <span class="text-slate-500">{{ t('insp.len') }} {{ e.len }}</span>
          <span v-if="e.offset >= 0" class="text-slate-500">@ {{ e.offset }}</span>
          <span
            v-for="op in e.opcodes"
            :key="op"
            class="rounded bg-accent/10 px-1.5 py-0.5 text-[10px] font-bold text-accent"
          >{{ op }}</span>
        </div>
        <div class="mt-1.5 select-text break-all font-mono text-[11px] leading-relaxed text-slate-300">{{ spaced(e.hex) }}</div>
      </div>
    </div>
  </div>
</template>
