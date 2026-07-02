<script lang="ts" setup>
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()

// Build-time app version (from wails.json's info.productVersion, injected by Vite).
const appVersion = __APP_VERSION__

// Changelog — newest first. Each change carries both languages so it follows the
// active locale. Add a new release object at the TOP for each version bump.
interface Change {
  en: string
  th: string
}
interface Release {
  version: string
  changes: Change[]
}
const releases: Release[] = [
  {
    version: '1.2.4',
    changes: [
      { en: 'Removed the "Render as" skill-replacement feature from Ping Maker.', th: 'เอาฟีเจอร์ "แสดงเป็น" (แทนสกิล) ออกจาก Ping Maker' },
    ],
  },
  {
    version: '1.2.3',
    changes: [
      { en: 'Ping Maker: each skill can now be rendered as another skill — pick a "Render as" target (with icons) to show a different cast animation/effect (client-side only; the real skill still fires).', th: 'Ping Maker: แต่ละสกิลเลือกให้แสดงเป็นอีกสกิลได้แล้ว — เลือก "แสดงเป็น" (มีรูปสกิล) เพื่อให้ร่ายออกมาเป็นท่า/เอฟเฟกต์ของสกิลอื่น (เปลี่ยนแค่ภาพ สกิลจริงยังทำงานเหมือนเดิม)' },
      { en: 'Caster selection is now sticky — once you pick your caster it no longer flips to a same-class teammate who casts your skill; they just appear as a selectable option.', th: 'การเลือก caster อยู่หมัดแล้ว — พอเลือกตัวเองแล้วจะไม่เด้งไปหาเพื่อนคลาสเดียวกันที่ร่ายสกิลเดียวกัน (เพื่อนจะโผล่เป็นตัวเลือกให้เลือกเองแทน)' },
      { en: 'Removed the Break button from skill rows.', th: 'เอาปุ่ม Break ออกจากแถวสกิล' },
    ],
  },
  {
    version: '1.2.2',
    changes: [
      { en: 'Updated "Disable Skill Effects" for the latest game build (adds the new class). Re-enable it in the Mod menu after this update.', th: 'อัปเดต "ปิดเอฟเฟกต์สกิล" ให้รองรับเกมเวอร์ชันล่าสุด (รวมคลาสใหม่) — เปิดใช้งานใหม่ในเมนู Mod หลังอัปเดตนี้' },
    ],
  },
  {
    version: '1.2.1',
    changes: [
      { en: 'Moved the "Disable other players\' skill animations" toggle into the Mod menu, alongside the other mods.', th: 'ย้ายปุ่ม "ปิดอนิเมชั่นสกิลของผู้เล่นคนอื่น" ไปไว้ในเมนู Mod รวมกับม็อดอื่นๆ' },
    ],
  },
  {
    version: '1.2.0',
    changes: [
      { en: 'New Mod: "Disable Skill Effects" — removes every class\'s skill cast visual effects on your screen for higher FPS and a clearer view. Downloads a mod pack on first enable; restart the game to apply; fully reversible.', th: 'ม็อดใหม่ "ปิดเอฟเฟกต์สกิล" — ลบเอฟเฟกต์ตอนร่ายสกิลของทุกคลาสบนจอของคุณ เพื่อ FPS สูงขึ้นและจอโล่ง โหลดม็อดครั้งแรกที่เปิด รีสตาร์ตเกมเพื่อให้มีผล ย้อนกลับได้ทั้งหมด' },
    ],
  },
  {
    version: '1.1.2',
    changes: [
      { en: 'Fixed caster auto-detect in party play — it now locks onto your own caster instantly from your edit-list skills, instead of flipping to teammates and skipping your speed edits', th: 'แก้ caster auto-detect ในปาร์ตี้ — ล็อก caster ของคุณทันทีจากสกิลใน edit list แทนที่จะสลับไปหาเพื่อนจนข้ามการแก้ speed ของคุณ' },
    ],
  },
  {
    version: '1.1.1',
    changes: [
      { en: 'Removed the Overlay (always-on-top HUD) mode', th: 'เอาโหมด Overlay (ปักหมุดหน้าต่างทับเกม) ออก' },
      { en: 'Cleaner Ping Maker log output', th: 'ปรับ log ของ Ping Maker ให้สะอาดขึ้น' },
    ],
  },
  {
    version: '1.1.0',
    changes: [
      { en: 'Ping Maker now edits combat speed inside LZ4-compressed party packets — boosts apply to ~100% of party casts (no more occasional slow casts)', th: 'Ping Maker แก้ความเร็วในแพ็กเก็ตปาร์ตี้ที่ถูกบีบอัด (LZ4) ได้แล้ว — บูสต์ติดเกือบ 100% ของการร่ายในปาร์ตี้ (ไม่มีร่ายช้าเป็นบางครั้งอีก)' },
      { en: 'Fixed a reconnect/disconnect issue during play', th: 'แก้ปัญหา reconnect/หลุดการเชื่อมต่อระหว่างเล่น' },
      { en: 'Added image icons for all skills', th: 'เพิ่มไอคอนรูปภาพให้สกิลทั้งหมด' },
    ],
  },
  {
    version: '1.0.7',
    changes: [
      { en: 'Added caster data recording and caster-prioritized Ping Maker logs', th: 'เพิ่มการบันทึกข้อมูล Caster และกรองบันทึก Ping Maker ตาม Caster' },
    ],
  },
  {
    version: '1.0.6',
    changes: [
      { en: 'Improved Ping Maker compact-speed parsing for party play and reduced reconnect risk', th: 'ปรับปรุง Ping Maker สำหรับปาร์ตี้ ลดโอกาส reconnect และจับ compact speed ได้ดีขึ้น' },
    ],
  },
  {
    version: '1.0.5',
    changes: [
      { en: 'Improved Ping Maker to work well in party dungeons', th: 'ปรับปรุง Ping Maker ให้ทำงานได้ดีในปาร์ตี้ดันเจี้ยน' },
    ],
  },
  {
    version: '1.0.4',
    changes: [
      { en: 'Fixed Ping Maker bug', th: 'แก้บั๊ก Ping Maker' },
      { en: 'Added TCP & Network tuning mods', th: 'เพิ่มม็อด TCP และปรับแต่งเครือข่าย' },
    ],
  },
  {
    version: '1.0.3',
    changes: [
      { en: 'Improved Ping Maker', th: 'ปรับปรุงฟีเจอร์ Ping Maker' },
      { en: 'Added Mod feature', th: 'เพิ่มฟีเจอร์ Mod' },
    ],
  },
  { version: '1.0.2', changes: [{ en: 'Added Memory Reader', th: 'เพิ่มฟีเจอร์ Memory Reader' }] },
  { version: '1.0.1', changes: [{ en: 'Added Caster Filter', th: 'เพิ่มฟีเจอร์ Caster Filter' }] },
  { version: '1.0.0', changes: [{ en: 'Added Ping Maker', th: 'เพิ่มฟีเจอร์ Ping Maker' }] },
]

