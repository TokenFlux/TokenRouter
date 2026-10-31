import { defineStore } from 'pinia'
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { getLocale } from '@/i18n'
import { getPluginUI } from '@/api/plugins'
import type { PluginUI } from '@/api/plugins.types'
import { useAuthStore } from './auth'

// plugins.ts caches visible contribution descriptions within one account, role,
// and language scope. A scope change cancels prior work and clears its menus
// before another response can publish. Ordinary callers share an active read;
// explicit administrative refreshes supersede it so pre-mutation responses cannot
// replace a newer installation observation.
//
// One timer refreshes discovery every five minutes while authenticated. A failed
// read retains same-scope content for navigation recovery, while the emergency
// disabled response clears contributions without disturbing unrelated pages.
// Disposal cancels requests and releases the timer with the owning Pinia scope.

// Discovery refreshes at five-minute intervals without persisting role-specific menus
// to browser storage; every authenticated scope starts with a fresh in-memory read.
const REFRESH_INTERVAL = 5 * 60 * 1000

/** usePluginsStore coordinates scoped contribution discovery for navigation and page loading.
 * Install Pinia and authentication first. Consumers treat published descriptions
 * as immutable and call refreshUi after successful administration changes.
 *
 * Request generations combine the authenticated account, role, and current locale.
 * Responses publish only while their captured generation remains current, so a
 * late administrator response cannot populate a later user's menu. The same
 * ownership check protects loading cleanup and active-promise replacement after cancellation.
 */
export const usePluginsStore = defineStore('plugins', () => {
  // Public state describes only the active authentication scope; request ownership
  // below determines which completion may replace these observations and busy flags.
  const auth = useAuthStore()
  const plugins = shallowRef<PluginUI[]>([])
  const loading = ref(false)
  const error = shallowRef<unknown>(null)
  const loadedAt = ref(0)
  const disabled = ref(false)
  const scope = computed(() =>
    auth.isAuthenticated ? `${auth.user?.id}:${auth.user?.role}:${getLocale()}` : '',
  )

  // Generation, controller, and pending belong to the current read; interval owns
  // periodic refresh only while an authenticated scope exists and the store is alive.
  let generation = 0
  let controller: AbortController | null = null
  let pending: Promise<PluginUI[]> | null = null
  let interval: ReturnType<typeof setInterval> | null = null

  /** refreshUi replaces the current read and returns fresh visible descriptions.
   * Unauthenticated callers receive an empty list. An administrative refresh
   * aborts earlier reads, and stale completion cannot replace current data or flags.
   *
   * The new generation is reserved before awaiting HTTP. Every success, failure,
   * and cleanup checks that generation, preserving a later request when cancellation
   * races with response delivery. The global disabled reason is an ordinary empty
   * contribution state; other failures remain available for page-level recovery.
   */
  function refreshUi(): Promise<PluginUI[]> {
    if (!scope.value) return Promise.resolve([])
    // Replace cancellation ownership before dispatch so administrative refreshes cannot
    // publish an older installation observation after their new request begins.
    controller?.abort()
    controller = new AbortController()
    const current = ++generation
    loading.value = true
    error.value = null
    const request = getPluginUI(controller.signal)
      .then((response) => {
        if (current !== generation) throw new DOMException('Plugin scope changed', 'AbortError')
        plugins.value = response.plugins
        loadedAt.value = Date.now()
        disabled.value = false
        return plugins.value
      })
      .catch((failure: unknown) => {
        if (current !== generation) throw failure
        const reason =
          failure && typeof failure === 'object'
            ? (failure as { reason?: unknown; code?: unknown })
            : {}
        // The emergency switch removes navigation as an ordinary empty result;
        // retaining stale links here would keep offering pages the server explicitly disabled.
        if (reason.reason === 'PLUGINS_DISABLED' || reason.code === 'PLUGINS_DISABLED') {
          plugins.value = []
          disabled.value = true
          loadedAt.value = Date.now()
          return []
        }
        error.value = failure
        loadedAt.value = 0
        throw failure
      })
      .finally(() => {
        // Only the current owner can release busy state; an aborted older request
        // must not erase the promise or cancellation handle of its replacement.
        if (current === generation) {
          loading.value = false
          pending = null
          controller = null
        }
      })
    pending = request
    return request
  }

  /** ensureUi shares active work or reuses descriptions inside the refresh window.
   * Stale data starts a fresh read. Page consumers await this promise before
   * selecting their authorized audience and path from the current description list.
   */
  function ensureUi(): Promise<PluginUI[]> {
    if (!scope.value) return Promise.resolve([])
    if (pending) return pending
    if (loadedAt.value && Date.now() - loadedAt.value < REFRESH_INTERVAL)
      return Promise.resolve(plugins.value)
    return refreshUi()
  }

  /** resetScope clears obsolete menu ownership and starts the new authenticated read.
   * Old requests are canceled before state resets. One periodic timer is replaced
   * with the scope, and background failures remain in error without unhandled rejections.
   */
  function resetScope(): void {
    generation++
    controller?.abort()
    controller = null
    pending = null
    plugins.value = []
    loadedAt.value = 0
    loading.value = false
    error.value = null
    disabled.value = false
    if (interval) clearInterval(interval)
    interval = null
    if (scope.value) {
      // Background reads publish their failure into store state; consume rejection
      // here because a timer has no page caller to receive the rejected promise.
      void ensureUi().catch(() => undefined)
      interval = setInterval(() => {
        void refreshUi().catch(() => undefined)
      }, REFRESH_INTERVAL)
    }
  }

  watch(scope, resetScope, { immediate: true, flush: 'sync' })
  onScopeDispose(() => {
    // Seal request publication before stopping resources so late HTTP completions
    // cannot repopulate state after the application disposes this store scope.
    generation++
    controller?.abort()
    if (interval) clearInterval(interval)
  })

  return { plugins, loading, error, disabled, ensureUi, refreshUi }
})
