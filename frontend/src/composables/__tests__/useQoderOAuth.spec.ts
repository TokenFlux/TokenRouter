import { beforeEach, describe, expect, it, vi } from 'vitest'

const showError = vi.fn()

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError
  })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    qoder: {
      generateAuthUrl: vi.fn(),
      exchangeCode: vi.fn(),
      poll: vi.fn()
    }
  }
}))

import { adminAPI } from '@/api/admin'
import { useQoderOAuth } from '@/composables/useQoderOAuth'

beforeEach(() => {
  showError.mockReset()
  vi.mocked(adminAPI.qoder.generateAuthUrl).mockReset()
  vi.mocked(adminAPI.qoder.exchangeCode).mockReset()
  vi.mocked(adminAPI.qoder.poll).mockReset()
})

describe('useQoderOAuth', () => {
  it('generates an authorization URL and stores session state', async () => {
    vi.mocked(adminAPI.qoder.generateAuthUrl).mockResolvedValueOnce({
      auth_url: 'https://qoder.com/device/selectAccounts?nonce=n',
      session_id: 'session-id',
      state: 'state-value',
      expires_in: 600,
      interval: 2
    })

    const oauth = useQoderOAuth()
    const ok = await oauth.generateAuthUrl(7)

    expect(ok).toBe(true)
    expect(adminAPI.qoder.generateAuthUrl).toHaveBeenCalledWith({ proxy_id: 7 })
    expect(oauth.authUrl.value).toBe('https://qoder.com/device/selectAccounts?nonce=n')
    expect(oauth.sessionId.value).toBe('session-id')
    expect(oauth.state.value).toBe('state-value')
    expect(oauth.pollInterval.value).toBe(2)
  })

  it('exchanges a pasted callback URL with session and state', async () => {
    vi.mocked(adminAPI.qoder.exchangeCode).mockResolvedValueOnce({
      security_oauth_token: 'access-token',
      machine_id: 'machine-id'
    })

    const oauth = useQoderOAuth()
    const tokenInfo = await oauth.exchangeAuthCode({
      code: ' code ',
      callbackUrl: ' http://localhost:1455/callback?code=code&state=state-value ',
      sessionId: 'session-id',
      state: 'state-value',
      proxyId: 7
    })

    expect(tokenInfo?.security_oauth_token).toBe('access-token')
    expect(adminAPI.qoder.exchangeCode).toHaveBeenCalledWith({
      session_id: 'session-id',
      state: 'state-value',
      code: 'code',
      callback_url: 'http://localhost:1455/callback?code=code&state=state-value',
      proxy_id: 7
    })
  })

  it('allows completing authorization without a pasted code for device polling', async () => {
    vi.mocked(adminAPI.qoder.exchangeCode).mockResolvedValueOnce({
      security_oauth_token: 'access-token',
      machine_id: 'machine-id'
    })

    const oauth = useQoderOAuth()
    const tokenInfo = await oauth.exchangeAuthCode({
      sessionId: 'session-id',
      state: 'state-value'
    })

    expect(tokenInfo?.machine_id).toBe('machine-id')
    expect(adminAPI.qoder.exchangeCode).toHaveBeenCalledWith({
      session_id: 'session-id',
      state: 'state-value'
    })
  })

  it('polls the device authorization session', async () => {
    vi.mocked(adminAPI.qoder.poll).mockResolvedValueOnce({
      status: 'completed',
      token_info: {
        security_oauth_token: 'access-token',
        machine_id: 'machine-id'
      }
    })

    const oauth = useQoderOAuth()
    const result = await oauth.pollAuthorization({
      sessionId: 'session-id',
      state: 'state-value',
      proxyId: 7
    })

    expect(result?.status).toBe('completed')
    expect(result?.token_info?.machine_id).toBe('machine-id')
    expect(adminAPI.qoder.poll).toHaveBeenCalledWith({
      session_id: 'session-id',
      state: 'state-value',
      proxy_id: 7
    })
    expect(oauth.loading.value).toBe(false)
    expect(oauth.polling.value).toBe(false)
  })

  it('builds credentials compatible with Qoder token provider', () => {
    const oauth = useQoderOAuth()
    const credentials = oauth.buildCredentials({
      security_oauth_token: 'access-token',
      refresh_token: 'refresh-token',
      machine_id: 'machine-id',
      machine_token: 'machine-token',
      machine_type: 'desktop',
      uid: 'uid',
      aid: 'aid',
      organization_id: 'org-1',
      organization_name: 'Qoder Org',
      name: 'Qoder User',
      user_type: 'personal_standard',
      extra: { email: 'user@example.com' }
    })

    expect(credentials).toEqual({
      security_oauth_token: 'access-token',
      refresh_token: 'refresh-token',
      machine_id: 'machine-id',
      machine_token: 'machine-token',
      machine_type: 'desktop',
      uid: 'uid',
      aid: 'aid',
      organization_id: 'org-1',
      organization_name: 'Qoder Org',
      name: 'Qoder User',
      user_type: 'personal_standard',
      extra: { email: 'user@example.com' }
    })
  })
})
