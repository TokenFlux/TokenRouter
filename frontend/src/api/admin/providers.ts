import { apiClient } from '../client'
import type {
  AdminDataImportResult,
  AdminDataPayload,
  AdvancedSchedulerScoreDiagnosticResponse,
  AdvancedSchedulerScorePreviewRequest,
  BatchOperationResult,
  BatchProviderUsageResponse,
  BatchTodayStatsResponse,
  BatchUpstreamUsageResponse,
  CodexInviteResetConsumeResult,
  CodexInviteResetInviteResult,
  CodexInviteResetStatus,
  CodexSessionImportRequest,
  CodexSessionImportResult,
  CreateProviderRequest,
  OllamaCloudUsageSettings,
  OllamaCloudUsageState,
  OpenAICodexPATCreateRequest,
  OpenAIQuotaRefreshResult,
  PreviewFromCRSResult,
  Provider,
  ProviderListFilters,
  ProviderListWithEtagResult,
  ProviderUsageInfo,
  ProviderUsageStatsResponse,
  SparkShadowCreatePayload,
  SyncUpstreamModelsResult,
  SyncUpstreamPreviewParams,
  TempUnschedulableStatus,
  UpdateProviderRequest,
  UpstreamBalanceAlertStatus,
  UpstreamUsageQueryResult,
} from '@/api/admin/providers.types'
import type { PaginatedResponse } from '@/api/common.types'
import type { ClaudeModel } from '@/api/models.types'

/**
 * providers.ts calls administrator provider endpoints for configuration,
 * credentials, usage and diagnostic operations. DTO records live in
 * providers.types.ts. Most requests return response data directly; conditional
 * lists also return ETag metadata so consumers can retain their rows after a
 * 304 response, while long upstream queries, imports and synchronization
 * operations supply request budgets beyond the shared client default.
 *
 * Provider duplication retains an operation key by source provider ID in memory
 * and sessionStorage before posting. Ambiguous failures (network timeouts,
 * HTTP 408, 409, 429, or 5xx responses) preserve that key for safe retry,
 * whereas deterministic client rejections (other 4xx responses) clear both
 * caches so subsequent attempts are not pinned to a permanently rejected key.
 */

export type * from '@/api/admin/providers.types'

/**
 * list lists all providers with pagination. Page defaults to 1, pageSize
 * defaults to 20; the caller may cancel through the supplied AbortSignal.
 */
export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: ProviderListFilters,
  options?: {
    signal?: AbortSignal // Caller-owned cancellation signal.
  },
): Promise<PaginatedResponse<Provider>> {
  const { data } = await apiClient.get<PaginatedResponse<Provider>>('/admin/providers', {
    params: {
      page,
      page_size: pageSize,
      ...filters,
    },
    signal: options?.signal,
  })
  return data
}

/**
 * listWithEtag retrieves a provider page, defaulting page to 1 and pageSize to
 * 20. A supplied ETag sets If-None-Match, while signal is a cancellation request
 * option. A 304 response returns notModified with null data so the caller keeps
 * its existing rows; other accepted responses carry replacement page data.
 */
export async function listWithEtag(
  page: number = 1,
  pageSize: number = 20,
  filters?: ProviderListFilters,
  options?: {
    signal?: AbortSignal // Caller-owned cancellation signal.
    etag?: string | null // Response entity tag.
  },
): Promise<ProviderListWithEtagResult> {
  const headers: Record<string, string> = {}
  if (options?.etag) {
    headers['If-None-Match'] = options.etag
  }

  const response = await apiClient.get<PaginatedResponse<Provider>>('/admin/providers', {
    params: {
      page,
      page_size: pageSize,
      ...filters,
    },
    headers,
    signal: options?.signal,
    /**
     * validateStatus accepts successful HTTP statuses and 304 for conditional
     * provider-list reuse.
     */
    validateStatus: (status) => (status >= 200 && status < 300) || status === 304,
  })

  const etagHeader = typeof response.headers?.etag === 'string' ? response.headers.etag : null
  if (response.status === 304) {
    return {
      notModified: true,
      etag: etagHeader,
      data: null,
    }
  }

  return {
    notModified: false,
    etag: etagHeader,
    data: response.data,
  }
}

/**
 * getById retrieves provider by ID and returns the server result.
 */
export async function getById(id: number): Promise<Provider> {
  const { data } = await apiClient.get<Provider>(`/admin/providers/${id}`)
  return data
}

