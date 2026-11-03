import type { TrendDataPoint } from '@/api/usage.types'

// usageChartData.ts builds dashboard time windows, fills absent usage buckets, and computes
// metric totals. Picker dates represent local calendar days, while trend queries use an
// exclusive end. The helpers retain that distinction when selecting presets, accepting
// custom ranges, and deriving the adjacent comparison period.
//
// The chart needs a continuous horizontal axis even when the API omits unused periods.
// Requested bucket keys control ordering and introduce zero records for those gaps, while
// future periods remain absent. Cache-hit percentages use accumulated input categories, so
// empty input remains distinguishable from a measured zero percentage and differently sized
// buckets receive their proper weight.

// UsageRangePreset identifies a dashboard shortcut or a picker-defined time window.
export type UsageRangePreset = 'today' | '24h' | '7d' | '30d' | '90d' | 'custom'

// UsageGranularity selects hourly or daily buckets in dashboard queries.
export type UsageGranularity = 'hour' | 'day'

// UsageRange carries local calendar instants and their selected aggregation precision.
export interface UsageRange {
  startAt: Date // Inclusive local start.
  endAt: Date // Exclusive local end.
  granularity: UsageGranularity // Query bucket precision.
}

// UsageTotals summarizes request volume, charged cost, and input cache effectiveness.
export interface UsageTotals {
  requests: number // Completed request count.
  tokens: number // Total recorded tokens.
  cost: number // Charged balance amount.
  // cacheHitRate is null when no positive input-side total exists, allowing the chart
  // to render an undefined rate separately from a measured zero percent.
  cacheHitRate: number | null
}

// HOUR_MS and HOURLY_MAX_SPAN_MS express elapsed milliseconds used to select hourly
// aggregation for short windows.
const HOUR_MS = 60 * 60 * 1000
// HOURLY_MAX_SPAN_MS keeps windows of at most two elapsed days in hourly buckets.
const HOURLY_MAX_SPAN_MS = 2 * 24 * HOUR_MS

// pad formats a numeric date part with at least two digits.
const pad = (value: number): string => String(value).padStart(2, '0')

// formatDayKey returns the local calendar date used to match daily API buckets.
export const formatDayKey = (date: Date): string =>
  `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`

// formatHourKey returns the local date and hour used to match hourly API buckets.
export const formatHourKey = (date: Date): string =>
  `${formatDayKey(date)} ${pad(date.getHours())}:00`

// formatQueryTime serializes local calendar components for the dashboard time query
// parameters.
export const formatQueryTime = (date: Date): string =>
  `${formatDayKey(date)}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`

// startOfDay returns a fresh Date at the supplied local calendar midnight.
const startOfDay = (date: Date): Date =>
  new Date(date.getFullYear(), date.getMonth(), date.getDate())

// addDays returns a cloned Date shifted by local calendar days. The input remains unchanged,
// and daylight-saving changes follow Date calendar arithmetic rather than elapsed-day
// multiplication.
const addDays = (date: Date, days: number): Date => {
  const next = new Date(date)
  next.setDate(next.getDate() + days)
  return next
}

// resolveGranularity selects hours for windows spanning at most two elapsed days. Longer
// windows use daily aggregation; callers validate ordering before passing a custom picker
// range.
export const resolveGranularity = (startAt: Date, endAt: Date): UsageGranularity =>
  endAt.getTime() - startAt.getTime() <= HOURLY_MAX_SPAN_MS ? 'hour' : 'day'

// resolvePresetRange expands a shortcut into the aligned dashboard query window. The default
// clock is sampled at invocation, and the current incomplete hour or calendar day remains
// included for live usage display. Today spans local midnight to the following midnight
// with hourly buckets; bucketKeys excludes future hours from the rendered series.
export const resolvePresetRange = (
  preset: Exclude<UsageRangePreset, 'custom'>,
  now: Date = new Date(),
): UsageRange => {
  if (preset === 'today') {
    const startAt = startOfDay(now)
    return { startAt, endAt: addDays(startAt, 1), granularity: 'hour' }
  }
  if (preset === '24h') {
    const endAt = new Date(now.getFullYear(), now.getMonth(), now.getDate(), now.getHours() + 1)
    return { startAt: new Date(endAt.getTime() - 24 * HOUR_MS), endAt, granularity: 'hour' }
  }
  const days = preset === '7d' ? 7 : preset === '30d' ? 30 : 90
  const endAt = addDays(startOfDay(now), 1)
  return { startAt: addDays(endAt, -days), endAt, granularity: 'day' }
}

// parsePickerValue converts picker text into a valid Date or null. Date-only text means
// local midnight; empty text and values producing an invalid timestamp prevent custom range
// selection.
const parsePickerValue = (value: string): Date | null => {
  if (!value) return null
  const date = new Date(value.length === 10 ? `${value}T00:00:00` : value)
  return Number.isFinite(date.getTime()) ? date : null
}

