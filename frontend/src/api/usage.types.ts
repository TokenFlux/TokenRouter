import type { User } from '@/api/identity.types'
import type { ApiKey } from '@/api/keys.types'
import type { Group } from '@/api/groups.types'
import type { UserSubscription } from '@/api/subscriptions.types'

// usage.types.ts describes request usage, failure records, and aggregated consumption
// reports. Timing fields distinguish stage durations from offsets measured at request
// ingress, and optional observations remain absent when historical records did not
// collect the corresponding measurement.

// UsageRequestType identifies the transport or execution mode recorded for a request.
export type UsageRequestType = 'unknown' | 'sync' | 'stream' | 'ws_v2' | 'cyber' | 'live'

// ImageSizeSource identifies how the billed image resolution was determined for usage.
export type ImageSizeSource = 'output' | 'input' | 'default' | 'legacy'

// ImageSizeBreakdown maps image resolution labels to their observed generated image
// counts.
export type ImageSizeBreakdown = Record<string, number>

// UsageLog records request identity, token consumption, and the user's billing result.
export interface UsageLog {
  // Platform snapshot of the executing provider; historical records receive this value
  // during migration.
  platform: string // Upstream service platform.
  id: number // Record numeric identifier.
  user_id: number | null // Customer account, absent for system requests.
  team_id?: number | null // Owning team identifier.
  api_key_id: number | null // Customer key, absent for system requests.
  provider_id: number | null // Upstream provider identifier.
  request_id: string // Canonical request identifier.
  model: string // Requested model identifier.
  service_tier?: string | null // Applied service tier.
  reasoning_effort?: string | null // Applied reasoning effort.
  requested_reasoning_effort?: string | null // Original requested reasoning effort.
  inbound_endpoint?: string | null // Client request endpoint.
  upstream_endpoint?: string | null // Resolved upstream endpoint.

  group_id: number | null // Routing group identifier.
  subscription_id: number | null // Funding subscription identifier.

  input_tokens: number // Consumed input tokens.
  output_tokens: number // Produced output tokens.
  cache_creation_tokens: number // Written cache tokens.
  cache_read_tokens: number // Reused cache tokens.
  cache_creation_5m_tokens: number // Cache creation 5m tokens.
  cache_creation_1h_tokens: number // Cache creation 1h tokens.

  input_cost: number // Input token cost.
  output_cost: number // Output token cost.
  cache_creation_cost: number // Cache creation cost.
  cache_read_cost: number // Cache read cost.
  total_cost: number // Standard billing amount.
  actual_cost: number // Actual charged amount.
  rate_multiplier: number // Applied billing multiplier.
  long_context_billing_applied: boolean // Long-context pricing activation.
  billing_type: number // Recorded billing category.

  request_type?: UsageRequestType // Request execution mode.
  stream: boolean // Client streaming request flag.
  openai_ws_mode?: boolean // OpenAI WebSocket transport flag.
  native_compaction_v2?: boolean // Native compaction activation.
  duration_ms: number | null // Request duration milliseconds.
  ttft_ms: number | null // First-token latency milliseconds.
  tps?: number | null // Output tokens per second.
  ttft_mode?: string // First-token measurement method.

  // Image generation usage observations.
  image_frame_count?: number // Observed animation frame units.
  image_cache_read_tokens?: number // Explicit cached image tokens.
  image_count: number // Generated image count.
  image_size: string | null // Billed image resolution.
  image_input_size: string | null // Input image resolution.
  image_output_size: string | null // Output image resolution.
  image_input_tokens: number // Image input tokens.
  image_input_cost: number // Image input cost.
  image_output_tokens: number // Image output tokens.
  image_output_cost: number // Image output cost.
  image_size_source: ImageSizeSource | null // Image resolution observation source.
  image_size_breakdown: ImageSizeBreakdown | null // Image counts by resolution.

