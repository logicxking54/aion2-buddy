<script lang="ts" setup>
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

// Backend status for the intro mod (mirrors gamemod.Status in Go). The game
// folder is auto-detected (no path to type), and detection works whether or not
// the game is running — the mod is meant to be applied before launch.
interface IntroStatus {
  moviesDir: string
  found: boolean
  removed: boolean
}

// Status for the reversible system tweaks (mirrors gamemod.TweakStatus in Go).
interface TweakStatus {
  applied: boolean
  detail: string
}

interface ModBackend {
  IntroStatus(): Promise<IntroStatus>
  RemoveIntro(): Promise<IntroStatus>
  RestoreIntro(): Promise<IntroStatus>
  TCPStatus(): Promise<TweakStatus>
  ApplyTCP(): Promise<TweakStatus>
  RevertTCP(): Promise<TweakStatus>
  NICStatus(): Promise<TweakStatus>
  ApplyNIC(): Promise<TweakStatus>
  RevertNIC(): Promise<TweakStatus>
}
function backend(): ModBackend | undefined {
  return (window as any)?.go?.main?.Mod
}
const available = !!backend()
const error = ref('')

// --- Intro mod (game file swap) --------------------------------------------
const introEnabled = ref(false) // intro currently removed
const introFound = ref(false) // game folder located
const moviesDir = ref('')
const introBusy = ref(false)

async function refreshIntro() {
  const b = backend()
  if (!b) return
  try {
    const s = await b.IntroStatus()
    introFound.value = !!s.found
    moviesDir.value = s.moviesDir ?? ''
    introEnabled.value = !!s.removed
  } catch (e: any) {
    error.value = String(e?.message ?? e)
  }
}

async function toggleIntro() {
  const b = backend()
  if (!b || introBusy.value) return
  introBusy.value = true
  error.value = ''
  try {
    const s = introEnabled.value ? await b.RestoreIntro() : await b.RemoveIntro()
    introFound.value = !!s.found
    moviesDir.value = s.moviesDir ?? ''
    introEnabled.value = !!s.removed
  } catch (e: any) {
    error.value = String(e?.message ?? e)
    await refreshIntro() // re-sync the toggle with the real on-disk state
  } finally {
    introBusy.value = false
  }
}

// --- System tweaks (TCP / NIC), data-driven --------------------------------
// Each entry maps a card to its backend status/apply/revert method names so the
// toggle logic is shared. `applied` mirrors the on-disk backup state.
interface SysMod {
  key: string // i18n key prefix under `mod`
  status: keyof ModBackend
  apply: keyof ModBackend
  revert: keyof ModBackend
  applied: boolean
  detail: string
  busy: boolean
}
const sysMods = reactive<SysMod[]>([
  { key: 'tcp', status: 'TCPStatus', apply: 'ApplyTCP', revert: 'RevertTCP', applied: false, detail: '', busy: false },
  { key: 'nic', status: 'NICStatus', apply: 'ApplyNIC', revert: 'RevertNIC', applied: false, detail: '', busy: false },
])

async function refreshSys(m: SysMod) {
  const b = backend()
  if (!b) return
  try {
    const s = (await (b[m.status] as () => Promise<TweakStatus>)()) ?? { applied: false, detail: '' }
    m.applied = !!s.applied
    m.detail = s.detail ?? ''
  } catch (e: any) {
    error.value = String(e?.message ?? e)
  }
}

async function toggleSys(m: SysMod) {
  const b = backend()
  if (!b || m.busy) return
  m.busy = true
  error.value = ''
  try {
    const fn = (m.applied ? b[m.revert] : b[m.apply]) as () => Promise<TweakStatus>
    const s = await fn()
    m.applied = !!s.applied
    m.detail = s.detail ?? ''
  } catch (e: any) {
    error.value = String(e?.message ?? e)
    await refreshSys(m) // re-sync the toggle with the real applied state
  } finally {
    m.busy = false
  }
}

onMounted(() => {
  refreshIntro()
  for (const m of sysMods) refreshSys(m)
})
</script>

<template>
  <div class="mx-auto flex h-full max-w-3xl flex-col gap-3">
    <ul class="space-y-3">
      <!-- Remove Intro screen (game file) -->
      <li
        class="flex items-center gap-4 rounded-xl border bg-ink-700 p-4 transition"
        :class="introEnabled ? 'border-accent/40' : 'border-white/5'"
      >
        <div class="min-w-0 flex-1">
          <div class="text-sm font-bold text-white">{{ t('mod.introTitle') }}</div>
          <div class="text-xs text-slate-400">{{ t('mod.introDesc') }}</div>
          <div class="mt-1 truncate text-[11px]" :class="introFound ? 'text-slate-500' : 'text-amber-400'">
            <template v-if="!available">{{ t('mod.backendUnavailable') }}</template>
            <template v-else-if="introFound">📁 {{ moviesDir }}</template>
            <template v-else>{{ t('mod.notFound') }}</template>
          </div>
        </div>
        <button
          type="button"
          role="switch"
          :aria-checked="introEnabled"
          :disabled="!available || !introFound || introBusy"
          @click="toggleIntro"
          class="relative h-6 w-11 shrink-0 rounded-full transition disabled:cursor-not-allowed disabled:opacity-40"
          :class="introEnabled ? 'bg-accent' : 'bg-slate-600'"
        >
          <span
            class="absolute top-0.5 h-5 w-5 rounded-full bg-white transition-all"
            :class="introEnabled ? 'left-[22px]' : 'left-0.5'"
          />
        </button>
      </li>

      <!-- System tweaks (TCP latency, NIC tuning) -->
      <li
        v-for="m in sysMods"
        :key="m.key"
        class="flex items-center gap-4 rounded-xl border bg-ink-700 p-4 transition"
        :class="m.applied ? 'border-accent/40' : 'border-white/5'"
      >
        <div class="min-w-0 flex-1">
          <div class="text-sm font-bold text-white">{{ t(`mod.${m.key}Title`) }}</div>
          <div class="text-xs text-slate-400">{{ t(`mod.${m.key}Desc`) }}</div>
          <div class="mt-1 truncate text-[11px] text-slate-500">
            <template v-if="!available">{{ t('mod.backendUnavailable') }}</template>
            <template v-else>🛠 {{ t('mod.adminNote') }}<span v-if="m.detail"> · {{ m.detail }}</span></template>
          </div>
        </div>
        <button
          type="button"
          role="switch"
          :aria-checked="m.applied"
          :disabled="!available || m.busy"
          @click="toggleSys(m)"
          class="relative h-6 w-11 shrink-0 rounded-full transition disabled:cursor-not-allowed disabled:opacity-40"
          :class="m.applied ? 'bg-accent' : 'bg-slate-600'"
        >
          <span
            class="absolute top-0.5 h-5 w-5 rounded-full bg-white transition-all"
            :class="m.applied ? 'left-[22px]' : 'left-0.5'"
          />
        </button>
      </li>
    </ul>

    <div
      v-if="error"
      class="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs font-semibold text-red-300"
    >⚠ {{ error }}</div>
  </div>
</template>
