<script lang="ts" setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import CaptureBar from './components/CaptureBar.vue'
import ChangelogModal from './components/ChangelogModal.vue'
import Overlay from './components/Overlay.vue'
import Sidebar from './components/Sidebar.vue'
import TitleBar from './components/TitleBar.vue'
import { config, loadConfig } from './config'
import { initCapture, running, start, stop } from './captureController'
import { setLocale } from './i18n'
import { projects } from './projects/registry'
import { WindowSetAlwaysOnTop, WindowSetSize } from '../wailsjs/runtime/runtime'

const { t } = useI18n()

const activeId = ref('pingmaker')
// Dev-only menus (e.g. Packet Inspector) are hidden unless Development mode is on.
const visibleProjects = computed(() => projects.filter((p) => !p.dev || config.devMode))
const active = computed(() => visibleProjects.value.find((p) => p.id === activeId.value) ?? visibleProjects.value[0])

function select(id: string) {
  activeId.value = id
}

// If the active menu gets hidden (dev mode turned off while on it), fall back.
watch(
  () => config.devMode,
  () => {
    if (!visibleProjects.value.some((p) => p.id === activeId.value)) activeId.value = 'pingmaker'
  },
)

// Overlay mode shrinks the window to a small always-on-top HUD; off restores it.
const NORMAL_W = 850
const NORMAL_H = 1000
const OVERLAY_W = 392
const OVERLAY_H = 240
function applyOverlay(on: boolean) {
  WindowSetAlwaysOnTop(on)
  WindowSetSize(on ? OVERLAY_W : NORMAL_W, on ? OVERLAY_H : NORMAL_H)
}
watch(() => config.overlay, (on) => applyOverlay(on))

// Load settings, wire capture events (so the overlay works even if we launch
// straight into it), and apply the persisted language + overlay mode.
onMounted(async () => {
  await loadConfig()
  setLocale(config.language)
  initCapture()
  if (config.overlay) applyOverlay(true)
})
</script>

<template>
  <Overlay v-if="config.overlay" />
  <div v-else class="flex h-screen w-screen flex-col overflow-hidden bg-ink-800 font-sans text-slate-200">
    <TitleBar />

    <div class="flex min-h-0 flex-1">
      <Sidebar :projects="visibleProjects" :active-id="activeId" @select="select" />

      <main class="flex min-w-0 flex-1 flex-col">
      <!-- Top bar -->
      <header class="flex items-center justify-between gap-3 border-b border-white/5 bg-ink-900/60 px-5 py-3">
        <div class="min-w-0">
          <h1 class="flex items-center gap-2 text-lg font-bold text-white">
            <span>{{ active.icon }}</span>{{ t(`menus.${active.id}.label`) }}
          </h1>
          <p class="truncate text-sm text-slate-400">{{ t(`menus.${active.id}.description`) }}</p>
        </div>
        <!-- Capture start/stop — only on the Ping Maker menu -->
        <template v-if="active.id === 'pingmaker'">
          <button
            v-if="!running"
            type="button"
            @click="start"
            class="flex shrink-0 items-center gap-2 rounded-lg bg-accent px-5 py-2 text-sm font-bold text-ink-900 transition hover:bg-accent-soft"
          >▶ {{ t('ping.start') }}</button>
          <button
            v-else
            type="button"
            @click="stop"
            class="flex shrink-0 items-center gap-2 rounded-lg bg-red-500 px-5 py-2 text-sm font-bold text-white transition hover:bg-red-400"
          >■ {{ t('ping.stop') }}</button>
        </template>
      </header>

      <!-- Active menu content -->
      <section class="min-h-0 flex-1 overflow-y-auto p-4">
        <component :is="active.component" v-if="active.component" />
        <div v-else class="grid h-full place-items-center text-slate-500">
          <div class="text-center">
            <div class="text-5xl">{{ active.icon }}</div>
            <p class="mt-3 font-semibold text-slate-300">{{ t(`menus.${active.id}.label`) }}</p>
            <p class="text-sm">{{ t('common.comingSoon') }}</p>
          </div>
        </div>
      </section>

      <!-- Shared capture controls + log, shown under every menu -->
      <CaptureBar />
      </main>
    </div>

    <!-- "What's New" popup — shows once after each version bump -->
    <ChangelogModal />
  </div>
</template>
