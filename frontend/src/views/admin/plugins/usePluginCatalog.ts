import { onBeforeUnmount, ref, shallowRef, watch, type Ref } from 'vue'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginCatalogEntry, PluginIndex } from '@/api/admin/plugins.types'

// usePluginCatalog.ts owns catalog and source observations for the plugin
// directory's discover view and the sources page. Each read generation cancels its
// predecessor and publishes only while this page remains mounted. Source diagnostics
// and market entries can fail independently, preserving accepted rows while the
// administrator retries a rejected transfer.
//
// Catalog downloads remain backend-owned even when a browser waiter is canceled.
// Explicit refresh therefore waits for verification and then rereads both views,
// including after failure. Polling source diagnostics runs only while the owner
// reports the view as visible and idle; installed process polling has its own
// controller. Closing this owner cancels readers and timers without claiming server
// writes were undone.

/** usePluginCatalog coordinates cancellable market reads and explicit source verification.
 * Call during page setup with a reactive visibility flag for the catalog view. Existing
 * rows survive refresh errors, and disposal prevents old responses from republishing.
 *
 * The same generation owns both read outcomes and loading cleanup. Source results
 * can still explain a failed market read because each endpoint settles independently.
 * Explicit verification owns a separate refreshing flag, so subsequent readback
 * cannot enable a second refresh before the first attempt and its observations finish.
 */
export function usePluginCatalog(visible: Ref<boolean>) {
  const entries = shallowRef<PluginCatalogEntry[]>([])
  const indexes = shallowRef<PluginIndex[]>([])
  const loading = ref(false)
  const refreshing = ref(false)
  const error = shallowRef<unknown>(null)
  let generation = 0
  let active = true
  let controller: AbortController | null = null

  /** load replaces observation requests while retaining earlier rows on individual failures.
   * A newer generation exclusively owns publication and cleanup. Cancellation from
   * replacement or disposal never changes another read's error or loading state.
   *
   * The two endpoints settle independently within one browser read generation.
   * A source failure can therefore remain explainable beside retained catalog
   * entries, without claiming both responses are one database snapshot. Only
   * this generation clears loading; a superseded request cannot make a newer
   * refresh appear complete while its own responses are still pending.
   */
  async function load(): Promise<void> {
    if (!active) return
    const current = ++generation
    controller?.abort()
    controller = new AbortController()
    loading.value = true
    error.value = null
    const results = await Promise.allSettled([
      pluginsAPI.catalog(controller.signal),
      pluginsAPI.indexes(controller.signal),
    ])
    if (!active || current !== generation) return
    if (results[0].status === 'fulfilled') entries.value = results[0].value
    else error.value = results[0].reason
    if (results[1].status === 'fulfilled') indexes.value = results[1].value
    else error.value ??= results[1].reason
    loading.value = false
    controller = null
  }

  /** refresh requests verification and rereads observations even when one source rejects it.
   * Repeated activation is ignored while pending. The action's failure remains
   * visible after readback, which may reveal independently successful source changes.
   *
   * Verification remains the action the administrator initiated, so its failure
   * takes precedence over a later observation failure. A successful readback can
   * update rows without hiding the rejected verification. The refresh lock remains
   * held through both operations, preventing repeated clicks from interleaving
   * unrelated attempts and presenting their diagnostics as one completed action.
   */
  async function refresh(): Promise<void> {
    if (!active || refreshing.value) return
    refreshing.value = true
    let failure: unknown = null
    try {
      await pluginsAPI.refreshCatalog()
    } catch (caught) {
      failure = caught
    } finally {
      // Readback follows every verification outcome because successful independent
      // sources may already have published even when the aggregate refresh failed.
      if (active) {
        await load()
        if (failure) error.value = failure
        refreshing.value = false
      }
    }
  }

  watch(
    visible,
    (shown) => {
      if (shown) void load()
    },
    { immediate: true },
  )
  // Source transfers finish independently of page requests. Visible idle polling
  // replaces pending indicators with their saved outcome without requiring navigation.
  const timer = setInterval(() => {
    if (
      visible.value &&
      !loading.value &&
      !refreshing.value &&
      document.visibilityState === 'visible'
    )
      void load()
  }, 10000)
  onBeforeUnmount(() => {
    active = false
    generation++
    controller?.abort()
    clearInterval(timer)
  })
  return { entries, indexes, loading, refreshing, error, load, refresh }
}
