import { onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginInstallation } from '@/api/admin/plugins.types'
import { usePluginsStore } from '@/stores/plugins'
import { useStepUp } from '@/composables/useStepUp'

// usePluginAdministration.ts owns installed-list reads and serialized management actions.
// A single step-up controller cannot serve overlapping decisions, so all lifecycle
// writes share one busy state. List reads use generations and cancellation to keep
// an earlier observation from replacing a post-mutation refresh or another request.
// Background polling runs only while the page is visible and otherwise idle.
//
// Every completed write attempt refreshes both installation observations and frontend
// contributions, including attempts whose transport outcome is unknown. This readback
// preserves backend state as the authority when a disable saved intent before drain
// failed or an installation succeeded before its response was lost. Refresh failures
// remain visible in list state without replacing the original action's outcome.

/** usePluginAdministration coordinates page-owned reads, step-up, and mutation readback.
 * Call during the management page's setup. UI callers review destructive choices
 * before invoking run, then handle its unchanged action result or failure.
 *
 * Only one action owns the step-up controller at a time. Completion invalidates
 * older list reads before fetching current installation and menu state, preventing
 * a pre-action response from reviving removed or disabled entries. Unmount cancels
 * reads and verification and prevents later action completion from publishing into
 * another page instance; dispatched server writes are not assumed to roll back.
 */
export function usePluginAdministration() {
  const discovery = usePluginsStore()
  const stepUp = useStepUp()
  const installations = shallowRef<PluginInstallation[]>([])
  const loading = ref(false)
  const busy = ref(false)
  const error = shallowRef<unknown>(null)
  let active = true
  let generation = 0
  let controller: AbortController | null = null
  let timer: ReturnType<typeof setInterval> | null = null

  /** load replaces the current list read and retains existing rows on failure.
   * Its generation owns publication and loading cleanup. Cancellation caused by
   * replacement or unmount cannot erase a newer result or display an obsolete failure.
   */
  async function load(): Promise<void> {
    if (!active) return
    const current = ++generation
    controller?.abort()
    controller = new AbortController()
    loading.value = true
    error.value = null
    try {
      const result = await pluginsAPI.list(controller.signal)
      if (active && current === generation) installations.value = result
    } catch (failure) {
      if (active && current === generation) error.value = failure
    } finally {
      if (active && current === generation) {
        loading.value = false
        controller = null
      }
    }
  }

  /** run serializes a reviewed mutation and reconciles state after either outcome.
   * The action may be retried once by native step-up before mutation is admitted.
   * Post-action reads never replace a successful persistence result with a refresh failure.
   */
  async function run<T>(action: () => Promise<T>): Promise<T> {
    if (busy.value || !active) throw new Error('Another plugin operation is pending')
    busy.value = true
    try {
      return await stepUp.run(action)
    } finally {
      // Read back even rejected writes because the backend can save disabled intent
      // before draining fails, and transport failure alone cannot prove a write was absent.
      if (active) await Promise.allSettled([load(), discovery.refreshUi()])
      busy.value = false
    }
  }

  /** refreshAfterChange reconciles an operation completed by a child editor or uploader.
   * Child dialogs and editor tabs own their own serialized step-up interactions.
   * Their successful persistence notifications trigger the same list and
   * contribution reads as lifecycle actions.
   */
  async function refreshAfterChange(): Promise<void> {
    if (active) await Promise.allSettled([load(), discovery.refreshUi()])
  }

  onMounted(() => {
    void load()
    // Runtime observations change independently of operator actions; ten-second polling
    // refreshes visible idle lists without competing with a write's required readback.
    timer = setInterval(() => {
      if (!busy.value && !loading.value && document.visibilityState === 'visible') void load()
    }, 10000)
  })
  onBeforeUnmount(() => {
    active = false
    generation++
    controller?.abort()
    stepUp.onCancel()
    if (timer) clearInterval(timer)
  })
  return { installations, loading, busy, error, stepUp, load, run, refreshAfterChange }
}
