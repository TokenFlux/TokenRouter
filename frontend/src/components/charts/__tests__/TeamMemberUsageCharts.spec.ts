import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import TeamMemberUsageCharts from '../TeamMemberUsageCharts.vue'

// TeamMemberUsageCharts.spec.ts checks how member summaries become daily and total-
// spending chart inputs. Hoisted chart spies capture props from mounted substitutes,
// allowing assertions to compare shared dates, missing-date zeroes, member order, and
// actual-cost totals.

// lineProps and doughnutProps capture the inputs passed to chart substitutes, allowing tests
// to inspect generated datasets while the substitutes render no canvas.
const lineProps = vi.hoisted(() => vi.fn())
const doughnutProps = vi.hoisted(() => vi.fn())

vi.mock('vue-chartjs', () => ({
  Line: {
    props: ['data', 'options'],
    // setup records chart inputs and returns an empty render function.
    setup(props: any) {
      lineProps(props)
      return () => null
    },
  },
  Doughnut: {
    props: ['data', 'options'],
    // setup records chart inputs and returns an empty render function.
    setup(props: any) {
      doughnutProps(props)
      return () => null
    },
  },
}))

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useTheme', () => ({ useTheme: () => ({ isDark: { value: false } }) }))
vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({ formatBalanceAmount: (value: number) => `C${value}` }),
}))

describe('TeamMemberUsageCharts', () => {
  beforeEach(() => {
    lineProps.mockClear()
    doughnutProps.mockClear()
  })

  // Two member summaries cover different sets of daily dates. The line chart must insert
  // zero for a missing date, while the comparison chart retains each member's total
  // spending and uses matching member colors and proportional tooltip percentages.
  it('aligns member daily costs and builds the spending share chart', () => {
    mount(TeamMemberUsageCharts, {
      props: {
        series: [
          {
            userID: 1,
            label: 'Owner',
            summary: {
              actual_cost: 1.5,
              request_count: 2,
              input_tokens: 10,
              output_tokens: 5,
              daily: [
                { date: '2026-07-26', actual_cost: 1, request_count: 1 },
                { date: '2026-07-27', actual_cost: 0.5, request_count: 1 },
              ],
            },
          },
          {
            userID: 2,
            label: 'Member',
            summary: {
              actual_cost: 0.25,
              request_count: 1,
              input_tokens: 3,
              output_tokens: 2,
              daily: [{ date: '2026-07-27', actual_cost: 0.25, request_count: 1 }],
            },
          },
        ],
      },
      global: { stubs: { LoadingSpinner: true } },
    })

    const lineData = lineProps.mock.calls.at(-1)?.[0].data
    const comparisonData = doughnutProps.mock.calls.at(-1)?.[0].data
    expect(lineData.labels).toEqual(['2026-07-26', '2026-07-27'])
    expect(lineData.datasets[1].data).toEqual([0, 0.25])
    expect(comparisonData.labels).toEqual(['Owner', 'Member'])
    expect(comparisonData.datasets[0].data).toEqual([1.5, 0.25])
    expect(comparisonData.datasets[0].backgroundColor).toEqual(
      lineData.datasets.map((dataset: { borderColor: string }) => dataset.borderColor),
    )
    const comparisonOptions = doughnutProps.mock.calls.at(-1)?.[0].options
    expect(comparisonOptions.plugins.tooltip.callbacks.label({ label: 'Owner', raw: 1.5 })).toBe(
      'Owner: C1.5 (85.7%)',
    )
  })
})
