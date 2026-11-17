import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import UsageStatsCards from '../UsageStatsCards.vue'

// UsageStatsCards.spec.ts tests cache totals and hidden-tooltip layout behavior on
// summary cards. Fixed usage totals and translation substitutes keep assertions focused
// on rendered counts and visibility classes rather than network retrieval.

// messages supplies cache-total and cache-breakdown labels used to locate the actual
// summary counts and tooltip content.
const messages: Record<string, string> = {
  'usage.totalRequests': 'Total Requests',
  'usage.inSelectedRange': 'in selected range',
  'usage.totalTokens': 'Total Tokens',
  'usage.in': 'In',
  'usage.out': 'Out',
  'usage.cacheTotal': 'Cache',
  'usage.cacheBreakdown': 'Cache Token Breakdown',
  'usage.cacheCreationTokensLabel': 'Cache Creation',
  'usage.cacheReadTokensLabel': 'Cache Read',
  'usage.totalCost': 'Total Cost',
  'usage.providerCost': 'Cost',
  'usage.standardCost': 'Standard',
  'usage.avgDuration': 'Avg Duration',
  'usage.incompleteCosts': '{count} requests have incomplete costs',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: { count: number }) =>
        (messages[key] ?? key).replace('{count}', String(params?.count ?? '')),
    }),
  }
})

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    formatBalanceAmount: (value: number) => `$${value.toFixed(4)}`,
    formatUsdAmount: (value: number) => `$${value.toFixed(4)}`,
  }),
}))

// stats supplies representative usage measurements reused by rendering cases, with each
// test overriding the fields relevant to its scenario.
const stats = {
  total_requests: 1,
  total_input_tokens: 100,
  total_output_tokens: 50,
  total_cache_tokens: 34,
  total_cache_creation_tokens: 12,
  total_cache_read_tokens: 22,
  total_tokens: 184,
  total_cost: 0.001,
  total_actual_cost: 0.001,
  total_provider_cost: 0.001,
  average_duration_ms: 250,
}

describe('UsageStatsCards', () => {
  // The first summary request can fail before any metrics are available. Pending
  // and failed states never show business zeros, while a successful response can.
  it('distinguishes a pending or failed summary from a measured zero', async () => {
    const wrapper = mount(UsageStatsCards, {
      props: { stats: null, loading: true },
      global: { stubs: { BalanceIcon: true, Icon: true } },
    })

    expect(wrapper.get('[aria-busy="true"]').find('.skeleton').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('$0.0000')

    await wrapper.setProps({ loading: false })
    expect(wrapper.get('[role="status"]').text()).toBe('usage.summaryUnavailable')

    await wrapper.setProps({ stats: { ...stats, total_requests: 0, total_actual_cost: 0 } })
    expect(wrapper.text()).toContain('$0.0000')
    expect(wrapper.find('[aria-busy="true"]').exists()).toBe(false)
    wrapper.unmount()
  })

  // Some system requests have unknown or partial costs alongside a known total.
  // The count must be visible with provider expenses and disappear when those expenses are hidden.
  it('marks an incomplete provider cost total', async () => {
    const wrapper = mount(UsageStatsCards, {
      props: { stats: { ...stats, incomplete_cost_requests: 2 } },
      global: { stubs: { BalanceIcon: true, Icon: true } },
    })
    expect(wrapper.text()).toContain('2 requests have incomplete costs')
    await wrapper.setProps({ showProviderCost: false })
    expect(wrapper.text()).not.toContain('requests have incomplete costs')
    await wrapper.setProps({ showProviderCost: true, stats })
    expect(wrapper.text()).not.toContain('requests have incomplete costs')
  })

  // A summary contains separate cache creation and read counts. The test protects the
  // combined cache total and the individual counts displayed in its details.
  it('shows cache token breakdown values', () => {
    const wrapper = mount(UsageStatsCards, {
      props: {
        stats,
      },
      global: {
        stubs: {
          BalanceIcon: true,
          Icon: true,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('Cache: 34')
    expect(text).toContain('Cache Token Breakdown')
    expect(text).toContain('Cache Creation')
    expect(text).toContain('12')
    expect(text).toContain('Cache Read')
    expect(text).toContain('22')
  })

  // The cache tooltip begins hidden before any hover interaction occurs. The test protects
  // layout removal through the hidden class rather than an opacity-only presentation.
  it('keeps the cache tooltip out of the layout while it is hidden', () => {
    const wrapper = mount(UsageStatsCards, {
      props: {
        stats,
      },
      global: {
        stubs: {
          BalanceIcon: true,
          Icon: true,
        },
      },
    })

    const tooltip = wrapper.findAll('span').find((el) => el.classes().includes('group-hover:block'))

    expect(tooltip).toBeDefined()
    // Opacity alone leaves the fixed-width tooltip in layout; hidden removes it so
    // narrow cards do not inherit that width.
    expect(tooltip?.classes()).toContain('hidden')
    expect(tooltip?.classes()).not.toContain('opacity-0')
  })
})
