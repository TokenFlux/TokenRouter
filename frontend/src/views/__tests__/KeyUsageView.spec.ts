import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import KeyUsageView from '../KeyUsageView.vue'
import { useAppStore } from '@/stores'

const mockFetch = vi.fn()

function createTestI18n() {
  return createI18n({
    legacy: false,
    locale: 'en',
    missingWarn: false,
    fallbackWarn: false,
    messages: {
      en: {
        keyUsage: {
          title: 'API Key Usage',
          subtitle: 'Usage status',
          placeholder: 'API key',
          query: 'Query',
          querying: 'Querying...',
          privacyNote: 'Privacy note',
          dateRange: 'Date Range:',
          dateRange15m: '15m',
          dateRange30m: '30m',
          dateRangeToday: 'Today',
          dateRange7d: '7 Days',
          dateRange30d: '30 Days',
          dateRange90d: '90 Days',
          dateRangeCustom: 'Custom',
          apply: 'Apply',
          used: 'Used',
          detailInfo: 'Detail Information',
          tokenStats: 'Token Statistics',
          dailyDetail: 'Daily Detail',
          date: 'Date',
          requests: 'Requests',
          inputTokens: 'Input Tokens',
          outputTokens: 'Output Tokens',
          cacheReadTokens: 'Cache Read',
          cacheWriteTokens: 'Cache Write',
          cost: 'Cost',
          quotaMode: 'Key Quota Mode',
          walletBalance: 'Wallet Balance',
          totalQuota: 'Total Quota',
          limit5h: '5-Hour Limit',
          limitDaily: 'Daily Limit',
          limit7d: '7-Day Limit',
          limitWeekly: 'Weekly Limit',
          limitMonthly: 'Monthly Limit',
          remainingQuota: 'Remaining Quota',
          usedQuota: 'Used Quota',
          subscriptionType: 'Subscription Type',
          subscriptionExpires: 'Subscription Expires',
          todayRequests: 'Today Requests',
          todayInputTokens: 'Today Input',
          todayOutputTokens: 'Today Output',
          todayTokens: 'Today Tokens',
          todayCacheCreation: 'Today Cache Creation',
          todayCacheRead: 'Today Cache Read',
          todayCost: 'Today Cost',
          rpmTpm: 'RPM / TPM',
          totalRequests: 'Total Requests',
          totalInputTokens: 'Total Input',
          totalOutputTokens: 'Total Output',
          totalTokensLabel: 'Total Tokens',
          totalCacheCreation: 'Total Cache Creation',
          totalCacheRead: 'Total Cache Read',
          totalCost: 'Total Cost',
          avgDuration: 'Avg Duration',
          querySuccess: 'Query successful',
          queryFailed: 'Query failed',
          queryFailedRetry: 'Query failed, please try again later',
          noDailyUsage: 'No daily usage data'
        },
        home: {
          viewDocs: 'Docs',
          switchToLight: 'Light',
          switchToDark: 'Dark',
          footer: {
            allRightsReserved: 'All rights reserved.'
          }
        },
        payment: {
          planCard: {
            unlimited: 'Unlimited'
          }
        }
      }
    }
  })
}

async function mountView() {
  const pinia = createPinia()
  setActivePinia(pinia)

  const appStore = useAppStore()
  appStore.publicSettingsLoaded = true
  appStore.showSuccess = vi.fn()
  appStore.showError = vi.fn()
  appStore.showInfo = vi.fn()

  const i18n = createTestI18n()

  return mount(KeyUsageView, {
    global: {
      plugins: [pinia, i18n],
      stubs: {
        LocaleSwitcher: { template: '<div />' },
        BalanceIcon: { template: '<span />' },
        Icon: { template: '<span />' },
        'router-link': { template: '<a><slot /></a>' }
      }
    }
  })
}

describe('KeyUsageView', () => {
  beforeEach(() => {
    mockFetch.mockReset()
    vi.stubGlobal('fetch', mockFetch)
    let perfNow = 0
    vi.spyOn(performance, 'now').mockImplementation(() => {
      perfNow += 1000
      return perfNow
    })
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      callback(perfNow)
      return 0
    })
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockReturnValue({
        matches: false,
        media: '',
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn()
      })
    })
  })

  afterEach(async () => {
    await new Promise(resolve => setTimeout(resolve, 60))
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('renders unlimited remaining quota for subscription responses with -1 sentinel', async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => ({
        mode: 'unrestricted',
        isValid: true,
        planName: 'Legacy Plan',
        remaining: -1,
        subscription: {
          daily_usage_usd: 0,
          weekly_usage_usd: 0,
          monthly_usage_usd: 0,
          daily_limit_usd: 0,
          weekly_limit_usd: 0,
          monthly_limit_usd: 0,
          expires_at: '2026-05-01T00:00:00Z'
        }
      })
    })

    const wrapper = await mountView()
    await wrapper.find('input').setValue('sk-test')
    await wrapper.find('input').trigger('keydown.enter')
    await flushPromises()

    expect(mockFetch).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('keyUsage.remainingQuota')
    const unlimitedNode = wrapper.findAll('span').find((node) => node.text() === 'payment.planCard.unlimited')
    expect(unlimitedNode).toBeTruthy()
    expect(unlimitedNode?.classes()).toContain('text-emerald-500')

    wrapper.unmount()
  })

  it('renders daily usage detail rows after a successful query', async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => ({
        mode: 'quota_limited',
        isValid: true,
        status: 'active',
        quota: {
          limit: 10,
          used: 1,
          remaining: 9,
          unit: 'USD'
        },
        usage: {
          today: {
            requests: 1,
            input_tokens: 10,
            output_tokens: 20,
            cache_creation_tokens: 0,
            cache_read_tokens: 0,
            total_tokens: 30,
            actual_cost: 0.01
          },
          total: {
            requests: 12,
            input_tokens: 100,
            output_tokens: 200,
            cache_creation_tokens: 10,
            cache_read_tokens: 30,
            total_tokens: 340,
            actual_cost: 0.12
          },
          rpm: 0,
          tpm: 0
        },
        daily_usage: [
          {
            date: '2026-05-19',
            requests: 12,
            input_tokens: 100,
            output_tokens: 200,
            cache_read_tokens: 30,
            cache_write_tokens: 10,
            total_tokens: 340,
            cost: 0.15,
            actual_cost: 0.12
          }
        ]
      })
    })

    const wrapper = await mountView()
    await wrapper.find('input').setValue('sk-test-key')
    await wrapper.find('input').trigger('keydown.enter')
    await flushPromises()
    await nextTick()

    expect(mockFetch).toHaveBeenCalledWith(
      expect.stringContaining('/v1/usage?'),
      expect.objectContaining({
        headers: { Authorization: 'Bearer sk-test-key' }
      })
    )
    expect(String(mockFetch.mock.calls[0][0])).toContain('days=30')

    const text = wrapper.text()
    expect(text).toContain('keyUsage.dailyDetail')
    expect(text).toContain('keyUsage.date')
    expect(text).toContain('keyUsage.cacheReadTokens')
    expect(text).toContain('keyUsage.cacheWriteTokens')
    expect(text).toContain('2026-05-19')
    expect(text).toContain('12')
    expect(text).toContain('100')
    expect(text).toContain('200')
    expect(text).toContain('30')
    expect(text).toContain('10')
    expect(text).toContain('0.12')

    wrapper.unmount()
  })
})
