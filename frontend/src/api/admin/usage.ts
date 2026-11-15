import { apiClient } from '../client'
import { buildExportIdempotencyKey } from '../usage'
import type {
  AdminUsageLog,
  CreateAdminUsageExportTaskRequest,
  EndpointStat,
  UsageExportDownloadTicket,
  UsageExportTask,
  UsageExportTaskListResponse,
  UsageQueryParams,
  UsageRequestType,
} from '@/api/usage.types'
import type { PaginatedResponse } from '@/api/common.types'

/**
 * usage.ts retrieves administrator usage records and manages cleanup and
 * background CSV export tasks. Query types retain ownership, billing, and
 * request-mode filters, while task endpoints expose idempotent creation,
 * progress polling, cancellation, and signed streaming ticket exchange.
 */

/**
 * AdminUsageStatsResponse summarizes filtered usage totals, costs, average
 * duration and optional endpoint breakdowns.
 */
export interface AdminUsageStatsResponse {
  incomplete_cost_requests?: number // Requests with partial or unknown costs.
  total_requests: number // Recorded total requests.
  total_input_tokens: number // Total input tokens.
  total_output_tokens: number // Total output tokens.
  total_cache_tokens: number // Total cache tokens.
  total_cache_creation_tokens: number // Total cache creation tokens.
  total_cache_read_tokens: number // Total cache read tokens.
  total_tokens: number // Combined token count.
  total_cost: number // Recorded total cost.
  total_actual_cost: number // Total actual cost.
  total_provider_cost: number // Total provider cost.
  average_duration_ms: number // Average duration milliseconds.
  endpoints?: EndpointStat[] // Endpoint usage summaries.
  upstream_endpoints?: EndpointStat[] // Recorded upstream endpoints.
  endpoint_paths?: EndpointStat[] // Recorded endpoint paths.
}

/**
 * SimpleUser identifies a user search result and indicates whether its record
 * was deleted.
 */
export interface SimpleUser {
  id: number // Record identifier value.
  email: string // Account email address.
  deleted: boolean // Stored user soft-deletion flag.
}

/**
 * SimpleApiKey identifies an API-key search result together with its owning
 * user.
 */
export interface SimpleApiKey {
  id: number // Record identifier value.
  name: string // Human-readable display name.
  user_id: number // Owning user identifier.
}

/**
 * UsageCleanupFilters records the interval and ownership or billing filters
 * attached to a cleanup task.
 */
export interface UsageCleanupFilters {
  request_source?: string // Optional customer or internal source selector.
  start_time: string // Requested interval start.
  end_time: string // Requested interval end.
  user_id?: number // Owning user identifier.
  api_key_id?: number // API-key record identifier.
  provider_id?: number // Provider record identifier.
  group_id?: number // Group record identifier.
  team_id?: number // Team record identifier.
  model?: string | null // Model identifier value.
  request_type?: UsageRequestType | null // Recorded request mode.
  stream?: boolean | null // Streaming request flag.
  billing_type?: number | null // Recorded billing classification.
}

/**
 * UsageCleanupTask reports a cleanup task's filters, deleted-row count and
 * execution or cancellation state.
 */
export interface UsageCleanupTask {
  id: number // Record identifier value.
  status: string // Current server-reported status.
  filters: UsageCleanupFilters // Applied query filters.
  created_by: number // Recorded created by.
  deleted_rows: number // Recorded deleted rows.
  error_message?: string | null // Recorded error message.
  canceled_by?: number | null // Recorded canceled by.
  canceled_at?: string | null // Recorded canceled at.
  started_at?: string | null // Operation start timestamp.
  finished_at?: string | null // Operation completion timestamp.
  created_at: string // Record creation timestamp.
  updated_at: string // Last modification timestamp.
}

/**
 * CreateUsageCleanupTaskRequest selects the date range, timezone and ownership
 * or billing filters for cleanup creation.
 */
