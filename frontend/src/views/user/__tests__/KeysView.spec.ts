import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

import type { ApiKey } from '@/api/keys.types'
import KeysView from '../KeysView.vue'

// KeysView.spec.ts exercises the production key-management page with controlled API
// responses and slot-preserving table and dialog substitutes. Tests cover persisted columns,
// separate list and usage loading, owner restrictions, rotation, billing-group choices,
// composite mappings, model redirects, and create or update payloads.

// Hoisted API and action spies share request state with the production page, while each case
// supplies exact records and deferred promises for its behavior.
const {
  listKeys,
  getPublicSettings,
  getDashboardApiKeysUsage,
  getAvailableGroups,
  getUserGroupRates,
  getBillingOptions,
  showError,
  showSuccess,
  copyToClipboard,
  isCurrentStep,
  nextStep,
  createKey,
  updateKey,
  rotateKey,
  toggleStatus,
  replaceRoute,
  routeQuery,
} = vi.hoisted(() => ({
  listKeys: vi.fn(),
  getPublicSettings: vi.fn(),
  getDashboardApiKeysUsage: vi.fn(),
  getAvailableGroups: vi.fn(),
  getUserGroupRates: vi.fn(),
  getBillingOptions: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  copyToClipboard: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
  createKey: vi.fn(),
  updateKey: vi.fn(),
  rotateKey: vi.fn(),
  toggleStatus: vi.fn(),
  replaceRoute: vi.fn(),
  routeQuery: {} as Record<string, string>, // Query of the mocked current route.
}))

// messages gives stable readable labels to table and form controls so tests can invoke user
// actions and inspect localized errors through the page.
const messages: Record<string, string> = {
  'common.actions': 'Actions',
  'common.name': 'Name',
  'common.refresh': 'Refresh',
  'common.status': 'Status',
  'common.edit': 'Edit',
  'common.more': 'More',
  'keys.apiKey': 'API Key',
  'keys.allGroups': 'All Groups',
  'keys.allStatus': 'All Status',
  'keys.columnSettings': 'Column Settings',
  'keys.createKey': 'Create API Key',
  'keys.disable': 'Disable',
  'keys.enable': 'Enable',
  'keys.importToTf': 'Import to TF CLI',
  'keys.apiKeyLimitReached': 'API key limit reached',
  'keys.created': 'Created',
  'keys.expiresAt': 'Expires',
  'keys.group': 'Group',
  'keys.groupRequired': 'Group required',
  'keys.selectGroup': 'Select a group',
  'keys.composite.label': 'Composite key',
  'keys.composite.hint': 'Choose a group with a prefix/model ID.',
  'keys.composite.addMapping': 'Add group mapping',
  'keys.composite.editMappings': 'Edit composite key mappings',
  'keys.composite.prefixPlaceholder': 'For example, GPT',
  'keys.composite.groupRequired': 'Select a group',
  'keys.composite.prefixRequired': 'Enter a prefix',
  'keys.composite.prefixInvalid': 'Invalid prefix',
  'keys.composite.prefixDuplicate': 'Duplicate prefix',
  'keys.composite.groupDuplicate': 'Duplicate group',
  'keys.composite.mappingRequired': 'Mapping required',
  'keys.composite.tooManyMappings': 'Too many mappings',
  'keys.modelRedirect.label': 'Model redirects',
  'keys.modelRedirect.hint': 'Redirect models',
  'keys.modelRedirect.addRule': 'Add rule',
  'keys.modelRedirect.empty': 'No redirect rules',
  'keys.modelRedirect.source': 'Source model',
  'keys.modelRedirect.target': 'Target model',
  'keys.modelRedirect.sourcePlaceholder': 'Source model',
  'keys.modelRedirect.targetPlaceholder': 'Target model',
  'keys.modelRedirect.sourceRequired': 'Source required',
  'keys.modelRedirect.targetRequired': 'Target required',
  'keys.modelRedirect.nameTooLong': 'Model name too long',
  'keys.modelRedirect.sourceWildcardInvalid': 'Invalid source wildcard',
  'keys.modelRedirect.targetWildcardInvalid': 'Invalid target wildcard',
  'keys.modelRedirect.selfMapping': 'Source and target must differ',
  'keys.modelRedirect.duplicateSource': 'Duplicate source model',
  'keys.modelRedirect.tooManyRules': 'Too many redirect rules',
  'keys.id': 'ID',
  'keys.lastUsedAt': 'Last Used',
  'keys.lastUsedIP': 'Last Used IP',
  'keys.rateLimitColumn': 'Rate Limit',
  'keys.searchPlaceholder': 'Search name or key...',
  'keys.status.active': 'Active',
  'keys.status.disabled': 'Disabled',
  'keys.status.team_owner_disabled': 'Disabled by team admin',
  'keys.status.expired': 'Expired',
  'keys.status.inactive': 'Inactive',
  'keys.status.quota_exhausted': 'Quota exhausted',
  'keys.usage': 'Usage',
  'keys.today': 'Today',
  'keys.total': 'Last 30d',
  'keys.teamOwnerLocked': 'Admin locked',
  'keys.teamOwnerDisabledHint': 'Only the team administrator can enable this key again.',
  'team.personalKeys': 'Personal keys',
  'team.teamKeys': 'Team keys',
  'team.scopeSwitch': 'Switch key scope',
}