/**
 * getAdvancedSchedulerScore retrieves a provider's advanced scheduling summary
 * or diagnostics for one group.
 */
export async function getAdvancedSchedulerScore(
  id: number,
  groupID?: number,
): Promise<AdvancedSchedulerScoreDiagnosticResponse> {
  const { data } = await apiClient.get<AdvancedSchedulerScoreDiagnosticResponse>(
    `/admin/providers/${id}/advanced-scheduler-score`,
    { params: groupID ? { group_id: groupID } : undefined },
  )
  return data
}

/**
 * previewAdvancedSchedulerScore recalculates advanced scheduling scores using
 * the supplied request-scenario fields.
 */
export async function previewAdvancedSchedulerScore(
  id: number,
  payload: AdvancedSchedulerScorePreviewRequest,
): Promise<AdvancedSchedulerScoreDiagnosticResponse> {
  const { data } = await apiClient.post<AdvancedSchedulerScoreDiagnosticResponse>(
    `/admin/providers/${id}/advanced-scheduler-score/preview`,
    payload,
  )
  return data
}

/**
 * create creates new provider and returns the server result.
 */
export async function create(providerData: CreateProviderRequest): Promise<Provider> {
  const { data } = await apiClient.post<Provider>('/admin/providers', providerData)
  return data
}

/**
 * duplicateOperationKeys retains unresolved provider-copy keys by source
 * provider ID for reuse by retries in this document.
 */
const duplicateOperationKeys = new Map<number, string>()

/**
 * duplicateOperationStorageKey names the sessionStorage entry for one source
 * provider's pending copy.
 */
function duplicateOperationStorageKey(id: number): string {
  return `tokenrouter:admin:provider-duplicate:${id}`
}

/**
 * getStoredDuplicateOperationKey reads the source provider's pending copy key
 * from sessionStorage. Missing or inaccessible storage returns null, leaving the
 * memory cache and fresh-key path available to duplicate.
 */
function getStoredDuplicateOperationKey(id: number): string | null {
  try {
    return globalThis.sessionStorage?.getItem(duplicateOperationStorageKey(id)) ?? null
  } catch {
    // Blocked tab storage leaves recovery to the in-memory key, allowing this
    // document to retry pending work even when reload recovery is unavailable.
    return null
  }
}

/**
 * storeDuplicateOperationKey saves or removes the source provider's pending copy
 * key. A failed save leaves the pending key in memory. A failed removal after
 * success can leave a stale storage key after the memory entry is cleared.
 */
function storeDuplicateOperationKey(id: number, key: string | null): void {
  try {
    if (key) globalThis.sessionStorage?.setItem(duplicateOperationStorageKey(id), key)
    else globalThis.sessionStorage?.removeItem(duplicateOperationStorageKey(id))
  } catch {
    // Save failures retain retry identity in memory. Removal failures leave any
    // stored key behind because duplicate has already cleared the memory entry.
  }
}

/**
 * extractDuplicateErrorStatus reads an HTTP status code from either an unwrapped
 * apiClient rejection object or an Axios-style response rejection.
 */
function extractDuplicateErrorStatus(error: unknown): number | null {
  if (!error || typeof error !== 'object') return null
  const directStatus = (error as { status?: unknown }).status
  if (typeof directStatus === 'number' && Number.isFinite(directStatus)) {
    return directStatus
  }
  const responseStatus = (error as { response?: { status?: unknown } }).response?.status
  if (typeof responseStatus === 'number' && Number.isFinite(responseStatus)) {
    return responseStatus
  }
  return null
}

/**
 * isDeterministicClientDuplicateError reports whether a duplicate rejection
 * proves the server definitively refused the operation (4xx excluding 408, 409,
 * and 429) so the cached idempotency key should be discarded instead of pinning
 * future attempts to a failed request identity.
 */
function isDeterministicClientDuplicateError(error: unknown): boolean {
  const status = extractDuplicateErrorStatus(error)
  if (status === null || status < 400 || status >= 500) return false
  return status !== 408 && status !== 409 && status !== 429
}

/**
 * duplicate requests a server-side provider copy with a reusable operation key.
 * It checks the memory cache before sessionStorage, stores the selected key
 * before posting, and clears both caches after success or deterministic 4xx
 * client rejections. Ambiguous network, timeout (408), conflict (409),
 * rate-limit (429), and 5xx errors retain the key so a retry reattaches to any
 * server-side completion.
 */
