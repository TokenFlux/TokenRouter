import type { PaginatedResponse } from '@/api/common.types'

/**
 * ops.types.ts defines request filters, dashboard snapshots, alert rules,
 * runtime logging settings, and error records for administrator operations
 * endpoints. Shared time-window filters keep overview, throughput, error-trend,
 * error-distribution, and latency-histogram queries consistent across views.
 */

/**
 * OpsRequestOptions passes caller cancellation into operations requests that
 * accept an AbortSignal.
 */
export interface OpsRequestOptions {
  signal?: AbortSignal // Caller-owned cancellation signal.
}

/**
 * OpsTimeRange enumerates preset observation windows accepted by operations
 * dashboard and request-detail endpoints.
 */
export type OpsTimeRange = '5m' | '30m' | '1h' | '6h' | '24h'

/**
 * OpsDashboardFilterParams selects an observation window and optional platform
 * or group scope shared across dashboard overview, snapshot, trend, and
 * distribution endpoints.
 */
export interface OpsDashboardFilterParams {
  time_range?: OpsTimeRange // Requested time range.
  start_time?: string // Requested interval start.
  end_time?: string // Requested interval end.
  platform?: string // Provider platform identifier.
  group_id?: number | null // Group record identifier.
}

/**
 * OpsDashboardOverview collects availability, throughput, token and latency
 * observations for one selected operations window.
 */
export interface OpsDashboardOverview {
  start_time: string // Requested interval start.
  end_time: string // Requested interval end.
  platform: string // Provider platform identifier.
  group_id?: number | null // Group record identifier.

  health_score?: number // Recorded health score.

  system_metrics?: OpsSystemMetricsSnapshot | null // Recorded system metrics.
  job_heartbeats?: OpsJobHeartbeat[] | null // Recorded job heartbeats.

  success_count: number // Recorded success count.
  error_count_total: number // Error count total.
  business_limited_count: number // Business limited count.
  error_count_sla: number // Error count sla.
  request_count_total: number // Request count total.
  request_count_sla: number // Request count sla.

  token_consumed: number // Recorded token consumed.

  sla: number // Service availability fraction.
  error_rate: number // SLA-error request fraction.
  upstream_error_rate: number // Countable upstream-error fraction.
  upstream_error_count_excl_429_529: number // Upstream error count excl 429 529.
  upstream_429_count: number // Upstream 429 count.
  upstream_529_count: number // Upstream 529 count.

  qps: {
    current: number // Current observed rate.
    peak: number // Peak observed rate.
    avg: number // Average observed rate.
  } // Requests per second.
  tps: {
    current: number // Current observed rate.
    peak: number // Peak observed rate.
    avg: number // Average observed rate.
  } // Tokens per second.

  duration: OpsPercentiles // Request duration distribution.
  ttft: OpsPercentiles // First-token latency distribution.
}

/**
 * OpsPercentiles carries observed duration percentiles and summaries in
 * milliseconds. Optional null observations represent missing measurements and
 * should remain distinguishable from a measured zero duration.
 */
export interface OpsPercentiles {
  p50_ms?: number | null // Median duration milliseconds.
  p90_ms?: number | null // 90th-percentile duration milliseconds.
  p95_ms?: number | null // 95th-percentile duration milliseconds.
  p99_ms?: number | null // 99th-percentile duration milliseconds.
  avg_ms?: number | null // Mean duration milliseconds.
  max_ms?: number | null // Maximum duration milliseconds.
}

/**
 * OpsThroughputTrendPoint contains request and token observations for one
 * throughput time bucket.
 */
export interface OpsThroughputTrendPoint {
  bucket_start: string // Recorded bucket start.
  request_count: number // Recorded request count.
  token_consumed: number // Recorded token consumed.
  switch_count?: number // Recorded switch count.
  qps: number // Requests per second.
  tps: number // Tokens per second.
}

/**
 * OpsThroughputPlatformBreakdownItem pairs a platform identifier with its
 * request and consumed-token totals.
 */
export interface OpsThroughputPlatformBreakdownItem {
  platform: string // Provider platform identifier.
  request_count: number // Recorded request count.
  token_consumed: number // Recorded token consumed.
}

/**
 * OpsThroughputGroupBreakdownItem pairs group identity with its request and
 * consumed-token totals.
 */
export interface OpsThroughputGroupBreakdownItem {
  group_id: number // Group record identifier.
  group_name: string // Recorded group name.
  request_count: number // Recorded request count.
  token_consumed: number // Recorded token consumed.
}

/**
 * OpsThroughputTrendResponse contains bucketed throughput observations and
 * optional platform or leading-group breakdowns.
 */
