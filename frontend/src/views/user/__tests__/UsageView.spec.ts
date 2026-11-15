import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import UsageView from '../UsageView.vue'

// UsageView.spec.ts tests user usage filters, error visibility, team scope, and background
// CSV export panel integration. API substitutes expose the parameters sent by real page
// loaders, while child-control stubs verify column permissions, inherited filter chips, and
// active background export badge state.

enableAutoUnmount(afterEach)

// User and team API substitutes record queries and control membership, public error
// visibility, translated options, chart data, background export tasks, and notifications.
const {
  query,
  getStats,
  getDashboardModels,
  getDashboardSnapshotV2,
  listMyErrorRequests,
  listExportTasks,
  publicSettings,
  list,
  getAvailable,
  getCurrentTeam,
  getTeamKeys,
  getTeamMembers,
  getTeamMemberUsage,
  showError,
  showWarning,
  showSuccess,
  showInfo,
} = vi.hoisted(() => ({
  query: vi.fn(),
  getStats: vi.fn(),
  getDashboardModels: vi.fn(),
  getDashboardSnapshotV2: vi.fn(),
  listMyErrorRequests: vi.fn(),
  listExportTasks: vi.fn(),
  publicSettings: { allow_user_view_error_requests: undefined as boolean | undefined },
  list: vi.fn(),
  getAvailable: vi.fn(),
  getCurrentTeam: vi.fn(),
  getTeamKeys: vi.fn(),
  getTeamMembers: vi.fn(),
  getTeamMemberUsage: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn(),
}))

// messages supplies user CSV headers and column-setting labels so export assertions
// compare the complete user-facing field sequence.
const messages: Record<string, string> = {
  'usage.time': 'Time',
  'usage.reasoningEffort': 'Reasoning Effort',
  'usage.inboundEndpoint': 'Inbound Endpoint',
  'admin.usage.ipAddress': 'IP Address',
  'admin.usage.inputTokens': 'Input Tokens',
  'admin.usage.outputTokens': 'Output Tokens',
  'admin.usage.cacheReadTokens': 'Cache Read Tokens',
  'admin.usage.cacheCreationTokens': 'Cache Creation Tokens',
  'usage.rate': 'Rate Multiplier',
  'usage.userBilled': 'Billed Cost',
  'usage.original': 'Original Cost',
  'usage.firstToken': 'TTFT (ms)',
  'usage.ttftMode': 'TTFT mode',
  'usage.latencyFirstToken': 'First output',
  'usage.duration': 'Duration (ms)',
  'usage.tps': 'TPS',
  'usage.tpsExport': 'TPS (tok/s)',

  'admin.dashboard.timeRange': 'Time range',
  'admin.dashboard.granularity': 'Granularity',
  'admin.dashboard.day': 'Day',
  'admin.dashboard.hour': 'Hour',
  'admin.users.columnSettings': 'Columns',
  'admin.usage.group': 'Group',
  'admin.usage.billingType': 'Billing type',
  'admin.usage.billingMode': 'Billing mode',
  'admin.usage.allTypes': 'All types',
  'admin.usage.allBillingTypes': 'All billing types',
  'admin.usage.billingTypeBalance': 'Balance',
  'admin.usage.billingTypeSubscription': 'Subscription',
  'admin.usage.allBillingModes': 'All billing modes',
  'admin.usage.billingModeToken': 'Token',
  'admin.usage.billingModePerRequest': 'Per request',
  'admin.usage.billingModeImage': 'Image',
  'admin.usage.allGroups': 'All groups',
  'admin.usage.allModels': 'All models',
  'usage.allApiKeys': 'All API Keys',
  'usage.apiKeyFilter': 'API Key',
  'usage.model': 'Model',
  'usage.type': 'Type',
  'usage.ws': 'WS',
  'usage.stream': 'Stream',
  'usage.sync': 'Sync',
  'usage.exportCsv': 'Export',
  'usage.compactionFilter': 'Compaction',
  'usage.nativeCompactionV2': 'Native compaction',
  'usage.legacyCompaction': 'Legacy compaction',
  'requests.id': 'Request ID',
  'usage.failedToLoad': 'Failed to load',
  'common.refresh': 'Refresh',
  'common.reset': 'Reset',
}

