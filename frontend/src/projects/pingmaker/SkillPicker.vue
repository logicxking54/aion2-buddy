<script lang="ts" setup>
// A searchable skill dropdown that shows each skill's icon (a native <select> can't
// render images in its options). The panel is teleported to <body> and positioned
// against the trigger so it isn't clipped by the scrollable rows list.
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { skills, skillColor, type Skill } from './skills'
import errorImage from '../../assets/images/skill-error.svg'

const props = defineProps<{
  modelValue: string // selected skill id ('' = none)
  excludeId?: string // hide this id (the row's own skill)
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()

const { t } = useI18n()

const open = ref(false)
const search = ref('')
const btn = ref<HTMLElement | null>(null)
const searchBox = ref<HTMLInputElement | null>(null)
const panelStyle = ref<Record<string, string>>({})

const selected = computed(() => (props.modelValue ? skills.find((s) => s.id === props.modelValue) : undefined))

const list = computed(() => {
  const q = search.value.trim().toLowerCase()
  return skills
    .filter((s) => s.id !== props.excludeId)
    .filter((s) => q === '' || s.name.toLowerCase().includes(q))
    .sort((a, b) => a.name.localeCompare(b.name))
})

function imgFor(s: Skill) {
  return s.image_url && s.image_url.trim() ? s.image_url : errorImage
}
function onImgError(e: Event) {
  ;(e.target as HTMLImageElement).src = errorImage
}
function classLabel(c: string) {
  const key = `ping.classes.${c}`
  const translated = t(key)
  return translated === key ? c : translated
}

// Position the teleported panel just under (or above) the trigger, using fixed
// coords from the trigger's bounding rect so it tracks scroll/resize.
function position() {
  const el = btn.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const width = Math.max(r.width, 224)
  const spaceBelow = window.innerHeight - r.bottom
  const openUp = spaceBelow < 288 && r.top > spaceBelow
  panelStyle.value = openUp
    ? { left: `${r.left}px`, bottom: `${window.innerHeight - r.top + 4}px`, width: `${width}px` }
    : { left: `${r.left}px`, top: `${r.bottom + 4}px`, width: `${width}px` }
}

async function toggle() {
  open.value = !open.value
  if (open.value) {
    search.value = ''
    await nextTick()
    position()
    searchBox.value?.focus()
  }
}
function choose(id: string) {
  emit('update:modelValue', id)
  open.value = false
}

function onDocPointer(e: MouseEvent) {
  const target = e.target as Node
  if (btn.value?.contains(target)) return
  if (document.getElementById('skillpicker-panel')?.contains(target)) return
  open.value = false
}
watch(open, (v) => {
  const m = v ? 'addEventListener' : 'removeEventListener'
  document[m]('mousedown', onDocPointer as EventListener)
  window[m]('resize', position)
  window[m]('scroll', position, true)
})
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocPointer as EventListener)
  window.removeEventListener('resize', position)
  window.removeEventListener('scroll', position, true)
})
</script>

<template>
  <button
    ref="btn"
    type="button"
    @click="toggle"
    class="flex w-full items-center gap-2 rounded-md border bg-ink-900 px-2 py-1.5 text-left outline-none transition hover:border-white/20 focus:border-accent/60"
    :class="modelValue ? 'border-accent/50' : 'border-white/10'"
  >
    <img
      v-if="selected"
      :src="imgFor(selected)"
      @error="onImgError"
      alt=""
      class="h-6 w-6 shrink-0 rounded object-cover ring-1 ring-accent/40"
    />
    <span v-else class="grid h-6 w-6 shrink-0 place-items-center rounded border border-dashed border-white/15 text-slate-500">+</span>
    <span class="min-w-0 flex-1 truncate text-xs" :class="modelValue ? 'font-semibold text-accent' : 'text-slate-400'">
      {{ selected ? selected.name : t('ping.swapNone') }}
    </span>
    <span class="shrink-0 text-[10px] text-slate-500">▾</span>
  </button>

  <Teleport to="body">
    <div
      v-if="open"
      id="skillpicker-panel"
      class="fixed z-50 overflow-hidden rounded-lg border border-white/10 bg-ink-800 shadow-2xl"
      :style="panelStyle"
    >
      <div class="border-b border-white/5 p-2">
        <input
          ref="searchBox"
          v-model="search"
          type="text"
          :placeholder="t('ping.search')"
          class="w-full rounded-md border border-white/10 bg-ink-900 px-2 py-1.5 text-xs text-white placeholder:text-slate-500 outline-none focus:border-accent/60"
        />
      </div>
      <ul class="max-h-64 overflow-y-auto p-1">
        <li>
          <button
            type="button"
            @click="choose('')"
            class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-slate-400 transition hover:bg-white/5"
          >
            <span class="grid h-6 w-6 shrink-0 place-items-center rounded border border-dashed border-white/15">×</span>
            {{ t('ping.swapNone') }}
          </button>
        </li>
        <li v-for="s in list" :key="s.id">
          <button
            type="button"
            @click="choose(s.id)"
            class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition hover:bg-white/5"
            :class="s.id === modelValue ? 'bg-accent/10' : ''"
          >
            <img :src="imgFor(s)" @error="onImgError" alt="" class="h-6 w-6 shrink-0 rounded object-cover" />
            <span class="min-w-0 flex-1 truncate text-xs text-white">{{ s.name }}</span>
            <span v-if="s.class" class="shrink-0 text-[10px] font-medium" :style="{ color: skillColor(s) }">
              {{ classLabel(s.class) }}
            </span>
          </button>
        </li>
        <li v-if="list.length === 0" class="px-2 py-4 text-center text-xs text-slate-500">{{ t('ping.noMatch') }}</li>
      </ul>
    </div>
  </Teleport>
</template>