export interface OpsThroughputTrendResponse {
  bucket: string // Time bucket interval.
  points: OpsThroughputTrendPoint[] // Time-series data points.
  by_platform?: OpsThroughputPlatformBreakdownItem[] // Recorded by platform.
  top_groups?: OpsThroughputGroupBreakdownItem[] // Recorded top groups.
}

/**
 * OpsRequestKind distinguishes successful usage records from recorded failures
 * in combined request views.
 */
export type OpsRequestKind = 'success' | 'error'

/**
 * OpsRequestDetailsKind adds an all-results choice to the success-or-error
 * request kind filter.
 */
export type OpsRequestDetailsKind = OpsRequestKind | 'all'

/**
 * OpsRequestDetailsSort selects descending creation-time or duration ordering
 * for operations request details.
 */
export type OpsRequestDetailsSort = 'created_at_desc' | 'duration_desc'

/**
 * OpsRequestDetail joins request identity and timing with the optional actor,
 * provider and error details available for that record.
 */
export interface OpsRequestDetail {
  kind: OpsRequestKind // Recorded item category.
  created_at: string // Record creation timestamp.
  request_id: string // Unified request identifier.

  platform?: string // Provider platform identifier.
  model?: string // Model identifier value.
  duration_ms?: number | null // Duration in milliseconds.
  status_code?: number | null // HTTP status code.

  error_id?: number | null // Requested error id.
  phase?: string // Recorded lifecycle phase.
  severity?: string // Recorded severity level.
  message?: string // Server-provided result message.

  user_id?: number | null // Owning user identifier.
  api_key_id?: number | null // API-key record identifier.
  provider_id?: number | null // Provider record identifier.
  group_id?: number | null // Group record identifier.

  stream?: boolean // Streaming request flag.
}

/**
 * OpsRequestDetailsParams selects request kind, time, ownership, duration bounds
 * and pagination for combined operations lists.
 */
export interface OpsRequestDetailsParams extends OpsDashboardFilterParams {
  kind?: OpsRequestDetailsKind // Recorded item category.
  sla_only?: '1' | '0' // Requested sla only.

  user_id?: number // Owning user identifier.
  api_key_id?: number // API-key record identifier.
  provider_id?: number // Provider record identifier.

  model?: string // Model identifier value.
  request_id?: string // Unified request identifier.
  q?: string // Free-text search query.

  min_duration_ms?: number // Min duration milliseconds.
  max_duration_ms?: number // Max duration milliseconds.

  sort?: OpsRequestDetailsSort // Requested result ordering.

  page?: number // Requested page number.
  page_size?: number // Requested page size.
}

/**
 * OpsRequestDetailsResponse wraps combined request detail rows in the shared
 * paginated response format.
 */
export type OpsRequestDetailsResponse = PaginatedResponse<OpsRequestDetail>

/**
 * OpsLatencyHistogramBucket pairs a displayed duration range with the number of
 * requests it contains.
 */
export interface OpsLatencyHistogramBucket {
  range: string // Histogram range label.
  count: number // Observed item count.
}

/**
 * OpsLatencyHistogramResponse contains duration buckets, their boundaries and
 * the observation window used for their totals.
 */
export interface OpsLatencyHistogramResponse {
  start_time: string // Requested interval start.
  end_time: string // Requested interval end.
  platform: string // Provider platform identifier.
  group_id?: number | null // Group record identifier.
  bucket_boundaries_ms: number[] // Bucket boundaries milliseconds.

  total_requests: number // Recorded total requests.
  buckets: OpsLatencyHistogramBucket[] // Histogram bucket entries.
}

/**
 * OpsLatencyHistogramParams selects an operations time window and optional
 * duration bucket boundaries. The wrapper serializes bucket_boundaries_ms as a
 * comma-separated query value before sending the request.
 */
export interface OpsLatencyHistogramParams extends OpsDashboardFilterParams {
  bucket_boundaries_ms?: number[] // Bucket boundaries milliseconds.
}

/**
 * OpsErrorTrendPoint contains SLA, business-limit and upstream error counts for
 * one time bucket.
 */
export interface OpsErrorTrendPoint {
  bucket_start: string // Recorded bucket start.
  error_count_total: number // Error count total.
  business_limited_count: number // Business limited count.
  error_count_sla: number // Error count sla.
  upstream_error_count_excl_429_529: number // Upstream error count excl 429 529.
  upstream_429_count: number // Upstream 429 count.
  upstream_529_count: number // Upstream 529 count.
}

/**
 * OpsErrorTrendResponse pairs the selected bucket interval with its sequence of
 * error-count observations.
 */
export interface OpsErrorTrendResponse {
  bucket: string // Time bucket interval.
  points: OpsErrorTrendPoint[] // Time-series data points.
}

/**
 * OpsErrorDistributionItem splits one HTTP status code's records into total, SLA
 * and business-limited counts.
 */