  // User-Agent
  user_agent: string | null // HTTP User-Agent value.
  ip_address?: string | null // Observed network address.

  // Cache TTL Override
  cache_ttl_overridden: boolean // Cache TTL substitution flag.

  // Recorded pricing unit.
  billing_mode?: string | null // Token, per_request, image, or video.

  created_at: string // Record creation timestamp.

  user?: User // Expanded account details.
  api_key?: ApiKey // Expanded API Key details.
  group?: Group // Expanded routing group.
  subscription?: UserSubscription // Expanded subscription details.
}

// UsageLogProviderSummary supplies the provider identifier and name displayed in usage
// rows.
export interface UsageLogProviderSummary {
  id: number // Record numeric identifier.
  name: string // Record display name.
}

// AdminUsageLog extends user usage records with upstream identity and provider billing
// observations.
export interface AdminUsageLog extends UsageLog {
  request_source?: string // Customer or internal request source.
  system_usage?: {
    parent_request_id: string // Owning internal test run.
    operator_user_id?: number // Initiating administrator, when available.
    outcome: string // Observed upstream execution result.
    usage_state: string // Complete, partial, or missing measurements.
    cost_state: string // Calculated, partial, or unknown cost.
    mapped_model?: string // Model before provider mapping.
    pricing_model?: string // Hosted-image pricing lookup model.
    pricing_source?: string // Cost calculation provenance.
    http_status?: number // Observed upstream HTTP status.
  } // Internal attribution never represents customer funding.

  detailed_timing?: UsageLogTiming | null // Administrative request timing observations.
  upstream_model?: string | null // Requested upstream model identifier.
  upstream_response_model?: string | null // Upstream-reported model identifier.
  upstream_model_mismatch?: boolean | null // Requested and reported model mismatch.
  model_mapping_chain?: string | null // Applied model mapping sequence.
  upstream_request_id?: string | null // Upstream request identifier.

  // Provider billing multiplier returned by administrative usage endpoints.
  provider_rate_multiplier?: number | null // Provider rate multiplier.
  // Custom provider statistics cost; null uses total_cost multiplied by the provider
  // billing multiplier instead.
  provider_stats_cost?: number | null // Custom provider statistics cost.

  // pricing_config_id and billing_tier identify the shared price rule used for this
  // request. Administrative endpoints expose both fields so operators can trace the
  // applied pricing selection.
  pricing_config_id?: number | null // Applied pricing configuration identifier.
  billing_tier?: string | null // Applied pricing tier.

  // Minimal provider identity returned by administrator endpoints for displaying the
  // provider in usage rows.
  provider?: UsageLogProviderSummary // Minimal upstream provider details.
}

// UsageLogTiming carries optional admission durations and ingress-relative transport
// observations.
export interface UsageLogTiming {
  upstream_stream?: boolean // Actual upstream streaming mode.
  upstream_complete?: boolean // Successful upstream terminal observed.
  usage_complete?: boolean // Final output usage observed.
  upstream_started_ms?: number | null // Milliseconds from request ingress.
  upstream_first_output_ms?: number | null // Milliseconds from request ingress.
  upstream_last_output_ms?: number | null // Milliseconds from request ingress.
  upstream_completed_ms?: number | null // Milliseconds from request ingress.
  upstream_finished_ms?: number | null // Milliseconds from request ingress.
  attempt_number?: number | null // Usage-producing exchange ordinal.
  // request_content_length, timing_stream, and timing_admitted retain missing
  // observations for older requests. Absence indicates an unknown body length or mode,
  // while zero remains an observed value.
  request_content_length?: number | null // Observed request body bytes.
  timing_stream?: number | null // Client streaming flag, zero or one.
  timing_admitted?: number | null // Initial admission flag, zero or one.

