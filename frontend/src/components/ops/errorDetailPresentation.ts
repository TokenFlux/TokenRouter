import type { OpsErrorDetail } from '@/api/admin/ops'

// errorDetailPresentation.ts prepares short error descriptions and retained
// response sections for the administrator error dialog. Summaries prefer
// upstream evidence, while response sections keep the exact stored strings so
// copying a response does not silently replace it with a parsed description.
//
// HTML and unrecognized JSON receive format labels rather than diagnostic
// conclusions. Recognized JSON messages and plain text are bounded for summary
// display. Identical payloads share a section with every source label retained,
// so deduplication does not imply that a client response came from the upstream.
// These rules also apply to individual upstream attempt descriptions.

/** PayloadSource identifies the stored field represented by a response section. */
export type PayloadSource =
  'client' | 'upstream_message' | 'upstream_detail' | 'upstream_events' | 'message'

/** ErrorSummary describes a bounded message and its recorded source. */
export interface ErrorSummary {
  kind: 'text' | 'html' | 'json' | 'empty' // summary response format classification
  text: string // bounded extracted message text
  // source identifies the selected stored field when a candidate exists,
  // including candidates displayed through a format label.
  source?: PayloadSource // selected stored field
}

/** ErrorPayload retains exact response content and all matching source labels. */
export interface ErrorPayload {
  key: PayloadSource // first matching stored source
  // sources retains all fields with exactly matching content, in candidate
  // order, for the inspector attribution labels.
  sources: PayloadSource[] // all identical sources
  value: string // exact retained response content
}

// Summary previews are bounded separately from the original strings retained for
// copying and inspection in the response section.
const SUMMARY_LIMIT = 320

/** meaningfulPayload excludes blank values and serialized empty response placeholders.
 * Original whitespace is preserved for nonempty payloads because the response
 * inspector and clipboard expose the stored content to administrators.
 */
function meaningfulPayload(value: string | undefined): string {
  if (!value || /^(?:null|\[\s*\]|\{\s*\})?$/i.test(value.trim())) return ''
  return value
}

// summarizeErrorText classifies retained response text and bounds a readable
// diagnostic message. Empty input receives the empty classification, and
// recognized JSON message fields are selected before whitespace normalization.
//
// JSON objects without a supported message remain classified as JSON, while
// detected HTML receives a format label instead of a derived cause. The returned
// summary can be shortened, so callers use retained payload strings for copying
// and full inspection. Plain text and malformed JSON remain eligible for message
// display after format detection.
export function summarizeErrorText(raw: string): ErrorSummary {
  let text = raw.trim()
  if (!text) return { kind: 'empty', text: '' }
  try {
    const parsed: unknown = JSON.parse(text)
    if (typeof parsed === 'string') {
      text = parsed
    } else if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const body = parsed as Record<string, unknown>
      const error = body.error
      const errorMessage =
        error && typeof error === 'object' ? (error as Record<string, unknown>).message : error
      const message = [errorMessage, body.message, body.error_description, body.detail].find(
        (value) => typeof value === 'string' && value.trim(),
      )
      if (typeof message !== 'string') return { kind: 'json', text: '' }
      text = message
    } else {
      return { kind: 'json', text: '' }
    }
  } catch {
    // Provider responses may contain plain text or HTML, so failed JSON parsing
    // keeps the original text available for format detection and summary
    // display.
  }
  if (/<(?:!doctype\s+html|html|head|body|title|h[1-6]|div|p|script|style)\b/i.test(text)) {
    return { kind: 'html', text: '' }
  }
  text = text.replace(/\s+/g, ' ').trim()
  if (!text) return { kind: 'empty', text: '' }
  // Bound summary length to keep diagnostic bodies from dominating the dialog.
  return {
    kind: 'text',
    text: text.length > SUMMARY_LIMIT ? `${text.slice(0, SUMMARY_LIMIT)}…` : text,
  }
}

/** getErrorSummary selects the first recorded error description by diagnostic priority.
 * Upstream messages precede upstream details, gateway messages, and client bodies.
 * The selected source accompanies the summary even when its format needs a label.
 */
export function getErrorSummary(detail: OpsErrorDetail | null): ErrorSummary {
  if (!detail) return { kind: 'empty', text: '' }
  const candidates: Array<[PayloadSource, string | undefined]> = [
    ['upstream_message', detail.upstream_error_message],
    ['upstream_detail', detail.upstream_error_detail],
    ['message', detail.message],
    ['client', detail.error_body],
  ]
  for (const [source, candidate] of candidates) {
    const value = meaningfulPayload(candidate)
    if (value) return { ...summarizeErrorText(value), source }
  }
  return { kind: 'empty', text: '' }
}

// getErrorPayloads groups identical stored strings while preserving every source
// attribution. Missing details and serialized empty placeholders produce no
// sections, and each retained value keeps its original whitespace for copying.
//
// The first matching field supplies the stable section key. Later fields with
// exactly equal contents append their source labels to that section, so equal
// client and upstream bodies remain distinguishable by provenance even though
// the inspector renders their shared content once.
export function getErrorPayloads(detail: OpsErrorDetail | null): ErrorPayload[] {
  if (!detail) return []
  const candidates: Array<[PayloadSource, string | undefined]> = [
    ['client', detail.error_body],
    ['upstream_message', detail.upstream_error_message],
    ['upstream_detail', detail.upstream_error_detail],
    ['upstream_events', detail.upstream_errors],
    ['message', detail.message],
  ]
  const sections: ErrorPayload[] = []
  for (const [source, candidate] of candidates) {
    const value = meaningfulPayload(candidate)
    if (!value) continue
    const existing = sections.find((section) => section.value === value)
    if (existing) {
      existing.sources.push(source)
    } else {
      sections.push({ key: source, sources: [source], value })
    }
  }
  return sections
}

/** getAttemptPayload selects the original response shown for one upstream attempt.
 * Detailed upstream evidence precedes upstream messages and client output.
 * Whitespace is retained so copying the preview returns the stored response.
 */
export function getAttemptPayload(detail: OpsErrorDetail): string {
  const candidates = [
    detail.upstream_error_detail,
    detail.upstream_errors,
    detail.upstream_error_message,
    detail.error_body,
    detail.message,
  ]
  for (const candidate of candidates) {
    const value = meaningfulPayload(candidate)
    if (value) return value
  }
  return ''
}

/** formatErrorPayload indents valid JSON and preserves other response text.
 * This formatting affects the inspector only. Copy actions receive the stored
 * string directly so formatting never changes the copied diagnostic evidence.
 */
export function formatErrorPayload(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    // Non-JSON responses remain readable as text, including truncated HTML and
    // provider messages whose payloads cannot be decoded as complete JSON
    // documents.
    return raw
  }
}
