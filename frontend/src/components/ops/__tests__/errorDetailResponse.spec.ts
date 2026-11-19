import { describe, expect, it } from 'vitest'
import type { OpsErrorDetail } from '@/api/admin/ops'
import { resolvePrimaryResponseBody, resolveUpstreamPayload } from '../errorDetailResponse'

// errorDetailResponse.spec.ts checks response selection from retained gateway
// and upstream evidence using direct helper calls. Fixtures vary wrapper
// specificity, source priority, and empty placeholders so the chosen body
// reflects the selected request or upstream view.

// makeDetail builds a complete request-error fixture with overridable response
// evidence. Defaults provide identity and classification fields, while each test
// replaces the specific bodies needed to exercise source selection.
function makeDetail(overrides: Partial<OpsErrorDetail>): OpsErrorDetail {
  return {
    id: 1,
    created_at: '2026-01-01T00:00:00Z',
    phase: 'request',
    type: 'api_error',
    error_owner: 'platform',
    error_source: 'gateway',
    severity: 'P2',
    status_code: 502,
    platform: 'openai',
    model: 'gpt-4o-mini',
    resolved: false,
    client_request_id: 'crid-1',
    request_id: 'rid-1',
    message: 'Upstream request failed',
    user_email: 'user@example.com',
    provider_name: 'acc',
    group_name: 'group',
    error_body: '',
    user_agent: '',
    is_business_limited: false,
    ...overrides,
  }
}

describe('errorDetailResponse', () => {
  // A request error contains a known generic gateway wrapper and a detailed
  // upstream body. The primary response must select the upstream evidence for
  // this recognized wrapper.
  it('prefers upstream payload for request modal when error_body is generic gateway wrapper', () => {
    const detail = makeDetail({
      error_body: JSON.stringify({
        type: 'error',
        error: {
          type: 'upstream_error',
          message: 'Upstream request failed',
        },
      }),
      upstream_error_detail: '{"provider_message":"real upstream detail"}',
    })

    expect(resolvePrimaryResponseBody(detail, 'request')).toBe(
      '{"provider_message":"real upstream detail"}',
    )
  })

  // A request body contains a specific authentication explanation alongside
  // upstream evidence. The primary response must retain that client-visible body
  // because its message is not a generic wrapper.
  it('keeps error_body for request modal when body is not generic wrapper', () => {
    const detail = makeDetail({
      error_body: JSON.stringify({
        type: 'error',
        error: {
          type: 'upstream_error',
          message: 'Upstream authentication failed, please contact administrator',
        },
      }),
      upstream_error_detail: '{"provider_message":"real upstream detail"}',
    })

    expect(resolvePrimaryResponseBody(detail, 'request')).toBe(detail.error_body)
  })

  // An upstream detail view contains both a gateway body and an upstream
  // message. The primary response must select the upstream evidence for that
  // view category.
  it('uses upstream payload first in upstream modal', () => {
    const detail = makeDetail({
      phase: 'upstream',
      upstream_error_message: 'provider 503 overloaded',
      error_body:
        '{"type":"error","error":{"type":"upstream_error","message":"Upstream request failed"}}',
    })

    expect(resolvePrimaryResponseBody(detail, 'upstream')).toBe('provider 503 overloaded')
  })

  // A request detail has no client response body but retains an upstream
  // message. The primary response must use the available upstream evidence as
  // its body.
  it('falls back to upstream payload when request error_body is empty', () => {
    const detail = makeDetail({
      error_body: '',
      upstream_error_message: 'dial tcp timeout',
    })

    expect(resolvePrimaryResponseBody(detail, 'request')).toBe('dial tcp timeout')
  })

  // Upstream detail, event, and message fields are progressively removed from
  // fixtures. Selection must follow their defined priority and return the first
  // remaining nonempty candidate.
  it('resolves upstream payload by detail -> events -> message priority', () => {
    expect(
      resolveUpstreamPayload(
        makeDetail({
          upstream_error_detail: 'detail payload',
          upstream_errors: '[{"message":"event payload"}]',
          upstream_error_message: 'message payload',
        }),
      ),
    ).toBe('detail payload')

    expect(
      resolveUpstreamPayload(
        makeDetail({
          upstream_error_detail: '',
          upstream_errors: '[{"message":"event payload"}]',
          upstream_error_message: 'message payload',
        }),
      ),
    ).toBe('[{"message":"event payload"}]')

    expect(
      resolveUpstreamPayload(
        makeDetail({
          upstream_error_detail: '',
          upstream_errors: '',
          upstream_error_message: 'message payload',
        }),
      ),
    ).toBe('message payload')
  })

  // Upstream fields contain serialized empty arrays, objects, or null
  // placeholders. Each fixture must resolve to empty evidence when no later
  // field contains useful text.
  it('treats empty JSON placeholders in upstream payload as empty', () => {
    expect(
      resolveUpstreamPayload(
        makeDetail({
          upstream_error_detail: '',
          upstream_errors: '[]',
          upstream_error_message: '',
        }),
      ),
    ).toBe('')

    expect(
      resolveUpstreamPayload(
        makeDetail({
          upstream_error_detail: '',
          upstream_errors: '{}',
          upstream_error_message: '',
        }),
      ),
    ).toBe('')

    expect(
      resolveUpstreamPayload(
        makeDetail({
          upstream_error_detail: '',
          upstream_errors: 'null',
          upstream_error_message: '',
        }),
      ),
    ).toBe('')
  })

  // A higher-priority upstream field contains an empty serialized placeholder.
  // Selection must continue to the later message field and return its available
  // diagnostic content.
  it('skips placeholder candidates and falls back to the next upstream field', () => {
    expect(
      resolveUpstreamPayload(
        makeDetail({
          upstream_error_detail: '',
          upstream_errors: '[]',
          upstream_error_message: 'fallback message',
        }),
      ),
    ).toBe('fallback message')

    expect(
      resolveUpstreamPayload(
        makeDetail({
          upstream_error_detail: 'null',
          upstream_errors: '',
          upstream_error_message: 'fallback message',
        }),
      ),
    ).toBe('fallback message')
  })
})
