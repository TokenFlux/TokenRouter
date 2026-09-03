import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

import type { ApiKey } from '@/types'
import KeysView from '../KeysView.vue'

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
  getDataSharingNotice,
  confirmDataSharingNotice,
  createKey,
  updateKey,
  toggleStatus,
  replaceRoute,
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
  getDataSharingNotice: vi.fn(),
  confirmDataSharingNotice: vi.fn(),
  createKey: vi.fn(),
  updateKey: vi.fn(),
  toggleStatus: vi.fn(),
  replaceRoute: vi.fn(),
}))

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
  'keys.currentConcurrency': 'Concurrency',
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
  dataSharingAPI: {
    getNotice: getDataSharingNotice,
    confirmNotice: confirmDataSharingNotice,
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
    formatBalanceAmount: (value: number | null | undefined, options?: { fractionDigits?: number }) =>
      `$${Number(value ?? 0).toFixed(options?.fractionDigits ?? 2)}`,
  }),
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: { scope: 'personal' } }),
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

const AppLayoutStub = {
  template: '<div><slot /></div>',
}

const BaseDialogStub = {
  name: 'BaseDialog',
  props: ['show', 'title', 'width'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

const TablePageLayoutStub = {
  template: `
    <div>
      <slot name="filters" />
      <slot name="table" />
      <slot name="pagination" />
    </div>
  `,
}

const DataTableStub = {
  name: 'DataTable',
  props: ['columns', 'data', 'loading'],
  emits: ['sort'],
  template: `
    <div>
      <div data-test="columns">{{ columns.map((col) => col.key).join(',') }}</div>
      <div data-test="columns-meta">{{ JSON.stringify(columns.map((col) => ({ key: col.key, sortable: !!col.sortable }))) }}</div>
      <button data-test="sort-current-concurrency" @click="$emit('sort', 'current_concurrency', 'asc')">
        Sort Concurrency
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
        <div data-test="current-concurrency">
          <slot name="cell-current_concurrency" :value="row.current_concurrency" :row="row" />
        </div>
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

const SelectStub = {
  name: 'Select',
  props: ['modelValue', 'options', 'disabled'],
  emits: ['update:modelValue', 'change'],
  template: '<button type="button" :disabled="disabled" @click="$emit(\'update:modelValue\', options?.[0]?.value ?? null)"></button>',
}

const SearchInputStub = {
  name: 'SearchInput',
  props: ['modelValue'],
  emits: ['update:modelValue', 'search'],
  template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
}

const ToggleStub = {
  name: 'Toggle',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: '<button type="button" @click="$emit(\'update:modelValue\', !modelValue)"></button>',
}

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

const IconStub = {
  props: ['name'],
  template: '<span data-test="icon">{{ name }}</span>',
}

const GroupOptionItemStub = {
  name: 'GroupOptionItem',
  inheritAttrs: false,
  props: ['name', 'capacity'],
  template: '<span data-test="group-option-item" :data-has-capacity="capacity ? \'true\' : \'false\'">{{ name }}</span>',
}

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

const visibleColumnKeys = (wrapper: VueWrapper) =>
  wrapper.get('[data-test="columns"]').text().split(',').filter(Boolean)

const visibleColumnMeta = (wrapper: VueWrapper): Array<{ key: string; sortable: boolean }> =>
  JSON.parse(wrapper.get('[data-test="columns-meta"]').text())

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
    getDataSharingNotice.mockReset()
    confirmDataSharingNotice.mockReset()
    createKey.mockReset()
    updateKey.mockReset()
    toggleStatus.mockReset()
    replaceRoute.mockReset()

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

  it('keeps the table loading until the first API key request completes', async () => {
    let resolvePublicSettings!: (settings: Record<string, never>) => void
    getPublicSettings.mockReturnValueOnce(new Promise((resolve) => {
      resolvePublicSettings = resolve
    }))

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

  it('uses the default API key columns with low-frequency columns hidden', async () => {
    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).toEqual([
      'name',
      'key',
      'group',
      'current_concurrency',
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

  it('omits the inline selection hint from the group table cell', async () => {
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="group"]').text()).not.toContain('Select a group')
  })

  it('places the key scope dropdown after column settings', async () => {
    const wrapper = await mountView()

    const refreshButton = wrapper.get('button[title="Refresh"]').element
    const columnButton = wrapper.get('button[title="Column Settings"]').element
    const scopeTrigger = wrapper.get('[data-test="scope-dropdown-trigger"]').element

    expect(refreshButton.nextElementSibling?.contains(columnButton)).toBe(true)
    expect(columnButton.parentElement?.nextElementSibling?.contains(scopeTrigger)).toBe(true)
    expect(wrapper.find('.mb-6').exists()).toBe(false)
  })

  it('keeps only edit, status and more as primary row actions', async () => {
    const wrapper = await mountView()

    const actionButtons = wrapper.get('[data-test="actions"]').findAll('button')
    expect(actionButtons).toHaveLength(3)
    expect(actionButtons.some((button) => button.text().includes('Edit'))).toBe(true)
    expect(actionButtons.some((button) => button.text().includes('Disable'))).toBe(true)
    expect(actionButtons.some((button) => button.text().includes('More'))).toBe(true)
  })

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

    expect(wrapper.get('[data-test="tf-cli-dialog-stub"]').attributes('data-key-name')).toBe('test-key')
  })

  it('shows a hidden column when toggled and persists the preference', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Rate Limit').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('rate_limit')
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['id', 'last_used_at', 'last_used_ip'])
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  it('shows the API key ID column when toggled', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'ID').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('id')
    expect(wrapper.get('[data-test="key-id"]').text()).toBe('#1')
    expect(visibleColumnMeta(wrapper).find((column) => column.key === 'id')?.sortable).toBe(true)
  })

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

  it('restores column preferences from localStorage on mount', async () => {
    localStorage.setItem('api-key-hidden-columns', JSON.stringify(['group', 'created_at']))
    localStorage.setItem('api-key-column-settings-version', '1')

    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).toEqual([
      'name',
      'key',
      'current_concurrency',
      'usage',
      'rate_limit',
      'expires_at',
      'status',
      'last_used_at',
      'actions',
    ])
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['group', 'created_at', 'last_used_ip', 'id'])
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  it('treats an invalid stored column version as the oldest version', async () => {
    localStorage.setItem('api-key-hidden-columns', JSON.stringify(['group']))
    localStorage.setItem('api-key-column-settings-version', 'invalid')

    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).not.toContain('last_used_ip')
    expect(visibleColumnKeys(wrapper)).not.toContain('id')
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['group', 'last_used_ip', 'id'])
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  it('does not include always-visible columns in the toggleable menu', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await nextTick()

    const columnMenuText = wrapper.text()
    expect(columnMenuText).toContain('API Key')
    expect(columnMenuText).toContain('ID')
    expect(columnMenuText).toContain('Concurrency')
    expect(columnMenuText).toContain('Rate Limit')
    expect(columnMenuText).toContain('Last Used IP')
    expect(columnMenuText).not.toContain('Name')
    expect(columnMenuText).not.toContain('Actions')
  })

  it('renders the current concurrency value', async () => {
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="current-concurrency"]').text()).toBe('3')
  })

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

  it('marks owner-disabled team keys and prevents members from enabling them', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{
        ...createApiKey(),
        status: 'disabled',
        scope: 'team',
        team_id: 1,
        group_id: 42,
        team_owner_disabled: true,
      }],
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

  it('marks current concurrency as sortable', async () => {
    const wrapper = await mountView()

    const currentConcurrencyColumn = visibleColumnMeta(wrapper).find(
      (column) => column.key === 'current_concurrency'
    )
    expect(currentConcurrencyColumn?.sortable).toBe(true)
  })

  it('keeps the create key form shrinkable on narrow screens', async () => {
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await nextTick()

    const dialog = wrapper.findComponent({ name: 'BaseDialog' })
    const form = wrapper.get('form#key-form')

    expect(dialog.props('width')).toBe('normal')
    expect(form.classes()).toEqual(expect.arrayContaining(['min-w-0', 'max-w-full']))
  })

  it('用户侧分组选择器不展示或投影容量数据', async () => {
    getAvailableGroups.mockResolvedValueOnce([{
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
      data_sharing_enabled: false,
      capacity: {
        concurrency_used: 25,
        concurrency_max: 144,
        sessions_used: 0,
        sessions_max: 0,
        rpm_used: 0,
        rpm_max: 0,
      },
    }])
    const wrapper = await mountView()

    await wrapper.get('[title="keys.clickToChangeGroup"]').trigger('click')
    await nextTick()
    expect(wrapper.get('[data-test="group-option-item"]').attributes('data-has-capacity')).toBe('false')

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await nextTick()
    const groupSelect = wrapper.findAllComponents({ name: 'Select' }).find(
      (select) => select.attributes('data-tour') === 'key-form-group'
    )
    expect(groupSelect?.props('options')[0]).not.toHaveProperty('capacity')
  })

  it('指定订阅后仅保留套餐允许的表单分组', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), group_id: 43 }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getAvailableGroups.mockImplementation((_scope, subscriptionID?: number) => Promise.resolve(
      subscriptionID === 71
        ? [{ id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1, data_sharing_enabled: false }]
        : [
            { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1, data_sharing_enabled: false },
            { id: 43, name: 'Claude', platform: 'anthropic', rate_multiplier: 1, data_sharing_enabled: false },
          ]
    ))
    getBillingOptions.mockResolvedValue([{
      id: 71,
      plan_name: 'Restricted',
      groups_restricted: true,
      applicable_groups: [42],
    }])
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Edit').trigger('click')
    await flushPromises()

    const billingMode = wrapper.findAllComponents({ name: 'Select' }).find(
      (select) => select.attributes('data-test') === 'api-key-billing-mode'
    )
    await billingMode!.vm.$emit('update:modelValue', 'subscription')
    await billingMode!.vm.$emit('change', 'subscription')
    await nextTick()

    const preferredSubscription = wrapper.findAllComponents({ name: 'Select' }).find(
      (select) => select.attributes('data-test') === 'api-key-preferred-subscription'
    )
    await preferredSubscription!.vm.$emit('update:modelValue', 71)
    await preferredSubscription!.vm.$emit('change', 71)
    await flushPromises()

    const groupSelect = wrapper.findAllComponents({ name: 'Select' }).find(
      (select) => select.attributes('data-tour') === 'key-form-group'
    )
    expect(groupSelect?.props('modelValue')).toBeNull()
    expect(groupSelect?.props('options')).toEqual(expect.arrayContaining([
      expect.objectContaining({ value: 42 }),
    ]))
    expect(groupSelect?.props('options')).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ value: 43 }),
    ]))
    expect(getAvailableGroups).toHaveBeenCalledWith('personal', 71)
  })

  it('keeps filters and selected page size when sorting by current concurrency', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 42, name: 'OpenAI' }])
    const wrapper = await mountView()

    await wrapper.get('[data-test="page-size-50"]').trigger('click')
    await flushPromises()

    await wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('update:modelValue', 'target')
    await wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('search')
    await flushPromises()

    const selects = wrapper.findAllComponents({ name: 'Select' })
    await selects[0].vm.$emit('update:modelValue', 42)
    await flushPromises()
    await selects[1].vm.$emit('update:modelValue', 'active')
    await flushPromises()

    listKeys.mockClear()

    await wrapper.get('[data-test="sort-current-concurrency"]').trigger('click')
    await flushPromises()

    expect(listKeys).toHaveBeenLastCalledWith(
      1,
      50,
      {
        search: 'target',
        status: 'active',
        group_id: 42,
        scope: 'personal',
        sort_by: 'current_concurrency',
        sort_order: 'asc',
      },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
  })

  it('submits the selected Fast mode policy when creating a key', async () => {
    getAvailableGroups.mockResolvedValueOnce([{
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
      data_sharing_enabled: false,
    }])
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await nextTick()
    await wrapper.get('[data-tour="key-form-name"]').setValue('fast-key')

    const selects = wrapper.findAllComponents({ name: 'Select' })
    const groupSelect = selects.find((select) => select.attributes('data-tour') === 'key-form-group')
    const fastSelect = selects.find((select) => select.attributes('data-test') === 'fast-mode-policy-select')
    expect(groupSelect).toBeDefined()
    expect(fastSelect).toBeDefined()
    await groupSelect!.vm.$emit('update:modelValue', 42)
    await fastSelect!.vm.$emit('update:modelValue', 'force_on')
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).toHaveBeenCalledWith(expect.objectContaining({
      name: 'fast-key',
      group_id: 42,
      fast_mode_policy: 'force_on',
    }))
  })

  it('shows the localized API key limit error with a rejected create request', async () => {
    getAvailableGroups.mockResolvedValueOnce([{
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
      data_sharing_enabled: false,
    }])
    createKey.mockRejectedValueOnce({
      reason: 'API_KEY_LIMIT_REACHED',
      metadata: { current: '2', limit: '2' },
    })
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await nextTick()
    await wrapper.get('[data-tour="key-form-name"]').setValue('limited-key')
    const groupSelect = wrapper.findAllComponents({ name: 'Select' })
      .find((select) => select.attributes('data-tour') === 'key-form-group')
    expect(groupSelect).toBeDefined()
    await groupSelect!.vm.$emit('update:modelValue', 42)
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('API key limit reached')
  })

  it('creates a composite key with ordered group prefix mappings', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1, data_sharing_enabled: false },
      { id: 43, name: 'Claude', platform: 'anthropic', rate_multiplier: 1, data_sharing_enabled: false },
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

    expect(createKey).toHaveBeenCalledWith(expect.objectContaining({
      name: 'composite-key',
      group_id: undefined,
      is_composite: true,
      composite_groups: [
        { group_id: 42, prefix: 'GPT' },
        { group_id: 43, prefix: 'Claude' },
      ],
    }))
  })

  it('shows composite mappings in the group column and opens full editing', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{
        ...createApiKey(),
        is_composite: true,
        composite_groups: [
          { group_id: 42, prefix: 'GPT', group: { id: 42, name: 'OpenAI', platform: 'openai' } },
          { group_id: 43, prefix: 'Claude', group: { id: 43, name: 'Claude', platform: 'anthropic' } },
        ],
      }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="group"]').text()).toContain('GPT')
    expect(wrapper.get('[data-test="group"]').text()).toContain('Claude')
    expect(wrapper.get('[data-test="composite-group-summary"]').classes()).toContain('max-w-[22rem]')
    await wrapper.get('[data-test="group"] button').trigger('click')
    await nextTick()

    expect(wrapper.get('[data-test="composite-group-editor"]').findAll('input')).toHaveLength(2)
  })

  it('blocks case-insensitive duplicate composite prefixes', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1, data_sharing_enabled: false },
      { id: 43, name: 'Claude', platform: 'anthropic', rate_multiplier: 1, data_sharing_enabled: false },
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

  it('blocks duplicate groups in composite mappings', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1, data_sharing_enabled: false },
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

  it('uses one data sharing confirmation for every new composite mapping', async () => {
    vi.useFakeTimers()
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1, data_sharing_enabled: true },
      { id: 43, name: 'Claude', platform: 'anthropic', rate_multiplier: 1, data_sharing_enabled: true },
    ])
    getDataSharingNotice.mockResolvedValue({ version: 7, content: 'notice' })
    confirmDataSharingNotice.mockResolvedValue(undefined)
    const wrapper = await mountView()

    try {
      await getButtonByText(wrapper, 'Create API Key').trigger('click')
      await wrapper.get('[data-tour="key-form-name"]').setValue('sharing-composite')
      await wrapper.get('[data-test="composite-key-toggle"]').trigger('click')
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

      expect(getDataSharingNotice).toHaveBeenCalledTimes(1)
      expect(getDataSharingNotice).toHaveBeenCalledWith(42)
      expect(createKey).not.toHaveBeenCalled()

      vi.advanceTimersByTime(10_000)
      await nextTick()
      await getButtonByText(wrapper, '我已阅读并确认').trigger('click')
      await flushPromises()

      expect(confirmDataSharingNotice).toHaveBeenCalledWith(42, 7)
      expect(createKey).toHaveBeenCalledWith(expect.objectContaining({
        is_composite: true,
        composite_groups: [
          { group_id: 42, prefix: 'GPT' },
          { group_id: 43, prefix: 'Claude' },
        ],
        data_sharing_confirmed: true,
        data_sharing_notice_version: 7,
      }))
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  it('requires an explicit target group when converting composite to ordinary', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1, data_sharing_enabled: false },
    ])
    listKeys.mockResolvedValueOnce({
      items: [{
        ...createApiKey(),
        group_id: null,
        is_composite: true,
        composite_groups: [
          { group_id: 42, prefix: 'GPT', group: { id: 42, name: 'OpenAI', platform: 'openai' } },
        ],
      }],
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

    const groupSelect = wrapper.findAllComponents({ name: 'Select' }).find(
      (select) => select.attributes('data-tour') === 'key-form-group'
    )
    expect(groupSelect).toBeDefined()
    await groupSelect!.vm.$emit('update:modelValue', 42)
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(updateKey).toHaveBeenCalledWith(1, expect.objectContaining({
      group_id: 42,
      is_composite: false,
      composite_groups: undefined,
    }))
  })

  it('creates a key with a trimmed model redirect rule', async () => {
    getAvailableGroups.mockResolvedValueOnce([
      { id: 42, name: 'OpenAI', platform: 'openai', rate_multiplier: 1, data_sharing_enabled: false },
    ])
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('redirect-key')
    const groupSelect = wrapper.findAllComponents({ name: 'Select' }).find(
      (select) => select.attributes('data-tour') === 'key-form-group'
    )
    await groupSelect!.vm.$emit('update:modelValue', 42)
    await wrapper.get('[data-test="model-mapping-add"]').trigger('click')
    await wrapper.get('[data-test="model-mapping-source-0"]').setValue(' codex-auto-review ')
    await wrapper.get('[data-test="model-mapping-target-0"]').setValue(' gpt-5.6-luna ')
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).toHaveBeenCalledWith(expect.objectContaining({
      model_mapping: { 'codex-auto-review': 'gpt-5.6-luna' },
    }))
  })

  it('loads and clears model redirect rules while editing', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{
        ...createApiKey(),
        group_id: 42,
        model_mapping: { 'codex-auto-review': 'gpt-5.6-luna' },
      }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Edit').trigger('click')
    expect((wrapper.get('[data-test="model-mapping-source-0"]').element as HTMLInputElement).value)
      .toBe('codex-auto-review')
    await wrapper.get('[data-test="model-mapping-remove-0"]').trigger('click')
    await wrapper.get('form#key-form').trigger('submit')
    await flushPromises()

    expect(updateKey).toHaveBeenCalledWith(1, expect.objectContaining({ model_mapping: {} }))
  })

  it('validates duplicate and wildcard model redirect sources in real time', async () => {
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'Create API Key').trigger('click')
    await wrapper.get('[data-test="model-mapping-add"]').trigger('click')
    await wrapper.get('[data-test="model-mapping-source-0"]').setValue('bad*source')
    await wrapper.get('[data-test="model-mapping-target-0"]').setValue('target')
    expect(wrapper.get('[role="alert"]').text()).toBe('Invalid source wildcard')

    await wrapper.get('[data-test="model-mapping-source-0"]').setValue('alias')
    await wrapper.get('[data-test="model-mapping-add"]').trigger('click')
    await wrapper.get('[data-test="model-mapping-source-1"]').setValue(' alias ')
    await wrapper.get('[data-test="model-mapping-target-1"]').setValue('target-2')
    expect(wrapper.findAll('[role="alert"]').some((error) => error.text() === 'Duplicate source model')).toBe(true)

    await wrapper.get('form#key-form').trigger('submit')
    expect(showError).toHaveBeenCalledWith('Duplicate source model')
    expect(createKey).not.toHaveBeenCalled()
  })
})
