import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { PaymentOrder } from '@/api/payment.types'
import AdminOrdersView from '../AdminOrdersView.vue'

// AdminOrdersView.spec.ts exercises refund force consent and manual expiration through
// rendered order actions. Controlled API responses reproduce a force requirement and a
// concurrent status change, while dialog controls and request assertions verify
// confirmation and refresh behavior.

// Hoisted request replacements are shared with imported module mocks so each test can
// control API outcomes and inspect calls.
const {
  mockGetOrders,
  mockRefundOrder,
  mockQueryRefund,
  mockForceExpireOrder,
  mockShowError,
  mockShowSuccess,
  mockShowWarning,
} = vi.hoisted(() => ({
  mockGetOrders: vi.fn(),
  mockRefundOrder: vi.fn(),
  mockQueryRefund: vi.fn(),
  mockForceExpireOrder: vi.fn(),
  mockShowError: vi.fn(),
  mockShowSuccess: vi.fn(),
  mockShowWarning: vi.fn(),
}))

vi.mock('@/api/admin/payment', () => {
  const paymentAPI = {
    getOrders: (...args: unknown[]) => mockGetOrders(...args),
    refundOrder: (...args: unknown[]) => mockRefundOrder(...args),
    queryRefund: (...args: unknown[]) => mockQueryRefund(...args),
    forceExpireOrder: (...args: unknown[]) => mockForceExpireOrder(...args),
  }
  return {
    adminPaymentAPI: paymentAPI,
    default: paymentAPI,
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: mockShowError,
    showSuccess: mockShowSuccess,
    showWarning: mockShowWarning,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) =>
        key === 'payment.errors.ORDER_STATUS_CHANGED' ? '订单状态已变更，列表已刷新' : key,
    }),
  }
})

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitSymbol: '$',
    formatBalanceAmount: (amount: number) => `$${amount.toFixed(2)}`,
  }),
}))

// order supplies a completed balance-purchase fixture reused by refund tests and adapted
// to pending status for force-expiration cases.
const order: PaymentOrder = {
  id: 42,
  user_id: 7,
  amount: 100,
  pay_amount: 100,
  currency: 'USD',
  fee_rate: 0,
  fee_fixed: 0,
  fee_rate_amount: 0,
  fee_amount: 0,
  payment_type: 'alipay',
  out_trade_no: 'order-42',
  status: 'COMPLETED',
  order_type: 'balance',
  created_at: '2026-08-01T00:00:00Z',
  expires_at: '2026-08-01T00:30:00Z',
  refund_amount: 0,
}

// BaseDialogStub renders dialog content and footer slots so tests can exercise the
// contained form controls and emitted actions.
const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

// OrderTableStub exposes order action slots for each supplied row so tests invoke page
// handlers through their rendered buttons.
const OrderTableStub = {
  props: ['orders'],
  template: `
    <div>
      <div v-for="row in orders" :key="row.id">
        <slot name="actions" :row="row" />
      </div>
    </div>
  `,
}

// mountView mounts the order page with dialogs and action slots available to tests. API
// replacements supply rows so simulated clicks exercise the page refund and force-
// expiration handlers.
function mountView() {
  return mount(AdminOrdersView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        BaseDialog: BaseDialogStub,
        Icon: true,
        OrderStatusBadge: true,
        OrderTable: OrderTableStub,
        Pagination: true,
        Select: true,
      },
    },
  })
}

// findRefundAction locates the rendered administrator refund button in the mounted view.
function findRefundAction(wrapper: ReturnType<typeof mountView>) {
  return wrapper.findAll('button').find((button) => button.text() === 'payment.admin.refund')
}

