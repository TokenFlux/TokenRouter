import { definePlugin } from '@tokenflux/tokenrouter-plugin-sdk'
import HomePage from './pages/HomePage.vue'
import './style.css'

// index.ts registers the example's user and administrator pages with the host.
// Registration runs synchronously while the host script element identifies this
// plugin. The administrator component loads lazily in source and is inlined
// into the same production IIFE by the shared build preset.

definePlugin({
  pages: {
    home: HomePage,
    admin: () => import('./pages/AdminPage.vue').then((module) => module.default),
  },
})
