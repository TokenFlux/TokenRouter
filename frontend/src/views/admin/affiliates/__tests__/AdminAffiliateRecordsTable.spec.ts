import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AdminAffiliateRecordsTable from '../AdminAffiliateRecordsTable.vue'

// AdminAffiliateRecordsTable.spec.ts checks balance-unit and currency rendering across
// affiliate record types and the user overview. Mocked endpoints provide invitation,
// rebate, and transfer examples, while slot-rendering table stubs exercise the
// production cell formatters and overview interaction.

// Hoisted endpoint, formatter, and notification mocks let each test inspect production
// rendering against predictable affiliate response fixtures.
const {
  formatBalanceAmount,
  formatPaymentAmount,
  getUserOverview,
  listInviteRecords,
  listRebateRecords,
  listTransferRecords,
  showError,
} = vi.hoisted(() => ({
  formatBalanceAmount: vi.fn(),
  formatPaymentAmount: vi.fn(),
  getUserOverview: vi.fn(),
  listInviteRecords: vi.fn(),
  listRebateRecords: vi.fn(),
  listTransferRecords: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin/affiliates', () => ({
  affiliatesAPI: {
    getUserOverview,
    listInviteRecords,
    listRebateRecords,
    listTransferRecords,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError }),
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitName: { value: '推理积分' },
    formatBalanceAmount,
  }),
}))

vi.mock('@/utils/payment/currency', () => ({
  formatPaymentAmount,
}))

vi.mock('@/utils/format', () => ({
  formatDateTime: () => '2026-07-20 12:00:00',
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

// paginated wraps fixture rows in a first-page response with matching total.
const paginated = (items: Record<string, unknown>[]) => ({
  items,
  total: items.length,
  page: 1,
  page_size: 20,
})

// DataTableStub renders production cell slots for each fixture row, retaining column
// keys so assertions observe the selected record presentation.
const DataTableStub = {
  // columns names the production slots and data supplies their rows; the fixture
  // exposes both so assertions exercise the parent cell renderers.
  props: ['columns', 'data'],
  template: `
    <div>
      <div v-for="(row, rowIndex) in data" :key="rowIndex">
        <div v-for="column in columns" :key="column.key" :data-column="column.key">
          <slot :name="'cell-' + column.key" :row="row" :value="row[column.key]">
            {{ row[column.key] }}
          </slot>
        </div>
      </div>
    </div>
  `,
}

// mountTable mounts the selected affiliate record type with slot-rendering table and
// dialog stubs. Endpoint and formatting mocks remain observable, so tests can inspect
// production cell output and trigger the user overview.
function mountTable(type: 'invites' | 'rebates' | 'transfers') {
  return mount(AdminAffiliateRecordsTable, {
    props: { type },
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template:
            '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
        },
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show" data-testid="overview-dialog"><slot /></div>',
        },
        Icon: true,
        OrderStatusBadge: true,
      },
    },
  })
}

