import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import OrderTable from '@/components/payment/OrderTable.vue'

const formatBalanceAmountMock = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    formatBalanceAmount: (...args: any[]) => formatBalanceAmountMock(...args),
  }),
}))

const baseOrder = {
  id: 42,
  user_id: 7,
  amount: 65,
  pay_amount: 12.92,
  fee_rate: 2.2,
  fee_fixed: 0,
  fee_rate_amount: 0,
  fee_amount: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_42',
  status: 'COMPLETED' as const,
  order_type: 'balance' as const,
  created_at: '2026-05-11T12:00:00Z',
  expires_at: '2026-05-11T12:30:00Z',
  refund_amount: 0,
}

describe('OrderTable', () => {
  beforeEach(() => {
    formatBalanceAmountMock.mockReset()
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    })
  })

  it('uses configured balance unit for credited balance orders', () => {
    // 回归覆盖：余额订单到账金额不能写死为 $，必须走用户配置的余额单位格式化。
    formatBalanceAmountMock.mockReturnValue('点数65.00')

    const wrapper = mount(OrderTable, {
      props: {
        orders: [baseOrder],
        loading: false,
      },
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    expect(formatBalanceAmountMock).toHaveBeenCalledWith(65, { fractionDigits: 2 })
    expect(wrapper.text()).toContain('payment.orders.creditedAmount: 点数65.00')
    expect(wrapper.text()).not.toContain('$65.00')
  })

  it('uses order currency for pay amount and subscription amount', () => {
    const wrapper = mount(OrderTable, {
      props: {
        orders: [
          {
            ...baseOrder,
            order_type: 'subscription' as const,
            amount: 100,
            pay_amount: 108,
            currency: 'USD',
          },
        ],
        loading: false,
      },
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    expect(wrapper.text()).toContain('$108.00')
    expect(wrapper.text()).toContain('payment.orders.creditedAmount: $100.00')
    expect(wrapper.text()).not.toContain('¥108.00')
  })
})