  // request_body_read_ms, preprocess_ms, user_slot_wait_ms, and provider_slot_wait_ms
  // freeze after initial admission. Later attempts add their provider waits to
  // retry_provider_slot_wait_ms so initial admission remains separately measurable.
  request_body_read_ms?: number | null // Milliseconds spent in stage.
  preprocess_ms?: number | null // Milliseconds spent in stage.
  user_slot_wait_ms?: number | null // Milliseconds spent in stage.
  provider_slot_wait_ms?: number | null // Milliseconds spent in stage.
  // Later attempts accumulate this duration without changing the four initial admission
  // measurements.
  retry_provider_slot_wait_ms?: number | null // Milliseconds spent in stage.

  // Event fields from provider_slot_acquired_ms through first_downstream_flush_ms measure
  // elapsed time from request ingress. They locate observations on one timeline and
  // cannot be summed as independent stage durations.
  provider_slot_acquired_ms?: number | null // Milliseconds from request ingress.
  upstream_get_conn_ms?: number | null // Milliseconds from request ingress.
  upstream_got_conn_ms?: number | null // Milliseconds from request ingress.
  upstream_wrote_request_ms?: number | null // Milliseconds from request ingress.
  upstream_first_response_byte_ms?: number | null // Milliseconds from request ingress.
  upstream_first_event_ms?: number | null // Milliseconds from request ingress.
  first_visible_output_ms?: number | null // Milliseconds from request ingress.
  first_downstream_flush_ms?: number | null // Milliseconds from request ingress.

  // upstream_get_conn_count, upstream_got_conn_count, upstream_attempt_count, and
  // upstream_first_response_byte_count count observed transport events. Zero is a
  // measured count; absent fields indicate historical requests that lack the
  // corresponding observation.
  upstream_get_conn_count?: number | null // Observed transport event count.
  upstream_got_conn_count?: number | null // Observed transport event count.
  upstream_attempt_count?: number | null // Observed transport event count.
  upstream_first_response_byte_count?: number | null // Observed transport event count.

  // upstream_connection_reused and upstream_wrote_request_error record transport facts
  // independently of timing and counters. Each optional boolean retains the distinction
  // between a negative observation and missing instrumentation.
  upstream_connection_reused?: boolean // Observed connection reuse flag.
  upstream_wrote_request_error?: boolean // Observed request write-error flag.
}

// UsageStatsResponse aggregates request consumption and charges over the selected
// reporting period.
export interface UsageStatsResponse {
  incomplete_cost_requests?: number // System requests with partial or unknown cost.
  period?: string // Requested reporting period.
  total_requests: number // Total observed requests.
  total_input_tokens: number // Total input tokens.
  total_output_tokens: number // Total output tokens.
  total_cache_tokens: number // Total cache tokens.
  total_cache_creation_tokens: number // Total cache creation tokens.
  total_cache_read_tokens: number // Total cache read tokens.
  total_tokens: number // Total consumed tokens.
  total_cost: number // Standard request billing amount.
  total_actual_cost: number // Actual account deduction amount.
  average_duration_ms: number // average duration milliseconds.
  models?: Record<string, number> // Numeric totals by model.
  endpoints?: EndpointStat[] // Endpoint usage breakdowns.
  upstream_endpoints?: EndpointStat[] // Upstream endpoint usage breakdowns.
  endpoint_paths?: EndpointStat[] // Request-path usage breakdowns.
}

// TrendDataPoint contains request, token, and spending totals for one time bucket.
export interface TrendDataPoint {
  date: string // Reporting calendar date.
  requests: number // Observed request count.
  input_tokens: number // Consumed input tokens.
  output_tokens: number // Produced output tokens.
  cache_creation_tokens: number // Written cache tokens.
  cache_read_tokens: number // Reused cache tokens.
  total_tokens: number // Total consumed tokens.
  cost: number // Standard request billing amount.
  actual_cost: number // Actual account deduction amount.
}

