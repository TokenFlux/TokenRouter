import {
  computed,
  createApp,
  defineAsyncComponent,
  defineComponent,
  h,
  inject,
  provide,
  readonly,
  ref,
} from 'vue'
import { HostRequestError, normalizePluginPage } from '../types.js'
import type {
  HostApi,
  HostRoute,
  Messages,
  PluginDefinition,
  RequestOptions,
  Translator,
} from '../types.js'

// index.ts mounts plugin pages in a local mock host during Vite development.
// It provides account context, navigation, language selection, and a small
// in-memory check-in response fixture. Production bundles externalize the SDK
// and therefore never contain this mount path or its simulated business state.
//
// One app owns the registered definition and current page selection. Request
// fixtures are explicitly replaceable by authors, and unknown endpoints fail
// rather than producing a fabricated successful response. Page components use
// the same injected HostApi shape as production, keeping their integration
// independent of the mock's native-element component implementations.

export * from '../types.js'
export * from './components.js'

declare const __TR_PLUGIN_DEV_ID__: string

/** MockRequestHandler supplies author-defined data for local development requests. */
export type MockRequestHandler = (
  method: string,
  path: string,
  options?: RequestOptions,
) => Promise<unknown>

// Mock state belongs to this development page and resets on full browser reload.
// These values are never persisted into the host or packaged as production assets.
const hostKey = Symbol.for('tokenrouter.plugin.host.v1')
const locale = ref('en')
const theme = ref<'light' | 'dark'>('light')
const route = ref<HostRoute>({ pageId: 'home', path: '', query: {} })
const notification = ref('')
const balance = ref(1)
let checkedIn = false
let requestHandler: MockRequestHandler = defaultRequest
let mounted = false

/** configureMock replaces local request fixtures before author interactions run. */
export function configureMock(handler: MockRequestHandler): void {
  requestHandler = handler
}

/** defaultRequest implements only the example's explicit local check-in fixtures.
 * Unknown routes return an error. All state is confined to this development
 * page, so successful responses cannot be mistaken for a persisted host operation.
 */
async function defaultRequest(method: string, path: string): Promise<unknown> {
  if (method === 'GET' && path === '/status')
    return { checked_in: checkedIn, reward: '0.1', notice: '' }
  if (method === 'POST' && path === '/checkin') {
    if (checkedIn) throw new HostRequestError('Already checked in', 409, 'ALREADY_CHECKED_IN')
    checkedIn = true
    balance.value += 0.1
    return { reward: '0.1' }
  }
  if (method === 'GET' && path === '/admin/history') {
    return {
      rows: checkedIn
        ? [
            {
              user_id: 1,
              day: new Date().toISOString().slice(0, 10),
              amount: '0.1',
              completed: true,
              at: Math.floor(Date.now() / 1000),
            },
          ]
        : [],
      next_cursor: '',
    }
  }
  throw new HostRequestError(
    'Configure a mock response for this endpoint',
    501,
    'MOCK_NOT_CONFIGURED',
  )
}

/** createHost creates a mock that satisfies the production frontend contract. */
function createHost(): HostApi {
  return {
    pluginId: __TR_PLUGIN_DEV_ID__,
    version: 'development',
    apiVersion: 1,
    user: readonly(ref({ id: 1, username: 'Developer', role: 'admin' as const })),
    locale: readonly(locale),
    theme: readonly(theme),
    route: readonly(route),
    site: {
      name: 'TokenRouter',
      balanceUnitName: 'USD',
      balanceUnitSymbol: '$',
      timezone: 'UTC',
    },
    /** request invokes the selected local fixture while honoring caller cancellation.
     * Cancellation is checked before dispatch and before returning its result.
     * It does not undo fixture state already changed by a dispatched request.
     */
    async request<T>(method: string, path: string, options?: RequestOptions): Promise<T> {
      options?.signal?.throwIfAborted()
      const result = await requestHandler(method, path, options)
      options?.signal?.throwIfAborted()
      return result as T
    },
    /** navigate selects a registered mock page using its local page identifier.
     * Empty paths select home. The mock has no manifest route table, so authors
     * use registered page IDs when exercising navigation in this development shell.
     */
    navigate(path: string, query = {}) {
      route.value = { pageId: path || 'home', path, query }
    },
    /** refreshAccount resolves after fixture requests have updated the shared balance.
     * The mock has no remote account service to reload. Production implementations
     * replace this behavior with the authenticated host account refresh operation.
     */
    async refreshAccount() {},
    toast: {
      /** success displays the latest successful action message in the mock shell. */
      success(message: string) {
        notification.value = message
      },
      /** error displays the latest failed action message in the mock shell. */
      error(message: string) {
        notification.value = message
      },
      /** info displays the latest informational message in the mock shell. */
      info(message: string) {
        notification.value = message
      },
    },
    /** confirm asks for an author action through the browser's native dialog. */
    async confirm(options) {
      return window.confirm(`${options.title}\n\n${options.message}`)
    },
  }
}

/** definePlugin mounts one author definition inside the local development host.
 * Repeated registration fails to surface duplicate entry execution. Synchronous
 * component definitions and asynchronous page loaders share the same page selector.
 */
