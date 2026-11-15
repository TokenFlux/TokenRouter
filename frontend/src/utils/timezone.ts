// timezone.ts converts wall-clock timestamps across IANA timezones, formats searchable
// timezone options, and parses flexible timestamp paste formats for usage exports.
// Browser Date constructors always interpret offset-free strings in the host system
// zone, which shifts calendar boundaries whenever an operator exports usage in UTC or
// another region.
//
// Formatting parts through Intl.DateTimeFormat and converging timezone offsets in two
// passes keeps wall-clock conversions accurate across daylight-saving transitions and
// half-hour zones without external date libraries. All emitted local picker strings use
// second-precision YYYY-MM-DDTHH:mm:ss notation paired with explicit IANA identifiers.

// TimezoneSelectOption pairs a canonical IANA timezone identifier with its UTC offset label.
export interface TimezoneSelectOption {
  value: string // Canonical IANA timezone identifier.
  label: string // Searchable display label including the zone's current UTC offset.
  // Additional SelectOption compatibility metadata.
  [key: string]: unknown
}

// IntlWithSupportedValues models ES2022 Intl.supportedValuesOf when compiling against older TS libs.
interface IntlWithSupportedValues {
  supportedValuesOf?: (key: 'timeZone') => string[] // Enumerates canonical runtime IANA timezone names.
}

// LocalDateTimeParts holds validated calendar and second-level clock fields prior to timezone conversion.
export interface LocalDateTimeParts {
  year: number // Four-digit Gregorian calendar year.
  month: number // One-based calendar month from 1 through 12.
  day: number // One-based day of the month validated against month length.
  hour: number // 24-hour clock hour from 0 through 23.
  minute: number // Clock minute from 0 through 59.
  second: number // Clock second from 0 through 59.
}

// ParsedFlexibleTimestamp retains both the target-timezone wall-clock string and the
// resolved UTC Date instant so unambiguous inputs (Unix epochs and explicit-offset ISO
// timestamps) never lose their exact instant during repeated DST fall-back hours.
export interface ParsedFlexibleTimestamp {
  localValue: string // Wall-clock timestamp formatted as YYYY-MM-DDTHH:mm:ss in targetTimezone.
  instant: Date // Whole-second UTC Date instant represented by the input.
}

// FALLBACK_IANA_TIMEZONES and zonedPartFormatters supply deterministic zone choices in
// minimal runtimes and cache Intl formatters by validated IANA identifier.
const FALLBACK_IANA_TIMEZONES: readonly string[] = [
  'UTC',
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Asia/Singapore',
  'Asia/Hong_Kong',
  'Asia/Seoul',
  'Asia/Kolkata',
  'Asia/Dubai',
  'Europe/London',
  'Europe/Berlin',
  'Europe/Paris',
  'Europe/Moscow',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Sao_Paulo',
  'Australia/Sydney',
  'Pacific/Auckland',
]
const zonedPartFormatters = new Map<string, Intl.DateTimeFormat>()

// pad2 formats a non-negative integer to at least two digits with leading zeros.
function pad2(value: number): string {
  return String(value).padStart(2, '0')
}

// pad4 formats a Gregorian calendar year to at least four digits with leading zeros.
function pad4(value: number): string {
  return String(value).padStart(4, '0')
}

// getZonedFormatter returns a cached 24-hour Intl.DateTimeFormat for a validated IANA timezone.
function getZonedFormatter(timeZone: string): Intl.DateTimeFormat {
  const existing = zonedPartFormatters.get(timeZone)
  if (existing) {
    return existing
  }
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  })
  zonedPartFormatters.set(timeZone, formatter)
  return formatter
}

