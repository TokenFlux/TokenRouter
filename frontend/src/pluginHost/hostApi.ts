import { computed, onScopeDispose, readonly, shallowRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  HostRequestError,
  type ConfirmOptions,
  type HostApi,
  type HostRoute,
  type HttpMethod,
  type RequestOptions,
} from '@tokenflux/tokenrouter-plugin-sdk'
import type { PluginPage, PluginUI } from '@/api/plugins.types'
import { requestPlugin } from '@/api/plugins'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { useTheme } from '@/composables/useTheme'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { getLocale } from '@/i18n'

// hostApi.ts creates the capability object owned by one mounted plugin page.
// Requests and navigation remain inside that installation and audience. Account
// information is projected into immutable presentation fields, while authentication
// credentials remain inside the shared HTTP client. The object has the lifetime of
// its mounting scope and rejects new requests after disposal or an account change.
//
// Confirmation owns one pending resolver per page. A replacement cancels the older
// question, and leaving the page resolves any remaining question as declined.
// This keeps asynchronous plugin actions from resuming with approval belonging to a
// different component mount. The native dialog still owns focus and scroll behavior.

/** usePluginHost creates a scoped HostApi and its native confirmation dialog state.
 * Call inside the keyed plugin mount's setup with one accepted plugin and page.
 * The returned capability cannot be reassigned to another installation or audience.
 *
 * A captured account identity prevents retained plugin callbacks from issuing new
 * requests under a later login. Reactive user, locale, theme, route, and site fields
 * expose current presentation only. Confirmation replacement and scope disposal
 * settle the old promise before releasing its owner, preventing unresolved actions.
 */
export function usePluginHost(plugin: PluginUI, page: PluginPage) {
  const auth = useAuthStore()
  const app = useAppStore()
  const router = useRouter()
  const route = useRoute()
  const { isDark } = useTheme()
  const { balanceUnitName, balanceUnitSymbol } = useBalanceDisplay()
  const accountId = auth.user?.id
  const accountRole = auth.user?.role
  const confirmation = shallowRef<ConfirmOptions | null>(null)
  let resolveConfirmation: ((accepted: boolean) => void) | null = null
  let active = true

  /** ensureActive rejects actions retained beyond this page or its authenticated account.
   * Authentication refresh for the same account remains valid. A disposed page or
   * changed account must mount a fresh capability before issuing another scoped action.
   */
  function ensureActive(): void {
    if (
      !active ||
      !auth.isAuthenticated ||
      auth.user?.id !== accountId ||
      auth.user?.role !== accountRole
    ) {
      throw new HostRequestError('Plugin page is no longer active', 401, 'PLUGIN_CONTEXT_CLOSED')
    }
  }

  /** settleConfirmation closes the current question and resolves its pending decision.
   * Clear ownership before invoking the resolver. A plugin continuation can then
   * ask another question without inheriting or clearing the preceding question's state.
   */
  function settleConfirmation(accepted: boolean): void {
    const resolve = resolveConfirmation
    resolveConfirmation = null
    confirmation.value = null
    resolve?.(accepted)
  }

  /** confirm replaces any pending question with a native page-owned confirmation.
   * Replacement declines the old question. Scope disposal also declines it, so
   * confirmation cannot remain pending after its plugin component has left the page.
   */
  function confirm(options: ConfirmOptions): Promise<boolean> {
    ensureActive()
    settleConfirmation(false)
    confirmation.value = { ...options }
    return new Promise((resolve) => {
      resolveConfirmation = resolve
    })
  }

  /** request sends an authenticated call under the captured installation namespace.
   * Caller cancellation is preserved as a stable error code. Backend reasons take
   * precedence over numeric envelope codes when constructing the SDK error value.
   */
  async function request<T>(
    method: HttpMethod,
    path: string,
    options?: RequestOptions,
  ): Promise<T> {
    ensureActive()
    try {
      return await requestPlugin<T>(plugin.id, method, path, options)
    } catch (failure) {
      // Normalize transport objects and authoring errors at the SDK boundary, retaining
      // stable host reasons while giving failures without an HTTP response status zero.
      const error =
        failure && typeof failure === 'object' ? (failure as Record<string, unknown>) : {}
      throw new HostRequestError(
        typeof error.message === 'string' ? error.message : 'Plugin request failed',
        typeof error.status === 'number' ? error.status : 0,
        typeof error.reason === 'string'
          ? error.reason
          : typeof error.code === 'string'
            ? error.code
            : 'PLUGIN_REQUEST_FAILED',
      )
    }
  }

  /** navigate selects a declared page inside this plugin's current audience.
   * Paths use the manifest spelling, including an empty root path. Exact declaration
   * lookup rejects external URLs, traversal, and pages belonging to another audience.
   */
  function navigate(path: string, query?: Record<string, string>): void {
    ensureActive()
    if (
      !plugin.pages.some(
        (candidate) => candidate.audience === page.audience && candidate.path === path,
      )
    ) {
      throw new HostRequestError('Plugin page does not exist', 404, 'PLUGIN_PAGE_NOT_FOUND')
    }
    const prefix = page.audience === 'admin' ? '/admin/ext' : '/ext'
    void router.push({ path: `${prefix}/${plugin.id}${path ? `/${path}` : ''}`, query })
  }

  /** refreshAccount reloads the current account after a completed plugin wallet action. */
  async function refreshAccount(): Promise<void> {
    ensureActive()
    await auth.refreshUser()
  }

  // Router arrays collapse to their first string because HostRoute's public query
  // contract is single-valued; null entries represent absent values and are omitted.
  const hostRoute = computed<HostRoute>(() => ({
    pageId: page.id,
    path: page.path,
    query: Object.fromEntries(
      Object.entries(route.query).flatMap(([key, value]) => {
        const first = Array.isArray(value) ? value[0] : value
        return typeof first === 'string' ? [[key, first]] : []
      }),
    ),
  }))
  const host: HostApi = Object.freeze({
    pluginId: plugin.id,
    version: plugin.version,
    apiVersion: 1 as const,
    user: readonly(
      computed(() =>
        auth.user
          ? {
              id: auth.user.id,
              username: auth.user.username,
              role: auth.isAdmin ? ('admin' as const) : ('user' as const),
            }
          : null,
      ),
    ),
    locale: computed(getLocale),
    theme: computed(() => (isDark.value ? ('dark' as const) : ('light' as const))),
    site: Object.freeze({
      /** name exposes the current public site name without a mutable settings reference. */
      get name() {
        return app.siteName
      },
      /** balanceUnitName exposes the resolved native balance label for plugin presentation. */
      get balanceUnitName() {
        return balanceUnitName.value
      },
      /** balanceUnitSymbol exposes the resolved native balance symbol for plugin presentation. */
      get balanceUnitSymbol() {
        return balanceUnitSymbol.value
      },
      /** timezone exposes the host's configured IANA timezone when settings are available.
       * Before a successful public settings read, UTC supplies an explicit provisional
       * value and later reads observe the published server configuration.
       */
      get timezone() {
        return app.cachedPublicSettings?.server_timezone || 'UTC'
      },
    }),
    route: readonly(hostRoute),
    request,
    navigate,
    refreshAccount,
    toast: Object.freeze({ success: app.showSuccess, error: app.showError, info: app.showInfo }),
    confirm,
  })
  onScopeDispose(() => {
    // Close capability admission before resolving a pending question, so continuations
    // from the departing plugin cannot start authenticated work through this mount.
    active = false
    settleConfirmation(false)
  })
  return { host, confirmation, settleConfirmation }
}
