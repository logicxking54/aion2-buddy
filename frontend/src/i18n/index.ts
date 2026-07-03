import { createI18n } from 'vue-i18n'
import { config, saveConfig } from '../config'
import en from './en'
import th from './th'
import zh from './zh'

export type Locale = 'th' | 'en' | 'zh'

const saved = localStorage.getItem('locale')
const initial: Locale =
  saved === 'en' || saved === 'th' || saved === 'zh' ? saved : 'th' // default: Thai

export const i18n = createI18n({
  legacy: false,
  locale: initial,
  fallbackLocale: 'en',
  messages: { en, th, zh },
})

document.documentElement.setAttribute('lang', initial)

export function setLocale(locale: Locale) {
  i18n.global.locale.value = locale
  localStorage.setItem('locale', locale) // fast synchronous read on next startup
  document.documentElement.setAttribute('lang', locale)
  config.language = locale // and persist into the shared JSON config
  saveConfig()
}