// extractZonedParts projects a UTC Date instant into calendar and 24-hour clock fields
// within timeZone, normalizing midnight hour 24 outputs from older ICU builds to zero.
function extractZonedParts(instant: Date, timeZone: string): LocalDateTimeParts {
  const parts = getZonedFormatter(timeZone).formatToParts(instant)
  let year = 1970
  let month = 1
  let day = 1
  let hour = 0
  let minute = 0
  let second = 0

  for (const part of parts) {
    switch (part.type) {
      case 'year':
        year = Number(part.value)
        break
      case 'month':
        month = Number(part.value)
        break
      case 'day':
        day = Number(part.value)
        break
      case 'hour': {
        // Older ICU tables occasionally emit 24 at 00:00 even with hourCycle h23;
        // folding 24 to 0 prevents Date.UTC from advancing into the next calendar day.
        const parsedHour = Number(part.value)
        hour = parsedHour === 24 ? 0 : parsedHour
        break
      }
      case 'minute':
        minute = Number(part.value)
        break
      case 'second':
        second = Number(part.value)
        break
    }
  }

  return { year, month, day, hour, minute, second }
}

// isValidIanaTimezone reports whether tz is accepted by the runtime's Intl IANA database.
export function isValidIanaTimezone(tz: string): boolean {
  const trimmed = tz.trim()
  if (!trimmed) {
    return false
  }
  try {
    getZonedFormatter(trimmed)
    return true
  } catch {
    // Intl throws RangeError for unrecognized zone names; callers receive false so UI
    // state can fall back to the browser zone or reject pasted input cleanly.
    return false
  }
}

// getBrowserTimezone returns the host browser's resolved IANA timezone, or UTC when
// Intl reports an empty or unrecognized zone identifier.
export function getBrowserTimezone(): string {
  try {
    const resolved = Intl.DateTimeFormat().resolvedOptions().timeZone
    if (resolved && isValidIanaTimezone(resolved)) {
      return resolved
    }
  } catch {
    // Restricted or mocked Intl environments may throw during option resolution, in
    // which case UTC provides a valid IANA default accepted by the backend.
  }
  return 'UTC'
}

// getTimezoneOffsetMinutes computes timeZone's offset east of UTC in minutes at instant.
// Invalid timezone names fall back to UTC (0 minutes) so UI rendering never throws.
export function getTimezoneOffsetMinutes(timeZone: string, instant: Date = new Date()): number {
  const safeZone = isValidIanaTimezone(timeZone) ? timeZone.trim() : 'UTC'
  if (safeZone === 'UTC') {
    return 0
  }
  const parts = extractZonedParts(instant, safeZone)
  const zonedWallAsUtcMs = Date.UTC(
    parts.year,
    parts.month - 1,
    parts.day,
    parts.hour,
    parts.minute,
    parts.second,
  )
  // Truncate sub-second milliseconds so whole-second Intl parts compare against the
  // same second boundary on instant.
  const instantWholeSecMs = Math.floor(instant.getTime() / 1000) * 1000
  return Math.round((zonedWallAsUtcMs - instantWholeSecMs) / 60000)
}

// formatUtcOffset formats a signed minute offset as +HH:MM or -HH:MM.
export function formatUtcOffset(offsetMinutes: number): string {
  const sign = offsetMinutes >= 0 ? '+' : '-'
  const absMinutes = Math.abs(offsetMinutes)
  const hours = pad2(Math.floor(absMinutes / 60))
  const minutes = pad2(absMinutes % 60)
  return `${sign}${hours}:${minutes}`
}

