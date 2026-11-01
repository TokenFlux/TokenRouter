import { definePlugin } from '@tokenflux/tokenrouter-plugin-sdk'
import Page from './Page.vue'

// entry.ts registers the smallest Vue consumer used by SDK build tests.
// One static page and one dynamically imported page exercise the public
// registration contract without carrying business data, release assets, or
// a second plugin implementation alongside the official repositories.

definePlugin({
  pages: {
    home: Page,
    lazy: () => import('./LazyPage.vue').then((module) => module.default),
  },
})
