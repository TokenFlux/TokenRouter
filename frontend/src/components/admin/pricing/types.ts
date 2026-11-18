import type { BillingMode, TimePricingConfig, PricingInterval } from '@/api/admin/pricing'

// types.ts converts administrator price drafts and validates their intervals, schedules, and
// model patterns. Token amounts cross between million-token form units and per-token API units,
// while media prices retain their output or duration units. Null and empty inputs remain
// distinct from configured free amounts.
//
// Validation combines interval bounds, billing mode, and optional amounts. Token ranges must
// not overlap, and an unbounded range must be last when present; media tiers use labels,
// including canonical video aliases and custom dimensions. Schedule validation interprets
// midnight end markers, checks timezone and multiplier formats, and compares sorted daily
// periods. Pattern matching handles exact names and trailing-prefix wildcards for conflict
// detection.

// TranslateFn resolves pricing validation messages with their optional interpolation arguments.
type TranslateFn = (key: string, params?: Record<string, unknown>) => string

// IntervalFormEntry holds one editable context or named media price interval. Nullable amount
// and multiplier fields accept empty numeric-input text during editing; converters turn cleared
// values into null and retain explicit zero amounts.
export interface IntervalFormEntry {
  min_tokens: number // exclusive token lower bound
  // max_tokens is the inclusive token upper bound. Null represents an unbounded range, which
  // token validation requires to be the final interval.
  max_tokens: number | null
  tier_label: string // media tier label
  input_price: number | string | null // USD per million input tokens
  output_price: number | string | null // USD per million output tokens
  cache_write_price: number | string | null // USD per million cache-write tokens
  cache_write_1h_price?: number | string | null // USD per million hour-cache-write tokens
  cache_write_30m_price?: number | string | null // USD per million 30m cache-write tokens
  cache_write_12h_price?: number | string | null // USD per million 12h cache-write tokens
  cache_read_price: number | string | null // USD per million cache-read tokens
  input_multiplier: number | string | null // input token price factor
  output_multiplier: number | string | null // output token price factor
  cache_write_multiplier: number | string | null // cache-write price factor
  cache_read_multiplier: number | string | null // cache-read price factor
  per_request_price: number | string | null // request or media unit price
  sort_order: number // persisted interval ordering
}

// PricingFormEntry represents a model price card with editable nullable amounts. Token price
// fields use million-token units in the form, media request prices retain their billing units,
// and empty inputs are normalized during serialization.
export interface PricingFormEntry {
  // reference_model remains editable draft state when custom mode is selected; only
  // reference mode sends its trimmed value to the configuration API.
  reference_model?: string // Retained reference target draft.
  reference_mode?: boolean // Selects reference-only serialization.

  models: string[] // ordered model patterns
  billing_mode: BillingMode // selected billing mode

  // price_multiplier scales the configured price. Null and empty text mean unconfigured, while
  // zero remains an explicit multiplier checked by the form validator.
  price_multiplier: number | string | null

  // fast_mode_multiplier stores the legacy Fast override, including zero. A configured current
  // Fast multiplier takes precedence during hydration and clears this legacy field.
  fast_mode_multiplier: number | string | null
  // fast_multiplier, flex_multiplier, and max_reasoning_effort_multiplier are optional token
  // factors. Validation requires configured values to be positive; empty inputs keep the
  // inherited factor available.
  fast_multiplier?: number | string | null // Fast service-tier factor
  flex_multiplier?: number | string | null // Flex service-tier factor
  max_reasoning_effort_multiplier?: number | string | null // maximum reasoning price factor
  input_price: number | string | null // USD per million input tokens
  output_price: number | string | null // USD per million output tokens
  cache_write_price: number | string | null // USD per million cache-write tokens
  cache_write_1h_price?: number | string | null // USD per million hour-cache-write tokens
  cache_write_30m_price?: number | string | null // USD per million 30m cache-write tokens
  cache_write_12h_price?: number | string | null // USD per million 12h cache-write tokens
  cache_read_price: number | string | null // USD per million cache-read tokens
  image_input_price: number | string | null // USD per million image-input tokens
  image_output_price: number | string | null // USD per million image-output tokens
  per_request_price: number | string | null // request or media unit price
  intervals: IntervalFormEntry[] // ordered price interval drafts
  time_pricing: TimePricingFormEntry // recurring price schedule
}