export interface CreateUsageCleanupTaskRequest {
  request_source?: string // Optional customer or internal source selector.
  start_date: string // Requested start date.
  end_date: string // Requested end date.
  user_id?: number // Owning user identifier.
  api_key_id?: number // API-key record identifier.
  provider_id?: number // Provider record identifier.
  group_id?: number // Group record identifier.
  team_id?: number // Team record identifier.
  model?: string | null // Model identifier value.
  request_type?: UsageRequestType | null // Recorded request mode.
  stream?: boolean | null // Streaming request flag.
  billing_type?: number | null // Recorded billing classification.
  timezone?: string // IANA timezone name.
}

/**
 * AdminUsageQueryParams adds administrator ownership, ordering and error-list
 * filters to the shared usage query fields.
 */
export interface AdminUsageQueryParams extends UsageQueryParams {
  user_id?: number // Owning user identifier.
  team_id?: number // Team record identifier.
  exact_total?: boolean // Requested exact total.
  billing_mode?: string // Selected billing calculation.
  sort_by?: string // Requested sort column.
  sort_order?: 'asc' | 'desc' // Requested ordering direction.
  /**
   * Error-list filters share the usage view's filter object, but error_phase,
   * error_category and status_code are consumed by the error request endpoint.
   */
  error_phase?: string | null // Requested error phase.
  error_category?: string | null // Requested error category.
  status_code?: number | null // HTTP status code.
  native_compaction_v2?: boolean | null // Native compaction v2.
}

/**
 * list retrieves administrator usage records with the supplied query filters.
 * Optional cancellation is forwarded to the shared client, and the returned data
 * contains the server's paginated usage entries.
 */
export async function list(
  params: AdminUsageQueryParams,
  options?: {
    signal?: AbortSignal // Caller-owned cancellation signal.
  },
): Promise<PaginatedResponse<AdminUsageLog>> {
  const { data } = await apiClient.get<PaginatedResponse<AdminUsageLog>>('/admin/usage', {
    params,
    signal: options?.signal,
  })
  return data
}

/**
 * getStats retrieves usage statistics with optional filters (admin only). The
 * caller may cancel through the supplied AbortSignal. The function returns the
 * data portion of the completed response.
 */
export async function getStats(
  params: {
    request_id?: string // Unified request identifier.
    request_source?: string // Optional customer or internal source selector.
    endpoint_source?: 'all' | 'inbound' | 'upstream' | 'path' // Recorded endpoint source.
    user_id?: number // Owning user identifier.
    team_id?: number // Team record identifier.
    api_key_id?: number // API-key record identifier.
    provider_id?: number // Provider record identifier.
    group_id?: number // Group record identifier.
    model?: string // Model identifier value.
    request_type?: UsageRequestType // Recorded request mode.
    stream?: boolean // Streaming request flag.
    period?: string // Requested statistics period.
    start_date?: string // Requested start date.
    end_date?: string // Requested end date.
    timezone?: string // IANA timezone name.
    nocache?: number // Cache bypass selector.
    native_compaction_v2?: boolean | null // Native compaction v2.
  },
  options?: {
    signal?: AbortSignal // Caller-owned cancellation signal.
  },
): Promise<AdminUsageStatsResponse> {
  const { data } = await apiClient.get<AdminUsageStatsResponse>('/admin/usage/stats', {
    params,
    signal: options?.signal,
  })
  return data
}

/**
 * searchUsers searches users by email keyword (admin only).
 */
export async function searchUsers(keyword: string): Promise<SimpleUser[]> {
  const { data } = await apiClient.get<SimpleUser[]>('/admin/usage/search-users', {
    params: { q: keyword },
  })
  return data
}

/**
 * searchApiKeys searches API keys by user ID and/or keyword (admin only).
 */
export async function searchApiKeys(userId?: number, keyword?: string): Promise<SimpleApiKey[]> {
  const params: Record<string, unknown> = {}
  if (userId !== undefined) {
    params.user_id = userId
  }
  if (keyword) {
    params.q = keyword
  }
  const { data } = await apiClient.get<SimpleApiKey[]>('/admin/usage/search-api-keys', {
    params,
  })
  return data
}

