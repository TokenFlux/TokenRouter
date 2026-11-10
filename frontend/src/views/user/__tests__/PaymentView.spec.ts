import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import PaymentView from '../PaymentView.vue'
import { PAYMENT_RECOVERY_STORAGE_KEY } from '@/utils/payment/paymentFlow'
import { formatPaymentAmount } from '@/utils/payment/currency'
import AmountInput from '@/components/payment/AmountInput.vue'
import SubscriptionPlanCard from '@/components/payment/SubscriptionPlanCard.vue'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
import type { CheckoutInfoResponse, MethodLimit, SubscriptionPlan } from '@/api/payment.types'

/**
 * PaymentView.spec.ts checks purchase selection, fee previews, authorization resume, and order
 * launch through the mounted checkout page. API, store, bridge, and router doubles keep
 * billing and browser navigation isolated while exercising the page-private controllers.
 */

// The transition stub retains payment content for component assertions; browser transition
// behavior is verified separately against the rendered application.
const motionTransitionStub = { inheritAttrs: false, template: '<slot />' }

/**
 * createMemoryStorage returns isolated string storage for purchase recovery tests. Each
 * instance owns its record and exposes spyable browser Storage operations, allowing tests
 * to verify persistence and cleanup ordering.
 */
function createMemoryStorage(): Storage {
  let store: Record<string, string> = {}
  return {
    /** length returns the number of keys currently stored by this fixture. */
    get length() {
      return Object.keys(store).length
    },
    clear: vi.fn(() => {
      store = {}
    }),
    getItem: vi.fn((key: string) => store[key] ?? null),
    key: vi.fn((index: number) => Object.keys(store)[index] ?? null),
    removeItem: vi.fn((key: string) => {
      delete store[key]
    }),
    setItem: vi.fn((key: string, value: string) => {
      store[key] = String(value)
    }),
  }
}

// Some test environments omit browser localStorage, so these recovery tests install isolated
// string storage before mounting checkout and inspecting persistence.
if (!window.localStorage) {
  const storage = createMemoryStorage()
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: storage,
  })
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: storage,
  })
}

// Hoisted route, account, gateway, and feedback doubles are reset per scenario so mounted checkout and its controllers share one deterministic test context.
const routeState = vi.hoisted(() => ({
  path: '/purchase',
  query: {} as Record<string, unknown>,
}))

const routerReplace = vi.hoisted(() => vi.fn())
const routerPush = vi.hoisted(() => vi.fn())
const routerResolve = vi.hoisted(() => vi.fn(() => ({ href: '/payment/stripe?mock=1' })))
const createOrder = vi.hoisted(() => vi.fn())
const refreshUser = vi.hoisted(() => vi.fn())
const authUserState = vi.hoisted(() => ({
  value: {
    username: 'demo-user',
    email: 'buyer@example.com',
    balance: 0,
  },
}))
const fetchActiveSubscriptions = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))
const activeSubscriptionsState = vi.hoisted(() => ({ value: [] as Array<Record<string, unknown>> }))
const showError = vi.hoisted(() => vi.fn())
const showInfo = vi.hoisted(() => vi.fn())
const showWarning = vi.hoisted(() => vi.fn())
const getCheckoutInfo = vi.hoisted(() => vi.fn())
const bridgeInvoke = vi.hoisted(() => vi.fn())
const translate = vi.hoisted(() => vi.fn((key: string) => key))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({
      replace: routerReplace,
      push: routerPush,
      resolve: routerResolve,
    }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: translate,
    }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: authUserState.value,
    refreshUser,
  }),
}))

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    createOrder,
  }),
}))

vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({
    activeSubscriptions: activeSubscriptionsState.value,
    fetchActiveSubscriptions,
  }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
    showInfo,
    showWarning,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo,
  },
}))

vi.mock('@/utils/device', () => ({
  isMobileDevice: () => true,
}))

/**
 * checkoutInfoFixture creates checkout configuration with an available WeChat method and
 * supplied overrides. Each call returns fresh editable data so method, plan, and settings
 * scenarios remain isolated between mounted tests.
 */
function checkoutInfoFixture(overrides: Partial<CheckoutInfoResponse> = {}) {
  return {
    data: {
      methods: {
        wxpay: {
          daily_limit: 0,
          daily_used: 0,
          daily_remaining: 0,
          single_min: 0,
          single_max: 0,
          fee_fixed: 0,
          fee_rate: 0,
          available: true,
        },
      },
      global_min: 0,
      global_max: 0,
      plans: [],
      balance_disabled: false,
      balance_recharge_multiplier: 1,
      subscription_usd_to_cny_rate: 0,
      recharge_fee_rate: 0,
      method_fees: {},
      help_text: '',
      help_image_url: '',
      stripe_publishable_key: '',
      ...overrides,
    },
  }
}

/**
 * checkoutInfoWithPlansFixture adds one subscription plan to the checkout fixture.
 * Optional checkout, method, and plan overrides target each layer independently while
 * retaining the base WeChat method used for selection.
 */
