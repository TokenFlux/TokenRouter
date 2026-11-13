import { useProtocolCatalogFixture } from '@/__tests__/helpers/protocolCatalog'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { AdminGroup } from '@/api/groups.types'
import GroupsView from '../GroupsView.vue'

// GroupsView.columnSettings.spec.ts checks persisted table column choices and conditional group
// summary requests. Mounted components or pure helper calls exercise the real owning
// implementation, while controlled API and display dependencies expose the observable updates
// asserted by each scenario.

// Hoisted group API and notification mocks let each scenario control request outcomes before
// the mounted page imports its dependencies.
const {
  listGroups,
  getAllGroups,
  getModelsListCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  listProviders,
  showError,
  showSuccess,
  isCurrentStep,
  nextStep,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getAllGroups: vi.fn(),
  getModelsListCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  listProviders: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
}))

// messages provides the test translations used to locate rendered controls and inspect
// notification text through the mounted groups page.
const messages: Record<string, string> = {
  'admin.groups.columnSettings': 'Column Settings',
  'admin.groups.form.sortOrder': 'Manual Sort Value',
  'admin.groups.columns.name': 'Name',
  'admin.groups.columns.id': 'ID',
  'admin.groups.columns.platform': 'Platform',
  'admin.groups.columns.displayBrand': 'Brand Type',
  'admin.groups.columns.rateMultiplier': 'Rate Multiplier',
  'admin.groups.columns.exclusive': 'Exclusive',
  'admin.groups.columns.sessionIsolation': 'Session Isolation',
  'admin.groups.columns.providers': 'Providers',
  'admin.groups.columns.capacity': 'Capacity',
  'admin.groups.columns.usage': 'Usage',
  'admin.groups.columns.status': 'Status',
  'admin.groups.columns.actions': 'Actions',
}

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      getAll: getAllGroups,
      getModelsListCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      updateSortOrder: vi.fn(),
    },
    providers: {
      list: listProviders,
    },
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

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitSymbol: { value: '$' },
    formatBalanceAmount: (value: number | null | undefined) => `$${(value ?? 0).toFixed(2)}`,
  }),
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

// createGroup builds a complete group fixture with the requested field overrides.
const createGroup = (overrides: Partial<AdminGroup> = {}): AdminGroup => ({
  id: 1,
  name: 'Core Anthropic',
  description: null,
  scheduler_type: 'basic',
  display_brand: '',
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  session_isolation_enabled: false,
  status: 'active',
  allow_image_generation: false,

  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_id_on_invalid_request: null,
  unavailable_fallback_group_id: null,
  allow_messages_dispatch: false,
  allow_live: false,
  default_mapped_model: '',
  availability_probe_config: undefined,
  require_oauth_only: false,
  require_privacy_set: false,
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:00:00Z',
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: true,
  supported_model_scopes: [],
  provider_count: 3,
  active_provider_count: 2,
  rate_limited_provider_count: 1,
  models_list_config: undefined,
  sort_order: 10,
  ...overrides,
})

// The layout and table stubs expose page slots, row actions, and displayed columns so these
// tests can inspect the actual page state.
const AppLayoutStub = {
  template: '<div><slot /></div>',
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
  props: ['columns', 'data'],
  emits: ['sort'],
  template: `
    <div>
      <div data-test="columns">{{ columns.map((col) => col.key).join(',') }}</div>
      <div data-test="rows">{{ data.map((row) => row.name).join(',') }}</div>
    </div>
  `,
}

// BaseDialogStub and adjacent control stubs expose test-visible slots and events while keeping
// dialog layout outside the tested page behavior.
const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

const IconStub = {
  props: ['name'],
  template: '<span data-test="icon">{{ name }}</span>',
}

// mountView mounts the administrator page with controlled API and display dependencies. The
// returned wrapper exposes real page actions while stubs keep unrelated layout and dialog
// rendering outside the assertions.
const mountView = async () => {
  const wrapper = mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        PlatformIcon: true,
        Icon: IconStub,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: { template: '<div><slot /></div>' },
      },
    },
  })
  await flushPromises()
  return wrapper
}

