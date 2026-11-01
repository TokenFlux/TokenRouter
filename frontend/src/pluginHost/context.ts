import { inject, type InjectionKey } from 'vue'
import type { HostApi } from '@tokenflux/tokenrouter-plugin-sdk'

// context.ts provides the single injection identity shared by plugin pages and
// host-owned component wrappers. The mounted page supplies a scoped HostApi;
// component code outside that provider fails explicitly instead of receiving an
// unscoped request client or access to authentication storage.

// The private symbol connects page providers to runtime useHost and teleported
// component wrappers without publishing a token or mutable authentication object.
export const PLUGIN_HOST_KEY: InjectionKey<HostApi> = Symbol('TokenRouter plugin host')

/** useHost retrieves the mounted page's plugin-scoped host capability object.
 * Call during component setup or synchronous functional rendering beneath
 * PluginPageView's provider. A missing context is an authoring error and never
 * falls back to global authenticated services.
 */
export function useHost(): HostApi {
  const host = inject(PLUGIN_HOST_KEY)
  if (!host) throw new Error('useHost requires a mounted plugin page')
  return host
}