/**
 * listCleanupTasks lists usage cleanup tasks (admin only). The caller may cancel
 * through the supplied AbortSignal. The function returns the data portion of the
 * completed response.
 */
export async function listCleanupTasks(
  params: {
    page?: number // Requested page number.
    page_size?: number // Requested page size.
  },
  options?: {
    signal?: AbortSignal // Caller-owned cancellation signal.
  },
): Promise<PaginatedResponse<UsageCleanupTask>> {
  const { data } = await apiClient.get<PaginatedResponse<UsageCleanupTask>>(
    '/admin/usage/cleanup-tasks',
    {
      params,
      signal: options?.signal,
    },
  )
  return data
}

/**
 * createCleanupTask creates a usage cleanup task (admin only).
 */
export async function createCleanupTask(
  payload: CreateUsageCleanupTaskRequest,
): Promise<UsageCleanupTask> {
  const { data } = await apiClient.post<UsageCleanupTask>('/admin/usage/cleanup-tasks', payload)
  return data
}

/**
 * cancelCleanupTask submits cancel cleanup task and returns the server response
 * data.
 */
export async function cancelCleanupTask(taskId: number): Promise<{
  id: number // Record identifier value.
  status: string // Current server-reported status.
}> {
  const { data } = await apiClient.post<{
    id: number // Record identifier value.
    status: string // Current server-reported status.
  }>(`/admin/usage/cleanup-tasks/${taskId}/cancel`)
  return data
}

/**
 * listExportTasks lists administrator-scoped background CSV export tasks ordered
 * newest first. Callers may cancel in-flight polls via AbortSignal.
 */
export async function listExportTasks(
  params: {
    page?: number // Requested 1-based page number.
    page_size?: number // Requested page size.
    watch_ids?: string // Optional comma-separated off-page task IDs to inspect.
  } = {},
  options?: {
    signal?: AbortSignal // Caller-owned cancellation signal.
  },
): Promise<UsageExportTaskListResponse> {
  const { data } = await apiClient.get<UsageExportTaskListResponse>('/admin/usage/export-tasks', {
    params,
    signal: options?.signal,
  })
  return data
}

/**
 * createExportTask submits an administrator background CSV export task with an
 * Idempotency-Key header so network retries do not create duplicate tasks.
 */
export async function createExportTask(
  payload: CreateAdminUsageExportTaskRequest,
  options?: {
    idempotencyKey?: string // Optional caller-managed idempotency replay token.
  },
): Promise<UsageExportTask> {
  const idempotencyKey = options?.idempotencyKey || buildExportIdempotencyKey('admin-usage-export')
  const { data } = await apiClient.post<UsageExportTask>('/admin/usage/export-tasks', payload, {
    headers: { 'Idempotency-Key': idempotencyKey },
  })
  return data
}

/**
 * cancelExportTask cancels a pending or running administrator export task and
 * returns its updated persisted task record.
 */
export async function cancelExportTask(taskId: number): Promise<UsageExportTask> {
  const { data } = await apiClient.post<UsageExportTask>(
    `/admin/usage/export-tasks/${taskId}/cancel`,
  )
  return data
}

/**
 * createExportDownloadTicket exchanges an authenticated POST request for a
 * 60-second signed streaming download URL targeting an administrator export task.
 */
export async function createExportDownloadTicket(
  taskId: number,
): Promise<UsageExportDownloadTicket> {
  const { data } = await apiClient.post<UsageExportDownloadTicket>(
    `/admin/usage/export-tasks/${taskId}/download-ticket`,
  )
  return data
}

/**
 * adminUsageAPI groups usage records, summary queries, cleanup jobs, and
 * background CSV export task endpoints for administrator callers.
 */
export const adminUsageAPI = {
  list,
  getStats,
  searchUsers,
  searchApiKeys,
  listCleanupTasks,
  createCleanupTask,
  cancelCleanupTask,
  listExportTasks,
  createExportTask,
  cancelExportTask,
  createExportDownloadTicket,
}

export default adminUsageAPI