describe('AdminAffiliateRecordsTable', () => {
  beforeEach(() => {
    formatBalanceAmount.mockReset()
    formatPaymentAmount.mockReset()
    getUserOverview.mockReset()
    listInviteRecords.mockReset()
    listRebateRecords.mockReset()
    listTransferRecords.mockReset()
    showError.mockReset()

    formatBalanceAmount.mockImplementation(
      (value: number | null | undefined) => `积分${Number(value ?? 0).toFixed(2)}`,
    )
    formatPaymentAmount.mockImplementation(
      (value: number, currency?: string | null) =>
        `${currency || 'CNY'} ${Number(value).toFixed(2)}`,
    )
    listInviteRecords.mockResolvedValue(
      paginated([
        {
          inviter_id: 1,
          inviter_email: 'inviter@example.com',
          inviter_username: 'inviter',
          invitee_id: 2,
          invitee_email: 'invitee@example.com',
          invitee_username: 'invitee',
          aff_code: 'AFF123',
          total_rebate: 120,
          created_at: '2026-07-20T03:00:00Z',
        },
      ]),
    )
    listRebateRecords.mockResolvedValue(
      paginated([
        {
          order_id: 10,
          out_trade_no: 'ORDER-10',
          inviter_id: 1,
          inviter_email: 'inviter@example.com',
          inviter_username: 'inviter',
          invitee_id: 2,
          invitee_email: 'invitee@example.com',
          invitee_username: 'invitee',
          order_amount: 1000,
          pay_amount: 100,
          order_type: 'balance',
          order_currency: '',
          pay_currency: 'CNY',
          rebate_amount: 200,
          payment_type: 'alipay',
          order_status: 'completed',
          created_at: '2026-07-20T03:00:00Z',
        },
        {
          order_id: 11,
          out_trade_no: 'ORDER-11',
          inviter_id: 1,
          inviter_email: 'inviter@example.com',
          inviter_username: 'inviter',
          invitee_id: 2,
          invitee_email: 'invitee@example.com',
          invitee_username: 'invitee',
          order_amount: 10,
          pay_amount: 72,
          order_type: 'subscription',
          order_currency: 'USD',
          pay_currency: 'CNY',
          rebate_amount: 200,
          payment_type: 'stripe',
          order_status: 'completed',
          created_at: '2026-07-20T03:00:00Z',
        },
      ]),
    )
    listTransferRecords.mockResolvedValue(
      paginated([
        {
          ledger_id: 20,
          user_id: 1,
          user_email: 'inviter@example.com',
          username: 'inviter',
          amount: 80,
          balance_after: 580,
          available_quota_after: 20,
          frozen_quota_after: 5,
          history_quota_after: 300,
          snapshot_available: true,
          created_at: '2026-07-20T03:00:00Z',
        },
      ]),
    )
    getUserOverview.mockResolvedValue({
      user_id: 1,
      email: 'inviter@example.com',
      username: 'inviter',
      aff_code: 'AFF123',
      rebate_rate_percent: 20,
      invited_count: 2,
      rebated_invitee_count: 1,
      available_quota: 60,
      history_quota: 360,
    })
  })

  // Invitation and rebate fixtures contain both balance units and currency-denominated
  // orders. Cells must use the configured balance formatter for credits and retain
  // payment currencies for subscription prices and payments.
  it('uses the configured balance unit for invite and rebate records', async () => {
    const invites = mountTable('invites')
    await flushPromises()
    expect(invites.text()).toContain('积分120.00')

    const rebates = mountTable('rebates')
    await flushPromises()
    expect(rebates.text()).toContain('积分1000.00')
    expect(rebates.text()).toContain('积分200.00')
    expect(rebates.text()).toContain('USD 10.00')
    expect(rebates.text()).toContain('CNY 72.00')
    expect(rebates.text()).not.toContain('$1000.00')
    expect(rebates.text()).not.toContain('$200.00')
    expect(formatPaymentAmount).toHaveBeenCalledWith(100, 'CNY')
    expect(formatPaymentAmount).toHaveBeenCalledWith(10, 'USD')
    expect(formatPaymentAmount).toHaveBeenCalledWith(72, 'CNY')
  })

  // A transfer fixture contains account and affiliate quota snapshots, then opens its
  // user overview. Both surfaces must format balance amounts through the configured
  // account-unit formatter.
  it('uses the configured balance unit for transfers and user overview', async () => {
    const wrapper = mountTable('transfers')
    await flushPromises()

    for (const amount of [80, 580, 20, 5, 300]) {
      expect(wrapper.text()).toContain(`积分${amount.toFixed(2)}`)
      expect(formatBalanceAmount).toHaveBeenCalledWith(amount)
    }

    const userButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('inviter@example.com'))
    expect(userButton).toBeDefined()
    await userButton!.trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="overview-dialog"]').text()).toContain('积分60.00')
    expect(wrapper.get('[data-testid="overview-dialog"]').text()).toContain('积分360.00')
    expect(wrapper.get('[data-testid="overview-dialog"]').text()).not.toContain('$')
  })
})