export async function duplicate(id: number): Promise<Provider> {
  let idempotencyKey = duplicateOperationKeys.get(id) ?? getStoredDuplicateOperationKey(id)
  if (!idempotencyKey) {
    // Create a fresh identifier only when neither retry cache contains this
    // provider, preserving operation identity after an unknown server outcome.
    const requestID =
      globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    idempotencyKey = `provider-duplicate-${id}-${requestID}`
  }
  // Save the key before posting so a request that times out after server
  // completion can retry the same duplication operation.
  duplicateOperationKeys.set(id, idempotencyKey)
  storeDuplicateOperationKey(id, idempotencyKey)
  try {
    const { data } = await apiClient.post<Provider>(`/admin/providers/${id}/duplicate`, undefined, {
      headers: { 'Idempotency-Key': idempotencyKey },
    })
    // A confirmed response completes the pending operation; clear memory before
    // attempting storage removal.
    duplicateOperationKeys.delete(id)
    storeDuplicateOperationKey(id, null)
    return data
  } catch (error) {
    if (isDeterministicClientDuplicateError(error)) {
      // Clear definitively rejected 4xx operation keys so later attempts after
      // permissions, state, or step-up changes generate a fresh request identity.
      duplicateOperationKeys.delete(id)
      storeDuplicateOperationKey(id, null)
    }
    throw error
  }
}

/**
 * update updates provider and returns the server result.
 */
export async function update(id: number, updates: UpdateProviderRequest): Promise<Provider> {
  const { data } = await apiClient.put<Provider>(`/admin/providers/${id}`, updates)
  return data
}

/**
 * deleteProvider requests deletion of the selected provider and returns the
 * server's result message.
 */
export async function deleteProvider(id: number): Promise<{
  message: string // Server-provided result message.
}> {
  const { data } = await apiClient.delete<{
    message: string // Server-provided result message.
  }>(`/admin/providers/${id}`)
  return data
}

/**
 * refreshCredentials refreshes provider credentials and returns the server
 * result.
 */
export async function refreshCredentials(id: number): Promise<Provider> {
  const { data } = await apiClient.post<Provider>(`/admin/providers/${id}/refresh`)
  return data
}

/**
 * applyOAuthCredentials stores refreshed OAuth credentials and merges submitted
 * extra fields on the server. Runtime configuration fields absent from this
 * request remain outside the submitted patch.
 */
export async function applyOAuthCredentials(
  id: number,
  payload: {
    type: 'oauth' | 'setup-token' // Represented record category.
    credentials: Record<string, unknown> // Submitted provider credentials.
    extra?: Record<string, unknown> // Additional provider metadata.
  },
): Promise<Provider> {
  const { data } = await apiClient.post<Provider>(
    `/admin/providers/${id}/apply-oauth-credentials`,
    payload,
  )
  return data
}

/**
 * getStats retrieves historical usage aggregates for the selected provider. The
 * days argument defaults to thirty and is sent as a query value; callers receive
 * the returned statistics object.
 */
export async function getStats(id: number, days: number = 30): Promise<ProviderUsageStatsResponse> {
  const { data } = await apiClient.get<ProviderUsageStatsResponse>(`/admin/providers/${id}/stats`, {
    params: { days },
  })
  return data
}

/**
 * clearError clears provider error and returns the server result.
 */
export async function clearError(id: number): Promise<Provider> {
  const { data } = await apiClient.post<Provider>(`/admin/providers/${id}/clear-error`)
  return data
}

/**
 * getUsage retrieves a provider's upstream usage windows. The source selects the
 * requested usage source, and force asks the server to refresh its upstream
 * snapshot before returning.
 */
export async function getUsage(
  id: number,
  source?: 'passive' | 'active',
  force?: boolean,
): Promise<ProviderUsageInfo> {
  const params: Record<string, string> = {}
  if (source) params.source = source
  if (force) params.force = 'true'
  const { data } = await apiClient.get<ProviderUsageInfo>(`/admin/providers/${id}/usage`, {
    params: Object.keys(params).length > 0 ? params : undefined,
  })
  return data
}

/**
 * getBatchUsage submits batch usage and returns the server response data.
 */
