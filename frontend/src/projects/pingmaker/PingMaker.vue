<script lang="ts" setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { classes, skills, skillColor, type Skill, type SkillClass } from './skills'
import errorImage from '../../assets/images/skill-error.svg'
import { config, loadConfig, saveConfig } from '../../config'
import { casterAutoDetecting, startCasterAutoDetect, stopCasterAutoDetect } from '../../captureController'

const { t } = useI18n()

function classLabel(c: string) {
  const key = `ping.classes.${c}`
  const translated = t(key)
  return translated === key ? c : translated
}

function skillImage(s: Skill) {
  return s.image_url && s.image_url.trim() ? s.image_url : errorImage
}
function onImgError(e: Event) {
  ;(e.target as HTMLImageElement).src = errorImage
}

const DEFAULT_SPEED = 250
const defaultSpeed = ref(DEFAULT_SPEED)

// ── Left panel: filtering ───────────────────────────────────────────────
const activeClass = ref<SkillClass | 'all'>('all')
const search = ref('')

const filteredSkills = computed(() => {
  const q = search.value.trim().toLowerCase()
  const added = new Set(rows.map((r) => r.skill.id))
  return skills.filter((s) => {
    if (added.has(s.id)) return false // hide skills already added on the right
    const matchesClass = activeClass.value === 'all' || s.class === activeClass.value
    const matchesText = q === '' || s.name.toLowerCase().includes(q)
    return matchesClass && matchesText
  })
})

// ── Right panel: configured skills ──────────────────────────────────────
interface ConfigRow {
  uid: number
  skill: Skill
  speedPct: number
  overridden: boolean
  brk: boolean
}

let nextUid = 1
const rows = reactive<ConfigRow[]>([])

// Search within the added (right-side) skills.
const rightSearch = ref('')
const visibleRows = computed(() => {
  const q = rightSearch.value.trim().toLowerCase()
  if (!q) return rows
  return rows.filter((r) => r.skill.name.toLowerCase().includes(q))
})

// Push the default speed into rows the user hasn't customised.
watch(defaultSpeed, (val) => {
  for (const r of rows) if (!r.overridden) r.speedPct = val
  writeConfig()
})

function addSkill(skill: Skill) {
  rows.push({ uid: nextUid++, skill, speedPct: defaultSpeed.value, overridden: false, brk: false })
  writeConfig()
}
function setRowSpeed(row: ConfigRow, value: string) {
  row.speedPct = value === '' ? 0 : Number(value)
  row.overridden = true
  writeConfig()
}
function resetRowSpeed(row: ConfigRow) {
  row.speedPct = defaultSpeed.value
  row.overridden = false
  writeConfig()
}
function toggleBreak(row: ConfigRow) {
  row.brk = !row.brk
  writeConfig()
}
function removeRow(uid: number) {
  const i = rows.findIndex((r) => r.uid === uid)
  if (i !== -1) rows.splice(i, 1)
  writeConfig()
}
function clearAll() {
  rows.splice(0, rows.length)
  writeConfig()
}

// ── Persistence: mirror local state into the shared config store ────────
let loading = false // suppress saves while restoring on mount

function writeConfig() {
  if (loading) return
  config.defaultSpeed = defaultSpeed.value
  config.rows = rows.map((r) => ({ id: r.skill.id, speedPct: r.speedPct, overridden: r.overridden, brk: r.brk }))
  saveConfig()
}

onMounted(async () => {
  loading = true
  await loadConfig()
  defaultSpeed.value = config.defaultSpeed
  rows.splice(0, rows.length)
  const byId = new Map(skills.map((s) => [s.id, s]))
  for (const r of config.rows) {
    const sk = byId.get(r.id)
    if (sk) {
      rows.push({
        uid: nextUid++,
        skill: sk,
        speedPct: Number(r.speedPct) || 0,
        overridden: !!r.overridden,
        brk: !!r.brk,
      })
    }
  }
  loading = false
})
</script>

