import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PaymentQRDialog from '../PaymentQRDialog.vue'

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

const paidOrder = {
  id: 42,
  user_id: 9,
  amount: 100,
  pay_amount: 108,
  currency: 'USD',
  fee_rate: 8,
  fee_fixed: 0,
  fee_rate_amount: 0,
  fee_amount: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_202606250001',
  status: 'COMPLETED',
  order_type: 'subscription',
  created_at: '2026-06-25T10:00:00Z',
  expires_at: '2099-01-01T10:30:00Z',
  refund_amount: 0,
}

describe('PaymentQRDialog currency display', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    pollOrderStatus.mockReset().mockResolvedValue(paidOrder)
    cancelOrder.mockReset()
    verifyOrder.mockReset()
    showError.mockReset()
    toCanvas.mockReset().mockResolvedValue(undefined)
    formatBalanceAmountMock.mockReset().mockImplementation((amount: number) => `Balance ${amount.toFixed(2)}`)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('uses order currency for successful subscription payment amounts', async () => {
    const wrapper = mount(PaymentQRDialog, {
      props: {
        show: false,
        orderId: 42,
        qrCode: '',
        expiresAt: '2099-01-01T10:30:00Z',
        paymentType: 'alipay',
      },
      global: {
        stubs: {
          BaseDialog: {
            props: ['show'],
            template: '<div v-if="show"><slot /><slot name="footer" /></div>',
          },
          Icon: true,
        },
      },
    })

    await wrapper.setProps({ show: true })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('$100.00')
    expect(wrapper.text()).toContain('$108.00')
    expect(wrapper.text()).not.toContain('¥108.00')
  })
})
