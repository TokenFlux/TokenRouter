import { apiClient } from '../client'
import type { PaginatedResponse } from '@/api/common.types'
import type {
  AlertEvent,
  AlertEventsQuery,
  AlertRule,
  AlertSilenceCreateRequest,
  EmailNotificationConfig,
  OpsAdvancedSettings,
  OpsAlertRuntimeSettings,
  OpsConcurrencyStatsResponse,
  OpsDashboardFilterParams,
  OpsDashboardOverview,
  OpsDashboardSnapshotV2Response,
  OpsErrorDetail,
  OpsErrorDistributionResponse,
  OpsErrorListQueryParams,
  OpsErrorLogsResponse,
  OpsErrorTrendResponse,
  OpsLatencyHistogramParams,
  OpsLatencyHistogramResponse,
  OpsMetricThresholds,
  OpsProviderAvailabilityStatsResponse,
  OpsRealtimeTrafficSummaryResponse,
  OpsRequestDetailsParams,
  OpsRequestDetailsResponse,
  OpsRequestOptions,
  OpsRuntimeLogConfig,
  OpsSystemLogCleanupRequest,
  OpsSystemLogListResponse,
  OpsSystemLogQuery,
  OpsSystemLogSinkHealth,
  OpsThroughputTrendResponse,
  OpsTokenStatsParams,
  OpsTokenStatsResponse,
  OpsUserConcurrencyStatsResponse,
} from './ops.types'

/**
 * ops.ts retrieves operational dashboards, request errors, alerts and runtime
 * logging settings. DTO records live in ops.types.ts while this module handles
 * query serialization and forwards caller AbortSignal instances to dashboard
 * and realtime traffic endpoints.
 */

export type * from './ops.types'

/**
 * getConcurrencyStats retrieves observed concurrency for an optional platform
 * and group. A platform is forwarded when truthy; the group filter is sent only
 * when it is a positive numeric identifier.
 */
export async function getConcurrencyStats(
  platform?: string,
  groupId?: number | null,
): Promise<OpsConcurrencyStatsResponse> {
  const params: Record<string, unknown> = {}
  if (platform) {
    params.platform = platform
  }
  if (typeof groupId === 'number' && groupId > 0) {
    params.group_id = groupId
  }

  const { data } = await apiClient.get<OpsConcurrencyStatsResponse>('/admin/ops/concurrency', {
    params,
  })
  return data
}

/**
 * getUserConcurrencyStats retrieves user concurrency stats and returns the
 * server response data.
 */
export async function getUserConcurrencyStats(): Promise<OpsUserConcurrencyStatsResponse> {
  const { data } = await apiClient.get<OpsUserConcurrencyStatsResponse>(
    '/admin/ops/user-concurrency',
  )
  return data
}

/**
 * getProviderAvailabilityStats retrieves provider availability for an optional
 * platform and group. A platform is forwarded when truthy; the group filter is
 * sent only when it is a positive numeric identifier.
 */
export async function getProviderAvailabilityStats(
  platform?: string,
  groupId?: number | null,
): Promise<OpsProviderAvailabilityStatsResponse> {
  const params: Record<string, unknown> = {}
  if (platform) {
    params.platform = platform
  }
  if (typeof groupId === 'number' && groupId > 0) {
    params.group_id = groupId
  }
  const { data } = await apiClient.get<OpsProviderAvailabilityStatsResponse>(
    '/admin/ops/provider-availability',
    { params },
  )
  return data
}

/**
 * getRealtimeTrafficSummary retrieves throughput for the requested observation
 * window. Optional platform and positive numeric group filters narrow the
 * request, and options.signal cancels an in-flight request when filters change.
 */
