import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { ref } from 'vue'

import UsageExportDialog from '../UsageExportDialog.vue'

// UsageExportDialog.spec.ts verifies page-range inheritance, timezone switching,
// flexible timestamp paste parsing, filter-inheritance toggling, progress polling,
// task cancellation, and signed-ticket CSV download delivery across user and admin modes.

// API mock functions isolate user and administrator export HTTP calls from network I/O.
const mockUserListExportTasks = vi.fn()
const mockUserCreateExportTask = vi.fn()
const mockUserCancelExportTask = vi.fn()
const mockUserCreateDownloadTicket = vi.fn()
const mockTriggerExportDownloadUrl = vi.fn()

const mockAdminListExportTasks = vi.fn()
const mockAdminCreateExportTask = vi.fn()
const mockAdminCancelExportTask = vi.fn()
const mockAdminCreateDownloadTicket = vi.fn()

const mockShowSuccess = vi.fn()
const mockShowError = vi.fn()
const mockShowInfo = vi.fn()

vi.mock('@/api/usage', () => ({
  usageAPI: {
    listExportTasks: (...args: unknown[]) => mockUserListExportTasks(...args),
    createExportTask: (...args: unknown[]) => mockUserCreateExportTask(...args),
    cancelExportTask: (...args: unknown[]) => mockUserCancelExportTask(...args),
    createExportDownloadTicket: (...args: unknown[]) => mockUserCreateDownloadTicket(...args),
    triggerExportDownloadUrl: (...args: unknown[]) => mockTriggerExportDownloadUrl(...args),
  },
}))

vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: {
    listExportTasks: (...args: unknown[]) => mockAdminListExportTasks(...args),
    createExportTask: (...args: unknown[]) => mockAdminCreateExportTask(...args),
    cancelExportTask: (...args: unknown[]) => mockAdminCancelExportTask(...args),
    createExportDownloadTicket: (...args: unknown[]) => mockAdminCreateDownloadTicket(...args),
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: mockShowSuccess,
    showError: mockShowError,
    showInfo: mockShowInfo,
  }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
      locale: ref('zh-Hans'),
    }),
  }
})

// BaseDialogStub renders header, icon, and body slots inline without body teleportation
// so wrapper queries inspect rendered export controls directly.
const BaseDialogStub = {
  props: ['show', 'title', 'subtitle', 'width'],
  emits: ['close'],
  template: `
    <div v-if="show" data-testid="export-dialog-shell">
      <h3>{{ title }}</h3>
      <slot name="header-icon" />
      <slot />
    </div>
  `,
}

// SelectStub exposes modelValue and forwards string selections in unit tests.
const SelectStub = {
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: `
    <button
      type="button"
      data-testid="timezone-select-stub"
      @click="$emit('update:modelValue', 'America/New_York')"
    >
      {{ modelValue }}
    </button>
  `,
}

