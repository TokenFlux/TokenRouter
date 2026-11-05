import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { AdminUser } from '@/api/admin/users.types'
import UsersView from '../UsersView.vue'

// UsersView.spec.ts checks administrator user-table columns, saved preferences,
// ordering, and bulk selection across pages. Mocked list and secondary-data endpoints
// exercise the production view, while slot-rendering table stubs expose selected
// identifiers and row order for assertions after asynchronous refreshes.

// Hoisted primary and secondary endpoint mocks let tests control user pages, groups,
// custom attributes, and asynchronously loaded usage values.
const {
  listUsers,
  getAllGroups,
  getBatchUsersUsage,
  listEnabledDefinitions,
  getBatchUserAttributes,
} = vi.hoisted(() => ({
  listUsers: vi.fn(),
  getAllGroups: vi.fn(),
  getBatchUsersUsage: vi.fn(),
  listEnabledDefinitions: vi.fn(),
  getBatchUserAttributes: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      list: listUsers,
      toggleStatus: vi.fn(),
      delete: vi.fn(),
    },
    groups: {
      getAll: getAllGroups,
    },
    dashboard: {
      getBatchUsersUsage,
    },
    userAttributes: {
      listEnabledDefinitions,
      getBatchUserAttributes,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
  }),
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

// createAdminUser builds an active user fixture and applies supplied field overrides.
// Each call creates its own default arrays, letting tests vary identifiers, activity
// dates, and email without sharing those defaults.
const createAdminUser = (overrides: Partial<AdminUser> = {}): AdminUser => ({
  id: 42,
  username: 'scoped-user',
  email: 'scoped@example.com',
  role: 'user',
  balance: 0,
  concurrency: 1,
  status: 'active',
  allowed_groups: [],
  balance_notify_enabled: false,
  balance_notify_threshold: null,
  balance_notify_extra_emails: [],
  created_at: '2026-04-17T00:00:00Z',
  updated_at: '2026-04-17T00:00:00Z',
  notes: '',
  last_active_at: '2026-04-16T02:00:00Z',
  last_used_at: '2026-04-17T02:00:00Z',
  current_concurrency: 0,
  ...overrides,
})

// mountUsersView mounts the user page with observable table and bulk-dialog stubs.
// Other account dialogs are stubbed so the tests focus on listing, column preferences,
// usage sorting, and selected identifiers.
const mountUsersView = () =>
  mount(UsersView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template:
            '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
        },
        DataTable: DataTableStub,
        Pagination: true,
        ConfirmDialog: true,
        EmptyState: true,
        GroupBadge: true,
        Select: true,
        UserAttributesConfigModal: true,
        UserConcurrencyCell: true,
        PlatformUsageBreakdown: {
          props: ['today', 'total', 'byPlatform'],
          template: '<div data-test="usage-cell">{{ total }}</div>',
        },
        PlatformCostCell: {
          props: ['usage'],
          template: '<div data-test="platform-cost">{{ usage?.total_actual_cost ?? "-" }}</div>',
        },
        UserCreateModal: true,
        UserEditModal: true,
        BulkEditUserModal: BulkEditUserModalStub,
        UserPlatformQuotaModal: true,
        UserApiKeysModal: true,
        UserAllowedGroupsModal: true,
        UserBalanceModal: UserBalanceModalStub,
        UserBalanceHistoryModal: UserBalanceHistoryModalStub,
        GroupReplaceModal: true,
        Icon: true,
        Teleport: true,
      },
    },
  })

// DataTableStub exposes visible columns, row order, and selected identifiers while
// forwarding header, cell, sort, and selection interactions.
const DataTableStub = {
  // columns and data expose parent rendering, while selectedKeys supplies cross-page
  // selection; emitted updates let tests observe the parent's retained identifier
  // set.
  props: ['columns', 'data', 'selectedKeys'],
  // sort carries column and direction, while update:selectedKeys replaces the
  // selected identifiers held by the production page.
  emits: ['sort', 'update:selectedKeys'],
  template: `
    <div>
      <div data-test="columns">{{ columns.map(col => col.key).join(',') }}</div>
      <div data-test="row-order">{{ data.map(row => row.email).join(',') }}</div>
      <div data-test="selected-keys">{{ (selectedKeys || []).join(',') }}</div>
      <button data-test="sort-last-used" @click="$emit('sort', 'last_used_at', 'desc')">sort</button>
      <button
        v-for="row in data"
        :key="'select-' + row.id"
        :data-test="'select-' + row.id"
        @click="$emit('update:selectedKeys', Array.from(new Set([...(selectedKeys || []), row.id])))"
      >
        select
      </button>
      <template v-for="col in columns" :key="col.key">
        <slot :name="'header-' + col.key" :column="col" />
      </template>
      <div v-for="row in data" :key="row.id">
        <div data-test="row-email">{{ row.email }}</div>
        <slot name="cell-last_used_at" :value="row.last_used_at" :row="row" />
        <slot v-for="column in columns" :name="'cell-' + column.key" :value="row[column.key]" :row="row" />
      </div>
    </div>
  `,
}

