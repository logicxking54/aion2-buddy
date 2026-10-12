<script lang="ts" setup>
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import CaptureBar from './components/CaptureBar.vue'
import TitleBar from './components/TitleBar.vue'
import PingMaker from './projects/pingmaker/PingMaker.vue'
import { config, loadConfig } from './config'
import { gameDetected, initCapture, running, start, stop } from './captureController'
import { setLocale, type Locale } from './i18n'

const { t, locale } = useI18n()
const appVersion = __APP_VERSION__

onMounted(async () => {
  await loadConfig()
  setLocale(config.language)
  await initCapture()
})
</script>

<template>
  <div class="flex h-screen w-screen flex-col overflow-hidden bg-ink-800 font-sans text-slate-200">
    <TitleBar />
    <header class="flex shrink-0 flex-wrap items-center gap-3 border-b border-white/5 bg-ink-900/60 px-4 py-3">
      <h1 class="text-lg font-bold text-white">{{ t('menus.pingmaker.label') }}</h1>
      <span class="text-xs text-slate-500">v{{ appVersion }}</span>
      <div class="ml-auto flex items-center gap-3">
        <select
          :value="locale"
          @change="setLocale(($event.target as HTMLSelectElement).value as Locale)"
          :aria-label="t('common.language')"
          class="rounded-md border border-white/10 bg-ink-900 px-2 py-1.5 text-xs text-slate-300 outline-none focus:border-accent/60"
        >
          <option value="th">ไทย</option>
          <option value="en">English</option>
          <option value="zh">繁體中文</option>
        </select>
        <button
          v-if="!running"
          type="button"
          @click="start"
          class="flex h-9 shrink-0 items-center gap-2 rounded-md bg-accent px-4 text-sm font-bold text-ink-900 transition hover:bg-accent-soft"
        >▶ {{ t('ping.start') }}</button>
        <button
          v-else
          type="button"
          @click="stop"
          class="flex h-9 shrink-0 items-center gap-2 rounded-md bg-red-500 px-4 text-sm font-bold text-white transition hover:bg-red-400"
        >■ {{ t('ping.stop') }}</button>
      </div>
      <div class="flex w-full items-center gap-2 text-xs" :class="gameDetected ? 'text-emerald-300' : 'text-slate-400'">
        <span class="h-2 w-2 shrink-0 rounded-full" :class="gameDetected ? 'bg-emerald-400' : 'bg-amber-400'" />
        {{ gameDetected ? t('app.detected') : t('app.notDetected') }}
      </div>
    </header>
    <main class="min-h-0 flex-1 overflow-y-auto p-4">
      <PingMaker />
    </main>
    <CaptureBar />
  </div>
</template>
