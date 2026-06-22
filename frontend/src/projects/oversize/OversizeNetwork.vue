<script lang="ts" setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { config, saveConfig } from '../../config'
import { log as globalLog } from '../../captureController'
import { EventsOn } from '../../../wailsjs/runtime/runtime'

const { t } = useI18n()

type Status = 'disconnected' | 'connecting' | 'connected' | 'error'
const status = ref<Status>('disconnected')
const ping = ref(0)
const showPass = ref(false)
const busy = ref(false)
const settingUp = ref(false)

// Thin defensive access to the Wails-bound Oversize backend (absent in a plain
// browser / when the backend hasn't loaded), mirroring capture.ts.
interface OversizeBackend {
  Connect(relayIP: string, key: string, fullTunnel: boolean, knownGameIPs: string[]): Promise<void>
  Disconnect(): Promise<void>
  IsConnected(): Promise<boolean>
  SetupServer(ip: string, port: number, user: string, pass: string): Promise<string>
}
function backend(): OversizeBackend | undefined {
  return (window as any)?.go?.main?.Oversize
}
const available = !!backend()

const connected = computed(() => status.value === 'connected')
const hasKey = computed(() => !!config.oversize.key)
const statusClass = computed(() => {
  switch (status.value) {
    case 'connected':
      return 'bg-emerald-500/15 text-emerald-300 ring-emerald-400/30'
    case 'connecting':
      return 'bg-amber-500/15 text-amber-300 ring-amber-400/30'
    case 'error':
      return 'bg-rose-500/15 text-rose-300 ring-rose-400/30'
    default:
      return 'bg-white/5 text-slate-400 ring-white/10'
  }
})

let offs: Array<() => void> = []
onMounted(async () => {
  offs.push(EventsOn('oversize:status', (s: string) => (status.value = s as Status)))
  offs.push(EventsOn('oversize:ping', (p: number) => (ping.value = p ?? 0)))
  offs.push(
    EventsOn('oversize:server', (ip: string) => {
      // Accumulate learned Aion 2 server IPs so split-tunnel can pre-route them.
      if (ip && !config.oversize.gameIPs.includes(ip)) {
        config.oversize.gameIPs.push(ip)
        saveConfig()
      }
    }),
  )
  const b = backend()
  if (b) {
    try {
      status.value = (await b.IsConnected()) ? 'connected' : 'disconnected'
    } catch {
      /* ignore */
    }
  }
})
onUnmounted(() => {
  offs.forEach((f) => f && f())
  offs = []
})

