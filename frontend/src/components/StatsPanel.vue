<script lang="ts" setup>
import { computed } from 'vue'
import { modified, ports, running, start, status, stop } from '../captureController'

const statusText = computed(() => (status.value === 'running' ? 'RUNNING' : status.value === 'error' ? 'ERROR' : 'IDLE'))
const statusDot = computed(() => {
  if (status.value === 'running') return 'animate-pulse bg-accent shadow-[0_0_6px] shadow-accent/60'
  if (status.value === 'error') return 'bg-red-500'
  return 'bg-slate-600'
})
const statusTextClass = computed(() => {
  if (status.value === 'running') return 'text-accent'
  if (status.value === 'error') return 'text-red-400'
  return 'text-slate-500'
})
</script>

<template>
  <div class="border-b border-white/5 bg-ink-900/70 px-4 py-3 font-mono">
    <!-- capture status / ports / modified -->
    <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-[10px] uppercase tracking-wider text-slate-500">
      <span class="flex items-center gap-1.5">
        <span class="h-1.5 w-1.5 rounded-full" :class="statusDot" />
        <span :class="statusTextClass">{{ statusText }}</span>
      </span>
      <span>Ports <b class="text-slate-300">{{ ports.length ? ports.join(', ') : '—' }}</b></span>
      <span>Modified <b class="text-accent">{{ modified }}</b></span>
    </div>

    <!-- capture control -->
    <div class="mt-2">
      <button
        type="button"
        @click="running ? stop() : start()"
        :title="running ? 'Stop capture' : 'Start capture'"
        class="inline-flex w-fit cursor-pointer items-center gap-1.5 rounded-md px-3 py-1 text-[11px] font-bold transition active:scale-95"
        :class="running ? 'bg-red-500/20 text-red-300 hover:bg-red-500/30' : 'bg-accent/20 text-accent hover:bg-accent/30'"
      >{{ running ? '■ Stop' : '▶ Start' }}</button>
    </div>
  </div>
</template>