// TimePricingPeriodFormEntry represents one daily clock interval and its editable multiplier.
export interface TimePricingPeriodFormEntry {
  start_time: string // HH:mm:ss
  // end_time uses seconds-resolution clock text. Schedule validation interprets 00:00:00 as the
  // end of the day when calculating an interval.
  end_time: string
  multiplier: number | string // period price factor
}

// TimePricingFormEntry combines a schedule timezone, weekday scope, and daily periods.
export interface TimePricingFormEntry {
  timezone: string // IANA timezone identifier
  weekdays_only: boolean // weekday-only schedule flag
  periods: TimePricingPeriodFormEntry[] // daily price periods
}

// DEFAULT_TIME_PRICING_TIMEZONE supplies the timezone used when new or older schedules have no
// configured timezone value.
export const DEFAULT_TIME_PRICING_TIMEZONE = 'Asia/Shanghai'

// CLOCK_TIME and LEGACY_CLOCK_TIME identify seconds and minute clock encodings, while
// TWO_DECIMAL_MULTIPLIER limits editable schedule precision.
const CLOCK_TIME = /^(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d$/
const LEGACY_CLOCK_TIME = /^(?:[01]\d|2[0-3]):[0-5]\d$/
const TWO_DECIMAL_MULTIPLIER = /^\d+(?:\.\d{1,2})?$/

// isValidTimePricingMultiplier accepts decimal schedule factors with at most two fractional
// digits. The numeric value must be finite and at least 0.01, so zero and alternate numeric
// encodings fail.
export function isValidTimePricingMultiplier(value: number | string): boolean {
  const multiplier = String(value)
  const numericValue = Number(multiplier)
  return (
    TWO_DECIMAL_MULTIPLIER.test(multiplier) && Number.isFinite(numericValue) && numericValue >= 0.01
  )
}

// COMMON_TIMEZONES supplies the schedule selector choices; validation separately accepts
// timezones recognized by the browser internationalization implementation.
export const COMMON_TIMEZONES = [
  'UTC',
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Asia/Singapore',
  'Asia/Kolkata',
  'Australia/Sydney',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Toronto',
  'America/Sao_Paulo',
  'Pacific/Auckland',
  'Pacific/Honolulu',
]

// createDefaultTimePricingForm returns a fresh schedule using Asia/Shanghai with no periods.
// The weekday-only flag starts false, and the empty period list keeps time pricing unconfigured
// until a period is added.
export function createDefaultTimePricingForm(): TimePricingFormEntry {
  return { timezone: DEFAULT_TIME_PRICING_TIMEZONE, weekdays_only: false, periods: [] }
}

// apiTimePricingToForm converts an optional stored schedule into editable period fields.
// Missing schedules use fresh defaults, missing timezone uses the default timezone, and legacy
// minute-only endpoints gain seconds. Multipliers are displayed with two decimal places. Legacy
// endpoints remain comparable with newly entered seconds-resolution values after hydration. The
// converted periods are newly allocated, so editing their clock text or multiplier does not
// mutate the stored API schedule supplied by the caller.
export function apiTimePricingToForm(
  value: TimePricingConfig | null | undefined,
): TimePricingFormEntry {
  if (!value) return createDefaultTimePricingForm()
  return {
    timezone: value.timezone || DEFAULT_TIME_PRICING_TIMEZONE,
    weekdays_only: value.weekdays_only === true,
    // Saved minute-only clocks gain seconds for the editor, and schedule factors are rounded
    // to two-decimal text so legacy records use the current field representations.
    periods: (value.periods || []).map((period) => ({
      start_time: LEGACY_CLOCK_TIME.test(period.start_time)
        ? `${period.start_time}:00`
        : period.start_time,
      end_time: LEGACY_CLOCK_TIME.test(period.end_time) ? `${period.end_time}:00` : period.end_time,
      multiplier: Number(period.multiplier).toFixed(2),
    })),
  }
}

// formTimePricingToAPI converts configured schedule periods into numeric API values. Missing or
// empty period lists return null, timezone text is trimmed, and the weekday flag is enabled
// only by true.
export function formTimePricingToAPI(
  value: TimePricingFormEntry | null | undefined,
): TimePricingConfig | null {
  if (!value?.periods?.length) return null
  return {
    timezone: typeof value.timezone === 'string' ? value.timezone.trim() : '',
    weekdays_only: value.weekdays_only === true,
    periods: value.periods.map((period) => ({
      start_time: period.start_time,
      end_time: period.end_time,
      multiplier: Number(period.multiplier),
    })),
  }
}

// timeToSeconds converts a validated clock value to seconds from midnight. An end value of
// 00:00:00 represents the end of the day, while a start value represents its beginning.
function timeToSeconds(value: string, isEnd: boolean): number {
  // Midnight end markers represent the following day boundary.
  if (isEnd && value === '00:00:00') return 24 * 60 * 60
  const [hours, minutes, seconds] = value.split(':').map(Number)
  // Convert clock components to comparable seconds within one day.
  return hours * 60 * 60 + minutes * 60 + seconds
}

// timePricingValidationMessage translates a validation key inside the time-pricing message
// namespace.
function timePricingValidationMessage(t: TranslateFn, key: string): string {
  return t(`admin.pricing.timePricingValidation.${key}`)
}

// validateTimePricing checks configured daily periods and returns the first localized failure.
// An empty period list disables scheduling and passes immediately. Configured schedules require
// a recognized timezone, seconds-resolution clock values, increasing ranges, and valid positive
// two-decimal multipliers.
//
// Midnight is interpreted as day end for end values, so a period can finish at 00:00:00 while
// starting later in the day. A sorted copy detects overlap without reordering the draft, and
// touching endpoints are accepted.
export function validateTimePricing(value: TimePricingFormEntry, t: TranslateFn): string | null {
  if (!value?.periods?.length) return null
  const timezone = typeof value.timezone === 'string' ? value.timezone.trim() : ''
  if (!timezone) return timePricingValidationMessage(t, 'timezone')

  try {
    // Use the browser timezone implementation to reject unsupported zone identifiers before
    // comparing daily periods that require a valid local time basis.
    new Intl.DateTimeFormat('en-US', { timeZone: timezone })
  } catch {
    return timePricingValidationMessage(t, 'timezone')
  }

  const periods: {
    start: number // start seconds after midnight
    end: number // end seconds after midnight
  }[] = []
  for (const period of value.periods) {
    if (!CLOCK_TIME.test(period.start_time) || !CLOCK_TIME.test(period.end_time)) {
      return timePricingValidationMessage(t, 'format')
    }
    if (period.start_time === period.end_time) {
      return timePricingValidationMessage(t, 'range')
    }
    const start = timeToSeconds(period.start_time, false)
    const end = timeToSeconds(period.end_time, true)
    if (start >= end) return timePricingValidationMessage(t, 'range')
    if (!isValidTimePricingMultiplier(period.multiplier)) {
      return timePricingValidationMessage(t, 'multiplier')
    }
    periods.push({ start, end })
  }

  const sorted = [...periods].sort((a, b) => a.start - b.start)
  for (let i = 1; i < sorted.length; i++) {
    if (sorted[i].start < sorted[i - 1].end) {
      return timePricingValidationMessage(t, 'overlap')
    }
  }
  return null
}

// formatTimezoneOffset formats a timezone's UTC offset at the supplied date. The date defaults
// to now; invalid timezones or unexpected offset formats return empty text, while GMT becomes
// UTC+00:00.
export function formatTimezoneOffset(timezone: string, at = new Date()): string {
  try {
    const part = new Intl.DateTimeFormat('en-US', {
      timeZone: timezone,
      timeZoneName: 'shortOffset',
    })
      .formatToParts(at)
      .find((item) => item.type === 'timeZoneName')?.value
    if (!part || part === 'GMT') return 'UTC+00:00'
    const match = /^GMT([+-])(\d{1,2})(?::(\d{2}))?$/.exec(part)
    if (!match) return ''
    return `UTC${match[1]}${match[2].padStart(2, '0')}:${match[3] || '00'}`
  } catch {
    // An unsupported timezone leaves no formatted offset; the selector can still display its
    // timezone identifier because its option builder appends offsets only when available.
    return ''
  }
}

// MTOK defines the million-token unit used by form amounts and both directions of API
// token-price conversion.
const MTOK = 1_000_000

// toNullableNumber converts a clearable numeric input to a number or null. Null, undefined,
// empty strings, and NaN results become null; other Number conversions are returned for
// subsequent validation.
export function toNullableNumber(val: number | string | null | undefined): number | null {
  if (val === null || val === undefined || val === '') return null
  const num = Number(val)
  // Failed numeric conversion uses the same null representation as cleared input so price
  // serializers represent unusable amounts with the cleared-input value consumed by the API.
  return isNaN(num) ? null : num
}

// isValidPositiveMultiplier accepts omitted multipliers or finite positive numeric values.
// Null, undefined, and empty strings mean inheritance, while zero, negative values, and
// nonfinite conversions are rejected by this check.
export function isValidPositiveMultiplier(val: number | string | null | undefined): boolean {
  if (val === null || val === undefined || val === '') return true
  const multiplier = Number(val)
  return Number.isFinite(multiplier) && multiplier > 0
}

// mTokToPerToken converts a form amount per million tokens into a per-token amount.
// Unconfigured inputs remain null, and ten significant digits limit floating-point
// multiplication or division artifacts in serialized prices.
export function mTokToPerToken(val: number | string | null | undefined): number | null {
  const num = toNullableNumber(val)
  // Convert million-token inputs into stored per-token price units.
  return num === null ? null : parseFloat((num / MTOK).toPrecision(10))
}

// perTokenToMTok converts a stored per-token amount into the form's million-token units. Null
// and undefined remain null, and ten significant digits reduce floating-point artifacts in the
// displayed amount.
export function perTokenToMTok(val: number | null | undefined): number | null {
  if (val === null || val === undefined) return null
  // Ten significant digits suppress small binary arithmetic artifacts, keeping values such as
  // 0.05 readable after million-token conversion.
  return parseFloat((val * MTOK).toPrecision(10))
}

// hasConfiguredPrice reports whether a value is present, including an explicit zero.
function hasConfiguredPrice(val: number | string | null | undefined): boolean {
  return val !== null && val !== undefined && val !== ''
}

// hasExplicitPricing reports whether a card configures a value used by its billing mode.
// Per-request, image, and video cards inspect request amounts on the card or its intervals.
// Token cards inspect token and image-token amounts, then interval token amounts and interval
// multipliers.
//
// Presence includes zero and excludes null, undefined, and empty strings; numeric validity is
// checked separately. Fields belonging to other billing modes do not satisfy this presence
// test, so a hidden stale amount cannot make an unrelated mode appear configured.
export function hasExplicitPricing(entry: PricingFormEntry): boolean {
  if (
    entry.billing_mode === 'per_request' ||
    entry.billing_mode === 'image' ||
    entry.billing_mode === 'video'
  ) {
    return (
      hasConfiguredPrice(entry.per_request_price) ||
      entry.intervals.some((iv) => hasConfiguredPrice(iv.per_request_price))
    )
  }

  if (
    [
      entry.input_price,
      entry.output_price,
      entry.cache_write_price,
      entry.cache_write_1h_price,
      entry.cache_write_30m_price,
      entry.cache_write_12h_price,
      entry.cache_read_price,
      entry.image_input_price,
      entry.image_output_price,
    ].some(hasConfiguredPrice)
  ) {
    return true
  }

  return entry.intervals.some((iv) =>
    [
      iv.input_price,
      iv.output_price,
      iv.cache_write_price,
      iv.cache_write_1h_price,
      iv.cache_write_30m_price,
      iv.cache_write_12h_price,
      iv.cache_read_price,
      iv.input_multiplier,
      iv.output_multiplier,
      iv.cache_write_multiplier,
      iv.cache_read_multiplier,
    ].some(hasConfiguredPrice),
  )
}

// apiIntervalsToForm maps stored intervals into fresh editable rows. Token amounts become
// million-token values, missing labels become empty text, and bounds, media amounts,
// multipliers, and sort positions retain their API units.
export function apiIntervalsToForm(intervals: PricingInterval[]): IntervalFormEntry[] {
  return (intervals || []).map((iv) => ({
    min_tokens: iv.min_tokens,
    max_tokens: iv.max_tokens,
    tier_label: iv.tier_label || '',
    input_price: perTokenToMTok(iv.input_price),
    output_price: perTokenToMTok(iv.output_price),
    cache_write_price: perTokenToMTok(iv.cache_write_price),
    cache_write_1h_price: perTokenToMTok(iv.cache_write_1h_price),
    cache_write_30m_price: perTokenToMTok(iv.cache_write_30m_price),
    cache_write_12h_price: perTokenToMTok(iv.cache_write_12h_price),
    cache_read_price: perTokenToMTok(iv.cache_read_price),
    input_multiplier: iv.input_multiplier,
    output_multiplier: iv.output_multiplier,
    cache_write_multiplier: iv.cache_write_multiplier,
    cache_read_multiplier: iv.cache_read_multiplier,
    per_request_price: iv.per_request_price,
    sort_order: iv.sort_order,
  }))
}

// formIntervalsToAPI maps draft intervals into fresh API rows. Token amounts become per-token
// values, clearable media amounts and multipliers become nullable numbers, and labels, bounds,
// and sort positions retain their values.
export function formIntervalsToAPI(intervals: IntervalFormEntry[]): PricingInterval[] {
  return (intervals || []).map((iv) => ({
    min_tokens: iv.min_tokens,
    max_tokens: iv.max_tokens,
    tier_label: iv.tier_label,
    input_price: mTokToPerToken(iv.input_price),
    output_price: mTokToPerToken(iv.output_price),
    cache_write_price: mTokToPerToken(iv.cache_write_price),
    cache_write_1h_price: mTokToPerToken(iv.cache_write_1h_price),
    cache_write_30m_price: mTokToPerToken(iv.cache_write_30m_price),
    cache_write_12h_price: mTokToPerToken(iv.cache_write_12h_price),
    cache_read_price: mTokToPerToken(iv.cache_read_price),
    input_multiplier: toNullableNumber(iv.input_multiplier),
    output_multiplier: toNullableNumber(iv.output_multiplier),
    cache_write_multiplier: toNullableNumber(iv.cache_write_multiplier),
    cache_read_multiplier: toNullableNumber(iv.cache_read_multiplier),
    per_request_price: toNullableNumber(iv.per_request_price),
    sort_order: iv.sort_order,
  }))
}

// ModelPattern stores an input model pattern and its normalized comparison prefix.
interface ModelPattern {
  pattern: string // model matching pattern
  prefix: string // lowercase comparison prefix

  wildcard: boolean // trailing wildcard flag
}

// toModelPattern prepares a model identifier for case-insensitive conflict comparison. A
// trailing asterisk selects prefix matching and is removed from the comparison prefix, while
// the original pattern remains available for errors.
function toModelPattern(model: string): ModelPattern {
  const lower = model.toLowerCase()
  const wildcard = lower.endsWith('*')
  return {
    pattern: model,
    prefix: wildcard ? lower.slice(0, -1) : lower,
    wildcard,
  }
}

// patternsConflict reports whether two normalized model patterns can match the same identifier.
// Exact patterns compare their complete names, an exact name matches a wildcard when it starts
// with that prefix, and two wildcards conflict when either prefix contains the other. Comparing
// wildcard prefixes directly covers their shared set of possible model identifiers. The caller
// retains the input patterns separately so the detected pair can be reported using
// administrator-facing names.
function patternsConflict(a: ModelPattern, b: ModelPattern): boolean {
  if (!a.wildcard && !b.wildcard) return a.prefix === b.prefix
  if (a.wildcard && !b.wildcard) return b.prefix.startsWith(a.prefix)
  if (!a.wildcard && b.wildcard) return a.prefix.startsWith(b.prefix)
  // Two wildcard prefixes share possible matches when either prefix starts with the other, so
  // their price patterns conflict.
  return a.prefix.startsWith(b.prefix) || b.prefix.startsWith(a.prefix)
}

// findModelConflict returns the first pair of conflicting model patterns in input order. It
// compares normalized exact names and trailing wildcards, while null indicates no conflicting
// pair was found.
export function findModelConflict(models: string[]): [string, string] | null {
  const patterns = models.map(toModelPattern)
  for (let i = 0; i < patterns.length; i++) {
    for (let j = i + 1; j < patterns.length; j++) {
      if (patternsConflict(patterns[i], patterns[j])) {
        return [patterns[i].pattern, patterns[j].pattern]
      }
    }
  }
  return null
}

// validateIntervals validates amounts, bounds, and mode-specific relationships between price
// rows. Empty lists pass immediately. Video labels are normalized before duplicate detection
// and checked for character limits and control characters, while custom labels retain their
// lowercase spelling.
//
// A sorted copy supplies stable token-range comparisons and leaves the editor's order
// untouched. Every mode validates individual bounds and amounts, but only token mode rejects
// overlap and requires an unbounded interval to be last. The first failed rule returns a
// localized message.
export function validateIntervals(
  intervals: IntervalFormEntry[],
  mode: BillingMode,
  t: TranslateFn,
): string | null {
  if (!intervals || intervals.length === 0) return null

  if (mode === 'video') {
    const seen = new Set<string>()
    for (const interval of intervals) {
      const label = normalizeVideoResolution(interval.tier_label)
      if (!label) continue
      if ([...label].length > 50 || /\p{Cc}/u.test(label)) {
        return t('admin.pricing.intervalValidation.invalidVideoResolution')
      }
      if (seen.has(label))
        return t('admin.pricing.intervalValidation.duplicateVideoResolution', { resolution: label })
      seen.add(label)
    }
  }

  // Compare a sorted copy when checking token bounds so validation leaves the administrator's
  // editable row ordering available for subsequent changes.
  const sorted = [...intervals].sort((a, b) => a.min_tokens - b.min_tokens)

  for (let i = 0; i < sorted.length; i++) {
    const err = validateSingleInterval(sorted[i], i, t)
    if (err) return err
  }

  // Media rows select prices by tier label, so token-range overlap is irrelevant after each
  // row's bounds and amounts have passed validation.
  if (mode !== 'token') return null
  return checkIntervalOverlap(sorted, t)
}

// normalizeVideoResolution trims and lowercases video tier labels and canonicalizes known
// aliases. Empty labels remain empty, custom dimensions retain their normalized text, and
// duplicate checking compares these resulting label values.
function normalizeVideoResolution(value: string): string {
  const label = value.trim().toLowerCase()
  if (['480', '480p', 'sd'].includes(label)) return '480p'
  if (['720', '720p', 'hd'].includes(label)) return '720p'
  if (['1080', '1080p', 'full_hd', 'full-hd', 'fhd'].includes(label)) return '1080p'
  return label
}

// intervalValidationMessage translates an interval validation key with the supplied row
// details.
function intervalValidationMessage(
  t: TranslateFn,
  key: string,
  params: Record<string, unknown>,
): string {
  return t(`admin.pricing.intervalValidation.${key}`, params)
}

// intervalPriceLabel translates the field name used in interval validation errors.
function intervalPriceLabel(t: TranslateFn, key: string): string {
  return t(`admin.pricing.intervalValidation.price.${key}`)
}

// validateSingleInterval checks one row's lower bound, optional upper bound, and prices. Upper
// bounds must be positive and greater than the lower bound; errors display the supplied
// zero-based index as a one-based row number.
function validateSingleInterval(iv: IntervalFormEntry, idx: number, t: TranslateFn): string | null {
  const index = idx + 1
  if (iv.min_tokens < 0) {
    return intervalValidationMessage(t, 'negativeMin', { index, value: iv.min_tokens })
  }
  if (iv.max_tokens != null) {
    if (iv.max_tokens <= 0) {
      return intervalValidationMessage(t, 'maxPositive', { index, value: iv.max_tokens })
    }
    if (iv.max_tokens <= iv.min_tokens) {
      return intervalValidationMessage(t, 'maxGreaterThanMin', {
        index,
        max: iv.max_tokens,
        min: iv.min_tokens,
      })
    }
  }
  return validateIntervalPrices(iv, idx, t)
}

// validateIntervalPrices checks configured row amounts and optional positive multipliers.
// Missing amounts are accepted, zero amounts remain free prices, and the first invalid amount
// or multiplier returns a localized row-specific message.
function validateIntervalPrices(iv: IntervalFormEntry, idx: number, t: TranslateFn): string | null {
  const index = idx + 1
  const prices: [string, number | string | null | undefined][] = [
    ['inputPrice', iv.input_price],
    ['outputPrice', iv.output_price],
    ['cacheWritePrice', iv.cache_write_price],
    ['cacheWrite1hPrice', iv.cache_write_1h_price],
    ['cacheWrite30mPrice', iv.cache_write_30m_price],
    ['cacheWrite12hPrice', iv.cache_write_12h_price],
    ['cacheReadPrice', iv.cache_read_price],
    ['perRequestPrice', iv.per_request_price],
  ]
  for (const [key, val] of prices) {
    if (val != null && val !== '' && (!Number.isFinite(Number(val)) || Number(val) < 0)) {
      const field = intervalPriceLabel(t, key)
      return intervalValidationMessage(t, 'negativePrice', { index, field })
    }
  }
  const multipliers: [string, number | string | null][] = [
    ['inputMultiplier', iv.input_multiplier],
    ['outputMultiplier', iv.output_multiplier],
    ['cacheWriteMultiplier', iv.cache_write_multiplier],
    ['cacheReadMultiplier', iv.cache_read_multiplier],
  ]
  for (const [key, value] of multipliers) {
    if (!isValidPositiveMultiplier(value)) {
      return intervalValidationMessage(t, 'multiplierPositive', {
        index,
        field: intervalPriceLabel(t, key),
      })
    }
  }
  return null
}

// checkIntervalOverlap checks a sorted token interval list for range conflicts. An unbounded
// interval must be last, and adjacent ranges may share an endpoint because the ranges use open
// lower and closed upper bounds. The caller supplies rows ordered by increasing lower bound,
// which lets adjacent comparisons detect the first conflict. Error parameters use one-based row
// numbers and a visible infinity marker when the preceding interval has no upper limit.
function checkIntervalOverlap(sorted: IntervalFormEntry[], t: TranslateFn): string | null {
  for (let i = 0; i < sorted.length; i++) {
    // An unbounded range consumes every larger context, so a later interval would make token
    // price selection ambiguous.
    if (sorted[i].max_tokens == null && i < sorted.length - 1) {
      return intervalValidationMessage(t, 'unboundedLast', { index: i + 1 })
    }
    if (i === 0) continue
    const prev = sorted[i - 1]
    // Token intervals use open lower and closed upper bounds; touching endpoints remain valid,
    // while a previous upper bound above this minimum overlaps.
    if (prev.max_tokens == null || prev.max_tokens > sorted[i].min_tokens) {
      const prevMax = prev.max_tokens == null ? '∞' : String(prev.max_tokens)
      return intervalValidationMessage(t, 'overlap', {
        previousIndex: i,
        currentIndex: i + 1,
        previousMax: prevMax,
        currentMin: sorted[i].min_tokens,
      })
    }
  }
  return null
}

// getPlatformTagClass returns the tag color classes for a recognized provider platform. Unknown
// platforms use the neutral light and dark classes so callers can render catalog additions
// consistently.
export function getPlatformTagClass(platform: string): string {
  switch (platform) {
    case 'anthropic':
      return 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'
    case 'openai':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
    case 'gemini':
      return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
    case 'antigravity':
      return 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400'
    case 'grok':
      return 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300'
    case 'kimi':
      return 'bg-pink-100 text-pink-700 dark:bg-pink-900/30 dark:text-pink-400'
    case 'zhipu':
      return 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-400'
    case 'deepseek':
      return 'bg-teal-100 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400'
    default:
      return 'bg-gray-200/80 text-gray-800 dark:bg-dark-700 dark:text-dark-100'
  }
}
