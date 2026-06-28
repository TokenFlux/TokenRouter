import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { PaymentOrder } from '@/types/payment'
import AdminOrderDetail from '../AdminOrderDetail.vue'
import AdminOrderTable from '../AdminOrderTable.vue'
import AdminRefundDialog from '../AdminRefundDialog.vue'

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

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitSymbol: '点',
    formatBalanceAmount: (...args: any[]) => formatBalanceAmountMock(...args),
  }),
}))

const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id">
        <slot name="cell-pay_amount" :value="row.pay_amount" :row="row" />
      </div>
    </div>
  `,
}

function orderFactory(overrides: Partial<PaymentOrder> = {}): PaymentOrder {
  return {
    id: 1,
    user_id: 10,
    amount: 100,
    pay_amount: 108,
    currency: 'USD',
    fee_rate: 8,
    fee_fixed: 2,
    fee_rate_amount: 6,
    fee_amount: 8,
    payment_type: 'stripe',
    out_trade_no: 'sub2_202606250001',
    status: 'COMPLETED',
    order_type: 'subscription',
    created_at: '2026-06-25T10:00:00Z',
    expires_at: '2026-06-25T10:30:00Z',
    refund_amount: 25,
    ...overrides,
  }
}

describe('admin order currency display', () => {
  it('uses order currency for detail paid/base/fee amounts', () => {
    const wrapper = mount(AdminOrderDetail, {
      props: {
        show: true,
        order: orderFactory(),
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('$100.00')
    expect(text).toContain('$2.00')
    expect(text).toContain('$6.00')
    expect(text).toContain('$8.00')
    expect(text).toContain('$108.00')
    expect(text).toContain('$25.00')
    expect(text).not.toContain('¥108.00')
  })

  it('keeps balance amounts on the configured balance unit', () => {
    formatBalanceAmountMock.mockReturnValue('点数100.00')

    const wrapper = mount(AdminOrderDetail, {
      props: {
        show: true,
        order: orderFactory({ order_type: 'balance', amount: 100, pay_amount: 108 }),
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
        },
      },
    })

    expect(formatBalanceAmountMock).toHaveBeenCalledWith(100, { fractionDigits: 2 })
    expect(wrapper.text()).toContain('点数100.00')
  })

  it('uses order currency in admin table fee tooltip and pay amount', () => {
    const wrapper = mount(AdminOrderTable, {
      props: {
        orders: [orderFactory()],
        loading: false,
        page: 1,
        pageSize: 20,
        total: 1,
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          Icon: true,
          Pagination: true,
          Select: true,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('$108.00')
    expect(text).toContain('+$8.00')
    expect(wrapper.find('[title*="$2.00"]').exists()).toBe(true)
    expect(wrapper.find('[title*="$6.00"]').exists()).toBe(true)
  })

  it('uses order currency for subscription refund amounts', () => {
    const wrapper = mount(AdminRefundDialog, {
      props: {
        show: true,
        order: orderFactory({
          status: 'PARTIALLY_REFUNDED',
          refund_amount: 20,
        }),
        userBalance: 200,
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('$108.00')
    expect(text).toContain('$100.00')
    expect(text).toContain('$20.00')
    expect(text).toContain('$80.00')
  })
})