export async function getBatchUsage(
  providerIds: number[],
  force?: boolean,
): Promise<BatchProviderUsageResponse> {
  const { data } = await apiClient.post<BatchProviderUsageResponse>(
    '/admin/providers/usage/batch',
    {
      provider_ids: providerIds,
      force: force === true,
    },
  )
  return data
}

/**
 * UPSTREAM_USAGE_QUERY_TIMEOUT_MS sets the browser budget for upstream usage
 * queries. Its sixty-five seconds cover the server's sixty-second lookup budget
 * plus response transfer and interceptor processing.
 */
const UPSTREAM_USAGE_QUERY_TIMEOUT_MS = 65_000

/**
 * queryUpstreamUsage submits upstream usage and returns the server response
 * data. The request uses the UPSTREAM_USAGE_QUERY_TIMEOUT_MS timeout budget.
 */
export async function queryUpstreamUsage(id: number): Promise<UpstreamUsageQueryResult> {
  const { data } = await apiClient.post<UpstreamUsageQueryResult>(
    `/admin/providers/${id}/upstream-usage/query`,
    undefined,
    { timeout: UPSTREAM_USAGE_QUERY_TIMEOUT_MS },
  )
  return data
}

/**
 * queryBatchUpstreamUsage retrieves current upstream usage for a batch of
 * API-key providers. The request uses the UPSTREAM_USAGE_QUERY_TIMEOUT_MS
 * timeout budget.
 */
export async function queryBatchUpstreamUsage(
  providerIds: number[],
): Promise<BatchUpstreamUsageResponse> {
  const { data } = await apiClient.post<BatchUpstreamUsageResponse>(
    '/admin/providers/upstream-usage/query/batch',
    { provider_ids: providerIds },
    { timeout: UPSTREAM_USAGE_QUERY_TIMEOUT_MS },
  )
  return data
}

/**
 * getUpstreamBalanceAlertStatus reads the provider's latest stored reminder
 * attempt. The endpoint returns persisted state only; it neither queries the
 * upstream nor sends email, so callers can refresh it freely while a form is
 * open.
 */
export async function getUpstreamBalanceAlertStatus(
  id: number,
): Promise<UpstreamBalanceAlertStatus> {
  const { data } = await apiClient.get<UpstreamBalanceAlertStatus>(
    `/admin/providers/${id}/upstream-balance-alert`,
  )
  return data
}

/**
 * recoverState recovers provider runtime state in one call.
 */
export async function recoverState(id: number): Promise<Provider> {
  const { data } = await apiClient.post<Provider>(`/admin/providers/${id}/recover-state`)
  return data
}

/**
 * resetProviderQuota resets provider quota usage and returns the server result.
 */
export async function resetProviderQuota(id: number): Promise<Provider> {
  const { data } = await apiClient.post<Provider>(`/admin/providers/${id}/reset-quota`)
  return data
}

/**
 * getTempUnschedulableStatus retrieves temporary unschedulable status and
 * returns the server result.
 */
export async function getTempUnschedulableStatus(id: number): Promise<TempUnschedulableStatus> {
  const { data } = await apiClient.get<TempUnschedulableStatus>(
    `/admin/providers/${id}/temp-unschedulable`,
  )
  return data
}

/**
 * generateAuthUrl generates OAuth authorization URL and returns the server
 * result.
 */
export async function generateAuthUrl(
  endpoint: string,
  config: {
    proxy_id?: number // Selected proxy identifier.
  },
): Promise<{
  auth_url: string // Authorization navigation URL.
  session_id: string // Authorization session identifier.
}> {
  const { data } = await apiClient.post<{
    auth_url: string // Authorization navigation URL.
    session_id: string // Authorization session identifier.
  }>(endpoint, config)
  return data
}

/**
 * exchangeCode exchanges authorization code for tokens and returns the server
 * result.
 */
export async function exchangeCode(
  endpoint: string,
  exchangeData: {
    session_id: string // Authorization session identifier.
    code: string // OAuth authorization code.
    state?: string // Current recorded state.
    proxy_id?: number // Selected proxy identifier.
    tls_fingerprint_router_id?: number // TLS fingerprint router ID.
  },
): Promise<Record<string, unknown>> {
  const { data } = await apiClient.post<Record<string, unknown>>(endpoint, exchangeData)
  return data
}

/**
 * bulkUpdate accepts either a complete batch payload or provider IDs plus
 * updates. In the array form updates are spread after provider_ids and can
 * override that property; object payloads are forwarded directly.
 */