function checkoutInfoWithPlansFixture(
  options: {
    checkout?: Partial<CheckoutInfoResponse> // checkout configuration field overrides

    method?: Partial<MethodLimit> // checkout method field overrides

    plan?: Partial<SubscriptionPlan> // subscription plan field overrides
  } = {},
) {
  const base = checkoutInfoFixture(options.checkout).data
  const plan: SubscriptionPlan = {
    id: 7,
    group_ids: [3],
    name: 'Starter',
    description: '',
    price: 128,
    original_price: 0,
    validity_days: 30,
    validity_unit: 'day',
    rate_multiplier: 1,
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    features: [],
    group_platform: 'openai',
    sort_order: 1,
    for_sale: true,
    group_name: 'OpenAI',
    ...options.plan,
  }

  return {
    data: {
      ...base,
      methods: {
        ...base.methods,
        wxpay: {
          ...base.methods.wxpay,
          ...options.method,
        },
      },
      plans: [plan],
    },
  }
}

/**
 * stripeCheckoutInfoFixture builds checkout data with an enabled top-level Stripe method.
 */
function stripeCheckoutInfoFixture() {
  return {
    data: {
      ...checkoutInfoFixture().data,
      methods: {
        stripe: {
          daily_limit: 0,
          daily_used: 0,
          daily_remaining: 0,
          single_min: 0,
          single_max: 0,
          fee_fixed: 0,
          fee_rate: 0,
          available: true,
        },
      },
      stripe_publishable_key: 'pk_test_123',
    },
  }
}

/**
 * stripeCheckoutInfoWithFeeFixture adds fixed and percentage fees to Stripe checkout data.
 */
function stripeCheckoutInfoWithFeeFixture() {
  return {
    data: {
      ...stripeCheckoutInfoFixture().data,
      methods: {
        stripe: {
          daily_limit: 0,
          daily_used: 0,
          daily_remaining: 0,
          single_min: 0,
          single_max: 0,
          fee_fixed: 2.5,
          fee_rate: 2.2,
          available: true,
        },
      },
    },
  }
}

/**
 * stripeCheckoutInfoWithCurrencyFeeFixture sets the currency and fee precision inputs for
 * checkout. Tests choose currency minor units while retaining the shared available Stripe
 * method and billing form fixture.
 */
function stripeCheckoutInfoWithCurrencyFeeFixture(
  currency: string,
  fixedFee: number,
  feeRate: number,
) {
  return {
    data: {
      ...stripeCheckoutInfoFixture().data,
      methods: {
        stripe: {
          daily_limit: 0,
          daily_used: 0,
          daily_remaining: 0,
          single_min: 0,
          single_max: 0,
          fee_fixed: fixedFee,
          fee_rate: feeRate,
          currency,
          available: true,
        },
      },
    },
  }
}

/**
 * jsapiOrderFixture creates a WeChat bridge-ready order reply using the supplied resume
 * token.
 */
function jsapiOrderFixture(resumeToken: string) {
  return {
    order_id: 123,
    amount: 88,
    pay_amount: 88,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    payment_type: 'wxpay',
    out_trade_no: 'sub2_jsapi_123',
    result_type: 'jsapi_ready' as const,
    resume_token: resumeToken,
    jsapi: {
      appId: 'wx123',
      timeStamp: '1712345678',
      nonceStr: 'nonce',
      package: 'prepay_id=wx123',
      signType: 'RSA',
      paySign: 'signed',
    },
  }
}

/**
 * oauthOrderFixture creates a WeChat authorization reply with its purchase return
 * destination.
 */
function oauthOrderFixture() {
  return {
    order_id: 456,
    amount: 128,
    pay_amount: 128,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    payment_type: 'wxpay',
    result_type: 'oauth_required' as const,
    oauth: {
      authorize_url:
        '/api/v1/auth/oauth/wechat/payment/start?payment_type=wxpay&redirect=%2Fpurchase%3Ffrom%3Dwechat',
      appid: 'wx123',
      scope: 'snsapi_base',
      redirect_url: '/auth/wechat/payment/callback',
    },
  }
}

/**
 * mountSubscriptionConfirm mounts checkout with subscription route context and supplied
 * configuration. It waits for initial promises before returning the wrapper so assertions
 * observe the selected plan confirmation state.
 */
async function mountSubscriptionConfirm(
  options: Parameters<typeof checkoutInfoWithPlansFixture>[0] = {},
  query: Record<string, unknown> = { tab: 'subscription', group: '3' },
) {
  vi.useRealTimers()
  routeState.path = '/purchase'
  routeState.query = query
  routerReplace.mockReset().mockResolvedValue(undefined)
  routerPush.mockReset().mockResolvedValue(undefined)
  routerResolve.mockClear()
  createOrder.mockReset()
  refreshUser.mockReset()
  fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
  showError.mockReset()
  showInfo.mockReset()
  showWarning.mockReset()
  getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoWithPlansFixture(options))
  bridgeInvoke.mockReset()
  window.localStorage.clear()
  ;(
    window as Window & {
      WeixinJSBridge?: {
        invoke: typeof bridgeInvoke // mock WeChat payment invocation
      } // optional browser WeChat bridge
    }
  ).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: {
        Collapse: false,
        RetainedContent: false,
        MotionTransition: motionTransitionStub,
        AppLayout: {
          template: '<div><slot /></div>',
        },
        Teleport: true,
        Transition: false,
      },
    },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

