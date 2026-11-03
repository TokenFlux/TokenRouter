import { describe, expect, it } from 'vitest'
import {
  formatRequestSize,
  formatRequestTimingForExport,
  formatTimingMs,
  timingBreakdown,
  timingValue,
} from './requestTiming'

/**
 * requestTiming.test.ts tests timing observation grouping, units, and export
 * text. Fixtures distinguish measured zero from absence, complete admission
 * totals from partial rows, independent stream applicability, positive retry
 * waits, and raw field-name retention during export.
 */

describe('request timing breakdown', () => {
  /**
   * Missing observations and measured zero are read from timing fields. The helper
   * returns null for absence and retains zero for both event offsets and admission
   * durations.
   */
  it('uses current event offsets without historical SSE fallback', () => {
    expect(timingValue({}, 'upstream_first_event_ms')).toBeNull()
    expect(timingValue({ upstream_first_event_ms: 0 }, 'upstream_first_event_ms')).toBe(0)
    expect(timingValue({ user_slot_wait_ms: 0 }, 'user_slot_wait_ms')).toBe(0)
  })

  /**
   * Timing records combine partial durations, retry waits, stream flags, and admission
   * flags. Row selection retains observed groups, hides inapplicable events, and
   * distinguishes missing admission state from false.
   */
  it('selects duration and event rows from the recorded fields', () => {
    expect(timingBreakdown({ provider_slot_acquired_ms: 118235 }).durations).toEqual([])
    expect(timingBreakdown({ user_slot_wait_ms: 0 }).durations.map((row) => row.key)).toEqual([
      'request_body_read_ms',
      'preprocess_ms',
      'user_slot_wait_ms',
      'provider_slot_wait_ms',
    ])
    expect(
      timingBreakdown({ user_slot_wait_ms: 0, retry_provider_slot_wait_ms: 0 }).retryWait,
    ).toBeNull()
    expect(
      timingBreakdown({ user_slot_wait_ms: 0, retry_provider_slot_wait_ms: 9 }).retryWait?.value,
    ).toBe(9)
    expect(
      timingBreakdown({ user_slot_wait_ms: 0, retry_provider_slot_wait_ms: 9 }).durations,
    ).toHaveLength(4)
    expect(timingBreakdown({ upstream_stream: false }, false).events).toHaveLength(7)
    expect(timingBreakdown({ timing_stream: 1 }, false).events).toHaveLength(13)
    expect(timingBreakdown({}).events).toHaveLength(13)
    expect(timingBreakdown({}).admitted).toBeNull()
    expect(timingBreakdown({ timing_admitted: 0 }).admitted).toBe(false)
  })

  /**
   * A complete admission split sums to the supplied provider-slot offset in the
   * fixture. Partial and absent duration groups leave the total null rather than
   * inventing missing values.
   */
  it('sums complete admission durations to the slot offset', () => {
    const timing = {
      request_body_read_ms: 12,
      preprocess_ms: 4,
      user_slot_wait_ms: 0,
      provider_slot_wait_ms: 104,
      provider_slot_acquired_ms: 120,
    }
    expect(timingBreakdown(timing).admissionTotal).toBe(timing.provider_slot_acquired_ms)
    expect(timingBreakdown({ user_slot_wait_ms: 0 }).admissionTotal).toBeNull()
    expect(timingBreakdown({}).admissionTotal).toBeNull()
  })

  /**
   * Missing, zero, second-scale, and minute-scale durations enter the formatter. Output
   * retains the missing distinction and converts recorded milliseconds into the
   * expected display units and precision.
   */
  it('formats milliseconds, seconds and minutes', () => {
    expect(formatTimingMs(null)).toBe('-')
    expect(formatTimingMs(0)).toBe('0ms')
    expect(formatTimingMs(1500)).toBe('1.50s')
    expect(formatTimingMs(118235)).toBe('1m 58s')
  })

  /**
   * Missing, byte, kilobyte, and megabyte payload sizes enter the request-size
   * formatter. Output keeps nullish values distinct from zero and applies binary unit
   * thresholds with one or two decimal places.
   */
  it('formats request payload sizes across binary thresholds', () => {
    expect(formatRequestSize(null)).toBe('-')
    expect(formatRequestSize(0)).toBe('0B')
    expect(formatRequestSize(512)).toBe('512B')
    expect(formatRequestSize(2048)).toBe('2.0KB')
    expect(formatRequestSize(8360000)).toBe('7.97MB')
  })

  /**
   * A timing object contains a measured zero duration and a provider admission offset.
   * Export retains both raw names and units, while a missing object yields empty text.
   */
  it('exports available raw metrics without inventing durations', () => {
    expect(
      formatRequestTimingForExport({ user_slot_wait_ms: 0, provider_slot_acquired_ms: 118235 }),
    ).toBe('user_slot_wait_ms=0ms; provider_slot_acquired_ms=118235ms')
    expect(formatRequestTimingForExport()).toBe('')
  })
})
