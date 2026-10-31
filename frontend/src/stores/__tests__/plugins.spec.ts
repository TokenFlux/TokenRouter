import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { reactive, ref } from 'vue'
import { usePluginsStore } from '@/stores/plugins'
import type { PluginUI, PluginUIResponse } from '@/api/plugins.types'

// plugins.spec.ts checks account and language ownership of contribution discovery.
// Deferred HTTP responses expose stale publication races, while fake timers verify
// the five-minute refresh cadence and disposal without depending on a browser
// session refresh loop or a running backend.

// Shared mock handles are installed before store imports and replaced for each
// case; Vue refs provide real scope-change notifications to the production watcher.
const fixture = vi.hoisted(() => ({
  auth: null as unknown as { user: { id: number; role: string } | null; isAuthenticated: boolean },
  locale: null as unknown as { value: string },
  getUI: vi.fn(),
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => fixture.auth }))
vi.mock('@/i18n', () => ({ getLocale: () => fixture.locale.value }))
vi.mock('@/api/plugins', () => ({ getPluginUI: fixture.getUI }))

/** deferred exposes a discovery promise for explicit response-order assertions.
 * Tests resolve after changing authentication or locale. Each instance owns its
 * completion function and does not impose an artificial request timing delay.
 */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((complete) => {
    resolve = complete
  })
  return { promise, resolve }
}

/** contribution constructs one immutable description suitable for cache-ownership assertions. */
function contribution(id: string): PluginUI {
  return {
    id,
    version: '1.0.0',
    name: id,
    script: { url: '/asset.js', integrity: 'sha384-fixture' },
    styles: [],
    pages: [],
    menus: [],
  }
}

describe('plugin UI discovery scope', () => {
  let store: ReturnType<typeof usePluginsStore> | undefined

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-01T00:00:00Z'))
    setActivePinia(createPinia())
    fixture.auth = reactive({ user: { id: 1, role: 'user' }, isAuthenticated: true })
    fixture.locale = ref('en')
    fixture.getUI.mockReset()
    store = undefined
  })

  afterEach(() => {
    store?.$dispose()
    vi.useRealTimers()
  })

  // Several navigation consumers arrive during the initial read. They share one
  // request, reuse the completed snapshot, and refresh once the periodic deadline arrives.
  it('shares active discovery and refreshes every five minutes', async () => {
    const pending = deferred<PluginUIResponse>()
    fixture.getUI.mockReturnValueOnce(pending.promise).mockResolvedValue({ plugins: [] })
    store = usePluginsStore()
    const first = store.ensureUi()
    const second = store.ensureUi()
    expect(fixture.getUI).toHaveBeenCalledTimes(1)
    pending.resolve({ plugins: [contribution('test.first')] })
    await Promise.all([first, second])
    expect(await store.ensureUi()).toHaveLength(1)
    expect(fixture.getUI).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(5 * 60 * 1000)
    expect(fixture.getUI).toHaveBeenCalledTimes(2)
    expect(store.plugins).toEqual([])
  })

  // An earlier account response completes after identity changes. Its request is
  // canceled and cannot publish old descriptions into the new account's store scope.
  it('rejects stale account results and releases the old request', async () => {
    const old = deferred<PluginUIResponse>()
    const current = deferred<PluginUIResponse>()
    fixture.getUI.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    store = usePluginsStore()
    const previous = store.ensureUi().catch(() => undefined)
    const oldSignal = fixture.getUI.mock.calls[0][0] as AbortSignal
    fixture.auth.user = { id: 2, role: 'user' }
    expect(oldSignal.aborted).toBe(true)
    old.resolve({ plugins: [contribution('test.private')] })
    await previous
    expect(store.plugins).toEqual([])
    current.resolve({ plugins: [contribution('test.current')] })
    await store.ensureUi()
    expect(store.plugins.map((item) => item.id)).toEqual(['test.current'])
    fixture.auth.isAuthenticated = false
    expect(store.plugins).toEqual([])
  })

  // Locale changes invalidate a completed description without reusing its labels.
  // The next result belongs to the new language while the shared account identity remains unchanged.
  it('refreshes descriptions on locale changes', async () => {
    fixture.getUI.mockResolvedValue({ plugins: [contribution('test.localized')] })
    store = usePluginsStore()
    await store.ensureUi()
    fixture.locale.value = 'zh-Hans'
    await store.ensureUi()
    expect(fixture.getUI).toHaveBeenCalledTimes(2)
  })

  // Emergency disable is a successful empty feature state for surrounding pages.
  // It removes plugin menus without keeping a generic API error in shared presentation state.
  it('clears contributions on the host emergency switch', async () => {
    fixture.getUI.mockRejectedValue({ reason: 'PLUGINS_DISABLED' })
    store = usePluginsStore()
    expect(await store.ensureUi()).toEqual([])
    expect(store.disabled).toBe(true)
    expect(store.error).toBeNull()
  })
})