export async function getRealtimeTrafficSummary(
  window: string,
  platform?: string,
  groupId?: number | null,
  options: OpsRequestOptions = {},
): Promise<OpsRealtimeTrafficSummaryResponse> {
  const params: Record<string, unknown> = { window }
  if (platform) {
    params.platform = platform
  }
  if (typeof groupId === 'number' && groupId > 0) {
    params.group_id = groupId
  }

  const { data } = await apiClient.get<OpsRealtimeTrafficSummaryResponse>(
    '/admin/ops/realtime-traffic',
    {
      params,
      signal: options.signal,
    },
  )
  return data
}

/**
 * getDashboardOverview retrieves dashboard overview and returns the server
 * response data. Options defaults to {}; the caller may cancel through the
 * supplied AbortSignal.
 */
export async function getDashboardOverview(
  params: OpsDashboardFilterParams,
  options: OpsRequestOptions = {},
): Promise<OpsDashboardOverview> {
  const { data } = await apiClient.get<OpsDashboardOverview>('/admin/ops/dashboard/overview', {
    params,
    signal: options.signal,
  })
  return data
}

/**
 * getDashboardSnapshotV2 retrieves dashboard snapshot v2 and returns the server
 * response data. Options defaults to {}; the caller may cancel through the
 * supplied AbortSignal.
 */
export async function getDashboardSnapshotV2(
  params: OpsDashboardFilterParams,
  options: OpsRequestOptions = {},
): Promise<OpsDashboardSnapshotV2Response> {
  const { data } = await apiClient.get<OpsDashboardSnapshotV2Response>(
    '/admin/ops/dashboard/snapshot-v2',
    {
      params,
      signal: options.signal,
    },
  )
  return data
}

/**
 * getThroughputTrend retrieves throughput trend and returns the server response
 * data. Options defaults to {}; the caller may cancel through the supplied
 * AbortSignal.
 */
export async function getThroughputTrend(
  params: OpsDashboardFilterParams,
  options: OpsRequestOptions = {},
): Promise<OpsThroughputTrendResponse> {
  const { data } = await apiClient.get<OpsThroughputTrendResponse>(
    '/admin/ops/dashboard/throughput-trend',
    {
      params,
      signal: options.signal,
    },
  )
  return data
}

/**
 * getLatencyHistogram retrieves duration buckets for the supplied operations
 * filters. Numeric bucket boundaries become one comma-separated query value,
 * while the other filters and optional caller cancellation signal pass to the
 * request.
 */
export async function getLatencyHistogram(
  params: OpsLatencyHistogramParams,
  options: OpsRequestOptions = {},
): Promise<OpsLatencyHistogramResponse> {
  // Separate bucket boundaries from ordinary filters because the server expects
  // one comma-separated query value instead of repeated numeric array parameters.
  const { bucket_boundaries_ms: boundaries, ...filterParams } = params
  const { data } = await apiClient.get<OpsLatencyHistogramResponse>(
    '/admin/ops/dashboard/latency-histogram',
    {
      params: {
        ...filterParams,
        bucket_boundaries_ms: boundaries?.join(','),
      },
      signal: options.signal,
    },
  )
  return data
}

/**
 * getErrorTrend retrieves error trend and returns the server response data.
 * Options defaults to {}; the caller may cancel through the supplied
 * AbortSignal.
 */
export async function getErrorTrend(
  params: OpsDashboardFilterParams,
  options: OpsRequestOptions = {},
): Promise<OpsErrorTrendResponse> {
  const { data } = await apiClient.get<OpsErrorTrendResponse>('/admin/ops/dashboard/error-trend', {
    params,
    signal: options.signal,
  })
  return data
}

/**
 * getErrorDistribution retrieves error distribution and returns the server
 * response data. Options defaults to {}; the caller may cancel through the
 * supplied AbortSignal.
 */
export async function getErrorDistribution(
  params: OpsDashboardFilterParams,
  options: OpsRequestOptions = {},
): Promise<OpsErrorDistributionResponse> {
  const { data } = await apiClient.get<OpsErrorDistributionResponse>(
    '/admin/ops/dashboard/error-distribution',
    {
      params,
      signal: options.signal,
    },
  )
  return data
}

