import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { h, type App } from 'vue'

// development.spec.ts mounts the SDK's actual development entry in a browser fixture.
// It verifies that local authoring retains the same functional-page and lazy-loader
// registration contract as the production host, including the development HostApi
// provided to functions during their Vue render calls.

afterEach(() => {
  // Release the mock application's Vue scope before removing its container, so
  // another test cannot inherit its mounted account context or rendering effects.
  for (const root of document.querySelectorAll<HTMLElement & { __vue_app__?: App }>(
    '.tr-dev-host',
  )) {
    root.__vue_app__?.unmount()
    root.remove()
  }
  for (const style of document.head.querySelectorAll('style')) {
    if (style.textContent?.includes('.tr-dev-host')) style.remove()
  }
  vi.unstubAllGlobals()
})

// The development entry registers a functional home page and a legacy lazy page together.
// Its toolbar navigation must render both while the functional page reads injected host identity.
it('mounts functional and lazy registrations through the development entry', async () => {
  vi.stubGlobal('__TR_PLUGIN_DEV_ID__', 'test.development')
  const sdk = await import('../../../../plugin-sdk/web/src/dev/index')
  const loader = vi.fn(async () => ({ render: () => h('p', 'Lazy development page') }))
  sdk.definePlugin({
    pages: {
      home: () => h('p', `Hello ${sdk.useHost().user.value?.username}`),
      later: loader,
    },
  })
  await flushPromises()
  expect(document.querySelector('.tr-dev-page')?.textContent).toBe('Hello Developer')
  const selector = document.querySelector<HTMLSelectElement>('select[aria-label="Page"]')!
  selector.value = 'later'
  selector.dispatchEvent(new Event('change', { bubbles: true }))
  await flushPromises()
  expect(document.querySelector('.tr-dev-page')?.textContent).toBe('Lazy development page')
  expect(loader).toHaveBeenCalledTimes(1)
})