// getTimezoneSelectOptions builds searchable Select options with preferredTimezone, the
// browser timezone, and UTC pinned ahead of alphabetically sorted IANA zones. Offsets
// reflect referenceInstant so daylight-saving shifts match the selected export window.
export function getTimezoneSelectOptions(
  referenceInstant: Date = new Date(),
  preferredTimezone?: string,
): TimezoneSelectOption[] {
  let runtimeZones: readonly string[] = FALLBACK_IANA_TIMEZONES
  const intlWithSupportedValues = Intl as unknown as IntlWithSupportedValues
  if (typeof intlWithSupportedValues.supportedValuesOf === 'function') {
    try {
      const supported = intlWithSupportedValues.supportedValuesOf('timeZone')
      if (supported.length > 0) {
        runtimeZones = supported
      }
    } catch {
      // Older or sandboxed JS engines may expose supportedValuesOf without supporting
      // the timeZone key; retain the curated static IANA list in that case.
      runtimeZones = FALLBACK_IANA_TIMEZONES
    }
  }

  const pinned: string[] = []
  const seen = new Set<string>()
  const pushUnique = (candidate?: string) => {
    const trimmed = candidate?.trim()
    if (!trimmed || seen.has(trimmed) || !isValidIanaTimezone(trimmed)) {
      return
    }
    seen.add(trimmed)
    pinned.push(trimmed)
  }

  pushUnique(preferredTimezone)
  pushUnique(getBrowserTimezone())
  pushUnique('UTC')

  const remaining: string[] = []
  for (const zone of runtimeZones) {
    const trimmed = zone.trim()
    if (!trimmed || seen.has(trimmed) || !isValidIanaTimezone(trimmed)) {
      continue
    }
    seen.add(trimmed)
    remaining.push(trimmed)
  }
  remaining.sort((left, right) => left.localeCompare(right))

  return [...pinned, ...remaining].map((zone) => {
    const offset = formatUtcOffset(getTimezoneOffsetMinutes(zone, referenceInstant))
    return {
      value: zone,
      label: `${zone} (UTC${offset})`,
    }
  })
}

// parseLocalDateTimeParts validates YYYY-MM-DD[T ]HH:mm[:ss] text without consulting
// the browser timezone. Impossible calendar dates such as February 30 return null.
export function parseLocalDateTimeParts(localValue: string): LocalDateTimeParts | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2}))?$/.exec(localValue.trim())
  if (!match) {
    return null
  }
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  const hour = Number(match[4])
  const minute = Number(match[5])
  const second = match[6] ? Number(match[6]) : 0

  if (month < 1 || month > 12 || day < 1 || day > 31) {
    return null
  }
  if (hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59) {
    return null
  }

  const calendarCheck = new Date(Date.UTC(year, month - 1, day))
  if (
    calendarCheck.getUTCFullYear() !== year ||
    calendarCheck.getUTCMonth() !== month - 1 ||
    calendarCheck.getUTCDate() !== day
  ) {
    return null
  }

  return { year, month, day, hour, minute, second }
}

// shiftCalendarDate shifts a validated YYYY-MM-DD date string by deltaDays in pure
// calendar arithmetic. Invalid date strings return an empty string.
export function shiftCalendarDate(dateStr: string, deltaDays: number): string {
  const parts = parseLocalDateTimeParts(`${dateStr.trim()}T00:00:00`)
  if (!parts) {
    return ''
  }
  const shifted = new Date(Date.UTC(parts.year, parts.month - 1, parts.day + deltaDays))
  return `${pad4(shifted.getUTCFullYear())}-${pad2(shifted.getUTCMonth() + 1)}-${pad2(shifted.getUTCDate())}`
}

// dateRangeToHalfOpenLocalRange converts inclusive page dates [startDate, endDate] into
// half-open local timestamps [startDateT00:00:00, (endDate + 1 day)T00:00:00). Reversed
// dates are ordered automatically, and malformed dates return null.
export function dateRangeToHalfOpenLocalRange(
  startDate: string,
  endDate: string,
): {
  startLocal: string // Inclusive local start bound at 00:00:00.
  endLocal: string // Exclusive local end bound at the next calendar day's 00:00:00.
} | null {
  const cleanStart = startDate.trim()
  const cleanEnd = endDate.trim()
  if (!parseLocalDateTimeParts(`${cleanStart}T00:00:00`)) {
    return null
  }
  if (!parseLocalDateTimeParts(`${cleanEnd}T00:00:00`)) {
    return null
  }
  const [orderedStart, orderedEnd] =
    cleanStart <= cleanEnd ? [cleanStart, cleanEnd] : [cleanEnd, cleanStart]
  const nextDay = shiftCalendarDate(orderedEnd, 1)
  if (!nextDay) {
    return null
  }
  return {
    startLocal: `${orderedStart}T00:00:00`,
    endLocal: `${nextDay}T00:00:00`,
  }
}