export interface OpsErrorDistributionItem {
  status_code: number // HTTP status code.
  total: number // Total matching records.
  sla: number // SLA-eligible client-failure count.
  business_limited: number // Recorded business limited.
}

/**
 * OpsErrorDistributionResponse contains per-status error counts and their
 * overall total for the distribution display.
 */
export interface OpsErrorDistributionResponse {
  total: number // Total matching records.
  items: OpsErrorDistributionItem[] // Returned collection entries.
}

/**
 * OpsDashboardSnapshotV2Response combines overview, throughput and error trends
 * under one generated-at timestamp.
 */
export interface OpsDashboardSnapshotV2Response {
  generated_at: string // Recorded generated at.
  overview: OpsDashboardOverview // Operations summary observations.
  throughput_trend: OpsThroughputTrendResponse // Recorded throughput trend.
  error_trend: OpsErrorTrendResponse // Recorded error trend.
}

/**
 * OpsTokenStatsTimeRange enumerates supported windows for the operations model
 * token-statistics query.
 */
export type OpsTokenStatsTimeRange = '30m' | '1h' | '1d' | '15d' | '30d'

/**
 * OpsTokenStatsItem reports a model's requests, output tokens and observed
 * duration or first-token rates.
 */
export interface OpsTokenStatsItem {
  model: string // Model identifier value.
  request_count: number // Recorded request count.
  avg_tokens_per_sec?: number | null // Avg tokens per sec.
  avg_ttft_ms?: number | null // Avg TTFT milliseconds.
  total_output_tokens: number // Total output tokens.
  avg_duration_ms: number // Avg duration milliseconds.
  requests_with_first_token: number // Requests with first token.
}

/**
 * OpsTokenStatsResponse contains model token-statistics rows with interval,
 * scope and pagination metadata.
 */
export interface OpsTokenStatsResponse {
  time_range: OpsTokenStatsTimeRange // Recorded time range.
  start_time: string // Requested interval start.
  end_time: string // Requested interval end.
  platform?: string // Provider platform identifier.
  group_id?: number | null // Group record identifier.
  items: OpsTokenStatsItem[] // Returned collection entries.
  total: number // Total matching records.
  page?: number // Requested page number.
  page_size?: number // Requested page size.
  top_n?: number | null // Ranking result limit.
}

/**
 * OpsTokenStatsParams selects the token-statistics interval, platform, group and
 * ranking or page limits.
 */
export interface OpsTokenStatsParams {
  time_range?: OpsTokenStatsTimeRange // Requested time range.
  platform?: string // Provider platform identifier.
  group_id?: number | null // Group record identifier.
  page?: number // Requested page number.
  page_size?: number // Requested page size.
  top_n?: number // Ranking result limit.
}

/**
 * OpsSystemMetricsSnapshot contains optional process, storage and connection
 * observations collected in one monitoring window.
 */
export interface OpsSystemMetricsSnapshot {
  id: number // Record identifier value.
  created_at: string // Record creation timestamp.
  window_minutes: number // Recorded window minutes.

  cpu_usage_percent?: number | null // Cpu usage percent.
  memory_used_mb?: number | null // Memory used mb.
  memory_total_mb?: number | null // Memory total mb.
  memory_usage_percent?: number | null // Memory usage percent.
  disk_used_mb?: number | null // Disk used mb.
  disk_total_mb?: number | null // Disk total mb.
  disk_usage_percent?: number | null // Disk usage percent.

  db_ok?: boolean | null // Recorded db ok.
  redis_ok?: boolean | null // Recorded redis ok.

  db_max_open_conns?: number | null // Db max open conns.
  redis_pool_size?: number | null // Redis pool size.

  redis_conn_total?: number | null // Redis conn total.
  redis_conn_idle?: number | null // Redis conn idle.

  db_conn_active?: number | null // Db conn active.
  db_conn_idle?: number | null // Db conn idle.
  db_conn_waiting?: number | null // Db conn waiting.

  goroutine_count?: number | null // Recorded goroutine count.
  concurrency_queue_depth?: number | null // Concurrency queue depth.
  provider_switch_count?: number | null // Provider switch count.
}

/**
 * OpsJobHeartbeat records a background job's most recent run, success, failure
 * and observed duration.
 */
export interface OpsJobHeartbeat {
  job_name: string // Recorded job name.
  last_run_at?: string | null // Last run at.
  last_success_at?: string | null // Last success at.
  last_error_at?: string | null // Last error at.
  last_error?: string | null // Recorded last error.
  last_duration_ms?: number | null // Last duration milliseconds.
  last_result?: string | null // Recorded last result.
  updated_at: string // Last modification timestamp.
}

/**
 * PlatformConcurrencyInfo summarizes occupied slots, configured capacity and
 * queued work for a platform.
 */
