import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import * as XLSX from 'xlsx'

import UsageView from '../UsageView.vue'

// UsageView.spec.ts tests administrator usage queries, endpoint caching, route hydration,
// and workbook export. API substitutes control response order and child controls expose
// page actions, while spreadsheet assertions read serialized values and column preference
// cases retain the real page storage operations.

// Administrator API substitutes record list, summary, chart, user, and error queries;
// routeQuery supplies mutable initial navigation filters for hydration cases.
const { list, getStats, getSnapshotV2, getById, getModelStats, listErrorLogs, routeQuery } =
  vi.hoisted(() => {
    vi.stubGlobal('localStorage', {
      getItem: vi.fn(() => null),
      setItem: vi.fn(),
      removeItem: vi.fn(),
    })

    return {
      list: vi.fn(),
      getStats: vi.fn(),
      getSnapshotV2: vi.fn(),
      getById: vi.fn(),
      getModelStats: vi.fn(),
      listErrorLogs: vi.fn(),
      routeQuery: {} as Record<string, string>,
    }
  })

// exportMocks records export page requests, worksheet headers, appended rows, and the
// saved workbook so serialization assertions inspect the actual written result.
const exportMocks = vi.hoisted(() => ({
  list: vi.fn(),
  headers: vi.fn(),
  rows: vi.fn(),
  save: vi.fn(),
}))
vi.mock('xlsx', async (importOriginal) => {
  const actual = await importOriginal<typeof import('xlsx')>()
  exportMocks.headers.mockImplementation(actual.utils.aoa_to_sheet)
  exportMocks.rows.mockImplementation(actual.utils.sheet_add_aoa)
  return {
    ...actual,
    utils: { ...actual.utils, aoa_to_sheet: exportMocks.headers, sheet_add_aoa: exportMocks.rows },
  }
})
vi.mock('file-saver', () => ({ saveAs: exportMocks.save }))

// messages supplies administrator chart, table, and export labels so tests locate
// controls and compare spreadsheet columns against their headings.
const messages: Record<string, string> = {
  'usage.tps': 'TPS',
  'usage.tpsExport': 'TPS (tok/s)',
  'admin.dashboard.timeRange': 'Time Range',
  'admin.dashboard.day': 'Day',
  'admin.dashboard.hour': 'Hour',
  'admin.usage.failedToLoadUser': 'Failed to load user',
  'admin.usage.requestId': 'Request ID',
  'admin.usage.upstreamRequestId': 'Upstream ID',
  'usage.requestedModel': 'Requested model',
  'usage.sentUpstreamModel': 'Sent upstream model',
  'usage.upstreamResponseModel': 'Upstream response model',
  'usage.upstreamModelMismatch': 'Upstream model mismatch',
  'common.yes': 'Yes',
  'common.no': 'No',
}

// formatLocalDate formats fixture dates using browser-local calendar components for
// expected query values.
const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

vi.mock('@/api/admin', () => ({
  adminAPI: {
    usage: {
      list,
      getStats,
    },
    dashboard: {
      getSnapshotV2,
      getModelStats,
    },
    users: {
      getById,
    },
  },
}))

vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: {
    list: exportMocks.list,
  },
}))

vi.mock('@/api/admin/ops', () => ({
  listErrorLogs,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showWarning: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
  }),
}))

