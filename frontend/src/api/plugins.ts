import { apiClient } from './client'
import { pluginApiPath } from './pluginPaths'
import type { PluginUIResponse } from './plugins.types'

// plugins.ts retrieves authenticated contribution descriptions and sends scoped
// plugin requests through the existing session-aware client. Discovery uses the
// panel envelope, while contribution replies retain their original payload and
// still receive normal token refresh, cancellation, and HTTP error handling.

/** getPluginUI retrieves contributions for the current account and request language.
 * Callers own cancellation and cache scope. The backend has already filtered
 * page audiences, and no stored configuration appears in this response.
 */
export async function getPluginUI(signal?: AbortSignal): Promise<PluginUIResponse> {
  const { data } = await apiClient.get<PluginUIResponse>('/plugins/ui', { signal })
  return data
}

/** requestPlugin sends JSON to a validated endpoint inside one plugin namespace.
 * The request uses the host session without exposing its token. Its timeout
 * exceeds the maximum sixty-second route budget so the server owns timeout classification.
 */
export async function requestPlugin<T>(
  pluginId: string,
  method: string,
  path: string,
  options: { query?: Record<string, string>; body?: unknown; signal?: AbortSignal } = {},
): Promise<T> {
  const { data } = await apiClient.request<T>({
    url: pluginApiPath(pluginId, path),
    method,
    params: options.query,
    data: options.body,
    signal: options.signal,
    timeout: 65000,
  })
  return data
}
