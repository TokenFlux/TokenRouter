import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { ref } from 'vue'

import UsageCleanupDialog from '../UsageCleanupDialog.vue'
import { Z_INDEX } from '@/constants/overlay'
import type { UsageCleanupTask } from '@/api/admin/usage'

// UsageCleanupDialog.spec.ts verifies host date normalization, inline filter inheritance,
// unsupported-predicate warnings, nested confirmation summaries, skeleton loading,
// active-task polling, task cancellation, and structured error mapping. Mocked API clients
// isolate modal behavior from network I/O while keeping real ConfirmDialog,
// DateRangePicker, and inline UsageFilters rendering active.

// API and notification mocks capture cleanup requests and feedback messages across cases.
const mockListCleanupTasks = vi.fn()
const mockCreateCleanupTask = vi.fn()
const mockCancelCleanupTask = vi.fn()
const mockTeamsList = vi.fn()
const mockGroupsList = vi.fn()
const mockShowSuccess = vi.fn()
const mockShowError = vi.fn()

vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: {
    listCleanupTasks: (...args: unknown[]) => mockListCleanupTasks(...args),
    createCleanupTask: (...args: unknown[]) => mockCreateCleanupTask(...args),
    cancelCleanupTask: (...args: unknown[]) => mockCancelCleanupTask(...args),
  },
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    usage: {
      searchUsers: vi.fn().mockResolvedValue([]),
      searchApiKeys: vi.fn().mockResolvedValue([]),
    },
    groups: { list: (...args: unknown[]) => mockGroupsList(...args) },
    teams: { list: (...args: unknown[]) => mockTeamsList(...args) },
    providers: { list: vi.fn().mockResolvedValue({ items: [] }) },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: mockShowSuccess,
    showError: mockShowError,
  }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
      locale: ref('en'),
    }),
  }
})

// BaseDialogStub renders BaseDialog headers, slots, and modal z-index inline so tests can
// inspect main and nested confirmation dialogs without body teleportation.
const BaseDialogStub = {
  props: ['show', 'title', 'subtitle', 'width', 'zIndex'],
  emits: ['close'],
  template: `
    <div v-if="show" data-testid="dialog-shell" :data-z-index="zIndex ?? 50">
      <h3>{{ title }}</h3>
      <p v-if="subtitle">{{ subtitle }}</p>
      <slot name="header-icon" />
      <slot />
      <div v-if="$slots.footer" data-testid="dialog-footer">
        <slot name="footer" />
      </div>
    </div>
  `,
}

// SelectStub renders a minimal timezone selector trigger for unit tests.
const SelectStub = {
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: `
    <button
      type="button"
      data-testid="cleanup-timezone-select-stub"
      @click="$emit('update:modelValue', 'America/New_York')"
    >
      {{ modelValue }}
    </button>
  `,
}

// deferred creates an externally controlled promise with resolve and reject handles.
// Tests use it to hold listCleanupTasks pending while inspecting initial skeleton loading
// states before results settle.
function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve
    reject = promiseReject
  })
  return { promise, resolve, reject }
}

// buildTask constructs a persisted UsageCleanupTask fixture with sensible pending
// defaults and optional field overrides. Callers supply overrides for status, deleted
// counts, or persisted filters under test.
function buildTask(overrides: Partial<UsageCleanupTask> = {}): UsageCleanupTask {
  return {
    id: 1,
    status: 'pending',
    filters: {
      start_time: '2026-11-01T00:00:00Z',
      end_time: '2026-11-05T23:59:59Z',
    },
    created_by: 1,
    deleted_rows: 0,
    created_at: '2026-11-15T10:00:00Z',
    updated_at: '2026-11-15T10:00:00Z',
    ...overrides,
  }
}

