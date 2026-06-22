<script lang="ts" setup>
import { useI18n } from 'vue-i18n'
import { setLocale, type Locale } from '../../i18n'
import { config, saveConfig } from '../../config'

const { t, locale } = useI18n()

const languages: { id: Locale; native: string; sub: string }[] = [
  { id: 'th', native: 'ไทย', sub: 'Thai' },
  { id: 'en', native: 'English', sub: 'EN' },
]

function toggleDev() {
  config.devMode = !config.devMode
  saveConfig()
}
</script>

<template>
  <div class="mx-auto max-w-2xl space-y-4">
    <!-- Language -->
    <section class="rounded-xl border border-white/5 bg-ink-700 p-5">
      <h2 class="text-sm font-bold uppercase tracking-wider text-slate-400">{{ t('settings.language') }}</h2>
      <p class="mt-1 text-sm text-slate-500">{{ t('settings.languageHint') }}</p>

      <div class="mt-4 grid gap-3 sm:grid-cols-2">
        <button
          v-for="l in languages"
          :key="l.id"
          type="button"
          @click="setLocale(l.id)"
          class="flex items-center gap-3 rounded-lg border p-3 text-left transition"
          :class="locale === l.id
            ? 'border-accent/50 bg-accent/10 ring-1 ring-accent/30'
            : 'border-white/10 bg-ink-800 hover:border-white/20'"
        >
          <span class="text-2xl">{{ l.id === 'th' ? '🇹🇭' : '🇬🇧' }}</span>
          <span class="flex-1">
            <span class="block text-sm font-bold text-white">{{ l.native }}</span>
            <span class="block text-xs text-slate-400">{{ l.sub }}</span>
          </span>
          <span
            v-if="locale === l.id"
            class="grid h-5 w-5 place-items-center rounded-full bg-accent text-xs font-bold text-ink-900"
          >✓</span>
        </button>
      </div>
    </section>

    <!-- Development mode -->
    <section class="rounded-xl border border-white/5 bg-ink-700 p-5">
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-sm font-bold uppercase tracking-wider text-slate-400">{{ t('settings.devMode') }}</h2>
          <p class="mt-1 text-sm text-slate-500">{{ t('settings.devModeHint') }}</p>
        </div>
        <button
          type="button"
          role="switch"
          :aria-checked="config.devMode"
          @click="toggleDev"
          class="relative inline-block h-6 w-11 shrink-0 cursor-pointer rounded-full transition-colors"
          :class="config.devMode ? 'bg-accent' : 'bg-white/20'"
          aria-label="Toggle development mode"
        >
          <span
            class="absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all"
            :class="config.devMode ? 'left-[22px]' : 'left-0.5'"
          />
        </button>
      </div>
    </section>
  </div>
</template>
