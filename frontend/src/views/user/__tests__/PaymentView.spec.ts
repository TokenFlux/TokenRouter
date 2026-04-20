import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createRouter, createMemoryHistory } from 'vue-router'
import PaymentView from '../PaymentView.vue'

const mockGetCheckoutInfo = vi.fn()
const mockGetActiveSubscriptions = vi.fn()

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo: (...args: unknown[]) => mockGetCheckoutInfo(...args)
  }
}))

vi.mock('@/api/subscriptions', () => ({
  default: {
    getActiveSubscriptions: (...args: unknown[]) => mockGetActiveSubscriptions(...args)
  }
}))

function createTestI18n() {
  return createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        common: {
          processing: 'Processing',
          cancel: 'Cancel'
        },
        payment: {
          tabTopUp: 'Recharge',
          tabSubscribe: 'Subscribe',
          createOrder: 'Create Order',
          amountLabel: 'Amount',
          fee: 'Fee',
          actualPay: 'Actual Pay',
          noPlans: 'No plans',
          activeSubscription: 'Active subscription',
          rechargeAccount: 'Recharge account',
          currentBalance: 'Current balance',
          days: 'days',
          perMonth: 'month',
          perYear: 'year',
          planCard: {
            dailyLimit: 'Daily limit',
            weeklyLimit: 'Weekly limit',
            monthlyLimit: 'Monthly limit',
            quota: 'Quota',
            unlimited: 'Unlimited'
          },
          result: {
            failed: 'Failed'
          },
          errors: {
            tooManyPending: 'Too many pending orders',
            cancelRateLimited: 'Cancel rate limited'
          },
          notAvailable: 'Not available',
          amountNoMethod: 'No method',
          amountTooLow: 'Too low {min}',
          amountTooHigh: 'Too high {max}',
          rechargeRatePreview: 'Rate {amount} {unitName}'
        },
        userSubscriptions: {
          daysRemaining: '{days} days remaining',
          status: {
            active: 'Active'
          }
        }
      }
    }
  })
}

async function mountView() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const i18n = createTestI18n()
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/purchase', component: PaymentView }]
  })
  await router.push('/purchase?tab=subscription&plan=2')
  await router.isReady()

  return mount(PaymentView, {
    global: {
      plugins: [pinia, i18n, router],
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        AmountInput: { template: '<div />' },
        PaymentMethodSelector: { template: '<div />' },
        SubscriptionPlanCard: { template: '<div />' },
        PaymentStatusPanel: { template: '<div />' },
        StripePaymentInline: { template: '<div />' },
        Icon: { template: '<span />' }
      }
    }
  })
}

describe('PaymentView', () => {
  beforeEach(() => {
    mockGetCheckoutInfo.mockReset()
    mockGetActiveSubscriptions.mockReset()
    mockGetActiveSubscriptions.mockResolvedValue([])
  })

  it('preselects the plan from route query on subscription tab', async () => {
    mockGetCheckoutInfo.mockResolvedValue({
      data: {
        methods: {
          alipay: {
            daily_limit: 1000,
            daily_used: 0,
            daily_remaining: 1000,
            single_min: 1,
            single_max: 1000,
            fee_rate: 0,
            available: true
          }
        },
        global_min: 1,
        global_max: 1000,
        balance_disabled: false,
        balance_recharge_multiplier: 1,
        recharge_fee_rate: 0,
        help_text: '',
        help_image_url: '',
        stripe_publishable_key: '',
        plans: [
          {
            id: 1,
            name: 'Plan Basic',
            description: 'Basic description',
            price: 10,
            original_price: null,
            validity_days: 30,
            validity_unit: 'day',
            daily_limit_usd: 5,
            weekly_limit_usd: null,
            monthly_limit_usd: null,
            features: [],
            product_name: '',
            for_sale: true,
            sort_order: 0
          },
          {
            id: 2,
            name: 'Plan Plus',
            description: 'Plus description',
            price: 20,
            original_price: null,
            validity_days: 30,
            validity_unit: 'day',
            daily_limit_usd: 12.5,
            weekly_limit_usd: null,
            monthly_limit_usd: null,
            features: [],
            product_name: '',
            for_sale: true,
            sort_order: 0
          }
        ]
      }
    })

    const wrapper = await mountView()
    await flushPromises()

    const text = wrapper.text()
    expect(mockGetCheckoutInfo).toHaveBeenCalledTimes(1)
    expect(text).toContain('Plan Plus')
    expect(text).toContain('$12.50')
    expect(text).not.toContain('Plan Basic')
  })
})
