import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { AdminUser } from '@/api/admin/users.types'

// UserBalanceHistoryModal.spec.ts checks how the administrator history dialog follows the
// account it displays and the parent's edit-refresh key. Mocked history responses expose
// the requested page and filter, so the tests observe first-page reloads, retained
// filters, account switches, and discarded late responses.

// apiMocks supplies the paginated history responses requested by the dialog.
const apiMocks = vi.hoisted(() => ({
  getUserBalanceHistory: vi.fn(), // History request spy.
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      getUserBalanceHistory: apiMocks.getUserBalanceHistory, // History request substitute.
    }, // User endpoint substitutes.
  }, // Administrator API substitute.
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    cachedPublicSettings: null, // No configured balance-unit overrides.
  }), // Store substitute without cached settings.
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    cachedPublicSettings: null, // No configured balance-unit overrides.
  }), // Direct store-module substitute.
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key, // Translation key passthrough.
    }), // Translation composable substitute.
  }
})

import UserBalanceHistoryModal from '../UserBalanceHistoryModal.vue'

// historyUser is the selected account whose history the dialog requests and displays.
const historyUser = {
  id: 42, // Fixture record identifier.
  email: 'history@example.com', // Account email address.
  username: 'history-user', // Account display name.
  balance: 7.45, // Current account balance.
} as AdminUser

// createHistoryPage builds one paginated history response from a balance adjustment
// fixture, using the supplied row count, recharge total, and administrator note.
const createHistoryPage = (itemCount: number, totalRecharged: number, notes = 'adjustment') => ({
  items: Array.from({ length: itemCount }, (_, index) => ({
    id: index + 1, // Record identifier value.
    code: `code-${index + 1}`, // Adjustment code.
    type: 'admin_balance', // Administrator balance adjustment type.
    value: 7.5, // Adjusted amount.
    status: 'used', // Applied record status.
    used_by: 42, // Adjusted user identifier.
    used_at: '2026-10-29T09:33:01Z', // Application timestamp.
    created_at: '2026-10-29T09:33:01Z', // Record creation timestamp.
    group_id: null, // Unrelated group identifier.
    validity_days: 0, // Unused subscription duration.
    notes, // Administrator note.
  })),
  total: itemCount, // Matching record count.
  page: 1, // One-based result page.
  page_size: 15, // Rows per page.
  pages: 1, // Available page count.
  total_recharged: totalRecharged, // Aggregate recharge amount.
})

// createDeferredResponse returns a promise with its settlement functions, letting a test
// finish two history requests out of order or fail the later one.
const createDeferredResponse = () => {
  let resolve!: (value: ReturnType<typeof createHistoryPage>) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<ReturnType<typeof createHistoryPage>>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
}

// mountDialog mounts the closed dialog with a shell stub, a single-action filter stub,
// and inert child rendering so the tests focus on data reloads.
const mountDialog = () =>
  mount(UserBalanceHistoryModal, {
    props: {
      show: false, // Parent-controlled dialog visibility.
      user: historyUser, // Selected account record.
    },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show'], // Accepted component props.
          template: '<div v-if="show"><slot /></div>', // Rendered stub markup.
        }, // Dialog rendering substitute.
        Select: {
          props: ['modelValue', 'options'], // Accepted component props.
          emits: ['update:modelValue', 'change'], // Filter events.
          template:
            "<button data-test=\"filter-balance\" @click=\"$emit('update:modelValue', 'balance'); $emit('change')\">filter</button>", // Filter selection stub.
        }, // Single-action selector substitute.
        ContentSkeleton: true,
        UserAvatar: true,
        Icon: true,
        BalanceIcon: true,
        Teleport: true,
      },
    },
  })

beforeEach(() => {
  vi.clearAllMocks()
  apiMocks.getUserBalanceHistory.mockResolvedValue(createHistoryPage(1, 7.5))
})

