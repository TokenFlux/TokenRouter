import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref } from 'vue'
import type { HostApi, SdkRuntime } from '@tokenflux/tokenrouter-plugin-sdk'
import { initializePluginRuntime, useMessages } from '../runtime'
import { PLUGIN_HOST_KEY } from '../context'
import { definePlugin } from '../registry'

// runtime.spec.ts checks the bridge consumed by externalized plugin scripts.
// A synthetic prior global models development module replacement, and an injected
// host locale exercises reactive dictionary selection without mutating application
// translations or loading a real plugin archive.

// The runtime owns a detached subscription; this fixture records its installation
// while keeping application discovery and authenticated network requests out of these cases.
const fixture = vi.hoisted(() => ({
  subscribe: vi.fn(() => vi.fn()),
  store: null as unknown as { plugins: never[]; $subscribe: () => () => void },
}))
vi.mock('@/stores/plugins', () => ({ usePluginsStore: () => fixture.store }))
fixture.store = { plugins: [], $subscribe: fixture.subscribe }

/** PluginWindow exposes the test-visible runtime globals without widening the browser interface. */
type PluginWindow = Window & { __TR_PLUGIN_SDK__?: SdkRuntime; __TR_VUE__?: unknown }
afterEach(() => {
  delete (window as PluginWindow).__TR_PLUGIN_SDK__
  delete (window as PluginWindow).__TR_VUE__
})

describe('plugin SDK runtime bridge', () => {
  // A development reload can leave the browser global pointing at an older registry.
  // Initialization replaces that reference and remains idempotent within the current module generation.
  it('rebinds globals to the current registry and reuses its SDK identity', () => {
    const target = window as PluginWindow
    target.__TR_PLUGIN_SDK__ = { definePlugin: vi.fn() } as unknown as SdkRuntime
    initializePluginRuntime()
    expect(target.__TR_PLUGIN_SDK__?.definePlugin).toBe(definePlugin)
    expect(target.__TR_VUE__).toBeDefined()
    const first = target.__TR_PLUGIN_SDK__
    initializePluginRuntime()
    expect(target.__TR_PLUGIN_SDK__).toBe(first)
    expect(fixture.subscribe).toHaveBeenCalledTimes(1)
  })

  // Locale selection changes after a page has obtained its translator.
  // Exact tags, base language, and English fallback remain reactive, while interpolation returns text and missing keys remain visible.
  it('follows locale changes with dictionary fallback and plain interpolation', async () => {
    const locale = ref('zh-CN')
    const component = defineComponent({
      setup() {
        const t = useMessages({
          'zh-CN': { hello: '你好 {name}' },
          zh: { hello: '您好 {name}' },
          en: { hello: 'Hello {name}' },
        })
        return () => h('p', `${t('hello', { name: '<Reader>' })} / ${t('missing')}`)
      },
    })
    const wrapper = mount(component, {
      global: { provide: { [PLUGIN_HOST_KEY as symbol]: { locale } as HostApi } },
    })
    expect(wrapper.text()).toBe('你好 <Reader> / missing')
    expect(wrapper.find('reader').exists()).toBe(false)
    locale.value = 'zh-TW'
    await nextTick()
    expect(wrapper.text()).toBe('您好 <Reader> / missing')
    locale.value = 'fr'
    await nextTick()
    expect(wrapper.text()).toBe('Hello <Reader> / missing')
    wrapper.unmount()
  })
})
