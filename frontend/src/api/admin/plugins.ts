import { apiClient } from '../client'
import type {
  PluginChange,
  PluginConfig,
  PluginInstallInput,
  PluginInstallation,
  PluginLimits,
  PluginLogLine,
  PluginUploadPreview,
  PluginStatus,
} from './plugins.types'

// plugins.ts sends installed-plugin administration through the shared panel client.
// UI controllers own permission review and step-up retries. Upload and install
// requests receive enough time for bounded package transfer and startup, while
// configuration requests keep secret omission and clearing semantics explicit.

/** list retrieves accepted installations and their current local runtime observations. */
export async function list(signal?: AbortSignal): Promise<PluginInstallation[]> {
  return (await apiClient.get<PluginInstallation[]>('/admin/plugins', { signal })).data
}

/** get retrieves one installation without returning configuration secrets or local paths. */
export async function get(id: string): Promise<PluginInstallation> {
  return (await apiClient.get<PluginInstallation>(`/admin/plugins/${encodeURIComponent(id)}`)).data
}

/** upload validates one package without executing its code or persisting an installation.
 * The caller retains the selected file while step-up may retry this request.
 * Successful validation returns a temporary token for the later reviewed installation.
 */
export async function upload(file: File, signal?: AbortSignal): Promise<PluginUploadPreview> {
  const body = new FormData()
  body.append('file', file)
  return (
    await apiClient.post<PluginUploadPreview>('/admin/plugins/uploads', body, {
      signal,
      timeout: 180000,
      headers: { 'Content-Type': 'multipart/form-data' },
    })
  ).data
}

/** install persists a reviewed release and optionally starts its native contribution.
 * Confirm every requested permission before calling. A saved installation can
 * still require configuration, and its response retains that distinction from startup success.
 */
export async function install(input: PluginInstallInput): Promise<PluginChange> {
  return (await apiClient.post<PluginChange>('/admin/plugins', input, { timeout: 180000 })).data
}

/** enable requests execution after required configuration and capacity checks succeed. */
export async function enable(id: string): Promise<PluginChange> {
  return (await apiClient.post<PluginChange>(`/admin/plugins/${encodeURIComponent(id)}/enable`))
    .data
}

/** disable saves disabled intent and waits for admitted calls to drain. */
export async function disable(id: string): Promise<PluginChange> {
  return (await apiClient.post<PluginChange>(`/admin/plugins/${encodeURIComponent(id)}/disable`))
    .data
}

/** restart reloads package metadata and restarts the selected native contribution. */
export async function restart(id: string): Promise<PluginStatus> {
  return (await apiClient.post<PluginStatus>(`/admin/plugins/${encodeURIComponent(id)}/restart`))
    .data
}

/** uninstall drains execution before removing installation state and optional business data.
 * The caller obtains explicit destructive confirmation, including the purge choice.
 * Ledger history remains retained by the backend regardless of that choice.
 */
export async function uninstall(id: string, purgeData: boolean): Promise<void> {
  await apiClient.delete(`/admin/plugins/${encodeURIComponent(id)}`, {
    params: { purge_data: purgeData },
  })
}

/** config retrieves ordinary configuration and redacted secret-presence markers for editing. */
export async function config(id: string): Promise<PluginConfig> {
  return (await apiClient.get<PluginConfig>(`/admin/plugins/${encodeURIComponent(id)}/config`)).data
}

/** saveConfig replaces ordinary fields while preserving omitted stored secret values.
 * Empty secret strings explicitly clear them. The caller sends only edited
 * secret values and retains its draft if the backend rejects the configuration.
 */
export async function saveConfig(
  id: string,
  values: Record<string, string | number | boolean>,
): Promise<PluginConfig> {
  return (
    await apiClient.put<PluginConfig>(`/admin/plugins/${encodeURIComponent(id)}/config`, { values })
  ).data
}

/** limits retrieves the complete installation-level policy with backend defaults applied. */
export async function limits(id: string): Promise<PluginLimits> {
  return (await apiClient.get<PluginLimits>(`/admin/plugins/${encodeURIComponent(id)}/limits`)).data
}

/** saveLimits replaces capability allowances while retaining explicit zero prohibitions. */
export async function saveLimits(id: string, values: PluginLimits): Promise<PluginLimits> {
  return (
    await apiClient.put<PluginLimits>(`/admin/plugins/${encodeURIComponent(id)}/limits`, values)
  ).data
}

/** logs retrieves the latest bounded process lines without scheduling polling itself. */
export async function logs(id: string, signal?: AbortSignal): Promise<PluginLogLine[]> {
  return (
    await apiClient.get<PluginLogLine[]>(`/admin/plugins/${encodeURIComponent(id)}/logs`, {
      signal,
    })
  ).data
}
