import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import GroupDistributionChart from '../GroupDistributionChart.vue'

// GroupDistributionChart.spec.ts checks grouped usage ordering, chart proportions,
// optional cost columns, and ungrouped labels. Mounted chart substitutes expose raw
// doughnut datasets and tooltip options so chart values, table order, and
// displayed percentages can be compared for each selected metric.

// messages supplies stable English labels for rendered assertions, with unknown keys
// passed through by the local translation substitute during each component test.
const messages: Record<string, string> = {
  'admin.dashboard.groupDistribution': 'Group Distribution',
  'admin.dashboard.group': 'Group',
  'admin.dashboard.noGroup': 'No Group',
  'admin.dashboard.requests': 'Requests',
  'admin.dashboard.tokens': 'Tokens',
  'admin.dashboard.actual': 'Actual',
  'admin.dashboard.providerCost': 'Provider Cost',
  'admin.dashboard.standard': 'Standard',
  'admin.dashboard.metricTokens': 'By Tokens',
  'admin.dashboard.metricActualCost': 'By Actual Cost',
  'admin.dashboard.noDataAvailable': 'No data available',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

vi.mock('vue-chartjs', () => ({
  Doughnut: {
    props: ['data'],
    template: '<div class="chart-data">{{ JSON.stringify(data) }}</div>',
  },
}))

describe('GroupDistributionChart', () => {
  // Shared aggregate fixtures give token and spending metrics different sort orders so
  // each test can observe which metric controls the chart and table.
  const groupStats = [
    {
      group_id: 1,
      group_name: 'group-a',
      requests: 9,
      total_tokens: 1200,
      cost: 1.8,
      actual_cost: 0.1,
    },
    {
      group_id: 2,
      group_name: 'group-b',
      requests: 4,
      total_tokens: 600,
      cost: 0.7,
      actual_cost: 0.9,
    },
  ]

  // Default props receive two groups with opposing token and cost order. Chart values,
  // table rows, desktop alignment, and tooltip percentages must follow token usage.
  it('uses total_tokens and token ordering by default', () => {
    const wrapper = mount(GroupDistributionChart, {
      props: {
        groupStats,
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.labels).toEqual(['group-a', 'group-b'])
    expect(chartData.datasets[0].data).toEqual([1200, 600])

    const chartTableLayout = wrapper.find('table').element.parentElement?.parentElement
    expect(chartTableLayout?.className).toContain('sm:items-start')

    const rows = wrapper.findAll('tbody tr')
    expect(rows[0].text()).toContain('group-a')
    expect(rows[1].text()).toContain('group-b')

    const options = (wrapper.vm as any).$?.setupState.doughnutOptions
    expect(options.plugins.tooltip.enabled).toBe(false)
    expect(options.plugins.tooltip.external).toBeTypeOf('function')
    const label = options.plugins.tooltip.callbacks.label({
      label: 'group-a',
      dataIndex: 0,
    })
    expect(label).toBe('group-a: 1.20K (66.7%)')
  })

  // Actual-cost mode receives groups whose token ranking differs from spending. Both
  // rows and doughnut values must reorder together, and the tooltip must display the
  // spending share.
  it('uses actual_cost and reorders rows in actual cost mode', () => {
    const wrapper = mount(GroupDistributionChart, {
      props: {
        groupStats,
        metric: 'actual_cost',
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.labels).toEqual(['group-b', 'group-a'])
    expect(chartData.datasets[0].data).toEqual([0.9, 0.1])

    const rows = wrapper.findAll('tbody tr')
    expect(rows[0].text()).toContain('group-b')
    expect(rows[1].text()).toContain('group-a')

    const options = (wrapper.vm as any).$?.setupState.doughnutOptions
    const label = options.plugins.tooltip.callbacks.label({
      label: 'group-b',
      dataIndex: 0,
    })
    expect(label).toBe('group-b: $0.900 (90.0%)')
  })

  // User usage records omit provider charges and disable their display column. Header
  // and body column counts must agree, and provider-cost text must be absent.
  it('can hide provider cost for user usage stats without provider_cost', () => {
    const wrapper = mount(GroupDistributionChart, {
      props: {
        groupStats,
        showProviderCost: false,
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    expect(wrapper.text()).not.toContain('Provider Cost')
    expect(wrapper.findAll('thead th')).toHaveLength(5)
    expect(wrapper.findAll('tbody tr')[0].findAll('td')).toHaveLength(5)
  })

  // Group token totals differ by several orders of magnitude. Doughnut inputs must
  // retain the raw values while the tooltip rounds the small category percentage for
  // display.
  it('keeps doughnut slices proportional to raw values even with large differences', () => {
    const wrapper = mount(GroupDistributionChart, {
      props: {
        groupStats: [
          {
            group_id: 1,
            group_name: 'big',
            requests: 1,
            total_tokens: 1_000_000,
            cost: 1,
            actual_cost: 1,
          },
          {
            group_id: 2,
            group_name: 'small',
            requests: 1,
            total_tokens: 100,
            cost: 1,
            actual_cost: 1,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.labels).toEqual(['big', 'small'])
    // Raw dataset magnitudes protect proportional slices even when their ratio makes the
    // smaller displayed percentage round to zero.
    expect(chartData.datasets[0].data).toEqual([1_000_000, 100])

    const options = (wrapper.vm as any).$?.setupState.doughnutOptions
    const label = options.plugins.tooltip.callbacks.label({
      label: 'small',
      dataIndex: 1,
    })
    expect(label).toBe('small: 100 (0.0%)')
  })

  // An unnamed zero-ID group appears alongside a named group. The chart must label the
  // zero-ID entry with the translated ungrouped name after token sorting.
  it('labels entries without a group as No Group instead of 0', () => {
    const wrapper = mount(GroupDistributionChart, {
      props: {
        groupStats: [
          { group_id: 0, group_name: '', requests: 1, total_tokens: 10, cost: 0, actual_cost: 0 },
          {
            group_id: 1,
            group_name: 'group-a',
            requests: 1,
            total_tokens: 20,
            cost: 0,
            actual_cost: 0,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.labels).toEqual(['group-a', 'No Group'])
  })
})
