import { useProtocolCatalogFixture } from '@/__tests__/helpers/protocolCatalog'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import ProvidersView from '../ProvidersView.vue'
import ProviderActionMenu from '@/views/admin/providers/table/ProviderActionMenu.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'

// ProvidersView.sparkShadow.spec.ts checks provider action wiring, inherited
// shadow metadata, and Grok plan labels. Page mounts use observable child
// events and row slots to verify API dispatch, confirmation behavior, safe
// homepage links, and refreshed snapshot presentation.

// Provider action spies capture duplicate and shadow requests alongside list
// reloads and notifications, so event wiring is observable without creating
// accounts.
const {
  listProviders,
  listWithEtag,
  getBatchTodayStats,
  getAllProxies,
  getAllGroups,
  duplicateProvider,
  createSparkShadow,
  showSuccess,
  showError,
} = vi.hoisted(() => ({
  listProviders: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  duplicateProvider: vi.fn(),
  createSparkShadow: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    providers: {
      list: listProviders,
      listWithEtag,
      getBatchTodayStats,
      duplicate: duplicateProvider,
      createSparkShadow,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn(),
    },
    proxies: { getAll: getAllProxies },
    groups: {
      getAll: getAllGroups,
      getAllIncludingInactive: getAllGroups,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess, showInfo: vi.fn() }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token' }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

// mountView creates a provider page with stubbed action and confirmation
// components. Tests emit events through those component wrappers and await
// page handlers before inspecting endpoint calls and notifications.
const mountView = () =>
  mount(ProvidersView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template:
            '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
        },
        DataTable: true,
        Pagination: true,
        ConfirmDialog: true,
        ProviderTableActions: {
          template: '<div><slot name="beforeCreate" /><slot name="after" /></div>',
        },
        ProviderTableFilters: { template: '<div></div>' },
        ProviderBulkActionsBar: true,
        ProviderActionMenu: true,
        ImportDataModal: true,
        ReAuthProviderModal: true,
        ProviderTestModal: true,
        ProviderStatsModal: true,
        ScheduledTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateProviderModal: true,
        EditProviderModal: true,
        BulkEditProviderModal: true,
        PlatformTypeBadge: true,
        ProviderCapacityCell: true,
        ProviderStatusIndicator: true,
        ProviderTodayStatsCell: true,
        ProviderGroupsCell: true,
        ProviderUsageCell: true,
        Icon: true,
      },
    },
  })