export interface PlatformConcurrencyInfo {
  platform: string // Provider platform identifier.
  current_in_use: number // Current in use.
  max_capacity: number // Recorded max capacity.
  load_percentage: number // Recorded load percentage.
  waiting_in_queue: number // Waiting in queue.
}

/**
 * GroupConcurrencyInfo summarizes occupied slots, configured capacity and queued
 * work for a group.
 */
export interface GroupConcurrencyInfo {
  group_id: number // Group record identifier.
  group_name: string // Recorded group name.
  current_in_use: number // Current in use.
  max_capacity: number // Recorded max capacity.
  load_percentage: number // Recorded load percentage.
  waiting_in_queue: number // Waiting in queue.
}

/**
 * ProviderConcurrencyInfo summarizes one provider's occupied slots and queue
 * within a group and platform.
 */
export interface ProviderConcurrencyInfo {
  provider_id: number // Provider record identifier.
  provider_name?: string // Recorded provider name.
  platform: string // Provider platform identifier.
  group_id: number // Group record identifier.
  group_name: string // Recorded group name.
  current_in_use: number // Current in use.
  max_capacity: number // Recorded max capacity.
  load_percentage: number // Recorded load percentage.
  waiting_in_queue: number // Waiting in queue.
}

/**
 * OpsConcurrencyStatsResponse groups observed slot usage by platform, group and
 * provider. enabled reports whether the feature is active, while timestamp
 * identifies the returned observation when supplied.
 */
export interface OpsConcurrencyStatsResponse {
  enabled: boolean // Feature activation flag.
  platform: Record<string, PlatformConcurrencyInfo> // Platform-indexed observation map.
  group: Record<string, GroupConcurrencyInfo> // Group-indexed observation map.
  provider: Record<string, ProviderConcurrencyInfo> // Provider-indexed observation map.
  timestamp?: string // Observation snapshot timestamp.
}

/**
 * UserConcurrencyInfo pairs user identity with occupied slots, capacity and
 * queued request counts.
 */
export interface UserConcurrencyInfo {
  user_id: number // Owning user identifier.
  user_email: string // Recorded user email.
  username: string // Account display username.
  current_in_use: number // Current in use.
  max_capacity: number // Recorded max capacity.
  load_percentage: number // Recorded load percentage.
  waiting_in_queue: number // Waiting in queue.
}

/**
 * OpsUserConcurrencyStatsResponse maps user identifiers to observed concurrency
 * usage for the operations user view.
 */
export interface OpsUserConcurrencyStatsResponse {
  enabled: boolean // Feature activation flag.
  user: Record<string, UserConcurrencyInfo> // User-indexed observation map.
  timestamp?: string // Observation snapshot timestamp.
}

/**
 * PlatformAvailability summarizes available, limited and errored providers
 * belonging to one platform.
 */
export interface PlatformAvailability {
  platform: string // Provider platform identifier.
  total_providers: number // Recorded total providers.
  available_count: number // Recorded available count.
  rate_limit_count: number // Rate limit count.
  error_count: number // Recorded error count.
}

/**
 * GroupAvailability summarizes available, limited and errored providers
 * belonging to one group.
 */
export interface GroupAvailability {
  group_id: number // Group record identifier.
  group_name: string // Recorded group name.
  total_providers: number // Recorded total providers.
  available_count: number // Recorded available count.
  rate_limit_count: number // Rate limit count.
  error_count: number // Recorded error count.
}

/**
 * ProviderAvailability reports an individual provider's scheduling state and any
 * rate-limit, overload or error observations.
 */
export interface ProviderAvailability {
  provider_id: number // Provider record identifier.
  provider_name: string // Recorded provider name.
  platform: string // Provider platform identifier.
  group_id: number // Group record identifier.
  group_name: string // Recorded group name.
  status: string // Current server-reported status.
  is_available: boolean // Recorded is available.
  is_rate_limited: boolean // Is rate limited.
  rate_limit_reset_at?: string // Rate limit reset at.
  rate_limit_remaining_sec?: number // Rate limit remaining sec.
  is_overloaded: boolean // Recorded is overloaded.
  overload_until?: string // Recorded overload until.
  overload_remaining_sec?: number // Overload remaining sec.
  has_error: boolean // Recorded has error.
  error_message?: string // Recorded error message.
}

/**
 * OpsProviderAvailabilityStatsResponse groups availability observations by
 * platform, group and provider for operations displays.
 */
export interface OpsProviderAvailabilityStatsResponse {
  enabled: boolean // Feature activation flag.
  platform: Record<string, PlatformAvailability> // Platform-indexed observation map.
  group: Record<string, GroupAvailability> // Group-indexed observation map.
  provider: Record<string, ProviderAvailability> // Provider-indexed observation map.
  timestamp?: string // Observation snapshot timestamp.
}