function changeText(c: Change) {
  return locale.value === 'th' ? c.th : c.en
}

// Show once per version: remember the last version the user dismissed. After an
// update (new version) the popup appears again so they see what changed.
const SEEN_KEY = 'changelog-seen'
const visible = ref(false)

function close() {
  localStorage.setItem(SEEN_KEY, appVersion)
  visible.value = false
}

onMounted(() => {
  if (localStorage.getItem(SEEN_KEY) !== appVersion) visible.value = true
})
</script>

<template>
  <div
    v-if="visible"
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-6 backdrop-blur-sm"
    @click.self="close"
  >
    <div class="w-full max-w-md overflow-hidden rounded-2xl border border-white/10 bg-ink-800 shadow-2xl">
      <!-- Header -->
      <div class="flex items-start justify-between border-b border-white/5 bg-ink-900/60 px-5 py-4">
        <div>
          <h2 class="flex items-center gap-2 text-base font-bold text-white">
            <span>✨</span>{{ t('changelog.title') }}
          </h2>
          <p class="mt-0.5 text-xs text-slate-400">{{ t('changelog.subtitle') }}</p>
        </div>
        <span class="rounded-md bg-accent/15 px-2 py-1 text-xs font-bold text-accent">v{{ appVersion }}</span>
      </div>

      <!-- Release list -->
      <div class="max-h-[60vh] space-y-4 overflow-y-auto px-5 py-4">
        <div v-for="r in releases" :key="r.version">
          <div class="mb-1.5 flex items-center gap-2">
            <span class="text-sm font-bold text-white">v{{ r.version }}</span>
            <span v-if="r.version === appVersion" class="rounded bg-emerald-400/15 px-1.5 py-0.5 text-[10px] font-bold uppercase text-emerald-300">New</span>
          </div>
          <ul class="space-y-1">
            <li v-for="(c, i) in r.changes" :key="i" class="flex gap-2 text-sm text-slate-300">
              <span class="text-accent">•</span><span>{{ changeText(c) }}</span>
            </li>
          </ul>
        </div>
      </div>

      <!-- Footer -->
      <div class="border-t border-white/5 px-5 py-4">
        <button
          type="button"
          @click="close"
          class="w-full rounded-lg bg-accent px-4 py-2.5 text-sm font-bold text-ink-900 transition hover:bg-accent-soft"
        >{{ t('changelog.close') }}</button>
      </div>
    </div>
  </div>
</template>