vi.mock('@/utils/format', () => ({
  formatReasoningEffort: (value: string | null | undefined) => value ?? '-',
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({ balanceUnitName: { value: 'Credits' }, usdUnitName: 'USD' }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

vi.mock('vue-router', () => ({
  useRoute: () => ({
    query: routeQuery,
  }),
}))

// AppLayoutStub renders the administrator page slot so page-loading tests can inspect
// sections independently of the surrounding application layout.
const AppLayoutStub = { template: '<div><slot /></div>' }
// UsageFiltersStub exposes search revision and label methods used by route hydration,
// with a settings slot for column-control interactions.
const UsageFiltersStub = defineComponent({
  // setup exposes a mutable user label and revision-aware setter for route-hydration tests.
  setup(_, { expose }) {
    const userKeyword = ref('')
    let userSearchRevision = 0
    // setUserKeyword advances the fixture search revision before publishing the requested
    // label. This lets hydration tests simulate later typing even when the numeric user
    // filter remains unchanged.
    const setUserKeyword = (email: string) => {
      userSearchRevision += 1
      userKeyword.value = email
    }
    expose({
      getUserSearchRevision: () => userSearchRevision,
      setUserKeyword,
      simulateUserInput: setUserKeyword,
    })
    return { userKeyword }
  },
  template:
    '<div><span data-test="user-filter-label">{{ userKeyword }}</span><slot name="after-reset" /></div>',
})
// UsageTableStub accepts visible column definitions so preference tests observe the
// exact columns passed by the administrator page.
const UsageTableStub = {
  props: [
    'columns', // visible table column definitions
  ],
  emits: ['userClick'],
  template:
    '<div data-test="usage-table"><button class="user-click" @click="$emit(\'userClick\', 2)">user</button></div>',
}
// UserTokenRankingStub exposes a row-selection event so the page can exercise lazy
// mounting and ranking drill-down through the rendered button.
const UserTokenRankingStub = {
  emits: ['select-user'],
  template:
    '<div data-test="ranking"><button class="pick-user" @click="$emit(\'select-user\', 5, \'rank@test.com\')">pick</button></div>',
}
// ModelDistributionChartStub exposes the selected metric and emits changes so tests
// inspect independent model-chart display choices.
const ModelDistributionChartStub = {
  props: [
    'metric', // selected chart measurement key
  ],
  emits: ['update:metric'],
  template: `
    <div data-test="model-chart">
      <span class="metric">{{ metric }}</span>
      <button class="switch-metric" @click="$emit('update:metric', 'actual_cost')">switch</button>
    </div>
  `,
}
// GroupDistributionChartStub exposes the metric so tests compare group-chart
// selection with the independent model chart while using the same snapshot.
const GroupDistributionChartStub = {
  props: [
    'metric', // selected chart measurement key
  ],
  emits: ['update:metric'],
  template: `
    <div data-test="group-chart">
      <span class="metric">{{ metric }}</span>
      <button class="switch-metric" @click="$emit('update:metric', 'actual_cost')">switch</button>
    </div>
  `,
}
// EndpointDistributionChartStub marks the endpoint card without drawing a canvas,
// keeping page-level loader and layout tests independent of Chart.js rendering.
const EndpointDistributionChartStub = {
  template: '<div data-test="endpoint-chart" />',
}

// mountRouteFilteredUsageView renders the administrator page with controlled child
// components and the route-label search stub. Tests retain the real page loaders while
// recording query parameters and observing filter-label changes.
const mountRouteFilteredUsageView = () =>
  mount(UsageView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        UsageStatsCards: true,
        UsageFilters: UsageFiltersStub,
        UsageTable: true,
        UsageExportProgress: true,
        UsageCleanupDialog: true,
        UserBalanceHistoryModal: true,
        Pagination: true,
        Select: true,
        DateRangePicker: true,
        Icon: true,
        TokenUsageTrend: true,
        ModelDistributionChart: true,
        GroupDistributionChart: true,
        EndpointDistributionChart: true,
        UserTokenRanking: true,
      },
    },
  })

describe('admin UsageView 路由筛选', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    Object.keys(routeQuery).forEach((key) => delete routeQuery[key])
    list.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockReset().mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockReset().mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockReset().mockResolvedValue({ models: [] })
    getById.mockReset()
  })

  // Clear the shared route query before restoring the clock so later page mounts cannot
  // inherit another hydration scenario's user or date filters.
  afterEach(() => {
    Object.keys(routeQuery).forEach((key) => delete routeQuery[key])
    vi.useRealTimers()
  })

  // Endpoint selection changes while an earlier raw-source request is pending. The test
  // protects cancellation, source-specific queries, cache reuse, and rejection of a
  // superseded chart result.
  it('端点图按需加载，切换时取消旧请求且复用已加载结果', async () => {
    const wrapper = mountRouteFilteredUsageView()
    await flushPromises()
    expect(getStats).toHaveBeenCalledTimes(1)
    expect(getStats.mock.calls[0][0].endpoint_source).toBe('inbound')
    const chart = wrapper.findComponent({ name: 'EndpointDistributionChart' })
    chart.vm.$emit('update:source', 'upstream')
    await flushPromises()
    expect(getStats.mock.calls[1][0].endpoint_source).toBe('upstream')
    let resolvePath!: (value: any) => void
    getStats.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePath = resolve
        }),
    )
    chart.vm.$emit('update:source', 'path')
    await flushPromises()
    expect(getStats.mock.calls[2][0].endpoint_source).toBe('path')
    const signal = getStats.mock.calls[2][1].signal as AbortSignal
    chart.vm.$emit('update:source', 'upstream')
    await flushPromises()
    expect(signal.aborted).toBe(true)
    expect(getStats).toHaveBeenCalledTimes(3)
    resolvePath({ endpoint_paths: [{ endpoint: 'stale', requests: 999 }] })
    await flushPromises()
    expect(chart.props('endpointPathStats')).toEqual([])
    wrapper.unmount()
  })

  // Filters change while a raw endpoint chart is selected. The test protects independent
  // summary and raw-source loading and canceling the obsolete endpoint request during the
  // refresh.
  it('筛选变更后重新加载当前端点图，摘要独立更新', async () => {
    const wrapper = mountRouteFilteredUsageView()
    await flushPromises()
    const chart = wrapper.findComponent({ name: 'EndpointDistributionChart' })
    chart.vm.$emit('update:source', 'upstream')
    await flushPromises()
    getStats.mockClear()
    getStats.mockImplementation((params) =>
      params.endpoint_source === 'upstream'
        ? new Promise(() => {})
        : Promise.resolve({ total_requests: 17, endpoints: [] }),
    )
    wrapper.findComponent(UsageFiltersStub).vm.$emit('change')
    await flushPromises()
    expect(getStats.mock.calls.map((call) => call[0].endpoint_source)).toEqual([
      'upstream',
      'inbound',
    ])
    expect(wrapper.findComponent({ name: 'UsageStatsCards' }).props('stats').total_requests).toBe(
      17,
    )
    expect(chart.props('loading')).toBe(true)
    const rawCall = getStats.mock.calls.find((call) => call[0].endpoint_source === 'upstream')!
    wrapper.unmount()
    expect(rawCall[1].signal.aborted).toBe(true)
  })

  // Exported rows include matching, different, and unknown upstream response models. The
  // test protects the corresponding model cells, mismatch values, and each row matching the
  // header width.
  it('导出上游响应模型并区分相同、不同和未知', async () => {
    exportMocks.list.mockResolvedValue({
      total: 3,
      pages: 1,
      items: [
        { model: 'sent', upstream_response_model: 'runtime', upstream_model_mismatch: true },
        { model: 'sent', upstream_response_model: 'sent', upstream_model_mismatch: false },
        { model: 'historical' },
      ],
    })
    exportMocks.headers.mockClear()
    exportMocks.rows.mockClear()
    const wrapper = mountRouteFilteredUsageView()
    await flushPromises()
    wrapper.findComponent(UsageFiltersStub).vm.$emit('export')
    await flushPromises()
    const headers = exportMocks.headers.mock.calls[0]?.[0]?.[0] as string[]
    const rows = exportMocks.rows.mock.calls[0]?.[1] as unknown[][]
    expect(headers).toContain('usage.userBilled (Credits)')
    expect(headers).toContain('usage.original (USD)')
    expect(headers).toContain('usage.providerBilled (USD)')
    const model = headers.indexOf('Upstream response model')
    const mismatch = headers.indexOf('Upstream model mismatch')
    expect(model).toBeGreaterThan(-1)
    expect(rows.map((row) => [row[model], row[mismatch]])).toEqual([
      ['runtime', 'Yes'],
      ['sent', 'No'],
      ['', ''],
    ])
    expect(rows.every((row) => row.length === headers.length)).toBe(true)
    wrapper.unmount()
  })

  // Workbook export spans multiple pages containing numeric and missing TPS observations.
  // The test protects numeric cell types, one-decimal display formats, empty missing
  // values, and one final download.
  it('TPS 分页导出为数值并设置一位小数，服务端未返回估算时留空', async () => {
    const row = { model: 'test', output_tokens: 100, duration_ms: 5000, ttft_ms: 1000, tps: 25 }
    exportMocks.list
      .mockReset()
      .mockResolvedValueOnce({ total: 104, items: Array.from({ length: 100 }, () => row) })
      .mockResolvedValueOnce({
        total: 104,
        items: [
          { ...row, output_tokens: 101, duration_ms: 4000, tps: 33.7 },
          { ...row, output_tokens: 23, duration_ms: 21000, tps: 1.2 },
          { ...row, output_tokens: 469, duration_ms: 21000, tps: 23.5 },
          { ...row, ttft_ms: null, tps: null },
        ],
      })
    exportMocks.headers.mockClear()
    exportMocks.rows.mockClear()
    exportMocks.save.mockClear()
    const wrapper = mountRouteFilteredUsageView()
    await flushPromises()
    wrapper.findComponent(UsageFiltersStub).vm.$emit('export')
    await flushPromises()

    const { utils, write, read } = XLSX
    const headers = exportMocks.headers.mock.calls[0][0][0] as string[]
    const column = headers.indexOf('TPS (tok/s)')
    expect(column).toBe(headers.indexOf('usage.duration') + 1)
    expect(exportMocks.list.mock.calls.map((call) => call[0].page)).toEqual([1, 2])
    expect(exportMocks.rows).toHaveBeenCalledTimes(2)
    const sheet = exportMocks.rows.mock.calls[1][0]
    const workbook = utils.book_new()
    utils.book_append_sheet(workbook, sheet, 'Usage')
    // Read the serialized workbook so assertions inspect actual page-ending values,
    // cell types, and number formats after writing.
    const restored = read(write(workbook, { type: 'array', bookType: 'xlsx' }), {
      type: 'array',
      cellNF: true,
    }).Sheets.Usage
    expect(restored[utils.encode_cell({ r: 1, c: column })]).toMatchObject({
      t: 'n',
      v: 25,
      z: '0.0',
      w: '25.0',
    })
    expect(restored[utils.encode_cell({ r: 101, c: column })]).toMatchObject({
      t: 'n',
      v: 33.7,
      z: '0.0',
      w: '33.7',
    })
    expect(restored[utils.encode_cell({ r: 102, c: column })]).toMatchObject({
      t: 'n',
      v: 1.2,
      z: '0.0',
      w: '1.2',
    })
    expect(restored[utils.encode_cell({ r: 103, c: column })]).toMatchObject({
      t: 'n',
      v: 23.5,
      z: '0.0',
      w: '23.5',
    })
    expect(restored[utils.encode_cell({ r: 104, c: column })]?.v ?? '').toBe('')
    expect(exportMocks.save).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  // The route supplies a user identifier before the usage page mounts. The test protects
  // using that predicate in list queries and resolving the corresponding readable filter
  // label.
  it('应用 user_id 请求筛选时回显路由用户', async () => {
    routeQuery.user_id = '42'
    getById.mockResolvedValue({ id: 42, email: 'route-user@test.com' })

    const wrapper = mountRouteFilteredUsageView()
    await flushPromises()

    expect(getById).toHaveBeenCalledWith(42, true)
    expect(list).toHaveBeenCalledWith(expect.objectContaining({ user_id: 42 }), expect.anything())
    expect(wrapper.find('[data-test="user-filter-label"]').text()).toBe('route-user@test.com')
  })

  // The selected user changes before route-label retrieval completes. The test protects
  // retaining the newer user label when the earlier user response eventually arrives
  // successfully.
  it('user_id 已变化时忽略过期的路由用户标签', async () => {
    routeQuery.user_id = '42'
    let resolveLookup!: (user: {
      id: number // record numeric identifier
      email: string // user email display text
    }) => void
    getById.mockReturnValue(
      new Promise((resolve) => {
        resolveLookup = resolve
      }),
    )

    const wrapper = mountRouteFilteredUsageView()
    await wrapper.vm.$nextTick()
    ;(wrapper.vm as any).filters.user_id = 84
    ;(wrapper.findComponent(UsageFiltersStub).vm as any).setUserKeyword('current-user@test.com')

    resolveLookup({ id: 42, email: 'stale-user@test.com' })
    await flushPromises()

    expect(wrapper.find('[data-test="user-filter-label"]').text()).toBe('current-user@test.com')
  })

  // The user types a search after route-label retrieval starts for the same identifier. The
  // test protects the newer typed label from a delayed successful hydration response.
  it('路由用户查询成功时不覆盖后续输入', async () => {
    routeQuery.user_id = '42'
    let resolveLookup!: (user: {
      id: number // record numeric identifier
      email: string // user email display text
    }) => void
    getById.mockReturnValue(
      new Promise((resolve) => {
        resolveLookup = resolve
      }),
    )

    const wrapper = mountRouteFilteredUsageView()
    await wrapper.vm.$nextTick()
    ;(wrapper.findComponent(UsageFiltersStub).vm as any).simulateUserInput('new-search@test.com')

    resolveLookup({ id: 42, email: 'route-user@test.com' })
    await flushPromises()

    expect((wrapper.vm as any).filters.user_id).toBe(42)
    expect(wrapper.find('[data-test="user-filter-label"]').text()).toBe('new-search@test.com')
  })

  // The user types a search after route-label retrieval starts and that lookup fails. The
  // test protects retaining the typed text instead of replacing it with numeric identity.
  it('路由用户查询失败时不覆盖后续输入', async () => {
    routeQuery.user_id = '42'
    let rejectLookup!: (error: Error) => void
    getById.mockReturnValue(
      new Promise((_, reject) => {
        rejectLookup = reject
      }),
    )

    const wrapper = mountRouteFilteredUsageView()
    await wrapper.vm.$nextTick()
    ;(wrapper.findComponent(UsageFiltersStub).vm as any).simulateUserInput('new-search@test.com')

    rejectLookup(new Error('lookup failed'))
    await flushPromises()

    expect((wrapper.vm as any).filters.user_id).toBe(42)
    expect(wrapper.find('[data-test="user-filter-label"]').text()).toBe('new-search@test.com')
  })

  // Route-label retrieval fails while its user selection remains current. The test protects
  // keeping the user predicate and displaying the numeric identifier as the readable
  // fallback.
  it('路由用户查询失败时回显用户 ID', async () => {
    routeQuery.user_id = '42'
    getById.mockRejectedValue(new Error('lookup failed'))

    const wrapper = mountRouteFilteredUsageView()
    await flushPromises()

    expect(list).toHaveBeenCalledWith(expect.objectContaining({ user_id: 42 }), expect.anything())
    expect(wrapper.find('[data-test="user-filter-label"]').text()).toBe('42')
  })
})

