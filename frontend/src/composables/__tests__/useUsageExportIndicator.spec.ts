import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { useUsageExportIndicator } from '../useUsageExportIndicator'
import type { UsageExportTask } from '@/api/usage.types'

// useUsageExportIndicator.spec.ts verifies closed-dialog export badge hydration, handoff
// between UsageExportDialog and background polling, completion toast transitions, and
// unmount cleanup across user and administrator modes.

enableAutoUnmount(afterEach)

// Hoisted API and notification doubles isolate background polling from HTTP transport.
const { userListExportTasks, adminListExportTasks, showSuccess, showError } = vi.hoisted(() => ({
  userListExportTasks: vi.fn(),
  adminListExportTasks: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/usage', () => ({
  usageAPI: {
    listExportTasks: userListExportTasks,
  },
}))

vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: {
    listExportTasks: adminListExportTasks,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess,
    showError,
  }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

// makeTask builds a minimal persisted UsageExportTask fixture for indicator state tests.
function makeTask(id: number, status: UsageExportTask['status']): UsageExportTask {
  return {
    id,
    scope: 'user',
    created_by: 7,
    status,
    format: 'csv',
    locale: 'zh-CN',
    timezone: 'Asia/Shanghai',
    start_time: '2026-11-15T00:00:00+08:00',
    end_time: '2026-11-16T00:00:00+08:00',
    filters: {
      start_time: '2026-11-15T00:00:00+08:00',
      end_time: '2026-11-16T00:00:00+08:00',
      timezone: 'Asia/Shanghai',
      locale: 'zh-CN',
      balance_unit_name: 'USD',
    },
    total_rows: 100,
    exported_rows: status === 'succeeded' ? 100 : 25,
    file_name: `usage_${id}.csv`,
    file_size_bytes: status === 'succeeded' ? 4096 : 0,
    error_code: '',
    error_message: status === 'failed' ? 'boom' : '',
    started_at: '2026-11-15T01:00:00Z',
    completed_at: status === 'succeeded' || status === 'failed' ? '2026-11-15T01:00:05Z' : null,
    expires_at: status === 'succeeded' ? '2026-11-16T01:00:05Z' : null,
    created_at: '2026-11-15T01:00:00Z',
    updated_at: '2026-11-15T01:00:05Z',
  }
}

// mountHarness mounts a minimal host component exposing useUsageExportIndicator state.
function mountHarness(mode: 'user' | 'admin' = 'user', initialOpen = false) {
  const isDialogOpen = ref(initialOpen)
  const Host = defineComponent({
    setup() {
      const indicator = useUsageExportIndicator(mode, isDialogOpen)
      return { isDialogOpen, ...indicator }
    },
    template: '<div>{{ activeExportCount }}</div>',
  })
  const wrapper = mount(Host)
  return { wrapper, isDialogOpen }
}