/**
 * getTokenStats retrieves token stats and returns the server response data.
 * Options defaults to {}; the caller may cancel through the supplied
 * AbortSignal.
 */
export async function getTokenStats(
  params: OpsTokenStatsParams,
  options: OpsRequestOptions = {},
): Promise<OpsTokenStatsResponse> {
  const { data } = await apiClient.get<OpsTokenStatsResponse>('/admin/ops/dashboard/token-stats', {
    params,
    signal: options.signal,
  })
  return data
}

/**
 * listErrorLogs retrieves combined operations error records using the supplied
 * list filters.
 */
export async function listErrorLogs(
  params: OpsErrorListQueryParams,
): Promise<OpsErrorLogsResponse> {
  const { data } = await apiClient.get<OpsErrorLogsResponse>('/admin/ops/errors', { params })
  return data
}

/**
 * listRequestErrors retrieves request-level failures using the supplied
 * operations list filters.
 */
export async function listRequestErrors(
  params: OpsErrorListQueryParams,
): Promise<OpsErrorLogsResponse> {
  const { data } = await apiClient.get<OpsErrorLogsResponse>('/admin/ops/request-errors', {
    params,
  })
  return data
}

/**
 * listUpstreamErrors retrieves upstream errors and returns the server response
 * data.
 */
export async function listUpstreamErrors(
  params: OpsErrorListQueryParams,
): Promise<OpsErrorLogsResponse> {
  const { data } = await apiClient.get<OpsErrorLogsResponse>('/admin/ops/upstream-errors', {
    params,
  })
  return data
}

/**
 * getRequestErrorDetail retrieves request error detail and returns the server
 * response data.
 */
export async function getRequestErrorDetail(id: number): Promise<OpsErrorDetail> {
  const { data } = await apiClient.get<OpsErrorDetail>(`/admin/ops/request-errors/${id}`)
  return data
}

/**
 * getUpstreamErrorDetail retrieves upstream error detail and returns the server
 * response data.
 */
export async function getUpstreamErrorDetail(id: number): Promise<OpsErrorDetail> {
  const { data } = await apiClient.get<OpsErrorDetail>(`/admin/ops/upstream-errors/${id}`)
  return data
}

/**
 * listRequestErrorUpstreamErrors retrieves request error upstream errors and
 * returns the server response data. Params defaults to {}, options defaults to
 * {}.
 */
export async function listRequestErrorUpstreamErrors(
  id: number,
  params: OpsErrorListQueryParams = {},
  options: {
    include_detail?: boolean // Recorded include detail.
  } = {},
): Promise<PaginatedResponse<OpsErrorDetail>> {
  const query: Record<string, unknown> = { ...params }
  if (options.include_detail) query.include_detail = '1'
  const { data } = await apiClient.get<PaginatedResponse<OpsErrorDetail>>(
    `/admin/ops/request-errors/${id}/upstream-errors`,
    { params: query },
  )
  return data
}

/**
 * listRequestDetails retrieves request details and returns the server response
 * data.
 */
export async function listRequestDetails(
  params: OpsRequestDetailsParams,
): Promise<OpsRequestDetailsResponse> {
  const { data } = await apiClient.get<OpsRequestDetailsResponse>('/admin/ops/requests', { params })
  return data
}

/**
 * listAlertRules retrieves configured alert rules for the administrator
 * operations controls.
 */
export async function listAlertRules(): Promise<AlertRule[]> {
  const { data } = await apiClient.get<AlertRule[]>('/admin/ops/alert-rules')
  return data
}

/**
 * createAlertRule submits alert rule and returns the server response data.
 */
export async function createAlertRule(rule: AlertRule): Promise<AlertRule> {
  const { data } = await apiClient.post<AlertRule>('/admin/ops/alert-rules', rule)
  return data
}

/**
 * updateAlertRule updates alert rule and returns the server response data.
 */
