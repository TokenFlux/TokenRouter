import { apiClient } from './client'
import { buildApiUrl } from './url'
import type {
  UsageLog,
  UsageQueryParams,
  UsageStatsResponse,
  TrendDataPoint,
  ModelStat,
  GroupStat,
  UsageRequestType,
  UserErrorRequest,
  UserErrorRequestDetail,
  UserErrorListParams,
  UsageExportTask,
  UsageExportTaskListResponse,
  UsageExportDownloadTicket,
  CreateUserUsageExportTaskRequest,
} from '@/api/usage.types'
import type { PaginatedResponse } from '@/api/common.types'

/**
 * usage.ts retrieves user usage records, dashboard aggregates, failed-request
 * details, and background CSV export tasks. Query helpers retain time, ownership,
 * and billing filters, while export task helpers manage idempotent creation,
 * progress polling, cancellation, and signed streaming downloads.
 */

/**
 * PlatformDashboardStats summarizes lifetime and daily request, token and
 * deducted-cost totals for one platform.
 */
export interface PlatformDashboardStats {
  platform: string // Provider platform identifier.
  total_requests: number // Recorded total requests.
  total_tokens: number // Combined token count.
  total_actual_cost: number // Total actual cost.
  today_requests: number // Recorded today requests.
  today_tokens: number // Recorded today tokens.
  today_actual_cost: number // Today actual cost.
}

/**
 * UserDashboardStats combines the user's key counts, lifetime or daily usage
 * totals and recent throughput observations.
 */
export interface UserDashboardStats {
  total_api_keys: number // Total API keys.
  active_api_keys: number // Active API keys.
  total_requests: number // Recorded total requests.
  total_input_tokens: number // Total input tokens.
  total_output_tokens: number // Total output tokens.
  total_cache_creation_tokens: number // Total cache creation tokens.
  total_cache_read_tokens: number // Total cache read tokens.
  total_tokens: number // Combined token count.
  total_cost: number /**
   * Standard billed cost.
   */
  total_actual_cost: number /**
   * Actual deducted cost.
   */
  today_requests: number // Recorded today requests.
  today_input_tokens: number // Today input tokens.
  today_output_tokens: number // Today output tokens.
  today_cache_creation_tokens: number // Today cache creation tokens.
  today_cache_read_tokens: number // Today cache read tokens.
  today_tokens: number // Recorded today tokens.
  today_cost: number /**
   * Today's standard billed cost.
   */
  today_actual_cost: number /**
   * Today's actual deducted cost.
   */
  average_duration_ms: number // Average duration milliseconds.
  rpm: number /**
   * Requests per minute averaged over the preceding five minutes.
   */
  tpm: number /**
   * Tokens per minute averaged over the preceding five minutes.
   */
  by_platform?: PlatformDashboardStats[] // Recorded by platform.
}

/**
 * TrendParams selects a usage trend's date range, granularity and available
 * ownership filters.
 */
export interface TrendParams {
  start_date?: string // Requested start date.
  end_date?: string // Requested end date.
  granularity?: 'day' | 'hour' // Time bucket granularity.
  api_key_id?: number // API-key record identifier.
  model?: string // Model identifier value.
  group_id?: number // Group record identifier.
  team_id?: number // Team record identifier.
  request_type?: UsageRequestType // Recorded request mode.
  stream?: boolean // Streaming request flag.
  billing_type?: number | null // Recorded billing classification.
  billing_mode?: string | null // Selected billing calculation.
  native_compaction_v2?: boolean | null // Native compaction v2.
  timezone?: string // IANA timezone name.
}

/**
 * TrendResponse pairs usage trend points with the returned date range and bucket
 * granularity.
 */
export interface TrendResponse {
  trend: TrendDataPoint[] // Usage time-series points.
  start_date: string // Requested start date.
  end_date: string // Requested end date.
  granularity: string // Time bucket granularity.
}

/**
 * ModelStatsResponse pairs model usage aggregates with the date range returned
 * by the statistics endpoint.
 */
