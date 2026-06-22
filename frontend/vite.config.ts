import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'
import {readFileSync} from 'node:fs'
import {fileURLToPath} from 'node:url'

// Single source of truth for the app version: wails.json's info.productVersion
// (the same value that stamps the installer). Injected here so the sidebar and
// the installer can never drift apart — bump it in one place.
const wailsConfig = JSON.parse(
  readFileSync(fileURLToPath(new URL('../wails.json', import.meta.url)), 'utf-8'),
)
const appVersion: string = wailsConfig?.info?.productVersion ?? '0.0.0'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
  },
})