// zonedDateTimeToInstant resolves a YYYY-MM-DDTHH:mm[:ss] wall-clock string in timeZone
// to its UTC Date instant. Invalid wall-clock strings or timezone identifiers return null.
//
// Offset resolution runs in two passes so timestamps near daylight-saving transitions
// re-evaluate the zone offset at the first candidate instant rather than using the UTC
// wall-clock timestamp's pre-transition offset.
export function zonedDateTimeToInstant(localValue: string, timeZone: string): Date | null {
  const parts = parseLocalDateTimeParts(localValue)
  const cleanZone = timeZone.trim()
  if (!parts || !isValidIanaTimezone(cleanZone)) {
    return null
  }
  const wallAsUtcMs = Date.UTC(
    parts.year,
    parts.month - 1,
    parts.day,
    parts.hour,
    parts.minute,
    parts.second,
  )
  if (cleanZone === 'UTC') {
    return new Date(wallAsUtcMs)
  }
  const firstOffsetMin = getTimezoneOffsetMinutes(cleanZone, new Date(wallAsUtcMs))
  const firstCandidateMs = wallAsUtcMs - firstOffsetMin * 60000
  const secondOffsetMin = getTimezoneOffsetMinutes(cleanZone, new Date(firstCandidateMs))
  return new Date(wallAsUtcMs - secondOffsetMin * 60000)
}

// instantToZonedLocalDateTime formats a Date instant as YYYY-MM-DDTHH:mm:ss in timeZone.
// Invalid timezone identifiers fall back to UTC.
export function instantToZonedLocalDateTime(instant: Date, timeZone: string): string {
  if (Number.isNaN(instant.getTime())) {
    return ''
  }
  const safeZone = isValidIanaTimezone(timeZone) ? timeZone.trim() : 'UTC'
  const parts = extractZonedParts(instant, safeZone)
  return `${pad4(parts.year)}-${pad2(parts.month)}-${pad2(parts.day)}T${pad2(parts.hour)}:${pad2(parts.minute)}:${pad2(parts.second)}`
}

// formatInstantRFC3339InTimezone formats an exact Date instant as an explicit-offset
// RFC3339 timestamp in timeZone without re-parsing wall-clock text, preserving the
// exact -04:00 or -05:00 occurrence across repeated daylight-saving fall-back hours.
export function formatInstantRFC3339InTimezone(instant: Date, timeZone: string): string | null {
  const cleanZone = timeZone.trim()
  if (Number.isNaN(instant.getTime()) || !isValidIanaTimezone(cleanZone)) {
    return null
  }
  const resolvedLocal = instantToZonedLocalDateTime(instant, cleanZone)
  if (!resolvedLocal) {
    return null
  }
  if (cleanZone === 'UTC') {
    return `${resolvedLocal}Z`
  }
  const offset = formatUtcOffset(getTimezoneOffsetMinutes(cleanZone, instant))
  return `${resolvedLocal}${offset}`
}

// formatZonedRFC3339 converts a local picker timestamp and IANA timezone into an
// explicit-offset RFC3339 string suitable for usage export task creation payloads.
// Invalid timestamps or timezone names return null.
export function formatZonedRFC3339(localValue: string, timeZone: string): string | null {
  const instant = zonedDateTimeToInstant(localValue, timeZone)
  if (!instant) {
    return null
  }
  return formatInstantRFC3339InTimezone(instant, timeZone)
}

// formatInstantInTimezoneDisplay formats an ISO timestamp or Date as YYYY-MM-DD HH:mm:ss
// in timeZone for task cards and boundary previews. Unparsable inputs return an empty string.
export function formatInstantInTimezoneDisplay(
  isoOrDate: string | Date | null | undefined,
  timeZone: string,
): string {
  if (!isoOrDate) {
    return ''
  }
  const instant = isoOrDate instanceof Date ? isoOrDate : new Date(isoOrDate)
  if (Number.isNaN(instant.getTime())) {
    return ''
  }
  return instantToZonedLocalDateTime(instant, timeZone).replace('T', ' ')
}