/**
 * OpsRateSummary gives current, peak and average rates for a throughput
 * observation window.
 */
export interface OpsRateSummary {
  current: number // Current observed rate.
  peak: number // Peak observed rate.
  avg: number // Average observed rate.
}

/**
 * OpsRealtimeTrafficSummary pairs request and token rates with the selected
 * window, platform and optional group.
 */
export interface OpsRealtimeTrafficSummary {
  window: string // Observation interval selector.
  start_time: string // Requested interval start.
  end_time: string // Requested interval end.
  platform: string // Provider platform identifier.
  group_id?: number | null // Group record identifier.
  qps: OpsRateSummary // Requests per second.
  tps: OpsRateSummary // Tokens per second.
}

/**
 * OpsRealtimeTrafficSummaryResponse reports realtime monitoring availability
 * with an optional summary and observation timestamp.
 */
export interface OpsRealtimeTrafficSummaryResponse {
  enabled: boolean // Feature activation flag.
  summary: OpsRealtimeTrafficSummary | null // Aggregated usage summary.
  timestamp?: string // Observation snapshot timestamp.
}

/**
 * OpsSeverity retains the server's severity text for operations records and
 * alert displays.
 */
export type OpsSeverity = string

/**
 * OpsPhase retains the server's lifecycle phase text for recorded request
 * failures.
 */
export type OpsPhase = string

/**
 * AlertSeverity selects critical, warning or informational importance for alert
 * configuration.
 */
export type AlertSeverity = 'critical' | 'warning' | 'info'

/**
 * MetricType enumerates availability, error, resource and provider-state metrics
 * that an alert rule can observe.
 */
export type MetricType =
  | 'success_rate'
  | 'error_rate'
  | 'upstream_error_rate'
  | 'cpu_usage_percent'
  | 'memory_usage_percent'
  | 'disk_usage_percent'
  | 'concurrency_queue_depth'
  | 'group_available_providers'
  | 'group_available_ratio'
  | 'group_rate_limit_ratio'
  | 'provider_rate_limited_count'
  | 'provider_error_count'
  | 'provider_error_ratio'
  | 'overload_provider_count'

/**
 * Operator selects the comparison applied between an observed alert metric and
 * its threshold.
 */
export type Operator = '>' | '>=' | '<' | '<=' | '==' | '!='

/**
 * AlertRule combines an observed metric and threshold comparison with its
 * window, severity and notification policy.
 */
export interface AlertRule {
  id?: number // Record identifier value.
  name: string // Human-readable display name.
  description?: string // Human-readable record description.
  enabled: boolean // Feature activation flag.
  metric_type: MetricType // Recorded metric type.
  operator: Operator // Threshold comparison operator.
  threshold: number // Configured comparison threshold.
  window_minutes: number // Recorded window minutes.
  sustained_minutes: number // Recorded sustained minutes.
  severity: OpsSeverity // Recorded severity level.
  cooldown_minutes: number // Recorded cooldown minutes.
  notify_email: boolean // Recorded notify email.
  filters?: Record<string, unknown> // Applied query filters.
  created_at?: string // Record creation timestamp.
  updated_at?: string // Last modification timestamp.
  last_triggered_at?: string | null // Last triggered at.
}

/**
 * AlertEvent records a fired rule's dimensions, observed value, severity and
 * resolution state.
 */
export interface AlertEvent {
  id: number // Record identifier value.
  rule_id: number // Alert rule identifier.
  severity: OpsSeverity | string // Recorded severity level.
  status: 'firing' | 'resolved' | 'manual_resolved' | string // Current server-reported status.
  title?: string // Human-readable result title.
  description?: string // Human-readable record description.
  metric_value?: number // Recorded metric value.
  threshold_value?: number // Recorded threshold value.
  dimensions?: Record<string, unknown> // Observed metric dimensions.
  fired_at: string // Recorded fired at.
  resolved_at?: string | null // Recorded resolved at.
  email_sent: boolean // Recorded email sent.
  created_at: string // Record creation timestamp.
}

/**
 * AlertEventsQuery selects alert history with status filters and a fired-time
 * plus ID continuation position.
 */
export interface AlertEventsQuery {
  limit?: number // Maximum returned entries.
  status?: string // Current server-reported status.
  severity?: string // Recorded severity level.
  email_sent?: boolean // Requested email sent.
  time_range?: string // Requested time range.
  start_time?: string // Requested interval start.
  end_time?: string // Requested interval end.
  before_fired_at?: string // Before fired at.
  before_id?: number // Pagination predecessor identifier.
  platform?: string // Provider platform identifier.
  group_id?: number // Group record identifier.
}

/**
 * AlertSilenceCreateRequest supplies rule, dimension, expiry, and reason fields
 * when creating a temporary alert silence.
 */
