import type { PluginDefinition } from '@tokenflux/tokenrouter-plugin-sdk'
import type { PluginUI, PluginAsset } from '@/api/plugins.types'
import { buildApiUrl } from '@/api/url'
import { expectRegistration, forgetRegistration, registeredDefinition } from './registry'

// loader.ts installs integrity-checked plugin bundles once per accepted asset
// identity. Styles finish before the IIFE script is inserted, and the script must
// synchronously register its pages. Pending callers share one promise; failed or
// timed-out loads release elements, listeners, and registration before retry.
//
// A plugin's newest requested identity controls publication. Older loads cannot
// remove a newer bundle when network completion arrives out of order. Successful
// replacement removes previous styles for that plugin, preventing two releases'
// scoped rules from accumulating on the same page root after an update or repair.

/** BundleLoad owns the promise, DOM resources, and cancellation for one asset identity. */
interface BundleLoad {
  id: string // Host-selected plugin installation identity.
  key: string // Version and integrity-derived load identity.
  controller: AbortController // Pending resource cancellation owner.
  nodes: (HTMLLinkElement | HTMLScriptElement)[] // Owned stylesheet and script elements.
  promise: Promise<PluginDefinition> // Shared registration completion result.
  // Ready bundles retain their styles while the same installation's replacement
  // loads; pending obsolete bundles are canceled when discovery changes their identity.
  ready: boolean
}

// Bundle caches retain successful definitions and in-flight work. Desired keys
// select the newest request for each plugin, while the timeout bounds the entire load.
const bundles = new Map<string, BundleLoad>()
const desired = new Map<string, string>()
const LOAD_TIMEOUT = 15000

/** pluginBundleKey includes resource identities so a same-version repair loads fresh content. */
export function pluginBundleKey(plugin: PluginUI): string {
  return JSON.stringify([
    plugin.id,
    plugin.version,
    plugin.script.url,
    plugin.script.integrity,
    plugin.styles.map((style) => [style.url, style.integrity]),
  ])
}

/** loadPlugin shares one bounded load for an installed frontend contribution.
 * Initialize host globals first. Script or style failure removes this attempt,
 * allowing the page controller to refresh discovery and retry once.
 *
 * The desired key is selected before cache lookup, so every navigation has one
 * current bundle identity. Resource events and the global timeout share an abort
 * controller, which also releases pending promise listeners. A completed older
 * request cannot replace the latest selected bundle or remove its style elements.
 */
export function loadPlugin(plugin: PluginUI): Promise<PluginDefinition> {
  const key = pluginBundleKey(plugin)
  desired.set(plugin.id, key)
  const existing = bundles.get(key)
  if (existing) return existing.promise
  const controller = new AbortController()
  const entry: BundleLoad = {
    id: plugin.id,
    key,
    controller,
    nodes: [],
    promise: Promise.resolve({ pages: {} }),
    ready: false,
  }
  const timeout = window.setTimeout(
    () => controller.abort(new Error('Plugin loading timed out')),
    LOAD_TIMEOUT,
  )
  const promise = installBundle(plugin, entry)
    .then((definition) => {
      if (desired.get(plugin.id) !== key) throw new Error('Plugin load was superseded')
      entry.ready = true
      // Retire old CSS only after the replacement registers successfully, keeping
      // a failed update from removing resources that another mounted page still owns.
      for (const [oldKey, old] of bundles) {
        if (old.id === plugin.id && oldKey !== key) removeBundle(old)
      }
      return definition
    })
    .catch((error: unknown) => {
      removeBundle(entry)
      throw error
    })
    .finally(() => window.clearTimeout(timeout))
  entry.promise = promise
  bundles.set(key, entry)
  return promise
}

/** installBundle loads all declared styles before executing the accepted IIFE script.
 * Each resource carries SRI and anonymous cross-origin mode. The bounded controller
 * remains owned by loadPlugin, and cancellation prevents later script insertion.
 */
async function installBundle(plugin: PluginUI, entry: BundleLoad): Promise<PluginDefinition> {
  await Promise.all(
    plugin.styles.map((style) => {
      const link = document.createElement('link')
      link.rel = 'stylesheet'
      link.href = assetRequestURL(style)
      link.integrity = style.integrity
      link.crossOrigin = 'anonymous'
      link.dataset.pluginId = plugin.id
      entry.nodes.push(link)
      return appendResource(link, entry.controller.signal)
    }),
  )
  if (entry.controller.signal.aborted) throw entry.controller.signal.reason
  const script = document.createElement('script')
  script.src = assetRequestURL(plugin.script)
  script.integrity = plugin.script.integrity
  script.crossOrigin = 'anonymous'
  script.async = true
  script.dataset.pluginId = plugin.id
  script.dataset.pluginKey = entry.key
  expectRegistration(entry.key, plugin.id, script)
  entry.nodes.push(script)
  await appendResource(script, entry.controller.signal)
  return registeredDefinition(entry.key)
}

/** appendResource waits for a resource event while retaining cancellation cleanup.
 * Listeners are installed before insertion. Every completion clears them and
 * removes its abort listener, preventing canceled loads from retaining promise callbacks.
 */
function appendResource(
  node: HTMLLinkElement | HTMLScriptElement,
  signal: AbortSignal,
): Promise<void> {
  return new Promise((resolve, reject) => {
    /** finish releases event ownership before publishing this resource's result.
     * Load, error, and abort all clear the handlers first. The pending promise
     * then receives exactly the outcome associated with this admitted resource.
     */
    const finish = (error?: unknown): void => {
      node.onload = null
      node.onerror = null
      signal.removeEventListener('abort', aborted)
      if (error) reject(error)
      else resolve()
    }
    /** aborted reports the owning load's cancellation reason to its pending resource. */
    const aborted = (): void => finish(signal.reason || new Error('Plugin load was canceled'))
    node.onload = () => finish()
    node.onerror = () => finish(new Error('Plugin resource could not be loaded'))
    signal.addEventListener('abort', aborted, { once: true })
    if (signal.aborted) aborted()
    else document.head.appendChild(node)
  })
}

/** removeBundle cancels pending work and releases only this attempt's DOM ownership.
 * A failed older attempt cannot delete a newer cache entry with the same key.
 * Successful entries are also removable when their installation disappears or changes.
 */
function removeBundle(entry: BundleLoad): void {
  entry.controller.abort(new Error('Plugin bundle was released'))
  for (const node of entry.nodes) node.remove()
  if (bundles.get(entry.key) === entry) {
    bundles.delete(entry.key)
    forgetRegistration(entry.key)
  }
}

/** retainPluginBundles reconciles cached resources with current contribution visibility.
 * Call after discovery changes or authentication ends. Removed installations release
 * everything; changed identities cancel pending loads but retain ready styles until replacement succeeds.
 */
export function retainPluginBundles(plugins: readonly PluginUI[]): void {
  const keys = new Set(plugins.map(pluginBundleKey))
  const ids = new Set(plugins.map((plugin) => plugin.id))
  for (const entry of bundles.values()) {
    if (!ids.has(entry.id) || (!keys.has(entry.key) && !entry.ready)) removeBundle(entry)
  }
  for (const [id, key] of desired) {
    if (!keys.has(key)) desired.delete(id)
  }
}

/** assetRequestURL binds browser caching to accepted content instead of version alone.
 * A repaired same-version package may contain different bytes. Its public integrity
 * value changes the cache key while the backend still serves the same bounded file path.
 */
function assetRequestURL(asset: PluginAsset): string {
  const url = new URL(buildApiUrl(asset.url), document.baseURI)
  url.searchParams.set('v', asset.integrity)
  return url.href
}