// resolveCustomRange turns picker endpoints into the exclusive-end interval used by
// dashboard queries. Invalid dates and reversed or empty intervals return null so the caller
// retains its previous selection. A date-only end includes that entire local calendar day by
// advancing to the following midnight. Timestamp ends already describe the exclusive instant
// and remain unchanged, with aggregation chosen from the resulting elapsed span.
export const resolveCustomRange = (startValue: string, endValue: string): UsageRange | null => {
  const startAt = parsePickerValue(startValue)
  const parsedEnd = parsePickerValue(endValue)
  if (!startAt || !parsedEnd) return null
  const endAt = endValue.length === 10 ? addDays(parsedEnd, 1) : parsedEnd
  if (endAt.getTime() <= startAt.getTime()) return null
  return { startAt, endAt, granularity: resolveGranularity(startAt, endAt) }
}

// previousRange builds the adjacent comparison interval ending at the current start. Hourly
// windows preserve elapsed duration, while daily windows derive a rounded calendar-day count
// and shift through local dates. That distinction keeps daily query endpoints at calendar
// midnights across daylight-saving changes. The returned endAt shares the supplied startAt
// Date, so consumers use these dates as query values and do not mutate them.
export const previousRange = (range: UsageRange): UsageRange => {
  if (range.granularity === 'day') {
    // Round elapsed days before calendar subtraction so an hour gained or lost at
    // a daylight-saving transition does not shift a daily comparison off midnight.
    const days = Math.round(
      (startOfDay(range.endAt).getTime() - startOfDay(range.startAt).getTime()) / (24 * HOUR_MS),
    )
    return { startAt: addDays(range.startAt, -days), endAt: range.startAt, granularity: 'day' }
  }
  const span = range.endAt.getTime() - range.startAt.getTime()
  return {
    startAt: new Date(range.startAt.getTime() - span),
    endAt: range.startAt,
    granularity: 'hour',
  }
}

// bucketKeys enumerates local bucket labels up to the earlier of the window end and now. The
// current bucket is included, future buckets are omitted, and the supplied Date objects
// remain unchanged.
export const bucketKeys = (range: UsageRange, now: Date = new Date()): string[] => {
  const keys: string[] = []
  const limit = Math.min(range.endAt.getTime(), now.getTime() + 1)
  if (range.granularity === 'hour') {
    const cursor = new Date(range.startAt)
    cursor.setMinutes(0, 0, 0)
    while (cursor.getTime() < limit) {
      keys.push(formatHourKey(cursor))
      cursor.setHours(cursor.getHours() + 1)
    }
    return keys
  }
  let cursor = startOfDay(range.startAt)
  while (cursor.getTime() < limit) {
    keys.push(formatDayKey(cursor))
    cursor = addDays(cursor, 1)
  }
  return keys
}

// emptyPoint supplies a dated zero-usage record for an omitted API bucket.
const emptyPoint = (date: string): TrendDataPoint => ({
  date,
  requests: 0,
  input_tokens: 0,
  output_tokens: 0,
  cache_creation_tokens: 0,
  cache_read_tokens: 0,
  total_tokens: 0,
  cost: 0,
  actual_cost: 0,
})

// fillTrendBuckets returns records in the supplied bucket-key order and fills missing
// records with zeros. Existing records remain shared with points; duplicate API dates
// resolve to the last supplied record.
export const fillTrendBuckets = (points: TrendDataPoint[], keys: string[]): TrendDataPoint[] => {
  const byKey = new Map(points.map((point) => [point.date, point]))
  return keys.map((key) => byKey.get(key) ?? emptyPoint(key))
}

// cacheHitRateOf returns cache-read tokens as a percentage of all input-side tokens. Input,
// cache creation, and cache reads are separate quantities in these usage records, so all
// three contribute to the denominator. No positive input-side total returns null, allowing
// the chart to distinguish an undefined rate from measured zero cache hits. Callers
// aggregate token counts first when computing a period-wide rate.
export const cacheHitRateOf = (
  input: number,
  cacheCreation: number,
  cacheRead: number,
): number | null => {
  const inputSide = input + cacheCreation + cacheRead
  return inputSide > 0 ? (cacheRead / inputSide) * 100 : null
}

// summarizeTrend sums requests, total tokens, charged cost, and input-side token categories
// across the supplied records. Cache effectiveness is calculated from those accumulated
// input categories, so periods with different token volumes contribute proportionally to the
// final percentage. Averaging each bucket percentage would give small buckets the same
// weight as large ones. Empty input returns zero additive totals and a null cache-hit rate.
export const summarizeTrend = (points: TrendDataPoint[]): UsageTotals => {
  let requests = 0
  let tokens = 0
  let cost = 0
  let input = 0
  let cacheCreation = 0
  let cacheRead = 0
  for (const point of points) {
    requests += point.requests
    tokens += point.total_tokens
    cost += point.actual_cost
    input += point.input_tokens
    cacheCreation += point.cache_creation_tokens
    cacheRead += point.cache_read_tokens
  }
  return { requests, tokens, cost, cacheHitRate: cacheHitRateOf(input, cacheCreation, cacheRead) }
}
