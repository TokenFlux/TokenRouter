import { describe, expect, it, vi } from 'vitest'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
  }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    antigravity: {
      generateAuthUrl: vi.fn(),
      exchangeCode: vi.fn(),
      refreshAntigravityToken: vi.fn(),
    },
  },
}))

import { adminAPI } from '@/api/admin'
import { useAntigravityOAuth } from '@/composables/useAntigravityOAuth'

// useAntigravityOAuth.spec.ts checks refresh-token selection, optional GCP
// Project ID forwarding, and server diagnostic preservation in Antigravity
// authorization helpers. API, translation, and notification dependencies are
// replaced by fixtures so each case isolates credential payloads and error
// state updates.

describe('useAntigravityOAuth.buildCredentials', () => {
  // A refresh response omits a replacement refresh token. Credential
  // construction keeps the submitted token so the provider record still
  // contains a refresh credential.
  it('falls back to the submitted refresh token when the response omits it', () => {
    const oauth = useAntigravityOAuth()

    const credentials = oauth.buildCredentials(
      {
        access_token: 'access-token',
        expires_at: 1_900_000_000,
      },
      'submitted-refresh-token',
    )

    expect(credentials.refresh_token).toBe('submitted-refresh-token')
  })

  // A refresh response supplies whitespace for its refresh token. Credential
  // construction treats that response as absent and carries the submitted
  // token into the record.
  it('falls back to the submitted refresh token when the response returns whitespace', () => {
    const oauth = useAntigravityOAuth()

    const credentials = oauth.buildCredentials(
      {
        access_token: 'access-token',
        refresh_token: '   ',
        expires_at: 1_900_000_000,
      },
      'submitted-refresh-token',
    )

    expect(credentials.refresh_token).toBe('submitted-refresh-token')
  })

  // A refresh response supplies a rotated refresh token alongside the
  // submitted token. Credential construction selects the returned
  // replacement for the resulting provider record.
  it('prefers a new refresh token returned by the response', () => {
    const oauth = useAntigravityOAuth()

    const credentials = oauth.buildCredentials(
      {
        access_token: 'access-token',
        refresh_token: 'rotated-refresh-token',
        expires_at: 1_900_000_000,
      },
      'submitted-refresh-token',
    )

    expect(credentials.refresh_token).toBe('rotated-refresh-token')
  })

  // Token exchange and refresh validation return plan_type and privacy_mode
  // metadata. Credential and extra builders preserve nonblank values while
  // omitting absent fields.
  it('persists plan_type in credentials and privacy_mode in extra metadata', () => {
    const oauth = useAntigravityOAuth()
    const tokenInfo = {
      access_token: 'access-token',
      refresh_token: 'rt-1',
      expires_at: 1_900_000_000,
      plan_type: 'Pro',
      privacy_mode: 'privacy_set',
    }

    expect(oauth.buildCredentials(tokenInfo)).toMatchObject({
      plan_type: 'Pro',
    })
    expect(oauth.buildExtraInfo(tokenInfo)).toEqual({
      privacy_mode: 'privacy_set',
    })
    expect(oauth.buildExtraInfo({ access_token: 'access-token' })).toBeUndefined()
  })
})

describe('useAntigravityOAuth.exchangeAuthCode and validateRefreshToken', () => {
  // An operator enters a GCP Project ID during Antigravity manual exchange or
  // refresh validation. Both calls trim and forward project_id to the backend
  // while preserving unwrapped server diagnostic messages on failure.
  it('forwards trimmed project_id and surfaces unwrapped API error messages', async () => {
    const exchangeMock = vi.mocked(adminAPI.antigravity.exchangeCode)
    const refreshMock = vi.mocked(adminAPI.antigravity.refreshAntigravityToken)
    exchangeMock.mockResolvedValueOnce({ access_token: 'at', project_id: 'my-ag-proj' })
    refreshMock.mockRejectedValueOnce({ message: 'missing project_id: no re-login needed' })

    const oauth = useAntigravityOAuth()
    const exchanged = await oauth.exchangeAuthCode({
      code: ' auth-code ',
      sessionId: 'sess-1',
      state: 'st-1',
      projectId: '  my-ag-proj  ',
      proxyId: 7,
    })

    expect(exchanged?.project_id).toBe('my-ag-proj')
    expect(exchangeMock).toHaveBeenCalledWith({
      session_id: 'sess-1',
      state: 'st-1',
      code: 'auth-code',
      project_id: 'my-ag-proj',
      proxy_id: 7,
    })

    const validated = await oauth.validateRefreshToken(' rt-1 ', 7, '  my-ag-proj  ')
    expect(validated).toBeNull()
    expect(refreshMock).toHaveBeenCalledWith('rt-1', 7, '  my-ag-proj  ')
    expect(oauth.error.value).toBe('missing project_id: no re-login needed')
  })
})