describe('useUsageExportIndicator', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    userListExportTasks.mockReset()
    adminListExportTasks.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  // Mounting the indicator when a background export is already running hydrates
  // activeExportCount without firing a premature toast. Once the closed-dialog poll
  // observes succeeded status, activeExportCount clears and a completion toast appears.
  it('hydrates active count on mount and toasts when a background task finishes', async () => {
    userListExportTasks
      .mockResolvedValueOnce({
        items: [makeTask(11, 'running'), makeTask(10, 'succeeded')],
        total: 2,
        page: 1,
        page_size: 10,
        pages: 1,
      })
      .mockResolvedValueOnce({
        items: [makeTask(11, 'succeeded'), makeTask(10, 'succeeded')],
        total: 2,
        page: 1,
        page_size: 10,
        pages: 1,
      })

    const { wrapper } = mountHarness('user')
    await flushPromises()

    expect(wrapper.vm.activeExportCount).toBe(1)
    expect(showSuccess).not.toHaveBeenCalled()

    vi.advanceTimersByTime(4000)
    await flushPromises()

    expect(wrapper.vm.activeExportCount).toBe(0)
    expect(showSuccess).toHaveBeenCalledWith('usage.exportPanel.taskCompletedToast')

    // Once activeExportCount drops to zero, no further background polls should fire.
    vi.advanceTimersByTime(8000)
    await flushPromises()
    expect(userListExportTasks).toHaveBeenCalledTimes(2)
  })

  // Starting an export inside UsageExportDialog publishes its active ID to the indicator
  // while keeping closed-dialog polling paused. Closing the dialog starts the 4-second
  // background timer and surfaces an error toast when the task fails.
  it('pauses while the dialog is open and resumes tracking when the dialog closes', async () => {
    adminListExportTasks.mockResolvedValueOnce({
      items: [],
      total: 0,
      page: 1,
      page_size: 10,
      pages: 0,
    })

    const { wrapper, isDialogOpen } = mountHarness('admin')
    await flushPromises()
    expect(wrapper.vm.activeExportCount).toBe(0)

    isDialogOpen.value = true
    await flushPromises()
    wrapper.vm.handleDialogTasksChanged(1)
    wrapper.vm.handleDialogActiveIdsChanged([99])

    // While open, the background indicator must not poll on its own timer.
    vi.advanceTimersByTime(8000)
    await flushPromises()
    expect(adminListExportTasks).toHaveBeenCalledTimes(1)

    adminListExportTasks.mockResolvedValueOnce({
      items: [makeTask(99, 'failed')],
      total: 1,
      page: 1,
      page_size: 10,
      pages: 1,
    })

    isDialogOpen.value = false
    await flushPromises()

    vi.advanceTimersByTime(4000)
    await flushPromises()

    expect(wrapper.vm.activeExportCount).toBe(0)
    expect(showError).toHaveBeenCalledWith('usage.exportPanel.taskFailedToast')
  })

  // When ten newer finished exports push an older running task (#1) onto page two,
  // activeExportCount must remain 1, background polling must keep running with watch_ids,
  // and the completion toast must still fire once task #1 settles off page one.
  it('keeps polling and emits completion toast when an active task moves past page one', async () => {
    const newerFinished = Array.from({ length: 10 }, (_, idx) => makeTask(11 - idx, 'succeeded'))

    userListExportTasks
      .mockResolvedValueOnce({
        items: [makeTask(1, 'running')],
        total: 1,
        page: 1,
        page_size: 10,
        pages: 1,
        active_count: 1,
        active_ids: [1],
      })
      .mockResolvedValueOnce({
        items: newerFinished,
        total: 11,
        page: 1,
        page_size: 10,
        pages: 2,
        active_count: 1,
        active_ids: [1],
        watched_items: [makeTask(1, 'running')],
      })
      .mockResolvedValueOnce({
        items: newerFinished,
        total: 11,
        page: 1,
        page_size: 10,
        pages: 2,
        active_count: 0,
        active_ids: [],
        watched_items: [makeTask(1, 'succeeded')],
      })

    const { wrapper } = mountHarness('user')
    await flushPromises()
    expect(wrapper.vm.activeExportCount).toBe(1)

    // Poll 1: task #1 is pushed off page 1 by 10 newer tasks, but stays running on page 2.
    vi.advanceTimersByTime(4000)
    await flushPromises()
    expect(userListExportTasks).toHaveBeenNthCalledWith(
      2,
      { page: 1, page_size: 10, watch_ids: '1' },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
    expect(wrapper.vm.activeExportCount).toBe(1)
    expect(showSuccess).not.toHaveBeenCalled()

    // Poll 2: off-page task #1 transitions to succeeded via watched_items.
    vi.advanceTimersByTime(4000)
    await flushPromises()
    expect(wrapper.vm.activeExportCount).toBe(0)
    expect(showSuccess).toHaveBeenCalledWith('usage.exportPanel.taskCompletedToast')
  })

  // If a response still carries a running task in items or watched_items alongside an
  // empty active_ids array, the indicator must keep polling until the terminal status arrives.
  it('retains active tracking when an observed item is still running despite empty active_ids', async () => {
    userListExportTasks
      .mockResolvedValueOnce({
        items: [makeTask(21, 'running')],
        total: 1,
        page: 1,
        page_size: 10,
        pages: 1,
        active_count: 0,
        active_ids: [],
      })
      .mockResolvedValueOnce({
        items: [makeTask(21, 'succeeded')],
        total: 1,
        page: 1,
        page_size: 10,
        pages: 1,
        active_count: 0,
        active_ids: [],
      })

    const { wrapper } = mountHarness('user')
    await flushPromises()
    expect(wrapper.vm.activeExportCount).toBe(1)

    vi.advanceTimersByTime(4000)
    await flushPromises()

    expect(wrapper.vm.activeExportCount).toBe(0)
    expect(showSuccess).toHaveBeenCalledWith('usage.exportPanel.taskCompletedToast')
  })
})