describe('UserBalanceHistoryModal', () => {
  // Opening the dialog requests the first page without a type filter. A new refresh key
  // must request the first page again so the new entry and recharge total replace the
  // pre-edit values while the dialog stays open.
  it('reloads the first page when the parent reports an edit', async () => {
    const wrapper = mountDialog()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(apiMocks.getUserBalanceHistory).toHaveBeenCalledTimes(1)
    expect(apiMocks.getUserBalanceHistory).toHaveBeenLastCalledWith(42, 1, 15, undefined)
    expect(wrapper.text()).toContain('$7.50')

    apiMocks.getUserBalanceHistory.mockResolvedValue(createHistoryPage(2, 15))
    await wrapper.setProps({ refreshKey: 1 })
    await flushPromises()

    expect(apiMocks.getUserBalanceHistory).toHaveBeenCalledTimes(2)
    expect(apiMocks.getUserBalanceHistory).toHaveBeenLastCalledWith(42, 1, 15, undefined)
    expect(wrapper.text()).toContain('$15.00')
  })

  // The reload keeps the active type filter, so an editor opened under a filtered history
  // does not silently widen the list back to every entry type. The request after the
  // refresh must carry the same filter as the filtered request before it.
  it('keeps the selected type filter when reloading', async () => {
    const wrapper = mountDialog()
    await wrapper.setProps({ show: true })
    await flushPromises()

    await wrapper.get('[data-test="filter-balance"]').trigger('click')
    await flushPromises()
    expect(apiMocks.getUserBalanceHistory).toHaveBeenLastCalledWith(42, 1, 15, 'balance')

    await wrapper.setProps({ refreshKey: 1 })
    await flushPromises()
    expect(apiMocks.getUserBalanceHistory).toHaveBeenLastCalledWith(42, 1, 15, 'balance')
  })

  // A refresh key is also published by edits started outside the history dialog. The
  // closed dialog must ignore it and leave the request for the next opening.
  it('ignores the refresh key while the dialog is closed', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    expect(apiMocks.getUserBalanceHistory).not.toHaveBeenCalled()

    await wrapper.setProps({ refreshKey: 1 })
    await flushPromises()
    expect(apiMocks.getUserBalanceHistory).not.toHaveBeenCalled()

    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(apiMocks.getUserBalanceHistory).toHaveBeenCalledTimes(1)
  })

  // The parent can select another account while the dialog stays open. The dialog must
  // request that account's history and discard a slower response for the previous one.
  it('replaces the history when the selected account changes while open', async () => {
    const firstResponse = createDeferredResponse()
    apiMocks.getUserBalanceHistory
      .mockReturnValueOnce(firstResponse.promise)
      .mockResolvedValueOnce(createHistoryPage(1, 15, 'second-account-entry'))

    const wrapper = mountDialog()
    await wrapper.setProps({ show: true })
    await wrapper.setProps({ user: { ...historyUser, id: 43, email: 'other@example.com' } })
    await flushPromises()

    expect(apiMocks.getUserBalanceHistory).toHaveBeenLastCalledWith(43, 1, 15, undefined)
    expect(wrapper.text()).toContain('second-account-entry')

    firstResponse.resolve(createHistoryPage(1, 7.5, 'first-account-entry'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('first-account-entry')
  })

  // Closing the dialog during a request must invalidate it. Reopening requests the history
  // again, renders that response, and then the discarded response arrives late.
  it('discards an in-flight response that lands after the dialog reopens', async () => {
    const pendingResponse = createDeferredResponse()
    apiMocks.getUserBalanceHistory
      .mockReturnValueOnce(pendingResponse.promise)
      .mockResolvedValueOnce(createHistoryPage(1, 15, 'reopened-entry'))

    const wrapper = mountDialog()
    await wrapper.setProps({ show: true })
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(apiMocks.getUserBalanceHistory).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('reopened-entry')

    pendingResponse.resolve(createHistoryPage(1, 7.5, 'closed-entry'))
    await flushPromises()

    expect(wrapper.text()).toContain('reopened-entry')
    expect(wrapper.text()).not.toContain('closed-entry')
  })

  // A different account selected while the dialog is closed stays unloaded until the next
  // opening. The previous ledger must already be gone when that opening starts loading, and
  // a failed request must not bring it back under the new account.
  it('clears the previous ledger when the account switches while closed', async () => {
    const secondResponse = createDeferredResponse()
    apiMocks.getUserBalanceHistory
      .mockResolvedValueOnce(createHistoryPage(1, 7.5, 'first-account-entry'))
      .mockReturnValueOnce(secondResponse.promise)

    const wrapper = mountDialog()
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.text()).toContain('first-account-entry')

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ user: { ...historyUser, id: 43, email: 'other@example.com' } })
    await wrapper.setProps({ show: true })

    expect(apiMocks.getUserBalanceHistory).toHaveBeenLastCalledWith(43, 1, 15, undefined)
    expect(wrapper.text()).not.toContain('first-account-entry')
    expect(wrapper.text()).not.toContain('$7.50')

    secondResponse.reject(new Error('history request failed'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('first-account-entry')
  })

  // The opening request can still be pending when an edit refresh starts a second one.
  // The later response must win, so a slower first response cannot restore the pre-edit
  // rows and totals.
  it('ignores a late response from an earlier request', async () => {
    const firstResponse = createDeferredResponse()
    const secondResponse = createDeferredResponse()
    apiMocks.getUserBalanceHistory
      .mockReturnValueOnce(firstResponse.promise)
      .mockReturnValueOnce(secondResponse.promise)

    const wrapper = mountDialog()
    await wrapper.setProps({ show: true })
    await wrapper.setProps({ refreshKey: 1 })

    secondResponse.resolve(createHistoryPage(2, 15, 'fresh-entry'))
    await flushPromises()
    expect(wrapper.text()).toContain('$15.00')
    expect(wrapper.text()).toContain('fresh-entry')

    firstResponse.resolve(createHistoryPage(1, 7.5, 'stale-entry'))
    await flushPromises()
    expect(wrapper.text()).toContain('$15.00')
    expect(wrapper.text()).toContain('fresh-entry')
    expect(wrapper.text()).not.toContain('stale-entry')
  })
})

// Plugin debits share monetary formatting and expose their source through notes.
// The administrator dialog labels the adjustment without presenting its internal receipt code.
it('renders plugin debit history with its source and monetary value', async () => {
  const page = createHistoryPage(1, 7.5, '[test.reward] Feature purchase')
  page.items[0]!.type = 'plugin_balance'
  page.items[0]!.value = -1.25
  apiMocks.getUserBalanceHistory.mockResolvedValue(page)
  const wrapper = mountDialog()
  await wrapper.setProps({ show: true })
  await flushPromises()
  expect(wrapper.text()).toContain('redeem.balanceDeductedPlugin')
  expect(wrapper.text()).toContain('redeem.pluginAdjustment')
  expect(wrapper.text()).toContain('[test.reward] Feature purchase')
  expect(wrapper.text()).toContain('-$1.25')
  expect(wrapper.text()).not.toContain('code-1')
  expect(wrapper.find('balance-icon-stub').exists()).toBe(true)
  wrapper.unmount()
})