describe('admin UsageView distribution metric toggles', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getById.mockReset()
    getModelStats.mockReset()

    list.mockResolvedValue({
      items: [],
      total: 0,
      pages: 0,
    })
    getStats.mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({
      trend: [],
      models: [],
      groups: [],
    })
    getModelStats.mockResolvedValue({ models: [] })
  })

  // Restore the real clock after each fake-timer scenario so later cases do not inherit
  // its pending debounce or refresh schedule.
  afterEach(() => {
    vi.useRealTimers()
  })

  // A model-statistics refresh remains pending after earlier rows were displayed. The test
  // protects retaining those rows during refresh and replacing them when the new response
  // arrives.
  it('keeps previous model stats visible during refresh until new data arrives', async () => {
    // The initial response supplies model A before the delayed refresh begins.
    getModelStats.mockResolvedValueOnce({ models: [{ model: 'A', total_tokens: 10 }] })

    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: true,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          AuditLogModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: ModelDistributionChartStub,
          GroupDistributionChart: GroupDistributionChartStub,
          EndpointDistributionChart: true,
          UserTokenRanking: true,
        },
      },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()
    expect((wrapper.vm as any).requestedModelStats).toEqual([{ model: 'A', total_tokens: 10 }])

    // resolveSecond supplies the replaceable resolver for a pending model-statistics
    // refresh.
    let resolveSecond: (v: any) => void = () => {}
    getModelStats.mockReturnValueOnce(
      new Promise((res) => {
        resolveSecond = res
      }),
    )
    ;(wrapper.vm as any).refreshData()
    await flushPromises()
    expect((wrapper.vm as any).requestedModelStats).toEqual([{ model: 'A', total_tokens: 10 }])

    // The completed refresh replaces the displayed model with B.
    resolveSecond({ models: [{ model: 'B', total_tokens: 20 }] })
    await flushPromises()
    expect((wrapper.vm as any).requestedModelStats).toEqual([{ model: 'B', total_tokens: 20 }])
  })

  // The usage page renders both filters and a records table. The test protects separate
  // card sections so the filter container does not also own the table.
  it('renders filters and the usage table in separate sections', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: UsageTableStub,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
          UserTokenRanking: true,
        },
      },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    const filtersCard = wrapper.get('[data-testid="admin-usage-filters-card"]')
    const tableSection = wrapper.get('[data-testid="admin-usage-table-section"]')
    expect(filtersCard.classes()).toContain('card')
    expect(filtersCard.find('[data-test="usage-table"]').exists()).toBe(false)
    expect(tableSection.find('[data-test="usage-table"]').exists()).toBe(true)
  })

  // Model and group charts change their display metric independently. The test protects
  // their separate selections and reuse of existing snapshot data
  // without another fetch.
  it('keeps model and group metric toggles independent without refetching chart data', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: true,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: ModelDistributionChartStub,
          GroupDistributionChart: GroupDistributionChartStub,
          EndpointDistributionChart: EndpointDistributionChartStub,
          UserTokenRanking: true,
        },
      },
    })

    vi.advanceTimersByTime(120)
    await flushPromises()

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)
    expect(getSnapshotV2).toHaveBeenCalledWith(
      expect.objectContaining({
        start_date: formatLocalDate(yesterday),
        end_date: formatLocalDate(now),
        granularity: 'hour',
      }),
    )

    const modelChart = wrapper.find('[data-test="model-chart"]')
    const groupChart = wrapper.find('[data-test="group-chart"]')

    expect(modelChart.find('.metric').text()).toBe('tokens')
    expect(groupChart.find('.metric').text()).toBe('tokens')

    await modelChart.find('.switch-metric').trigger('click')
    await flushPromises()

    expect(modelChart.find('.metric').text()).toBe('actual_cost')
    expect(groupChart.find('.metric').text()).toBe('tokens')
    expect(getSnapshotV2).toHaveBeenCalledTimes(1)

    await groupChart.find('.switch-metric').trigger('click')
    await flushPromises()

    expect(modelChart.find('.metric').text()).toBe('actual_cost')
    expect(groupChart.find('.metric').text()).toBe('actual_cost')
    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
  })
})