export interface AlertSilenceCreateRequest {
  rule_id: number // Alert rule identifier.
  platform: string // Provider platform identifier.
  group_id?: number | null // Group record identifier.
  region?: string | null // Service deployment region.
  until: string // Suppression expiry timestamp.
  reason?: string // Administrator-supplied silence reason.
}

/**
 * EmailNotificationConfig keeps immediate alert delivery settings separate from
 * scheduled summary and digest reports.
 */
export interface EmailNotificationConfig {
  alert: {
    enabled: boolean // Feature activation flag.
    recipients: string[] // Notification recipient addresses.
    min_severity: AlertSeverity | '' // Recorded min severity.
    rate_limit_per_hour: number // Rate limit per hour.
    batching_window_seconds: number // Batching window seconds.
    include_resolved_alerts: boolean // Include resolved alerts.
  } // Alert notification settings.
  report: {
    enabled: boolean // Feature activation flag.
    recipients: string[] // Notification recipient addresses.
    daily_summary_enabled: boolean // Daily summary enabled.
    daily_summary_schedule: string // Daily summary schedule.
    weekly_summary_enabled: boolean // Weekly summary enabled.
    weekly_summary_schedule: string // Weekly summary schedule.
    error_digest_enabled: boolean // Error digest enabled.
    error_digest_schedule: string // Error digest schedule.
    error_digest_min_count: number // Error digest min count.
    provider_health_enabled: boolean // Provider health enabled.
    provider_health_schedule: string // Provider health schedule.
    provider_health_error_rate_threshold: number // Provider health error rate threshold.
  } // Scheduled report settings.
}

/**
 * OpsMetricThresholds provides optional dashboard warning cutoffs. SLA warnings
 * compare below the minimum, while TTFT and error-rate warnings compare above
 * their corresponding maxima.
 */
export interface OpsMetricThresholds {
  sla_percent_min?: number | null // SLA percentage warning threshold.
  ttft_p99_ms_max?: number | null // TTFT P99 warning threshold.
  request_error_rate_percent_max?: number | null // Request error-rate warning threshold.
  upstream_error_rate_percent_max?: number | null // Upstream error-rate warning threshold.
}

/**
 * OpsDistributedLockSettings configures the shared lock key and lifetime used by
 * alert evaluation.
 */
export interface OpsDistributedLockSettings {
  enabled: boolean // Feature activation flag.
  key: string // Stable entry key.
  ttl_seconds: number // Lifetime in seconds.
}

/**
 * OpsAlertRuntimeSettings combines evaluation scheduling, shared locking,
 * temporary silencing and display threshold settings.
 */
export interface OpsAlertRuntimeSettings {
  evaluation_interval_seconds: number // Evaluation interval seconds.
  distributed_lock: OpsDistributedLockSettings // Configured distributed lock.
  silencing: {
    enabled: boolean // Feature activation flag.
    global_until_rfc3339: string // Global until rfc3339.
    global_reason: string // Recorded global reason.
    entries?: Array<{
      rule_id?: number // Alert rule identifier.
      severities?: Array<OpsSeverity | string> // Selected severity levels.
      until_rfc3339: string // Recorded until rfc3339.
      reason: string // Server-provided outcome reason.
    }> // Submitted update entries.
  } // Temporary alert-suppression settings.
  thresholds: OpsMetricThresholds // Configured category thresholds.
}

/**
 * OpsOpenAIProviderQuotaAutoPauseSettings provides default five-hour and
 * seven-day quota fractions, with zero disabling each default.
 */
export interface OpsOpenAIProviderQuotaAutoPauseSettings {
  /**
   * Global five-hour threshold as a fraction from zero to one; zero disables this
   * default threshold.
   */
  default_threshold_5h: number // Five-hour quota fraction.
  /**
   * Global seven-day threshold as a fraction from zero to one; zero disables this
   * default threshold.
   */
  default_threshold_7d: number // Seven-day quota fraction.
}

/**
 * OpsDataRetentionSettings selects cleanup scheduling and retention durations
 * for error and aggregate metric records.
 */
export interface OpsDataRetentionSettings {
  cleanup_enabled: boolean // Configured cleanup enabled.
  cleanup_schedule: string // Configured cleanup schedule.
  cleanup_batch_size: number // Cleanup batch size.
  cleanup_pause_ms: number // Cleanup pause milliseconds.
  error_log_retention_days: number // Error log retention days.
  minute_metrics_retention_days: number // Minute metrics retention days.
  hourly_metrics_retention_days: number // Hourly metrics retention days.
}

/**
 * OpsAdvancedSettings combines retention, quota-pause, error exclusion and
 * dashboard refresh settings for operations.
 */