export async function bulkUpdate(
  providerIdsOrPayload: number[] | Record<string, unknown>,
  updates?: Record<string, unknown>,
): Promise<{
  success: number // Successful operation count.
  failed: number // Failed operation count.
  success_ids?: number[] // Recorded success ids.
  failed_ids?: number[] // Recorded failed ids.
  results: Array<{
    provider_id: number // Provider record identifier.
    success: boolean // Whether this provider update succeeded.
    error?: string // Operation failure detail.
  }> // Returned operation results.
}> {
  const payload = Array.isArray(providerIdsOrPayload)
    ? {
        provider_ids: providerIdsOrPayload,
        ...(updates ?? {}),
      }
    : providerIdsOrPayload
  const { data } = await apiClient.post<{
    success: number // Successful operation count.
    failed: number // Failed operation count.
    success_ids?: number[] // Recorded success ids.
    failed_ids?: number[] // Recorded failed ids.
    results: Array<{
      provider_id: number // Provider record identifier.
      success: boolean // Whether this provider update succeeded.
      error?: string // Operation failure detail.
    }> // Returned operation results.
  }>('/admin/providers/bulk-update', payload)
  return data
}

/**
 * getBatchTodayStats retrieves today's provider statistics as a map indexed by
 * stringified provider identifiers.
 */
export async function getBatchTodayStats(providerIds: number[]): Promise<BatchTodayStatsResponse> {
  const { data } = await apiClient.post<BatchTodayStatsResponse>(
    '/admin/providers/today-stats/batch',
    {
      provider_ids: providerIds,
    },
  )
  return data
}

/**
 * setSchedulable sets provider schedulable status and returns the server result.
 */
export async function setSchedulable(id: number, schedulable: boolean): Promise<Provider> {
  const { data } = await apiClient.post<Provider>(`/admin/providers/${id}/schedulable`, {
    schedulable,
  })
  return data
}

/**
 * getAvailableModels retrieves the model identifiers available for the selected
 * provider.
 */
export async function getAvailableModels(id: number): Promise<ClaudeModel[]> {
  const { data } = await apiClient.get<ClaudeModel[]>(`/admin/providers/${id}/models`)
  return data
}

/**
 * syncUpstreamModels requests a fresh supported-model list from the selected
 * provider's saved upstream endpoint and credentials. Configuration changes
 * must be saved before calling; an optional signal cancels the HTTP request.
 */
export async function syncUpstreamModels(
  id: number,
  signal?: AbortSignal,
): Promise<SyncUpstreamModelsResult> {
  const { data } = await apiClient.post<SyncUpstreamModelsResult>(
    `/admin/providers/${id}/models/sync-upstream`,
    undefined,
    { signal },
  )
  return data
}

/**
 * syncUpstreamModelsPreview queries upstream models with temporary credentials
 * supplied by the creation form. The optional signal cancels the request when
 * the draft changes or closes, and returned protocol suggestions remain separate
 * from saved administrator overrides.
 */
export async function syncUpstreamModelsPreview(
  params: SyncUpstreamPreviewParams,
  signal?: AbortSignal,
): Promise<SyncUpstreamModelsResult> {
  const { data } = await apiClient.post<SyncUpstreamModelsResult>(
    '/admin/providers/models/sync-upstream-preview',
    params,
    { signal },
  )
  return data
}

/**
 * previewFromCrs submits preview from crs and returns the server response data.
 */
export async function previewFromCrs(params: {
  base_url: string // Recorded base url.
  username: string // Account display username.
  password: string // Submitted password text.
}): Promise<PreviewFromCRSResult> {
  const { data } = await apiClient.post<PreviewFromCRSResult>(
    '/admin/providers/sync/crs/preview',
    params,
  )
  return data
}

/**
 * syncFromCrs submits connection settings and optional provider selection to the
 * CRS synchronization endpoint. Its three-minute request budget accommodates
 * sequential credential refresh work performed during server-side
 * synchronization.
 */
