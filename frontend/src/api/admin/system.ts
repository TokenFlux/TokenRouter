import { apiClient } from '../client'

/**
 * system.ts requests server version checks, updates and rollback operations.
 * Binary replacement requests use a fifteen-minute timeout, and rollback
 * distinguishes a supplied release version from restoring the local backup;
 * restart is a separate API operation.
 */

/**
 * ReleaseInfo contains the release title, notes, publication timestamp and
 * browser URL shown by update views.
 */
export interface ReleaseInfo {
  name: string // Human-readable display name.
  body: string // Published release notes.
  published_at: string // Recorded published at.
  html_url: string // Recorded html url.
}

/**
 * VersionInfo pairs current and available versions with cached-check state and
 * optional release details or warnings.
 */
export interface VersionInfo {
  current_version: string // Recorded current version.
  latest_version: string // Recorded latest version.
  has_update: boolean // Recorded has update.
  release_info?: ReleaseInfo // Recorded release info.
  cached: boolean // Cached version-check result.
  warning?: string // Server advisory message.
  build_type: string // "source" for manual builds, "release" for CI builds
}

/**
 * checkUpdates retrieves the server version comparison and available release
 * details. The default uses ordinary checking; force true adds the string true
 * query flag to request a refreshed check.
 */
export async function checkUpdates(force = false): Promise<VersionInfo> {
  const { data } = await apiClient.get<VersionInfo>('/admin/system/check-updates', {
    params: force ? { force: 'true' } : undefined,
  })
  return data
}

/**
 * UpdateResult reports the server's binary-update message and whether a service
 * restart is required.
 */
export interface UpdateResult {
  message: string // Server-provided result message.
  need_restart: boolean // Recorded need restart.
}

/**
 * RollbackVersionInfo describes one eligible rollback release with publication
 * time and browser URL.
 */
export interface RollbackVersionInfo {
  version: string // Reported version identifier.
  published_at: string // Recorded published at.
  html_url: string // Recorded html url.
}

/**
 * getRollbackVersions retrieves the server's allowed rollback versions preceding
 * the current release.
 */
export async function getRollbackVersions(): Promise<{
  versions: RollbackVersionInfo[] // Eligible rollback releases.
}> {
  const { data } = await apiClient.get<{
    versions: RollbackVersionInfo[] // Eligible rollback releases.
  }>('/admin/system/rollback-versions')
  return data
}

/**
 * UPDATE_REQUEST_TIMEOUT_MS allows fifteen minutes for binary downloads used by
 * update and targeted rollback requests, which can exceed the shared
 * thirty-second request budget.
 */
const UPDATE_REQUEST_TIMEOUT_MS = 15 * 60 * 1000

/**
 * performUpdate requests download and installation of the latest server
 * version. It uses the fifteen-minute update timeout and returns the result
 * message with the server's restart requirement.
 */
export async function performUpdate(): Promise<UpdateResult> {
  const { data } = await apiClient.post<UpdateResult>('/admin/system/update', undefined, {
    timeout: UPDATE_REQUEST_TIMEOUT_MS,
  })
  return data
}

/**
 * rollback requests restoration of a selected release or the local backup
 * binary. A nonempty version is sent as the target; omission sends no body, and
 * the request uses the fifteen-minute update download timeout.
 */
export async function rollback(version?: string): Promise<UpdateResult> {
  const { data } = await apiClient.post<UpdateResult>(
    '/admin/system/rollback',
    version ? { version } : undefined,
    { timeout: UPDATE_REQUEST_TIMEOUT_MS },
  )
  return data
}

/**
 * restartService restarts the service and returns the server result.
 */
export async function restartService(): Promise<{
  message: string // Server-provided result message.
}> {
  const { data } = await apiClient.post<{
    message: string // Server-provided result message.
  }>('/admin/system/restart')
  return data
}

/**
 * probeServiceHealth sends an uncached GET probe to /health during post-update
 * or post-restart readiness polling, returning true only on a 2xx response.
 */
export async function probeServiceHealth(): Promise<boolean> {
  const response = await fetch('/health', {
    method: 'GET',
    cache: 'no-cache',
  })
  return response.ok
}

/**
 * systemAPI groups server update and rollback requests for callers using the
 * module's named or default API object.
 */
export const systemAPI = {
  checkUpdates,
  performUpdate,
  getRollbackVersions,
  rollback,
  restartService,
  probeServiceHealth,
}

export default systemAPI