// PaginationStub emits a page-two request so bulk-selection tests can observe
// identifiers surviving a change of visible rows.
const PaginationStub = {
  // update:page requests page two from the parent so the test can retain identifiers
  // while the visible user changes.
  emits: ['update:page'],
  template: '<button data-test="next-page" @click="$emit(\'update:page\', 2)">next</button>',
}

// UserBalanceModalStub exposes the layer and account requested for the balance editor,
// and can report a close request or a completed edit so tests can check stacking and the
// history refresh path.
const UserBalanceModalStub = {
  // show, user, and zIndex expose the parent's visibility state, target account, and
  // requested modal layer.
  props: ['show', 'user', 'zIndex'],
  // close lets the test dismiss the editor, while success reports an adjusted account.
  emits: ['close', 'success'],
  template: `
    <div v-if="show" data-test="balance-editor" :data-z-index="zIndex">
      <button data-test="balance-editor-close" @click="$emit('close')">close</button>
      <button
        data-test="balance-editor-success"
        @click="$emit('success', { id: user.id, balance: 77.5 })"
      >
        success
      </button>
    </div>
  `,
}

// UserBalanceHistoryModalStub forwards the deposit and withdrawal actions and exposes the
// account and refresh key received from the parent page.
const UserBalanceHistoryModalStub = {
  // show, user, and refreshKey expose the history surface state and parent updates.
  props: ['show', 'user', 'refreshKey'],
  // The parent listens for close, deposit, and withdraw from the history dialog.
  emits: ['close', 'deposit', 'withdraw'],
  template: `
    <div v-if="show" data-test="balance-history" :data-refresh-key="refreshKey">
      <span data-test="history-email">{{ user?.email ?? '' }}</span>
      <span data-test="history-balance">{{ user?.balance ?? '' }}</span>
      <button data-test="history-deposit" @click="$emit('deposit')">deposit</button>
      <button data-test="history-withdraw" @click="$emit('withdraw')">withdraw</button>
    </div>
  `,
}

// BulkEditUserModalStub exposes selected identifiers and emits success, allowing the
// parent view refresh and selection cleanup to be asserted.
const BulkEditUserModalStub = {
  // show controls fixture visibility and selectedIds exposes the parent's retained
  // selection so the bulk operation can be asserted across pages.
  props: ['show', 'selectedIds'],
  // success reports the selected count to exercise the parent's refresh and selection
  // clearing after a completed bulk operation.
  emits: ['close', 'success'],
  template: `
    <div v-if="show" data-test="bulk-modal">
      <span data-test="bulk-modal-ids">{{ selectedIds.join(',') }}</span>
      <button data-test="bulk-success" @click="$emit('success', selectedIds.length)">success</button>
    </div>
  `,
}