export interface ModelStatsResponse {
  models: ModelStat[] // Model usage aggregates.
  start_date: string // Requested start date.
  end_date: string // Requested end date.
}

/**
 * UsageRankingItem pairs a ranked user's display identity with whichever usage
 * totals the endpoint exposes.
 */
export interface UsageRankingItem {
  rank: number // Position within ranking.
  user_id: number // Owning user identifier.
  display_name: string // Recorded display name.
  avatar_url: string // Recorded avatar url.
  requests?: number // Observed request count.
  input_tokens?: number // Consumed input tokens.
  output_tokens?: number // Generated output tokens.
  cache_creation_tokens?: number // Cache creation tokens.
  cache_read_tokens?: number // Cache read tokens.
  total_tokens?: number // Combined token count.
  actual_cost?: number // Actual deducted cost.
}

/**
 * UsageRankingSortBy selects total tokens, request count or deducted cost as the
 * usage-ranking order.
 */
export type UsageRankingSortBy = 'total_tokens' | 'requests' | 'actual_cost'

/**
 * UsageRankingResponse contains ranked users, display switches and interval
 * metadata for the usage leaderboard.
 */
export interface UsageRankingResponse {
  ranking: UsageRankingItem[] // Ranked user entries.
  total_requests?: number // Recorded total requests.
  total_tokens?: number // Combined token count.
  total_actual_cost?: number // Total actual cost.
  sort_by: UsageRankingSortBy // Requested sort column.
  show_total_tokens: boolean // Show total tokens.
  show_requests: boolean // Recorded show requests.
  show_actual_cost: boolean // Show actual cost.
  start_date: string // Requested start date.
  end_date: string // Requested end date.
  limit: number // Maximum returned entries.
}

/**
 * UsageRankingParams optionally selects the start and end dates for a usage
 * ranking query.
 */
export interface UsageRankingParams {
  start_date?: string // Requested start date.
  end_date?: string // Requested end date.
}

/**
 * UsageDashboardSnapshotV2Params extends usage filters with switches selecting
 * trend, model and group aggregates.
 */
export interface UsageDashboardSnapshotV2Params extends TrendParams {
  include_trend?: boolean // Requested include trend.
  include_model_stats?: boolean // Include model stats.
  include_group_stats?: boolean // Include group stats.
}

/**
 * UsageDashboardSnapshotV2Response collects requested user-dashboard aggregates
 * with their generation timestamp and interval metadata.
 */
export interface UsageDashboardSnapshotV2Response {
  generated_at: string // Recorded generated at.
  start_date: string // Requested start date.
  end_date: string // Requested end date.
  granularity: string // Time bucket granularity.
  trend?: TrendDataPoint[] // Usage time-series points.
  models?: ModelStat[] // Model usage aggregates.
  groups?: GroupStat[] // Group usage aggregates.
}

/**
 * query retrieves usage logs with advanced query parameters. Config defaults to
 * {}; the caller may cancel through the supplied AbortSignal. The function
 * returns the data portion of the completed response.
 */
export async function query(
  params: UsageQueryParams & {
    sort_by?: string // Requested sort column.
    sort_order?: 'asc' | 'desc' // Requested ordering direction.
  },
  config: {
    signal?: AbortSignal // Caller-owned cancellation signal.
  } = {},
): Promise<PaginatedResponse<UsageLog>> {
  const { data } = await apiClient.get<PaginatedResponse<UsageLog>>('/usage', {
    ...config,
    params,
  })
  return data
}

/**
 * getStats retrieves usage statistics from a period name or query object. The
 * default period is today; an explicit apiKeyId overrides the object's
 * api_key_id value, including when that argument is zero.
 */
