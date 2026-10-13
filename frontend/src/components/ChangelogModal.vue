<script lang="ts" setup>
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()

// Build-time app version (from wails.json's info.productVersion, injected by Vite).
const appVersion = __APP_VERSION__

// Changelog — newest first. Each change carries all supported languages so it follows the
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
    version: '1.2.21',
    changes: [
      { en: 'New sub-switch under Game boost: "Game thread on its own core (experimental)". While the boost is applied, the game\'s main thread (UE GameThread) is pinned to one CPU of the last core and every other game thread, plus this app, is kept off that core, so a burst of worker threads never shares its core. Re-checked every few seconds for threads the game creates later; off puts every thread back.', th: 'เพิ่มสวิตช์ย่อยใต้บูสต์เกม: "แยก GameThread ไว้คอร์เดียว (ทดลอง)" ขณะบูสต์ทำงาน เธรดหลักของเกม (UE GameThread) จะถูกล็อกไว้บน CPU เดียวของคอร์สุดท้าย และกันเธรดอื่นของเกมกับแอปนี้ออกจากคอร์นั้น เธรดงานที่ตื่นพร้อมกันจะไม่มาแชร์คอร์อีก ตรวจซ้ำทุกไม่กี่วินาทีสำหรับเธรดที่เกมสร้างทีหลัง ปิดแล้วทุกเธรดกลับเป็นเดิม', zh: '遊戲加速下新增子開關：「遊戲主執行緒獨占核心（實驗）」。加速生效時，遊戲主執行緒（UE GameThread）會固定在最後一個核心的單一 CPU，遊戲的其他執行緒與本程式則避開該核心，工作執行緒突然喚醒時不會再與它共用核心。每隔幾秒會重新檢查遊戲稍後建立的執行緒；關閉後所有執行緒還原。' },
      { en: 'The status line reports where the threads went (e.g. "GameThread → CPU 14, 60 threads → CPU 0-13") or why the game refused.', th: 'บรรทัดสถานะบอกว่าเธรดไปอยู่ที่ไหน (เช่น "GameThread → CPU 14, 60 threads → CPU 0-13") หรือเหตุผลที่ทำไม่ได้', zh: '狀態列會顯示執行緒的去向（例如「GameThread → CPU 14, 60 threads → CPU 0-13」）或遊戲拒絕的原因。' },
      { en: '"Hide others\' skill animations" is now armed only while your locked caster has cast a configured skill within the last 20 s. Entity ids change on every instance entry, so a lock left over from the previous run used to turn your own casts into dodges (you could only dash, and could never re-lock). The mask now pauses instead, your casts play normally, and its status line says why it is paused.', th: '"ซ่อนท่าสกิลของคนอื่น" จะทำงานเฉพาะตอนที่ caster ที่ล็อกไว้กดสกิลที่ตั้งไว้ภายใน 20 วินาทีล่าสุด เพราะ ID ตัวละครเปลี่ยนทุกครั้งที่เข้าดัน ล็อกเก่าที่ค้างจากรอบก่อนเคยทำให้ท่าของคุณเองกลายเป็น dodge (ได้แต่แดช และล็อกใหม่ไม่ได้) ตอนนี้จะพักการซ่อนแทน ท่าของคุณเล่นปกติ และบรรทัดสถานะบอกเหตุผลที่พัก', zh: '「隱藏其他玩家的技能動作」現在只在你鎖定的施法者於最近 20 秒內施放過已設定技能時才生效。每次進入副本角色 ID 都會改變，先前殘留的鎖定會把你自己的施法變成閃避（只能衝刺、也無法重新鎖定）。現在改為暫停遮蔽，你的施法正常播放，狀態列會說明暫停原因。' },
      { en: 'Casts from the character bound to your configured name are never masked, whatever the caster lock says.', th: 'ท่าของตัวละครที่ผูกกับชื่อที่คุณตั้งไว้จะไม่ถูกซ่อนเด็ดขาด ไม่ว่าล็อก caster จะชี้ไปที่ใคร', zh: '與你設定名稱綁定的角色，其施法永遠不會被遮蔽，無論施法者鎖定指向誰。' },
    ],
  },
  {
    version: '1.2.20',
    changes: [
      { en: 'New "Hide others\' skill animations" switch in Ping Maker: while capture runs, every other player\'s cast packet is rewritten to that player\'s class dodge, so their skill animations and effects never play on your client. Your own casts keep their animation; bosses and monsters are never touched.', th: 'เพิ่มสวิตช์ "ซ่อนท่าสกิลของคนอื่น" ใน Ping Maker: ขณะดักแพ็กเก็ต คาสต์ของผู้เล่นคนอื่นทุกคนจะถูกเปลี่ยนเป็นท่า dodge ของอาชีพนั้น ท่าและเอฟเฟคสกิลของเขาจะไม่เล่นบนเครื่องคุณ ของคุณเองยังปกติ บอส/มอนสเตอร์ไม่ถูกแตะ', zh: 'Ping Maker 新增「隱藏其他玩家的技能動作」開關：擷取執行時，其他玩家的每個施法封包都會改寫為該職業的閃避動作，技能動作與特效不會在你的客戶端播放。你自己的施法保持原樣，首領與怪物不受影響。' },
      { en: 'The rewrite now uses each class\'s own dodge id (11000101 … 18000101) instead of one fixed id, so every class animates correctly.', th: 'การเปลี่ยน id ใช้ท่า dodge ของแต่ละอาชีพ (11000101 … 18000101) แทน id เดียวแบบเดิม ทุกอาชีพจึงแสดงถูกต้อง', zh: '改寫現在使用各職業自己的閃避 id（11000101 … 18000101），而非單一固定 id，所有職業都能正確播放。' },
    ],
  },
  {
    version: '1.2.19',
    changes: [
      { en: 'New "Game boost" switch in Ping Maker: while Aion2.exe is running it is kept at High priority and pinned to the performance cores (P-cores on hybrid Intel CPUs; the affinity is left alone on CPUs without efficiency cores). Switching it off restores the process to what it had before.', th: 'เพิ่มสวิตช์ "บูสต์เกม" ใน Ping Maker: ขณะ Aion2.exe เปิดอยู่ แอพจะคง priority High และล็อกเกมไว้บน P-core (CPU ที่ไม่มี E-core จะไม่เปลี่ยน affinity) ปิดสวิตช์แล้วโปรเซสจะกลับเป็นค่าเดิม', zh: 'Ping Maker 新增「遊戲加速」開關：Aion2.exe 執行時會維持高優先權並固定在效能核心（混合架構 Intel CPU 的 P-core；沒有效率核心的 CPU 不變更親和性）。關閉後會還原先前的設定。' },
      { en: 'The packet engine now runs its capture thread at raised priority, and the app itself one class above normal, so a busy game, browser or Discord cannot delay packet re-injection.', th: 'ตัวดักแพ็กเก็ตรัน thread ที่ priority สูงขึ้น และตัวแอพเองสูงกว่าปกติหนึ่งขั้น เพื่อไม่ให้เกม เบราว์เซอร์ หรือ Discord ที่ยุ่งอยู่หน่วงการส่งแพ็กเก็ตกลับ', zh: '封包引擎的擷取執行緒改以較高優先權執行，應用程式本身也提高一級，避免忙碌的遊戲、瀏覽器或 Discord 延遲封包重新注入。' },
    ],
  },
  {
    version: '1.2.18',
    changes: [
      { en: 'Automatic caster selection now requires a full match to the configured character name, preventing Maliwan from being confused with Mali茉莉 or another player.', th: 'การเลือก caster อัตโนมัติต้องตรงกับชื่อตัวละครที่ตั้งไว้ทั้งชื่อ ป้องกันการสับสนระหว่าง Maliwan กับ Mali茉莉 หรือผู้เล่นคนอื่น', zh: '自動選擇施法者現在必須完整符合設定的角色名稱，避免將 Maliwan 誤認為 Mali茉莉 或其他玩家。' },
      { en: 'With a character name configured, Ping Maker no longer guesses your identity from the first skill cast or a pause in combat. Manual caster selection remains available when the name record was missed.', th: 'เมื่อตั้งชื่อตัวละครไว้ Ping Maker จะไม่เดาว่าใครเป็นตัวคุณจากคนที่ออกสกิลก่อนหรือช่วงหยุดต่อสู้ หากพลาดข้อมูลชื่อ ยังเลือก caster เองได้', zh: '設定角色名稱後，Ping Maker 不再根據首次施法或戰鬥間隔猜測你的身分。若錯過名稱資料，仍可手動選擇施法者。' },
      { en: 'Changing the configured name clears the previous caster selection. Identity scanning now skips unrelated names and continues looking for the configured character in the same packet.', th: 'เมื่อเปลี่ยนชื่อตัวละคร ระบบจะล้าง caster ที่เลือกไว้เดิม และการอ่านข้อมูลจะข้ามชื่อที่ไม่ตรงเพื่อค้นหาตัวละครที่ตั้งไว้ต่อในแพ็กเก็ตเดียวกัน', zh: '變更設定的角色名稱時會清除先前選取的施法者。身分掃描現在會略過不相符的名稱，繼續在同一封包中尋找設定的角色。' },
    ],
  },
  {
    version: '1.2.17',
    changes: [
      { en: 'The app now opens directly into Ping Maker, with skill settings, caster selection, language selection, Start/Stop, and the capture log on one screen.', th: 'แอพเปิดเข้า Ping Maker โดยตรง รวมค่าสกิล การเลือก caster ภาษา ปุ่มเริ่ม/หยุด และ log ไว้ในหน้าเดียว', zh: '應用程式現在直接開啟 Ping Maker，技能設定、施法者選擇、語言選擇、啟動／停止與擷取紀錄都集中於同一畫面。' },
      { en: 'Removed Latency Lab, Rotation Lab, Macro Optimizer, Mod, Packet Inspector, and the separate Settings page from the app, along with their backend bindings.', th: 'นำ Latency Lab, Rotation Lab, Macro Optimizer, Mod, Packet Inspector และหน้าตั้งค่าแยกออกจากแอพ พร้อมถอดคำสั่ง backend ของฟีเจอร์เหล่านี้', zh: '移除應用程式中的 Latency Lab、Rotation Lab、Macro Optimizer、Mod、Packet Inspector 與獨立設定頁面，以及這些功能的後端繫結。' },
      { en: 'Existing Ping Maker skill and speed settings are preserved. External Speeder macros and installed game mods are not changed.', th: 'ค่าสกิลและความเร็วเดิมของ Ping Maker ยังคงอยู่ โดยไม่เปลี่ยนมาโคร Speeder ภายนอกหรือม็อดที่ติดตั้งในเกม', zh: '保留既有的 Ping Maker 技能與速度設定，不變更外部 Speeder 巨集或已安裝的遊戲模組。' },
    ],
  },
  {
    version: '1.2.16',
    changes: [
      { en: '"Disable Skill Effects" supports game builds 97 and 98.', th: '"ปิดเอฟเฟกต์สกิล" รองรับเกมเวอร์ชัน 97 และ 98', zh: '「關閉技能特效」支援遊戲版本 97 與 98。' },
    ],
  },
  {
    version: '1.2.15',
    changes: [
      { en: 'Performance Config now strips more scenery detail — decorative props, simpler surface shaders, coarser distant geometry and less foliage.', th: 'Performance Config ตัดรายละเอียดฉากได้มากขึ้น — ของประดับ, พื้นผิวแบบเรียบง่าย, ฉากไกลหยาบลง และลดต้นไม้ใบหญ้า', zh: 'Performance Config 現在會移除更多場景細節 — 裝飾物件、更簡化的表面著色、更粗略的遠景幾何與更少的植被。' },
      { en: 'Performance Config no longer shrinks and blurs textures on cards with plenty of memory. It now asks for a large texture budget and lets the game scale that back on smaller cards.', th: 'Performance Config ไม่ลดและทำให้ texture เบลอบนการ์ดที่มีหน่วยความจำเหลือเฟืออีกแล้ว — ตอนนี้ขอพื้นที่ texture เยอะไว้ก่อน แล้วให้เกมลดเองบนการ์ดที่เล็กกว่า', zh: 'Performance Config 不再於記憶體充足的顯示卡上縮減並模糊貼圖。現在會要求較大的貼圖配額，並讓遊戲在較小的顯示卡上自行縮減。' },
      { en: '"Disable Skill Effects" supports game build 94.', th: '"ปิดเอฟเฟกต์สกิล" รองรับเกมเวอร์ชัน 94', zh: '「關閉技能特效」支援遊戲版本 94。' },
    ],
  },
  {
    version: '1.2.14',
    changes: [
      { en: 'Fixed Performance Config not turning off. The game rewrites its config file and strips comments, which the mod used to find its own settings — so the switch read as off while the settings stayed, and enabling it again just added another copy. It now finds its settings by name, cleans up any duplicates it left behind, and turns off properly.', th: 'แก้ปัญหา Performance Config ปิดไม่ได้ — เกมเขียนไฟล์ config ใหม่แล้วตัดคอมเมนต์ที่มอดใช้ระบุค่าของตัวเองทิ้ง ทำให้สวิตช์ขึ้นว่าปิดทั้งที่ค่ายังอยู่ และกดเปิดใหม่ก็เพิ่มค่าซ้ำเข้าไปอีก ตอนนี้ระบุค่าด้วยชื่อแทน ล้างค่าซ้ำที่ค้างไว้ให้ และปิดได้จริงแล้ว', zh: '修正 Performance Config 無法關閉的問題。遊戲會重寫設定檔並移除註解，而模組原本靠註解辨識自己的設定 — 導致開關顯示為關閉但設定仍在，再次啟用只會重複寫入。現在改以設定名稱辨識，會清理殘留的重複項目，並能正常關閉。' },
      { en: 'Performance Config now sizes the texture pool for 8GB graphics cards, leaving room for DLSS Frame Generation.', th: 'Performance Config ปรับขนาด texture pool ให้เหมาะกับการ์ดจอ 8GB เผื่อพื้นที่ให้ DLSS Frame Generation', zh: 'Performance Config 現在會依 8GB 顯示卡調整貼圖快取大小，為 DLSS 影格生成保留空間。' },
      { en: '"Disable Skill Effects" supports game build 93.', th: '"ปิดเอฟเฟกต์สกิล" รองรับเกมเวอร์ชัน 93', zh: '「關閉技能特效」支援遊戲版本 93。' },
    ],
  },
  {
    version: '1.2.13',
    changes: [
      { en: 'New Mod: "Performance Config (max FPS)" — one toggle that writes an aggressive graphics config (Lumen & shadows off, post-processing off, cheaper effects and crowds, DLSS Frame Generation for RTX 40-series). Fully reversible; re-enable it after a game patch. Boss telegraph effects stay visible.', th: 'มอดใหม่: "Performance Config (เน้น FPS สูงสุด)" — สวิตช์เดียวเขียนคอนฟิกกราฟิกแบบรีด FPS สุด (ปิด Lumen และเงา, ปิด post-processing, ลดเอฟเฟกต์และฝูงคน, เปิด DLSS Frame Generation สำหรับ RTX 40) ย้อนกลับได้ทั้งหมด — หลังเกมแพตช์ให้กดเปิดใหม่ วงเตือนสกิลบอสยังแสดงปกติ', zh: '新模組：「Performance Config（極限 FPS）」— 一鍵寫入激進圖形設定（關閉 Lumen 與陰影、關閉後處理、降低特效與人群細節、為 RTX 40 系列開啟 DLSS 影格生成）。完全可還原；遊戲更新後請重新開啟。Boss 警示特效仍會顯示。' },
      { en: '"Disable Skill Effects" supports game builds 91 and 92.', th: '"ปิดเอฟเฟกต์สกิล" รองรับเกมเวอร์ชัน 91 และ 92', zh: '「關閉技能特效」支援遊戲版本 91 與 92。' },
    ],
  },
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