describe('AdminOrdersView', () => {
  beforeEach(() => {
    mockGetOrders.mockReset()
    mockRefundOrder.mockReset()
    mockQueryRefund.mockReset()
    mockForceExpireOrder.mockReset()
    mockShowError.mockReset()
    mockShowSuccess.mockReset()
    mockShowWarning.mockReset()
    mockGetOrders.mockResolvedValue({
      data: { items: [order], total: 1, page: 1, page_size: 20 },
    })
  })

  // An unconfirmed channel reply leaves the server order claimed for recovery. The
  // page closes its stale draft, reloads that state, and blocks another refund submission.
  it('refreshes and closes the refund dialog when recovery is required', async () => {
    mockRefundOrder.mockRejectedValueOnce({
      reason: 'REFUND_RECOVERY_REQUIRED',
      message: 'manual verification',
    })
    const wrapper = mountView()
    await flushPromises()
    await findRefundAction(wrapper)!.trigger('click')
    await flushPromises()
    mockGetOrders.mockResolvedValueOnce({
      data: { items: [{ ...order, status: 'REFUNDING' }], total: 1 },
    })
    await wrapper.find('#refund-form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('#refund-form').exists()).toBe(false)
    expect(mockGetOrders).toHaveBeenCalledTimes(2)
    expect(mockShowWarning).toHaveBeenCalledWith('payment.errors.REFUND_RECOVERY_REQUIRED')
    expect(findRefundAction(wrapper)).toBeUndefined()
    expect(mockRefundOrder).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  // A structured pending response closes the refund draft even when the
  // advisory message does not name the channel's pending status.
  it('refreshes a pending refund and offers only the query action', async () => {
    mockRefundOrder.mockResolvedValueOnce({ data: { success: false, status: 'pending' } })
    const wrapper = mountView()
    await flushPromises()
    await findRefundAction(wrapper)!.trigger('click')
    await flushPromises()
    mockGetOrders.mockResolvedValueOnce({
      data: { items: [{ ...order, status: 'REFUND_PENDING' }], total: 1 },
    })
    await wrapper.get('#refund-form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('#refund-form').exists()).toBe(false)
    expect(findRefundAction(wrapper)).toBeUndefined()
    expect(wrapper.text()).toContain('payment.admin.queryRefundStatus')
    expect(mockShowWarning).toHaveBeenCalledWith('payment.admin.refundPending')
  })

  // Missing Stripe references open a verification form for the original
  // order. A rejected ID stays editable until Stripe verifies the replacement.
  it('verifies a supplied Stripe refund reference before closing the form', async () => {
    mockGetOrders.mockResolvedValue({
      data: { items: [{ ...order, status: 'REFUNDING', payment_type: 'stripe' }], total: 1 },
    })
    mockQueryRefund
      .mockRejectedValueOnce({ reason: 'REFUND_REFERENCE_REQUIRED' })
      .mockRejectedValueOnce({ reason: 'REFUND_REFERENCE_UNVERIFIED' })
      .mockResolvedValueOnce({
        data: { success: false, status: 'failed', warning: 'Refund failed' },
      })
    const wrapper = mountView()
    await flushPromises()
    const query = wrapper
      .findAll('button')
      .find((button) => button.text() === 'payment.admin.queryRefundStatus')
    expect(query).toBeDefined()
    await query!.trigger('click')
    await flushPromises()
    expect(mockQueryRefund).toHaveBeenNthCalledWith(1, order.id, undefined)
    await wrapper.get('#stripe-refund-reference').setValue('re_unrelated')
    await wrapper.get('#refund-reference-form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('#refund-reference-form').exists()).toBe(true)
    expect(mockQueryRefund).toHaveBeenNthCalledWith(2, order.id, 're_unrelated')
    await wrapper.get('#stripe-refund-reference').setValue('re_verified')
    await wrapper.get('#refund-reference-form').trigger('submit')
    await flushPromises()
    expect(mockQueryRefund).toHaveBeenNthCalledWith(3, order.id, 're_verified')
    expect(wrapper.find('#refund-reference-form').exists()).toBe(false)
    expect(mockShowError).toHaveBeenLastCalledWith('Refund failed')
  })

  // The refund API requires force consent after the initial submission. The dialog must
  // retain the warning, require the checkbox before retry, and clear that state when
  // reopened.
  it('后端要求强制退款时保留弹窗并在显式确认后重试', async () => {
    mockRefundOrder
      .mockResolvedValueOnce({
        data: {
          success: false,
          require_force: true,
          warning: 'insufficient balance, use force',
        },
      })
      .mockResolvedValueOnce({ data: { success: true } })

    const wrapper = mountView()
    await flushPromises()

    const refundAction = findRefundAction(wrapper)
    expect(refundAction).toBeDefined()
    await refundAction!.trigger('click')
    await flushPromises()

    expect(wrapper.find('#force-refund').exists()).toBe(false)
    await wrapper.find('#refund-form').trigger('submit')
    await flushPromises()

    expect(mockRefundOrder).toHaveBeenNthCalledWith(1, 42, {
      amount: 100,
      reason: '',
      deduct_balance: true,
      force: false,
    })
    expect(wrapper.text()).toContain('insufficient balance, use force')
    const forceCheckbox = wrapper.find<HTMLInputElement>('#force-refund')
    expect(forceCheckbox.exists()).toBe(true)
    expect(wrapper.find<HTMLButtonElement>('button[form="refund-form"]').element.disabled).toBe(
      true,
    )

    await forceCheckbox.setValue(true)
    expect(wrapper.find<HTMLButtonElement>('button[form="refund-form"]').element.disabled).toBe(
      false,
    )
    await wrapper.find('#refund-form').trigger('submit')
    await flushPromises()

    expect(mockRefundOrder).toHaveBeenNthCalledWith(2, 42, {
      amount: 100,
      reason: '',
      deduct_balance: true,
      force: true,
    })
    expect(wrapper.find('#refund-form').exists()).toBe(false)
    expect(mockShowSuccess).toHaveBeenCalledWith('payment.admin.refundSuccess')

    await findRefundAction(wrapper)!.trigger('click')
    await flushPromises()
    expect(wrapper.find('#force-refund').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('insufficient balance, use force')
  })

  // A pending order is selected for manual expiration. The confirmation must require both
  // a reason and consent before sending the API request and closing after success.
  it('管理员确认并填写原因后才能强制过期待支付订单', async () => {
    const pendingOrder = { ...order, status: 'PENDING' as const }
    mockGetOrders.mockResolvedValue({
      data: { items: [pendingOrder], total: 1, page: 1, page_size: 20 },
    })
    mockForceExpireOrder.mockResolvedValue({ data: { message: 'force_expired' } })

    const wrapper = mountView()
    await flushPromises()

    const forceAction = wrapper
      .findAll('button')
      .find((button) => button.text() === 'payment.admin.forceExpire')
    expect(forceAction).toBeDefined()
    await forceAction!.trigger('click')
    await flushPromises()

    const submitButton = wrapper.find<HTMLButtonElement>('button[form="force-expire-form"]')
    expect(submitButton.exists()).toBe(true)
    expect(submitButton.element.disabled).toBe(true)
    await wrapper
      .find<HTMLTextAreaElement>('#force-expire-reason')
      .setValue('provider endpoint returned HTML')
    expect(submitButton.element.disabled).toBe(true)
    await wrapper.find<HTMLInputElement>('#force-expire-confirm').setValue(true)
    expect(submitButton.element.disabled).toBe(false)

    await wrapper.find('#force-expire-form').trigger('submit')
    await flushPromises()

    expect(mockForceExpireOrder).toHaveBeenCalledWith(42, {
      reason: 'provider endpoint returned HTML',
    })
    expect(mockShowSuccess).toHaveBeenCalledWith('payment.admin.forceExpireSuccess')
    expect(wrapper.find('#force-expire-form').exists()).toBe(false)
  })

  // Payment changes the order status before manual expiration completes. The page must
  // report the status conflict, close its stale confirmation, and reload the order list.
  it('强制过期与付款完成竞争时刷新订单列表并关闭确认弹窗', async () => {
    const pendingOrder = { ...order, status: 'PENDING' as const }
    mockGetOrders.mockResolvedValue({
      data: { items: [pendingOrder], total: 1, page: 1, page_size: 20 },
    })
    mockForceExpireOrder.mockRejectedValue({
      reason: 'ORDER_STATUS_CHANGED',
      message: 'order status changed before force expiration',
    })

    const wrapper = mountView()
    await flushPromises()
    const forceAction = wrapper
      .findAll('button')
      .find((button) => button.text() === 'payment.admin.forceExpire')
    await forceAction!.trigger('click')
    await wrapper
      .find<HTMLTextAreaElement>('#force-expire-reason')
      .setValue('provider callback won the race')
    await wrapper.find<HTMLInputElement>('#force-expire-confirm').setValue(true)
    await wrapper.find('#force-expire-form').trigger('submit')
    await flushPromises()

    expect(mockShowError).toHaveBeenCalledWith('订单状态已变更，列表已刷新')
    expect(mockGetOrders).toHaveBeenCalledTimes(2)
    expect(wrapper.find('#force-expire-form').exists()).toBe(false)
  })
})
