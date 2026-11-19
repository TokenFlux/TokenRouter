import { describe, expect, it } from 'vitest'
import type { OpsErrorDetail } from '@/api/admin/ops'
import {
  formatErrorPayload,
  getAttemptPayload,
  getErrorPayloads,
  getErrorSummary,
  summarizeErrorText,
} from '../errorDetailPresentation'

// errorDetailPresentation.spec.ts verifies readable summaries and exact retained
// response evidence through direct helper calls. Cases cover HTML, nested JSON
// messages, long text, source attribution, and empty placeholders while leaving
// rendering and clipboard behavior to the component suite.

/** makeDetail supplies stored text fields for focused presentation tests. */
function makeDetail(fields: Partial<OpsErrorDetail>): OpsErrorDetail {
  return { message: '', error_body: '', ...fields } as OpsErrorDetail
}

describe('error detail presentation', () => {
  // An upstream HTML page takes precedence over a gateway wrapper. The summary
  // identifies its format while the inspector retains the complete stored
  // markup.
  it('labels HTML and preserves its original content and all source labels', () => {
    const html =
      '  <!DOCTYPE html><html><script>throw new Error("unsafe")</script><h1>Blocked</h1></html>\n'
    const detail = makeDetail({
      upstream_error_message: html,
      upstream_error_detail: html,
      message: 'Gateway error',
    })
    expect(getErrorSummary(detail)).toEqual({ kind: 'html', text: '', source: 'upstream_message' })
    expect(getErrorPayloads(detail)[0]).toEqual({
      key: 'upstream_message',
      sources: ['upstream_message', 'upstream_detail'],
      value: html,
    })
    expect(formatErrorPayload(html)).toBe(html)
    expect(getAttemptPayload(detail)).toBe(html)
  })

  // Provider JSON encodings vary across supported protocols. Each recognized
  // message shape produces readable text from the supported nested objects,
  // quoted strings, and plain-text provider response fixtures.
  it.each([
    ['{"error":{"message":"Quota exceeded"}}', 'Quota exceeded'],
    ['{"error":"Quota exceeded"}', 'Quota exceeded'],
    ['{"message":"Quota exceeded"}', 'Quota exceeded'],
    ['{"detail":"Quota exceeded"}', 'Quota exceeded'],
    ['"Quota exceeded"', 'Quota exceeded'],
    ['Quota\n exceeded', 'Quota exceeded'],
  ])('extracts a message from %s', (raw, text) => {
    expect(summarizeErrorText(raw)).toEqual({ kind: 'text', text })
  })

  // Empty JSON placeholders should not displace a recorded gateway message.
  // Unknown structured responses retain their JSON classification and raw
  // evidence.
  it('skips empty placeholders and handles unknown JSON without inventing a cause', () => {
    const detail = makeDetail({
      upstream_error_message: ' { } ',
      upstream_error_detail: '[ ]',
      message: 'Useful message',
      error_body: 'null',
    })
    expect(getErrorSummary(detail)).toEqual({
      kind: 'text',
      text: 'Useful message',
      source: 'message',
    })
    expect(getErrorPayloads(detail)).toHaveLength(1)
    expect(getAttemptPayload(detail)).toBe('Useful message')
    expect(summarizeErrorText('{"debug":{"code":42}}')).toEqual({ kind: 'json', text: '' })
    expect(summarizeErrorText('{"error":{"message":"<html>Blocked</html>"}}').kind).toBe('html')
    expect(getErrorSummary(null).kind).toBe('empty')
  })

  // Long messages are shortened only in the summary. Copying or inspecting
  // diagnostic evidence must still expose the full string including surrounding
  // whitespace.
  it('bounds summaries while retaining exact long payloads and JSON formatting', () => {
    const raw = `  ${'failure '.repeat(200)}\n`
    const detail = makeDetail({ message: raw })
    expect(getErrorSummary(detail).text.length).toBeLessThanOrEqual(321)
    expect(getErrorSummary(detail).text.endsWith('…')).toBe(true)
    expect(getErrorPayloads(detail)[0].value).toBe(raw)
    expect(formatErrorPayload('{"message":"error"}')).toBe('{\n  "message": "error"\n}')
  })
})