describe('admin UsersView', () => {
  beforeEach(() => {
    vi.useRealTimers()
    localStorage.clear()

    listUsers.mockReset()
    getAllGroups.mockReset()
    getBatchUsersUsage.mockReset()
    listEnabledDefinitions.mockReset()
    getBatchUserAttributes.mockReset()

    listUsers.mockResolvedValue({
      items: [createAdminUser()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getAllGroups.mockResolvedValue([])
    getBatchUsersUsage.mockResolvedValue({ stats: {} })
    listEnabledDefinitions.mockResolvedValue([])
    getBatchUserAttributes.mockResolvedValue({ values: {} })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  // The personal-rate column stays visible by default and leaves inherited cells empty.
  // Both directions reach server sorting, and saved sort preferences survive remounting.
  it('displays nullable personal rates and requests server sorting', async () => {
    listUsers.mockResolvedValue({
      items: [
        createAdminUser({ id: 1, balance_recharge_multiplier: 18 }),
        createAdminUser({ id: 2, balance_recharge_multiplier: null }),
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = mountUsersView()
    await flushPromises()
    const table = wrapper.findComponent(DataTableStub)
    const columns = table.props('columns')
    const index = columns.findIndex((column: { key: string }) => column.key === 'balance')
    expect(columns[index + 1]).toMatchObject({ key: 'balance_recharge_multiplier', sortable: true })
    const rows = wrapper.findAll('[data-test="row-email"]')
    expect(rows[0].element.parentElement?.textContent).toContain('18.00')
    expect(rows[1].element.parentElement?.textContent).not.toContain('18.00')
    for (const direction of ['asc', 'desc']) {
      table.vm.$emit('sort', 'balance_recharge_multiplier', direction)
      await flushPromises()
      expect(listUsers).toHaveBeenLastCalledWith(
        1,
        20,
        expect.objectContaining({ sort_by: 'balance_recharge_multiplier', sort_order: direction }),
        expect.anything(),
      )
    }
    wrapper.unmount()
    localStorage.setItem(
      'admin-users-table-sort',
      JSON.stringify({ key: 'balance_recharge_multiplier', order: 'asc' }),
    )
    const restored = mountUsersView()
    await flushPromises()
    expect(listUsers).toHaveBeenLastCalledWith(
      1,
      20,
      expect.objectContaining({ sort_by: 'balance_recharge_multiplier', sort_order: 'asc' }),
      expect.anything(),
    )
    restored.unmount()
  })

  // The user page loads a fixture with active and used timestamps. Activity columns
  // must appear in the expected order, and selecting last-used sorting must reach the
  // server query.
  it('shows active, used, and created activity columns in order and requests last_used_at sort', async () => {
    const wrapper = mountUsersView()

    await flushPromises()

    const columns = wrapper.get('[data-test="columns"]').text()
    const visibleColumns = columns.split(',')
    expect(visibleColumns.slice(-4, -1)).toEqual(['last_active_at', 'last_used_at', 'created_at'])
    expect(visibleColumns).not.toContain('last_login_at')

    await wrapper.get('[data-test="sort-last-used"]').trigger('click')
    await flushPromises()

    expect(listUsers).toHaveBeenLastCalledWith(
      1,
      20,
      expect.objectContaining({
        sort_by: 'last_used_at',
        sort_order: 'desc',
      }),
      expect.any(Object),
    )
  })

  // Browser storage contains an older hidden-column schema and an existing preference.
  // Mounting must add the new platform usage columns to hidden settings and persist
  // those migration additions.
  it('hides new platform usage columns for existing column settings schema', async () => {
    localStorage.setItem('user-hidden-columns', JSON.stringify(['notes']))
    localStorage.setItem('user-column-settings-version', '1')

    const wrapper = mountUsersView()
    await flushPromises()

    const visibleColumns = wrapper.get('[data-test="columns"]').text().split(',')
    expect(visibleColumns).not.toContain('usage_anthropic')
    expect(visibleColumns).not.toContain('usage_openai')
    expect(visibleColumns).not.toContain('usage_gemini')
    expect(visibleColumns).not.toContain('usage_antigravity')
    expect(visibleColumns).not.toContain('usage_qoder')
    expect(JSON.parse(localStorage.getItem('user-hidden-columns') || '[]')).toEqual(
      expect.arrayContaining([
        'usage_anthropic',
        'usage_openai',
        'usage_gemini',
        'usage_antigravity',
        'usage_qoder',
      ]),
    )
  })

  // A platform usage column is visible with saved descending cost sorting. After
  // deferred usage arrives, the current page must reorder by platform cost while
  // retaining both loaded user identities.
  it('sorts current page by platform usage after enabling a platform column', async () => {
    const highUsageUser = createAdminUser()
    highUsageUser.id = 42
    highUsageUser.email = 'high@example.com'
    const lowUsageUser = createAdminUser()
    lowUsageUser.id = 7
    lowUsageUser.email = 'low@example.com'

    localStorage.setItem(
      'user-hidden-columns',
      JSON.stringify([
        'notes',
        'groups',
        'subscriptions',
        'usage',
        'concurrency',
        'usage_openai',
        'usage_gemini',
        'usage_antigravity',
        'usage_qoder',
      ]),
    )
    localStorage.setItem('user-column-settings-version', '2')
    localStorage.setItem(
      'admin-users-usage-sort',
      JSON.stringify({ key: 'usage_anthropic', metric: 'total', order: 'desc' }),
    )
    listUsers.mockResolvedValue({
      items: [lowUsageUser, highUsageUser],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getBatchUsersUsage.mockResolvedValue({
      stats: {
        42: {
          user_id: 42,
          today_actual_cost: 1,
          total_actual_cost: 10,
          by_platform: [{ platform: 'anthropic', today_actual_cost: 1, total_actual_cost: 10 }],
        },
        7: {
          user_id: 7,
          today_actual_cost: 1,
          total_actual_cost: 1,
          by_platform: [{ platform: 'anthropic', today_actual_cost: 1, total_actual_cost: 1 }],
        },
      },
    })

    const wrapper = mountUsersView()
    await flushPromises()
    // Wait beyond the production fifty-millisecond secondary-data delay so row-order
    // assertions observe the usage response rather than the initial primary list.
    await new Promise((resolve) => window.setTimeout(resolve, 60))
    await flushPromises()

    expect(getBatchUsersUsage).toHaveBeenCalledWith([7, 42])
    expect(wrapper.findAll('[data-test="row-email"]').map((node) => node.text())).toEqual([
      'high@example.com',
      'low@example.com',
    ])
  })

  // Local usage sorting reorders a loaded page before a server sort is chosen.
  // Selecting last-used sorting must clear the persisted usage preference and restore
  // the returned server order.
  it('clears usage current-page sort when switching to last_used_at server sort', async () => {
    vi.useFakeTimers()
    localStorage.setItem('user-column-settings-version', '4')
    localStorage.setItem(
      'user-hidden-columns',
      JSON.stringify([
        'notes',
        'groups',
        'subscriptions',
        'concurrency',
        'usage_anthropic',
        'usage_openai',
        'usage_gemini',
        'usage_antigravity',
        'usage_qoder',
        'balance_platform_quota',
      ]),
    )

    listUsers.mockResolvedValue({
      items: [
        createAdminUser({ id: 1, email: 'last-used-first@example.com' }),
        createAdminUser({ id: 2, email: 'usage-first@example.com' }),
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getBatchUsersUsage.mockResolvedValue({
      stats: {
        1: { user_id: 1, today_actual_cost: 1, total_actual_cost: 1, by_platform: [] },
        2: { user_id: 2, today_actual_cost: 9, total_actual_cost: 9, by_platform: [] },
      },
    })

    const wrapper = mountUsersView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(50)
    await flushPromises()

    expect(wrapper.get('[data-test="row-order"]').text()).toBe(
      'last-used-first@example.com,usage-first@example.com',
    )

    await wrapper.get('[data-test="usage-sort-trigger-usage"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="usage-sort-usage-today"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="row-order"]').text()).toBe(
      'usage-first@example.com,last-used-first@example.com',
    )
    expect(localStorage.getItem('admin-users-usage-sort')).toContain('"key":"usage"')

    await wrapper.get('[data-test="sort-last-used"]').trigger('click')
    await flushPromises()

    expect(localStorage.getItem('admin-users-usage-sort')).toBeNull()
    expect(wrapper.get('[data-test="row-order"]').text()).toBe(
      'last-used-first@example.com,usage-first@example.com',
    )
    expect(listUsers).toHaveBeenLastCalledWith(
      1,
      20,
      expect.objectContaining({
        sort_by: 'last_used_at',
        sort_order: 'desc',
      }),
      expect.any(Object),
    )
  })

  // Two user pages contribute identifiers to one bulk selection. Successful bulk
  // editing must reload the visible page, clear selected identifiers, and hide the
  // bulk-action control.
  it('keeps selected user IDs across pages and clears them after a successful bulk update', async () => {
    let refreshed = false
    listUsers.mockImplementation(async (page: number) => {
      const user =
        page === 2
          ? createAdminUser({
              id: 43,
              email: refreshed ? 'refreshed-page-two@example.com' : 'page-two@example.com',
            })
          : createAdminUser({ id: 42, email: 'page-one@example.com' })
      return {
        items: [user],
        total: 2,
        page,
        page_size: 20,
        pages: 2,
      }
    })

    const wrapper = mount(UsersView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: {
            template:
              '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
          },
          DataTable: DataTableStub,
          Pagination: PaginationStub,
          ConfirmDialog: true,
          EmptyState: true,
          GroupBadge: true,
          Select: true,
          UserAttributesConfigModal: true,
          UserConcurrencyCell: true,
          UserCreateModal: true,
          UserEditModal: true,
          BulkEditUserModal: BulkEditUserModalStub,
          UserPlatformQuotaModal: true,
          UserApiKeysModal: true,
          UserAllowedGroupsModal: true,
          UserBalanceModal: true,
          UserBalanceHistoryModal: true,
          GroupReplaceModal: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.find('[data-test="bulk-edit-limits"]').exists()).toBe(false)
    await wrapper.get('[data-test="select-42"]').trigger('click')
    expect(wrapper.get('[data-test="selected-keys"]').text()).toBe('42')
    expect(wrapper.find('[data-test="bulk-edit-limits"]').exists()).toBe(true)

    await wrapper.get('[data-test="next-page"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="selected-keys"]').text()).toBe('42')

    await wrapper.get('[data-test="select-43"]').trigger('click')
    expect(wrapper.get('[data-test="selected-keys"]').text()).toBe('42,43')

    await wrapper.get('[data-test="bulk-edit-limits"]').trigger('click')
    expect(wrapper.get('[data-test="bulk-modal-ids"]').text()).toBe('42,43')

    const callsBeforeSuccess = listUsers.mock.calls.length
    refreshed = true
    await wrapper.get('[data-test="bulk-success"]').trigger('click')
    await flushPromises()

    expect(listUsers.mock.calls.length).toBeGreaterThan(callsBeforeSuccess)
    expect(wrapper.get('[data-test="row-order"]').text()).toBe('refreshed-page-two@example.com')
    expect(wrapper.find('[data-test="bulk-edit-limits"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="selected-keys"]').text()).toBe('')
  })

  // The balance cell opens the history dialog, and that dialog can open the deposit
  // editor. The editor must request the nested modal layer in that path so it renders
  // above the history surface, while the row deposit action keeps the shared layer.
  it('opens deposit from balance history above the history dialog', async () => {
    const wrapper = mountUsersView()
    await flushPromises()

    // The row deposit action works without an open history dialog.
    const depositButton = wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.users.deposit')
    expect(depositButton).toBeDefined()
    await depositButton!.trigger('click')
    expect(wrapper.get('[data-test="balance-editor"]').attributes('data-z-index')).toBe('50')

    await wrapper.get('[data-test="balance-editor-close"]').trigger('click')
    expect(wrapper.find('[data-test="balance-editor"]').exists()).toBe(false)

    // The formatted balance opens the history dialog, whose deposit action opens the
    // editor one layer higher.
    const historyTrigger = wrapper.findAll('button').find((button) => button.text() === '$0.00')
    expect(historyTrigger).toBeDefined()
    await historyTrigger!.trigger('click')
    expect(wrapper.find('[data-test="balance-history"]').exists()).toBe(true)

    await wrapper.get('[data-test="history-deposit"]').trigger('click')
    expect(wrapper.get('[data-test="balance-editor"]').attributes('data-z-index')).toBe('60')
  })

  // A deposit completed from the history dialog returns the server-updated account. The
  // page must publish that balance to the open history header, advance its refresh key,
  // and still refresh the user table even though the history dialog opened the editor.
  it('refreshes the open balance history after a deposit from it', async () => {
    const wrapper = mountUsersView()
    await flushPromises()

    const historyTrigger = wrapper.findAll('button').find((button) => button.text() === '$0.00')
    await historyTrigger!.trigger('click')
    expect(wrapper.get('[data-test="history-balance"]').text()).toBe('0')

    const callsBeforeEdit = listUsers.mock.calls.length
    await wrapper.get('[data-test="history-deposit"]').trigger('click')
    await wrapper.get('[data-test="balance-editor-success"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="history-balance"]').text()).toBe('77.5')
    // The editor reports only the fields it changed; the page keeps the rest of the row.
    expect(wrapper.get('[data-test="history-email"]').text()).toBe('scoped@example.com')
    expect(wrapper.get('[data-test="balance-history"]').attributes('data-refresh-key')).toBe('1')
    expect(listUsers.mock.calls.length).toBeGreaterThan(callsBeforeEdit)
  })
})