export interface OpsAdvancedSettings {
  data_retention: OpsDataRetentionSettings // Configured data retention.
  openai_provider_quota_auto_pause: OpsOpenAIProviderQuotaAutoPauseSettings // Openai provider quota auto pause.
  ignore_count_tokens_errors: boolean // Ignore count tokens errors.
  ignore_context_canceled: boolean // Ignore context canceled.
  ignore_no_available_providers: boolean // Ignore no available providers.
  ignore_invalid_api_key_errors: boolean // Ignore invalid API key errors.
  ignore_insufficient_balance_errors: boolean // Ignore insufficient balance errors.
  ignored_status_codes: number[] // Ignored status codes.
  display_openai_token_stats: boolean // Display openai token stats.
  display_alert_events: boolean // Display alert events.
  auto_refresh_enabled: boolean // Auto refresh enabled.
  auto_refresh_interval_seconds: number // Auto refresh interval seconds.
}

/**
 * OpsRuntimeLogConfig configures runtime logging severity, sampling, caller
 * details and persisted log retention.
 */
export interface OpsRuntimeLogConfig {
  level: 'debug' | 'info' | 'warn' | 'error' // Log severity level.
  enable_sampling: boolean // Configured enable sampling.
  sampling_initial: number // Configured sampling initial.
  sampling_thereafter: number // Configured sampling thereafter.
  caller: boolean // Caller-location logging flag.
  stacktrace_level: 'none' | 'error' | 'fatal' // Configured stacktrace level.
  retention_days: number // Configured retention days.
  source?: string // Recorded observation source.
  updated_at?: string // Last modification timestamp.
  updated_by_user_id?: number // Updated by user ID.
}

/**
 * OpsSystemLog records a structured service log message with optional request
 * and actor correlation fields.
 */
export interface OpsSystemLog {
  id: number // Record identifier value.
  created_at: string // Record creation timestamp.
  host: string // Service host address.
  level: string // Log severity level.
  component: string // Producing service component.
  message: string // Server-provided result message.
  request_id?: string // Unified request identifier.
  client_request_id?: string // Client request ID.
  user_id?: number | null // Owning user identifier.
  api_key_id?: number | null // API-key record identifier.
  provider_id?: number | null // Provider record identifier.
  platform?: string // Provider platform identifier.
  model?: string // Model identifier value.
  extra?: Record<string, unknown> // Structured service-log fields.
}

/**
 * OpsSystemLogListResponse wraps structured service log records in the shared
 * pagination response.
 */
export type OpsSystemLogListResponse = PaginatedResponse<OpsSystemLog>

/**
 * OpsSystemLogQuery selects service logs by interval, host, component and
 * request or actor identifiers.
 */
export interface OpsSystemLogQuery {
  page?: number // Requested page number.
  page_size?: number // Requested page size.
  time_range?: '5m' | '30m' | '1h' | '6h' | '24h' | '7d' | '30d' // Requested time range.
  start_time?: string // Requested interval start.
  end_time?: string // Requested interval end.
  host?: string // Service host address.
  level?: string // Log severity level.
  component?: string // Producing service component.
  request_id?: string // Unified request identifier.
  client_request_id?: string // Client request ID.
  user_id?: number | null // Owning user identifier.
  api_key_id?: number | null // API-key record identifier.
  provider_id?: number | null // Provider record identifier.
  platform?: string // Provider platform identifier.
  model?: string // Model identifier value.
  q?: string // Free-text search query.
}

/**
 * OpsSystemLogCleanupRequest selects structured service logs for
 * administrator-requested cleanup using optional matching filters.
 */
export interface OpsSystemLogCleanupRequest {
  start_time?: string // Requested interval start.
  end_time?: string // Requested interval end.
  host?: string // Service host address.
  level?: string // Log severity level.
  component?: string // Producing service component.
  request_id?: string // Unified request identifier.
  client_request_id?: string // Client request ID.
  user_id?: number | null // Owning user identifier.
  api_key_id?: number | null // API-key record identifier.
  provider_id?: number | null // Provider record identifier.
  platform?: string // Provider platform identifier.
  model?: string // Model identifier value.
  q?: string // Free-text search query.
}

/**
 * OpsSystemLogSinkHealth reports pending log writes, capacity, dropped or failed
 * counts and recent write-delay observations.
 */
export interface OpsSystemLogSinkHealth {
  queue_depth: number // Recorded queue depth.
  queue_capacity: number // Recorded queue capacity.
  dropped_count: number // Recorded dropped count.
  write_failed_count: number // Write failed count.
  written_count: number // Recorded written count.
  avg_write_delay_ms: number // Avg write delay milliseconds.
  last_error?: string // Recorded last error.
}

/**
 * OpsErrorLog carries one recorded request or upstream error with ownership and
 * classification metadata. Optional user, key, provider and group identifiers
 * retain unavailable relationships, while names support display of historical
 * records.
 */