<template>
  <div class="mx-auto flex h-full max-w-6xl flex-col gap-4">
    <div class="grid min-h-0 flex-1 grid-cols-2 gap-4">
    <!-- ============ LEFT: skill picker ============ -->
    <div class="flex min-h-0 flex-col rounded-xl border border-white/5 bg-ink-700 p-5">
      <h2 class="text-sm font-bold uppercase tracking-wider text-slate-400">{{ t('ping.skills') }}</h2>

      <!-- Class filter -->
      <div class="mt-3 flex flex-wrap gap-2">
        <button
          type="button"
          @click="activeClass = 'all'"
          class="rounded-lg px-3 py-1.5 text-xs font-semibold transition"
          :class="activeClass === 'all'
            ? 'bg-accent/15 text-white ring-1 ring-accent/30'
            : 'text-slate-300 hover:bg-white/5'"
        >{{ t('ping.all') }}</button>
        <button
          v-for="c in classes"
          :key="c.id"
          type="button"
          @click="activeClass = c.id"
          class="flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-semibold transition"
          :class="activeClass === c.id ? 'text-white ring-1' : 'text-slate-300 hover:bg-white/5'"
          :style="activeClass === c.id ? { backgroundColor: c.color + '22', boxShadow: 'inset 0 0 0 1px ' + c.color } : {}"
        >
          <img :src="c.icon" @error="onImgError" alt="" class="h-5 w-5 rounded object-cover ring-1 ring-white/10" />
          {{ classLabel(c.id) }}
        </button>
      </div>

      <!-- Search -->
      <input
        v-model="search"
        type="text"
        :placeholder="t('ping.search')"
        class="mt-3 w-full rounded-lg border border-white/10 bg-ink-800 px-3 py-2 text-sm text-white placeholder:text-slate-500 outline-none focus:border-accent/60 focus:ring-2 focus:ring-accent/20"
      />

      <!-- Skill list -->
      <ul class="mt-3 min-h-0 flex-1 space-y-2 overflow-y-auto pr-1">
        <li
          v-for="s in filteredSkills"
          :key="s.id"
          class="flex items-center gap-3 rounded-lg border border-white/5 bg-ink-800 p-2.5 transition hover:border-white/10"
        >
          <img :src="skillImage(s)" @error="onImgError" alt="" class="h-9 w-9 shrink-0 rounded-lg object-cover" />
          <div class="min-w-0 flex-1">
            <div class="truncate text-sm font-semibold text-white">{{ s.name }}</div>
            <div v-if="s.class" class="text-[11px] font-medium" :style="{ color: skillColor(s) }">
              {{ classLabel(s.class) }}
            </div>
            <div v-else class="text-[11px] text-slate-600">{{ t('ping.unclassified') }}</div>
          </div>
          <button
            type="button"
            @click="addSkill(s)"
            class="shrink-0 rounded-md bg-accent/15 px-3 py-1.5 text-xs font-bold text-accent transition hover:bg-accent hover:text-ink-900"
          >{{ t('ping.add') }}</button>
        </li>
        <li v-if="filteredSkills.length === 0" class="py-8 text-center text-sm text-slate-500">
          {{ t('ping.noMatch') }}
        </li>
      </ul>
    </div>

    <!-- ============ RIGHT: combat-speed config ============ -->
    <div class="flex min-h-0 flex-col rounded-xl border border-white/5 bg-ink-700 p-5">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-bold uppercase tracking-wider text-slate-400">
          {{ t('ping.active') }} <span class="ml-1 text-slate-500">({{ rows.length }})</span>
        </h2>
        <button
          v-if="rows.length"
          type="button"
          @click="clearAll"
          class="rounded-md px-2 py-1 text-xs font-semibold text-slate-400 transition hover:bg-red-500/10 hover:text-red-400"
        >{{ t('ping.clear') }}</button>
      </div>

      <!-- Default combat speed -->
      <div class="mt-2 flex items-center gap-2 rounded-lg border border-white/5 bg-ink-800 px-3 py-2 transition">
        <span class="text-xs font-semibold text-slate-300">{{ t('ping.defaultSpeed') }}</span>
        <input
          v-model.number="defaultSpeed"
          type="number"
          min="0"
          step="10"
          class="ml-auto w-20 rounded-md border border-white/10 bg-ink-900 px-2 py-1 text-right text-sm text-white outline-none focus:border-accent/60 focus:ring-2 focus:ring-accent/20"
        />
        <span class="text-[11px] text-slate-500">%</span>
      </div>

      <div class="mt-2 flex items-center justify-between gap-3 rounded-lg border border-white/5 bg-ink-800 px-3 py-2 text-xs font-semibold text-slate-300">
        <label class="flex items-center gap-2">
          <span>{{ t('ping.casterRecord') }}</span>
          <input
            v-model.number="config.casterRecord"
            @change="saveConfig"
            type="number"
            min="0"
            placeholder="-"
            class="w-28 rounded-md border border-white/10 bg-ink-900 px-2 py-1 text-right text-sm text-white outline-none focus:border-accent/60 focus:ring-2 focus:ring-accent/20"
          />
        </label>
        <button
          type="button"
          @click="casterAutoDetecting ? stopCasterAutoDetect() : startCasterAutoDetect()"
          :title="t('ping.casterAutoHint')"
          class="shrink-0 rounded-md px-2.5 py-1 text-xs font-semibold transition"
          :class="casterAutoDetecting
            ? 'bg-amber-500/20 text-amber-300 ring-1 ring-amber-400/30'
            : 'bg-accent/15 text-accent hover:bg-accent/25'"
        >{{ casterAutoDetecting ? '● ' + t('ping.casterAutoStop') : t('ping.casterAuto') }}</button>
      </div>

      <!-- Search added skills -->
      <input
        v-if="rows.length"
        v-model="rightSearch"
        type="text"
        :placeholder="t('ping.searchActive')"
        class="mt-2 w-full rounded-lg border border-white/10 bg-ink-800 px-3 py-2 text-sm text-white placeholder:text-slate-500 outline-none focus:border-accent/60 focus:ring-2 focus:ring-accent/20"
      />

      <!-- Empty state -->
      <div
        v-if="rows.length === 0"
        class="mt-3 grid flex-1 place-items-center rounded-lg border border-dashed border-white/10 text-center"
      >
        <div>
          <div class="text-4xl">📍</div>
          <p class="mt-2 font-semibold text-slate-300">{{ t('ping.emptyTitle') }}</p>
          <p class="text-sm text-slate-500">{{ t('ping.emptyHint') }}</p>
        </div>
      </div>

      <!-- Rows -->
      <ul v-else class="mt-3 min-h-0 flex-1 space-y-2 overflow-y-auto pr-1">
        <li v-if="visibleRows.length === 0" class="py-8 text-center text-sm text-slate-500">
          {{ t('ping.noMatch') }}
        </li>
        <li
          v-for="row in visibleRows"
          :key="row.uid"
          class="flex items-center gap-3 rounded-lg border border-white/5 bg-ink-800 p-2.5"
          :style="{ boxShadow: 'inset 3px 0 0 ' + skillColor(row.skill) }"
        >
          <img :src="skillImage(row.skill)" @error="onImgError" alt="" class="h-9 w-9 shrink-0 rounded-lg object-cover" />

          <div class="min-w-0 flex-1">
            <div class="truncate text-sm font-semibold text-white">{{ row.skill.name }}</div>
            <div v-if="row.skill.class" class="text-[11px] font-medium" :style="{ color: skillColor(row.skill) }">
              {{ classLabel(row.skill.class) }}
            </div>
            <div v-else class="text-[11px] text-slate-600">{{ t('ping.unclassified') }}</div>
          </div>

          <!-- Break toggle -->
          <button
            type="button"
            @click="toggleBreak(row)"
            :title="t('ping.breakHint')"
            class="shrink-0 rounded-md px-2 py-1 text-[11px] font-bold transition"
            :class="row.brk ? 'bg-amber-400/20 text-amber-300 ring-1 ring-amber-400/40' : 'text-slate-500 hover:bg-white/5'"
          >{{ t('ping.break') }}</button>

          <!-- Speed input (typing overrides the default) -->
          <label class="flex shrink-0 items-center gap-1.5" :class="row.brk ? 'opacity-40' : ''">
            <input
              :value="row.speedPct"
              @input="setRowSpeed(row, ($event.target as HTMLInputElement).value)"
              :disabled="row.brk"
              type="number"
              min="0"
              step="10"
              class="w-20 rounded-md border bg-ink-900 px-2 py-1 text-right text-sm text-white placeholder:text-accent/70 outline-none focus:border-accent/60 focus:ring-2 focus:ring-accent/20"
              :class="row.overridden ? 'border-accent/40' : 'border-white/10'"
            />
            <span class="text-[11px] text-slate-500">%</span>
            <button
              type="button"
              @click="resetRowSpeed(row)"
              :class="row.overridden ? 'text-slate-400 hover:text-accent' : 'invisible'"
              class="text-xs leading-none transition"
              :title="t('ping.resetDefault')"
            >↺</button>
          </label>

          <button
            type="button"
            @click="removeRow(row.uid)"
            class="shrink-0 rounded-md px-2 py-1 text-xs font-semibold text-slate-500 transition hover:bg-red-500/10 hover:text-red-400"
            :title="t('ping.clear')"
          >✕</button>
        </li>
      </ul>
    </div>
    </div>
  </div>
</template>
