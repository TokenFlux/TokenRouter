import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

// Hoisted payment, rendering, and feedback doubles supply controlled order transitions while fake timers advance polling and mobile handoff deadlines.
const pollOrderStatus = vi.hoisted(() => vi.fn())
const cancelOrder = vi.hoisted(() => vi.fn())
const verifyOrder = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const toCanvas = vi.hoisted(() => vi.fn())
const formatBalanceAmountMock = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    pollOrderStatus,
  }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    cancelOrder,
    verifyOrder,
  },
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    formatBalanceAmount: (...args: any[]) => formatBalanceAmountMock(...args),
  }),
}))

vi.mock('qrcode', () => ({
  default: {
    toCanvas,
  },
}))

import PaymentStatusPanel from '../PaymentStatusPanel.vue'

/**
 * PaymentStatusPanel.spec.ts checks payment waiting, upstream recovery, cancellation races,
 * and mobile Alipay presentation through the mounted panel. Controlled gateway replies and
 * fake timers keep order state transitions deterministic without sending payment or refund
 * requests.
 */

/**
 * orderFactory creates a payment order fixture carrying the requested server status.
 */
const orderFactory = (status: string) => ({
  id: 42,
  user_id: 9,
  amount: 88,
  pay_amount: 88,
  fee_rate: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_20260420abcd1234',
  status,
  order_type: 'balance',
  created_at: '2026-04-20T12:00:00Z',
  expires_at: '2099-01-01T12:30:00Z',
  refund_amount: 0,
})

