import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import EndpointDistributionChart from '../EndpointDistributionChart.vue'

// EndpointDistributionChart.spec.ts verifies metric sorting and doughnut proportions for
// endpoint usage. Chart substitutes expose dataset values while mounted table rows and
// tooltip callbacks establish that endpoint labels and proportions follow the selected
// metric.

// messages supplies stable English labels for rendered assertions, with unknown keys
// passed through by the local translation substitute during each component test.
const messages: Record<string, string> = {
  'usage.endpointDistribution': 'Endpoint Distribution',
  'usage.endpoint': 'Endpoint',
  'usage.inbound': 'Inbound',
  'usage.upstream': 'Upstream',
  'usage.path': 'Path',
  'admin.dashboard.requests': 'Requests',
  'admin.dashboard.tokens': 'Tokens',
  'admin.dashboard.actual': 'Actual',
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

describe('EndpointDistributionChart', () => {
  // Shared aggregate fixtures give token and spending metrics different sort orders so
  // each test can observe which metric controls the chart and table.
  const endpointStats = [
    {
      endpoint: '/v1/responses',
      requests: 9,
      total_tokens: 1200,
      cost: 1.8,
      actual_cost: 0.1,
    },
    {
      endpoint: '/v1/messages',
      requests: 4,
      total_tokens: 600,
      cost: 0.7,
      actual_cost: 0.9,
    },
  ]

  // Default chart props receive endpoints with opposing token and cost rankings. The
  // doughnut data must follow descending token totals and retain the raw values.
  it('renders a doughnut chart ordered by tokens by default', () => {
    const wrapper = mount(EndpointDistributionChart, {
      props: {
        endpointStats,
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.labels).toEqual(['/v1/responses', '/v1/messages'])
    expect(chartData.datasets[0].data).toEqual([1200, 600])

    const options = (wrapper.vm as any).$?.setupState.doughnutOptions
    expect(options.plugins.tooltip.enabled).toBe(false)
    expect(options.plugins.tooltip.external).toBeTypeOf('function')
    expect(options.plugins.tooltip.callbacks.label({ label: '/v1/responses', dataIndex: 0 })).toBe(
      '/v1/responses: 1.20K (66.7%)',
    )
  })

  // Actual-cost mode receives endpoints whose spending order differs from token order.
  // Both chart data and table rows must place the higher spending endpoint first.
  it('reorders rows by actual cost in actual cost mode', () => {
    const wrapper = mount(EndpointDistributionChart, {
      props: {
        endpointStats,
        metric: 'actual_cost',
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.labels).toEqual(['/v1/messages', '/v1/responses'])
    expect(chartData.datasets[0].data).toEqual([0.9, 0.1])

    const rows = wrapper.findAll('tbody tr')
    expect(rows[0].text()).toContain('/v1/messages')
    expect(rows[1].text()).toContain('/v1/responses')
  })
})