export function definePlugin(definition: PluginDefinition): void {
  if (mounted) throw new Error('A plugin is already mounted in this development host')
  const pages = Object.keys(definition.pages)
  if (pages.length === 0) throw new Error('Plugin must register at least one page')
  route.value.pageId = pages.includes('home') ? 'home' : pages[0]!
  mounted = true
  installStyles()
  const host = createHost()
  const components = Object.fromEntries(
    Object.entries(definition.pages).map(([id, page]) => [
      id,
      normalizePluginPage(page, { h, defineAsyncComponent }),
    ]),
  )
  const root = document.createElement('div')
  root.className = 'tr-dev-host'
  document.body.append(root)
  createApp(
    defineComponent({
      setup() {
        provide(hostKey, host)
        const page = computed(() => components[route.value.pageId])
        return () =>
          h('div', { 'data-theme': theme.value }, [
            h('header', { class: 'tr-dev-toolbar' }, [
              h('strong', 'TokenRouter'),
              h(
                'select',
                {
                  value: route.value.pageId,
                  'aria-label': 'Page',
                  onChange: (event: Event) => {
                    route.value.pageId = (event.target as HTMLSelectElement).value
                  },
                },
                pages.map((id) => h('option', { value: id }, id)),
              ),
              h(
                'select',
                {
                  value: locale.value,
                  'aria-label': 'Language',
                  onChange: (event: Event) => {
                    locale.value = (event.target as HTMLSelectElement).value
                  },
                },
                [h('option', { value: 'en' }, 'English'), h('option', { value: 'zh' }, '中文')],
              ),
              h(
                'button',
                {
                  type: 'button',
                  onClick: () => {
                    theme.value = theme.value === 'light' ? 'dark' : 'light'
                  },
                },
                theme.value === 'light' ? 'Dark theme' : 'Light theme',
              ),
              h('span', `$${balance.value.toFixed(2)}`),
            ]),
            h(
              'main',
              { 'data-tr-plugin': host.pluginId, class: 'tr-dev-page' },
              page.value ? h(page.value, { key: route.value.pageId }) : 'Page not found',
            ),
            notification.value
              ? h('div', { role: 'status', class: 'tr-dev-notification' }, notification.value)
              : null,
          ])
      },
    }),
  ).mount(root)
}

/** useHost retrieves the mock interface provided by the mounted development page.
 * Call it during component setup or synchronous functional rendering. Components
 * mounted outside the mock root fail explicitly rather than borrowing another
 * plugin's account or request context.
 */
export function useHost(): HostApi {
  const host = inject<HostApi>(hostKey)
  if (!host) throw new Error('Plugin host is unavailable outside a plugin page')
  return host
}

/** useMessages follows the injected locale with base-language and English fallback.
 * Interpolation substitutes named values as plain text. Missing message keys
 * remain visible so authors can detect incomplete local translation dictionaries.
 */
export function useMessages(messages: Messages): Translator {
  const host = useHost()
  return (key, params = {}) => {
    const tag = host.locale.value
    const text = (messages[tag] ?? messages[tag.split('-')[0]!] ?? messages.en ?? {})[key] ?? key
    return text.replace(/\{([^{}]+)\}/g, (match, name: string) => String(params[name] ?? match))
  }
}

/** installStyles provides local mock surfaces using the approved host brand value.
 * These styles belong only to the development shell. Plugin selectors still
 * use their scoped stylesheet and the stable host CSS variable contract.
 */
function installStyles(): void {
  const style = document.createElement('style')
  style.textContent = `
    .tr-dev-host { --tr-color-brand:#12A7E8; --tr-color-accent:#12A7E8; --tr-color-brand-contrast:#fff; --tr-color-surface:#fff; --tr-color-surface-muted:#FAFAFA; --tr-color-border:rgba(45,79,104,.1); --tr-color-text:#18181b; --tr-color-text-muted:#71717a; --tr-color-success:#16a34a; --tr-color-warning:#d97706; --tr-color-danger:#dc2626; --radius-control:8px; --radius-surface:12px; font:14px/1.5 system-ui,sans-serif; }
    .tr-dev-host > div { min-height:100vh; color:var(--tr-color-text); background:var(--tr-color-surface-muted); }
    .tr-dev-host [data-theme=dark] { --tr-color-accent:#00D2FF; --tr-color-surface:#0F0F10; --tr-color-surface-muted:#141416; --tr-color-border:rgba(250,250,250,.078); --tr-color-text:#DEE0E2; --tr-color-text-muted:#A1A1AA; }
    body { margin:0; }
    .tr-dev-toolbar { display:flex; align-items:center; flex-wrap:wrap; gap:16px; padding:16px 24px; border-bottom:1px solid var(--tr-color-border); background:var(--tr-color-surface); }
    .tr-dev-toolbar strong { margin-right:auto; }
    .tr-dev-page { max-width:1120px; margin:0 auto; padding:32px 24px; }
    .tr-dev-card { padding:24px; background:var(--tr-color-surface); border:1px solid var(--tr-color-border); border-radius:var(--radius-surface); }
    .tr-dev-button, .tr-dev-toolbar button, .tr-dev-host select { font:inherit; min-height:36px; padding:8px 16px; border:1px solid var(--tr-color-border); border-radius:var(--radius-control); cursor:pointer; }
    .tr-dev-button.primary { color:var(--tr-color-brand-contrast); background:var(--tr-color-brand); }
    .tr-dev-button:disabled { cursor:default; }
    .tr-dev-host table { width:100%; border-collapse:collapse; text-align:left; }
    .tr-dev-host th,.tr-dev-host td { padding:12px 16px; border-bottom:1px solid var(--tr-color-border); }
    .tr-dev-skeleton { background:var(--tr-color-surface-muted); }
    .tr-dev-notification { padding:16px 24px; }
  `
  document.head.append(style)
}