/**
 * mountSubscriptionPlanList mounts the requested number of plans with subscription
 * browsing active. It waits for checkout loading so responsive-layout assertions inspect
 * the rendered plan collection rather than loading placeholders.
 */
async function mountSubscriptionPlanList(planCount: number) {
  vi.useRealTimers()
  routeState.path = '/purchase'
  routeState.query = { tab: 'subscription' }
  routerReplace.mockReset().mockResolvedValue(undefined)
  routerPush.mockReset().mockResolvedValue(undefined)
  routerResolve.mockClear()
  createOrder.mockReset()
  refreshUser.mockReset()
  authUserState.value = {
    username: 'demo-user',
    email: 'buyer@example.com',
    balance: 0,
  }
  activeSubscriptionsState.value = []
  fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
  showError.mockReset()
  showInfo.mockReset()
  showWarning.mockReset()
  const basePlan = checkoutInfoWithPlansFixture().data.plans[0]
  const plans = Array.from({ length: planCount }, (_, index) => ({
    ...basePlan,
    id: index + 1,
    name: `Plan ${index + 1}`,
  }))
  getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ plans }))
  bridgeInvoke.mockReset()
  window.localStorage.clear()
  ;(
    window as Window & {
      WeixinJSBridge?: {
        invoke: typeof bridgeInvoke // mock WeChat payment invocation
      } // optional browser WeChat bridge
    }
  ).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: {
        MotionTransition: motionTransitionStub,
        AppLayout: {
          template: '<div><slot /></div>',
        },
        Teleport: true,
        Transition: false,
      },
    },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('PaymentView 横向切换', () => {
  // Checkout fields are edited before moving between recharge and subscription tabs. Input
  // within a tab must keep its panel identity, and returning must retain amount and billing
  // drafts.
  it('同一页签输入不重建面板，往返切换保留已填金额和账单信息', async () => {
    const wrapper = await mountSubscriptionPlanList(3)
    const view = wrapper.vm as unknown as {
      activeTab: 'recharge' | 'subscription' // selected purchase page tab

      amount: number | null // selected purchase amount

      billingInfo: {
        name: string // entered billing name text
      } // editable Stripe billing fields
    }
    view.activeTab = 'recharge'
    await flushPromises()
    const panel = wrapper.get('.purchase-viewport > div').element
    view.amount = 123
    view.billingInfo.name = 'Kai'
    await flushPromises()
    expect(wrapper.get('.purchase-viewport > div').element).toBe(panel)

    view.activeTab = 'subscription'
    await flushPromises()
    expect(wrapper.get('.purchase-viewport').attributes('style')).toContain(
      '--purchase-direction: 1',
    )
    view.activeTab = 'recharge'
    await flushPromises()
    expect(wrapper.get('.purchase-viewport').attributes('style')).toContain(
      '--purchase-direction: -1',
    )
    expect(view.amount).toBe(123)
    expect(view.billingInfo.name).toBe('Kai')
    expect(createOrder).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

describe('PaymentView subscription plan group matching', () => {
  // A subscription route names a group listed in a plan's group_ids. Initial checkout selection
  // must choose that plan and expose its subscription confirmation controls.
  it('selects a plan when the route group is included in group_ids', async () => {
    const wrapper = await mountSubscriptionConfirm({
      plan: {
        group_ids: [2, 3],
      },
    })

    expect(
      (
        wrapper.vm as unknown as {
          selectedPlan: SubscriptionPlan | null // selected subscription plan fixture
        }
      ).selectedPlan?.id,
    ).toBe(7)
  })

  // A subscription plan has an empty modern group list. A valid route group must treat the plan
  // as globally available and select it for checkout.
  it('treats an empty group_ids list as available to every group', async () => {
    const wrapper = await mountSubscriptionConfirm({
      plan: {
        group_ids: [],
      },
    })

    expect(
      (
        wrapper.vm as unknown as {
          selectedPlan: SubscriptionPlan | null // selected subscription plan fixture
        }
      ).selectedPlan?.id,
    ).toBe(7)
  })

  // A route contains an invalid group identifier while a global plan exists. Initialization must
  // leave plan selection open rather than treating invalid context as a valid renewal group.
  it('does not select a global plan for an invalid route group', async () => {
    const wrapper = await mountSubscriptionConfirm(
      {
        plan: {
          group_ids: [],
        },
      },
      { tab: 'subscription', group: 'invalid' },
    )

    expect(
      (
        wrapper.vm as unknown as {
          selectedPlan: SubscriptionPlan | null // selected subscription plan fixture
        }
      ).selectedPlan,
    ).toBeNull()
  })
})

describe('PaymentView subscription plan grid', () => {
  // The subscription catalog contains several different plan counts. Its grid must retain
  // mobile, tablet, and desktop classes so added plans do not change the responsive layout rule.
  it.each([3, 4, 6])('为 %i 个套餐保留移动端、平板和桌面端响应式网格', async (planCount) => {
    const wrapper = await mountSubscriptionPlanList(planCount)
    const cards = wrapper.findAllComponents(SubscriptionPlanCard)

    expect(cards).toHaveLength(planCount)
    expect([...(cards[0].element.parentElement?.classList ?? [])]).toEqual(
      expect.arrayContaining(['grid', 'grid-cols-1', 'sm:grid-cols-2', 'xl:grid-cols-3']),
    )
  })
})

describe('PaymentView recharge rate preview', () => {
  // Checkout selects a method with its own currency under different locale representations.
  // Amount labels and limit messages must consistently use that selected payment currency.
  it('uses the selected payment method currency in both locale templates', async () => {
    translate.mockClear()
    routeState.path = '/purchase'
    routeState.query = {}
    getCheckoutInfo.mockReset().mockResolvedValue(
      checkoutInfoFixture({
        balance_recharge_multiplier: 0.5,
        methods: {
          stripe: {
            ...checkoutInfoFixture().data.methods.wxpay,
            currency: 'USD',
          },
        },
      }),
    )

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    wrapper.getComponent(AmountInput).vm.$emit('update:modelValue', 10)
    await flushPromises()

    expect(translate).toHaveBeenCalledWith('payment.rechargeRatePreview', {
      currency: 'USD',
      amount: '0.50',
      unitName: 'USD',
    })
    expect(en.payment.rechargeRatePreview).toBe('Current rate: 1 {currency} = {amount} {unitName}')
    expect(zh.payment.rechargeRatePreview).toBe('当前倍率：1 {currency} = {amount} {unitName}')
  })
})

describe('PaymentView subscription confirmation amounts', () => {
  // Subscription checkout has distinct balance and subscription conversion multipliers. The
  // payment preview must use the configured subscription USD-to-CNY rate for the plan charge.
  it('shows converted CNY pay amount using the subscription rate, not the balance multiplier', async () => {
    const wrapper = await mountSubscriptionConfirm({
      checkout: {
        balance_recharge_multiplier: 0.14,
        subscription_usd_to_cny_rate: 7.15,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 9.99,
        original_price: 12.99,
      },
    })

    const text = wrapper.text()
    const convertedPrice = formatPaymentAmount(71.43, 'CNY')
    const convertedOriginalPrice = formatPaymentAmount(92.88, 'CNY')

    expect(text).toContain(convertedPrice)
    expect(text).toContain(convertedOriginalPrice)
    expect(text).not.toContain(formatPaymentAmount(9.99, 'CNY'))
    // The configured subscription rate converts each USD of plan price into 7.15 CNY before
    // checkout computes gateway fees.
    expect(text).not.toContain(formatPaymentAmount(71.36, 'CNY'))
    expect(wrapper.findAll('button').some((button) => button.text().includes(convertedPrice))).toBe(
      true,
    )
  })

  // The subscription conversion rate is disabled or the selected gateway uses another currency.
  // Checkout must retain the stored plan price before applying currency precision.
  it('keeps plan price when the subscription rate is not configured or payment currency is not CNY', async () => {
    // A balance multiplier alone does not enable subscription conversion; CNY subscriptions
    // keep their plan price when the dedicated subscription rate is absent.
    const cnyWrapper = await mountSubscriptionConfirm({
      checkout: {
        balance_recharge_multiplier: 0.14,
        subscription_usd_to_cny_rate: 0,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 7.99,
      },
    })

    expect(cnyWrapper.text()).toContain(formatPaymentAmount(7.99, 'CNY'))
    expect(cnyWrapper.text()).not.toContain(formatPaymentAmount(57.07, 'CNY'))
    expect(cnyWrapper.text()).not.toContain(formatPaymentAmount(57.13, 'CNY'))

    const usdWrapper = await mountSubscriptionConfirm({
      checkout: {
        subscription_usd_to_cny_rate: 7.15,
      },
      method: {
        currency: 'USD',
      },
      plan: {
        price: 7.99,
        original_price: 9.99,
      },
    })

    expect(usdWrapper.text()).toContain(formatPaymentAmount(7.99, 'USD'))
    expect(usdWrapper.text()).toContain(formatPaymentAmount(9.99, 'USD'))
  })

  // A CNY subscription applies both conversion and a percentage gateway fee. The preview must
  // convert the plan price before calculating the fee and final payable amount.
  it('adds fee rate after CNY rate conversion to match backend pay_amount', async () => {
    const wrapper = await mountSubscriptionConfirm({
      checkout: {
        subscription_usd_to_cny_rate: 7.15,
        recharge_fee_rate: 2.5,
      },
      method: {
        currency: 'CNY',
        fee_rate: 2.5,
      },
      plan: {
        price: 9.99,
      },
    })

    const text = wrapper.text()
    const convertedPrice = formatPaymentAmount(71.43, 'CNY')
    const fee = formatPaymentAmount(1.79, 'CNY')
    const total = formatPaymentAmount(73.22, 'CNY')

    expect(text).toContain(convertedPrice)
    expect(text).toContain(fee)
    expect(text).toContain(total)
    expect(wrapper.findAll('button').some((button) => button.text().includes(total))).toBe(true)
  })
})

describe('PaymentView payment recovery', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {}
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    bridgeInvoke.mockReset()
    window.localStorage.clear()
    ;(
      window as Window & {
        WeixinJSBridge?: {
          invoke: typeof bridgeInvoke // mock WeChat payment invocation
        } // optional browser WeChat bridge
      }
    ).WeixinJSBridge = undefined
  })

  // Local recovery belongs to an administrator-defined EasyPay method. After dismissing its
  // waiting panel, checkout must retain that custom method as the selected payment option.
  it('restores a custom EasyPay method as the selected payment method', async () => {
    getCheckoutInfo.mockResolvedValue(
      checkoutInfoFixture({
        methods: {
          wxpay: checkoutInfoFixture().data.methods.wxpay,
          ldc: {
            daily_limit: 0,
            daily_used: 0,
            daily_remaining: 0,
            single_min: 0,
            single_max: 0,
            fee_rate: 0,
            available: true,
            display_name: 'LDC Pay',
          },
        },
      }),
    )
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify({
        orderId: 888,
        amount: 66,
        qrCode: 'ldc-qr',
        expiresAt: '2099-01-01T00:10:00.000Z',
        paymentType: 'ldc',
        payUrl: 'https://pay.example.com/ldc',
        outTradeNo: 'sub2_ldc_888',
        clientSecret: '',
        intentId: '',
        currency: '',
        countryCode: '',
        paymentEnv: '',
        payAmount: 66,
        orderType: 'balance',
        paymentMode: 'popup',
        resumeToken: '',
        createdAt: Date.now(),
      }),
    )

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          AppLayout: {
            template: '<div><slot /></div>',
          },
          PaymentStatusPanel: {
            template: '<button data-test="payment-done" @click="$emit(\'done\')" />',
          },
          PaymentMethodSelector: {
            props: ['selected'],
            template: '<div data-test="method-selector">{{ selected }}</div>',
          },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()
    await wrapper.find('[data-test="payment-done"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="method-selector"]').text()).toBe('ldc')
  })
})

