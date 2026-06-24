import type { Component } from 'vue'
import Inspector from './inspector/Inspector.vue'
import MemProbe from './memread/MemProbe.vue'
import Mod from './mod/Mod.vue'
import PingMaker from './pingmaker/PingMaker.vue'
import Settings from './settings/Settings.vue'

// A "project" is one menu/tool inside Aion 2 Buddy. Adding a new menu = drop a
// Vue component here, register it below, and add its label/description to the
// i18n files under `menus.<id>`.
export interface ProjectMenu {
  id: string
  icon: string
  component?: Component
  comingSoon?: boolean
  pinBottom?: boolean // rendered at the bottom of the sidebar (e.g. Settings)
  dev?: boolean // only shown when Development mode is on
}

export const projects: ProjectMenu[] = [
  { id: 'pingmaker', icon: '📍', component: PingMaker },
  { id: 'mod', icon: '🧩', component: Mod },
  { id: 'inspector', icon: '🔍', component: Inspector, dev: true },
  { id: 'memread', icon: '🧠', component: MemProbe, dev: true },
  { id: 'settings', icon: '⚙️', component: Settings, pinBottom: true },
]
