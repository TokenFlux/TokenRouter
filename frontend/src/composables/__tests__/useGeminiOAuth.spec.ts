import { describe, expect, it, vi } from 'vitest'

// showErrorMock records toast notifications published during authorization
// helper failures so tests can assert exact diagnostic propagation.
const showErrorMock = vi.hoisted(() => vi.fn())

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
  }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    gemini: {
      generateAuthUrl: vi.fn(),
      exchangeCode: vi.fn(),
      getCapabilities: vi.fn(),
    },
  },
}))

import { adminAPI } from '@/api/admin'
import { useGeminiOAuth } from '@/composables/useGeminiOAuth'

// useGeminiOAuth.spec.ts checks optional GCP Project ID forwarding and
// structured backend diagnostic retention during Gemini OAuth code exchange.
// Mocked API, i18n, and toast stores let each test inspect payload fields and
// surfaced recovery messages directly.

describe('useGeminiOAuth.exchangeAuthCode', () => {
  // An operator completes a second code exchange after filling a GCP Project ID
  // in place. The composable trims and forwards project_id alongside session
  // proof and returns the exchanged token metadata.
  it('forwards trimmed project_id override during code exchange', async () => {
    const exchangeMock = vi.mocked(adminAPI.gemini.exchangeCode)
    exchangeMock.mockResolvedValueOnce({
      access_token: 'at-1',
      project_id: 'gcp-proj-42',
      oauth_type: 'code_assist',
      tier_id: 'gcp_standard',
    })

    const oauth = useGeminiOAuth()
    const result = await oauth.exchangeAuthCode({
      code: ' code-1 ',
      sessionId: 'sess-1',
      state: 'state-1',
      projectId: '  gcp-proj-42  ',
      oauthType: 'code_assist',
      tierId: 'gcp_standard',
    })

    expect(result?.project_id).toBe('gcp-proj-42')
    expect(exchangeMock).toHaveBeenCalledWith({
      session_id: 'sess-1',
      state: 'state-1',
      code: 'code-1',
      project_id: 'gcp-proj-42',
      oauth_type: 'code_assist',
      tier_id: 'gcp_standard',
    })
  })

  // Backend code exchange rejects with an unwrapped diagnostic containing an
  // activation URL or same-session Project ID instruction. The composable
  // preserves that exact diagnostic text instead of replacing it with a generic
  // fallback message.
  it('preserves structured server diagnostic text when exchange fails', async () => {
    const exchangeMock = vi.mocked(adminAPI.gemini.exchangeCode)
    const diagnostic =
      'Failed to exchange code: account is enrolled in Code Assist without a managed project_id (no re-login needed)'
    exchangeMock.mockRejectedValueOnce({ message: diagnostic })

    const oauth = useGeminiOAuth()
    const result = await oauth.exchangeAuthCode({
      code: 'code-1',
      sessionId: 'sess-1',
      state: 'state-1',
    })

    expect(result).toBeNull()
    expect(oauth.error.value).toBe(diagnostic)
    expect(showErrorMock).toHaveBeenCalledWith(diagnostic)
  })
})
