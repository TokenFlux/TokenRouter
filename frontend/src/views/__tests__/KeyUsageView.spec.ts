import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
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
          placeholder: 'API key',
          detailInfo: 'Detail Information',
          remainingQuota: 'Remaining Quota',
          subscriptionType: 'Subscription Type',
          subscriptionExpires: 'Subscription Expires',
          querySuccess: 'Query successful',
          queryFailed: 'Query failed',
          queryFailedRetry: 'Query failed, please try again later'
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
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      callback(0)
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

  afterEach(() => {
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
})
