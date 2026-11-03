import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import { defineComponent, h, onMounted } from 'vue'
import UserDashboardUsageChart from '../UserDashboardUsageChart.vue'
import UserDashboardUsageToolbar from '../UserDashboardUsageToolbar.vue'
import { provideUsageChartState } from '../usageChartState'
import Select from '@/components/common/Select.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import { Line } from 'vue-chartjs'
import { usageAPI } from '@/api/usage'
import { formatDayKey } from '../usageChartData'

// UserDashboardUsageChart.spec.ts mounts the production usage toolbar, chart, and model
// ranking inside a shared dashboard-state provider. API responses and chart drawing are
// controlled while selection, filtering, persistence, metric formatting, and emitted refresh
// actions run through the actual components.

// The chart stub accepts the produced data and options so assertions inspect configuration passed by the actual component.
vi.mock('vue-chartjs', () => ({
  Line: {
    name: 'Line',
    props: ['data', 'options', 'plugins'],
    render: () => null,
  },
}))

enableAutoUnmount(afterEach)

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      locale: { value: 'en' },
    }),
  }
})

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    formatBalanceAmount: (value: number) => String(value),
  }),
}))

vi.mock('@/api/usage', () => ({
  usageAPI: {
    getDashboardTrend: vi.fn(),
    getDashboardModels: vi.fn(),
  },
}))

vi.mock('@/api/keys', () => ({
  keysAPI: {
    list: vi.fn().mockResolvedValue({ items: [{ id: 7, name: 'personal-key' }] }),
  },
}))

vi.mock('@/api/groups', () => ({
  userGroupsAPI: {
    getAvailable: vi.fn().mockResolvedValue([{ id: 3, name: 'Claude' }]),
  },
}))

vi.mock('@/api/team', () => ({
  teamAPI: {
    current: vi.fn().mockRejectedValue({ reason: 'TEAM_NOT_FOUND' }),
    keys: vi.fn(),
  },
}))

// trendOf expands abbreviated request observations into complete zero-default trend records
// for chart scenarios. Callers override date, request count, and optional charged cost while
// the remaining token categories stay deterministic.
const trendOf = (
  points: Array<{
    date: string // Local bucket date label.
    requests: number // Recorded bucket request count.
    actual_cost?: number // Optional charged bucket amount.
  }>,
) => ({
  trend: points.map((item) => ({
    input_tokens: 0,
    output_tokens: 0,
    cache_creation_tokens: 0,
    cache_read_tokens: 0,
    total_tokens: 0,
    cost: 0,
    actual_cost: 0,
    ...item,
  })),
  start_date: '',
  end_date: '',
  granularity: 'day',
})

// DashboardHarness shares one usage controller between the production toolbar and chart,
// starts its initial loads, and forwards page-refresh events for assertions.
const DashboardHarness = defineComponent({
  props: { refreshing: { type: Boolean, default: false } },
  emits: ['refresh'],
  // setup provides dashboard state and renders the toolbar with the usage chart. Mount
  // loading runs through the real controller while the test API mocks supply the resulting
  // observations.
  setup(props, { emit }) {
    const state = provideUsageChartState()
    onMounted(() => {
      void state.load()
      void state.loadFilterOptions()
    })
    return () =>
      h('div', [
        h(UserDashboardUsageToolbar, {
          refreshing: props.refreshing,
          onRefresh: () => emit('refresh'),
        }),
        h(UserDashboardUsageChart),
      ])
  },
})

// mountChart renders the dashboard harness with inspectable chart and selection stubs. It
// drains initial API promises before returning the wrapper for interactions; enableAutoUnmount
// registers disposal after each case so the harness releases its component resources.
const mountChart = async () => {
  const wrapper = mount(DashboardHarness, {
    global: {
      stubs: {
        Line: true,
        DateRangePicker: true,
        Select: true,
        // Render filter-panel slot content directly so each dropdown remains inspectable in component assertions.
        FilterDropdown: { template: '<div><slot /></div>' },
        RouterLink: { template: '<a><slot /></a>' },
        ModelIcon: true,
      },
    },
  })
  await flushPromises()
  return wrapper
}