describe('admin ProvidersView — 外审 F2:spark 影子创建接线', () => {
  beforeEach(() => {
    localStorage.clear()
    for (const fn of [
      listProviders,
      listWithEtag,
      getBatchTodayStats,
      getAllProxies,
      getAllGroups,
      duplicateProvider,
      createSparkShadow,
      showSuccess,
      showError,
    ]) {
      fn.mockReset()
    }
    listProviders.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
    duplicateProvider.mockResolvedValue({ id: 998, name: 'parent-acc (Copy)' })
    createSparkShadow.mockResolvedValue({ id: 999, name: 'parent-acc (Spark)' })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  // A provider menu emits duplicate for an existing account. The page calls
  // the duplication endpoint once, reports success, and fetches the provider
  // list again.
  it('ProviderActionMenu 的 duplicate 事件一键复制提供商并刷新列表', async () => {
    const wrapper = mountView()
    await flushPromises()

    wrapper.findComponent(ProviderActionMenu).vm.$emit('duplicate', { id: 42, name: 'parent-acc' })
    await flushPromises()

    expect(duplicateProvider).toHaveBeenCalledTimes(1)
    expect(duplicateProvider).toHaveBeenCalledWith(42)
    expect(showSuccess).toHaveBeenCalledWith('admin.providers.duplicateSuccess')
    expect(listProviders.mock.calls.length).toBeGreaterThan(1)
    wrapper.unmount()
  })

  // Two duplicate events arrive while the first request remains unresolved.
  // Only one API call starts for that provider, and releasing it allows the
  // test to finish.
  it('同一提供商复制请求未完成时忽略重复点击', async () => {
    // Hold the first duplication unresolved until both events have been
    // delivered; immediate resolution would bypass the pending-request guard
    // under test.
    let resolveDuplicate!: (provider: {
      id: number // created provider identifier
      name: string // created provider name
    }) => void
    duplicateProvider.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveDuplicate = resolve
        }),
    )
    const wrapper = mountView()
    await flushPromises()

    const menu = wrapper.findComponent(ProviderActionMenu)
    menu.vm.$emit('duplicate', { id: 42, name: 'parent-acc' })
    menu.vm.$emit('duplicate', { id: 42, name: 'parent-acc' })
    await flushPromises()

    expect(duplicateProvider).toHaveBeenCalledTimes(1)
    resolveDuplicate({ id: 998, name: 'parent-acc (Copy)' })
    await flushPromises()
    wrapper.unmount()
  })

  // The duplication endpoint rejects with a backend error message. The page
  // passes that message to the error notification instead of reporting a
  // successful copy.
  it('复制失败时显示后端错误', async () => {
    // Mute the expected rejection log for this case while retaining the
    // notification spy that checks the backend message reaches the
    // administrator.
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    duplicateProvider.mockRejectedValueOnce(new Error('duplicate failed'))
    const wrapper = mountView()
    await flushPromises()

    wrapper.findComponent(ProviderActionMenu).vm.$emit('duplicate', { id: 42, name: 'parent-acc' })
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('duplicate failed')
    consoleError.mockRestore()
    wrapper.unmount()
  })

  // A menu event requests a Spark shadow for its parent account. Confirming
  // the displayed dialog calls the shadow endpoint with the derived name and
  // reports success.
  it('ProviderActionMenu 的 create-spark-shadow 事件触发 createSparkShadow API + 成功提示', async () => {
    const wrapper = mountView()
    await flushPromises()

    const menu = wrapper.findComponent(ProviderActionMenu)
    expect(menu.exists()).toBe(true)

    menu.vm.$emit('create-spark-shadow', { id: 42, name: 'parent-acc' })
    await flushPromises()

    // Resolve the visible confirmation before checking dispatch, because the
    // menu event alone only prepares the shadow creation request.
    const dialog = wrapper.findAllComponents(ConfirmDialog).find((d) => d.props('show'))
    expect(dialog).toBeTruthy()
    dialog?.vm.$emit('confirm')
    await flushPromises()

    expect(createSparkShadow).toHaveBeenCalledTimes(1)
    expect(createSparkShadow).toHaveBeenCalledWith(42, { name: 'parent-acc (Spark)' })
    expect(showSuccess).toHaveBeenCalledWith('admin.providers.createSparkShadowSuccess')
    wrapper.unmount()
  })

  // A Spark shadow request opens a confirmation dialog for the parent.
  // Cancelling that dialog leaves the creation endpoint uncalled and closes
  // the attempted operation.
  it('用户取消确认时不调用 API', async () => {
    const wrapper = mountView()
    await flushPromises()

    wrapper
      .findComponent(ProviderActionMenu)
      .vm.$emit('create-spark-shadow', { id: 42, name: 'parent-acc' })
    await flushPromises()

    // Cancel the visible confirmation before checking dispatch so the test
    // covers the dialog event wired by the page.
    const dialog = wrapper.findAllComponents(ConfirmDialog).find((d) => d.props('show'))
    expect(dialog).toBeTruthy()
    dialog?.vm.$emit('cancel')
    await flushPromises()

    expect(createSparkShadow).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

// mountViewWithRow renders provider name and platform slots using supplied
// list rows. Configure the list response before mounting; callers can inspect
// badges and links after asynchronous page loading completes.
const mountViewWithRow = () =>
  mount(ProvidersView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template:
            '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
        },
        // Forward each fixture row into both slots so badge and homepage
        // assertions execute the page-owned rendering expressions.
        DataTable: {
          props: [
            'data', // provider rows supplied by page
            'columns', // page table column definitions
            'loading', // provider table loading state
          ],
          template: `<div>
            <div v-for="(row, idx) in (data || [])" :key="idx">
              <slot name="cell-name" :row="row" :value="row.name" />
              <slot name="cell-platform_type" :row="row" />
            </div>
          </div>`,
        },
        Pagination: true,
        ConfirmDialog: true,
        ProviderTableActions: {
          template: '<div><slot name="beforeCreate" /><slot name="after" /></div>',
        },
        ProviderTableFilters: { template: '<div></div>' },
        ProviderBulkActionsBar: true,
        ProviderActionMenu: true,
        ImportDataModal: true,
        ReAuthProviderModal: true,
        ProviderTestModal: true,
        ProviderStatsModal: true,
        ScheduledTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateProviderModal: true,
        EditProviderModal: true,
        BulkEditProviderModal: true,
        PlatformTypeBadge: true,
        ProviderCapacityCell: true,
        ProviderStatusIndicator: true,
        ProviderTodayStatsCell: true,
        ProviderGroupsCell: true,
        ProviderUsageCell: true,
        Icon: true,
      },
    },
  })