describe('admin UsageView request ID column visibility', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.mocked(localStorage.getItem).mockReset().mockReturnValue(null)
    vi.mocked(localStorage.setItem).mockReset()
    list.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockReset().mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockReset().mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockReset().mockResolvedValue({ models: [] })
  })

  // Restore the real clock after each fake-timer scenario so later cases do not inherit
  // its pending debounce or refresh schedule.
  afterEach(() => {
    vi.useRealTimers()
  })

  // mountColumnView renders administrator usage with the column-aware table stub. Storage
  // substitutes and the real settings actions remain active so tests can compare visible
  // definitions across preference changes.
  const mountColumnView = () =>
    mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: UsageTableStub,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          AuditLogModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
          UserTokenRanking: true,
        },
      },
    })

  // Old column preferences precede the TPS column and then explicitly hide it. The test
  // protects its default placement, unsortable status, and retaining the hidden choice
  // after remount.
  it('TPS 默认可见，旧偏好补入该列，隐藏后重新挂载仍隐藏', async () => {
    const saved = new Map([['usage-hidden-columns', JSON.stringify(['ip_address'])]])
    vi.mocked(localStorage.getItem).mockImplementation((key) => saved.get(key) ?? null)
    vi.mocked(localStorage.setItem).mockImplementation((key, value) => {
      saved.set(key, value)
    })
    const wrapper = mountColumnView()
    await flushPromises()
    const columns = wrapper.findComponent(UsageTableStub).props('columns') as Array<{
      key: string // table column identifier
      sortable?: boolean // server sorting availability flag
    }>
    expect(columns.findIndex((column) => column.key === 'tps')).toBe(
      columns.findIndex((column) => column.key === 'latency') + 1,
    )
    expect(columns.find((column) => column.key === 'tps')?.sortable).toBe(false)
    await wrapper.get('button[title="admin.users.columnSettings"]').trigger('click')
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'TPS')!
      .trigger('click')
    expect(JSON.parse(saved.get('usage-hidden-columns')!)).toContain('tps')
    wrapper.unmount()
    const restored = mountColumnView()
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
    restored.unmount()
  })

  // The request-ID column is visible by default and then hidden through settings. The test
  // protects removing it from the table and persisting that explicit preference.
  it('shows request ID by default and persists an explicit hide', async () => {
    const wrapper = mountColumnView()
    await wrapper.vm.$nextTick()

    const usageTable = wrapper.findComponent(UsageTableStub)
    expect(usageTable.props('columns')).toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'request_id' })]),
    )

    await wrapper.get('button[title="admin.users.columnSettings"]').trigger('click')
    const requestIdToggle = wrapper
      .findAll('button')
      .find((button) => button.text() === 'Request ID')
    expect(requestIdToggle).toBeDefined()
    await requestIdToggle!.trigger('click')

    expect(usageTable.props('columns')).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'request_id', label: 'Request ID' })]),
    )
    expect(localStorage.setItem).toHaveBeenCalledWith(
      'usage-hidden-columns-version',
      'upstream-request-id-hidden-by-default',
    )
  })

  // The upstream-ID column starts hidden and is enabled through settings. The test protects
  // its default omission and adding its definition to the visible table columns.
  it('keeps upstream ID hidden by default and allows enabling it from column settings', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: UsageTableStub,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          AuditLogModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
          UserTokenRanking: true,
        },
      },
    })
    await wrapper.vm.$nextTick()

    const usageTable = wrapper.findComponent(UsageTableStub)
    expect(usageTable.props('columns')).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'upstream_request_id' })]),
    )

    await wrapper.get('button[title="admin.users.columnSettings"]').trigger('click')
    const upstreamToggle = wrapper
      .findAll('button')
      .find((button) => button.text() === 'Upstream ID')
    expect(upstreamToggle).toBeDefined()
    await upstreamToggle!.trigger('click')

    expect(usageTable.props('columns')).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ key: 'upstream_request_id', label: 'Upstream ID' }),
      ]),
    )
  })

  // An old preference contains user-selected hidden columns before migration. The test
  // protects retaining those choices while adding the upstream-ID default and writing the
  // updated preference.
  it('migrates an old preference without replacing existing hidden columns', async () => {
    vi.mocked(localStorage.getItem).mockImplementation((key: string) => {
      if (key === 'usage-hidden-columns') return JSON.stringify(['ip_address'])
      return null
    })

    const wrapper = mountColumnView()
    await wrapper.vm.$nextTick()
    const columns = wrapper.findComponent(UsageTableStub).props('columns') as Array<{
      key: string // table column identifier
    }>

    expect(columns).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'ip_address' })]),
    )
    expect(columns).toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'request_id' })]),
    )
    expect(localStorage.setItem).toHaveBeenCalledWith(
      'usage-hidden-columns',
      expect.stringContaining('request_id'),
    )
  })

  // The earlier request-ID preference version is stored with request ID enabled.
  // The test protects that saved visible choice when the usage table mounts again.
  it('respects an enabled request ID after the migration version is stored', async () => {
    vi.mocked(localStorage.getItem).mockImplementation((key: string) => {
      if (key === 'usage-hidden-columns') return JSON.stringify(['ip_address'])
      if (key === 'usage-hidden-columns-version') return 'request-id-hidden-by-default'
      return null
    })

    const wrapper = mountColumnView()
    await wrapper.vm.$nextTick()

    expect(wrapper.findComponent(UsageTableStub).props('columns')).toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'request_id' })]),
    )
  })
})

