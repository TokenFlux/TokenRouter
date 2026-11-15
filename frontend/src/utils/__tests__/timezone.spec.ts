import { describe, expect, it } from 'vitest'

import {
  dateRangeToHalfOpenLocalRange,
  formatInstantInTimezoneDisplay,
  formatInstantRFC3339InTimezone,
  formatInstantUTCPreview,
  formatUtcOffset,
  formatZonedRFC3339,
  getTimezoneOffsetMinutes,
  getTimezoneSelectOptions,
  instantToZonedLocalDateTime,
  isValidIanaTimezone,
  parseFlexibleTimestamp,
  parseFlexibleTimestampInput,
  parseLocalDateTimeParts,
  zonedDateTimeToInstant,
} from '../timezone'

// timezone.spec.ts verifies IANA timezone validation, half-open calendar range
// conversion, DST-safe RFC3339 formatting, and flexible timestamp paste parsing.
// Explicit summer, winter, and spring-forward instants protect non-browser timezone
// exports from host-machine clock interference.

describe('timezone utilities', () => {
  // Valid IANA zone identifiers must pass validation while misspelled or arbitrary text
  // returns false. Selectable options must always pin UTC near the top with an explicit
  // offset label so operators can switch to UTC regardless of browser locale.
  it('validates IANA identifiers and pins UTC in searchable options', () => {
    expect(isValidIanaTimezone('UTC')).toBe(true)
    expect(isValidIanaTimezone('Asia/Shanghai')).toBe(true)
    expect(isValidIanaTimezone('America/New_York')).toBe(true)
    expect(isValidIanaTimezone('Mars/Olympus_Mons')).toBe(false)
    expect(isValidIanaTimezone('   ')).toBe(false)

    const options = getTimezoneSelectOptions(new Date('2026-01-15T00:00:00Z'), 'America/New_York')
    expect(options[0]).toEqual({
      value: 'America/New_York',
      label: 'America/New_York (UTC-05:00)',
    })
    expect(options.some((item) => item.value === 'UTC' && item.label === 'UTC (UTC+00:00)')).toBe(
      true,
    )
  })

  // Half-hour zones and seasonal daylight-saving zones require exact minute offsets
  // rather than whole-hour approximations. Winter EST, summer EDT, and India Standard
  // Time must produce their exact signed minute offsets and formatted UTC labels.
  it('computes exact offsets for standard, daylight-saving, and half-hour timezones', () => {
    const winterInstant = new Date('2026-01-15T12:00:00Z')
    const summerInstant = new Date('2026-07-15T12:00:00Z')

    expect(getTimezoneOffsetMinutes('Asia/Shanghai', winterInstant)).toBe(480)
    expect(getTimezoneOffsetMinutes('America/New_York', winterInstant)).toBe(-300)
    expect(getTimezoneOffsetMinutes('America/New_York', summerInstant)).toBe(-240)
    expect(getTimezoneOffsetMinutes('Asia/Kolkata', winterInstant)).toBe(330)
    expect(formatUtcOffset(330)).toBe('+05:30')
    expect(formatUtcOffset(-240)).toBe('-04:00')
  })

  // Inheriting an inclusive date filter [2026-11-01, 2026-11-15] must produce the
  // half-open local interval [2026-11-01T00:00:00, 2026-11-16T00:00:00). Impossible
  // calendar days such as February 30 must be rejected rather than rolling into March.
  it('converts inclusive date ranges to half-open bounds and rejects invalid calendar dates', () => {
    expect(dateRangeToHalfOpenLocalRange('2026-11-01', '2026-11-15')).toEqual({
      startLocal: '2026-11-01T00:00:00',
      endLocal: '2026-11-16T00:00:00',
    })
    expect(dateRangeToHalfOpenLocalRange('2026-02-28', '2026-02-28')).toEqual({
      startLocal: '2026-02-28T00:00:00',
      endLocal: '2026-03-01T00:00:00',
    })
    expect(parseLocalDateTimeParts('2026-02-30T10:00:00')).toBeNull()
    expect(dateRangeToHalfOpenLocalRange('2026-02-30', '2026-03-02')).toBeNull()
  })

  // Wall-clock timestamps immediately after a daylight-saving spring-forward transition
  // differ in offset from the naive UTC interpretation of the same numbers. Two-pass
  // convergence must resolve 2026-03-08 03:30:00 in New York to EDT (-04:00) at 07:30Z.
  it('resolves wall-clock instants accurately across daylight-saving spring-forward boundaries', () => {
    const instant = zonedDateTimeToInstant('2026-03-08T03:30:00', 'America/New_York')
    expect(instant?.toISOString()).toBe('2026-03-08T07:30:00.000Z')
    expect(formatZonedRFC3339('2026-03-08T03:30:00', 'America/New_York')).toBe(
      '2026-03-08T03:30:00-04:00',
    )
    expect(formatZonedRFC3339('2026-11-15T06:30:05', 'UTC')).toBe('2026-11-15T06:30:05Z')
    expect(formatInstantUTCPreview(instant!)).toBe('2026-03-08 07:30:00Z')
    expect(formatInstantInTimezoneDisplay('2026-03-08T07:30:00Z', 'Asia/Shanghai')).toBe(
      '2026-03-08 15:30:00',
    )
    expect(instantToZonedLocalDateTime(instant!, 'America/New_York')).toBe('2026-03-08T03:30:00')
  })

  // Operators paste Unix seconds, Unix milliseconds, explicit-offset ISO strings, or
  // space-separated timestamps copied from logs. Each format must normalize to the
  // target timezone's YYYY-MM-DDTHH:mm:ss representation while invalid text returns null.
  it('parses Unix seconds, milliseconds, ISO offsets, and local timestamps into target timezone', () => {
    // 2026-11-15T00:30:00Z === 1794702600 Unix seconds === 2026-11-15 08:30:00 in Asia/Shanghai.
    expect(parseFlexibleTimestampInput('1794702600', 'Asia/Shanghai')).toBe('2026-11-15T08:30:00')
    expect(parseFlexibleTimestampInput('1794702600987', 'UTC')).toBe('2026-11-15T00:30:00')
    expect(parseFlexibleTimestampInput('2026-11-15T08:30:15+08:00', 'America/New_York')).toBe(
      '2026-11-14T19:30:15',
    )
    expect(parseFlexibleTimestampInput('2026-11-15 14:05:09', 'Asia/Shanghai')).toBe(
      '2026-11-15T14:05:09',
    )
    expect(parseFlexibleTimestampInput('2026/11/15 14:05', 'Asia/Shanghai')).toBe(
      '2026-11-15T14:05:00',
    )
    expect(parseFlexibleTimestampInput('not-a-timestamp', 'Asia/Shanghai')).toBeNull()
  })

  // During America/New_York fall-back on 2026-11-01, 01:30:00 occurs twice: once in EDT
  // (-04:00 at 05:30Z) and again in EST (-05:00 at 06:30Z). Pasting an explicit-offset
  // timestamp must retain its exact UTC instant and serialize back with its original offset.
  it('preserves unambiguous instants across daylight-saving fall-back repeated hours', () => {
    const firstFold = parseFlexibleTimestamp('2026-11-01T01:30:00-04:00', 'America/New_York')
    const secondFold = parseFlexibleTimestamp('2026-11-01T01:30:00-05:00', 'America/New_York')

    expect(firstFold?.localValue).toBe('2026-11-01T01:30:00')
    expect(secondFold?.localValue).toBe('2026-11-01T01:30:00')
    expect(firstFold?.instant.toISOString()).toBe('2026-11-01T05:30:00.000Z')
    expect(secondFold?.instant.toISOString()).toBe('2026-11-01T06:30:00.000Z')

    expect(formatInstantRFC3339InTimezone(firstFold!.instant, 'America/New_York')).toBe(
      '2026-11-01T01:30:00-04:00',
    )
    expect(formatInstantRFC3339InTimezone(secondFold!.instant, 'America/New_York')).toBe(
      '2026-11-01T01:30:00-05:00',
    )
  })
})
