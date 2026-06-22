<script lang="ts" setup>
import { ref } from 'vue'
import { recentCasts } from '../captureController'
import { skillByNumericId } from '../projects/pingmaker/skills'
import { config, saveConfig } from '../config'
import StatsPanel from './StatsPanel.vue'
import errorImage from '../assets/images/skill-error.svg'

const hovered = ref<number | null>(null)

function img(id: number) {
  const s = skillByNumericId(id)
  return s && s.image_url.trim() ? s.image_url : errorImage
}
function skillName(id: number) {
  return skillByNumericId(id)?.name ?? `ID:${id}`
}
function onImgError(e: Event) {
  ;(e.target as HTMLImageElement).src = errorImage
}
function toggleOverlay() {
  config.overlay = !config.overlay // App.vue watches this to restore the window
  saveConfig()
}
</script>

<template>
  <!-- Whole box is the drag handle; buttons/icons opt out with no-drag. -->
  <div class="flex h-screen w-screen flex-col bg-ink-900/95 text-slate-200 select-none" style="--wails-draggable: drag">
    <!-- header: hovered skill name + overlay toggle -->
    <div class="flex items-center gap-2 px-2 py-1">
      <span class="shrink-0 text-[10px] font-bold uppercase tracking-widest text-slate-500">Buddy</span>
      <span class="min-w-0 flex-1 truncate text-center text-[10px] font-semibold text-slate-300">
        {{ hovered != null ? skillName(hovered) : '' }}
      </span>
      <button
        type="button"
        role="switch"
        :aria-checked="config.overlay"
        @click="toggleOverlay"
        title="Overlay mode ON — click to turn off (restore the full window)"
        style="--wails-draggable: no-drag"
        class="flex shrink-0 cursor-pointer items-center gap-1.5 rounded-md px-1 py-0.5 text-[10px] font-semibold text-accent transition active:scale-95"
        aria-label="Toggle overlay mode"
      >
        <span>📌</span>
        <span class="relative inline-block h-4 w-7 rounded-full transition-colors" :class="config.overlay ? 'bg-accent' : 'bg-white/25'">
          <span
            class="absolute top-0.5 h-3 w-3 rounded-full bg-white shadow transition-all"
            :class="config.overlay ? 'left-[14px]' : 'left-0.5'"
          />
        </span>
      </button>
    </div>

    <!-- network/data console -->
    <StatsPanel />

    <!-- recently used skills: newest (left) → oldest (right) -->
    <div class="flex flex-1 items-center gap-1.5 overflow-hidden px-2 py-2">
      <div
        v-for="c in recentCasts"
        :key="c.uid"
        @mouseenter="hovered = c.id"
        @mouseleave="hovered = null"
        style="--wails-draggable: no-drag"
        class="shrink-0 transition-transform"
        :class="hovered === c.id ? 'scale-110' : ''"
      >
        <img :src="img(c.id)" @error="onImgError" alt="" class="h-12 w-12 rounded-md object-cover ring-1 ring-white/10" />
      </div>
      <span v-if="recentCasts.length === 0" class="text-[10px] text-slate-600">waiting for skills…</span>
    </div>
  </div>
</template>
