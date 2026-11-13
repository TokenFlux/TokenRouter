import apiClient from './client'

/**
 * requests.ts retrieves administrator request diagnostics and their linked usage
 * records. Request lifecycle details and failure observations remain separate
 * from per-usage performance fields, while alias entries preserve the upstream
 * attempt and provider that supplied an observed identifier. Provider names
 * describe the current configuration for IDs in those diagnostics.
 */

/**
 * RequestAlias records an observed request identifier and optional
 * upstream-attempt or provider ownership metadata.
 */
export interface RequestAlias {
  kind: string // Recorded item category.
  value: string // Observed identifier value.
  source?: string // Recorded observation source.
  attempt_number?: number // Owning upstream attempt number.
  provider_id?: number // Owning upstream provider identifier.
}

/**
 * RequestDetail combines request lifecycle diagnostics with linked usage and
 * error records. Usage entries own their captured performance fields, while
 * aliases and attempts retain the identity that produced upstream observations.
 */
export interface RequestDetail {
  request_id: string // Unified request identifier.
  parent_request_id?: string // Parent request ID.
  started_at: string // Operation start timestamp.
  finished_at?: string // Operation completion timestamp.
  method: string // HTTP request method.
  path: string // HTTP request path.
  state: string // Current recorded state.
  error_code?: string // Recorded request error code.
  status_code?: number // HTTP status code.
  duration_ms?: number // Duration in milliseconds.
  model?: string // Model identifier value.
  platform?: string // Provider platform identifier.
  legacy: boolean // Legacy request marker.
  pending?: boolean // Pending request marker.
  usage_write_state?: string // Observed usage persistence result.
  usage_write_error?: string // Bounded persistence failure diagnostic.
  /** Current display names keyed by attempt or alias provider ID, when resolved by the server. */
  provider_names?: Partial<Record<number, string>>
  aliases?: RequestAlias[] // Observed request aliases.
  failure_timings?: Record<string, number> // Failed request stage observations.
  attempts?: {
    number: number // Upstream attempt number.
    provider_id?: number // Provider record identifier.
    upstream_request_id?: string // Upstream request ID.
    upstream_request_id_header?: string // Upstream request ID header.
    status_code?: number // HTTP status code.
    duration_ms?: number | null // Duration in milliseconds.
    outcome?: string // Recorded attempt outcome.
    stage?: string // Recorded attempt stage.
    reason?: string // Server-provided outcome reason.
    failure_timings?: Record<string, number> // Recorded failure timings.
  }[] // Recorded upstream attempts.
  usage:
    | {
        id: number // Record identifier value.
        model: string // Model identifier value.
        input_tokens: number // Consumed input tokens.
        output_tokens: number // Generated output tokens.
        actual_cost: number // Actual deducted cost.
        duration_ms?: number | null // Duration in milliseconds.
        ttft_ms?: number | null // TTFT in milliseconds.
        tps?: number | null // Tokens per second.
        timings?: {
          ttft_mode: string // Recorded ttft mode.
          upstream_stream: boolean // Recorded upstream stream.
          values: Record<string, number> // Named timing observations.
        } // Captured performance observations.
      }[]
    | null // Observed usage entries.
  errors:
    | {
        id: number // Record identifier value.
        status_code: number // HTTP status code.
        phase: string // Recorded lifecycle phase.
      }[]
    | null // Recorded failure details.
  audit_ids?: number[] // Linked audit-record identifiers.
}

/**
 * findRequests queries administrator request diagnostics using the supplied
 * filters. The API client returns the response body after shared authentication
 * and error handling, leaving the request view to render individual diagnostic
 * records. The caller may cancel through the supplied AbortSignal.
 */
export async function findRequests(requestId: string, signal?: AbortSignal) {
  const { data } = await apiClient.get<{
    items: RequestDetail[] // Returned collection entries.
    has_more: boolean // More-results availability flag.
  }>('/admin/requests', {
    params: { request_id: requestId },
    signal,
  })
  return data
}
