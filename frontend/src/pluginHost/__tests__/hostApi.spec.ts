import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, reactive } from 'vue'
import { usePluginHost } from '../hostApi'
import type { PluginPage, PluginUI } from '@/api/plugins.types'

// hostApi.spec.ts exercises the public capability contract without a real login
// or backend. Reactive fixtures reproduce account and route changes, while request
// spies verify namespace binding, error normalization, confirmation ownership, and
// the absence of credentials in the projected account object.

// Fixtures expose only the application services needed by the capability factory;
// each mounted scope owns its confirmation resolver independently of other tests.
const fixture = vi.hoisted(() => ({
  request: vi.fn(),
  push: vi.fn(),
  refresh: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  info: vi.fn(),
  auth: null as unknown as {
    isAuthenticated: boolean
    isAdmin: boolean
    user: { id: number; username: string; role: string; email: string }
    refreshUser: () => Promise<void>
  },
  route: { query: { search: 'one', tags: ['first', 'second'], empty: null } },
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => fixture.auth }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    siteName: 'Test',
    cachedPublicSettings: { server_timezone: 'Asia/Singapore' },
    showSuccess: fixture.success,
    showError: fixture.error,
    showInfo: fixture.info,
  }),
}))
vi.mock('@/composables/useTheme', () => ({ useTheme: () => ({ isDark: { value: false } }) }))
vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitName: { value: 'Credits' },
    balanceUnitSymbol: { value: 'C' },
  }),
}))
vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))
vi.mock('@/api/plugins', () => ({ requestPlugin: fixture.request }))
vi.mock('vue-router', () => ({
  useRoute: () => fixture.route,
  useRouter: () => ({ push: fixture.push }),
}))

/** mountedHost creates one real effect scope around the plugin capability lifecycle.
 * Tests stop the scope to model page removal. The fixture account includes email
 * deliberately, allowing assertions that the exposed user projection omits it.
 */
function mountedHost() {
  fixture.auth = reactive({
    isAuthenticated: true,
    isAdmin: false,
    user: { id: 7, username: 'reader', role: 'user', email: 'private@example.invalid' },
    refreshUser: fixture.refresh,
  })
  const page: PluginPage = { id: 'home', path: '', audience: 'user', title: 'Home' }
  const plugin: PluginUI = {
    id: 'test.host',
    version: '1.0.0',
    name: 'Host',
    script: { url: '/asset.js', integrity: 'hash' },
    styles: [],
    pages: [
      page,
      { id: 'settings', path: 'settings', audience: 'user', title: 'Settings' },
      { id: 'admin', path: 'admin', audience: 'admin', title: 'Admin' },
    ],
    menus: [],
  }
  const scope = effectScope()
  const context = scope.run(() => usePluginHost(plugin, page))!
  return { scope, ...context }
}

afterEach(() => {
  vi.clearAllMocks()
})

describe('mounted plugin capabilities', () => {
  // The native account contains private fields and query values include arrays.
  // The public capability exposes only documented fields and deterministic string query values.
  it('projects account and site information without credentials', () => {
    const { host, scope } = mountedHost()
    expect(host.user.value).toEqual({ id: 7, username: 'reader', role: 'user' })
    expect(host.site).toEqual({
      name: 'Test',
      balanceUnitName: 'Credits',
      balanceUnitSymbol: 'C',
      timezone: 'Asia/Singapore',
    })
    expect(host.route.value).toEqual({
      pageId: 'home',
      path: '',
      query: { search: 'one', tags: 'first' },
    })
    expect(host.locale.value).toBe('zh-CN')
    scope.stop()
  })

  // A plugin sends its own JSON and receives a structured host failure.
  // The adapter binds its installation and retains the stable reason over a numeric envelope code.
  it('scopes requests and preserves stable HTTP failures', async () => {
    const { host, scope } = mountedHost()
    fixture.request.mockResolvedValueOnce({ code: 15, data: 'plugin-owned' })
    await expect(host.request('POST', '/claim', { body: { day: 1 } })).resolves.toEqual({
      code: 15,
      data: 'plugin-owned',
    })
    expect(fixture.request).toHaveBeenCalledWith('test.host', 'POST', '/claim', {
      body: { day: 1 },
    })
    fixture.request.mockRejectedValueOnce({
      status: 429,
      code: 3,
      reason: 'PLUGIN_QUOTA_EXCEEDED',
      message: 'Limit reached',
    })
    await expect(host.request('POST', '/claim')).rejects.toMatchObject({
      name: 'HostRequestError',
      status: 429,
      code: 'PLUGIN_QUOTA_EXCEEDED',
    })
    scope.stop()
  })

  // Root and nested manifest paths share the current audience and installation.
  // External, traversing, slash-prefixed, and administrative paths cannot escape that declared page set.
  it('navigates only to declared pages in its audience', () => {
    const { host, scope } = mountedHost()
    host.navigate('', { tab: 'one' })
    expect(fixture.push).toHaveBeenLastCalledWith({ path: '/ext/test.host', query: { tab: 'one' } })
    host.navigate('settings')
    expect(fixture.push).toHaveBeenLastCalledWith({
      path: '/ext/test.host/settings',
      query: undefined,
    })
    for (const path of ['admin', '../other', 'https://example.test', '/settings'])
      expect(() => host.navigate(path)).toThrow('does not exist')
    scope.stop()
  })

  // A second question replaces the first before the user answers it.
  // Replacement and page disposal decline pending decisions, while an explicit acceptance resolves only the current question.
  it('settles replaced confirmations and closes admission on disposal', async () => {
    const { host, scope, settleConfirmation, confirmation } = mountedHost()
    const first = host.confirm({ title: 'First', message: 'First question' })
    const second = host.confirm({ title: 'Second', message: 'Second question' })
    await expect(first).resolves.toBe(false)
    settleConfirmation(true)
    await expect(second).resolves.toBe(true)
    const last = host.confirm({ title: 'Last', message: 'Leaving' })
    scope.stop()
    await expect(last).resolves.toBe(false)
    expect(confirmation.value).toBeNull()
    await expect(host.request('GET', '/status')).rejects.toMatchObject({
      code: 'PLUGIN_CONTEXT_CLOSED',
    })
  })

  // An old component retains its host object after another account signs in.
  // The capability refuses new requests and refreshes instead of using the later account's session.
  it('rejects actions after the authenticated account changes', async () => {
    const { host, scope } = mountedHost()
    fixture.auth.user.id = 9
    await expect(host.request('GET', '/status')).rejects.toMatchObject({
      code: 'PLUGIN_CONTEXT_CLOSED',
    })
    await expect(host.refreshAccount()).rejects.toMatchObject({ code: 'PLUGIN_CONTEXT_CLOSED' })
    expect(fixture.request).not.toHaveBeenCalled()
    scope.stop()
  })
})