export async function updateAlertRule(id: number, rule: Partial<AlertRule>): Promise<AlertRule> {
  const { data } = await apiClient.put<AlertRule>(`/admin/ops/alert-rules/${id}`, rule)
  return data
}

/**
 * deleteAlertRule removes alert rule and returns after the request completes.
 */
export async function deleteAlertRule(id: number): Promise<void> {
  await apiClient.delete(`/admin/ops/alert-rules/${id}`)
}

/**
 * listAlertEvents retrieves alert history using optional status, time and
 * continuation filters. An omitted query becomes an empty object, leaving limit
 * and interval selection to the alert-events endpoint.
 */
export async function listAlertEvents(params: AlertEventsQuery = {}): Promise<AlertEvent[]> {
  const { data } = await apiClient.get<AlertEvent[]>('/admin/ops/alert-events', { params })
  return data
}

/**
 * getAlertEvent retrieves alert event and returns the server response data.
 */
export async function getAlertEvent(id: number): Promise<AlertEvent> {
  const { data } = await apiClient.get<AlertEvent>(`/admin/ops/alert-events/${id}`)
  return data
}

/**
 * updateAlertEventStatus updates alert event status and returns after the
 * request completes.
 */
export async function updateAlertEventStatus(
  id: number,
  status: 'resolved' | 'manual_resolved',
): Promise<void> {
  await apiClient.put(`/admin/ops/alert-events/${id}/status`, { status })
}

/**
 * createAlertSilence submits alert silence and returns after the request
 * completes.
 */
export async function createAlertSilence(payload: AlertSilenceCreateRequest): Promise<void> {
  await apiClient.post('/admin/ops/alert-silences', payload)
}

/**
 * getEmailNotificationConfig retrieves alert delivery and scheduled report email
 * settings.
 */
export async function getEmailNotificationConfig(): Promise<EmailNotificationConfig> {
  const { data } = await apiClient.get<EmailNotificationConfig>(
    '/admin/ops/email-notification/config',
  )
  return data
}

/**
 * updateEmailNotificationConfig updates email notification config and returns
 * the server response data.
 */
export async function updateEmailNotificationConfig(
  config: EmailNotificationConfig,
): Promise<EmailNotificationConfig> {
  const { data } = await apiClient.put<EmailNotificationConfig>(
    '/admin/ops/email-notification/config',
    config,
  )
  return data
}

/**
 * getAlertRuntimeSettings retrieves alert evaluation, locking, silencing and
 * threshold configuration.
 */
export async function getAlertRuntimeSettings(): Promise<OpsAlertRuntimeSettings> {
  const { data } = await apiClient.get<OpsAlertRuntimeSettings>('/admin/ops/runtime/alert')
  return data
}

/**
 * updateAlertRuntimeSettings updates alert runtime settings and returns the
 * server response data.
 */
export async function updateAlertRuntimeSettings(
  config: OpsAlertRuntimeSettings,
): Promise<OpsAlertRuntimeSettings> {
  const { data } = await apiClient.put<OpsAlertRuntimeSettings>('/admin/ops/runtime/alert', config)
  return data
}

/**
 * getRuntimeLogConfig retrieves runtime log config and returns the server
 * response data.
 */
export async function getRuntimeLogConfig(): Promise<OpsRuntimeLogConfig> {
  const { data } = await apiClient.get<OpsRuntimeLogConfig>('/admin/ops/runtime/logging')
  return data
}

/**
 * updateRuntimeLogConfig updates runtime log config and returns the server
 * response data.
 */
export async function updateRuntimeLogConfig(
  config: OpsRuntimeLogConfig,
): Promise<OpsRuntimeLogConfig> {
  const { data } = await apiClient.put<OpsRuntimeLogConfig>('/admin/ops/runtime/logging', config)
  return data
}

/**
 * resetRuntimeLogConfig submits runtime log config and returns the server
 * response data.
 */