describe('UsageCleanupDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-15T10:20:30Z'))
    vi.clearAllMocks()

    mockListCleanupTasks.mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 5,
      pages: 0,
    })
    mockTeamsList.mockResolvedValue([{ id: 7, name: 'Platform Team' }])
    mockGroupsList.mockResolvedValue({
      items: [{ id: 12, name: 'Anthropic Pro', display_brand: 'anthropic' }],
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  // Opening the cleanup dialog from a filtered usage page must normalize timestamp
  // bounds to YYYY-MM-DD, show seeded chips, and warn about unsupported page filters.
  // Confirming in the nested dialog at Z_INDEX.MODAL_NESTED submits the cleanup request
  // with the selected timezone.
  it('normalizes host bounds, warns on unsupported filters, and submits with selected timezone', async () => {
    mockCreateCleanupTask.mockResolvedValue(buildTask({ id: 91 }))

    const wrapper = mount(UsageCleanupDialog, {
      props: {
        show: true,
        startDate: '2026-11-01T08:15:00',
        endDate: '2026-11-05T19:45:00',
        filters: {
          request_id: 'req-excluded',
          billing_mode: 'token',
          native_compaction_v2: false,
          user_id: 42,
          api_key_id: 88,
          team_id: 7,
          model: 'gpt-5',
          billing_type: 0,
        },
        filterLabels: [
          { key: 'request_id', label: 'Request ID', value: 'req-excluded' },
          { key: 'user_id', label: 'User', value: 'owner@example.com' },
          { key: 'api_key_id', label: 'API Key', value: 'Prod Key' },
          { key: 'team_id', label: 'Team', value: 'Platform Team' },
          { key: 'model', label: 'Model', value: 'gpt-5' },
          { key: 'billing_type', label: 'Billing Type', value: 'Balance' },
        ],
        modelOptions: ['gpt-5', 'claude-sonnet-4'],
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Select: SelectStub,
          Icon: true,
        },
      },
    })
    await flushPromises()

    expect(wrapper.get('[data-testid="cleanup-excluded-filters-notice"]').exists()).toBe(true)
    const chipsText = wrapper.get('[data-testid="cleanup-active-filter-chips"]').text()
    expect(chipsText).toContain('owner@example.com')
    expect(chipsText).toContain('Prod Key')
    expect(chipsText).toContain('Platform Team')
    expect(chipsText).toContain('gpt-5')
    expect(chipsText).not.toContain('req-excluded')

    await wrapper.get('[data-testid="cleanup-switch-utc"]').trigger('click')
    expect(wrapper.get('[data-testid="cleanup-window-preview"]').text()).toContain(
      '[2026-11-01 00:00:00, 2026-11-05 23:59:59] (UTC)',
    )

    await wrapper.get('[data-testid="open-cleanup-confirm-btn"]').trigger('click')
    await flushPromises()

    const dialogs = wrapper.findAll('[data-testid="dialog-shell"]')
    expect(dialogs).toHaveLength(2)
    expect(dialogs[1].attributes('data-z-index')).toBe(String(Z_INDEX.MODAL_NESTED))
    expect(dialogs[1].text()).toContain('2026-11-01 ~ 2026-11-05 (UTC)')
    expect(dialogs[1].text()).toContain('owner@example.com')

    await dialogs[1].get('.btn-danger').trigger('click')
    await flushPromises()

    expect(mockCreateCleanupTask).toHaveBeenCalledWith({
      start_date: '2026-11-01',
      end_date: '2026-11-05',
      timezone: 'UTC',
      user_id: 42,
      api_key_id: 88,
      team_id: 7,
      model: 'gpt-5',
      billing_type: 0,
    })
    expect(mockShowSuccess).toHaveBeenCalledWith('admin.usage.cleanup.submitSuccess')
    wrapper.unmount()
  })

  // Removing user_id via its filter chip must also drop the dependent api_key_id chip,
  // and clearing all filters must surface both inline and confirmation all-records
  // warnings. Syncing from the host page must restore the inherited filter scope.
  it('supports chip removal, clear-all all-records warning, and page-scope resync', async () => {
    const wrapper = mount(UsageCleanupDialog, {
      props: {
        show: true,
        startDate: '2026-11-10',
        endDate: '2026-11-12',
        filters: {
          user_id: 42,
          api_key_id: 88,
          model: 'gpt-5',
        },
        filterLabels: [
          { key: 'user_id', label: 'User', value: 'owner@example.com' },
          { key: 'api_key_id', label: 'API Key', value: 'Prod Key' },
          { key: 'model', label: 'Model', value: 'gpt-5' },
        ],
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Select: SelectStub,
          Icon: true,
        },
      },
    })
    await flushPromises()

    await wrapper.get('[data-testid="cleanup-remove-chip-user_id"]').trigger('click')
    await flushPromises()

    const remainingChips = wrapper.get('[data-testid="cleanup-active-filter-chips"]').text()
    expect(remainingChips).not.toContain('owner@example.com')
    expect(remainingChips).not.toContain('Prod Key')
    expect(remainingChips).toContain('gpt-5')

    await wrapper.get('[data-testid="cleanup-clear-filters"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="cleanup-active-filter-chips"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="cleanup-unfiltered-scope"]').exists()).toBe(true)

    await wrapper.get('[data-testid="open-cleanup-confirm-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="cleanup-confirm-all-records-warning"]').exists()).toBe(true)
    await wrapper.findAll('[data-testid="dialog-shell"]')[1].get('.btn-secondary').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="cleanup-sync-page"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="cleanup-active-filter-chips"]').text()).toContain(
      'owner@example.com',
    )
    wrapper.unmount()
  })

  // Initial task retrieval must display skeleton placeholders and render persisted filter
  // tags once loaded. Active tasks poll silently every 3 seconds without replacing rows,
  // emit completion notifications and stop polling when settled, and support cancellation.
  it('renders task skeletons, formats persisted filter tags, polls active tasks, and cancels', async () => {
    const initialLoad = deferred<{
      items: UsageCleanupTask[] // Returned cleanup task records.
      total: number // Total matching task rows.
      page: number // Current page number.
      page_size: number // Current page size.
      pages: number // Total page count.
    }>()

    mockListCleanupTasks
      .mockReturnValueOnce(initialLoad.promise)
      .mockResolvedValueOnce({
        items: [
          buildTask({
            id: 15,
            status: 'succeeded',
            deleted_rows: 320,
            filters: {
              start_time: '2026-11-01T00:00:00Z',
              end_time: '2026-11-05T23:59:59Z',
              team_id: 7,
              request_source: 'system',
              model: 'gpt-5',
            },
          }),
        ],
        total: 1,
        page: 1,
        page_size: 5,
        pages: 1,
      })
      .mockResolvedValue({
        items: [
          buildTask({
            id: 16,
            status: 'pending',
          }),
        ],
        total: 1,
        page: 1,
        page_size: 5,
        pages: 1,
      })
    mockCancelCleanupTask.mockResolvedValue({ id: 16, status: 'canceled' })

    const wrapper = mount(UsageCleanupDialog, {
      props: {
        show: true,
        startDate: '2026-11-01',
        endDate: '2026-11-05',
        filters: {},
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Select: SelectStub,
          Icon: true,
        },
      },
    })
    await flushPromises()

    expect(wrapper.find('[data-testid="cleanup-tasks-skeleton"]').exists()).toBe(true)

    initialLoad.resolve({
      items: [
        buildTask({
          id: 15,
          status: 'running',
          deleted_rows: 120,
          filters: {
            start_time: '2026-11-01T00:00:00Z',
            end_time: '2026-11-05T23:59:59Z',
            team_id: 7,
            request_source: 'system',
            model: 'gpt-5',
          },
        }),
      ],
      total: 1,
      page: 1,
      page_size: 5,
      pages: 1,
    })
    await flushPromises()

    const card = wrapper.get('[data-testid="cleanup-task-card-15"]')
    expect(card.text()).toContain('Platform Team')
    expect(card.text()).toContain('gpt-5')
    expect(wrapper.find('[data-testid="cleanup-tasks-skeleton"]').exists()).toBe(false)

    // Advance three seconds to trigger a silent poll while task #15 is still running.
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(mockListCleanupTasks).toHaveBeenCalledTimes(2)
    expect(mockShowSuccess).toHaveBeenCalledWith('admin.usage.cleanup.taskCompletedToast')

    // Once settled, no additional polling request fires after another 3 seconds.
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(mockListCleanupTasks).toHaveBeenCalledTimes(2)

    // Refresh loads pending task #16; cancelling it passes through nested ConfirmDialog.
    await wrapper.get('[data-testid="refresh-cleanup-tasks-btn"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="cancel-cleanup-task-16"]').trigger('click')
    await flushPromises()
    const cancelDialog = wrapper.findAll('[data-testid="dialog-shell"]')[1]
    expect(cancelDialog.attributes('data-z-index')).toBe(String(Z_INDEX.MODAL_NESTED))
    await cancelDialog.get('.btn-danger').trigger('click')
    await flushPromises()

    expect(mockCancelCleanupTask).toHaveBeenCalledWith(16)
    expect(mockShowSuccess).toHaveBeenCalledWith('admin.usage.cleanup.cancelSuccess')
    wrapper.unmount()
  })

  // Structured backend error codes on task creation must translate to localized cleanup
  // error keys via appStore.showError. Failed submissions keep the user's draft intact
  // so the operator can adjust the range or retry.
  it('maps structured backend cleanup error codes to localized error notifications', async () => {
    mockCreateCleanupTask
      .mockRejectedValueOnce({ reason: 'USAGE_CLEANUP_RANGE_TOO_LARGE' })
      .mockRejectedValueOnce({ reason: 'USAGE_CLEANUP_DISABLED' })

    const wrapper = mount(UsageCleanupDialog, {
      props: {
        show: true,
        startDate: '2026-10-01',
        endDate: '2026-11-15',
        filters: {},
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Select: SelectStub,
          Icon: true,
        },
      },
    })
    await flushPromises()

    expect(wrapper.get('[data-testid="cleanup-window-preview"]').text()).toContain(
      'admin.usage.cleanup.rangeExceedsDefaultHint',
    )

    await wrapper.get('[data-testid="open-cleanup-confirm-btn"]').trigger('click')
    await flushPromises()
    await wrapper.findAll('[data-testid="dialog-shell"]')[1].get('.btn-danger').trigger('click')
    await flushPromises()
    expect(mockShowError).toHaveBeenCalledWith('admin.usage.cleanup.errors.rangeTooLarge')

    await wrapper.get('[data-testid="open-cleanup-confirm-btn"]').trigger('click')
    await flushPromises()
    await wrapper.findAll('[data-testid="dialog-shell"]')[1].get('.btn-danger').trigger('click')
    await flushPromises()
    expect(mockShowError).toHaveBeenCalledWith('admin.usage.cleanup.errors.disabled')
    wrapper.unmount()
  })
})