export async function syncFromCrs(params: {
  base_url: string // Recorded base url.
  username: string // Account display username.
  password: string // Submitted password text.
  sync_proxies?: boolean // Recorded sync proxies.
  selected_provider_ids?: string[] // Selected provider IDs.
}): Promise<{
  created: number // Created record count.
  updated: number // Updated record count.
  skipped: number // Skipped record count.
  failed: number // Failed operation count.
  items: Array<{
    crs_account_id: string // Crs account ID.
    kind: string // Recorded item category.
    name: string // Human-readable display name.
    action: string // Selected outcome action.
    error?: string // Operation failure detail.
  }> // Returned collection entries.
}> {
  const { data } = await apiClient.post<{
    created: number // Created record count.
    updated: number // Updated record count.
    skipped: number // Skipped record count.
    failed: number // Failed operation count.
    items: Array<{
      crs_account_id: string // Crs account ID.
      kind: string // Recorded item category.
      name: string // Human-readable display name.
      action: string // Selected outcome action.
      error?: string // Operation failure detail.
    }> // Returned collection entries.
  }>('/admin/providers/sync/crs', params, {
    timeout: 180_000, // Allow sequential OAuth refreshes to finish during synchronization.
  })
  return data
}

/**
 * exportData requests provider export data for explicit IDs or matching filters.
 * A nonempty ID list takes precedence over filters, and includeProxies false is
 * encoded as the string false in query parameters.
 */
export async function exportData(options?: {
  ids?: number[] // Selected record identifiers.
  filters?: Omit<ProviderListFilters, 'lite' | 'include_scheduler_score'> // Applied query filters.
  includeProxies?: boolean // Recorded include proxies.
}): Promise<AdminDataPayload> {
  const params: Record<string, string> = {}
  if (options?.ids && options.ids.length > 0) {
    params.ids = options.ids.join(',')
  } else if (options?.filters) {
    const { platform, type, status, group, privacy_mode, search, sort_by, sort_order } =
      options.filters
    if (platform) params.platform = platform
    if (type) params.type = type
    if (status) params.status = status
    if (group) params.group = group
    if (privacy_mode) params.privacy_mode = privacy_mode
    if (search) params.search = search
    if (sort_by) params.sort_by = sort_by
    if (sort_order) params.sort_order = sort_order
  }
  if (options?.includeProxies === false) {
    params.include_proxies = 'false'
  }
  const { data } = await apiClient.get<AdminDataPayload>('/admin/providers/data', { params })
  return data
}

/**
 * importData submits import data and returns the server response data.
 */
export async function importData(payload: {
  data: AdminDataPayload // Submitted provider import document.
}): Promise<AdminDataImportResult> {
  const { data } = await apiClient.post<AdminDataImportResult>('/admin/providers/data', {
    data: payload.data,
  })
  return data
}

/**
 * importCodexSession submits Codex session import data and returns the
 * per-import result. A two-minute timeout accommodates larger batches before the
 * shared client reports a request timeout to the caller.
 */
export async function importCodexSession(
  payload: CodexSessionImportRequest,
): Promise<CodexSessionImportResult> {
  const { data } = await apiClient.post<CodexSessionImportResult>(
    '/admin/providers/import/codex-session',
    payload,
    {
      timeout: 120000, // Allow larger session import batches to finish.
    },
  )
  return data
}

/**
 * createOpenAICodexPAT submits open ai codex pat and returns the server response
 * data.
 */
export async function createOpenAICodexPAT(
  payload: OpenAICodexPATCreateRequest,
): Promise<Provider> {
  const { data } = await apiClient.post<Provider>('/admin/openai/create-from-codex-pat', payload)
  return data
}

/**
 * getAntigravityDefaultModelMapping retrieves Antigravity default model mapping
 * from backend.
 */
export async function getAntigravityDefaultModelMapping(): Promise<Record<string, string>> {
  const { data } = await apiClient.get<Record<string, string>>(
    '/admin/providers/antigravity/default-model-mapping',
  )
  return data
}

/**
 * refreshOpenAIToken exchanges the supplied refresh token at the selected
 * endpoint. Proxy, client and TLS-router fields are sent only when truthy; the
 * default endpoint selects OpenAI credential refresh.
 */
export async function refreshOpenAIToken(
  refreshToken: string,
  proxyId?: number | null,
  endpoint: string = '/admin/openai/refresh-token',
  clientId?: string,
  tlsFingerprintRouterId?: number | null,
): Promise<Record<string, unknown>> {
  const payload: {
    refresh_token: string // Session rotation token.
    proxy_id?: number // Selected proxy identifier.
    client_id?: string // OAuth client identifier.
    tls_fingerprint_router_id?: number // TLS fingerprint router ID.
  } = {
    refresh_token: refreshToken,
  }
  if (proxyId) {
    payload.proxy_id = proxyId
  }
  if (clientId) {
    payload.client_id = clientId
  }
  if (tlsFingerprintRouterId) {
    payload.tls_fingerprint_router_id = tlsFingerprintRouterId
  }
  const { data } = await apiClient.post<Record<string, unknown>>(endpoint, payload)
  return data
}

