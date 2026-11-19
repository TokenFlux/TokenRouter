import { describe, expect, it } from 'vitest'
import { countryCodeToFlagEmoji } from '../countryFlag'

/**
 * countryFlag.spec.ts verifies offline ISO 3166-1 alpha-2 to Unicode flag
 * conversion and empty fallbacks for invalid country codes.
 */

describe('countryCodeToFlagEmoji', () => {
  // Valid upper- or lowercase two-letter codes map to regional indicator symbols
  // while empty, numeric, or three-letter inputs return an empty string.
  it('converts valid ISO alpha-2 codes and rejects invalid inputs', () => {
    expect(countryCodeToFlagEmoji('US')).toBe('🇺🇸')
    expect(countryCodeToFlagEmoji('jp')).toBe('🇯🇵')
    expect(countryCodeToFlagEmoji(' SG ')).toBe('🇸🇬')
    expect(countryCodeToFlagEmoji('')).toBe('')
    expect(countryCodeToFlagEmoji('USA')).toBe('')
    expect(countryCodeToFlagEmoji('1A')).toBe('')
    expect(countryCodeToFlagEmoji(null)).toBe('')
  })
})
