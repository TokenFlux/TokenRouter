import { onBeforeUnmount, ref, type Ref } from 'vue'
import { opsAPI } from '@/api/admin/ops'
import type { OpsRealtimeTrafficSummary } from '@/api/admin/ops.types'

/**
 * useOpsRealtimeTraffic.ts coordinates short-window realtime QPS/TPS queries for
 * OpsDashboardHeader. Each load increments a generation counter and aborts any
 * superseded in-flight request so rapid window, platform, or group filter
 * changes never drop the latest selection or publish stale responses.
 */

/**
 * UseOpsRealtimeTrafficOptions supplies reactive filter readers and monitoring
 * state callbacks from the dashboard header.
 */
export interface UseOpsRealtimeTrafficOptions {
  window: Readonly<Ref<string>> // Selected realtime observation window.
  getPlatform: () => string // Current platform filter reader.
  getGroupId: () => number | null // Current group filter reader.
  isMonitoringEnabled: () => boolean // Realtime monitoring switch reader.
  onServerDisabled: () => void // Invoked when server reports enabled === false.
}

/**
 * isAbortRejection detects cancellation errors produced by AbortController across
 * Axios and Fetch adapters so superseded requests settle silently.
 */
function isAbortRejection(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const record = error as { name?: unknown; code?: unknown }
  return (
    record.name === 'AbortError' ||
    record.name === 'CanceledError' ||
    record.code === 'ERR_CANCELED'
  )
}

/**
 * useOpsRealtimeTraffic manages cancellable realtime traffic fetches and zeroed
 * placeholders when monitoring is disabled.
 */
export function useOpsRealtimeTraffic(options: UseOpsRealtimeTrafficOptions) {
  const realtimeTrafficSummary = ref<OpsRealtimeTrafficSummary | null>(null)
  const realtimeTrafficLoading = ref(false)

  let requestSeq = 0
  let activeController: AbortController | null = null
  let disposed = false

  /**
   * makeZeroRealtimeTrafficSummary builds a zero-valued summary stamped with the
   * current filters when realtime monitoring is disabled.
   */
  function makeZeroRealtimeTrafficSummary(): OpsRealtimeTrafficSummary {
    const now = new Date().toISOString()
    return {
      window: options.window.value,
      start_time: now,
      end_time: now,
      platform: options.getPlatform(),
      group_id: options.getGroupId(),
      qps: { current: 0, peak: 0, avg: 0 },
      tps: { current: 0, peak: 0, avg: 0 },
    }
  }

  /**
   * abortInFlight cancels any active realtime traffic request.
   */
  function abortInFlight() {
    if (activeController) {
      activeController.abort()
      activeController = null
    }
  }

  /**
   * loadRealtimeTrafficSummary cancels any superseded request and fetches the
   * latest window, platform, and group summary. Only the newest non-disposed
   * request generation updates display or loading state.
   */
  async function loadRealtimeTrafficSummary(): Promise<void> {
    const seq = ++requestSeq
    abortInFlight()

    if (!options.isMonitoringEnabled()) {
      realtimeTrafficLoading.value = false
      realtimeTrafficSummary.value = makeZeroRealtimeTrafficSummary()
      return
    }

    const controller = new AbortController()
    activeController = controller
    realtimeTrafficLoading.value = true

    try {
      const res = await opsAPI.getRealtimeTrafficSummary(
        options.window.value,
        options.getPlatform(),
        options.getGroupId(),
        { signal: controller.signal },
      )
      if (disposed || seq !== requestSeq) return
      if (res && res.enabled === false) {
        options.onServerDisabled()
      }
      realtimeTrafficSummary.value = res?.summary ?? null
    } catch (err) {
      if (disposed || seq !== requestSeq || isAbortRejection(err)) return
      // Clear stale measurements on genuine transport failure while ignoring cancellations.
      console.error('[OpsDashboardHeader] Failed to load realtime traffic summary', err)
      realtimeTrafficSummary.value = null
    } finally {
      if (!disposed && seq === requestSeq) {
        realtimeTrafficLoading.value = false
        activeController = null
      }
    }
  }

  /**
   * applyDisabledMonitoringState cancels in-flight requests and publishes a
   * zero summary immediately when realtime monitoring is switched off.
   */
  function applyDisabledMonitoringState() {
    requestSeq++
    abortInFlight()
    realtimeTrafficLoading.value = false
    realtimeTrafficSummary.value = makeZeroRealtimeTrafficSummary()
  }

  onBeforeUnmount(() => {
    disposed = true
    requestSeq++
    abortInFlight()
  })

  return {
    realtimeTrafficSummary,
    realtimeTrafficLoading,
    loadRealtimeTrafficSummary,
    applyDisabledMonitoringState,
    makeZeroRealtimeTrafficSummary,
  }
}
