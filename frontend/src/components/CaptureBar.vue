<script lang="ts" setup>
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { config, loadConfig, saveConfig } from '../config'
import { clearLogs, errorMsg, initCapture, logs, running } from '../captureController'
import { setCasterMask } from '../projects/pingmaker/capture'

const { t } = useI18n()
const logBox = ref<HTMLElement | null>(null)

// ── FPS mask ────────────────────────────────────────────────────────────
// When on, the engine rewrites every OTHER player's cast skill_id to a no-VFX
// "Dodge" skill for higher FPS in crowded fights. Hidden for now; kept here so the
// toggle still wires up if re-enabled.
const DODGE_SKILL_ID = 15000100 // one fixed Dodge id for everyone (near-invisible)

function pushMask() {
  if (!running.value) return // engine isn't capturing; nothing to mask
  setCasterMask(config.casterMask, 0, DODGE_SKILL_ID).catch(() => {})
}
watch([() => config.casterMask, running], pushMask, { immediate: true })

function toggleMask() {
  config.casterMask = !config.casterMask
  saveConfig()
}

// ── Resizable panel height ──────────────────────────────────────────────
const panelHeight = ref(300)
let startY = 0
let startH = 0

function onDrag(e: MouseEvent) {
  const dy = startY - e.clientY // drag up → taller
  const max = Math.max(160, window.innerHeight - 200)
  panelHeight.value = Math.min(max, Math.max(120, startH + dy))
}
function endDrag() {
  window.removeEventListener('mousemove', onDrag)
  window.removeEventListener('mouseup', endDrag)
  document.body.style.userSelect = ''
  config.panelHeight = Math.round(panelHeight.value)
  saveConfig()
}
function startDrag(e: MouseEvent) {
  startY = e.clientY
  startH = panelHeight.value
  document.body.style.userSelect = 'none'
  window.addEventListener('mousemove', onDrag)
  window.addEventListener('mouseup', endDrag)
  e.preventDefault()
}

// Auto-scroll the log to the newest line. Watch the newest entry's identity (not
// the length): once the log hits its line cap, each new line pushes one and trims
// one, so the length stays constant and a length watcher would stop firing — but
// the last element is a fresh object on every append.
watch(
  () => logs[logs.length - 1],
  () => {
    nextTick(() => {
      if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
    })
  },
)

onMounted(async () => {
  initCapture()
  await loadConfig()
  if (config.panelHeight) panelHeight.value = config.panelHeight
})
onUnmounted(endDrag)
</script>

<template>
  <div
    class="flex shrink-0 flex-col border-t border-white/5 bg-ink-900/60"
    :style="{ height: panelHeight + 'px' }"
  >
    <!-- Drag handle to resize the panel -->
    <div
      class="group flex h-2.5 shrink-0 cursor-row-resize items-center justify-center"
      @mousedown="startDrag"
    >
      <div class="h-0.5 w-10 rounded-full bg-white/15 transition group-hover:bg-accent/70" />
    </div>

    <div class="flex min-h-0 flex-1 flex-col px-4 pb-3">
      <div class="flex flex-wrap items-center gap-3">
        <!-- FPS mask toggle hidden for now (logic kept intact; re-enable by removing v-if="false"). -->
        <button
          v-if="config.devMode && false"
          type="button"
          @click="toggleMask"
          :title="t('ping.fpsMaskHint')"
          class="rounded-md px-3 py-1 text-xs font-semibold transition"
          :class="config.casterMask ? 'bg-amber-500/20 text-amber-300 ring-1 ring-amber-400/30' : 'bg-white/5 text-slate-300 hover:bg-white/10'"
        >{{ config.casterMask ? '● ' + t('ping.fpsMaskOn') : t('ping.fpsMask') }}</button>
        <button
          type="button"
          @click="clearLogs"
          :disabled="logs.length === 0"
          class="ml-auto rounded-md px-2.5 py-1 text-xs font-semibold text-slate-400 transition hover:bg-white/5 hover:text-white disabled:opacity-30"
        >{{ t('ping.clearLogs') }}</button>
      </div>

      <!-- Error banner -->
      <div
        v-if="errorMsg"
        class="mt-2 rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs font-semibold text-red-300"
      >⚠ {{ errorMsg }}</div>

      <!-- Log box (fills the remaining panel height) -->
      <div
        ref="logBox"
        class="mt-2 min-h-0 flex-1 overflow-y-auto rounded-lg border border-white/5 bg-ink-900 p-3 font-mono text-xs leading-relaxed"
      >
        <p v-if="logs.length === 0" class="text-slate-600">{{ t('ping.logsEmpty') }}</p>
        <div v-for="(entry, i) in logs" :key="i" class="flex gap-2">
          <span class="shrink-0 text-slate-600">{{ entry.time }}</span>
          <span
            :class="{
              'text-slate-300': entry.kind === 'info',
              'text-accent-soft': entry.kind === 'cast',
              'text-amber-400': entry.kind === 'warn',
            }"
          >{{ entry.msg }}</span>
        </div>
      </div>
    </div>
  </div>
</template>