vi.mock('@/api', () => ({
  keysAPI: {
    list: listKeys,
    create: vi.fn(),
    createWithPayload: createKey,
    update: updateKey,
    rotate: rotateKey,
    delete: vi.fn(),
    toggleStatus,
    getBillingOptions,
  },
  authAPI: {
    getPublicSettings,
  },
  usageAPI: {
    getDashboardApiKeysUsage,
  },
  userGroupsAPI: {
    getAvailable: getAvailableGroups,
    getUserGroupRates,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep,
    nextStep,
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard,
  }),
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitName: { value: 'USD' },
    balanceUnitSymbol: { value: '$' },
    formatBalanceAmount: (
      value: number | null | undefined,
      options?: { fractionDigits?: number /* Requested displayed decimal precision. */ },
    ) => `$${Number(value ?? 0).toFixed(options?.fractionDigits ?? 2)}`,
  }),
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: routeQuery }),
  useRouter: () => ({ replace: replaceRoute }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

// createApiKey returns an active unrestricted key with distinct concurrency and identity
// fields. Cases override status, bindings, limits, or secrets while retaining the required
// API record shape.
const createApiKey = (): ApiKey => ({
  id: 1,
  user_id: 1,
  key: 'sk-test-key',
  name: 'test-key',
  group_id: null,
  status: 'active',
  fast_mode_policy: 'follow_request',
  model_mapping: {},
  ip_whitelist: [],
  ip_blacklist: [],
  last_used_at: null,
  last_used_ip: null,
  quota: 0,
  quota_used: 0,
  expires_at: null,
  created_at: '2026-06-27T00:00:00Z',
  updated_at: '2026-06-27T00:00:00Z',
  current_concurrency: 3,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  usage_5h: 0,
  usage_1d: 0,
  usage_7d: 0,
  window_5h_start: null,
  window_1d_start: null,
  window_7d_start: null,
  reset_5h_at: null,
  reset_1d_at: null,
  reset_7d_at: null,
})

// AppLayoutStub retains page content while TablePageLayoutStub exposes named filters, table,
// and pagination slots needed for key-page interactions.
const AppLayoutStub = {
  template: '<div><slot /></div>',
}

// BaseDialogStub retains visibility and body/footer content so real form handlers execute
// through the production page's dialog props.
const BaseDialogStub = {
  name: 'BaseDialog',
  props: ['show', 'title', 'width'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

// TablePageLayoutStub exposes the page-owned toolbar, table, and pagination content
// independently from the application layout implementation.
const TablePageLayoutStub = {
  template: `
    <div>
      <slot name="filters" />
      <slot name="table" />
      <slot name="pagination" />
    </div>
  `,
}

// DataTableStub exposes column metadata, sort events, and relevant row slots so assertions
// exercise page-owned cell actions and visibility preferences.
const DataTableStub = {
  name: 'DataTable',
  props: ['columns', 'data', 'loading'],
  emits: ['sort'],
  template: `
    <div>
      <div data-test="columns">{{ columns.map((col) => col.key).join(',') }}</div>
      <div data-test="columns-meta">{{ JSON.stringify(columns.map((col) => ({ key: col.key, sortable: !!col.sortable }))) }}</div>
      <button data-test="sort-expires-at" @click="$emit('sort', 'expires_at', 'asc')">
        Sort Expires At
      </button>
      <div v-if="loading" data-test="table-loading">Loading</div>
      <div v-for="row in data" :key="row.id">
        <div
          v-if="columns.some((col) => col.key === 'id')"
          data-test="key-id"
        >
          <slot name="cell-id" :value="row.id" :row="row" />
        </div>
        <slot name="cell-name" :value="row.name" :row="row" />
        <div data-test="group"><slot name="cell-group" :value="row.group" :row="row" /></div>
        <div data-test="usage"><slot name="cell-usage" :row="row" /></div>
        <div data-test="status">
          <slot name="cell-status" :value="row.status" :row="row" />
        </div>
        <div data-test="actions">
          <slot name="cell-actions" :value="row" :row="row" />
        </div>
        <div
          v-if="columns.some((column) => column.key === 'last_used_ip')"
          data-test="last-used-ip"
        >
          <slot name="cell-last_used_ip" :value="row.last_used_ip" :row="row" />
        </div>
      </div>
      <div v-if="!loading && data.length === 0" data-test="table-empty"><slot name="empty" /></div>
    </div>
  `,
}

// SelectStub supplies model updates from available options while exposing choice props for
// tests that need exact group or billing selection.
const SelectStub = {
  name: 'Select',
  props: ['modelValue', 'options', 'disabled'],
  emits: ['update:modelValue', 'change'],
  template:
    '<button type="button" :disabled="disabled" @click="$emit(\'update:modelValue\', options?.[0]?.value ?? null)"></button>',
}

// SearchInputStub exposes model and search events so list queries can retain a chosen search
// term while sorting or resizing pages.
const SearchInputStub = {
  name: 'SearchInput',
  props: ['modelValue'],
  emits: ['update:modelValue', 'search'],
  template:
    '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
}

// ToggleStub flips the supplied boolean model through its update event, exercising the
// page's status and composite-mode handlers.
const ToggleStub = {
  name: 'Toggle',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: '<button type="button" @click="$emit(\'update:modelValue\', !modelValue)"></button>',
}

// PaginationStub emits a fixed page-size choice while retaining pagination props so sorting
// scenarios can verify the selected request size.
const PaginationStub = {
  name: 'Pagination',
  props: ['page', 'total', 'pageSize'],
  emits: ['update:page', 'update:pageSize'],
  template: `
    <div>
      <button data-test="page-size-50" @click="$emit('update:pageSize', 50)">50</button>
    </div>
  `,
}

// IconStub exposes requested icon names as text so page controls remain identifiable without
// installing icon interaction resources in these cases.
const IconStub = {
  props: ['name'],
  template: '<span data-test="icon">{{ name }}</span>',
}

// GroupOptionItemStub exposes whether capacity data reaches the user group row while
// rendering its name for ordinary selection assertions.
const GroupOptionItemStub = {
  name: 'GroupOptionItem',
  inheritAttrs: false,
  props: ['name', 'capacity'],
  template:
    '<span data-test="group-option-item" :data-has-capacity="capacity ? \'true\' : \'false\'">{{ name }}</span>',
}

// TfCliImportDialogStub exposes the selected key name when visible so overflow-menu tests
// verify page-to-dialog identity without starting local discovery.
const TfCliImportDialogStub = {
  name: 'TfCliImportDialog',
  props: ['show', 'apiKey'],
  emits: ['close'],
  template: `
    <div
      v-if="show"
      data-test="tf-cli-dialog-stub"
      :data-key-name="apiKey?.name"
    />
  `,
}

// mountView renders KeysView with inspectable shared-control substitutes and optionally
// settles initial promises. Passing false retains pending setup for loading-state
// assertions; callers unmount when resource disposal matters to their scenario.
const mountView = async (settle = true) => {
  const wrapper = mount(KeysView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: PaginationStub,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: SelectStub,
        Toggle: ToggleStub,
        SearchInput: SearchInputStub,
        Icon: IconStub,
        UseKeyModal: true,
        TfCliImportDialog: TfCliImportDialogStub,
        EndpointPopover: true,
        GroupBadge: true,
        GroupOptionItem: GroupOptionItemStub,
        Teleport: true,
      },
    },
  })
  if (settle) {
    await flushPromises()
    await nextTick()
  }
  return wrapper
}