describe('admin ProvidersView — 提供商行展示', () => {
  beforeEach(() => {
    localStorage.clear()
    for (const fn of [
      listProviders,
      listWithEtag,
      getBatchTodayStats,
      getAllProxies,
      getAllGroups,
      duplicateProvider,
      createSparkShadow,
      showSuccess,
      showError,
    ]) {
      fn.mockReset()
    }
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
    vi.stubGlobal(
      'confirm',
      vi.fn(() => true),
    )
  })

  afterEach(() => {
    // Restore clock behavior, visibility spies, and global stubs together so
    // the timed refresh case cannot change later page mounts or confirmation
    // behavior.
    vi.useRealTimers()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  // A shadow row supplies its parent's email, plan, privacy, and expiration
  // metadata. The page displays the inherited email and passes the
  // corresponding values to its platform badge.
  it('影子行 email 单元格显示 parent_email，PlatformTypeBadge 接收 parent_plan_type/parent_privacy_mode', async () => {
    const shadowProvider = {
      id: 100,
      name: '影子提供商',
      platform: 'openai',
      type: 'oauth',
      parent_provider_id: 1,
      parent_email: 'parent@example.com',
      parent_plan_type: 'plus',
      parent_privacy_mode: 'false',
      parent_subscription_expires_at: '2027-01-01T00:00:00Z',
      parent_chatgpt_account_id: 'chatgpt-abc123',
    }

    listProviders.mockResolvedValue({
      items: [shadowProvider],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const wrapper = mountViewWithRow()
    await flushPromises()

    expect(wrapper.text()).toContain('parent@example.com')

    const badge = wrapper.findComponent(PlatformTypeBadge)
    expect(badge.exists()).toBe(true)
    expect(badge.props('planType')).toBe('plus')
    expect(badge.props('privacyMode')).toBe('false')
    expect(badge.props('subscriptionExpiresAt')).toBe('2027-01-01T00:00:00Z')

    wrapper.unmount()
  })

  // Rows include API Key and OAuth accounts with safe, credential-bearing,
  // relative, and script URLs. Only safe API Key origins become text links,
  // with credentials and paths removed.
  it('仅将具有安全 base_url 的 API Key 提供商名称链接到站点主页', async () => {
    listProviders.mockResolvedValue({
      items: [
        {
          id: 101,
          name: 'relay-provider',
          platform: 'openai',
          type: 'apikey',
          credentials: { base_url: 'https://relay.example.com/api/v1/' },
        },
        {
          id: 102,
          name: 'oauth-provider',
          platform: 'openai',
          type: 'oauth',
          credentials: { base_url: 'https://oauth.example.com/v1' },
        },
        {
          id: 103,
          name: 'invalid-url',
          platform: 'openai',
          type: 'apikey',
          credentials: { base_url: 'javascript:alert(1)' },
        },
        {
          id: 104,
          name: 'credential-url',
          platform: 'openai',
          type: 'apikey',
          credentials: {
            base_url: 'https://user:secret@secure.example.com:8443/api/v1?token=secret#fragment',
          },
        },
        {
          id: 105,
          name: 'relative-url',
          platform: 'openai',
          type: 'apikey',
          credentials: { base_url: '/api/v1' },
        },
      ],
      total: 5,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const wrapper = mountViewWithRow()
    await flushPromises()

    const links = wrapper.findAll('a')
    expect(links).toHaveLength(2)
    const [link, credentialLink] = links
    expect(link.text()).toBe('relay-provider')
    expect(link.attributes()).toMatchObject({
      href: 'https://relay.example.com',
      target: '_blank',
      rel: 'noopener noreferrer',
    })
    expect(credentialLink.text()).toBe('credential-url')
    expect(credentialLink.attributes('href')).toBe('https://secure.example.com:8443')
    expect(credentialLink.attributes('href')).not.toContain('secret')
    expect(credentialLink.attributes('href')).not.toContain('/api/v1')
    expect(link.classes()).toContain('text-link')
    const tooltip = wrapper.findComponent(HelpTooltip)
    expect(tooltip.props('content')).toBe('https://relay.example.com')
    expect(tooltip.props('widthClass')).toBe('w-max max-w-sm break-all')
    expect(tooltip.classes()).toEqual(expect.arrayContaining(['self-start']))
    expect(wrapper.text()).toContain('oauth-provider')
    expect(wrapper.text()).toContain('invalid-url')
    expect(wrapper.text()).toContain('relative-url')

    wrapper.unmount()
  })

  // Grok rows combine credential tiers with billing, usage, and legacy quota
  // snapshots. Badge values follow the tested precedence and model-specific
  // evidence instead of blindly accepting stale plans.
  it('prefers persisted Grok JWT tier over lagging billing/quota snapshots', async () => {
    const grokProviders = [
      {
        id: 201,
        name: 'oauth-tier',
        platform: 'grok',
        type: 'oauth',
        credentials: { subscription_tier: 'FREE', plan_type: 'legacy' },
        extra: {
          grok_billing_snapshot: { plan: 'SuperGrok' },
          subscription_tier: 'BASIC',
        },
      },
      {
        id: 202,
        name: 'billing-tier',
        platform: 'grok',
        type: 'oauth',
        credentials: {},
        extra: {
          grok_billing_snapshot: { plan: 'SuperGrok Heavy' },
          subscription_tier: 'BASIC',
        },
      },
      {
        id: 203,
        name: 'quota-tier',
        platform: 'grok',
        type: 'oauth',
        credentials: { subscription_tier: 'FREE' },
        extra: {
          grok_quota_snapshot: { subscription_tier: 'SuperGrok' },
          subscription_tier: 'BASIC',
        },
      },
      {
        id: 204,
        name: 'extra-tier',
        platform: 'grok',
        type: 'oauth',
        credentials: { plan_type: 'SuperGrok' },
        extra: { subscription_tier: 'BASIC' },
      },
      {
        id: 205,
        name: 'legacy-tier',
        platform: 'grok',
        type: 'oauth',
        credentials: { plan_type: 'SuperGrok' },
      },
      {
        id: 206,
        name: 'supergrokpro-responses-quota',
        platform: 'grok',
        type: 'oauth',
        credentials: { subscription_tier: 'SuperGrokPro' },
        extra: {
          grok_billing_snapshot: { plan: 'SuperGrok' },
          grok_usage_snapshot: {
            model: 'grok-4.5',
            last_headers_seen_at: new Date().toISOString(),
            requests: { limit: 8300 },
            tokens: { limit: 53_000_000 },
          },
          grok_quota_snapshot: {
            model: 'grok-4.6',
            last_headers_seen_at: new Date().toISOString(),
            requests: { limit: 8300 },
            tokens: { limit: 53_000_000 },
          },
        },
      },
      {
        id: 207,
        name: 'supergrokpro-other-model-quota',
        platform: 'grok',
        type: 'oauth',
        credentials: { subscription_tier: 'SuperGrokPro' },
        extra: {
          grok_billing_snapshot: { plan: 'SuperGrok' },
          grok_usage_snapshot: {
            model: 'grok-4.6',
            last_headers_seen_at: new Date().toISOString(),
            requests: { limit: 8300 },
            tokens: { limit: 53_000_000 },
          },
        },
      },
      {
        id: 208,
        name: 'usage-over-legacy-quota',
        platform: 'grok',
        type: 'oauth',
        credentials: {},
        extra: {
          grok_usage_snapshot: { subscription_tier: 'SuperGrok' },
          grok_quota_snapshot: { subscription_tier: 'Free' },
        },
      },
      {
        id: 209,
        name: 'legacy-quota-alias',
        platform: 'grok',
        type: 'oauth',
        credentials: {},
        extra: { grok_quota_snapshot: { subscription_tier: 'SuperGrok' } },
      },
    ]

    listProviders.mockResolvedValue({
      items: grokProviders,
      total: grokProviders.length,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const wrapper = mountViewWithRow()
    await flushPromises()

    const badges = wrapper.findAllComponents(PlatformTypeBadge)
    expect(badges.map((badge) => badge.props('planType'))).toEqual([
      'FREE',
      'SuperGrok Heavy',
      'FREE',
      'BASIC',
      'SuperGrok',
      'SuperGrok Heavy',
      'SuperGrok',
      'SuperGrok',
      'SuperGrok',
    ])

    wrapper.unmount()
  })

  // Grok plan fields contain objects, arrays, numbers, and whitespace
  // alongside usable strings. Badge selection skips malformed candidates and
  // yields undefined when no valid plan remains.
  it('skips malformed Grok plan fields and safely uses the next valid fallback', async () => {
    const grokProviders = [
      {
        id: 210,
        name: 'legacy-fallback',
        platform: 'grok',
        type: 'oauth',
        credentials: {},
        extra: {
          grok_usage_snapshot: { subscription_tier: { name: 'SuperGrok Heavy' } },
          grok_quota_snapshot: { subscription_tier: 'SuperGrok' },
        },
      },
      {
        id: 211,
        name: 'credential-plan-fallback',
        platform: 'grok',
        type: 'oauth',
        credentials: { subscription_tier: 0, plan_type: 'SuperGrok Heavy' },
        extra: {
          grok_billing_snapshot: { plan: {} },
          grok_usage_snapshot: { subscription_tier: 1 },
          grok_quota_snapshot: { subscription_tier: [] },
          subscription_tier: '   ',
        },
      },
      {
        id: 212,
        name: 'no-valid-plan',
        platform: 'grok',
        type: 'oauth',
        credentials: { subscription_tier: {}, plan_type: 2 },
        parent_plan_type: [],
        extra: {
          grok_billing_snapshot: { plan: [] },
          grok_usage_snapshot: { subscription_tier: 1 },
          grok_quota_snapshot: { subscription_tier: {} },
          subscription_tier: null,
        },
      },
    ]

    listProviders.mockResolvedValue({
      items: grokProviders,
      total: grokProviders.length,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const wrapper = mountViewWithRow()
    await flushPromises()

    expect(
      wrapper.findAllComponents(PlatformTypeBadge).map((badge) => badge.props('planType')),
    ).toEqual(['SuperGrok', 'SuperGrok Heavy', undefined])
    wrapper.unmount()
  })

  // Automatic refresh returns a changed canonical Grok usage snapshot for the
  // same row. The platform badge changes from Free to SuperGrok after the
  // timed ETag request.
  it('replaces a Grok row when auto refresh returns a changed canonical usage snapshot', async () => {
    // Control the refresh clock and mark the document visible so the ETag
    // polling path runs deterministically without waiting for real time.
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    localStorage.setItem(
      'provider-auto-refresh',
      JSON.stringify({ enabled: true, interval_seconds: 5 }),
    )

    const initialProvider = {
      id: 213,
      name: 'refresh-tier',
      platform: 'grok',
      type: 'oauth',
      extra: { grok_usage_snapshot: { subscription_tier: 'Free', status_code: 200 } },
    }
    const refreshedProvider = {
      ...initialProvider,
      extra: { grok_usage_snapshot: { subscription_tier: 'SuperGrok', status_code: 200 } },
    }
    listProviders.mockResolvedValue({
      items: [initialProvider],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    listWithEtag.mockResolvedValueOnce({
      notModified: false,
      etag: 'grok-snapshot-2',
      data: { items: [refreshedProvider], total: 1, page: 1, page_size: 20, pages: 1 },
    })

    const wrapper = mountViewWithRow()
    await flushPromises()
    expect(wrapper.findComponent(PlatformTypeBadge).props('planType')).toBe('Free')

    // Advance beyond the configured five-second interval so one refresh
    // observes the changed canonical snapshot before checking the rendered
    // plan badge.
    await vi.advanceTimersByTimeAsync(6000)
    await flushPromises()

    expect(listWithEtag).toHaveBeenCalledTimes(1)
    expect(wrapper.findComponent(PlatformTypeBadge).props('planType')).toBe('SuperGrok')
    wrapper.unmount()
  })
})

useProtocolCatalogFixture()