/**
 * revertProxyFallback restores provider proxy to original before fallback.
 */
export async function revertProxyFallback(id: number): Promise<{
  message: string // Server-provided result message.
}> {
  const { data } = await apiClient.post<{
    message: string // Server-provided result message.
  }>(`/admin/providers/${id}/revert-proxy-fallback`)
  return data
}

/**
 * batchDelete requests server-managed deletion of the selected provider
 * identifiers.
 */
export async function batchDelete(providerIds: number[]): Promise<BatchOperationResult> {
  const { data } = await apiClient.post<BatchOperationResult>('/admin/providers/batch-delete', {
    provider_ids: providerIds,
  })
  return data
}

/**
 * batchClearError submits provider identifiers and returns the server's
 * per-provider error-clearing outcomes.
 */
export async function batchClearError(providerIds: number[]): Promise<BatchOperationResult> {
  const { data } = await apiClient.post<BatchOperationResult>(
    '/admin/providers/batch-clear-error',
    {
      provider_ids: providerIds,
    },
  )
  return data
}

/**
 * batchRefresh requests credential refresh for the selected provider IDs and
 * returns batch outcomes. The request permits two minutes so larger batches can
 * finish beyond the shared client's default timeout.
 */
export async function batchRefresh(providerIds: number[]): Promise<BatchOperationResult> {
  const { data } = await apiClient.post<BatchOperationResult>(
    '/admin/providers/batch-refresh',
    {
      provider_ids: providerIds,
    },
    {
      timeout: 120000, // 120s timeout for large batch refreshes
    },
  )
  return data
}

/**
 * setPrivacy submits the privacy setting for a supported OAuth provider.
 */
export async function setPrivacy(id: number): Promise<Provider> {
  const { data } = await apiClient.post<Provider>(`/admin/providers/${id}/set-privacy`)
  return data
}

/**
 * getCodexInviteResetStatus retrieves invitation reset availability for an
 * OpenAI OAuth provider.
 */
export async function getCodexInviteResetStatus(id: number): Promise<CodexInviteResetStatus> {
  const { data } = await apiClient.get<CodexInviteResetStatus>(
    `/admin/providers/${id}/codex/invite-reset/status`,
  )
  return data
}

/**
 * sendCodexInviteResetInvite submits invitation email addresses through the
 * provider's Codex reset endpoint.
 */
export async function sendCodexInviteResetInvite(
  id: number,
  emails: string[],
): Promise<CodexInviteResetInviteResult> {
  const { data } = await apiClient.post<CodexInviteResetInviteResult>(
    `/admin/providers/${id}/codex/invite-reset/invite`,
    { emails },
  )
  return data
}

/**
 * consumeCodexInviteReset consumes one invitation reset opportunity for the
 * selected provider. Supply a credit ID when the dialog selected a particular
 * opportunity; omission leaves selection to the upstream service.
 */
export async function consumeCodexInviteReset(
  id: number,
  creditId?: string,
): Promise<CodexInviteResetConsumeResult> {
  // Send credit_id only when the dialog selected a specific opportunity; omission
  // lets the upstream choose an available reset credit.
  const payload = creditId?.trim() ? { credit_id: creditId.trim() } : {}
  const { data } = await apiClient.post<CodexInviteResetConsumeResult>(
    `/admin/providers/${id}/codex/invite-reset/consume`,
    payload,
  )
  return data
}

/**
 * refreshOpenAIQuota queries OpenAI OAuth quota and asks the server to save an
 * expiring reset-opportunity snapshot. The result reports quota data and whether
 * persistence succeeded for that snapshot.
 */
export async function refreshOpenAIQuota(id: number): Promise<OpenAIQuotaRefreshResult> {
  const { data } = await apiClient.post<OpenAIQuotaRefreshResult>(
    `/admin/openai/providers/${id}/quota/refresh`,
  )
  return data
}

/**
 * createSparkShadow submits spark shadow and returns the server response data.
 */
