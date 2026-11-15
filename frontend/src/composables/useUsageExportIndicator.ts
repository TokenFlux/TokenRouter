import { onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { usageAPI } from '@/api/usage'
import { adminUsageAPI } from '@/api/admin/usage'
import type { UsageExportTask } from '@/api/usage.types'
import { useAppStore } from '@/stores/app'

// useUsageExportIndicator.ts coordinates active background export badges and completion
// toasts for the user and administrator usage pages while the export dialog is closed.
// Opening the dialog transfers polling and toast ownership to UsageExportDialog so the two
// surfaces never issue duplicate list requests or duplicate notifications for the same task.
//
// Closing the dialog while tasks remain pending or running hands the tracked task
// identifiers back to a 4-second background loop guarded by a request sequence and an
// AbortController. Responses that settle after the dialog reopens or after page unmount are
// discarded before mutating badge counts or emitting notifications.

// BACKGROUND_POLL_MS and INDICATOR_PAGE_SIZE govern closed-dialog polling cadence and the
// first-page window inspected for active tasks (exceeding both user and admin quotas).
const BACKGROUND_POLL_MS = 4000
const INDICATOR_PAGE_SIZE = 10

// UsageExportIndicator bindings connect the usage page's export button and UsageExportDialog
// events to shared background task tracking.
export interface UsageExportIndicator {
  activeExportCount: Ref<number> // Count of pending or running export tasks for the toolbar badge.
  handleDialogTasksChanged: (count: number) => void // Applies active-count updates emitted by the open dialog.
  handleDialogActiveIdsChanged: (ids: number[]) => void // Synchronizes active task IDs observed by the open dialog.
  refreshActiveExports: (notifyTransitions?: boolean) => Promise<void> // Fetches page one of export tasks when closed.
}

// isTaskActive reports whether a persisted export task is still queued or running.
function isTaskActive(task: UsageExportTask): boolean {
  return task.status === 'pending' || task.status === 'running'
}

// useUsageExportIndicator tracks active usage export tasks for the given authorization mode
// whenever isDialogOpen is false. Callers bind activeExportCount to the toolbar button and
// forward UsageExportDialog's tasks-changed and active-ids-changed events to the returned
// handlers so background polling resumes with the latest active task set upon dialog close.
//
// Each background poll aborts any earlier indicator request and compares both its captured
// sequence and current dialog visibility before publishing badge counts or completion
// notifications. Unmounting advances the sequence, clears the interval timer, and aborts
// any in-flight request.
export function useUsageExportIndicator(
  mode: 'user' | 'admin',
  isDialogOpen: Ref<boolean>,
): UsageExportIndicator {
  const { t } = useI18n()
  const appStore = useAppStore()

  // activeExportCount drives the toolbar spinner and active count label, while
  // trackedActiveIds remembers which tasks were active on the preceding observation.
  const activeExportCount = ref(0)
  let trackedActiveIds = new Set<number>()
  let pollTimer: number | null = null
  let abortController: AbortController | null = null
  let requestSeq = 0

  // stopPolling clears the recurring closed-dialog timer when no tasks remain active or
  // when the export dialog takes over polling.
  const stopPolling = () => {
    if (pollTimer !== null) {
      window.clearInterval(pollTimer)
      pollTimer = null
    }
  }

  // abortInFlight cancels any pending background task-list request and releases its handle.
  const abortInFlight = () => {
    if (abortController) {
      abortController.abort()
      abortController = null
    }
  }

  // syncBackgroundPolling runs the 4-second background timer only while the dialog is closed
  // and at least one export task remains pending or running.
  const syncBackgroundPolling = () => {
    if (isDialogOpen.value || activeExportCount.value <= 0) {
      stopPolling()
      return
    }
    if (pollTimer === null) {
      pollTimer = window.setInterval(() => {
        void refreshActiveExports(true)
      }, BACKGROUND_POLL_MS)
    }
  }

  // refreshActiveExports fetches the first page of export tasks while the dialog is closed.
  // Initial mount calls pass notifyTransitions = false to hydrate the badge silently, whereas
  // recurring polls pass true so tasks transitioning to succeeded or failed emit one toast.
  //
  // Responses arriving after a newer request starts, after cancellation, or after the dialog
  // opens are ignored so open-dialog state remains authoritative.
  const refreshActiveExports = async (notifyTransitions = false): Promise<void> => {
    if (isDialogOpen.value) {
      return
    }
    const listFn = mode === 'admin' ? adminUsageAPI?.listExportTasks : usageAPI?.listExportTasks
    if (typeof listFn !== 'function') {
      return
    }

    // Abort any older indicator fetch before advancing requestSeq so late settlements cannot
    // overwrite newer badge counts or emit duplicate completion toasts.
    abortInFlight()
    const controller = new AbortController()
    abortController = controller
    const seq = ++requestSeq

    try {
      const page = await listFn(
        { page: 1, page_size: INDICATOR_PAGE_SIZE },
        { signal: controller.signal },
      )
      if (seq !== requestSeq || controller.signal.aborted || isDialogOpen.value) {
        return
      }

      const items = Array.isArray(page?.items) ? page.items : []
      const nextActive = new Set<number>()
      for (const item of items) {
        if (isTaskActive(item)) {
          nextActive.add(item.id)
          continue
        }
        if (notifyTransitions && trackedActiveIds.has(item.id)) {
          if (item.status === 'succeeded') {
            appStore.showSuccess(t('usage.exportPanel.taskCompletedToast'))
          } else if (item.status === 'failed') {
            appStore.showError(t('usage.exportPanel.taskFailedToast'))
          }
        }
      }

      trackedActiveIds = nextActive
      activeExportCount.value = nextActive.size
      syncBackgroundPolling()
    } catch {
      // Background indicator probes fail silently so transient polling errors do not disrupt
      // usage browsing or raise modal alerts while the export panel is closed.
    } finally {
      if (abortController === controller) {
        abortController = null
      }
    }
  }

  // handleDialogTasksChanged updates the toolbar badge count from UsageExportDialog and
  // clears tracked IDs immediately when all tasks settle.
  const handleDialogTasksChanged = (count: number) => {
    const normalized = Math.max(0, count)
    activeExportCount.value = normalized
    if (normalized === 0) {
      trackedActiveIds.clear()
    }
    syncBackgroundPolling()
  }

  // handleDialogActiveIdsChanged records the exact pending or running task IDs observed by
  // UsageExportDialog so closing the dialog preserves transition toast tracking.
  const handleDialogActiveIdsChanged = (ids: number[]) => {
    trackedActiveIds = new Set(ids)
    activeExportCount.value = trackedActiveIds.size
    syncBackgroundPolling()
  }

  watch(isDialogOpen, (open) => {
    if (open) {
      // Yield polling and cancel in-flight background requests as soon as the dialog opens
      // so UsageExportDialog owns refreshes exclusively while visible.
      requestSeq += 1
      stopPolling()
      abortInFlight()
      return
    }
    syncBackgroundPolling()
  })

  onMounted(() => {
    if (!isDialogOpen.value) {
      void refreshActiveExports(false)
    }
  })

  onUnmounted(() => {
    // Advance requestSeq before aborting so any settling promise observes invalidation.
    requestSeq += 1
    stopPolling()
    abortInFlight()
  })

  return {
    activeExportCount,
    handleDialogTasksChanged,
    handleDialogActiveIdsChanged,
    refreshActiveExports,
  }
}