export async function getStats(
  paramsOrPeriod:
    | (UsageQueryParams & {
        period?: string // Requested statistics period.
        timezone?: string // IANA timezone name.
      })
    | string = 'today',
  apiKeyId?: number,
): Promise<UsageStatsResponse> {
  const params: Record<string, unknown> =
    typeof paramsOrPeriod === 'string' ? { period: paramsOrPeriod } : { ...paramsOrPeriod }

  if (apiKeyId !== undefined) {
    params.api_key_id = apiKeyId
  }

  const { data } = await apiClient.get<UsageStatsResponse>('/usage/stats', {
    params,
  })
  return data
}

/**
 * getRanking retrieves usage rankings for the supplied time range. Omitting the
 * range lets the server select today. Ordering follows the server-configured
 * ranking metric, which can be total tokens, requests, or actual cost.
 */
export async function getRanking(params: UsageRankingParams = {}): Promise<UsageRankingResponse> {
  const { data } = await apiClient.get<UsageRankingResponse>('/usage/ranking', { params })
  return data
}

/**
 * getDashboardStats retrieves the current user's key, usage and throughput
 * dashboard totals.
 */
export async function getDashboardStats(): Promise<UserDashboardStats> {
  const { data } = await apiClient.get<UserDashboardStats>('/usage/dashboard/stats')
  return data
}

/**
 * getDashboardTrend retrieves user usage trend data and returns the server
 * result.
 */
export async function getDashboardTrend(params?: TrendParams): Promise<TrendResponse> {
  const { data } = await apiClient.get<TrendResponse>('/usage/dashboard/trend', { params })
  return data
}

/**
 * getDashboardModels retrieves user model usage statistics and returns the
 * server result.
 */
export async function getDashboardModels(params?: {
  start_date?: string // Requested start date.
  end_date?: string // Requested end date.
  api_key_id?: number // API-key record identifier.
  model?: string // Model identifier value.
  model_source?: 'requested' // Model identifier selection.
  group_id?: number // Group record identifier.
  request_type?: UsageRequestType // Recorded request mode.
  stream?: boolean // Streaming request flag.
  billing_type?: number | null // Recorded billing classification.
  billing_mode?: string | null // Selected billing calculation.
  native_compaction_v2?: boolean | null // Native compaction v2.
  timezone?: string // IANA timezone name.
}): Promise<ModelStatsResponse> {
  const { data } = await apiClient.get<ModelStatsResponse>('/usage/dashboard/models', { params })
  return data
}

/**
 * getDashboardSnapshotV2 retrieves dashboard snapshot v2 and returns the server
 * response data.
 */
export async function getDashboardSnapshotV2(
  params?: UsageDashboardSnapshotV2Params,
): Promise<UsageDashboardSnapshotV2Response> {
  const { data } = await apiClient.get<UsageDashboardSnapshotV2Response>(
    '/usage/dashboard/snapshot-v2',
    { params },
  )
  return data
}

/**
 * BatchApiKeyUsageStats pairs an API-key identifier with its daily and
 * accumulated deducted costs.
 */
export interface BatchApiKeyUsageStats {
  api_key_id: number // API-key record identifier.
  today_actual_cost: number // Today actual cost.
  total_actual_cost: number // Total actual cost.
}

/**
 * BatchApiKeysUsageResponse maps stringified API-key identifiers to the
 * requested usage summary records.
 */
export interface BatchApiKeysUsageResponse {
  stats: Record<string, BatchApiKeyUsageStats> // Identifier-indexed usage summaries.
}

/**
 * getDashboardApiKeysUsage retrieves batch usage stats for user's own API keys.
 * The caller may cancel through the supplied AbortSignal. The function returns
 * the data portion of the completed response.
 */
export async function getDashboardApiKeysUsage(
  apiKeyIds: number[],
  options?: {
    signal?: AbortSignal // Caller-owned cancellation signal.
  },
): Promise<BatchApiKeysUsageResponse> {
  const { data } = await apiClient.post<BatchApiKeysUsageResponse>(
    '/usage/dashboard/api-keys-usage',
    {
      api_key_ids: apiKeyIds,
    },
    {
      signal: options?.signal,
    },
  )
  return data
}

/**
 * listMyErrorRequests retrieves my error requests and returns the server
 * response data.
 */