async function connect() {
  const b = backend()
  if (!b || busy.value || !hasKey.value) return
  saveConfig()
  busy.value = true
  try {
    await b.Connect(
      config.oversize.ip.trim(),
      config.oversize.key,
      config.oversize.fullTunnel,
      [...config.oversize.gameIPs],
    )
  } catch (e: any) {
    globalLog(String(e?.message ?? e), 'warn')
  } finally {
    busy.value = false
  }
}
function clearGameIPs() {
  config.oversize.gameIPs = []
  saveConfig()
}
async function disconnect() {
  const b = backend()
  if (!b || busy.value) return
  busy.value = true
  try {
    await b.Disconnect()
  } finally {
    busy.value = false
  }
}
async function setupServer() {
  const b = backend()
  if (!b || settingUp.value || connected.value) return
  saveConfig()
  settingUp.value = true
  try {
    const key = await b.SetupServer(
      config.oversize.ip.trim(),
      Number(config.oversize.port) || 22,
      config.oversize.user.trim(),
      config.oversize.password,
    )
    if (key) {
      config.oversize.key = key
      saveConfig()
      globalLog('Tunnel key stored. You can now Connect.')
    }
  } catch (e: any) {
    globalLog(String(e?.message ?? e), 'warn')
  } finally {
    settingUp.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-2xl space-y-4">
    <!-- Backend missing notice -->
    <div
      v-if="!available"
      class="rounded-xl border border-rose-400/20 bg-rose-500/10 p-4 text-sm text-rose-200"
    >
      {{ t('oversize.unavailable') }}
    </div>

    <!-- Relay server settings -->
    <section class="rounded-xl border border-white/5 bg-ink-700 p-5">
      <h2 class="text-sm font-bold uppercase tracking-wider text-slate-400">{{ t('oversize.relay') }}</h2>
      <p class="mt-1 text-sm text-slate-500">{{ t('oversize.relayHint') }}</p>

      <div class="mt-4 grid gap-3 sm:grid-cols-2">
        <label class="block">
          <span class="text-xs font-semibold text-slate-400">{{ t('oversize.ip') }}</span>
          <input
            v-model="config.oversize.ip"
            :disabled="connected"
            @change="saveConfig()"
            type="text"
            spellcheck="false"
            class="mt-1 w-full rounded-lg border border-white/10 bg-ink-800 px-3 py-2 text-sm text-white outline-none focus:border-accent/50 disabled:opacity-50"
          />
        </label>
        <label class="block">
          <span class="text-xs font-semibold text-slate-400">{{ t('oversize.port') }}</span>
          <input
            v-model.number="config.oversize.port"
            :disabled="connected"
            @change="saveConfig()"
            type="number"
            min="1"
            max="65535"
            class="mt-1 w-full rounded-lg border border-white/10 bg-ink-800 px-3 py-2 text-sm text-white outline-none focus:border-accent/50 disabled:opacity-50"
          />
        </label>
        <label class="block">
          <span class="text-xs font-semibold text-slate-400">{{ t('oversize.user') }}</span>
          <input
            v-model="config.oversize.user"
            :disabled="connected"
            @change="saveConfig()"
            type="text"
            spellcheck="false"
            autocomplete="off"
            class="mt-1 w-full rounded-lg border border-white/10 bg-ink-800 px-3 py-2 text-sm text-white outline-none focus:border-accent/50 disabled:opacity-50"
          />
        </label>
        <label class="block">
          <span class="text-xs font-semibold text-slate-400">{{ t('oversize.password') }}</span>
          <div class="relative mt-1">
            <input
              v-model="config.oversize.password"
              :disabled="connected"
              @change="saveConfig()"
              :type="showPass ? 'text' : 'password'"
              autocomplete="off"
              class="w-full rounded-lg border border-white/10 bg-ink-800 px-3 py-2 pr-14 text-sm text-white outline-none focus:border-accent/50 disabled:opacity-50"
            />
            <button
              type="button"
              @click="showPass = !showPass"
              class="absolute right-2 top-1/2 -translate-y-1/2 rounded px-2 py-1 text-[11px] font-semibold text-slate-400 hover:text-white"
            >
              {{ showPass ? t('oversize.hide') : t('oversize.show') }}
            </button>
          </div>
        </label>
      </div>
      <p class="mt-3 text-[11px] text-slate-600">{{ t('oversize.passwordNote') }}</p>

      <div class="mt-4 flex flex-wrap items-center gap-3 border-t border-white/5 pt-4">
        <button
          type="button"
          :disabled="!available || settingUp || connected"
          @click="setupServer"
          class="rounded-lg border border-white/15 bg-ink-800 px-4 py-2 text-sm font-semibold text-white transition hover:border-white/30 disabled:opacity-50"
        >
          {{ settingUp ? t('oversize.settingUp') : t('oversize.setup') }}
        </button>
        <span
          v-if="hasKey"
          class="rounded-full bg-emerald-500/15 px-2.5 py-0.5 text-[11px] font-semibold text-emerald-300 ring-1 ring-emerald-400/30"
        >{{ t('oversize.keyReady') }}</span>
        <p class="flex-1 text-[11px] text-slate-500">{{ t('oversize.setupHint') }}</p>
      </div>
    </section>

    <!-- Connection control -->
    <section class="rounded-xl border border-white/5 bg-ink-700 p-5">
      <div class="flex flex-wrap items-center justify-between gap-4">
        <div class="flex items-center gap-3">
          <span class="rounded-full px-3 py-1 text-xs font-bold ring-1" :class="statusClass">
            {{ t(`oversize.status.${status}`) }}
          </span>
          <span v-if="connected" class="text-sm text-slate-400">
            {{ t('oversize.latency') }}: <span class="font-semibold text-white">{{ ping }}ms</span>
          </span>
        </div>

        <button
          v-if="!connected && status !== 'connecting'"
          type="button"
          :disabled="!available || busy || !hasKey"
          @click="connect"
          class="rounded-lg bg-accent px-5 py-2 text-sm font-bold text-ink-900 transition hover:brightness-110 disabled:opacity-50"
        >
          {{ t('oversize.connect') }}
        </button>
        <button
          v-else
          type="button"
          :disabled="busy"
          @click="disconnect"
          class="rounded-lg border border-white/15 bg-ink-800 px-5 py-2 text-sm font-bold text-white transition hover:border-white/30 disabled:opacity-50"
        >
          {{ t('oversize.disconnect') }}
        </button>
      </div>
      <!-- Full-tunnel toggle -->
      <div class="mt-4 flex items-center justify-between gap-4 border-t border-white/5 pt-4">
        <div>
          <div class="text-sm font-semibold text-slate-200">{{ t('oversize.fullTunnel') }}</div>
          <div class="text-[11px] text-slate-500">{{ t('oversize.fullTunnelHint') }}</div>
        </div>
        <button
          type="button"
          role="switch"
          :aria-checked="config.oversize.fullTunnel"
          :disabled="connected"
          @click="config.oversize.fullTunnel = !config.oversize.fullTunnel; saveConfig()"
          class="relative inline-block h-6 w-11 shrink-0 cursor-pointer rounded-full transition-colors disabled:opacity-50"
          :class="config.oversize.fullTunnel ? 'bg-accent' : 'bg-white/20'"
        >
          <span
            class="absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all"
            :class="config.oversize.fullTunnel ? 'left-[22px]' : 'left-0.5'"
          />
        </button>
      </div>

      <!-- Learned game IPs (split-tunnel) -->
      <div v-if="!config.oversize.fullTunnel" class="mt-3 flex items-center gap-3 text-[11px] text-slate-500">
        <span>{{ t('oversize.knownIPs', { n: config.oversize.gameIPs.length }) }}</span>
        <button
          v-if="config.oversize.gameIPs.length"
          type="button"
          @click="clearGameIPs"
          class="rounded px-1.5 py-0.5 font-semibold text-slate-400 hover:text-white"
        >{{ t('oversize.clearIPs') }}</button>
      </div>

      <p class="mt-3 text-sm text-slate-500">{{ t('oversize.hint') }}</p>
      <p v-if="!hasKey" class="mt-1 text-sm text-amber-300/80">{{ t('oversize.needDeploy') }}</p>
    </section>
  </div>
</template>