describe('admin UsageView handleUserClick', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getById.mockReset()

    list.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({ trend: [], models: [], groups: [] })
  })

  // Restore the real clock after each fake-timer scenario so later cases do not inherit
  // its pending debounce or refresh schedule.
  afterEach(() => {
    vi.useRealTimers()
  })

  // A usage row refers to a user who may have been deleted. The test protects requesting
  // the user with include_deleted when the row emits its navigation event.
  it('opens user via include_deleted when clicking a usage row user', async () => {
    getById.mockResolvedValue({ id: 2, email: 'd@test.com', deleted_at: '2026-05-28T00:00:00Z' })

    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: UsageTableStub,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          AuditLogModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
          UserTokenRanking: true,
        },
      },
    })

    vi.advanceTimersByTime(120)
    await flushPromises()

    await wrapper.find('[data-test="usage-table"] .user-click').trigger('click')
    await flushPromises()

    expect(getById).toHaveBeenCalledWith(2, true)
  })
})

describe('admin UsageView errors tab filter forwarding', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getModelStats.mockReset()
    listErrorLogs.mockReset()

    list.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockResolvedValue({ models: [] })
    listErrorLogs.mockResolvedValue({ items: [], total: 0, pages: 0 })
  })

  // Restore the real clock after each fake-timer scenario so later cases do not inherit
  // its pending debounce or refresh schedule.
  afterEach(() => {
    vi.useRealTimers()
  })

  // Shared model, provider, and group filters are set before opening errors. The test
  // protects forwarding those supported predicates to the administrator error-list query
  // for that tab.
  it('forwards model/provider_id/group_id to listErrorLogs on the errors tab', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: true,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          AuditLogModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
          UserTokenRanking: true,
          OpsErrorLogTable: true,
          OpsErrorDetailModal: true,
        },
      },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    // Set the model, provider, and group filters before opening the error tab.
    const vm = wrapper.vm as any
    vm.filters.model = 'gpt-5.3-codex'
    vm.filters.provider_id = 7
    vm.filters.group_id = 3
    await flushPromises()

    // Opening the second tab triggers its error loader using the current shared filter values.
    const tabs = wrapper.findAll('[data-testid="usage-detail-tab"]')
    await tabs[1].trigger('click')
    await flushPromises()

    expect(listErrorLogs).toHaveBeenCalledWith(
      expect.objectContaining({
        view: 'all',
        model: 'gpt-5.3-codex',
        provider_id: 7,
        group_id: 3,
      }),
    )
  })
})

