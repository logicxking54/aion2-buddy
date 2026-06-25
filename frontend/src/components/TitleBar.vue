<script lang="ts" setup>
import { Quit, WindowMinimise } from '../../wailsjs/runtime/runtime'
import logo from '../assets/images/app-logo.png'

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
    <div class="ml-auto flex h-full" style="--wails-draggable: no-drag">
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