// columnKeys reads the column identifiers published by the data-table test stub.
const columnKeys = (wrapper: ReturnType<typeof mount>) =>
  wrapper.get('[data-test="columns"]').text().split(',').filter(Boolean)

// openColumnSettings clicks the page control that exposes optional table column choices.
const openColumnSettings = async (wrapper: ReturnType<typeof mount>) => {
  await wrapper.get('button[title="Column Settings"]').trigger('click')
}

// clickColumnToggle finds the requested column action and clicks it. It asserts that the button
// exists and waits for queued work so the caller can inspect the resulting table and storage
// state.
const clickColumnToggle = async (wrapper: ReturnType<typeof mount>, label: string) => {
  const button = wrapper.findAll('button').find((item) => item.text().includes(label))
  expect(button, `column toggle ${label}`).toBeTruthy()
  await button!.trigger('click')
  await flushPromises()
}

describe('admin GroupsView column settings', () => {
  beforeEach(() => {
    localStorage.clear()

    listGroups.mockReset()
    getAllGroups.mockReset()
    getModelsListCandidates.mockReset()
    getUsageSummary.mockReset()
    getCapacitySummary.mockReset()
    getLiveCapability.mockReset()
    listProviders.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()

    listGroups.mockResolvedValue({
      items: [createGroup()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getAllGroups.mockResolvedValue([])
    getModelsListCandidates.mockResolvedValue([])
    getUsageSummary.mockResolvedValue([])
    getCapacitySummary.mockResolvedValue([])
    getLiveCapability.mockResolvedValue({ supported: false, reason: 'test server unsupported' })
    listProviders.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    isCurrentStep.mockReturnValue(false)
  })

  afterEach(() => {
    localStorage.clear()
  })

  // The groups page mounts with the Live capability endpoint mocked. Initialization requests
  // that capability once so repeated consumers share the page's recorded result.
  it('loads the Live capability once on mount', async () => {
    const wrapper = await mountView()

    expect(getLiveCapability).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  // The group toolbar and initial list request are inspected together. Exclusive-group
  // filtering is absent from both surfaces so the visible controls match the submitted query.
  it('omits the exclusive-group filter from the toolbar and list request', async () => {
    const wrapper = await mountView()

    // The filter panel exposes the currently supported filter fields after the toolbar button
    // is opened; status is the only one.
    await wrapper.get('button[aria-label="common.filter"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[role="dialog"] section')).toHaveLength(1)
    expect(listGroups.mock.calls[0]?.[2]).not.toHaveProperty('is_exclusive')
  })

  // A first-time administrator has no stored column preference. The table hides the ID column
  // and internal sort values according to the configured initial visibility rules.
  it('hides the id column and internal sort values by default', async () => {
    const wrapper = await mountView()

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'display_brand',
      'rate_multiplier',
      'is_exclusive',
      'session_isolation_enabled',
      'provider_count',
      'capacity',
      'usage',
      'status',
      'actions',
    ])
    expect(localStorage.getItem('group-hidden-columns')).toBe(JSON.stringify(['id']))
    expect(localStorage.getItem('group-column-settings-version')).toBe('2')
  })

  // The create form is opened from the group page. It exposes group settings while omitting a
  // manual sort-order input, leaving ordering to the dedicated sorting action.
  it('does not expose a manual sort value in the create form', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')

    expect(wrapper.text()).not.toContain('Manual Sort Value')
  })

  // Saved hidden keys include known and unknown columns. Mount restores valid optional keys and
  // ignores stale entries so unsupported columns cannot distort table visibility.
  it('applies saved hidden columns on mount and ignores unknown keys', async () => {
    localStorage.setItem(
      'group-hidden-columns',
      JSON.stringify(['usage', 'capacity', 'removed_column', 'name', 'actions']),
    )
    localStorage.setItem('group-column-settings-version', '2')

    const wrapper = await mountView()

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'id',
      'display_brand',
      'rate_multiplier',
      'is_exclusive',
      'session_isolation_enabled',
      'provider_count',
      'status',
      'actions',
    ])
  })

  // Stored column preferences predate the current settings version. Migration adds the new
  // default-hidden ID column and persists the upgraded version alongside the resulting
  // preference set.
  it('auto-hides id for existing saved column prefs after version bump', async () => {
    localStorage.setItem('group-hidden-columns', JSON.stringify(['usage']))
    // An absent preference version selects version one, which applies the migration introducing
    // version two's default-hidden column.

    const wrapper = await mountView()

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'display_brand',
      'rate_multiplier',
      'is_exclusive',
      'session_isolation_enabled',
      'provider_count',
      'capacity',
      'status',
      'actions',
    ])
    expect(JSON.parse(localStorage.getItem('group-hidden-columns')!)).toEqual(
      expect.arrayContaining(['usage', 'id']),
    )
    expect(localStorage.getItem('group-column-settings-version')).toBe('2')
  })

  // The stored column preference version is malformed. Loading treats it as the initial version
  // and applies pending default-hidden migrations so the current table reaches valid preference
  // state.
  it('treats an invalid column settings version as version 1', async () => {
    localStorage.setItem('group-hidden-columns', JSON.stringify([]))
    localStorage.setItem('group-column-settings-version', 'invalid')

    const wrapper = await mountView()

    expect(columnKeys(wrapper)).not.toContain('id')
    expect(localStorage.getItem('group-hidden-columns')).toBe(JSON.stringify(['id']))
    expect(localStorage.getItem('group-column-settings-version')).toBe('2')
  })

  // An optional column is toggled through the settings menu. The rendered table changes and
  // local storage records the updated hidden keys for the next page mount.
  it('toggles a column and persists hidden column keys', async () => {
    const wrapper = await mountView()

    await openColumnSettings(wrapper)
    await clickColumnToggle(wrapper, 'Usage')

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'display_brand',
      'rate_multiplier',
      'is_exclusive',
      'session_isolation_enabled',
      'provider_count',
      'capacity',
      'status',
      'actions',
    ])
    expect(JSON.parse(localStorage.getItem('group-hidden-columns')!)).toEqual(
      expect.arrayContaining(['id', 'usage']),
    )
  })

  // The ID column begins hidden under the default configuration. Enabling it through column
  // settings adds the column to the visible table and updates the stored preference.
  it('can show the id column from column settings', async () => {
    const wrapper = await mountView()

    await openColumnSettings(wrapper)
    await clickColumnToggle(wrapper, 'ID')

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'id',
      'display_brand',
      'rate_multiplier',
      'is_exclusive',
      'session_isolation_enabled',
      'provider_count',
      'capacity',
      'usage',
      'status',
      'actions',
    ])
    // columns defines the displayed table fields and their translated headings, ordering, and
    // sorting controls.
    const columns = wrapper.findComponent(DataTableStub).props('columns')
    expect(
      columns.find(
        (column: {
          key: string // selected property identifier
        }) => column.key === 'id',
      ),
    ).toMatchObject({
      key: 'id',
      sortable: true,
    })
    expect(localStorage.getItem('group-hidden-columns')).toBe(JSON.stringify([]))
  })

  // Usage and capacity columns begin hidden and are then revealed individually. Their summary
  // endpoints are skipped until needed, and showing each column requests its corresponding
  // data.
  it('skips hidden usage and capacity fetches until those columns are shown', async () => {
    localStorage.setItem('group-hidden-columns', JSON.stringify(['usage', 'capacity']))

    const wrapper = await mountView()

    expect(getUsageSummary).not.toHaveBeenCalled()
    expect(getCapacitySummary).not.toHaveBeenCalled()

    await openColumnSettings(wrapper)
    await clickColumnToggle(wrapper, 'Usage')
    expect(getUsageSummary).toHaveBeenCalledTimes(1)
    expect(getCapacitySummary).not.toHaveBeenCalled()

    await clickColumnToggle(wrapper, 'Capacity')
    expect(getUsageSummary).toHaveBeenCalledTimes(1)
    expect(getCapacitySummary).toHaveBeenCalledTimes(1)
  })
})

useProtocolCatalogFixture()