describe('admin UsageView ranking tab', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getModelStats.mockReset()

    list.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockResolvedValue({ models: [] })
  })

  // Restore the real clock after each fake-timer scenario so later cases do not inherit
  // its pending debounce or refresh schedule.
  afterEach(() => {
    vi.useRealTimers()
  })

  // The ranking tab is unopened until the user selects it. The test protects lazy mounting
  // and row drill-down updating user filters before returning to usage records.
  it('mounts ranking lazily and drill-down sets user filter then jumps back to usage tab', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: true,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
          UserTokenRanking: UserTokenRankingStub,
          OpsErrorLogTable: true,
          OpsErrorDetailModal: true,
        },
      },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    // The ranking component stays unmounted before the ranking tab is selected.
    expect(wrapper.find('[data-test="ranking"]').exists()).toBe(false)

    const tabs = wrapper.findAll('[data-testid="usage-detail-tab"]')
    expect(tabs).toHaveLength(3)
    await tabs[2].trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="ranking"]').exists()).toBe(true)

    // Drill-down updates user identity, returns to usage records, and retrieves the newly filtered list.
    list.mockClear()
    await wrapper.find('[data-test="ranking"] .pick-user').trigger('click')
    await flushPromises()

    expect((wrapper.vm as any).activeTab).toBe('usage')
    expect((wrapper.vm as any).filters.user_id).toBe(5)
    expect(list).toHaveBeenCalledWith(expect.objectContaining({ user_id: 5 }), expect.anything())
  })
})
