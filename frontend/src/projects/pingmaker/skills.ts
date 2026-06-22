import skillData from './skills.data.json'

// The three Aion 2 archetypes used to filter the skill list. A skill's `class`
// is an array of these ids — one skill can belong to several classes.
export type SkillClass = 'sorcerer'

export interface ClassMeta {
  id: SkillClass
  label: string // fallback label; UI translates via i18n
  icon: string
  color: string
}

export const classes: ClassMeta[] = [
  { id: 'sorcerer', label: 'Sorcerer', icon: '🔮', color: '#a855f7' },
]

export interface Skill {
  id: string // unique slug, used as list key
  name: string
  skill_ids: number[] // in-game skill IDs (all variants/levels) for packet matching
  image_url: string // may be empty; the UI shows an error image when blank/broken
  class: string // may be empty (unclassified); fill in skills.data.json
}

// Neutral look for skills that have no class assigned yet.
const NEUTRAL = { icon: '✦', color: '#64748b' }

function slugify(name: string) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/(^-|-$)/g, '')
}

// Build the skill list from skills.data.json, guaranteeing unique ids.
const seen = new Map<string, number>()
export const skills: Skill[] = (
  skillData as Array<{ name: string; skill_ids?: number[]; image_url?: string; class?: string | string[] }>
).map((s) => {
  let id = slugify(s.name)
  const n = seen.get(id) ?? 0
  seen.set(id, n + 1)
  if (n > 0) id = `${id}-${n}`
  // Tolerate either a string or a legacy array in the data file.
  const cls = Array.isArray(s.class) ? (s.class[0] ?? '') : (s.class ?? '')
  return {
    id,
    name: s.name,
    skill_ids: Array.isArray(s.skill_ids) ? s.skill_ids : [],
    image_url: s.image_url ?? '',
    class: cls,
  }
})

// Map every in-game numeric skill ID (all variants) to its Skill, so cast
// events from the capture engine can be resolved to a Skill for display.
const byNumericId = new Map<number, Skill>()
for (const s of skills) for (const n of s.skill_ids) byNumericId.set(n, s)
export function skillByNumericId(id: number): Skill | undefined {
  return byNumericId.get(id)
}

// Charge skills (e.g. Hellfire) are split across several catalog entries — a
// base form plus "- Level 1/2" and "- Max" tiers — each with its own in-game
// IDs. Holding to charge and releasing casts under a *tier* ID, not the base.
// So a combat-speed row added for the base would never match the charged cast.
// chargeBaseName() strips the tier suffix so all tiers share a base key.
const CHARGE_SUFFIX = /\s-\s(?:Level\s+\d+|Max)$/i
export function chargeBaseName(name: string): string {
  return name.replace(CHARGE_SUFFIX, '')
}

// Group every skill's IDs by its charge-base name, so any tier resolves to the
// full set of IDs across all tiers of that charge skill.
const idsByChargeBase = new Map<string, number[]>()
for (const s of skills) {
  const base = chargeBaseName(s.name)
  const arr = idsByChargeBase.get(base) ?? []
  arr.push(...s.skill_ids)
  idsByChargeBase.set(base, arr)
}

// relatedSkillIds returns a skill's own IDs plus those of every charge-tier
// sibling, so configuring one tier (or the base) covers casts at any charge
// level. Non-charge skills just get their own IDs back.
export function relatedSkillIds(s: Skill): number[] {
  const all = idsByChargeBase.get(chargeBaseName(s.name))
  if (!all || all.length === s.skill_ids.length) return s.skill_ids
  return Array.from(new Set([...s.skill_ids, ...all]))
}

export function classMeta(id: string): ClassMeta | undefined {
  return classes.find((c) => c.id === id)
}

// Display helpers — a skill has no icon/color of its own, so derive from its
// class, falling back to a neutral look when unclassified.
export function skillColor(s: Skill): string {
  return classMeta(s.class)?.color ?? NEUTRAL.color
}

export function skillIcon(s: Skill): string {
  return classMeta(s.class)?.icon ?? NEUTRAL.icon
}
