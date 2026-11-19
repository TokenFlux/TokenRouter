/**
 * proxyExpiry.ts converts between local YYYY-MM-DD date inputs, day-offset
 * presets, and API Unix-second timestamps so proxy creation, hydration, and
 * day-offset calculations share local midnight semantics without UTC date shifts.
 */

/**
 * EXPIRY_PRESETS lists standard proxy expiration day-count shortcuts.
 */
export const EXPIRY_PRESETS = [7, 30, 90, 180] as const

/**
 * toLocalDateStr formats a Date using its local calendar year, month, and day.
 */
export function toLocalDateStr(dt: Date): string {
  const y = dt.getFullYear()
  const m = String(dt.getMonth() + 1).padStart(2, '0')
  const d = String(dt.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

/**
 * parseLocalDateMidnight parses a YYYY-MM-DD string as local midnight, returning
 * null when the input does not match a valid calendar date.
 */
export function parseLocalDateMidnight(dateStr: string): Date | null {
  const trimmed = dateStr.trim()
  const match = trimmed.match(/^(\d{4})-(\d{2})-(\d{2})$/)
  if (!match) return null
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  const dt = new Date(year, month - 1, day, 0, 0, 0, 0)
  if (
    Number.isNaN(dt.getTime()) ||
    dt.getFullYear() !== year ||
    dt.getMonth() !== month - 1 ||
    dt.getDate() !== day
  ) {
    return null
  }
  return dt
}

/**
 * formatStoredTimestampToLocalDate converts a persisted ISO timestamp or plain
 * YYYY-MM-DD string into the local calendar YYYY-MM-DD date shown in date inputs.
 */
export function formatStoredTimestampToLocalDate(value: string | null | undefined): string {
  if (!value) return ''
  const trimmed = value.trim()
  if (!trimmed) return ''
  if (/^\d{4}-\d{2}-\d{2}$/.test(trimmed)) return trimmed
  const dt = new Date(trimmed)
  if (Number.isNaN(dt.getTime())) return trimmed.slice(0, 10)
  return toLocalDateStr(dt)
}

/**
 * parseLocalExpiryDateToUnixSeconds converts a YYYY-MM-DD date-picker value into
 * local-midnight Unix seconds for proxy create and update payloads, returning
 * null when the input is blank or invalid.
 */
export function parseLocalExpiryDateToUnixSeconds(dateStr: string): number | null {
  const dt = parseLocalDateMidnight(dateStr)
  return dt ? Math.floor(dt.getTime() / 1000) : null
}

/**
 * baseDateOrToday resolves local midnight for a supplied YYYY-MM-DD or ISO
 * timestamp string, falling back to today's local midnight when blank.
 */
export function baseDateOrToday(baseDateStr: string, now: Date = new Date()): Date {
  const localDate = formatStoredTimestampToLocalDate(baseDateStr)
  const parsed = localDate ? parseLocalDateMidnight(localDate) : null
  if (parsed) return parsed
  const today = new Date(now)
  today.setHours(0, 0, 0, 0)
  return today
}

/**
 * addDaysToBase adds a positive integer day count to baseDateOrToday and
 * formats the resulting local calendar date as YYYY-MM-DD.
 */
export function addDaysToBase(
  baseDateStr: string,
  n: number | null,
  now: Date = new Date(),
): string {
  const days = Number(n)
  if (!days || days <= 0) return ''
  const dt = baseDateOrToday(baseDateStr, now)
  dt.setDate(dt.getDate() + days)
  return toLocalDateStr(dt)
}

/**
 * daysFromBase computes the rounded whole-day offset between baseDateOrToday and
 * targetDateStr across daylight-saving transitions, returning null when blank.
 */
export function daysFromBase(
  baseDateStr: string,
  targetDateStr: string,
  now: Date = new Date(),
): number | null {
  const target = parseLocalDateMidnight(targetDateStr)
  if (!target) return null
  // Round elapsed milliseconds to whole days across daylight-saving hour shifts.
  return Math.round((target.getTime() - baseDateOrToday(baseDateStr, now).getTime()) / 86_400_000)
}
