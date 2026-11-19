import { describe, expect, it } from 'vitest'
import {
  addDaysToBase,
  daysFromBase,
  formatStoredTimestampToLocalDate,
  parseLocalExpiryDateToUnixSeconds,
} from '../proxyExpiry'

/**
 * proxyExpiry.spec.ts verifies that proxy expiration dates round-trip between
 * local YYYY-MM-DD inputs, day-count presets, and Unix-second API timestamps
 * without UTC-versus-local midnight shifts.
 */

describe('proxyExpiry', () => {
  // Local YYYY-MM-DD dates convert to local midnight Unix seconds and hydrate
  // back to the exact same local calendar date regardless of browser UTC offset.
  it('round-trips local calendar dates through Unix seconds and ISO timestamps', () => {
    const unixSeconds = parseLocalExpiryDateToUnixSeconds('2026-06-01')
    expect(unixSeconds).not.toBeNull()
    const expectedMidnight = new Date(2026, 5, 1, 0, 0, 0, 0)
    expect(unixSeconds).toBe(Math.floor(expectedMidnight.getTime() / 1000))

    const iso = new Date(unixSeconds! * 1000).toISOString()
    expect(formatStoredTimestampToLocalDate(iso)).toBe('2026-06-01')
    expect(parseLocalExpiryDateToUnixSeconds('')).toBeNull()
    expect(parseLocalExpiryDateToUnixSeconds('2026-02-31')).toBeNull()
  })

  // Day-offset presets and daysFromBase agree on whole-day counts from a fixed base.
  it('computes consistent day offsets from a base date', () => {
    const base = new Date(2026, 0, 10, 15, 30, 0, 0)
    const target = addDaysToBase('', 30, base)
    expect(target).toBe('2026-02-09')
    expect(daysFromBase('', target, base)).toBe(30)
  })
})