export async function createSparkShadow(
  parentId: number,
  payload: SparkShadowCreatePayload,
): Promise<Provider> {
  const { data } = await apiClient.post<Provider>(`/admin/providers/${parentId}/shadow`, payload)
  return data
}

/**
 * getOllamaCloudUsageSettings retrieves ollama cloud usage settings and returns
 * the server response data.
 */
export async function getOllamaCloudUsageSettings(): Promise<OllamaCloudUsageSettings> {
  const { data } = await apiClient.get<OllamaCloudUsageSettings>(
    '/admin/providers/ollama-cloud-usage/settings',
  )
  return data
}

/**
 * updateOllamaCloudUsageSettings updates ollama cloud usage settings and returns
 * the server response data.
 */
export async function updateOllamaCloudUsageSettings(
  settings: OllamaCloudUsageSettings,
): Promise<OllamaCloudUsageSettings> {
  const { data } = await apiClient.put<OllamaCloudUsageSettings>(
    '/admin/providers/ollama-cloud-usage/settings',
    settings,
  )
  return data
}

/**
 * getOllamaCloudUsage retrieves ollama cloud usage and returns the server
 * response data.
 */
export async function getOllamaCloudUsage(id: number): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.get<OllamaCloudUsageState>(
    `/admin/providers/${id}/ollama-cloud-usage`,
  )
  return data
}

/**
 * saveOllamaCloudUsageSession updates save ollama cloud usage session and
 * returns the server response data.
 */
export async function saveOllamaCloudUsageSession(
  id: number,
  session: string,
): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.put<OllamaCloudUsageState>(
    `/admin/providers/${id}/ollama-cloud-usage/session`,
    {
      session,
    },
  )
  return data
}

/**
 * deleteOllamaCloudUsageSession removes ollama cloud usage session and returns
 * the server response data.
 */
export async function deleteOllamaCloudUsageSession(id: number): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.delete<OllamaCloudUsageState>(
    `/admin/providers/${id}/ollama-cloud-usage/session`,
  )
  return data
}

/**
 * setOllamaCloudUsageAutoRefresh updates ollama cloud usage auto refresh and
 * returns the server response data.
 */
export async function setOllamaCloudUsageAutoRefresh(
  id: number,
  enabled: boolean,
): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.put<OllamaCloudUsageState>(
    `/admin/providers/${id}/ollama-cloud-usage/auto-refresh`,
    {
      enabled,
    },
  )
  return data
}

/**
 * refreshOllamaCloudUsage submits ollama cloud usage and returns the server
 * response data.
 */
export async function refreshOllamaCloudUsage(id: number): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.post<OllamaCloudUsageState>(
    `/admin/providers/${id}/ollama-cloud-usage/refresh`,
  )
  return data
}

/**
 * providersAPI groups this module's HTTP operations for callers using the
 * module's named or default API object.
 */
export const providersAPI = {
  list,
  listWithEtag,
  getById,
  getAdvancedSchedulerScore,
  previewAdvancedSchedulerScore,
  create,
  duplicate,
  update,
  delete: deleteProvider,
  refreshCredentials,
  applyOAuthCredentials,
  getStats,
  clearError,
  getUsage,
  getBatchUsage,
  queryUpstreamUsage,
  queryBatchUpstreamUsage,
  getUpstreamBalanceAlertStatus,
  getBatchTodayStats,
  recoverState,
  resetProviderQuota,
  getTempUnschedulableStatus,
  setSchedulable,
  getAvailableModels,
  syncUpstreamModels,
  syncUpstreamModelsPreview,
  generateAuthUrl,
  exchangeCode,
  refreshOpenAIToken,
  bulkUpdate,
  previewFromCrs,
  syncFromCrs,
  exportData,
  importData,
  importCodexSession,
  createOpenAICodexPAT,
  getAntigravityDefaultModelMapping,
  batchDelete,
  batchClearError,
  batchRefresh,
  setPrivacy,
  getCodexInviteResetStatus,
  sendCodexInviteResetInvite,
  consumeCodexInviteReset,
  refreshOpenAIQuota,
  revertProxyFallback,
  createSparkShadow,
  getOllamaCloudUsageSettings,
  updateOllamaCloudUsageSettings,
  getOllamaCloudUsage,
  saveOllamaCloudUsageSession,
  deleteOllamaCloudUsageSession,
  setOllamaCloudUsageAutoRefresh,
  refreshOllamaCloudUsage,
}

export default providersAPI