export interface OpsErrorLog {
  id: number // Record identifier value.
  created_at: string // Record creation timestamp.

  phase: OpsPhase // Recorded lifecycle phase.
  type: string // Represented record category.
  error_owner: 'client' | 'provider' | 'platform' | string // Recorded error owner.
  error_source: 'client_request' | 'upstream_http' | 'gateway' | string // Recorded error source.

  severity: OpsSeverity // Recorded severity level.
  status_code: number // HTTP status code.
  platform: string // Provider platform identifier.
  model: string // Model identifier value.

  resolved: boolean // Error resolution flag.
  resolved_at?: string | null // Recorded resolved at.
  resolved_by_user_id?: number | null // Resolved by user ID.

  client_request_id: string // Client request ID.
  request_id: string // Unified request identifier.
  message: string // Server-provided result message.

  user_id?: number | null // Owning user identifier.
  user_email: string // Recorded user email.
  api_key_id?: number | null // API-key record identifier.
  /**
   * Joined API-key name remains available for soft-deleted keys because the stored
   * key record retains its display name.
   */
  api_key_name?: string // API key name.
  api_key_deleted?: boolean // API key deleted.
  provider_id?: number | null // Provider record identifier.
  provider_name: string // Recorded provider name.
  group_id?: number | null // Group record identifier.
  group_name: string // Recorded group name.

  client_ip?: string | null // Recorded client ip.
  request_path?: string // Recorded request path.
  stream?: boolean // Streaming request flag.

  inbound_endpoint?: string // Recorded inbound endpoint.
  upstream_endpoint?: string // Recorded upstream endpoint.
  requested_model?: string // Recorded requested model.
  upstream_model?: string // Recorded upstream model.
  request_type?: number | null // Recorded request mode.
  user_agent?: string // Recorded user agent.
}

/**
 * OpsErrorDetail extends an error row with body, upstream context and stage
 * timings when recorded. Missing timing values remain distinct from measured
 * zero, and upstream details are optional enrichment.
 */
export interface OpsErrorDetail extends OpsErrorLog {
  error_body: string // Recorded error body.

  upstream_status_code?: number | null // Upstream status code.
  upstream_error_message?: string // Upstream error message.
  upstream_error_detail?: string // Upstream error detail.
  upstream_errors?: string // Recorded upstream errors.

  auth_latency_ms?: number | null // Auth latency milliseconds.
  routing_latency_ms?: number | null // Routing latency milliseconds.
  upstream_latency_ms?: number | null // Upstream latency milliseconds.
  response_latency_ms?: number | null // Response latency milliseconds.

  is_business_limited: boolean // Is business limited.

  /**
   * API-key prefix captured when the error occurred, provided the key was valid
   * and had not been deleted.
   */
  api_key_prefix?: string | null // API key prefix.
}

/**
 * OpsErrorLogsResponse wraps recorded operations errors in the shared pagination
 * response for list consumers.
 */
export type OpsErrorLogsResponse = PaginatedResponse<OpsErrorLog>

/**
 * OpsErrorListView selects ordinary, excluded or all error records for the
 * operations error list.
 */
export type OpsErrorListView = 'errors' | 'excluded' | 'all'

/**
 * OpsErrorListQueryParams carries pagination, ownership, classification and
 * ordering filters for administrator error lists. Request errors and upstream
 * errors reuse these filters while selecting their separate endpoints.
 */
export type OpsErrorListQueryParams = {
  request_id?: string // Unified request identifier.
  page?: number // Requested page number.
  page_size?: number // Requested page size.
  time_range?: string // Recorded time range.
  start_time?: string // Requested interval start.
  end_time?: string // Requested interval end.
  platform?: string // Provider platform identifier.
  group_id?: number | null // Group record identifier.
  provider_id?: number | null // Provider record identifier.
  user_id?: number // Owning user identifier.
  api_key_id?: number // API-key record identifier.
  /**
   * Exact model filter matches requested_model when recorded, otherwise the server
   * uses the execution model field for administrator queries.
   */
  model?: string // Model identifier value.

  phase?: string // Recorded lifecycle phase.
  /**
   * User-facing category such as auth or rate_limit; the server expands it into
   * matching error phases and types.
   */
  category?: string // Recorded classification category.
  error_owner?: string // Recorded error owner.
  error_source?: string // Recorded error source.
  resolved?: string // Error resolution flag.
  view?: OpsErrorListView // Error visibility selection.

  q?: string // Free-text search query.
  status_codes?: string // Recorded status codes.
  status_codes_other?: string // Status codes other.

  /**
   * Server sort column is restricted to the error-log ordering allowlist,
   * including created_at, model and status_code.
   */
  sort_by?: string // Requested sort column.
  sort_order?: 'asc' | 'desc' // Requested ordering direction.
}
