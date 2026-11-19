/**
 * countryFlag.ts converts two-letter ISO 3166-1 alpha-2 country codes into
 * offline Unicode regional-indicator flag symbols so proxy location cells render
 * without requesting external CDN assets.
 */

/**
 * REGIONAL_INDICATOR_OFFSET maps uppercase ASCII letters ('A'..='Z') onto
 * Unicode Regional Indicator Symbols (U+1F1E6..U+1F1FF).
 */
const REGIONAL_INDICATOR_OFFSET = 0x1f1e6 - 65

/**
 * countryCodeToFlagEmoji converts a two-letter ASCII country code (such as "US"
 * or "jp") into its two-character regional-indicator flag emoji, returning an
 * empty string for missing or malformed codes.
 */
export function countryCodeToFlagEmoji(code: string | null | undefined): string {
  if (typeof code !== 'string') return ''
  const normalized = code.trim().toUpperCase()
  if (!/^[A-Z]{2}$/.test(normalized)) return ''
  return String.fromCodePoint(
    normalized.charCodeAt(0) + REGIONAL_INDICATOR_OFFSET,
    normalized.charCodeAt(1) + REGIONAL_INDICATOR_OFFSET,
  )
}