describe('PaymentStatusPanel', () => {
  // A GMPay order remains pending after checkout opens and after a manual status check.
  // The panel shows cashier guidance and emits success only after a confirmed server result.
  it('keeps cryptocurrency payment pending while showing cashier and recovery actions', async () => {
    pollOrderStatus.mockResolvedValue({ ...orderFactory('PENDING'), payment_type: 'epusdt' })
    verifyOrder.mockResolvedValue({ data: { ...orderFactory('PENDING'), payment_type: 'epusdt' } })
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        outTradeNo: 'tr_fixture',
        paymentType: 'epusdt',
        payUrl: 'https://pay.example/cashier/trade',
        expiresAt: '2099-01-01T12:30:00Z',
        payAmount: 100,
        currency: 'CNY',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('payment.crypto.waiting')
    expect(wrapper.text()).toContain('payment.crypto.confirmationHint')
    const check = wrapper
      .findAll('button')
      .find((button) => button.text() === 'payment.crypto.checkStatus')
    expect(check).toBeDefined()
    await check!.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('success')).toBeUndefined()
    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    wrapper.unmount()
  })

  // An unconfirmed creation response can retain an order without a usable cashier URL.
  // The waiting panel offers status checks and avoids presenting an invented payment destination.
  it('shows pending creation recovery without a cashier link', async () => {
    pollOrderStatus.mockResolvedValue({ ...orderFactory('PENDING'), payment_type: 'epusdt' })
    verifyOrder.mockResolvedValue({ data: { ...orderFactory('PENDING'), payment_type: 'epusdt' } })
    const wrapper = mount(PaymentStatusPanel, {
      props: { orderId: 42, paymentType: 'epusdt', expiresAt: '2099-01-01T12:30:00Z' },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('payment.crypto.creationUnconfirmed')
    expect(wrapper.text()).not.toContain('payment.crypto.continue')
    expect(wrapper.emitted('success')).toBeUndefined()
    wrapper.unmount()
  })

  beforeEach(() => {
    vi.useFakeTimers()
    pollOrderStatus.mockReset()
    cancelOrder.mockReset()
    verifyOrder.mockReset()
    showError.mockReset()
    toCanvas.mockReset().mockResolvedValue(undefined)
    formatBalanceAmountMock
      .mockReset()
      .mockImplementation((amount: number) => `Balance ${amount.toFixed(2)}`)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  // Order polling returns RECHARGING while internal fulfillment is still progressing. The
  // waiting panel must display payment success after polling the order and emit one success
  // event for the observed response.
  it('treats RECHARGING as a successful terminal state', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('RECHARGING'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  // A recharging balance order reaches the payment-success presentation before fulfillment
  // completes. The panel must call the account-unit formatter with two fraction digits and
  // display its returned credited quantity.
  it('uses the shared balance formatter instead of a hard-coded currency symbol', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('RECHARGING'))
    // The custom formatted value identifies the shared balance formatter in the success panel,
    // independently of the gateway currency symbol.
    formatBalanceAmountMock.mockReturnValue('Credits 88.00')

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(formatBalanceAmountMock).toHaveBeenCalledWith(88, { fractionDigits: 2 })
    expect(wrapper.text()).toContain('Credits 88.00')
    expect(wrapper.text()).not.toContain('$88.00')
  })

  // The resolved order returns its actual gateway currency after successful polling. The
  // terminal panel must use that currency when rendering the charged payment amount.
  it('uses resolved order currency for pay amount after successful polling', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('COMPLETED'),
      order_type: 'subscription',
      amount: 100,
      pay_amount: 108,
      currency: 'USD',
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'subscription',
        currency: 'CNY',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(wrapper.text()).toContain('$108.00')
    expect(wrapper.text()).toContain('$100.00')
    expect(wrapper.text()).not.toContain('¥108.00')
  })

  // Polling resolves a USD subscription whose charge was converted to CNY. The terminal
  // view must show the saved price currency and the resolved channel currency independently.
  it('separates subscription price from converted payment after polling', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('COMPLETED'),
      order_type: 'subscription',
      amount: 10,
      amount_currency: 'USD',
      pay_amount: 70,
      currency: 'CNY',
    })
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'subscription',
        currency: 'USD',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(wrapper.text()).toContain('$10.00')
    expect(wrapper.text()).toContain('¥70.00')
    expect(wrapper.text()).not.toContain('¥10.00')
  })

  // A QR-mode order also supplies a payable browser URL. The waiting panel must expose its
  // reopen action so the user can reach the hosted destination as well.
  it('shows reopen button in QR mode when payUrl is also available', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue({ closed: false } as Window)

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        payUrl: 'https://pay.example.com/session/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('payment.qr.openPayWindow')

    await wrapper.get('button.btn.btn-secondary.text-sm').trigger('click')
    expect(openSpy).toHaveBeenCalledWith(
      'https://pay.example.com/session/42',
      'paymentPopup',
      expect.any(String),
    )

    openSpy.mockRestore()
  })

  // A Stripe order supplies a trade reference for upstream verification. Polling must query that
  // verification path and apply the returned status to the waiting panel.
  it('actively verifies upstream status when out_trade_no is available', async () => {
    verifyOrder.mockResolvedValue({ data: orderFactory('PAID') })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'stripe',
        outTradeNo: 'sub2_20260420abcd1234',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
  })

  // A non-Stripe order has a trade reference but is already resolved by local status. Polling
  // must use the local order path without issuing an unnecessary upstream verification request.
  it('keeps non-stripe payment polling read-only even when out_trade_no is available', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('RECHARGING'))
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        outTradeNo: 'sub2_20260420abcd1234',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(verifyOrder).not.toHaveBeenCalled()
    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('payment.result.success')
  })

  // A custom method code contains a built-in wallet name. QR instructions must remain generic
  // because only exact configured aliases identify the built-in payment flows.
  it('uses generic QR copy for custom methods that contain built-in names', async () => {
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'card_alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.qr.scanToPay')
    expect(wrapper.text()).not.toContain('payment.qr.scanAlipay')
  })

  // A WeChat order remains locally pending while its upstream payment succeeds. Bounded recovery
  // verification must discover that success and settle the waiting panel.
  it('actively verifies a stuck wxpay pending order and settles it when upstream confirms payment', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'wxpay',
        outTradeNo: 'sub2_20260420abcd1234',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  // Cancellation discovers that the order was already paid. The panel must resolve its current
  // status and show success instead of replacing a paid order with cancellation.
  it('shows success when cancel discovers the order was already paid', async () => {
    cancelOrder.mockResolvedValue({ data: { message: 'already_paid' } })
    verifyOrder.mockResolvedValue({ data: orderFactory('COMPLETED') })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'stripe',
        outTradeNo: 'sub2_20260420abcd1234',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await wrapper.get('button.btn.btn-secondary.w-full').trigger('click')
    await flushPromises()

    expect(cancelOrder).toHaveBeenCalledWith(42)
    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  // Cancellation reports already_paid but upstream verification still returns a pending order.
  // The panel must retain its waiting instructions, emit no success event, and resume the countdown.
  it('keeps waiting when cancel sees already_paid but local order is not successful yet', async () => {
    vi.setSystemTime(new Date('2099-01-01T12:00:00Z'))
    cancelOrder.mockResolvedValue({ data: { message: 'already_paid' } })
    verifyOrder.mockResolvedValue({ data: orderFactory('PENDING') })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'stripe',
        outTradeNo: 'sub2_20260420abcd1234',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await wrapper.get('button.btn.btn-secondary.w-full').trigger('click')
    await flushPromises()

    expect(cancelOrder).toHaveBeenCalledWith(42)
    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.text()).not.toContain('payment.result.success')
    expect(wrapper.emitted('success')).toBeUndefined()
    expect(wrapper.text()).toContain('payment.qr.payInNewWindowHint')

    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()

    expect(wrapper.text()).toContain('29:59')
  })

  // The server changes the order to PROCESSING near its local expiry. The panel must stop the
  // expiry countdown and use slower local status polling while payment settles.
  it('stops expiry countdown and switches to slow local polling while payment is processing', async () => {
    vi.setSystemTime(new Date('2099-01-01T12:00:00Z'))
    verifyOrder.mockResolvedValue({ data: orderFactory('PROCESSING') })
    pollOrderStatus.mockResolvedValue(orderFactory('PROCESSING'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: '2099-01-01T12:00:02Z',
        paymentType: 'stripe',
        outTradeNo: 'sub2_20260420abcd1234',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.text()).toContain('payment.result.processingHint')
    expect(wrapper.find('button.btn.btn-secondary.w-full').exists()).toBe(false)

    await vi.advanceTimersByTimeAsync(13999)
    await flushPromises()
    expect(pollOrderStatus).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrder).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).not.toContain('payment.qr.expired')
  })

  // A mobile Alipay precreate order remains locally pending. The panel must perform the eligible
  // upstream recovery check and apply the payment state returned by the provider.
  it('actively verifies a pending mobile Alipay precreate order', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign: vi.fn() },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => false,
    })
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({ data: orderFactory('COMPLETED') })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.emitted('success')).toHaveLength(1)

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })

  // A desktop Alipay order remains locally pending with a trade reference. The same bounded
  // wallet recovery path must verify upstream status before continuing to wait.
  it('actively verifies a pending desktop Alipay order', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({ data: orderFactory('COMPLETED') })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/desktop-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.emitted('success')).toHaveLength(1)

    wrapper.unmount()
  })

  // A mobile Alipay application launch leaves the browser visible. QR fallback must stay hidden
  // during the handoff deadline and appear only after that deadline elapses.
  it('keeps the QR fallback hidden until the Alipay app launch times out', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    const assign = vi.fn()
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => false,
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    expect(assign).toHaveBeenCalledWith(
      expect.stringContaining('alipays://platformapi/startapp?saId=10000007&qrcode='),
    )
    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(false)

    await vi.advanceTimersByTimeAsync(2200)
    await flushPromises()

    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('payment.qr.saveQRCode')
    expect(wrapper.text()).toContain('sub2_20260420abcd1234')
    expect(toCanvas).toHaveBeenCalledWith(
      expect.any(HTMLCanvasElement),
      'https://qr.alipay.com/dynamic-order-42',
      expect.any(Object),
    )

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })

  // The browser enters the background during mobile Alipay launch. The panel must treat that as
  // handoff and keep QR fallback hidden after the timer would otherwise expire.
  it('does not show the QR fallback after the page enters the background', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    let hidden = false
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign: vi.fn() },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => hidden,
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    hidden = true
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(2200)
    await flushPromises()

    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('payment.qr.alipayContinueInApp')

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })
})
