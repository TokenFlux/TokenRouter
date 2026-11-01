import * as Vue from 'vue'
import {
  CSS_VARIABLES,
  HostRequestError,
  normalizePluginPage,
  type Messages,
  type SdkRuntime,
  type Translator,
} from '@tokenflux/tokenrouter-plugin-sdk'
import { definePlugin } from './registry'
import { useHost } from './context'
import * as components from './components'
import TrCaptcha from './TrCaptcha.vue'
import { usePluginsStore } from '@/stores/plugins'
import { retainPluginBundles } from './loader'

// runtime.ts publishes the host's Vue instance and stable SDK implementation to
// externalized plugin bundles. Installation is lazy and idempotent, so applications
// without frontend contributions do not need a second Vue runtime or plugin
// initialization during their ordinary startup path. The SDK's public declarations
// verify the implementation without importing its declaration-only function exports.
//
// A detached store subscription keeps bundle ownership synchronized after pages
// unmount. Login changes, disabled plugins, and refreshed asset identities release
// obsolete DOM resources through the loader. Replacing the application store stops
// the previous subscription before attaching another, so a stale account description
// cannot remove resources belonging to the current application instance.

// The detached subscription outlives individual plugin pages so logout and
// disabled contributions release cached resources even while another native page is open.
let observedStore: ReturnType<typeof usePluginsStore> | null = null
let stopObserving: (() => void) | null = null

// One SDK object belongs to this module generation. Reinstalling that identity also
// keeps development hot reloads from retaining registration callbacks from an older registry.
const sdkRuntime = {
  ...components,
  TrCaptcha,
  definePlugin,
  useHost,
  useMessages,
  CSS_VARIABLES,
  HostRequestError,
  normalizePluginPage,
} satisfies SdkRuntime

/** PluginGlobals describes the two runtime objects consumed by compiled IIFE bundles. */
type PluginGlobals = Window & {
  __TR_VUE__?: typeof Vue // Shared host Vue runtime.
  __TR_PLUGIN_SDK__?: SdkRuntime // Stable plugin SDK implementation.
}

/** useMessages resolves plugin translations against the current reactive host locale.
 * Exact locale, base language, then English select the dictionary. Missing keys
 * remain visible, and parameter replacement returns plain text without HTML interpretation.
 */
export function useMessages(messages: Messages): Translator {
  const host = useHost()
  return (key, params = {}) => {
    const locale = host.locale.value
    // Locale fallback chooses one complete dictionary, matching the SDK contract;
    // missing keys stay visible instead of borrowing individual values from another language.
    const dictionary =
      messages[locale] ?? messages[locale.split('-')[0] ?? 'en'] ?? messages.en ?? {}
    const message = dictionary[key] ?? key
    return message.replace(/\{(\w+)\}/g, (match, name: string) =>
      Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : match,
    )
  }
}

/** initializePluginRuntime publishes globals before the first plugin script is inserted.
 * Subsequent calls reuse the installed objects. The SDK implementation is checked
 * against its independent package contract while native wrappers own their internal props.
 *
 * Resource observation belongs to the application store rather than an individual
 * page scope. This lets logout remove cached script and style elements after the
 * user has navigated to a native page. A different store replaces the previous
 * observer before publishing its own retained contribution set.
 */
export function initializePluginRuntime(): void {
  const target = window as PluginGlobals
  const store = usePluginsStore()
  if (observedStore !== store) {
    // A replaced application store takes ownership of resource retention; stop the
    // prior observer before subscribing so obsolete account state cannot remove current bundles.
    stopObserving?.()
    observedStore = store
    stopObserving = store.$subscribe(() => retainPluginBundles(store.plugins), {
      detached: true,
      flush: 'sync',
    })
    retainPluginBundles(store.plugins)
  }
  target.__TR_VUE__ = Vue
  target.__TR_PLUGIN_SDK__ = sdkRuntime
}
