import type { UsageLogTiming } from '@/api/usage.types'

/**
 * requestTiming.ts groups recorded request timing into three chronological stages
 * alongside payload observations for usage displays and exports:
 *   1. Pre-slot admission durations (including ordered sub-steps inside preprocess_ms),
 *   2. Post-slot provider preparation and retry queueing before upstream dispatch,
 *   3. Ingress-relative transport and streaming timeline milestones.
 *
 * A record with any admission duration receives the complete set of four duration rows,
 * retaining null for unrecorded observations. The total exists only when every duration
 * is present. Preprocess sub-stages retain only observed entries, client and upstream
 * stream flags filter their own event groups independently, and positive step deltas
 * annotate consecutive observed timeline milestones. Export retains all present fields.
 */

/**
 * admissionTimingKeys fixes the admission-duration row order and selects the fields
 * required before a complete admission total can be displayed.
 */
export const admissionTimingKeys = [
  'request_body_read_ms',
  'preprocess_ms',
  'user_slot_wait_ms',
  'provider_slot_wait_ms',
] as const

/**
 * preprocessTimingKeys orders optional gateway sub-stages executed inside preprocess_ms
 * prior to provider slot acquisition.
 */
export const preprocessTimingKeys = [
  'request_body_decode_ms',
  'ingress_validate_ms',
  'bootstrap_ms',
  'moderation_extract_ms',
  'moderation_external_ms',
  'session_ms',
  'scheduler_select_ms',
] as const

/**
 * eventTimingKeys fixes the display order of ingress-relative event offsets, keeping
 * transport milestones and downstream delivery observations in one timeline.
 */
export const eventTimingKeys = [
  'provider_slot_acquired_ms',
  'upstream_started_ms',
  'upstream_get_conn_ms',
  'upstream_got_conn_ms',
  'upstream_wrote_request_ms',
  'upstream_first_response_byte_ms',
  'upstream_first_event_ms',
  'upstream_first_output_ms',
  'upstream_last_output_ms',
  'upstream_completed_ms',
  'upstream_finished_ms',
  'first_visible_output_ms',
  'first_downstream_flush_ms',
] as const

/**
 * streamOnlyTimingKeys identifies downstream delivery events hidden when the resolved
 * client stream flag indicates a nonstreaming response for the record.
 */
const streamOnlyTimingKeys: ReadonlySet<TimingKey> = new Set([
  'first_visible_output_ms',
  'first_downstream_flush_ms',
])

/**
 * TimingKey names one recognized timing duration or event-offset display field.
 */
export type TimingKey =
  | (typeof admissionTimingKeys)[number]
  | (typeof preprocessTimingKeys)[number]
  | 'provider_prepare_ms'
  | 'retry_provider_slot_wait_ms'
  | (typeof eventTimingKeys)[number]

/**
 * TimingRow pairs a recognized metric with observed milliseconds or a missing value.
 */
export interface TimingRow {
  key: TimingKey // recognized metric name
  value: number | null // observed milliseconds or null
}

/**
 * EventTimingRow pairs an ingress-relative milestone with its positive step delta from
 * the preceding observed milestone when monotonic.
 */
export interface EventTimingRow extends TimingRow {
  deltaMs: number | null // positive elapsed milliseconds since previous observed event
}

/**
 * TimingBreakdown groups display rows, payload metadata, completeness totals, and
 * admission state for one request.
 */
export interface TimingBreakdown {
  /**
   * durations and admissionTotal describe the admission split together. Any
   * observed component creates four duration rows; the total remains null until
   * all four component values exist.
   */
  durations: TimingRow[] // admission duration rows
  admissionTotal: number | null // complete duration sum

  /**
   * preprocessSubstages holds observed gateway sub-stages executed inside
   * preprocess_ms in execution order, omitting steps not run on the recorded route.
   */
  preprocessSubstages: TimingRow[]

  /**
   * providerPrepare and retryWait describe work occurring after initial slot
   * acquisition and before upstream request dispatch: retryWait appears only when
   * positive, while providerPrepare retains observed zero and positive durations.
   */
  providerPrepare: TimingRow | null // post-slot attempt preparation duration
  retryWait: TimingRow | null // positive retry provider queue duration

  events: EventTimingRow[] // ingress-relative event offsets with step deltas

  /**
   * requestBytes, decodedBytes, upstreamBytes, and compressed expose wire, normalized,
   * and outbound payload measurements; requestBytes prefers request_raw_bytes over
   * request_content_length so chunked uploads still report ingress size.
   */
  requestBytes: number | null // wire or declared ingress bytes
  decodedBytes: number | null // normalized JSON request bytes
  upstreamBytes: number | null // serialized upstream request bytes
  compressed: boolean | null // transport compression state, or null when unrecorded

  /**
   * admitted is null when timing_admitted was absent; otherwise it reports whether
   * the stored flag equals one for this record.
   */
  admitted: boolean | null
}

/**
 * timingValue reads one observation without deriving it from adjacent measurements.
 * Nullish values become null, while measured zero remains numeric zero so all display
 * consumers can distinguish missing observations.
 */
export function timingValue(timing: UsageLogTiming, key: TimingKey): number | null {
  return timing[key] ?? null
}