describe('UserDashboardUsageChart', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.mocked(usageAPI.getDashboardTrend).mockReset()
    vi.mocked(usageAPI.getDashboardModels).mockReset()
    vi.mocked(usageAPI.getDashboardModels).mockResolvedValue({
      models: [{ model: 'claude-opus', requests: 1 } as any],
      start_date: '',
      end_date: '',
    })
  })

  afterEach(() => {
    localStorage.clear()
  })

  // The default dashboard load returns current and prior request totals. The test protects
  // hourly queries from local midnight, displayed aggregate change, undefined cost
  // comparison, and the picker's matching endpoints for today.
  it('loads today and the previous period by default with totals and comparisons', async () => {
    const today = formatDayKey(new Date())
    vi.mocked(usageAPI.getDashboardTrend)
      .mockResolvedValueOnce(trendOf([{ date: `${today} 00:00`, requests: 30 }]) as any)
      .mockResolvedValueOnce(trendOf([{ date: '2000-01-01', requests: 20 }]) as any)

    const wrapper = await mountChart()

    expect(usageAPI.getDashboardTrend).toHaveBeenCalledTimes(2)
    const tomorrow = new Date()
    tomorrow.setDate(tomorrow.getDate() + 1)
    expect(vi.mocked(usageAPI.getDashboardTrend).mock.calls[0][0]).toMatchObject({
      start_date: `${today}T00:00:00`,
      end_date: `${formatDayKey(tomorrow)}T00:00:00`,
      granularity: 'hour',
    })
    expect(wrapper.get('[data-testid="usage-metric-requests"]').text()).toContain('30')
    expect(wrapper.get('[data-testid="usage-delta-requests"]').text()).toContain('+50%')
    expect(wrapper.get('[data-testid="usage-delta-cost"]').text()).toContain(
      'dashboard.usageChart.noPrevious',
    )
    // Inclusive picker dates name today at both endpoints despite the API's exclusive end.
    const picker = wrapper.findComponent(DateRangePicker)
    expect(picker.props('startDate')).toBe(today)
    expect(picker.props('endDate')).toBe(today)
  })

  // The selected interval returns no usage in either period. The test protects the chart
  // empty state after loading completes rather than rendering meaningless lines.
  it('所选范围没有用量时显示空状态', async () => {
    vi.mocked(usageAPI.getDashboardTrend).mockResolvedValue(trendOf([]) as any)
    const wrapper = await mountChart()
    expect(wrapper.find('[data-testid="usage-chart-empty"]').exists()).toBe(true)
  })

  // The user selects cost and the picker's last-twenty-four-hours shortcut. The test
  // protects hourly queries and saved metric and preset preferences for the next visit.
  it('日期选择器选近 24 小时后按小时重新取数，并记住指标与范围', async () => {
    vi.mocked(usageAPI.getDashboardTrend).mockResolvedValue(trendOf([]) as any)
    const wrapper = await mountChart()

    await wrapper.get('[data-testid="usage-metric-cost"]').trigger('click')
    wrapper
      .findComponent(DateRangePicker)
      .vm.$emit('change', { startDate: '2000-01-01', endDate: '2000-01-02', preset: 'last24Hours' })
    await flushPromises()

    const lastCall = vi.mocked(usageAPI.getDashboardTrend).mock.calls.at(-2)?.[0]
    expect(lastCall).toMatchObject({ granularity: 'hour' })
    expect(wrapper.get('[data-testid="usage-metric-cost"]').attributes('aria-selected')).toBe(
      'true',
    )
    expect(JSON.parse(localStorage.getItem('dashboard-usage-chart') || '{}')).toEqual({
      metric: 'cost',
      range: '24h',
    })
  })

  // The token metric begins with every category visible in its legend. The test checks that
  // toggling cache-read updates the control's pressed state to hidden.
  it('Token 指标可以开关分项', async () => {
    vi.mocked(usageAPI.getDashboardTrend).mockResolvedValue(trendOf([]) as any)
    const wrapper = await mountChart()
    const chip = wrapper.get('[data-testid="usage-series-cache_read_tokens"]')
    expect(chip.attributes('aria-pressed')).toBe('true')
    await chip.trigger('click')
    expect(chip.attributes('aria-pressed')).toBe('false')
  })

  // Usage filters expose personal keys, models, groups, and transport choices. The test
  // protects their option sources and verifies filtered trend and ranking requests while
  // model candidates remain range-wide.
  it('筛选条件与使用记录页一致，选择后按筛选重新取数', async () => {
    vi.mocked(usageAPI.getDashboardTrend).mockResolvedValue(trendOf([]) as any)
    const wrapper = await mountChart()

    const selects = wrapper.findAllComponents(Select)
    expect(selects).toHaveLength(7)
    const [keySelect, modelSelect, groupSelect, typeSelect] = selects
    expect(keySelect.props('options')).toEqual([
      { value: null, label: 'usage.allApiKeys' },
      { value: 7, label: 'personal-key' },
    ])
    expect(modelSelect.props('options')).toEqual([
      { value: null, label: 'admin.usage.allModels' },
      { value: 'claude-opus', label: 'claude-opus' },
    ])
    expect(groupSelect.props('options')).toEqual([
      { value: null, label: 'admin.usage.allGroups' },
      { value: 3, label: 'Claude' },
    ])

    vi.mocked(usageAPI.getDashboardTrend).mockClear()
    groupSelect.vm.$emit('update:modelValue', 3)
    typeSelect.vm.$emit('update:modelValue', 'sync')
    typeSelect.vm.$emit('change', 'sync', null)
    await flushPromises()

    const calls = vi.mocked(usageAPI.getDashboardTrend).mock.calls.map((call) => call[0])
    expect(calls).toHaveLength(2)
    expect(calls[0]).toMatchObject({ group_id: 3, request_type: 'sync', stream: false })
    expect(calls[0]).not.toHaveProperty('model')
    // Ranking requests follow selected filters while model candidates retain the unfiltered range result.
    const modelCalls = vi.mocked(usageAPI.getDashboardModels).mock.calls
    expect(modelCalls).toHaveLength(2)
    expect(modelCalls[1][0]).toMatchObject({ group_id: 3, request_type: 'sync' })
    expect(modelSelect.props('options')).toHaveLength(2)
  })

  // No usage filters are active during initial loading. The test protects reuse of one
  // model-statistics request for both the ranking and available model choices.
  it('没有筛选条件时，模型排行与模型候选共用一次请求', async () => {
    vi.mocked(usageAPI.getDashboardTrend).mockResolvedValue(trendOf([]) as any)
    await mountChart()
    expect(usageAPI.getDashboardModels).toHaveBeenCalledTimes(1)
    expect(vi.mocked(usageAPI.getDashboardModels).mock.calls[0][0]).not.toHaveProperty('group_id')
  })

  // The heading refresh action delegates work to the dashboard page. The test protects its
  // emitted event and disabled state while the page reports a refresh in progress.
  it('工具栏刷新按钮通知页面刷新，刷新中禁用', async () => {
    vi.mocked(usageAPI.getDashboardTrend).mockResolvedValue(trendOf([]) as any)
    const wrapper = await mountChart()
    const button = wrapper.get('[data-testid="usage-refresh"]')
    await button.trigger('click')
    expect(wrapper.emitted('refresh')).toHaveLength(1)
    await wrapper.setProps({ refreshing: true })
    expect(button.attributes('disabled')).toBeDefined()
  })

  // Token mode receives a current-day observation within the default interval. The test
  // protects four unstacked unfilled lines, the incomplete-bucket dashed segment, and both
  // drawing plugins.
  it('Token 画成四条不堆叠的折线，未结束的末段画虚线，并挂上竖线和描线插件', async () => {
    const today = formatDayKey(new Date())
    vi.mocked(usageAPI.getDashboardTrend).mockResolvedValue(
      trendOf([{ date: `${today} 00:00`, requests: 3, actual_cost: 1 }]) as any,
    )
    const wrapper = await mountChart()
    const line = wrapper.findComponent(Line)

    // datasets captures the token chart props so assertions can inspect its line styling,
    // incomplete-bucket marker, and category order.
    const datasets = line.props('data').datasets
    expect(
      datasets.map((item: { label: string /* Rendered token category label. */ }) => item.label),
    ).toEqual([
      'dashboard.usageChart.series.input',
      'dashboard.usageChart.series.output',
      'dashboard.usageChart.series.cacheCreation',
      'dashboard.usageChart.series.cacheRead',
    ])
    expect(datasets[0].cubicInterpolationMode).toBe('monotone')
    // Lines have no area fill and the vertical axis is not stacked.
    expect(
      datasets.every(
        (item: { fill: boolean /* Dataset area-fill enabled flag. */ }) => item.fill === false,
      ),
    ).toBe(true)
    expect(line.props('options').scales.y.stacked).toBeUndefined()

    // Today includes the current hour, so its final incoming segment is dashed while earlier segments remain solid.
    const lastIndex = line.props('data').labels.length - 1
    expect(datasets[0].segment.borderDash({ p1DataIndex: lastIndex })).toEqual([4, 4])
    expect(datasets[0].segment.borderDash({ p1DataIndex: lastIndex - 1 })).toBeUndefined()

    expect(
      line
        .props('plugins')
        .map((plugin: { id: string /* Registered chart plugin identifier. */ }) => plugin.id),
    ).toEqual(['usageCrosshair', 'usageReveal'])
  })

  // The selected metric changes after the chart has loaded usage. The test protects creation
  // of a new chart instance so its entrance reveal restarts.
  it('切换指标时重建图表，与首次打开播放同一套入场动画', async () => {
    const today = formatDayKey(new Date())
    vi.mocked(usageAPI.getDashboardTrend).mockResolvedValue(
      trendOf([{ date: `${today} 00:00`, requests: 3 }]) as any,
    )
    const wrapper = await mountChart()
    const before = wrapper.findComponent(Line).vm

    await wrapper.get('[data-testid="usage-metric-requests"]').trigger('click')
    expect(wrapper.findComponent(Line).vm).not.toBe(before)
  })

  // A single-metric chart has a successful prior-period result. The test protects aligned
  // dashed comparison data and removal of that dataset when its legend control is disabled.
  it('单指标叠加上一周期虚线，可以从图例关闭', async () => {
    const today = formatDayKey(new Date())
    vi.mocked(usageAPI.getDashboardTrend)
      .mockResolvedValueOnce(trendOf([{ date: `${today} 00:00`, requests: 30 }]) as any)
      .mockResolvedValueOnce(trendOf([{ date: '2000-01-01', requests: 20 }]) as any)
    const wrapper = await mountChart()
    await wrapper.get('[data-testid="usage-metric-requests"]').trigger('click')

    // datasets reads the chart's latest rendered dataset props after legend changes.
    const datasets = () => wrapper.findComponent(Line).props('data').datasets
    expect(datasets()).toHaveLength(2)
    expect(datasets()[1]).toMatchObject({ previous: true, borderDash: [4, 4], order: 1 })
    // Comparison points are limited to the number of available current-period buckets.
    expect(datasets()[1].data).toHaveLength(datasets()[0].data.length)

    await wrapper.get('[data-testid="usage-series-previous"]').trigger('click')
    expect(datasets()).toHaveLength(1)
  })

  // Models have different token and request rankings in shared dashboard data. The test
  // protects metric-dependent order and toggling a row's model filter on and off.
  it('模型排行跟随当前指标排序，点击后按模型筛选，再点一次取消', async () => {
    vi.mocked(usageAPI.getDashboardModels).mockResolvedValue({
      models: [
        {
          model: 'gpt-4o',
          requests: 5,
          total_tokens: 900,
          actual_cost: 1,
          input_tokens: 0,
          output_tokens: 0,
          cache_creation_tokens: 0,
          cache_read_tokens: 0,
          cost: 1,
        },
        {
          model: 'claude-opus',
          requests: 10,
          total_tokens: 100,
          actual_cost: 3,
          input_tokens: 0,
          output_tokens: 0,
          cache_creation_tokens: 0,
          cache_read_tokens: 0,
          cost: 3,
        },
      ],
      start_date: '',
      end_date: '',
    })
    vi.mocked(usageAPI.getDashboardTrend).mockResolvedValue(trendOf([]) as any)
    const wrapper = await mountChart()

    // order reads the displayed model identifiers in their current ranking order.
    const order = () =>
      wrapper.findAll('[data-testid^="top-model-"]').map((row) => row.attributes('data-testid'))
    // The default selected metric is token volume.
    expect(order()).toEqual(['top-model-gpt-4o', 'top-model-claude-opus'])
    await wrapper.get('[data-testid="usage-metric-requests"]').trigger('click')
    expect(order()).toEqual(['top-model-claude-opus', 'top-model-gpt-4o'])

    vi.mocked(usageAPI.getDashboardTrend).mockClear()
    await wrapper.get('[data-testid="top-model-claude-opus"]').trigger('click')
    await flushPromises()
    expect(vi.mocked(usageAPI.getDashboardTrend).mock.calls[0][0]).toMatchObject({
      model: 'claude-opus',
    })
    expect(wrapper.get('[data-testid="top-model-claude-opus"]').attributes('aria-pressed')).toBe(
      'true',
    )

    await wrapper.get('[data-testid="top-model-claude-opus"]').trigger('click')
    await flushPromises()
    expect(vi.mocked(usageAPI.getDashboardTrend).mock.calls.at(-1)?.[0]).not.toHaveProperty('model')
  })
})
