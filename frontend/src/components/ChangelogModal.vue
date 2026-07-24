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
  zh?: string // Traditional Chinese (Taiwan); falls back to en if absent
}
interface Release {
  version: string
  changes: Change[]
}
const releases: Release[] = [
  {
    version: '1.2.12',
    changes: [
      { en: 'Ping Maker picks your caster up again by itself after you re-enter a dungeon — cast one of your configured skills and it locks on. If a party member plays your class, pick yourself from the dropdown as before.', th: 'Ping Maker จับ caster ของคุณเองได้อีกครั้งหลังลงดันใหม่ — ร่ายสกิลที่ตั้งค่าไว้ 1 ครั้งแล้วมันจะล็อกให้เอง ถ้ามีเพื่อนอาชีพเดียวกันในปาร์ตี้ ให้เลือกตัวเองจาก dropdown เหมือนเดิม', zh: 'Ping Maker 在重新進入副本後會自行重新鎖定你的施法者 — 施放一次已設定的技能即可。若隊伍中有相同職業的隊友，仍請從下拉選單選擇自己。' },
      { en: 'The log now shows only the casts Ping Maker actually changed, instead of every skill it saw.', th: 'ช่อง log แสดงเฉพาะสกิลที่ Ping Maker แก้ไขจริง ไม่แสดงทุกสกิลที่มองเห็นแล้ว', zh: '紀錄現在只顯示 Ping Maker 實際修改過的技能，不再列出所有偵測到的技能。' },
      { en: '"Disable Skill Effects" supports game builds 87 to 89.', th: '"ปิดเอฟเฟกต์สกิล" รองรับเกมเวอร์ชัน 87 ถึง 89', zh: '「關閉技能特效」支援遊戲版本 87 至 89。' },
    ],
  },
  {
    version: '1.2.11',
    changes: [
      { en: '"Disable Skill Effects" now supports game builds 87 and 88. Support for new game builds arrives on its own — you no longer need to update the app after most game patches.', th: '"ปิดเอฟเฟกต์สกิล" รองรับเกมเวอร์ชัน 87 และ 88 แล้ว — การรองรับเวอร์ชันใหม่จะมาเองอัตโนมัติ ไม่ต้องอัปเดตแอปหลังเกมแพตช์อีกต่อไป (ในกรณีส่วนใหญ่)', zh: '「關閉技能特效」現已支援遊戲版本 87 與 88。新版本的支援會自動送達 — 大多數遊戲更新後不再需要更新此應用程式。' },
    ],
  },
  {
    version: '1.2.10',
    changes: [
      { en: 'Updated "Disable Skill Effects" for game build 86. If you already had it on, re-enable it in the Mod menu after this update — no re-download needed.', th: 'อัปเดต "ปิดเอฟเฟกต์สกิล" ให้รองรับเกมเวอร์ชัน 86 — ถ้าเคยเปิดไว้อยู่แล้ว เปิดใหม่ในเมนู Mod หลังอัปเดตนี้ได้เลย ไม่ต้องโหลดไฟล์ใหม่', zh: '更新「關閉技能特效」以支援遊戲版本 86 — 若先前已開啟，更新後在「模組」選單重新開啟即可，無需重新下載。' },
    ],
  },
  {
    version: '1.2.9',
    changes: [
      { en: 'Updated "Disable Skill Effects" for game build 85. Re-enable it in the Mod menu after this update.', th: 'อัปเดต "ปิดเอฟเฟกต์สกิล" ให้รองรับเกมเวอร์ชัน 85 — เปิดใช้งานใหม่ในเมนู Mod หลังอัปเดตนี้', zh: '更新「關閉技能特效」以支援遊戲版本 85 — 更新後請在「模組」選單重新開啟。' },
    ],
  },
  {
    version: '1.2.8',
    changes: [
      { en: 'Updated "Disable Skill Effects" for game build 84. Re-enable it in the Mod menu after this update.', th: 'อัปเดต "ปิดเอฟเฟกต์สกิล" ให้รองรับเกมเวอร์ชัน 84 — เปิดใช้งานใหม่ในเมนู Mod หลังอัปเดตนี้', zh: '更新「關閉技能特效」以支援遊戲版本 84 — 更新後請在「模組」選單重新開啟。' },
      { en: 'The mod now picks up support for new game builds on its own — after a game patch you no longer have to update the app to keep "Disable Skill Effects" working.', th: 'มอดจะรับรองรับเกมเวอร์ชันใหม่ได้เอง — หลังเกมแพตช์ ไม่ต้องอัปเดตแอปเพื่อให้ "ปิดเอฟเฟกต์สกิล" ใช้งานต่อได้อีกแล้ว', zh: '模組現在會自行取得對新遊戲版本的支援 — 遊戲更新後，不必再更新此應用程式即可繼續使用「關閉技能特效」。' },
    ],
  },
  {
    version: '1.2.7',
    changes: [
      { en: 'Internal cleanup and maintenance.', th: 'ปรับปรุงโค้ดภายในและล้างไฟล์ที่ไม่ใช้', zh: '內部整理與維護。' },
    ],
  },
  {
    version: '1.2.6',
    changes: [
      { en: 'Added Traditional Chinese (Taiwan) — pick 繁體中文 in Settings.', th: 'เพิ่มภาษาจีนตัวเต็ม (ไต้หวัน) — เลือก 繁體中文 ได้ในเมนูตั้งค่า', zh: '新增繁體中文（台灣）— 可在「設定」中選擇繁體中文。' },
    ],
  },
  {
    version: '1.2.5',
    changes: [
      { en: 'Updated "Disable Skill Effects" for the latest game build. Re-enable it in the Mod menu after this update.', th: 'อัปเดต "ปิดเอฟเฟกต์สกิล" ให้รองรับเกมเวอร์ชันล่าสุด — เปิดใช้งานใหม่ในเมนู Mod หลังอัปเดตนี้', zh: '更新「關閉技能特效」以支援最新遊戲版本 — 更新後請在「模組」選單重新開啟。' },
    ],
  },
  {
    version: '1.2.4',
    changes: [
      { en: 'Removed the "Render as" skill-replacement feature from Ping Maker.', th: 'เอาฟีเจอร์ "แสดงเป็น" (แทนสกิล) ออกจาก Ping Maker', zh: '從 Ping Maker 移除「顯示為」（技能替換）功能。' },
    ],
  },
  {
    version: '1.2.3',
    changes: [
      { en: 'Ping Maker: each skill can now be rendered as another skill — pick a "Render as" target (with icons) to show a different cast animation/effect (client-side only; the real skill still fires).', th: 'Ping Maker: แต่ละสกิลเลือกให้แสดงเป็นอีกสกิลได้แล้ว — เลือก "แสดงเป็น" (มีรูปสกิล) เพื่อให้ร่ายออกมาเป็นท่า/เอฟเฟกต์ของสกิลอื่น (เปลี่ยนแค่ภาพ สกิลจริงยังทำงานเหมือนเดิม)', zh: 'Ping Maker：每個技能現在都能顯示為另一個技能 — 選擇「顯示為」目標（附圖示）即可呈現不同的施放動作／特效（僅改變畫面，實際仍施放原技能）。' },
      { en: 'Caster selection is now sticky — once you pick your caster it no longer flips to a same-class teammate who casts your skill; they just appear as a selectable option.', th: 'การเลือก caster อยู่หมัดแล้ว — พอเลือกตัวเองแล้วจะไม่เด้งไปหาเพื่อนคลาสเดียวกันที่ร่ายสกิลเดียวกัน (เพื่อนจะโผล่เป็นตัวเลือกให้เลือกเองแทน)', zh: '施法者選擇現在會固定 — 選好自己的施法者後，就不會再跳到施放相同技能的同職業隊友；他們只會以可選項目出現。' },
      { en: 'Removed the Break button from skill rows.', th: 'เอาปุ่ม Break ออกจากแถวสกิล', zh: '從技能列移除「突破」按鈕。' },
    ],
  },
  {
    version: '1.2.2',
    changes: [
      { en: 'Updated "Disable Skill Effects" for the latest game build (adds the new class). Re-enable it in the Mod menu after this update.', th: 'อัปเดต "ปิดเอฟเฟกต์สกิล" ให้รองรับเกมเวอร์ชันล่าสุด (รวมคลาสใหม่) — เปิดใช้งานใหม่ในเมนู Mod หลังอัปเดตนี้', zh: '更新「關閉技能特效」以支援最新遊戲版本（含新職業）— 更新後請在「模組」選單重新開啟。' },
    ],
  },
  {
    version: '1.2.1',
    changes: [
      { en: 'Moved the "Disable other players\' skill animations" toggle into the Mod menu, alongside the other mods.', th: 'ย้ายปุ่ม "ปิดอนิเมชั่นสกิลของผู้เล่นคนอื่น" ไปไว้ในเมนู Mod รวมกับม็อดอื่นๆ', zh: '將「關閉其他玩家的技能動作」開關移至「模組」選單，與其他模組並列。' },
    ],
  },
  {
    version: '1.2.0',
    changes: [
      { en: 'New Mod: "Disable Skill Effects" — removes every class\'s skill cast visual effects on your screen for higher FPS and a clearer view. Downloads a mod pack on first enable; restart the game to apply; fully reversible.', th: 'ม็อดใหม่ "ปิดเอฟเฟกต์สกิล" — ลบเอฟเฟกต์ตอนร่ายสกิลของทุกคลาสบนจอของคุณ เพื่อ FPS สูงขึ้นและจอโล่ง โหลดม็อดครั้งแรกที่เปิด รีสตาร์ตเกมเพื่อให้มีผล ย้อนกลับได้ทั้งหมด', zh: '新模組「關閉技能特效」— 移除畫面上所有職業的技能施放視覺特效，提升 FPS 並讓畫面更清晰。首次開啟時下載模組包，需重新啟動遊戲才生效，可完全還原。' },
    ],
  },
  {
    version: '1.1.2',
    changes: [
      { en: 'Fixed caster auto-detect in party play — it now locks onto your own caster instantly from your edit-list skills, instead of flipping to teammates and skipping your speed edits', th: 'แก้ caster auto-detect ในปาร์ตี้ — ล็อก caster ของคุณทันทีจากสกิลใน edit list แทนที่จะสลับไปหาเพื่อนจนข้ามการแก้ speed ของคุณ', zh: '修正組隊時的施法者自動偵測 — 現在會立即從你編輯清單中的技能鎖定你自己的施法者，而不會跳到隊友並略過你的速度修改。' },
    ],
  },
  {
    version: '1.1.1',
    changes: [
      { en: 'Removed the Overlay (always-on-top HUD) mode', th: 'เอาโหมด Overlay (ปักหมุดหน้าต่างทับเกม) ออก', zh: '移除「疊層」（永遠置頂 HUD）模式。' },
      { en: 'Cleaner Ping Maker log output', th: 'ปรับ log ของ Ping Maker ให้สะอาดขึ้น', zh: '讓 Ping Maker 的紀錄輸出更簡潔。' },
    ],
  },
  {
    version: '1.1.0',
    changes: [
      { en: 'Ping Maker now edits combat speed inside LZ4-compressed party packets — boosts apply to ~100% of party casts (no more occasional slow casts)', th: 'Ping Maker แก้ความเร็วในแพ็กเก็ตปาร์ตี้ที่ถูกบีบอัด (LZ4) ได้แล้ว — บูสต์ติดเกือบ 100% ของการร่ายในปาร์ตี้ (ไม่มีร่ายช้าเป็นบางครั้งอีก)', zh: 'Ping Maker 現在能修改 LZ4 壓縮組隊封包內的戰鬥速度 — 加成套用到約 100% 的組隊施放（不再偶爾出現慢速施放）。' },
      { en: 'Fixed a reconnect/disconnect issue during play', th: 'แก้ปัญหา reconnect/หลุดการเชื่อมต่อระหว่างเล่น', zh: '修正遊玩過程中的重連／斷線問題。' },
      { en: 'Added image icons for all skills', th: 'เพิ่มไอคอนรูปภาพให้สกิลทั้งหมด', zh: '為所有技能加入圖片圖示。' },
    ],
  },
  {
    version: '1.0.7',
    changes: [
      { en: 'Added caster data recording and caster-prioritized Ping Maker logs', th: 'เพิ่มการบันทึกข้อมูล Caster และกรองบันทึก Ping Maker ตาม Caster', zh: '新增施法者資料記錄，以及以施法者為優先的 Ping Maker 紀錄。' },
    ],
  },
  {
    version: '1.0.6',
    changes: [
      { en: 'Improved Ping Maker compact-speed parsing for party play and reduced reconnect risk', th: 'ปรับปรุง Ping Maker สำหรับปาร์ตี้ ลดโอกาส reconnect และจับ compact speed ได้ดีขึ้น', zh: '改善組隊時 Ping Maker 的戰鬥速度解析，並降低重連風險。' },
    ],
  },
  {
    version: '1.0.5',
    changes: [
      { en: 'Improved Ping Maker to work well in party dungeons', th: 'ปรับปรุง Ping Maker ให้ทำงานได้ดีในปาร์ตี้ดันเจี้ยน', zh: '改善 Ping Maker，讓它在組隊副本中運作良好。' },
    ],
  },
  {
    version: '1.0.4',
    changes: [
      { en: 'Fixed Ping Maker bug', th: 'แก้บั๊ก Ping Maker', zh: '修正 Ping Maker 的錯誤。' },
      { en: 'Added TCP & Network tuning mods', th: 'เพิ่มม็อด TCP และปรับแต่งเครือข่าย', zh: '新增 TCP 與網路調校模組。' },
    ],
  },
  {
    version: '1.0.3',
    changes: [
      { en: 'Improved Ping Maker', th: 'ปรับปรุงฟีเจอร์ Ping Maker', zh: '改善 Ping Maker。' },
      { en: 'Added Mod feature', th: 'เพิ่มฟีเจอร์ Mod', zh: '新增「模組」功能。' },
    ],
  },
  { version: '1.0.2', changes: [{ en: 'Added Memory Reader', th: 'เพิ่มฟีเจอร์ Memory Reader', zh: '新增記憶體讀取器。' }] },
  { version: '1.0.1', changes: [{ en: 'Added Caster Filter', th: 'เพิ่มฟีเจอร์ Caster Filter', zh: '新增施法者篩選。' }] },
  { version: '1.0.0', changes: [{ en: 'Added Ping Maker', th: 'เพิ่มฟีเจอร์ Ping Maker', zh: '新增 Ping Maker。' }] },
]

function changeText(c: Change) {
  if (locale.value === 'th') return c.th
  if (locale.value === 'zh') return c.zh ?? c.en
  return c.en
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
