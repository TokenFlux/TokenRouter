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

// BACKGROUND_POLL_MS, INDICATOR_PAGE_SIZE, and MAX_FALLBACK_SCAN_PAGES govern closed-dialog
// polling cadence, page size, and fallback multi-page scans when test doubles omit active_ids.
const BACKGROUND_POLL_MS = 4000
const INDICATOR_PAGE_SIZE = 10
const MAX_FALLBACK_SCAN_PAGES = 10

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
  let isUnmounted = false

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

  // syncBackgroundPolling runs the 4-second background timer only while mounted, with the
  // dialog closed, and while at least one export task remains pending or running.
  const syncBackgroundPolling = () => {
    if (isUnmounted || isDialogOpen.value || activeExportCount.value <= 0) {
      stopPolling()
      return
    }
    if (pollTimer === null) {
      pollTimer = window.setInterval(() => {
        void refreshActiveExports(true)
      }, BACKGROUND_POLL_MS)
    }
  }

  // refreshActiveExports queries export tasks while the dialog is closed, forwarding any
  // trackedActiveIds as watch_ids so older running tasks pushed past page one retain accurate
  // badge counts, keep background polling active, and still emit terminal transition toasts.
  //
  // Responses arriving after a newer request starts, after cancellation, after unmount, or
  // after the dialog opens are ignored so open-dialog state remains authoritative.
  const refreshActiveExports = async (notifyTransitions = false): Promise<void> => {
    if (isUnmounted || isDialogOpen.value) {
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
    const previousActive = new Set(trackedActiveIds)
    const watchIds = previousActive.size > 0 ? [...previousActive].join(',') : undefined

    try {
      const page = await listFn(
        {
          page: 1,
          page_size: INDICATOR_PAGE_SIZE,
          ...(watchIds ? { watch_ids: watchIds } : {}),
        },
        { signal: controller.signal },
      )
      if (isUnmounted || seq !== requestSeq || controller.signal.aborted || isDialogOpen.value) {
        return
      }

      const knownById = new Map<number, UsageExportTask>()
      const firstItems = Array.isArray(page?.items) ? page.items : []
      for (const item of firstItems) {
        knownById.set(item.id, item)
      }
      if (Array.isArray(page?.watched_items)) {
        for (const item of page.watched_items) {
          knownById.set(item.id, item)
        }
      }

      const hasServerActiveIds = Array.isArray(page?.active_ids)
      const totalPages = typeof page?.pages === 'number' ? page.pages : 1
      if (!hasServerActiveIds && totalPages > 1) {
        // Fallback scan for plain paginated fixtures that omit server-computed active_ids
        // and watched_items when older running tasks move onto subsequent pages.
        const maxPage = Math.min(totalPages, MAX_FALLBACK_SCAN_PAGES)
        for (let pageNum = 2; pageNum <= maxPage; pageNum += 1) {
          const nextPage = await listFn(
            { page: pageNum, page_size: INDICATOR_PAGE_SIZE },
            { signal: controller.signal },
          )
          if (
            isUnmounted ||
            seq !== requestSeq ||
            controller.signal.aborted ||
            isDialogOpen.value
          ) {
            return
          }
          if (Array.isArray(nextPage?.items)) {
            for (const item of nextPage.items) {
              knownById.set(item.id, item)
            }
          }
        }
      }

      const nextActive = new Set<number>()
      if (hasServerActiveIds) {
        for (const id of page.active_ids!) {
          if (typeof id === 'number' && id > 0) {
            nextActive.add(id)
          }
        }
      }
      // Retain any returned task record still reporting pending or running status so
      // polling cannot halt before observing its terminal status transition.
      for (const item of knownById.values()) {
        if (isTaskActive(item)) {
          nextActive.add(item.id)
        }
      }

      if (notifyTransitions) {
        for (const prevId of previousActive) {
          if (nextActive.has(prevId)) {
            continue
          }
          const settled = knownById.get(prevId)
          if (settled?.status === 'succeeded') {
            appStore.showSuccess(t('usage.exportPanel.taskCompletedToast'))
          } else if (settled?.status === 'failed') {
            appStore.showError(t('usage.exportPanel.taskFailedToast'))
          }
        }
      }

      trackedActiveIds = nextActive
      const reportedCount =
        typeof page?.active_count === 'number'
          ? Math.max(nextActive.size, page.active_count)
          : nextActive.size
      activeExportCount.value = reportedCount
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
    // Mark unmounted and advance requestSeq before aborting so any settling promise stops.
    isUnmounted = true
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
