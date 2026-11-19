// poolMode.ts normalizes provider pool retry counts and HTTP status-code drafts.
// The form displays the gateway defaults and accepts an override. Parsing accepts
// comma or whitespace separators, filters invalid codes, and sorts unique results.
// Count normalization supplies the default for non-finite input and clamps finite
// integers to the supported range.

// DEFAULT_POOL_MODE_RETRY_COUNT supplies three retries when a saved count is missing or
// cannot represent a finite number.
export const DEFAULT_POOL_MODE_RETRY_COUNT = 3
// MAX_POOL_MODE_RETRY_COUNT limits normalized retry counts to ten and supplies the upper
// bound shown by the shared input.
export const MAX_POOL_MODE_RETRY_COUNT = 10
// DEFAULT_POOL_MODE_RETRY_STATUS_CODES mirrors the gateway defaults for the
// provider form's status-code placeholder and help text.
export const DEFAULT_POOL_MODE_RETRY_STATUS_CODES = [401, 403, 429, 502, 503]

// collectStatusCodes converts iterable values into sorted unique integer HTTP status
// codes. Numeric strings are trimmed before conversion, and values outside 100 through
// 599 or non-finite values are discarded.
function collectStatusCodes(values: Iterable<unknown>): number[] {
  const seen = new Set<number>()
  const out: number[] = []
  for (const value of values) {
    const n = typeof value === 'string' ? Number(value.trim()) : Number(value)
    if (!Number.isFinite(n) || !Number.isInteger(n)) continue
    // Only integer codes within the HTTP status range are retained, so invalid
    // configuration entries cannot become retry response matches.
    if (n < 100 || n > 599) continue
    if (seen.has(n)) continue
    seen.add(n)
    out.push(n)
  }
  return out.sort((a, b) => a - b)
}

// parsePoolModeRetryStatusCodes parses comma-separated or whitespace-separated HTTP
// status codes into a normalized array. Blank input produces an empty array, and the
// shared collector removes invalid or duplicate numeric tokens.
export function parsePoolModeRetryStatusCodes(input: string): number[] {
  if (!input || !input.trim()) return []
  return collectStatusCodes(input.split(/[,\s]+/).filter((token) => token.trim()))
}

// formatPoolModeRetryStatusCodes formats a saved status-code array as comma-separated
// editable text. Non-array values produce an empty draft, and normalization removes
// invalid entries before sorting and joining the remaining codes.
export function formatPoolModeRetryStatusCodes(value: unknown): string {
  if (!Array.isArray(value)) return ''
  return collectStatusCodes(value).join(', ')
}

// normalizePoolModeRetryCount normalizes retry counts to integers between zero and the
// configured maximum. Non-finite input uses the default, while finite input is truncated
// before its lower and upper bounds are applied.
export function normalizePoolModeRetryCount(value: number): number {
  if (!Number.isFinite(value)) {
    // Non-finite input cannot represent an attempt count, so the shared default supplies a
    // usable draft before the provider form builds credentials.
    return DEFAULT_POOL_MODE_RETRY_COUNT
  }
  // Retry attempts are whole numbers; truncation precedes clamping so negative and
  // oversized finite drafts settle within the supported zero-to-ten range.
  const normalized = Math.trunc(value)
  if (normalized < 0) {
    return 0
  }
  if (normalized > MAX_POOL_MODE_RETRY_COUNT) {
    return MAX_POOL_MODE_RETRY_COUNT
  }
  return normalized
}
