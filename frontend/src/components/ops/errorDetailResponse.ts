import type { OpsErrorDetail } from '@/api/admin/ops'

// errorDetailResponse.ts selects the response body displayed for a request or
// upstream error. Stored gateway wrappers can hide the upstream response, while
// a specific client response remains useful evidence and keeps priority in the
// request view. Empty serialized containers do not provide evidence for
// selection.
//
// The selector recognizes a bounded set of gateway messages paired with the
// upstream_error type. It substitutes upstream evidence for those wrappers and
// for upstream views, checking detailed response, event payload, then message.
// Returned bodies are trimmed, so consumers needing exact stored whitespace use
// the separate errorDetailPresentation helpers.

// GENERIC_UPSTREAM_MESSAGES identifies gateway wrapper descriptions whose
// upstream payload can provide more specific diagnostic evidence.
const GENERIC_UPSTREAM_MESSAGES = new Set([
  'upstream request failed',
  'upstream request failed after retries',
  'upstream gateway error',
  'upstream service temporarily unavailable',
])

// ParsedGatewayError holds trimmed gateway error fields used to recognize
// wrappers.
type ParsedGatewayError = {
  type: string // trimmed gateway error type
  message: string // trimmed gateway error message
}

// parseGatewayErrorBody extracts trimmed type and message fields from a JSON
// error object. Malformed bodies, missing error objects, and objects with
// neither field return null for wrapper classification.
function parseGatewayErrorBody(raw: string): ParsedGatewayError | null {
  const text = String(raw || '').trim()
  if (!text) return null

  try {
    const parsed = JSON.parse(text) as Record<string, any>
    const err = parsed?.error as Record<string, any> | undefined
    if (!err || typeof err !== 'object') return null

    const type = typeof err.type === 'string' ? err.type.trim() : ''
    const message = typeof err.message === 'string' ? err.message.trim() : ''
    if (!type && !message) return null

    return { type, message }
  } catch {
    // A malformed body is not a recognized gateway wrapper; return no parsed
    // classification so request views can retain the available client text.
    return null
  }
}

// isGenericGatewayUpstreamError recognizes known messages paired with the
// upstream_error type. Parsing failures and other error types return false,
// keeping a specific client response eligible for primary display.
function isGenericGatewayUpstreamError(raw: string): boolean {
  const parsed = parseGatewayErrorBody(raw)
  if (!parsed) return false
  if (parsed.type !== 'upstream_error') return false
  return GENERIC_UPSTREAM_MESSAGES.has(parsed.message.toLowerCase())
}

// resolveUpstreamPayload selects the first nonempty trimmed upstream evidence
// string. Detailed bodies precede event data and messages; serialized empty
// containers and null placeholders are skipped before trying the next candidate.
export function resolveUpstreamPayload(
  detail:
    | Pick<OpsErrorDetail, 'upstream_error_detail' | 'upstream_errors' | 'upstream_error_message'>
    | null
    | undefined,
): string {
  if (!detail) return ''

  const candidates = [
    detail.upstream_error_detail,
    detail.upstream_errors,
    detail.upstream_error_message,
  ]

  for (const candidate of candidates) {
    const payload = String(candidate || '').trim()
    if (!payload) continue

    // Serialized empty containers carry no usable upstream response, so continue
    // to a later candidate before declaring the evidence absent.
    if (payload === '[]' || payload === '{}' || payload.toLowerCase() === 'null') {
      continue
    }

    return payload
  }

  return ''
}

// resolvePrimaryResponseBody chooses the response appropriate to the selected
// error category. Missing detail returns an empty string, and upstream views
// prefer available upstream evidence before the client body.
//
// Request views keep specific client bodies because they describe what the
// caller received. A recognized generic upstream gateway wrapper yields to
// retained upstream evidence, while an empty client body also permits that
// evidence to supply the response. All selected strings are trimmed before
// returning.
export function resolvePrimaryResponseBody(
  detail: OpsErrorDetail | null,
  errorType?: 'request' | 'upstream',
): string {
  if (!detail) return ''

  const upstreamPayload = resolveUpstreamPayload(detail)
  const errorBody = String(detail.error_body || '').trim()

  if (errorType === 'upstream') {
    return upstreamPayload || errorBody
  }

  if (!errorBody) {
    return upstreamPayload
  }

  // For request detail modal, keep client-visible body by default. But if that
  // body is a generic gateway wrapper, show upstream payload first.
  if (upstreamPayload && isGenericGatewayUpstreamError(errorBody)) {
    return upstreamPayload
  }

  return errorBody
}