export async function listMyErrorRequests(
  params: UserErrorListParams,
): Promise<PaginatedResponse<UserErrorRequest>> {
  const { data } = await apiClient.get<PaginatedResponse<UserErrorRequest>>('/usage/errors', {
    params,
  })
  return data
}

/**
 * getMyErrorDetail retrieves my error detail and returns the server response
 * data.
 */
export async function getMyErrorDetail(id: number): Promise<UserErrorRequestDetail> {
  const { data } = await apiClient.get<UserErrorRequestDetail>(`/usage/errors/${id}`)
  return data
}

/**
 * buildExportIdempotencyKey generates a unique Idempotency-Key header value for
 * export task creation requests when the caller omits an explicit replay key.
 */
export function buildExportIdempotencyKey(prefix = 'usage-export'): string {
  const token =
    globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
  return `${prefix}-${token}`
}

/**
 * listExportTasks retrieves paginated background CSV export tasks owned by the
 * authenticated user. Callers may cancel in-flight polls via AbortSignal.
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
  const { data } = await apiClient.get<UsageExportTaskListResponse>('/usage/export-tasks', {
    params,
    signal: options?.signal,
  })
  return data
}

/**
 * createExportTask submits a user-scoped background CSV export task with an
 * Idempotency-Key header so network retries do not enqueue duplicate jobs.
 */
export async function createExportTask(
  payload: CreateUserUsageExportTaskRequest,
  options?: {
    idempotencyKey?: string // Optional caller-managed idempotency replay token.
  },
): Promise<UsageExportTask> {
  const idempotencyKey = options?.idempotencyKey || buildExportIdempotencyKey('user-usage-export')
  const { data } = await apiClient.post<UsageExportTask>('/usage/export-tasks', payload, {
    headers: { 'Idempotency-Key': idempotencyKey },
  })
  return data
}

/**
 * cancelExportTask cancels a pending or running user export task and returns its
 * updated persisted task record.
 */
export async function cancelExportTask(taskId: number): Promise<UsageExportTask> {
  const { data } = await apiClient.post<UsageExportTask>(`/usage/export-tasks/${taskId}/cancel`)
  return data
}

/**
 * createExportDownloadTicket exchanges an authenticated POST request for a
 * 60-second signed streaming download URL targeting a succeeded user export task.
 */
export async function createExportDownloadTicket(
  taskId: number,
): Promise<UsageExportDownloadTicket> {
  const { data } = await apiClient.post<UsageExportDownloadTicket>(
    `/usage/export-tasks/${taskId}/download-ticket`,
  )
  return data
}

/**
 * triggerExportDownloadUrl resolves a relative or absolute signed download URL
 * against the configured API base and initiates a native browser file download
 * without buffering CSV bytes in JavaScript memory.
 */
export function triggerExportDownloadUrl(downloadUrl: string, fileName?: string): string {
  const href =
    /^[a-z][a-z\d+.-]*:\/\//i.test(downloadUrl) || downloadUrl.startsWith('//')
      ? downloadUrl
      : buildApiUrl(downloadUrl)
  if (typeof document !== 'undefined') {
    const anchor = document.createElement('a')
    anchor.href = href
    anchor.download = fileName || ''
    anchor.rel = 'noopener'
    anchor.style.display = 'none'
    document.body.appendChild(anchor)
    anchor.click()
    document.body.removeChild(anchor)
  }
  return href
}

/**
 * usageAPI groups user usage records, rankings, dashboard summaries, error reads,
 * and background export task endpoints for module consumers.
 */
export const usageAPI = {
  query,
  getStats,
  getRanking,
  getDashboardStats,
  getDashboardTrend,
  getDashboardModels,
  getDashboardSnapshotV2,
  getDashboardApiKeysUsage,
  listMyErrorRequests,
  getMyErrorDetail,
  listExportTasks,
  createExportTask,
  cancelExportTask,
  createExportDownloadTicket,
  triggerExportDownloadUrl,
}

export default usageAPI