describe('PaymentView WeChat JSAPI flow', () => {
  beforeEach(() => {
    routeState.path = '/purchase'
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-token-123',
    }
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    authUserState.value = {
      username: 'demo-user',
      email: 'buyer@example.com',
      balance: 0,
    }
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture())
    bridgeInvoke.mockReset()
    activeSubscriptionsState.value = []
    window.localStorage.clear()
    ;(
      window as Window & {
        WeixinJSBridge?: {
          invoke: typeof bridgeInvoke // mock WeChat payment invocation
        } // optional browser WeChat bridge
      }
    ).WeixinJSBridge = {
      invoke: bridgeInvoke,
    }
  })

  // The WeChat bridge reports a successful JSAPI result for a resumed order. Checkout must clear
  // waiting recovery and navigate to result verification with that order's identifiers.
  it('resets payment state and redirects to /payment/result after JSAPI reports success', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-123'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:ok' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(routerPush).toHaveBeenCalledWith({
      path: '/payment/result',
      query: {
        order_id: '123',
        out_trade_no: 'sub2_jsapi_123',
        resume_token: 'resume-token-123',
      },
    })
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  // The WeChat bridge reports that the user cancelled JSAPI payment. Checkout must clear its
  // waiting state and recovery snapshot while showing the cancellation feedback.
  it('resets payment state when JSAPI reports cancellation', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-cancel'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:cancel' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(showInfo).toHaveBeenCalledWith('payment.qr.cancelled')
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  // A resumed WeChat order never obtains an available bridge before the deadline. Checkout must
  // discard stale waiting recovery and report the unavailable payment path.
  it('clears stale recovery state when JSAPI never becomes available', async () => {
    vi.useFakeTimers()
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-missing-bridge'))
    ;(
      window as Window & {
        WeixinJSBridge?: {
          invoke: typeof bridgeInvoke // mock WeChat payment invocation
        } // optional browser WeChat bridge
      }
    ).WeixinJSBridge = undefined

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          Teleport: true,
          Transition: false,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(4000)
    await flushPromises()
    await flushPromises()

    expect(showError).toHaveBeenCalledWith(
      'payment.errors.wechatJsapiUnavailable payment.errors.wechatOpenInWeChatHint',
    )
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
    expect(wrapper.html()).not.toContain('payment-status-panel-stub')
  })

  // An old recovery entry exists when WeChat authorization return fields arrive. Initialization
  // must remove that entry before processing the resumed order so identities cannot mix.
  it('clears a stale recovery snapshot before handling wechat resume callback params', async () => {
    createOrder.mockRejectedValueOnce(new Error('resume failed'))
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify({
        orderId: 999,
        amount: 66,
        qrCode: 'stale-qr',
        expiresAt: '2099-01-01T00:10:00.000Z',
        paymentType: 'alipay',
        payUrl: 'https://pay.example.com/stale',
        outTradeNo: 'stale-out-trade-no',
        clientSecret: '',
        intentId: '',
        currency: '',
        countryCode: '',
        paymentEnv: '',
        payAmount: 66,
        orderType: 'balance',
        paymentMode: 'popup',
        resumeToken: '',
        createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
      }),
    )

    shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenCalledWith(
      expect.objectContaining({
        wechat_resume_token: 'resume-token-123',
      }),
    )
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  // A WeChat callback supplies a resume token for subscription checkout. The new request must
  // retain subscription type and plan context while leaving amount resolution to the saved
  // intent.
  it('keeps subscription resume context for token-only WeChat callbacks', async () => {
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-subscription-7',
      payment_type: 'wxpay_direct',
      order_type: 'subscription',
      plan_id: '7',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder.mockResolvedValue(oauthOrderFixture())

    const originalLocation = window.location
    const locationState = {
      href: 'http://localhost/purchase',
      origin: 'http://localhost',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState,
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(createOrder).toHaveBeenCalledWith(
      expect.objectContaining({
        payment_type: 'wxpay',
        order_type: 'subscription',
        plan_id: 7,
        wechat_resume_token: 'resume-subscription-7',
      }),
    )
    expect(locationState.href).toContain('/api/v1/auth/oauth/wechat/payment/start?')
    expect(new URL(locationState.href, 'http://localhost').searchParams.get('redirect')).toBe(
      '/purchase?from=wechat&payment_type=wxpay&order_type=subscription&plan_id=7',
    )

    Object.defineProperty(window, 'location', {
      configurable: true,
      value: originalLocation,
    })
  })

  // Mobile WeChat creation fails with a recoverable scenario error. Checkout must request
  // desktop QR parameters and publish the replacement waiting state only after a valid QR reply.
  it('falls back to QR flow when mobile WeChat payment is unavailable', async () => {
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-token-h5',
      payment_type: 'wxpay_direct',
    }
    createOrder
      .mockRejectedValueOnce({ reason: 'WECHAT_H5_NOT_AUTHORIZED' })
      .mockResolvedValueOnce({
        order_id: 778,
        amount: 88,
        pay_amount: 88,
        fee_rate: 0,
        expires_at: '2099-01-01T00:10:00.000Z',
        payment_type: 'wxpay',
        qr_code: 'weixin://wxpay/bizpayurl?pr=fallback-native',
        out_trade_no: 'sub2_qr_778',
      })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({
        payment_type: 'wxpay',
        is_mobile: true,
        wechat_resume_token: 'resume-token-h5',
      }),
    )
    expect(createOrder).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        payment_type: 'wxpay',
        is_mobile: false,
        payment_source: 'hosted_redirect',
      }),
    )
    expect(showWarning).toHaveBeenCalledWith('payment.errors.mobilePaymentFallbackToQr')
    expect(showError).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain(
      'weixin://wxpay/bizpayurl?pr=fallback-native',
    )
  })
})