vi.mock('@/api', () => ({
  usageAPI: {
    query,
    getStats,
    getDashboardModels,
    getDashboardSnapshotV2,
    listMyErrorRequests,
    listExportTasks,
  },
  keysAPI: {
    list,
  },
  userGroupsAPI: {
    getAvailable,
  },
}))

vi.mock('@/api/usage', () => ({
  usageAPI: {
    query,
    getStats,
    getDashboardModels,
    getDashboardSnapshotV2,
    listMyErrorRequests,
    listExportTasks,
  },
}))

vi.mock('@/api/team', () => ({
  teamAPI: {
    current: getCurrentTeam,
    keys: getTeamKeys,
    members: getTeamMembers,
    memberUsage: getTeamMemberUsage,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showWarning,
    showSuccess,
    showInfo,
    cachedPublicSettings: publicSettings,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'usage.exportPanel.activeCountLabel') {
          return `Export (${params?.count ?? 0})`
        }
        return messages[key] ?? key
      },
    }),
  }
})

// simpleStub exposes the child slots or props exercised by this suite while leaving the
// owning component logic active.
const simpleStub = { template: '<div><slot /></div>' }
// chartStub omits chart rendering while leaving the usage page loaders active
// for API request and filter assertions.
const chartStub = { template: '<div />' }
// UsageTableStub accepts visible columns and member presentation flags so tests inspect
// user permissions and team attribution configuration.
const UsageTableStub = {
  props: [
    'columns', // visible table column definitions
    'userClickable', // administrator navigation permission flag
    'compactUserColumn', // compact member attribution flag
  ],
  template: '<div data-test="usage-table" />',
}
// UsageExportDialogStub captures inherited date, filter, and chip props and exposes events
// used to drive active background export badge transitions.
const UsageExportDialogStub = {
  name: 'UsageExportDialog',
  props: [
    'show', // modal visibility flag
    'mode', // user or administrator mode
    'startDate', // inherited inclusive start date
    'endDate', // inherited inclusive end date
    'filters', // inherited filter object
    'filterLabels', // human-readable filter summary chips
  ],
  emits: ['close', 'tasksChanged', 'activeIdsChanged'],
  template: '<div data-test="usage-export-dialog" />',
}

// usageLog supplies representative usage measurements reused by rendering cases, with each
// test overriding the fields relevant to its scenario.
const usageLog = {
  id: 1,
  request_id: 'req-user-export',
  actual_cost: 0.092883,
  total_cost: 0.092883,
  rate_multiplier: 1,
  service_tier: 'priority',
  input_cost: 0.020285,
  output_cost: 0.00303,
  cache_creation_cost: 0.000001,
  cache_read_cost: 0.069568,
  input_tokens: 4057,
  output_tokens: 101,
  cache_creation_tokens: 4,
  cache_read_tokens: 278272,
  cache_creation_5m_tokens: 0,
  cache_creation_1h_tokens: 0,
  image_count: 0,
  image_size: null,
  ttft_ms: 12,
  ttft_mode: 'first_output',
  tps: 303.3,
  duration_ms: 345,
  created_at: '2026-03-08T00:00:00Z',
  model: 'gpt-5.4',
  reasoning_effort: null,
  ip_address: '203.0.113.10',
  api_key: { name: 'demo-key' },
  billing_mode: 'token',
  request_type: 'sync',
  stream: false,
}