// ModelStat groups request consumption and billing totals by requested model.
export interface ModelStat {
  model: string // Requested model identifier.
  requests: number // Observed request count.
  input_tokens: number // Consumed input tokens.
  output_tokens: number // Produced output tokens.
  cache_creation_tokens: number // Written cache tokens.
  cache_read_tokens: number // Reused cache tokens.
  total_tokens: number // Total consumed tokens.
  cost: number // Standard request billing amount.
  actual_cost: number // Actual account deduction amount.
  provider_cost?: number // Provider cost returned by administrator endpoints.
}

// EndpointStat groups requests, token counts, and charges by endpoint name.
export interface EndpointStat {
  endpoint: string // API endpoint path.
  requests: number // Observed request count.
  total_tokens: number // Total consumed tokens.
  cost: number // Standard billed amount.
  actual_cost: number // Actual charged amount.
}

// GroupStat groups request consumption and billing totals by routing group.
export interface GroupStat {
  group_id: number // Routing group identifier.
  group_name: string // Routing group display name.
  requests: number // Observed request count.
  total_tokens: number // Total consumed tokens.
  cost: number // Standard request billing amount.
  actual_cost: number // Actual account deduction amount.
  provider_cost?: number // Provider cost returned by administrator endpoints.
}

// UserErrorRequest describes a failed request visible to its owning user.
export interface UserErrorRequest {
  request_id?: string // Canonical request identifier.
  id: number // Record numeric identifier.
  created_at: string // Record creation timestamp.
  model: string // Requested model identifier.
  inbound_endpoint: string // Client request endpoint.
  status_code: number // Observed HTTP status.
  category: string // Request failure category.
  platform: string // Upstream service platform.
  message: string // Human-readable result message.
  key_name: string // Used API Key name.
  key_deleted: boolean // Deleted API Key flag.
  client_ip?: string // Observed client address.
  group_name?: string // Routing group display name.
  request_type?: number // Request execution mode.
  stream?: boolean // Client streaming request flag.
  user_agent?: string // HTTP User-Agent value.
}

// UserErrorRequestDetail adds the returned error body and upstream status to a failure.
export interface UserErrorRequestDetail extends UserErrorRequest {
  error_body: string // Returned error response body.
  upstream_status_code?: number // Upstream HTTP response status.
}

// UserErrorListParams filters and sorts the user's visible failed request records.
export interface UserErrorListParams {
  request_id?: string // Canonical request identifier.
  page?: number // Current page number.
  page_size?: number // Records per page.
  start_date?: string // Reporting interval start.
  end_date?: string // Reporting interval end.
  timezone?: string // IANA timezone name.
  model?: string // Requested model identifier.
  status_code?: number // Observed HTTP status.
  category?: string // Request failure category.
  api_key_id?: number // Used credential identifier.
  // Server sorting supports created_at, model, and status_code as defined by
  // opsErrorLogsOrderBy on the backend.
  sort_by?: string // Server sorting field.
  sort_order?: 'asc' | 'desc' // Server sorting direction.
}

// UsageQueryParams selects usage records by ownership, request attributes, and reporting
// period.
export interface UsageQueryParams {
  request_source?: string // Optional customer or internal source selector.
  request_id?: string // Canonical request identifier.
  page?: number // Current page number.
  page_size?: number // Records per page.
  api_key_id?: number // Used credential identifier.
  user_id?: number // Owning account identifier.
  provider_id?: number // Upstream provider identifier.
  group_id?: number // Routing group identifier.
  model?: string // Requested model identifier.
  request_type?: UsageRequestType // Request execution mode.
  stream?: boolean // Client streaming request flag.
  billing_type?: number | null // Recorded billing category.
  billing_mode?: string | null // Pricing-unit filter: token, per_request, image, or video.
  native_compaction_v2?: boolean | null // Native compaction activation.
  start_date?: string // Reporting interval start.
  end_date?: string // Reporting interval end.
  timezone?: string // IANA timezone name.
  sort_by?: string // Server sorting field.
  sort_order?: 'asc' | 'desc' // Server sorting direction.
}