export async function resetRuntimeLogConfig(): Promise<OpsRuntimeLogConfig> {
  const { data } = await apiClient.post<OpsRuntimeLogConfig>('/admin/ops/runtime/logging/reset')
  return data
}

/**
 * listSystemLogs retrieves system logs and returns the server response data.
 */
export async function listSystemLogs(params: OpsSystemLogQuery): Promise<OpsSystemLogListResponse> {
  const { data } = await apiClient.get<OpsSystemLogListResponse>('/admin/ops/system-logs', {
    params,
  })
  return data
}

/**
 * cleanupSystemLogs submits cleanup system logs and returns the server response
 * data.
 */
export async function cleanupSystemLogs(payload: OpsSystemLogCleanupRequest): Promise<{
  deleted: number // Deleted record count.
}> {
  const { data } = await apiClient.post<{
    deleted: number // Deleted record count.
  }>('/admin/ops/system-logs/cleanup', payload)
  return data
}

/**
 * getSystemLogSinkHealth retrieves system log sink health and returns the server
 * response data.
 */
export async function getSystemLogSinkHealth(): Promise<OpsSystemLogSinkHealth> {
  const { data } = await apiClient.get<OpsSystemLogSinkHealth>('/admin/ops/system-logs/health')
  return data
}

/**
 * getAdvancedSettings retrieves operations retention, exclusion, quota-pause and
 * display configuration.
 */
export async function getAdvancedSettings(): Promise<OpsAdvancedSettings> {
  const { data } = await apiClient.get<OpsAdvancedSettings>('/admin/ops/advanced-settings')
  return data
}

/**
 * updateAdvancedSettings updates advanced settings and returns the server
 * response data.
 */
export async function updateAdvancedSettings(
  config: OpsAdvancedSettings,
): Promise<OpsAdvancedSettings> {
  const { data } = await apiClient.put<OpsAdvancedSettings>('/admin/ops/advanced-settings', config)
  return data
}

/**
 * getMetricThresholds retrieves warning thresholds used by operations dashboard
 * metric displays.
 */
export async function getMetricThresholds(): Promise<OpsMetricThresholds> {
  const { data } = await apiClient.get<OpsMetricThresholds>('/admin/ops/settings/metric-thresholds')
  return data
}

/**
 * updateMetricThresholds updates metric thresholds and returns after the request
 * completes.
 */
export async function updateMetricThresholds(thresholds: OpsMetricThresholds): Promise<void> {
  await apiClient.put('/admin/ops/settings/metric-thresholds', thresholds)
}

/**
 * opsAPI groups operations dashboards, error records and alerts for callers
 * using the module's named or default API object.
 */
export const opsAPI = {
  getDashboardSnapshotV2,
  getDashboardOverview,
  getThroughputTrend,
  getLatencyHistogram,
  getErrorTrend,
  getErrorDistribution,
  getTokenStats,
  getConcurrencyStats,
  getUserConcurrencyStats,
  getProviderAvailabilityStats,
  getRealtimeTrafficSummary,
  listErrorLogs,
  listRequestErrors,
  listUpstreamErrors,
  getRequestErrorDetail,
  getUpstreamErrorDetail,
  listRequestErrorUpstreamErrors,
  listRequestDetails,
  listAlertRules,
  createAlertRule,
  updateAlertRule,
  deleteAlertRule,
  listAlertEvents,
  getAlertEvent,
  updateAlertEventStatus,
  createAlertSilence,
  getEmailNotificationConfig,
  updateEmailNotificationConfig,
  getAlertRuntimeSettings,
  updateAlertRuntimeSettings,
  getRuntimeLogConfig,
  updateRuntimeLogConfig,
  resetRuntimeLogConfig,
  getAdvancedSettings,
  updateAdvancedSettings,
  getMetricThresholds,
  updateMetricThresholds,
  listSystemLogs,
  cleanupSystemLogs,
  getSystemLogSinkHealth,
}

export default opsAPI