/**
 * timingBreakdown selects timing rows using admission completeness and independent
 * client/upstream stream flags. The record's timing_stream takes precedence over the
 * optional legacy argument, and unresolved flags leave corresponding event rows
 * visible.
 *
 * At least one admission measurement creates all four duration rows, but their sum is
 * available only when every row has a value. Retry queue time appears separately when
 * positive, and missing observations remain null. This keeps incomplete historical
 * records distinguishable from recorded zero durations while filtering delivery events
 * according to their own transport mode.
 */
export function timingBreakdown(timing: UsageLogTiming, stream?: boolean): TimingBreakdown {
  /**
   * Prefer the persisted client mode over the legacy argument so a separately supplied
   * flag cannot relabel recorded downstream delivery milestones.
   */
  const streaming = timing.timing_stream == null ? stream : timing.timing_stream === 1
  /**
   * row pairs a recognized timing key with its stored observation.
   */
  const row = (key: TimingKey): TimingRow => ({ key, value: timingValue(timing, key) })
  /**
   * Create the duration group when any component exists, retaining null companions so
   * partial records remain distinguishable from a complete zero-duration split.
   */
  const durations = admissionTimingKeys.some((key) => timing[key] != null)
    ? admissionTimingKeys.map(row)
    : []
  const complete = durations.length > 0 && durations.every((item) => item.value != null)
  const preprocessSubstages = preprocessTimingKeys.filter((key) => timing[key] != null).map(row)
  const providerPrepare = timing.provider_prepare_ms != null ? row('provider_prepare_ms') : null
  /**
   * Filter upstream milestones by upstream mode and downstream delivery by client mode
   * because protocol conversion can make those stream settings differ.
   */
  let prevObservedEventMs: number | null = null
  const events: EventTimingRow[] = eventTimingKeys
    .filter(
      (key) =>
        (streaming !== false || !streamOnlyTimingKeys.has(key)) &&
        (timing.upstream_stream !== false ||
          ![
            'upstream_first_event_ms',
            'upstream_first_output_ms',
            'upstream_last_output_ms',
            'upstream_completed_ms',
          ].includes(key)),
    )
    .map((key) => {
      const value = timingValue(timing, key)
      let deltaMs: number | null = null
      if (value != null) {
        if (prevObservedEventMs != null && value > prevObservedEventMs) {
          deltaMs = value - prevObservedEventMs
        }
        prevObservedEventMs = value
      }
      return { key, value, deltaMs }
    })
  return {
    durations,
    admissionTotal: complete ? durations.reduce((sum, item) => sum + (item.value ?? 0), 0) : null,
    preprocessSubstages,
    providerPrepare,

    /**
     * Positive retry queue time is reported separately from the initial admission
     * split, so it cannot inflate the complete admission duration total.
     */
    retryWait:
      (timing.retry_provider_slot_wait_ms ?? 0) > 0 ? row('retry_provider_slot_wait_ms') : null,
    events,
    requestBytes: timing.request_raw_bytes ?? timing.request_content_length ?? null,
    decodedBytes: timing.request_decoded_bytes ?? null,
    upstreamBytes: timing.upstream_request_bytes ?? null,
    compressed: timing.request_compressed == null ? null : timing.request_compressed === 1,
    admitted: timing.timing_admitted == null ? null : timing.timing_admitted === 1,
  }
}

/**
 * formatTimingMs renders milliseconds below one second, two-decimal seconds below one
 * minute, and minute/second pairs thereafter. Nullish inputs render a dash;
 * minute-scale values round to whole seconds before decomposition.
 */
export function formatTimingMs(ms: number | null | undefined): string {
  if (ms == null) return '-'
  /**
   * Use milliseconds below one second to retain the recorded granularity.
   */
  if (ms < 1000) return `${ms}ms`
  /**
   * Convert milliseconds into seconds before rounding the displayed interval.
   */
  if (ms < 60_000) return `${(ms / 1000).toFixed(2)}s`

  /**
   * Round before decomposing minutes so second rollover remains consistent.
   */
  const totalSec = Math.round(ms / 1000)
  return `${Math.floor(totalSec / 60)}m ${totalSec % 60}s`
}

/**
 * formatRequestSize renders bytes using binary thresholds with B, KB, or MB labels.
 * Nullish observations render a dash, while kilobytes retain one decimal and
 * megabytes retain two decimals.
 */
export function formatRequestSize(bytes: number | null | undefined): string {
  if (bytes == null) return '-'
  /**
   * Use binary unit thresholds for recorded request payload sizes.
   */
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`
  return `${(bytes / (1024 * 1024)).toFixed(2)}MB`
}

/**
 * formatRequestTimingForExport serializes all present timing fields in object entry
 * order. Nullish fields are omitted, missing objects produce empty text, and names
 * ending in _ms append the milliseconds suffix.
 */
export function formatRequestTimingForExport(timing?: UsageLogTiming | null): string {
  if (!timing) return ''
  const fields = Object.entries(timing).filter(([, value]) => value != null)
  return fields
    .map(([key, value]) => `${key}=${value}${key.endsWith('_ms') ? 'ms' : ''}`)
    .join('; ')
}
