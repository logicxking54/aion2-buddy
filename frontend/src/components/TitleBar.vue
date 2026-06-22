<script lang="ts" setup>
import { Quit, WindowMinimise, WindowSetAlwaysOnTop } from '../../wailsjs/runtime/runtime'
import { config, saveConfig } from '../config'
import logo from '../assets/images/app-logo.png'

// Overlay mode lives in the app-level config store (persisted; other components
// adapt their UI to it). Toggling pins/unpins the window above the game.
function toggleOverlay() {
  config.overlay = !config.overlay
  WindowSetAlwaysOnTop(config.overlay)
  saveConfig()
}
function minimise() {
  WindowMinimise()
}
function close() {
  Quit()
}
</script>

<template>
  <!-- The whole bar is the drag handle; buttons opt out with no-drag. -->
  <div
    class="flex h-9 shrink-0 select-none items-center border-b border-white/5 bg-ink-900 pl-3"
    style="--wails-draggable: drag"
  >
    <div class="flex items-center gap-2 text-xs font-semibold text-slate-400">
      <img :src="logo" alt="" class="h-5 w-5 rounded object-cover ring-1 ring-white/10" />
      <span class="tracking-wide">Aion 2 Buddy</span>
    </div>

    <!-- Window controls (no maximise — the window is fixed size) -->
    <div class="ml-auto flex h-full items-center gap-1 pr-1" style="--wails-draggable: no-drag">
      <button
        type="button"
        role="switch"
        :aria-checked="config.overlay"
        @click="toggleOverlay"
        :title="config.overlay ? 'Overlay mode ON — click to turn off' : 'Overlay mode — keep window above the game (always on top)'"
        class="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-[11px] font-semibold transition active:scale-95"
        :class="config.overlay ? 'text-accent' : 'text-slate-300 hover:bg-white/10 hover:text-white'"
        aria-label="Toggle overlay mode"
      >
        <span>📌 Overlay</span>
        <!-- toggle switch -->
        <span class="relative inline-block h-4 w-7 shrink-0 rounded-full transition-colors" :class="config.overlay ? 'bg-accent' : 'bg-white/25'">
          <span
            class="absolute top-0.5 h-3 w-3 rounded-full bg-white shadow transition-all"
            :class="config.overlay ? 'left-[14px]' : 'left-0.5'"
          />
        </span>
      </button>
    </div>
    <div class="flex h-full" style="--wails-draggable: no-drag">
      <button
        type="button"
        @click="minimise"
        class="grid h-full w-12 place-items-center text-slate-400 transition hover:bg-white/10 hover:text-white"
        aria-label="Minimise"
      >
        <svg width="10" height="10" viewBox="0 0 10 10"><path d="M0 5 H10" stroke="currentColor" stroke-width="1" /></svg>
      </button>
      <button
        type="button"
        @click="close"
        class="grid h-full w-12 place-items-center text-slate-400 transition hover:bg-red-500 hover:text-white"
        aria-label="Close"
      >
        <svg width="10" height="10" viewBox="0 0 10 10">
          <path d="M0.5 0.5 L9.5 9.5 M9.5 0.5 L0.5 9.5" stroke="currentColor" stroke-width="1.2" />
        </svg>
      </button>
    </div>
  </div>
</template>