// visibleColumnKeys reads the current ordered column identities exposed by the table stub.
const visibleColumnKeys = (wrapper: VueWrapper) =>
  wrapper.get('[data-test="columns"]').text().split(',').filter(Boolean)

// visibleColumnMeta reads the table stub's serialized column identities and sortable flags.
const visibleColumnMeta = (
  wrapper: VueWrapper,
): Array<{
  key: string // Table column field identity.
  sortable: boolean // Column supports sorting flag.
}> => JSON.parse(wrapper.get('[data-test="columns-meta"]').text())

// getButtonByText finds a button containing the supplied visible label. It throws when
// absent so a scenario fails at its missing action rather than continuing with an undefined
// control.
const getButtonByText = (wrapper: VueWrapper, text: string) => {
  const button = wrapper.findAll('button').find((item) => item.text().includes(text))
  if (!button) {
    throw new Error(`Button not found: ${text}`)
  }
  return button
}

describe('user KeysView column settings', () => {
  beforeEach(() => {
    localStorage.clear()

    listKeys.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    getBillingOptions.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()
    createKey.mockReset()
    updateKey.mockReset()
    rotateKey.mockReset()
    toggleStatus.mockReset()
    replaceRoute.mockReset()
    for (const key of Object.keys(routeQuery)) delete routeQuery[key]
    routeQuery.scope = 'personal'

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
    getBillingOptions.mockResolvedValue([])
    isCurrentStep.mockReturnValue(false)
    createKey.mockResolvedValue(createApiKey())
    updateKey.mockResolvedValue(createApiKey())
  })

  // The usage endpoint returns different daily and thirty-day charged costs. The test
  // protects the matching interval labels and four-decimal balance formatting in each key
  // row.
  it('显示今日和近 30 天的实际用量，保留四位小数', async () => {
    getDashboardApiKeysUsage.mockResolvedValueOnce({
      stats: { 1: { api_key_id: 1, today_actual_cost: 1.2345, total_actual_cost: 67.8912 } },
    })
    const wrapper = await mountView()

    const usage = wrapper.get('[data-test="usage"]').text()
    expect(usage).toContain('Today:$1.2345')
    expect(usage).toContain('Last 30d:$67.8912')
    wrapper.unmount()
  })

  // Initial public settings remain pending before the first key request can begin. The test
  // protects the table loading state until setup and list retrieval produce real rows.
  it('keeps the table loading until the first API key request completes', async () => {
    let resolvePublicSettings!: (settings: Record<string, never>) => void
    getPublicSettings.mockReturnValueOnce(
      new Promise((resolve) => {
        resolvePublicSettings = resolve
      }),
    )

    const wrapper = await mountView(false)
    await nextTick()

    expect(wrapper.find('[data-test="table-loading"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="table-empty"]').exists()).toBe(false)
    expect(listKeys).not.toHaveBeenCalled()

    resolvePublicSettings({})
    await flushPromises()
    await nextTick()

    expect(wrapper.find('[data-test="table-loading"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="table-empty"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="status"]').exists()).toBe(true)
  })

  // No column preferences exist when the key page mounts. The test protects the default
  // visible column order and hides the low-frequency fields until selected.
  it('uses the default API key columns with low-frequency columns hidden', async () => {
    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).toEqual([
      'name',
      'key',
      'group',
      'usage',
      'expires_at',
      'status',
      'created_at',
      'actions',
    ])
    expect(visibleColumnKeys(wrapper)).not.toContain('rate_limit')
    expect(visibleColumnKeys(wrapper)).not.toContain('last_used_at')
    expect(visibleColumnKeys(wrapper)).not.toContain('last_used_ip')
    expect(visibleColumnKeys(wrapper)).not.toContain('id')
  })

  // An ordinary row has no selected routing group. The test protects the compact group cell
  // by excluding the form-specific group-selection hint from its inline display.
  it('omits the inline selection hint from the group table cell', async () => {
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="group"]').text()).not.toContain('Select a group')
  })

  // The key toolbar renders refresh, column settings, and ownership controls. The test
  // protects their adjacent order and absence of an extra separated scope row.
  it('places the key scope dropdown after column settings', async () => {
    const wrapper = await mountView()

    const refreshButton = wrapper.get('button[title="Refresh"]').element
    const columnButton = wrapper.get('button[title="Column Settings"]').element
    const scopeTrigger = wrapper.get('[data-test="scope-dropdown-trigger"]').element

    expect(refreshButton.nextElementSibling?.contains(columnButton)).toBe(true)
    expect(columnButton.parentElement?.nextElementSibling?.contains(scopeTrigger)).toBe(true)
    expect(wrapper.find('.mb-6').exists()).toBe(false)
  })

  // An ordinary active key appears in the table. The test protects three primary row actions
  // while less frequent operations remain available through the overflow menu.
  it('keeps only edit, status and more as primary row actions', async () => {
    const wrapper = await mountView()

    const actionButtons = wrapper.get('[data-test="actions"]').findAll('button')
    expect(actionButtons).toHaveLength(3)
    expect(actionButtons.some((button) => button.text().includes('Edit'))).toBe(true)
    expect(actionButtons.some((button) => button.text().includes('Disable'))).toBe(true)
    expect(actionButtons.some((button) => button.text().includes('More'))).toBe(true)
  })

  // The user opens the overflow menu and selects TokenFlux CLI import. The test protects
  // expanded-state accessibility and the key identity passed into the import dialog.
  it('opens tf import from the More menu without a fragment session', async () => {
    const wrapper = await mountView()

    const moreButton = getButtonByText(wrapper, 'More')
    expect(moreButton.attributes('aria-expanded')).toBe('false')
    await moreButton.trigger('click')
    await nextTick()
    expect(moreButton.attributes('aria-expanded')).toBe('true')
    expect(moreButton.attributes('aria-controls')).toBe('key-action-menu-1')
    await getButtonByText(wrapper, 'Import to TF CLI').trigger('click')
    await nextTick()

    expect(wrapper.get('[data-test="tf-cli-dialog-stub"]').attributes('data-key-name')).toBe(
      'test-key',
    )
  })

  // The user requests rotation and then cancels its confirmation. The test protects the
  // confirmation requirement and checks that no secret-rotation request is issued.
  it('轮换先确认，取消时不发送请求', async () => {
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'More').trigger('click')
    await getButtonByText(wrapper, 'keys.rotateKey').trigger('click')
    const dialog = wrapper
      .findAllComponents({ name: 'ConfirmDialog' })
      .find((component) => component.props('title') === 'keys.rotateKey')!

    expect(dialog.props('show')).toBe(true)
    expect(rotateKey).not.toHaveBeenCalled()
    dialog.vm.$emit('cancel')
    await nextTick()
    expect(dialog.props('show')).toBe(false)
    expect(rotateKey).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  // Rotation remains pending while confirmation is repeated and cancellation is attempted.
  // The test protects one mutation and publication of the returned secret even when list
  // refresh fails.
  it('确认后只提交一次，成功展示并复制新凭据', async () => {
    const updated = { ...createApiKey(), key: 'sk-rotated-key' }
    let resolveRotation!: (key: ApiKey) => void
    rotateKey.mockReturnValue(
      new Promise<ApiKey>((resolve) => {
        resolveRotation = resolve
      }),
    )
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'More').trigger('click')
    await getButtonByText(wrapper, 'keys.rotateKey').trigger('click')
    const dialog = wrapper
      .findAllComponents({ name: 'ConfirmDialog' })
      .find((component) => component.props('title') === 'keys.rotateKey')!

    dialog.vm.$emit('confirm')
    dialog.vm.$emit('confirm')
    dialog.vm.$emit('cancel')
    await nextTick()
    expect(rotateKey).toHaveBeenCalledTimes(1)
    expect(rotateKey).toHaveBeenCalledWith(1)
    expect(dialog.props('loading')).toBe(true)
    expect(dialog.props('show')).toBe(true)

    listKeys.mockRejectedValueOnce(new Error('refresh failed'))
    resolveRotation(updated)
    await flushPromises()
    expect(dialog.props('show')).toBe(false)
    expect(wrapper.get('code').text()).toBe(updated.key)
    const table = wrapper.findComponent({ name: 'DataTable' })
    expect(table.props('data')[0].key).toBe(updated.key)
    await getButtonByText(wrapper, 'common.copy').trigger('click')
    expect(copyToClipboard).toHaveBeenCalledWith(updated.key, expect.any(String))
    wrapper.unmount()
  })

  // The first rotation request fails and the user confirms again. The test protects the
  // retained target and old credential before a successful retry displays the replacement
  // secret.
  it('轮换失败保留确认窗和原凭据，允许重试', async () => {
    rotateKey.mockRejectedValueOnce(new Error('rotation failed'))
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'More').trigger('click')
    await getButtonByText(wrapper, 'keys.rotateKey').trigger('click')
    const dialog = wrapper
      .findAllComponents({ name: 'ConfirmDialog' })
      .find((component) => component.props('title') === 'keys.rotateKey')!
    dialog.vm.$emit('confirm')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('rotation failed')
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('loading')).toBe(false)
    expect(wrapper.findComponent({ name: 'DataTable' }).props('data')[0].key).toBe('sk-test-key')
    rotateKey.mockResolvedValueOnce({ ...createApiKey(), key: 'sk-retry-key' })
    dialog.vm.$emit('confirm')
    await flushPromises()
    expect(rotateKey).toHaveBeenCalledTimes(2)
    expect(wrapper.get('code').text()).toBe('sk-retry-key')
    wrapper.unmount()
  })

  // The user enables a default-hidden spending-limit column. The test protects immediate
  // visibility and persistence of the remaining hidden set with the current settings
  // version.
  it('shows a hidden column when toggled and persists the preference', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Rate Limit').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('rate_limit')
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['id', 'last_used_at', 'last_used_ip']),
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  // The user enables the hidden key-ID column through settings. The test protects its
  // visible identifier text and sortable metadata passed to the table.
  it('shows the API key ID column when toggled', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'ID').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('id')
    expect(wrapper.get('[data-test="key-id"]').text()).toBe('#1')
    expect(visibleColumnMeta(wrapper).find((column) => column.key === 'id')?.sortable).toBe(true)
  })

  // The user enables the hidden last-used-IP column for a recorded client address. The test
  // protects both column visibility and the actual address shown in its cell.
  it('shows the last used IP column when toggled', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), last_used_ip: '203.0.113.10' }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Last Used IP').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('last_used_ip')
    expect(wrapper.get('[data-test="last-used-ip"]').text()).toBe('203.0.113.10')
  })

  // An older saved preference hides existing columns before newer default-hidden columns
  // were introduced. The test protects versioned migration while retaining the user's prior
  // choices.
  it('restores column preferences from localStorage on mount', async () => {
    localStorage.setItem('api-key-hidden-columns', JSON.stringify(['group', 'created_at']))
    localStorage.setItem('api-key-column-settings-version', '1')

    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).toEqual([
      'name',
      'key',
      'usage',
      'rate_limit',
      'expires_at',
      'status',
      'last_used_at',
      'actions',
    ])
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['group', 'created_at', 'last_used_ip', 'id']),
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  // The stored preference version is invalid while the hidden-column set is usable. The test
  // protects oldest-version migration and persistence of the resulting current version.
  it('treats an invalid stored column version as the oldest version', async () => {
    localStorage.setItem('api-key-hidden-columns', JSON.stringify(['group']))
    localStorage.setItem('api-key-column-settings-version', 'invalid')

    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).not.toContain('last_used_ip')
    expect(visibleColumnKeys(wrapper)).not.toContain('id')
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['group', 'last_used_ip', 'id']),
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  // The user opens column settings for an ordinary key list. The test protects configurable
  // optional fields while keeping mandatory name and action columns outside that menu.
  it('does not include always-visible columns in the toggleable menu', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await nextTick()

    const columnMenuText = wrapper.text()
    expect(columnMenuText).toContain('API Key')
    expect(columnMenuText).toContain('ID')
    expect(columnMenuText).toContain('Rate Limit')
    expect(columnMenuText).toContain('Last Used IP')
    expect(columnMenuText).not.toContain('Name')
    expect(columnMenuText).not.toContain('Actions')
  })

  // A team key arrives with the server disabled status. The test protects its localized
  // label and prevents a raw translation key from appearing in the status cell.
  it('renders a localized disabled status for team keys', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), status: 'disabled', scope: 'team', team_id: 1 }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const wrapper = await mountView()

    expect(wrapper.get('[data-test="status"]').text()).toBe('Disabled')
    expect(wrapper.text()).not.toContain('keys.status.disabled')
  })

  // A team administrator has locked a disabled key owned by a member. The test protects
  // editable unrelated fields while hiding reactivation and omitting status from update
  // payloads.
  it('marks owner-disabled team keys and prevents members from enabling them', async () => {
    listKeys.mockResolvedValueOnce({
      items: [
        {
          ...createApiKey(),
          status: 'disabled',
          scope: 'team',
          team_id: 1,
          group_id: 42,
          team_owner_disabled: true,
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const wrapper = await mountView()

    expect(wrapper.get('[data-test="status"]').text()).toContain('Disabled by team admin')
    expect(wrapper.get('[data-test="actions"]').text()).toContain('Admin locked')
    expect(getButtonByText(wrapper, 'Edit').exists()).toBe(true)
    expect(wrapper.findAll('button').some((button) => button.text() === 'Enable')).toBe(false)

    await getButtonByText(wrapper, 'Edit').trigger('click')
    await nextTick()

    expect(wrapper.get('[data-test="key-status-toggle"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('Only the team administrator can enable this key again.')

    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(toggleStatus).not.toHaveBeenCalled()
    expect(updateKey).toHaveBeenCalledTimes(1)
    expect(updateKey.mock.calls[0][1]).not.toHaveProperty('status')
  })

  // The user changes an ordinary active key through its edit status switch. The test
  // protects submission of the inactive value with the selected key identity.
  it('updates the key status through the edit switch', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), group_id: 42 }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Edit').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="key-status-toggle"]').trigger('click')
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(updateKey).toHaveBeenCalledWith(1, expect.objectContaining({ status: 'inactive' }))
  })

  // A key has active requests and no finite concurrency cap. The test protects its compact
  // name-adjacent count and unlimited hint without introducing a separate concurrency
  // column.
  it('shows in-flight concurrency next to the key name', async () => {
    const wrapper = await mountView()

    const indicator = wrapper.get('[data-test="key-concurrency"]')
    expect(indicator.text()).toBe('3')
    expect(indicator.attributes('title')).toBe('keys.concurrencyInUseUnlimited')
    expect(visibleColumnKeys(wrapper)).not.toContain('current_concurrency')
  })

  // A key uses four of five allowed concurrent requests. The test protects the current-to-
  // limit label and amber near-capacity styling for that occupancy.
  it('shows the concurrency limit and warns when the key is nearly full', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), current_concurrency: 4, concurrency_limit: 5 }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    const indicator = wrapper.get('[data-test="key-concurrency"]')
    expect(indicator.text().replace(/\s+/g, '')).toBe('4/5')
    expect(indicator.attributes('title')).toBe('keys.concurrencyInUse')
    expect(indicator.classes()).toContain('text-amber-600')
  })

  // A key has a finite concurrency cap but no active requests. The test protects omission of
  // the idle concurrency indicator beside its name.
  it('hides the concurrency indicator for idle keys', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), current_concurrency: 0, concurrency_limit: 5 }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    expect(wrapper.find('[data-test="key-concurrency"]').exists()).toBe(false)
  })

  // The global search "Create API key" action opens the page with create=1. The page opens
  // the create dialog after loading and removes only that parameter from the URL, so a
  // reload does not reopen the dialog.
  it('opens the create dialog from the create query parameter', async () => {
    routeQuery.create = '1'
    const wrapper = await mountView()

    expect(wrapper.find('form#key-form').exists()).toBe(true)
    expect(replaceRoute).toHaveBeenCalledWith({ query: { scope: 'personal' } })
  })

  // The user opens the key creation dialog in the production page. The test protects normal
  // dialog sizing and shrinkable form classes required by narrow layouts.
  it('keeps the create key form shrinkable on narrow screens', async () => {
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await nextTick()

    const dialog = wrapper.findComponent({ name: 'BaseDialog' })
    const form = wrapper.get('form#key-form')

    expect(dialog.props('width')).toBe('normal')
    expect(form.classes()).toEqual(expect.arrayContaining(['min-w-0', 'max-w-full']))
  })

  // A routing group supplies both its internal name and a localized display name. The test
  // protects use of the display name in creation choices.
  it('分组选择器优先显示当前语言的展示名称', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 42, name: 'business-group', display_name: 'English group', rate_multiplier: 1 },
    ])
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await nextTick()
    const select = wrapper
      .findAllComponents({ name: 'Select' })
      .find((item) => item.attributes('data-tour') === 'key-form-group')
    expect(select?.props('options')[0].label).toBe('English group')
  })

  // A user-visible group response also contains provider capacity observations. The test
  // protects omission of that capacity data from inline and form selector presentations.
  it('用户侧分组选择器不展示或投影容量数据', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      {
        id: 42,
        name: 'OpenAI',
        description: 'OpenAI group',
        display_brand: 'OpenAI',
        rate_multiplier: 1,
        peak_rate_enabled: false,
        peak_start: '',
        peak_end: '',
        peak_rate_multiplier: 1,
        platform: 'openai',
        capacity: {
          concurrency_used: 25,
          concurrency_max: 144,
          sessions_used: 0,
          sessions_max: 0,
          rpm_used: 0,
          rpm_max: 0,
        },
      },
    ])
    const wrapper = await mountView()

    await wrapper.get('[title="keys.clickToChangeGroup"]').trigger('click')
    await nextTick()
    expect(wrapper.get('[data-test="group-option-item"]').attributes('data-has-capacity')).toBe(
      'false',
    )

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await nextTick()
    const groupSelect = wrapper
      .findAllComponents({ name: 'Select' })
      .find((select) => select.attributes('data-tour') === 'key-form-group')
    expect(groupSelect?.props('options')[0]).not.toHaveProperty('capacity')
  })

  // An edited key switches to a subscription restricted to another group. The test protects
  // clearing the incompatible binding and requesting only the subscription-approved form
  // choices.
  it('指定订阅后仅保留套餐允许的表单分组', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), group_id: 43 }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getAvailableGroups.mockImplementation((_scope, subscriptionID?: number) =>
      Promise.resolve(
        subscriptionID === 71
          ? [{ id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 }]
          : [
              { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 },
              { id: 43, name: 'Claude', platform: 'anthropic', rate_multiplier: 1 },
            ],
      ),
    )
    getBillingOptions.mockResolvedValue([
      {
        id: 71,
        plan_name: 'Restricted',
        groups_restricted: true,
        applicable_groups: [42],
      },
    ])
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Edit').trigger('click')
    await flushPromises()

    const billingMode = wrapper
      .findAllComponents({ name: 'Select' })
      .find((select) => select.attributes('data-test') === 'api-key-billing-mode')
    await billingMode!.vm.$emit('update:modelValue', 'subscription')
    await billingMode!.vm.$emit('change', 'subscription')
    await nextTick()

    const preferredSubscription = wrapper
      .findAllComponents({ name: 'Select' })
      .find((select) => select.attributes('data-test') === 'api-key-preferred-subscription')
    await preferredSubscription!.vm.$emit('update:modelValue', 71)
    await preferredSubscription!.vm.$emit('change', 71)
    await flushPromises()

    const groupSelect = wrapper
      .findAllComponents({ name: 'Select' })
      .find((select) => select.attributes('data-tour') === 'key-form-group')
    expect(groupSelect?.props('modelValue')).toBeNull()
    expect(groupSelect?.props('options')).toEqual(
      expect.arrayContaining([expect.objectContaining({ value: 42 })]),
    )
    expect(groupSelect?.props('options')).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ value: 43 })]),
    )
    expect(getAvailableGroups).toHaveBeenCalledWith('personal', 71)
  })

  // The user changes page size and several filters before sorting by expiry. The test
  // protects a first-page sorted request that retains all selected conditions and page size.
  it('keeps filters and selected page size when sorting', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 42, name: 'OpenAI' }])
    const wrapper = await mountView()

    await wrapper.get('[data-test="page-size-50"]').trigger('click')
    await flushPromises()

    await wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('update:modelValue', 'target')
    await wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('search')
    await flushPromises()

    const filterOptions = wrapper.findAllComponents({ name: 'FilterOptions' })
    await filterOptions[0].vm.$emit('update:modelValue', 42)
    await flushPromises()
    await filterOptions[1].vm.$emit('update:modelValue', 'active')
    await flushPromises()

    listKeys.mockClear()

    await wrapper.get('[data-test="sort-expires-at"]').trigger('click')
    await flushPromises()

    expect(listKeys).toHaveBeenLastCalledWith(
      1,
      50,
      {
        search: 'target',
        status: 'active',
        group_id: 42,
        scope: 'personal',
        sort_by: 'expires_at',
        sort_order: 'asc',
      },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
  })

  // A new key selects a forced Fast policy with request-count caps. The test protects those
  // policy and numeric limit values in the submitted creation payload.
  it('submits the selected Fast mode policy when creating a key', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      {
        id: 42,
        name: 'OpenAI',
        description: '',
        display_brand: '',
        rate_multiplier: 1,
        peak_rate_enabled: false,
        peak_start: '',
        peak_end: '',
        peak_rate_multiplier: 1,
        platform: 'openai',
      },
    ])
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await nextTick()
    await wrapper.get('[data-tour="key-form-name"]').setValue('fast-key')

    const selects = wrapper.findAllComponents({ name: 'Select' })
    const groupSelect = selects.find(
      (select) => select.attributes('data-tour') === 'key-form-group',
    )
    const fastSelect = selects.find(
      (select) => select.attributes('data-test') === 'fast-mode-policy-select',
    )
    expect(groupSelect).toBeDefined()
    expect(fastSelect).toBeDefined()
    await groupSelect!.vm.$emit('update:modelValue', 42)
    await fastSelect!.vm.$emit('update:modelValue', 'force_on')
    await wrapper.get('#key-concurrency-limit').setValue('2')
    await wrapper.get('#key-rpm-limit').setValue('30')
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'fast-key',
        group_id: 42,
        fast_mode_policy: 'force_on',
        concurrency_limit: 2,
        rpm_limit: 30,
      }),
    )
  })

  // The create endpoint rejects a valid draft with an API-key-count limit reason. The test
  // protects the localized limit message rather than exposing the raw error category.
  it('shows the localized API key limit error with a rejected create request', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      {
        id: 42,
        name: 'OpenAI',
        description: '',
        display_brand: '',
        rate_multiplier: 1,
        peak_rate_enabled: false,
        peak_start: '',
        peak_end: '',
        peak_rate_multiplier: 1,
        platform: 'openai',
      },
    ])
    createKey.mockRejectedValueOnce({
      reason: 'API_KEY_LIMIT_REACHED',
      metadata: { current: '2', limit: '2' },
    })
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await nextTick()
    await wrapper.get('[data-tour="key-form-name"]').setValue('limited-key')
    const groupSelect = wrapper
      .findAllComponents({ name: 'Select' })
      .find((select) => select.attributes('data-tour') === 'key-form-group')
    expect(groupSelect).toBeDefined()
    await groupSelect!.vm.$emit('update:modelValue', 42)
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('API key limit reached')
  })

  // The user creates a composite key with two distinct prefix mappings. The test protects
  // their submitted order and suppression of an unrelated single-group assignment.
  it('creates a composite key with ordered group prefix mappings', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 },
      { id: 43, name: 'Claude', platform: 'anthropic', rate_multiplier: 1 },
    ])
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('composite-key')
    await wrapper.get('[data-test="composite-key-toggle"]').trigger('click')
    await nextTick()

    let editor = wrapper.get('[data-test="composite-group-editor"]')
    await editor.findAllComponents({ name: 'Select' })[0]!.vm.$emit('update:modelValue', 42)
    await editor.findAll('input')[0]!.setValue('GPT')
    await getButtonByText(wrapper, 'Add group mapping').trigger('click')
    await nextTick()

    editor = wrapper.get('[data-test="composite-group-editor"]')
    await editor.findAllComponents({ name: 'Select' })[1]!.vm.$emit('update:modelValue', 43)
    await editor.findAll('input')[1]!.setValue('Claude')
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'composite-key',
        group_id: undefined,
        is_composite: true,
        composite_groups: [
          { group_id: 42, prefix: 'GPT' },
          { group_id: 43, prefix: 'Claude' },
        ],
      }),
    )
  })

  // A listed key has multiple composite mappings with prefix and group metadata. The test
  // protects compact summary content and opening the complete mapping editor from that row.
  it('shows composite mappings in the group column and opens full editing', async () => {
    listKeys.mockResolvedValueOnce({
      items: [
        {
          ...createApiKey(),
          is_composite: true,
          composite_groups: [
            { group_id: 42, prefix: 'GPT', group: { id: 42, name: 'OpenAI', platform: 'openai' } },
            {
              group_id: 43,
              prefix: 'Claude',
              group: { id: 43, name: 'Claude', platform: 'anthropic' },
            },
          ],
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="group"]').text()).toContain('GPT')
    expect(wrapper.get('[data-test="group"]').text()).toContain('Claude')
    expect(wrapper.get('[data-test="composite-group-summary"]').classes()).toContain(
      'max-w-[22rem]',
    )
    await wrapper.get('[data-test="group"] button').trigger('click')
    await nextTick()

    expect(wrapper.get('[data-test="composite-group-editor"]').findAll('input')).toHaveLength(2)
  })

  // Two composite mappings use prefixes that differ only in letter case. The test protects
  // immediate duplicate-prefix feedback and blocks the create request for that ambiguous
  // routing draft.
  it('blocks case-insensitive duplicate composite prefixes', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 },
      { id: 43, name: 'Claude', platform: 'anthropic', rate_multiplier: 1 },
    ])
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await wrapper.get('[data-test="composite-key-toggle"]').trigger('click')
    let editor = wrapper.get('[data-test="composite-group-editor"]')
    await editor.findAllComponents({ name: 'Select' })[0]!.vm.$emit('update:modelValue', 42)
    await editor.findAll('input')[0]!.setValue('GPT')
    await getButtonByText(wrapper, 'Add group mapping').trigger('click')
    await nextTick()

    editor = wrapper.get('[data-test="composite-group-editor"]')
    await editor.findAllComponents({ name: 'Select' })[1]!.vm.$emit('update:modelValue', 43)
    await editor.findAll('input')[1]!.setValue('gpt')
    await wrapper.get('form#key-form').trigger('submit')
    await nextTick()

    expect(showError).toHaveBeenCalledWith('Duplicate prefix')
    expect(createKey).not.toHaveBeenCalled()
  })

  // Two composite mappings select the same routing group with different prefixes. The test
  // protects duplicate-group feedback and prevents creation of the rejected mapping set.
  it('blocks duplicate groups in composite mappings', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 },
    ])
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await wrapper.get('[data-test="composite-key-toggle"]').trigger('click')
    let editor = wrapper.get('[data-test="composite-group-editor"]')
    await editor.findAllComponents({ name: 'Select' })[0]!.vm.$emit('update:modelValue', 42)
    await editor.findAll('input')[0]!.setValue('GPT')
    await getButtonByText(wrapper, 'Add group mapping').trigger('click')
    await nextTick()

    editor = wrapper.get('[data-test="composite-group-editor"]')
    await editor.findAllComponents({ name: 'Select' })[1]!.vm.$emit('update:modelValue', 42)
    await editor.findAll('input')[1]!.setValue('Backup')
    await wrapper.get('form#key-form').trigger('submit')
    await nextTick()

    expect(showError).toHaveBeenCalledWith('Duplicate group')
    expect(createKey).not.toHaveBeenCalled()
  })

  // An existing composite key is converted to ordinary mode. The test protects required
  // explicit group selection before sending a noncomposite update with the chosen binding.
  it('requires an explicit target group when converting composite to ordinary', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 },
    ])
    listKeys.mockResolvedValueOnce({
      items: [
        {
          ...createApiKey(),
          group_id: null,
          is_composite: true,
          composite_groups: [
            { group_id: 42, prefix: 'GPT', group: { id: 42, name: 'OpenAI', platform: 'openai' } },
          ],
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Edit').trigger('click')
    await wrapper.get('[data-test="composite-key-toggle"]').trigger('click')
    await wrapper.get('form#key-form').trigger('submit')
    await nextTick()
    expect(showError).toHaveBeenCalledWith('Group required')
    expect(updateKey).not.toHaveBeenCalled()

    const groupSelect = wrapper
      .findAllComponents({ name: 'Select' })
      .find((select) => select.attributes('data-tour') === 'key-form-group')
    expect(groupSelect).toBeDefined()
    await groupSelect!.vm.$emit('update:modelValue', 42)
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(updateKey).toHaveBeenCalledWith(
      1,
      expect.objectContaining({
        group_id: 42,
        is_composite: false,
        composite_groups: undefined,
      }),
    )
  })

  // A creation draft contains surrounding whitespace in model redirect source and target
  // fields. The test protects trimming both values in the submitted mapping object.
  it('creates a key with a trimmed model redirect rule', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 },
    ])
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('redirect-key')
    const groupSelect = wrapper
      .findAllComponents({ name: 'Select' })
      .find((select) => select.attributes('data-tour') === 'key-form-group')
    await groupSelect!.vm.$emit('update:modelValue', 42)
    await wrapper.get('[data-testid="model-mapping-add"]').trigger('click')
    await wrapper.get('[data-testid="model-mapping-source-0"]').setValue(' codex-auto-review ')
    await wrapper.get('[data-testid="model-mapping-target-0"]').setValue(' gpt-5.6-luna ')
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).toHaveBeenCalledWith(
      expect.objectContaining({
        model_mapping: { 'codex-auto-review': 'gpt-5.6-luna' },
      }),
    )
  })

  // An existing key loads a persisted model redirect and the user removes its row. The test
  // protects submission of an empty mapping object to clear that restriction.
  it('loads and clears model redirect rules while editing', async () => {
    listKeys.mockResolvedValueOnce({
      items: [
        {
          ...createApiKey(),
          group_id: 42,
          model_mapping: { 'codex-auto-review': 'gpt-5.6-luna' },
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Edit').trigger('click')
    expect(
      (wrapper.get('[data-testid="model-mapping-source-0"]').element as HTMLInputElement).value,
    ).toBe('codex-auto-review')
    await wrapper.get('[data-testid="model-mapping-remove-0"]').trigger('click')
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(updateKey).toHaveBeenCalledWith(1, expect.objectContaining({ model_mapping: {} }))
  })

  // An existing key loads finite concurrency and RPM caps before the user enters zero. The
  // test protects loading those values and submitting zero to remove both caps.
  it('loads request limits and submits zero to remove them', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), group_id: 42, concurrency_limit: 3, rpm_limit: 60 }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'Edit').trigger('click')
    expect((wrapper.get('#key-concurrency-limit').element as HTMLInputElement).value).toBe('3')
    expect((wrapper.get('#key-rpm-limit').element as HTMLInputElement).value).toBe('60')
    await wrapper.get('#key-concurrency-limit').setValue('0')
    await wrapper.get('#key-rpm-limit').setValue('0')
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()
    expect(updateKey).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ concurrency_limit: 0, rpm_limit: 0 }),
    )
  })

  // The request-limit input receives negative, fractional, or oversized integer text. The
  // parameterized test protects rejection before creation and the corresponding request-
  // limit validation message.
  it.each(['-1', '1.5', '2147483648'])('rejects invalid request limit %s', async (value) => {
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await wrapper.get('#key-rpm-limit').setValue(value)
    await wrapper.get('form#key-form').trigger('submit')
    expect(createKey).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('keys.requestLimitsInvalid')
  })

  // The redirect editor receives an invalid wildcard and then duplicate trimmed source
  // names. The test protects live row errors and rejection of the duplicate mapping at
  // submit.
  it('validates duplicate and wildcard model redirect sources in real time', async () => {
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await wrapper.get('[data-testid="model-mapping-add"]').trigger('click')
    await wrapper.get('[data-testid="model-mapping-source-0"]').setValue('bad*source')
    await wrapper.get('[data-testid="model-mapping-target-0"]').setValue('target')
    expect(wrapper.get('[role="alert"]').text()).toBe('Invalid source wildcard')

    await wrapper.get('[data-testid="model-mapping-source-0"]').setValue('alias')
    await wrapper.get('[data-testid="model-mapping-add"]').trigger('click')
    await wrapper.get('[data-testid="model-mapping-source-1"]').setValue(' alias ')
    await wrapper.get('[data-testid="model-mapping-target-1"]').setValue('target-2')
    expect(
      wrapper.findAll('[role="alert"]').some((error) => error.text() === 'Duplicate source model'),
    ).toBe(true)

    await wrapper.get('form#key-form').trigger('submit')
    expect(showError).toHaveBeenCalledWith('Duplicate source model')
    expect(createKey).not.toHaveBeenCalled()
  })
})