// mountUsageView renders the user page with controlled child widgets and API substitutes.
// The actual page loaders, preference actions, permission checks, and export panel wiring
// stay active for these assertions.
function mountUsageView() {
  return mount(UsageView, {
    global: {
      stubs: {
        AppLayout: simpleStub,
        Pagination: true,
        Select: true,
        FilterOptions: true,
        DateRangePicker: true,
        Icon: true,
        UsageStatsCards: chartStub,
        UsageTable: UsageTableStub,
        UsageExportDialog: UsageExportDialogStub,
        ModelDistributionChart: chartStub,
        GroupDistributionChart: chartStub,
        EndpointDistributionChart: chartStub,
        TokenUsageTrend: chartStub,
        TeamMemberUsageCharts: chartStub,
        UserErrorRequestsTable: true,
      },
    },
  })
}

describe('user UsageView', () => {
  beforeEach(() => {
    localStorage.clear()
    query.mockReset()
    getStats.mockReset()
    getDashboardModels.mockReset()
    getDashboardSnapshotV2.mockReset()
    listMyErrorRequests.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    listExportTasks.mockReset().mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 10,
      pages: 0,
    })
    publicSettings.allow_user_view_error_requests = undefined
    list.mockReset()
    getAvailable.mockReset()
    getCurrentTeam.mockReset()
    getTeamKeys.mockReset()
    getTeamMembers.mockReset()
    getTeamMemberUsage.mockReset()
    showError.mockReset()
    showWarning.mockReset()
    showSuccess.mockReset()
    showInfo.mockReset()

    query.mockResolvedValue({ items: [usageLog], total: 1, pages: 1 })
    getStats.mockResolvedValue({
      total_requests: 1,
      total_input_tokens: 10,
      total_output_tokens: 20,
      total_cache_tokens: 0,
      total_tokens: 30,
      total_cost: 0.1,
      total_actual_cost: 0.08,
      average_duration_ms: 12,
      endpoints: [],
      upstream_endpoints: [],
      endpoint_paths: [],
    })
    getDashboardModels.mockResolvedValue({
      models: [
        {
          model: 'gpt-5.4',
          requests: 1,
          input_tokens: 10,
          output_tokens: 20,
          cache_creation_tokens: 0,
          cache_read_tokens: 0,
          total_tokens: 30,
          cost: 0.1,
          actual_cost: 0.08,
        },
      ],
      start_date: '2026-03-08',
      end_date: '2026-03-08',
    })
    getDashboardSnapshotV2.mockResolvedValue({
      generated_at: '2026-03-08T00:00:00Z',
      start_date: '2026-03-08',
      end_date: '2026-03-08',
      granularity: 'hour',
      trend: [],
      groups: [],
    })
    list.mockResolvedValue({ items: [{ id: 1, name: 'demo-key' }] })
    getAvailable.mockResolvedValue([{ id: 1, name: 'default' }])
    getCurrentTeam.mockRejectedValue({ response: { status: 404 } })
    getTeamKeys.mockResolvedValue([])
    getTeamMembers.mockResolvedValue([])
    getTeamMemberUsage.mockResolvedValue([])
  })

  // The usage page mounts with personal scope and no selected filters. The test protects
  // initial record and statistics requests together with personal key and group lookups.
  it('loads logs, stats, model stats, and snapshot on first render', async () => {
    mountUsageView()
    await flushPromises()

    expect(query).toHaveBeenCalled()
    expect(getStats).toHaveBeenCalled()
    expect(getDashboardModels).toHaveBeenCalled()
    expect(getDashboardSnapshotV2).toHaveBeenCalledWith(
      expect.objectContaining({
        include_trend: true,
        include_model_stats: false,
        include_group_stats: true,
      }),
    )
    expect(list).toHaveBeenCalledWith(1, 100, { scope: 'personal' })
    expect(getAvailable).toHaveBeenCalledWith('personal', undefined, false)
  })

  // Group options contain translated and legacy names before a locale change. The test
  // protects display-name precedence and refreshing the selected language's labels after
  // language restoration.
  it('分组筛选使用展示名称，并在切换语言后刷新', async () => {
    getAvailable.mockResolvedValue([
      { id: 1, name: 'business-group', display_name: 'English group' },
      { id: 2, name: 'Legacy group' },
    ])
    const wrapper = mountUsageView()
    await flushPromises()
    // options collects option objects exposed by every mounted filter option list stub.
    const options = () =>
      wrapper
        .findAllComponents({ name: 'FilterOptions' })
        .flatMap((item) => item.props('options') || [])
    expect(options()).toContainEqual(expect.objectContaining({ value: 1, label: 'English group' }))
    expect(options()).toContainEqual(expect.objectContaining({ value: 2, label: 'Legacy group' }))
    getAvailable.mockResolvedValue([{ id: 1, name: 'business-group', display_name: '中文分组' }])
    window.dispatchEvent(new CustomEvent('locale-changed', { detail: 'zh-Hans' }))
    await flushPromises()
    expect(options()).toContainEqual(expect.objectContaining({ value: 1, label: '中文分组' }))
    wrapper.unmount()
  })

  // The public error-visibility setting is absent or false on first render. The test
  // protects loading ordinary usage without querying errors, displaying error
  // notifications, or hiding the usage table.
  it.each([undefined, false])(
    'skips error queries on locale refresh when visibility is %s',
    async (enabled) => {
      publicSettings.allow_user_view_error_requests = enabled
      listMyErrorRequests.mockRejectedValue({ status: 403 })
      const wrapper = mountUsageView()
      await flushPromises()
      query.mockClear()

      window.dispatchEvent(new CustomEvent('locale-changed', { detail: 'zh-Hans' }))
      await flushPromises()

      expect(query).toHaveBeenCalledOnce()
      expect(listMyErrorRequests).not.toHaveBeenCalled()
      expect(showError).not.toHaveBeenCalled()
      expect(wrapper.findComponent(UsageTableStub).exists()).toBe(true)
    },
  )

  // The error loader is invoked directly while visibility is disabled. The test protects
  // returning before any API request, loading state change, or error notification is
  // produced.
  it('guards the error loader when error visibility is disabled', async () => {
    publicSettings.allow_user_view_error_requests = false
    const wrapper = mountUsageView()
    await flushPromises()

    await (wrapper.vm as any).loadErrors()

    expect(listMyErrorRequests).not.toHaveBeenCalled()
    expect((wrapper.vm as any).errorLoading).toBe(false)
    expect(showError).not.toHaveBeenCalled()
  })

  // Error visibility is enabled but the error tab remains unopened. The test protects
  // deferring its API request until the user selects the error view.
  it('defers enabled error queries until the errors tab is opened', async () => {
    publicSettings.allow_user_view_error_requests = true
    const wrapper = mountUsageView()
    await flushPromises()

    window.dispatchEvent(new CustomEvent('locale-changed', { detail: 'zh-Hans' }))
    await flushPromises()
    expect(listMyErrorRequests).not.toHaveBeenCalled()

    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'usage.tabs.errors')!
      .trigger('click')
    await flushPromises()
    expect(listMyErrorRequests).toHaveBeenCalledOnce()
  })

  // Locale changes occur with errors visible and then with their tab inactive. The test
  // protects immediate visible refresh and invalidating inactive records for retrieval when
  // reopened.
  it('refreshes visible errors and invalidates inactive error rows on locale changes', async () => {
    publicSettings.allow_user_view_error_requests = true
    listMyErrorRequests.mockResolvedValue({ items: [{ id: 1 }], total: 1, pages: 1 })
    const wrapper = mountUsageView()
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'usage.tabs.errors')!
      .trigger('click')
    await flushPromises()
    listMyErrorRequests.mockClear()

    window.dispatchEvent(new CustomEvent('locale-changed', { detail: 'en' }))
    await flushPromises()
    expect(listMyErrorRequests).toHaveBeenCalledOnce()

    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'usage.tabs.usage')!
      .trigger('click')
    listMyErrorRequests.mockClear()
    window.dispatchEvent(new CustomEvent('locale-changed', { detail: 'zh-Hans' }))
    await flushPromises()
    expect(listMyErrorRequests).not.toHaveBeenCalled()

    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'usage.tabs.errors')!
      .trigger('click')
    await flushPromises()
    expect(listMyErrorRequests).toHaveBeenCalledOnce()
    expect(showError).not.toHaveBeenCalled()
  })

  // Main filter lists start with unselected null values in the UI. The test protects their
  // leading all-option and omitting those predicates from the normalized usage query.
  it('主筛选项初始值为 null，首项是“全部”，请求参数不带未选条件', async () => {
    const wrapper = mountUsageView()
    await flushPromises()

    const labels = [
      'usage.allApiKeys',
      'admin.usage.allModels',
      'admin.usage.allGroups',
      'admin.usage.allTypes',
    ]
    for (const label of labels) {
      const filterOptions = wrapper
        .findAllComponents({ name: 'FilterOptions' })
        .find((item) => item.props('options')?.[0]?.label === (messages[label] ?? label))
      expect(filterOptions?.props('modelValue')).toBeNull()
    }
    const params = query.mock.calls[0][0]
    for (const key of ['api_key_id', 'group_id', 'model', 'request_type']) {
      expect(params[key]).toBeUndefined()
    }
  })

  // A team owner loads team keys and aggregate member usage. The test protects owner
  // requests, member column presentation, compact attribution, and disabling administrator
  // navigation for user rows.
  it('loads team keys and aggregated member charts for a team owner', async () => {
    getCurrentTeam.mockResolvedValue({
      team: { id: 7, name: 'Demo team' },
      membership: { user_id: 42, role: 'owner' },
      owner: { user_id: 42, role: 'owner' },
    })
    getTeamKeys.mockResolvedValue([{ id: 9, name: 'Team key' }])
    getTeamMembers.mockResolvedValue([
      { user_id: 42, username: 'Owner', email: 'owner@example.com', role: 'owner' },
      { user_id: 43, username: 'Member', email: 'member@example.com', role: 'member' },
    ])
    getTeamMemberUsage.mockResolvedValue([
      {
        actor_user_id: 42,
        display_name: 'Owner',
        status: 'active',
        summary: {
          actual_cost: 1.2,
          request_count: 1,
          input_tokens: 10,
          output_tokens: 5,
          daily: [],
        },
      },
      {
        actor_user_id: 43,
        display_name: 'Former member',
        status: 'left',
        summary: {
          actual_cost: 0.8,
          request_count: 1,
          input_tokens: 10,
          output_tokens: 5,
          daily: [],
        },
      },
    ])

    const wrapper = mountUsageView()
    await flushPromises()

    expect(getTeamKeys).toHaveBeenCalledOnce()
    expect(getTeamMembers).toHaveBeenCalledOnce()
    expect(getTeamMemberUsage).toHaveBeenCalledOnce()
    expect(getTeamMemberUsage).toHaveBeenCalledWith(
      expect.objectContaining({
        from: expect.any(String),
        to: expect.any(String),
      }),
    )
    const usageTable = wrapper.findComponent(UsageTableStub)
    const columns = usageTable.props('columns') as Array<{
      key: string // table column identifier
      class?: string // table column layout classes
    }>
    expect(columns.map((column) => column.key)).toContain('user')
    expect(columns.map((column) => column.key)).toContain('tps')
    expect(columns.find((column) => column.key === 'user')?.class).toContain('w-36')
    expect(usageTable.props('userClickable')).toBe(false)
    expect(usageTable.props('compactUserColumn')).toBe(true)
  })

  // The user usage table mounts with default column preferences. The test protects showing
  // reasoning effort while keeping user-agent details hidden until the user enables them.
  it('shows reasoning effort column by default while hiding user agent', async () => {
    const wrapper = mountUsageView()
    await flushPromises()

    const usageTable = wrapper.findComponent(UsageTableStub)
    const columns = usageTable.props('columns') as Array<{
      key: string // table column identifier
    }>
    expect(columns.map((col) => col.key)).toContain('reasoning_effort')
    expect(columns.map((col) => col.key)).not.toContain('user_agent')
  })

  // Old user preferences precede TPS and then explicitly hide it. The test protects default
  // placement after latency, its unsortable status, and persistence after the page
  // remounts.
  it('TPS 默认放在延迟后，旧偏好可见且隐藏后重新挂载仍隐藏', async () => {
    localStorage.setItem('user-usage-hidden-columns', JSON.stringify(['ip_address']))
    const wrapper = mountUsageView()
    await flushPromises()
    const columns = wrapper.findComponent(UsageTableStub).props('columns') as Array<{
      key: string // table column identifier
      sortable?: boolean // server sorting availability flag
    }>
    expect(columns.findIndex((column) => column.key === 'tps')).toBe(
      columns.findIndex((column) => column.key === 'latency') + 1,
    )
    expect(columns.find((column) => column.key === 'tps')?.sortable).toBe(false)
    await wrapper.get('button[title="Columns"]').trigger('click')
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'TPS')!
      .trigger('click')
    expect(JSON.parse(localStorage.getItem('user-usage-hidden-columns')!)).toEqual([
      'ip_address',
      'tps',
    ])
    wrapper.unmount()
    const restored = mountUsageView()
    await flushPromises()
    expect(
      restored
        .findComponent(UsageTableStub)
        .props('columns')
        .map(
          (column: {
            key: string // table column identifier
          }) => column.key,
        ),
    ).not.toContain('tps')
  })

  // Clicking the toolbar export button on the user Usage view opens UsageExportDialog
  // with the current date range. Resolved filter chips include API key, group, zero-valued
  // balance billing, and false-valued legacy compaction selections.
  it('opens UsageExportDialog with inherited date range and resolved filter labels', async () => {
    getAvailable.mockResolvedValue([{ id: 3, name: 'vip', display_name: 'VIP Group' }])
    const wrapper = mountUsageView()
    await flushPromises()

    const vm = wrapper.vm as any
    vm.startDate = '2026-03-01'
    vm.endDate = '2026-03-08'
    vm.filters.api_key_id = 1
    vm.filters.group_id = 3
    vm.filters.model = 'gpt-5.4'
    vm.filters.billing_type = 0
    vm.filters.native_compaction_v2 = false
    await flushPromises()

    const dialog = wrapper.findComponent(UsageExportDialogStub)
    expect(dialog.props('show')).toBe(false)

    await wrapper.get('[data-testid="user-usage-export-btn"]').trigger('click')
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('mode')).toBe('user')
    expect(dialog.props('startDate')).toBe('2026-03-01')
    expect(dialog.props('endDate')).toBe('2026-03-08')
    expect(dialog.props('filterLabels')).toEqual([
      { key: 'api_key_id', label: 'API Key', value: 'demo-key' },
      { key: 'model', label: 'Model', value: 'gpt-5.4' },
      { key: 'group_id', label: 'Group', value: 'VIP Group' },
      { key: 'billing_type', label: 'Billing type', value: 'Balance' },
      { key: 'native_compaction_v2', label: 'Compaction', value: 'Legacy compaction' },
    ])
  })

  // Mounting the user Usage view with pending and running background exports populates the
  // active count on the toolbar button. The button stays enabled so users can reopen
  // UsageExportDialog to monitor progress or cancel tasks.
  it('displays active background export count on the toolbar button without disabling it', async () => {
    listExportTasks.mockResolvedValueOnce({
      items: [
        { id: 21, status: 'running' },
        { id: 20, status: 'pending' },
      ],
      total: 2,
      page: 1,
      page_size: 10,
      pages: 1,
    })

    const wrapper = mountUsageView()
    await flushPromises()

    const exportBtn = wrapper.get('[data-testid="user-usage-export-btn"]')
    expect(exportBtn.attributes('disabled')).toBeUndefined()
    expect(exportBtn.text()).toBe('Export (2)')

    const dialog = wrapper.findComponent(UsageExportDialogStub)
    dialog.vm.$emit('tasksChanged', 0)
    await flushPromises()
    expect(exportBtn.text()).toBe('Export')
  })
})
