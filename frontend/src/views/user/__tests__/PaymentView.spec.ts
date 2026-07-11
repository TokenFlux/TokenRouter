import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import PaymentView from '../PaymentView.vue'
import { PAYMENT_RECOVERY_STORAGE_KEY } from '@/components/payment/paymentFlow'
import { formatPaymentAmount } from '@/components/payment/currency'
import type { CheckoutInfoResponse, MethodLimit, SubscriptionPlan } from '@/types/payment'

function createMemoryStorage(): Storage {
  let store: Record<string, string> = {}
  return {
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

// Vitest 在当前 Node 环境没有注入 window.localStorage，支付恢复逻辑测试需要一个可控实现。
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
      t: (key: string) => key,
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

function checkoutInfoWithPlansFixture(options: {
  checkout?: Partial<CheckoutInfoResponse>
  method?: Partial<MethodLimit>
  plan?: Partial<SubscriptionPlan>
} = {}) {
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

function stripeCheckoutInfoWithCurrencyFeeFixture(currency: string, fixedFee: number, feeRate: number) {
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
      authorize_url: '/api/v1/auth/oauth/wechat/payment/start?payment_type=wxpay&redirect=%2Fpurchase%3Ffrom%3Dwechat',
      appid: 'wx123',
      scope: 'snsapi_base',
      redirect_url: '/auth/wechat/payment/callback',
    },
  }
}

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
  ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: {
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

describe('PaymentView subscription plan group matching', () => {
  it('selects a plan when the route group is included in group_ids', async () => {
    const wrapper = await mountSubscriptionConfirm({
      plan: {
        group_ids: [2, 3],
      },
    })

    expect((wrapper.vm as unknown as { selectedPlan: SubscriptionPlan | null }).selectedPlan?.id).toBe(7)
  })

  it('treats an empty group_ids list as available to every group', async () => {
    const wrapper = await mountSubscriptionConfirm({
      plan: {
        group_ids: [],
      },
    })

    expect((wrapper.vm as unknown as { selectedPlan: SubscriptionPlan | null }).selectedPlan?.id).toBe(7)
  })

  it('does not select a global plan for an invalid route group', async () => {
    const wrapper = await mountSubscriptionConfirm({
      plan: {
        group_ids: [],
      },
    }, { tab: 'subscription', group: 'invalid' })

    expect((wrapper.vm as unknown as { selectedPlan: SubscriptionPlan | null }).selectedPlan).toBeNull()
  })
})

describe('PaymentView subscription confirmation amounts', () => {
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
    // 换算必须使用订阅汇率（×7.15），而不是余额倍率（÷0.14 = 71.36）
    expect(text).not.toContain(formatPaymentAmount(71.36, 'CNY'))
    expect(wrapper.findAll('button').some(button => button.text().includes(convertedPrice))).toBe(true)
  })

  it('keeps plan price when the subscription rate is not configured or payment currency is not CNY', async () => {
    // opt-in 回归锁：即使余额倍率已配置，未配置订阅汇率时 CNY 订阅仍按 price 直付
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
    expect(wrapper.findAll('button').some(button => button.text().includes(total))).toBe(true)
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
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined
  })

  it('restores a custom EasyPay method as the selected payment method', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
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
    }))
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
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
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
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
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = {
      invoke: bridgeInvoke,
    }
  })

  it('resets payment state and redirects to /payment/result after JSAPI reports success', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-123'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:ok' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
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

  it('resets payment state when JSAPI reports cancellation', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-cancel'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:cancel' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
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

  it('clears stale recovery state when JSAPI never becomes available', async () => {
    vi.useFakeTimers()
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-missing-bridge'))
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
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

  it('clears a stale recovery snapshot before handling wechat resume callback params', async () => {
    createOrder.mockRejectedValueOnce(new Error('resume failed'))
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
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
    }))

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      wechat_resume_token: 'resume-token-123',
    }))
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

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
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      payment_type: 'wxpay',
      order_type: 'subscription',
      plan_id: 7,
      wechat_resume_token: 'resume-subscription-7',
    }))
    expect(locationState.href).toContain('/api/v1/auth/oauth/wechat/payment/start?')
    expect(new URL(locationState.href, 'http://localhost').searchParams.get('redirect')).toBe(
      '/purchase?from=wechat&payment_type=wxpay&order_type=subscription&plan_id=7',
    )

    Object.defineProperty(window, 'location', {
      configurable: true,
      value: originalLocation,
    })
  })

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
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenNthCalledWith(1, expect.objectContaining({
      payment_type: 'wxpay',
      is_mobile: true,
      wechat_resume_token: 'resume-token-h5',
    }))
    expect(createOrder).toHaveBeenNthCalledWith(2, expect.objectContaining({
      payment_type: 'wxpay',
      is_mobile: false,
      payment_source: 'hosted_redirect',
    }))
    expect(showWarning).toHaveBeenCalledWith('payment.errors.mobilePaymentFallbackToQr')
    expect(showError).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain('weixin://wxpay/bizpayurl?pr=fallback-native')
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

  it('uses the username and registered email as the Stripe billing defaults and marks optional fields', async () => {
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
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
    const labels = wrapper.findAll('label').map(label => label.text())
    expect(labels).toContain('payment.billing.name')
    expect(labels).toContain('payment.billing.email')
    expect(labels).toContain('payment.billing.countrypayment.billing.optionalMark')
    expect(labels).toContain('payment.billing.postalCodepayment.billing.optionalMark')
    expect(labels).toContain('payment.billing.line1payment.billing.optionalMark')
    expect(labels).toContain('payment.billing.citypayment.billing.optionalMark')
    expect(labels).toContain('payment.billing.statepayment.billing.optionalMark')
  })

  it('uses the registered email as the Stripe billing name when username is empty', async () => {
    authUserState.value = {
      username: '',
      email: 'buyer@example.com',
      balance: 0,
    }

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
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

  it('shows Stripe fixed and rate fee breakdown on checkout', async () => {
    getCheckoutInfo.mockReset().mockResolvedValue(stripeCheckoutInfoWithFeeFixture())
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
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

  it('calculates fee preview with three-decimal payment currencies', async () => {
    getCheckoutInfo.mockReset().mockResolvedValue(stripeCheckoutInfoWithCurrencyFeeFixture('KWD', 0, 1))
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
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

  it('calculates fee preview with zero-decimal payment currencies', async () => {
    getCheckoutInfo.mockReset().mockResolvedValue(stripeCheckoutInfoWithCurrencyFeeFixture('JPY', 0, 2.5))
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
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
    activeSubscriptionsState.value = [{
      id: 99,
      plan_id: 7,
      status: 'active',
    }]
    fetchActiveSubscriptions.mockReset().mockResolvedValue(activeSubscriptionsState.value)
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoWithPlansFixture())
    bridgeInvoke.mockReset()
    window.localStorage.clear()
  })

  it('requires confirmation before selecting an already active plan', async () => {
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()

    await wrapper.getComponent({ name: 'SubscriptionPlanCard' }).vm.$emit('select', checkoutInfoWithPlansFixture().data.plans[0])
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
        help_text: '**发票说明**\\n\\n[联系站长](https://example.com/invoice)<script>alert(1)</script>',
      },
    })
    bridgeInvoke.mockReset()
    activeSubscriptionsState.value = []
    window.localStorage.clear()
  })

  it('renders help text as sanitized Markdown', async () => {
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
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
