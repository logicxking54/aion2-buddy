<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { gameDetected } from '../captureController'
import type { ProjectMenu } from '../projects/registry'
import logo from '../assets/images/app-logo.png'

const props = defineProps<{
  projects: ProjectMenu[]
  activeId: string
}>()

const emit = defineEmits<{ (e: 'select', id: string): void }>()

const { t } = useI18n()

const appVersion = __APP_VERSION__

const mainMenus = computed(() => props.projects.filter((p) => !p.pinBottom))
const bottomMenus = computed(() => props.projects.filter((p) => p.pinBottom))

function btnClass(p: ProjectMenu) {
  return [
    p.id === props.activeId
      ? 'bg-accent/15 text-white ring-1 ring-accent/30'
      : 'text-slate-300 hover:bg-white/5 hover:text-white',
    p.comingSoon ? 'cursor-not-allowed opacity-40 hover:bg-transparent hover:text-slate-300' : '',
  ]
}
</script>

<template>
  <aside class="flex h-full w-60 shrink-0 flex-col border-r border-white/5 bg-ink-900">
    <!-- Brand -->
    <div class="flex items-center gap-3 px-5 py-5">
      <img :src="logo" alt="" class="h-9 w-9 rounded-lg object-cover ring-1 ring-accent/30" />
      <div class="leading-tight">
        <div class="text-sm font-extrabold tracking-wide text-white">AION 2</div>
        <div class="text-[11px] font-semibold uppercase tracking-[0.2em] text-accent">Buddy</div>
      </div>
    </div>

    <!-- Main menu -->
    <nav class="flex-1 space-y-1 overflow-y-auto px-3 py-2">
      <button
        v-for="p in mainMenus"
        :key="p.id"
        type="button"
        :disabled="p.comingSoon"
        @click="emit('select', p.id)"
        class="group flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm transition"
        :class="btnClass(p)"
      >
        <span class="text-base">{{ p.icon }}</span>
        <span class="flex-1 font-semibold">{{ t(`menus.${p.id}.label`) }}</span>
        <span
          v-if="p.comingSoon"
          class="rounded bg-white/5 px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wide text-slate-400"
        >{{ t('common.soon') }}</span>
        <span v-else-if="p.id === activeId" class="h-1.5 w-1.5 rounded-full bg-accent" />
      </button>
    </nav>

    <!-- Pinned-bottom menu (Settings) -->
    <nav v-if="bottomMenus.length" class="space-y-1 border-t border-white/5 px-3 py-2">
      <button
        v-for="p in bottomMenus"
        :key="p.id"
        type="button"
        @click="emit('select', p.id)"
        class="group flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm transition"
        :class="btnClass(p)"
      >
        <span class="text-base">{{ p.icon }}</span>
        <span class="flex-1 font-semibold">{{ t(`menus.${p.id}.label`) }}</span>
        <span v-if="p.id === activeId" class="h-1.5 w-1.5 rounded-full bg-accent" />
      </button>
    </nav>

    <!-- Footer / status -->
    <div class="space-y-1 border-t border-white/5 px-5 py-4">
      <div class="flex items-center gap-2 text-xs" :class="gameDetected ? 'text-emerald-300' : 'text-slate-400'">
        <span
          class="h-2 w-2 rounded-full shadow-[0_0_8px]"
          :class="gameDetected ? 'bg-emerald-400 shadow-emerald-400/60' : 'bg-amber-400 shadow-amber-400/60'"
        />
        {{ gameDetected ? t('app.detected') : t('app.notDetected') }}
      </div>
      <div class="text-[10px] text-slate-600">v{{ appVersion }} · {{ t('common.uiPreview') }}</div>
    </div>
  </aside>
</template>