// formatInstantUTCPreview formats a Date instant as YYYY-MM-DD HH:mm:ssZ in UTC.
export function formatInstantUTCPreview(instant: Date): string {
  const formatted = formatInstantInTimezoneDisplay(instant, 'UTC')
  return formatted ? `${formatted}Z` : ''
}

// parseFlexibleTimestamp parses pasted Unix seconds, Unix milliseconds, explicit-offset
// ISO timestamps, or wall-clock date-time strings into both targetTimezone's wall-clock
// string and the exact whole-second UTC Date instant.
//
// Returning instant alongside localValue prevents DST fall-back ambiguity from shifting
// pasted explicit-offset or epoch timestamps onto the earlier repeated local hour when
// callers later serialize RFC3339 export bounds.
export function parseFlexibleTimestamp(
  rawInput: string,
  targetTimezone: string,
): ParsedFlexibleTimestamp | null {
  const trimmed = rawInput.trim()
  if (!trimmed || !isValidIanaTimezone(targetTimezone)) {
    return null
  }

  // 9-to-10-digit numbers (with optional fractional seconds) represent Unix epoch seconds.
  if (/^\d{9,10}(?:\.\d+)?$/.test(trimmed)) {
    const epochSeconds = Math.floor(Number(trimmed))
    const instant = new Date(epochSeconds * 1000)
    return {
      localValue: instantToZonedLocalDateTime(instant, targetTimezone),
      instant,
    }
  }

  // 12-to-14-digit integers represent millisecond Unix timestamps from JS or log exports.
  if (/^\d{12,14}$/.test(trimmed)) {
    const epochSeconds = Math.floor(Number(trimmed) / 1000)
    const instant = new Date(epochSeconds * 1000)
    return {
      localValue: instantToZonedLocalDateTime(instant, targetTimezone),
      instant,
    }
  }

  // Timestamps ending with Z or an explicit +/-HH:MM offset represent absolute instants
  // whose exact UTC millisecond point must survive projection into targetTimezone.
  if (/(?:[Zz]|[+-]\d{2}:?\d{2})$/.test(trimmed)) {
    const normalizedIso = trimmed
      .replace(/^(\d{4})\/(\d{2})\/(\d{2})/, '$1-$2-$3')
      .replace(/^(\d{4}-\d{2}-\d{2})\s+/, '$1T')
      .replace(/([+-]\d{2})(\d{2})$/, '$1:$2')
    const parsedMs = Date.parse(normalizedIso)
    if (Number.isNaN(parsedMs)) {
      return null
    }
    const instant = new Date(Math.floor(parsedMs / 1000) * 1000)
    return {
      localValue: instantToZonedLocalDateTime(instant, targetTimezone),
      instant,
    }
  }

  // Offset-free date-time text is interpreted directly as wall-clock time in targetTimezone.
  const normalizedLocal = trimmed.replace(/^(\d{4})\/(\d{2})\/(\d{2})/, '$1-$2-$3')
  const parts = parseLocalDateTimeParts(normalizedLocal)
  if (!parts) {
    return null
  }
  const localValue = `${pad4(parts.year)}-${pad2(parts.month)}-${pad2(parts.day)}T${pad2(parts.hour)}:${pad2(parts.minute)}:${pad2(parts.second)}`
  const instant = zonedDateTimeToInstant(localValue, targetTimezone)
  if (!instant) {
    return null
  }
  return { localValue, instant }
}

// parseFlexibleTimestampInput parses pasted Unix seconds, Unix milliseconds, offset ISO
// timestamps, or wall-clock date-time strings into YYYY-MM-DDTHH:mm:ss in targetTimezone.
// Unrecognized or out-of-range strings return null so callers can surface validation feedback.
export function parseFlexibleTimestampInput(
  rawInput: string,
  targetTimezone: string,
): string | null {
  return parseFlexibleTimestamp(rawInput, targetTimezone)?.localValue ?? null
}