describe('UsageExportDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-15T10:20:30Z'))
    vi.clearAllMocks()

    mockUserListExportTasks.mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 10,
      pages: 0,
    })
    mockAdminListExportTasks.mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 10,
      pages: 0,
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  // Opening UsageExportDialog on the user Usage page inherits the half-open page date
  // window and renders active filter summary chips. Switching to UTC and submitting sends
  // explicit RFC3339 bounds alongside zero-valued balance and false-valued compaction filters.
  it('inherits page dates and active filters and creates a user export task with RFC3339 bounds', async () => {
    mockUserCreateExportTask.mockResolvedValue({
      id: 41,
      scope: 'user',
      status: 'pending',
      filters: {
        start_time: '2026-11-01T00:00:00Z',
        end_time: '2026-11-16T00:00:00Z',
        timezone: 'UTC',
      },
      created_by: 7,
      total_rows: 0,
      exported_rows: 0,
      file_size_bytes: 0,
      created_at: '2026-11-15T10:20:30Z',
      updated_at: '2026-11-15T10:20:30Z',
    })

    const wrapper = mount(UsageExportDialog, {
      props: {
        show: true,
        mode: 'user',
        startDate: '2026-11-01',
        endDate: '2026-11-15',
        filters: {
          api_key_id: 12,
          model: 'gpt-5',
          billing_type: 0,
          native_compaction_v2: false,
        },
        filterLabels: [
          { key: 'api_key_id', label: 'API Key', value: 'Production Key' },
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

    expect(wrapper.find('[data-testid="export-active-filter-chips"]').text()).toContain(
      'Production Key',
    )

    // Switch to UTC and re-sync page dates so midnight aligns with UTC 00:00:00.
    await wrapper.find('[data-testid="export-switch-utc"]').trigger('click')
    await wrapper.find('[data-testid="export-preset-page"]').trigger('click')

    expect(wrapper.find('[data-testid="export-boundary-preview"]').text()).toContain(
      '[2026-11-01 00:00:00, 2026-11-16 00:00:00) (UTC)',
    )

    await wrapper.find('[data-testid="create-export-task-btn"]').trigger('click')
    await flushPromises()

    expect(mockUserCreateExportTask).toHaveBeenCalledWith({
      start_time: '2026-11-01T00:00:00Z',
      end_time: '2026-11-16T00:00:00Z',
      timezone: 'UTC',
      locale: 'zh',
      api_key_id: 12,
      model: 'gpt-5',
      billing_type: 0,
      native_compaction_v2: false,
    })
    expect(mockShowSuccess).toHaveBeenCalledWith('usage.exportPanel.taskCreatedToast')
  })

  // Administrators frequently paste Unix timestamps from logs and may toggle off page
  // filters to export all records in a narrow incident window. Submitting must convert
  // pasted epoch seconds into the selected timezone and omit excluded page filters.
  it('supports pasting Unix seconds and excluding inherited filters in admin mode', async () => {
    mockAdminCreateExportTask.mockResolvedValue({
      id: 88,
      scope: 'admin',
      status: 'pending',
      filters: {
        start_time: '2026-11-15T00:30:00Z',
        end_time: '2026-11-15T01:30:00Z',
        timezone: 'UTC',
      },
      created_by: 1,
      total_rows: 0,
      exported_rows: 0,
      file_size_bytes: 0,
      created_at: '2026-11-15T10:20:30Z',
      updated_at: '2026-11-15T10:20:30Z',
    })

    const wrapper = mount(UsageExportDialog, {
      props: {
        show: true,
        mode: 'admin',
        startDate: '2026-11-15',
        endDate: '2026-11-15',
        filters: {
          user_id: 99,
          provider_id: 5,
          request_source: 'system',
        },
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

    await wrapper.find('[data-testid="export-switch-utc"]').trigger('click')
    await wrapper.find('[data-testid="export-toggle-paste"]').trigger('click')

    await wrapper.find('[data-testid="export-paste-start-input"]').setValue('1794702600')
    await wrapper.find('[data-testid="export-paste-start-apply"]').trigger('click')
    await wrapper.find('[data-testid="export-paste-end-input"]').setValue('1794706200')
    await wrapper.find('[data-testid="export-paste-end-apply"]').trigger('click')

    // Turn off page filter inheritance via the accessible switch control.
    await wrapper.find('[role="switch"]').trigger('click')

    await wrapper.find('[data-testid="create-export-task-btn"]').trigger('click')
    await flushPromises()

    expect(mockAdminCreateExportTask).toHaveBeenCalledWith({
      start_time: '2026-11-15T00:30:00Z',
      end_time: '2026-11-15T01:30:00Z',
      timezone: 'UTC',
      locale: 'zh',
    })
  })

  // Opening the dialog with active pending and running tasks starts a 3-second polling
  // timer until all tasks reach terminal status. Operators can cancel pending tasks and
  // exchange a 60-second signed ticket to download succeeded CSV files.
  it('polls running tasks to completion, supports cancellation, and downloads via signed ticket', async () => {
    mockUserListExportTasks
      .mockResolvedValueOnce({
        items: [
          {
            id: 51,
            scope: 'user',
            status: 'running',
            filters: {
              start_time: '2026-11-15T00:00:00+08:00',
              end_time: '2026-11-16T00:00:00+08:00',
              timezone: 'Asia/Shanghai',
            },
            created_by: 7,
            total_rows: 1000,
            exported_rows: 400,
            file_size_bytes: 0,
            created_at: '2026-11-15T10:00:00Z',
            updated_at: '2026-11-15T10:01:00Z',
          },
          {
            id: 50,
            scope: 'user',
            status: 'pending',
            filters: {
              start_time: '2026-11-14T00:00:00+08:00',
              end_time: '2026-11-15T00:00:00+08:00',
              timezone: 'Asia/Shanghai',
            },
            created_by: 7,
            total_rows: 0,
            exported_rows: 0,
            file_size_bytes: 0,
            created_at: '2026-11-15T09:59:00Z',
            updated_at: '2026-11-15T09:59:00Z',
          },
        ],
        total: 2,
        page: 1,
        page_size: 10,
        pages: 1,
      })
      .mockResolvedValueOnce({
        items: [
          {
            id: 51,
            scope: 'user',
            status: 'succeeded',
            filters: {
              start_time: '2026-11-15T00:00:00+08:00',
              end_time: '2026-11-16T00:00:00+08:00',
              timezone: 'Asia/Shanghai',
            },
            created_by: 7,
            total_rows: 1000,
            exported_rows: 1000,
            file_name: 'usage_export_51.csv',
            file_size_bytes: 131072,
            expires_at: '2026-11-16T10:02:00Z',
            created_at: '2026-11-15T10:00:00Z',
            updated_at: '2026-11-15T10:02:00Z',
          },
          {
            id: 50,
            scope: 'user',
            status: 'canceled',
            filters: {
              start_time: '2026-11-14T00:00:00+08:00',
              end_time: '2026-11-15T00:00:00+08:00',
              timezone: 'Asia/Shanghai',
            },
            created_by: 7,
            total_rows: 0,
            exported_rows: 0,
            file_size_bytes: 0,
            created_at: '2026-11-15T09:59:00Z',
            updated_at: '2026-11-15T10:01:30Z',
          },
        ],
        total: 2,
        page: 1,
        page_size: 10,
        pages: 1,
      })

    mockUserCancelExportTask.mockResolvedValue({
      id: 50,
      scope: 'user',
      status: 'canceled',
      filters: {
        start_time: '2026-11-14T00:00:00+08:00',
        end_time: '2026-11-15T00:00:00+08:00',
        timezone: 'Asia/Shanghai',
      },
      created_by: 7,
      total_rows: 0,
      exported_rows: 0,
      file_size_bytes: 0,
      created_at: '2026-11-15T09:59:00Z',
      updated_at: '2026-11-15T10:01:30Z',
    })

    mockUserCreateDownloadTicket.mockResolvedValue({
      ticket: 'signed-ticket-51',
      download_url: '/api/v1/usage/export-downloads/signed-ticket-51',
      expires_at: '2026-11-15T10:21:30Z',
    })

    const wrapper = mount(UsageExportDialog, {
      props: {
        show: true,
        mode: 'user',
        startDate: '2026-11-15',
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

    expect(wrapper.emitted('tasksChanged')?.[0]).toEqual([2])
    expect(wrapper.find('[data-testid="export-task-card-51"]').text()).toContain('40%')

    // Cancel queued task #50.
    await wrapper.find('[data-testid="cancel-export-task-50"]').trigger('click')
    await flushPromises()
    expect(mockUserCancelExportTask).toHaveBeenCalledWith(50)

    // Advance 3s timer so running task #51 refreshes to succeeded and stops polling.
    vi.advanceTimersByTime(3000)
    await flushPromises()

    expect(mockShowSuccess).toHaveBeenCalledWith('usage.exportPanel.taskCompletedToast')
    expect(wrapper.emitted('tasksChanged')?.at(-1)).toEqual([0])

    // Click Download CSV on succeeded task #51.
    await wrapper.find('[data-testid="download-export-task-51"]').trigger('click')
    await flushPromises()

    expect(mockUserCreateDownloadTicket).toHaveBeenCalledWith(51)
    expect(mockTriggerExportDownloadUrl).toHaveBeenCalledWith(
      '/api/v1/usage/export-downloads/signed-ticket-51',
      'usage_export_51.csv',
    )
  })

  // Pasting 2026-11-01T01:30:00-05:00 under America/New_York targets the second fall-back
  // occurrence at 06:30:00Z rather than 01:30:00-04:00 at 05:30:00Z. The UTC preview and
  // submitted start_time payload must preserve -05:00 without shifting an hour earlier.
  it('preserves pasted EST offset during America/New_York daylight-saving fall-back', async () => {
    mockUserCreateExportTask.mockResolvedValue({
      id: 92,
      scope: 'user',
      status: 'pending',
      filters: {
        start_time: '2026-11-01T01:30:00-05:00',
        end_time: '2026-11-01T02:30:00-05:00',
        timezone: 'America/New_York',
      },
      created_by: 7,
      total_rows: 0,
      exported_rows: 0,
      file_size_bytes: 0,
      created_at: '2026-11-15T10:20:30Z',
      updated_at: '2026-11-15T10:20:30Z',
    })

    const wrapper = mount(UsageExportDialog, {
      props: {
        show: true,
        mode: 'user',
        startDate: '2026-11-01',
        endDate: '2026-11-01',
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

    // Select America/New_York via SelectStub, then paste unambiguous EST (-05:00) bounds.
    await wrapper.find('[data-testid="timezone-select-stub"]').trigger('click')
    await wrapper.find('[data-testid="export-toggle-paste"]').trigger('click')

    await wrapper
      .find('[data-testid="export-paste-start-input"]')
      .setValue('2026-11-01T01:30:00-05:00')
    await wrapper.find('[data-testid="export-paste-start-apply"]').trigger('click')

    await wrapper
      .find('[data-testid="export-paste-end-input"]')
      .setValue('2026-11-01T02:30:00-05:00')
    await wrapper.find('[data-testid="export-paste-end-apply"]').trigger('click')

    expect(wrapper.find('[data-testid="export-boundary-preview"]').text()).toContain(
      '[2026-11-01 06:30:00Z, 2026-11-01 07:30:00Z)',
    )

    await wrapper.find('[data-testid="create-export-task-btn"]').trigger('click')
    await flushPromises()

    expect(mockUserCreateExportTask).toHaveBeenCalledWith({
      start_time: '2026-11-01T01:30:00-05:00',
      end_time: '2026-11-01T02:30:00-05:00',
      timezone: 'America/New_York',
      locale: 'zh',
    })
  })

  // Navigating away unmounts UsageExportDialog while createExportTask may still be in
  // flight. A late creation resolution after unmount must neither call listExportTasks
  // nor start a background polling interval on the destroyed component instance.
  it('ignores late createExportTask completion after unmount without restarting polling', async () => {
    let resolveCreate!: (value: unknown) => void
    mockUserCreateExportTask.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveCreate = resolve
        }),
    )

    const wrapper = mount(UsageExportDialog, {
      props: {
        show: true,
        mode: 'user',
        startDate: '2026-11-15',
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
    expect(mockUserListExportTasks).toHaveBeenCalledTimes(1)

    await wrapper.find('[data-testid="create-export-task-btn"]').trigger('click')
    expect(mockUserCreateExportTask).toHaveBeenCalledTimes(1)

    // Unmount before createExportTask settles, then resolve the pending creation request.
    wrapper.unmount()
    resolveCreate({
      id: 77,
      scope: 'user',
      status: 'pending',
      filters: {
        start_time: '2026-11-15T00:00:00Z',
        end_time: '2026-11-16T00:00:00Z',
        timezone: 'UTC',
      },
      created_by: 7,
      total_rows: 0,
      exported_rows: 0,
      file_size_bytes: 0,
      created_at: '2026-11-15T10:20:30Z',
      updated_at: '2026-11-15T10:20:30Z',
    })
    await flushPromises()

    vi.advanceTimersByTime(12000)
    await flushPromises()

    expect(mockUserListExportTasks).toHaveBeenCalledTimes(1)
  })

  // Even if an intermediary or stale response ever pairs a running row in items with an
  // empty active_ids array, the dialog must keep polling until the terminal succeeded row
  // arrives so the Download button and completion toast still appear automatically.
  it('continues polling when a displayed row is still running despite empty active_ids', async () => {
    mockUserListExportTasks
      .mockResolvedValueOnce({
        items: [
          {
            id: 61,
            scope: 'user',
            status: 'running',
            filters: {
              start_time: '2026-11-15T00:00:00Z',
              end_time: '2026-11-16T00:00:00Z',
              timezone: 'UTC',
            },
            created_by: 7,
            total_rows: 200,
            exported_rows: 180,
            file_size_bytes: 0,
            created_at: '2026-11-15T10:00:00Z',
            updated_at: '2026-11-15T10:01:00Z',
          },
        ],
        total: 1,
        page: 1,
        page_size: 10,
        pages: 1,
        active_count: 0,
        active_ids: [],
      })
      .mockResolvedValueOnce({
        items: [
          {
            id: 61,
            scope: 'user',
            status: 'succeeded',
            filters: {
              start_time: '2026-11-15T00:00:00Z',
              end_time: '2026-11-16T00:00:00Z',
              timezone: 'UTC',
            },
            created_by: 7,
            total_rows: 200,
            exported_rows: 200,
            file_name: 'usage_export_61.csv',
            file_size_bytes: 8192,
            expires_at: '2026-11-16T10:02:00Z',
            created_at: '2026-11-15T10:00:00Z',
            updated_at: '2026-11-15T10:02:00Z',
          },
        ],
        total: 1,
        page: 1,
        page_size: 10,
        pages: 1,
        active_count: 0,
        active_ids: [],
      })

    const wrapper = mount(UsageExportDialog, {
      props: {
        show: true,
        mode: 'user',
        startDate: '2026-11-15',
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

    expect(wrapper.emitted('tasksChanged')?.[0]).toEqual([1])
    expect(wrapper.find('[data-testid="download-export-task-61"]').exists()).toBe(false)

    vi.advanceTimersByTime(3000)
    await flushPromises()

    expect(mockShowSuccess).toHaveBeenCalledWith('usage.exportPanel.taskCompletedToast')
    expect(wrapper.find('[data-testid="download-export-task-61"]').exists()).toBe(true)
    expect(wrapper.emitted('tasksChanged')?.at(-1)).toEqual([0])
  })
})
