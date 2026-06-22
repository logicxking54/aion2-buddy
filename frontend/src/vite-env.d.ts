/// <reference types="vite/client" />

// Injected by Vite at build time from wails.json's info.productVersion.
declare const __APP_VERSION__: string

declare module '*.vue' {
    import type {DefineComponent} from 'vue'
    const component: DefineComponent<{}, {}, any>
    export default component
}