describe('PaymentView Stripe billing form', () => {
  beforeEach(() => {
    routeState.path = '/purchase'
    routeState.query = {}
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    authUserState.value = {
      username: 'demo-user',
      email: 'buyer@example.com',
      balance: 0,
    }
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    getCheckoutInfo.mockReset().mockResolvedValue(stripeCheckoutInfoFixture())
    bridgeInvoke.mockReset()
    activeSubscriptionsState.value = []
    window.localStorage.clear()
  })

  // Stripe checkout shows the default name and email with the address fields collapsed.
  // Opening the optional section must reveal the editable address fields.
  it('shows Stripe billing defaults and keeps the optional address collapsed', async () => {
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Collapse: false,
          Disclosure: false,
          RetainedContent: false,
          MotionTransition: motionTransitionStub,
          AppLayout: { template: '<main><slot /></main>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()

    const nameInput = wrapper.get('input[autocomplete="name"]')
    const emailInput = wrapper.get('input[autocomplete="email"]')
    expect((nameInput.element as HTMLInputElement).value).toBe('demo-user')
    expect((emailInput.element as HTMLInputElement).value).toBe('buyer@example.com')
    const disclosure = wrapper.get('button[aria-expanded="false"]')
    const address = wrapper.get(`#${disclosure.attributes('aria-controls')}`)
    expect(disclosure.text()).toBe('payment.billing.optionalAddress')
    expect(address.attributes('aria-hidden')).toBe('true')
    await disclosure.trigger('click')
    expect(disclosure.attributes('aria-expanded')).toBe('true')
    expect(address.attributes('aria-hidden')).toBeUndefined()

    const labels = wrapper.findAll('label').map((label) => label.text())
    expect(labels).toContain('payment.billing.name')
    expect(labels).toContain('payment.billing.email')
    expect(labels).toContain('payment.billing.country')
    expect(labels).toContain('payment.billing.postalCode')
    expect(labels).toContain('payment.billing.line1')
    expect(labels).toContain('payment.billing.city')
    expect(labels).toContain('payment.billing.state')
  })

  // The account has an email address but no username. Stripe billing must use that email for the
  // default name and retain it as the billing email.
  it('uses the registered email as the Stripe billing name when username is empty', async () => {
    authUserState.value = {
      username: '',
      email: 'buyer@example.com',
      balance: 0,
    }

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Collapse: false,
          RetainedContent: false,
          MotionTransition: motionTransitionStub,
          AppLayout: { template: '<main><slot /></main>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()

    const nameInput = wrapper.get('input[autocomplete="name"]')
    expect((nameInput.element as HTMLInputElement).value).toBe('buyer@example.com')
  })

  // A Stripe method configures fixed and percentage gateway fees. Checkout must display both
  // components and their combined payable amount after the user enters a recharge amount.
  it('shows Stripe fixed and rate fee breakdown on checkout', async () => {
    getCheckoutInfo.mockReset().mockResolvedValue(stripeCheckoutInfoWithFeeFixture())
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Collapse: false,
          RetainedContent: false,
          MotionTransition: motionTransitionStub,
          AppLayout: { template: '<main><slot /></main>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()

    await wrapper.getComponent({ name: 'AmountInput' }).vm.$emit('update:modelValue', 100)
    await flushPromises()

    expect(wrapper.text()).toContain('payment.fixedFee')
    expect(wrapper.text()).toContain('¥2.50')
    expect(wrapper.text()).toContain('payment.rateFee')
    expect(wrapper.text()).toContain('2.2%')
    expect(wrapper.text()).toContain('¥2.20')
    expect(wrapper.text()).toContain('payment.feeTotal')
    expect(wrapper.text()).toContain('¥4.70')
    expect(wrapper.text()).toContain('¥104.70')
  })

  // Checkout initially selects its sole Stripe method. Deselecting it must disable payment
  // and survive subsequent amount edits until the buyer clicks the method again.
  it('keeps the only payment method deselected until the user selects it again', async () => {
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          AppLayout: { template: '<main><slot /></main>' },
          PaymentMethodSelector: false,
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()

    const amount = wrapper.getComponent(AmountInput)
    const method = wrapper.get('[data-testid="payment-method-grid"] button')
    const submit = wrapper.get('[data-testid="checkout-summary"] button')
    amount.vm.$emit('update:modelValue', 100)
    await flushPromises()
    expect(method.attributes('aria-pressed')).toBe('true')
    expect(submit.attributes('disabled')).toBeUndefined()

    await method.trigger('click')
    expect(method.attributes('aria-pressed')).toBe('false')
    expect(submit.attributes('disabled')).toBeDefined()
    amount.vm.$emit('update:modelValue', 200)
    await flushPromises()
    expect(method.attributes('aria-pressed')).toBe('false')
    expect(submit.attributes('disabled')).toBeDefined()
    expect(createOrder).not.toHaveBeenCalled()

    await method.trigger('click')
    expect(method.attributes('aria-pressed')).toBe('true')
    expect(submit.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  // The selected payment currency supports three fractional digits. Fixed and percentage fee
  // previews must use that precision when rounding the final gateway charge.
  it('calculates fee preview with three-decimal payment currencies', async () => {
    getCheckoutInfo
      .mockReset()
      .mockResolvedValue(stripeCheckoutInfoWithCurrencyFeeFixture('KWD', 0, 1))
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Collapse: false,
          RetainedContent: false,
          MotionTransition: motionTransitionStub,
          AppLayout: { template: '<main><slot /></main>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()

    await wrapper.getComponent({ name: 'AmountInput' }).vm.$emit('update:modelValue', 12.345)
    await flushPromises()

    expect(wrapper.text()).toContain('KWD 0.124')
    expect(wrapper.text()).toContain('KWD 12.469')
    expect(wrapper.text()).not.toContain('KWD 12.470')
  })

  // The selected payment currency has no fractional digits. Percentage fees must round upward to
  // whole currency units, and checkout must display the resulting payable amount.
  it('calculates fee preview with zero-decimal payment currencies', async () => {
    getCheckoutInfo
      .mockReset()
      .mockResolvedValue(stripeCheckoutInfoWithCurrencyFeeFixture('JPY', 0, 2.5))
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Collapse: false,
          RetainedContent: false,
          MotionTransition: motionTransitionStub,
          AppLayout: { template: '<main><slot /></main>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()

    await wrapper.getComponent({ name: 'AmountInput' }).vm.$emit('update:modelValue', 100)
    await flushPromises()

    expect(wrapper.text()).toContain('¥3')
    expect(wrapper.text()).toContain('¥103')
    expect(wrapper.text()).not.toContain('¥102')
  })
})

describe('PaymentView duplicate subscription notice', () => {
  beforeEach(() => {
    routeState.path = '/purchase'
    routeState.query = { tab: 'subscription' }
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    authUserState.value = {
      username: 'demo-user',
      email: 'buyer@example.com',
      balance: 0,
    }
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    activeSubscriptionsState.value = [
      {
        id: 99,
        plan_id: 7,
        status: 'active',
      },
    ]
    fetchActiveSubscriptions.mockReset().mockResolvedValue(activeSubscriptionsState.value)
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoWithPlansFixture())
    bridgeInvoke.mockReset()
    window.localStorage.clear()
  })

  // The user selects a plan that already has an active subscription. Checkout must show the
  // duplicate acknowledgement before replacing the plan list with purchase confirmation.
  it('requires confirmation before selecting an already active plan', async () => {
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          AppLayout: { template: '<main><slot /></main>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()

    await wrapper
      .getComponent({ name: 'SubscriptionPlanCard' })
      .vm.$emit('select', checkoutInfoWithPlansFixture().data.plans[0])
    await flushPromises()

    const dialog = wrapper.getComponent({ name: 'ConfirmDialog' })
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('title')).toBe('payment.duplicatePlan.title')
    expect(wrapper.findAllComponents({ name: 'SubscriptionPlanCard' })).toHaveLength(1)

    await dialog.vm.$emit('confirm')
    await flushPromises()

    expect(wrapper.findAllComponents({ name: 'SubscriptionPlanCard' })).toHaveLength(0)
    expect(wrapper.text()).toContain('payment.createOrder')
  })
})

describe('PaymentView payment help text', () => {
  beforeEach(() => {
    routeState.path = '/purchase'
    routeState.query = {}
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    authUserState.value = {
      username: 'demo-user',
      email: 'buyer@example.com',
      balance: 0,
    }
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    getCheckoutInfo.mockReset().mockResolvedValue({
      data: {
        ...checkoutInfoFixture().data,
        help_text:
          '**发票说明**\\n\\n[联系站长](https://example.com/invoice)<script>alert(1)</script>',
      },
    })
    bridgeInvoke.mockReset()
    activeSubscriptionsState.value = []
    window.localStorage.clear()
  })

  // Administrator help text contains Markdown and an embedded script tag. Checkout must render
  // the formatted text and link while removing executable markup before display.
  it('renders help text as sanitized Markdown', async () => {
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          MotionTransition: motionTransitionStub,
          AppLayout: { template: '<main><slot /></main>' },
          PaymentHelpNote: false,
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()

    const help = wrapper.get('.payment-help-markdown')
    expect(help.find('strong').text()).toBe('发票说明')
    expect(help.find('a').attributes('href')).toBe('https://example.com/invoice')
    expect(help.html()).not.toContain('<script>')
  })
})

// Personal recharge terms retain the selected currency and site unit while distinguishing
// inherited, equal, lower, and one-to-one rates through the actual translation component.
describe('PaymentView personal recharge rate', () => {
  // Rendering both locales with real named slots protects the deleted global number,
  // emphasized personal number, accessible explanation, and credited balance calculation.
  it.each([
    ['en', 18, 10, true],
    ['zh', 18, 10, true],
    ['en', 0.5, 10, true],
    ['zh', 1, 10, true],
    ['en', 10, 10, false],
  ] as const)(
    'renders %s personal rate %s against %s',
    async (locale, rate, globalRate, struck) => {
      routeState.path = '/purchase'
      routeState.query = {}
      getCheckoutInfo.mockResolvedValue(
        checkoutInfoFixture({
          balance_recharge_multiplier: rate,
          default_balance_recharge_multiplier: globalRate,
          user_balance_recharge_multiplier: rate,
        }),
      )
      const wrapper = shallowMount(PaymentView, {
        global: {
          plugins: [createPinia(), createI18n({ legacy: false, locale, messages: { en, zh } })],
          stubs: {
            I18nT: false,
            RouterLink: true,
            MotionTransition: motionTransitionStub,
            AppLayout: { template: '<div><slot /></div>' },
            Teleport: true,
            Transition: false,
          },
        },
      })
      await flushPromises()
      wrapper.getComponent(AmountInput).vm.$emit('update:modelValue', 100)
      await flushPromises()
      const personal = wrapper.get('[data-test="personal-recharge-rate"]')
      expect(personal.text()).toBe(rate.toFixed(2))
      expect(personal.classes()).toEqual(
        expect.arrayContaining(['text-gray-900', 'dark:text-dark-100', 'font-semibold']),
      )
      expect(wrapper.find('[data-test="default-recharge-rate"]').exists()).toBe(struck)
      expect(wrapper.get('[data-test="recharge-rate-preview"]').text()).toContain('CNY')
      expect(translate).toHaveBeenCalledWith(
        'payment.personalRechargeRateAccessible',
        expect.objectContaining({ amount: rate.toFixed(2), defaultAmount: '10.00' }),
      )
      const summary = wrapper.get('[data-test="recharge-rate-preview"]').element.parentElement
      expect(summary?.textContent).toContain(
        (100 * rate).toLocaleString('en-US', {
          minimumFractionDigits: 2,
          maximumFractionDigits: 2,
        }),
      )
      wrapper.unmount()
    },
  )
})

// Invalid entitlements must stop checkout even when the principal fits the selected
// payment method. These cases exercise the page's validation and submission boundary.
describe('PaymentView credit amount validation', () => {
  // Tiny and oversized personal rates cannot produce a payable order. The page explains
  // the credit problem and keeps payment disabled without sending a create request.
  it.each([1e-8, 1e11, 1e308])('blocks unfulfillable credits at rate %s', async (rate) => {
    createOrder.mockReset()
    routeState.path = '/purchase'
    routeState.query = {}
    getCheckoutInfo.mockResolvedValue(
      checkoutInfoFixture({
        balance_recharge_multiplier: rate,
        default_balance_recharge_multiplier: 10,
        user_balance_recharge_multiplier: rate,
      }),
    )
    const wrapper = shallowMount(PaymentView, {
      global: {
        plugins: [createPinia()],
        stubs: {
          MotionTransition: motionTransitionStub,
          AppLayout: { template: '<div><slot /></div>' },
          RouterLink: true,
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    wrapper.getComponent(AmountInput).vm.$emit('update:modelValue', 100)
    await flushPromises()
    expect(wrapper.text()).toContain('payment.errors.INVALID_CREDITED_AMOUNT')
    const pay = wrapper
      .findAll('button')
      .find((button) => button.text().includes('payment.createOrder'))!
    expect(pay.attributes('disabled')).toBeDefined()
    await pay.trigger('click')
    expect(createOrder).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
