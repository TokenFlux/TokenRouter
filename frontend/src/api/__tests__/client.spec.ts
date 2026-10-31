import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import axios from 'axios'
import type { AxiosInstance } from 'axios'

/**
 * client.spec.ts checks browser request metadata, response envelopes and
 * authentication recovery through an Axios adapter. The cases exercise session
 * storage, UI markers, cancellation and network rejection, including an account
 * change while token refresh is pending.
 */

/**
 * Install the fixed locale mock before importing client so interceptor error
 * translation uses the controlled locale returned by getLocale.
 */
vi.mock('@/i18n', () => ({
  /**
   * getLocale supplies the fixed locale used by this request-interceptor test
   * fixture.
   */
  getLocale: () => 'zh-CN',
}))

describe('API Client', () => {
  let apiClient: AxiosInstance

  beforeEach(async () => {
    localStorage.clear()
    window.history.replaceState({}, '', '/')
    Object.defineProperty(navigator, 'locks', {
      configurable: true,
      value: undefined,
    })
    /**
     * Reload the module after resetting mocks so interceptor registrations and
     * refresh state do not leak between cases.
     */
    vi.resetModules()
    const mod = await import('@/api/client')
    apiClient = mod.apiClient
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllEnvs()
  })

  // A plugin owns code and data as ordinary response fields while using the
  // shared session client. Its query remains exact and its payload is not treated as a native envelope.
  it('preserves raw plugin replies and explicit query values', async () => {
    localStorage.setItem('auth_token', 'plugin-session')
    const payload = { code: 42, data: { amount: '1' }, message: 'Plugin-owned payload' }
    const adapter = vi.fn(async (config) => ({
      status: 200,
      data: payload,
      headers: {},
      config,
      statusText: 'OK',
    }))
    apiClient.defaults.adapter = adapter
    const response = await apiClient.get('/plugins/test.plugin/api/status', {
      params: { day: 'today' },
    })
    expect(response.data).toEqual(payload)
    expect(adapter.mock.calls[0][0].params).toEqual({ day: 'today' })
    expect(adapter.mock.calls[0][0].headers.get('Authorization')).toBe('Bearer plugin-session')
  })

  // A plugin returns its own unauthorized or feature-looking error after its handler runs.
  // The producer marker preserves the host session and prevents automatic mutation replay or unrelated feature-flag changes.
  it.each([401, 404])(
    'keeps plugin-owned HTTP %s outside native session recovery',
    async (status) => {
      localStorage.setItem('auth_token', 'current-host-token')
      localStorage.setItem('refresh_token', 'current-refresh-token')
      const refresh = vi.spyOn(axios, 'post').mockResolvedValue({
        data: {
          code: 0,
          data: {
            access_token: 'unexpected',
            refresh_token: 'unexpected-refresh',
            expires_in: 3600,
          },
        },
      })
      const message = status === 404 ? 'Ops monitoring is disabled' : 'Plugin credential expired'
      const adapter = vi.fn(async (config) => {
        throw {
          config,
          code: 'ERR_BAD_REQUEST',
          message: 'HTTP failed',
          response: {
            status,
            data: { code: 'PLUGIN_REJECTED', message },
            headers: { 'x-tokenrouter-plugin-response': '1' },
          },
        }
      })
      apiClient.defaults.adapter = adapter
      await expect(
        apiClient.post('/plugins/test.plugin/api/action', { action: 'write' }),
      ).rejects.toMatchObject({ status, code: 'PLUGIN_REJECTED', message })
      expect(adapter).toHaveBeenCalledTimes(1)
      expect(refresh).not.toHaveBeenCalled()
      expect(localStorage.getItem('auth_token')).toBe('current-host-token')
      expect(localStorage.getItem('refresh_token')).toBe('current-refresh-token')
      expect(sessionStorage.getItem('auth_expired')).toBeNull()
      expect(localStorage.getItem('ops_monitoring_enabled_cached')).toBeNull()
    },
  )

  // Host JWT admission can reject a plugin URL before any plugin handler runs.
  // An unmarked unauthorized response still refreshes the host token and then delivers the plugin's raw successful payload.
  it('refreshes host authentication failures on contribution URLs', async () => {
    localStorage.setItem('auth_token', 'expired-host-token')
    localStorage.setItem('refresh_token', 'refresh-token')
    const adapter = vi
      .fn()
      .mockRejectedValueOnce({
        config: {
          url: '/plugins/test.plugin/api/status',
          headers: { Authorization: 'Bearer expired-host-token' },
        },
        response: { status: 401, data: { code: 'TOKEN_EXPIRED' }, headers: {} },
        code: 'ERR_BAD_REQUEST',
      })
      .mockImplementationOnce(async (config) => ({
        config,
        status: 200,
        data: { ok: true },
        headers: { 'x-tokenrouter-plugin-response': '1' },
        statusText: 'OK',
      }))
    apiClient.defaults.adapter = adapter
    vi.spyOn(axios, 'post').mockResolvedValueOnce({
      data: {
        code: 0,
        data: {
          access_token: 'fresh-host-token',
          refresh_token: 'fresh-refresh-token',
          expires_in: 3600,
          token_type: 'Bearer',
        },
      },
    })
    await expect(apiClient.get('/plugins/test.plugin/api/status')).resolves.toMatchObject({
      data: { ok: true },
    })
    expect(adapter).toHaveBeenCalledTimes(2)
    expect(adapter.mock.calls[1][0].headers.get('Authorization')).toBe('Bearer fresh-host-token')
  })

  /**
   * Request interceptors.
   */

  describe('请求拦截器', () => {
    /**
     * A configured API base omits its leading slash. The shared client
     * normalizes that relative input and builds a callback URL rooted at
     * /api/v1.
     */
    it('规范化相对 API base，避免在回调页拼出相对 v1 路径', async () => {
      vi.resetModules()
      vi.stubEnv('VITE_API_BASE_URL', 'api/v1')

      const mod = await import('@/api/client')

      expect(mod.apiClient.defaults.baseURL).toBe('/api/v1')
      expect(mod.buildApiUrl('/auth/oauth/github/callback?code=abc')).toBe(
        '/api/v1/auth/oauth/github/callback?code=abc',
      )
    })

    /**
     * A stored access token is present when a GET reaches the adapter. The
     * outgoing Authorization header contains that token with the Bearer prefix.
     */
    it('自动附加 Authorization 头', async () => {
      localStorage.setItem('auth_token', 'my-jwt-token')

      /**
       * Capture the outgoing request configuration.
       */
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.get('/test')

      const config = adapter.mock.calls[0][0]
      expect(config.headers.get('Authorization')).toBe('Bearer my-jwt-token')
    })

    /**
     * An anonymous browser sends a request through the shared client. The
     * adapter observes no Authorization header when localStorage contains no
     * access token.
     */
    it('无 token 时不附加 Authorization 头', async () => {
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.get('/test')

      const config = adapter.mock.calls[0][0]
      expect(config.headers.get('Authorization')).toBeFalsy()
    })

    /**
     * A GET passes through the request interceptor with no query parameters. The
     * outgoing parameters include a timezone for server-side date
     * interpretation.
     */
    it('GET 请求自动附加 timezone 参数', async () => {
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.get('/test')

      const config = adapter.mock.calls[0][0]
      expect(config.params).toHaveProperty('timezone')
    })

    /**
     * A POST passes through the same interceptor used by GET requests. Its
     * parameters receive no automatic timezone, keeping mutation payload
     * handling separate from date-filtered reads.
     */
    it('POST 请求不附加 timezone 参数', async () => {
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.post('/test', { foo: 'bar' })

      const config = adapter.mock.calls[0][0]
      expect(config.params?.timezone).toBeUndefined()
    })

    /**
     * An OAuth binding-cookie request uses the shared client defaults. The
     * adapter sees credentials enabled so browser cookies can participate in the
     * request.
     */
    it('请求默认带 withCredentials 以支持跨域 cookie', async () => {
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.post('/auth/oauth/bind-token')

      const config = adapter.mock.calls[0][0]
      expect(config.withCredentials).toBe(true)
    })

    /**
     * An administrator endpoint is requested before the browser enters an
     * administrator page. The target URL alone causes the administrator UI
     * marker header to be attached.
     */
    it('Admin API 在进入管理页面前也带 Admin UI 标记', async () => {
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.get('/admin/users')

      const config = adapter.mock.calls[0][0]
      expect(config.headers.get('X-Admin-UI-Request')).toBe('1')
    })

    /**
     * A shared group endpoint is called from an administrator dashboard. The
     * active page supplies administrator context even though the target itself
     * is shared with users.
     */
    it('管理页面调用共享 API 时带 Admin UI 标记', async () => {
      window.history.replaceState({}, '', '/admin/dashboard')
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.get('/groups/available')

      const config = adapter.mock.calls[0][0]
      expect(config.headers.get('X-Admin-UI-Request')).toBe('1')
    })

    /**
     * A shared group endpoint is called from an ordinary browser page. The
     * adapter sees no administrator marker, protecting classification of
     * user-originated requests.
     */
    it('普通用户页面调用共享 API 时不带 Admin UI 标记', async () => {
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.get('/groups/available')

      const config = adapter.mock.calls[0][0]
      expect(config.headers.get('X-Admin-UI-Request')).toBeFalsy()
    })

    /**
     * The current-user endpoint is requested from an ordinary page. Its
     * allowlisted path adds the user timing marker while leaving the
     * administrator marker absent.
     */
    it('用户侧 timing API 自动带 User UI 标记', async () => {
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.get('/auth/me')

      const config = adapter.mock.calls[0][0]
      expect(config.headers.get('X-User-UI-Request')).toBe('1')
      expect(config.headers.get('X-Admin-UI-Request')).toBeFalsy()
    })

    /**
     * Authenticated and public payment endpoints are requested consecutively.
     * The user timing marker appears on the plan request and remains absent from
     * public order verification.
     */
    it('支付用户 API 带 User UI 标记，公开支付 API 不带', async () => {
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.get('/payment/plans')
      expect(adapter.mock.calls[0][0].headers.get('X-User-UI-Request')).toBe('1')

      await apiClient.post('/payment/public/orders/verify', {})
      expect(adapter.mock.calls[1][0].headers.get('X-User-UI-Request')).toBeFalsy()
    })

    /**
     * An administrator page calls the shared keys endpoint. Both markers appear
     * because page context and the user timing allowlist independently qualify
     * this request.
     */
    it('管理页调用共享 API 时同时带 Admin 与 User UI 标记', async () => {
      window.history.replaceState({}, '', '/admin/dashboard')
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: {} },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await apiClient.get('/keys')

      const config = adapter.mock.calls[0][0]
      expect(config.headers.get('X-Admin-UI-Request')).toBe('1')
      expect(config.headers.get('X-User-UI-Request')).toBe('1')
    })
  })

  /**
   * Response interceptors.
   */

  describe('响应拦截器', () => {
    /**
     * A business error includes a server-generated validation detail. Rejection
     * retains its reason and localized detail so the form can display the
     * specific input failure.
     */
    it('保留服务端已生成的业务错误详情', async () => {
      apiClient.defaults.adapter = vi.fn().mockResolvedValue({
        status: 400,
        data: { code: 400, reason: 'INVALID_INPUT', message: '邮箱仅支持 example.com 域名。' },
        headers: {},
        config: { url: '/user' },
        statusText: 'Bad Request',
      })
      await expect(apiClient.put('/user', {})).rejects.toMatchObject({
        reason: 'INVALID_INPUT',
        message: '邮箱仅支持 example.com 域名。',
      })
    })

    /**
     * A successful response uses the standard zero-code envelope. Callers
     * receive the nested data object through response.data after the response
     * interceptor unwraps it.
     */
    it('code=0 时解包 data 字段', async () => {
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 0, data: { name: 'test' }, message: 'ok' },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      const response = await apiClient.get('/test')
      expect(response.data).toEqual({ name: 'test' })
    })

    /**
     * A successful HTTP response carries a nonzero business code. The client
     * rejects with that code and message so business failures cannot masquerade
     * as successful data.
     */
    it('code!=0 时拒绝并返回结构化错误', async () => {
      const adapter = vi.fn().mockResolvedValue({
        status: 200,
        data: { code: 1001, message: '参数错误', data: null },
        headers: {},
        config: {},
        statusText: 'OK',
      })
      apiClient.defaults.adapter = adapter

      await expect(apiClient.get('/test')).rejects.toEqual(
        expect.objectContaining({
          code: 1001,
          message: '参数错误',
        }),
      )
    })
  })

  /**
   * 401 token refresh.
   */

  describe('401 Token 刷新', () => {
    /**
     * An expired access token has no refresh token available. The unauthorized
     * response clears the stored access token before the test restores its
     * location stub.
     */
    it('无 refresh_token 时 401 清除 localStorage', async () => {
      localStorage.setItem('auth_token', 'expired-token')
      /**
       * Leave the refresh token absent.
       */

      // Mock window.location
      const originalLocation = window.location
      Object.defineProperty(window, 'location', {
        value: { ...originalLocation, pathname: '/dashboard', href: '/dashboard' },
        writable: true,
      })

      const adapter = vi.fn().mockRejectedValue({
        response: {
          status: 401,
          data: { code: 'TOKEN_EXPIRED', message: 'Token expired' },
        },
        config: {
          url: '/test',
          headers: { Authorization: 'Bearer expired-token' },
        },
        code: 'ERR_BAD_REQUEST',
      })
      apiClient.defaults.adapter = adapter

      await expect(apiClient.get('/test')).rejects.toBeDefined()

      expect(localStorage.getItem('auth_token')).toBeNull()

      /**
       * Restore the location stub.
       */
      Object.defineProperty(window, 'location', {
        value: originalLocation,
        writable: true,
      })
    })

    /**
     * Google One Tap rejects its credential while stale session data exists. The
     * client returns the credential reason without calling refresh or leaving
     * the current page.
     */
    it('One Tap 401 不刷新旧会话且不离开当前页面', async () => {
      localStorage.setItem('refresh_token', 'stale-refresh-token')
      localStorage.setItem('auth_user', JSON.stringify({ id: 7 }))
      const refreshSpy = vi.spyOn(axios, 'post').mockRejectedValue(new Error('unexpected refresh'))
      const originalLocation = window.location
      Object.defineProperty(window, 'location', {
        value: { ...originalLocation, pathname: '/home', href: '/home' },
        writable: true,
      })

      apiClient.defaults.adapter = vi.fn().mockRejectedValue({
        response: {
          status: 401,
          data: {
            code: 401,
            reason: 'GOOGLE_ONE_TAP_INVALID_CREDENTIAL',
            message: 'google credential is invalid',
          },
        },
        config: {
          url: '/auth/oauth/google/one-tap',
          headers: {},
        },
        code: 'ERR_BAD_REQUEST',
      })

      try {
        await expect(
          apiClient.post('/auth/oauth/google/one-tap', { credential: 'invalid' }),
        ).rejects.toMatchObject({ reason: 'GOOGLE_ONE_TAP_INVALID_CREDENTIAL' })
        expect(refreshSpy).not.toHaveBeenCalled()
        expect(window.location.href).toBe('/home')
      } finally {
        Object.defineProperty(window, 'location', {
          value: originalLocation,
          writable: true,
        })
      }
    })

    /**
     * An unauthorized request has a refresh token and a successful rotation
     * response. The client retries once, stores the replacement pair and sends
     * the replacement access token.
     */
    it('有 refresh_token 时刷新并重试原请求', async () => {
      localStorage.setItem('auth_token', 'expired-token')
      localStorage.setItem('refresh_token', 'refresh-token')
      localStorage.setItem('token_expires_at', String(Date.now() - 1))
      localStorage.setItem('auth_user', JSON.stringify({ id: 7 }))

      const adapter = vi
        .fn()
        .mockRejectedValueOnce({
          response: {
            status: 401,
            data: { code: 'TOKEN_EXPIRED', message: 'Token expired' },
          },
          config: {
            url: '/test',
            headers: { Authorization: 'Bearer expired-token' },
          },
          code: 'ERR_BAD_REQUEST',
        })
        .mockResolvedValueOnce({
          status: 200,
          data: { code: 0, data: { ok: true } },
          headers: {},
          config: {},
          statusText: 'OK',
        })
      apiClient.defaults.adapter = adapter
      vi.spyOn(axios, 'post').mockResolvedValueOnce({
        data: {
          code: 0,
          message: 'ok',
          data: {
            access_token: 'new-token',
            refresh_token: 'new-refresh-token',
            expires_in: 3600,
            token_type: 'Bearer',
          },
        },
      })

      await expect(apiClient.get('/test')).resolves.toMatchObject({ data: { ok: true } })

      expect(adapter).toHaveBeenCalledTimes(2)
      expect(localStorage.getItem('auth_token')).toBe('new-token')
      expect(localStorage.getItem('refresh_token')).toBe('new-refresh-token')
      expect(adapter.mock.calls[1][0].headers.get('Authorization')).toBe('Bearer new-token')
    })

    /**
     * Another user signs in while the first user's refresh is pending. The old
     * request rejects with session-changed status and leaves the later user's
     * stored session intact.
     */
    it('刷新期间换号时旧请求不会清除新会话', async () => {
      localStorage.setItem('auth_token', 'user-a-access')
      localStorage.setItem('refresh_token', 'user-a-refresh')
      localStorage.setItem('token_expires_at', String(Date.now() - 1))
      localStorage.setItem('auth_user', JSON.stringify({ id: 7 }))

      apiClient.defaults.adapter = vi.fn().mockRejectedValueOnce({
        response: {
          status: 401,
          data: { code: 'TOKEN_EXPIRED', message: 'Token expired' },
        },
        config: {
          url: '/test',
          headers: { Authorization: 'Bearer user-a-access' },
        },
        code: 'ERR_BAD_REQUEST',
      })

      /**
       * Hold the refresh rejection until its request has started and another
       * identity replaces storage, so failure handling observes the later user.
       */
      let rejectRefresh!: (reason: Error) => void
      vi.spyOn(axios, 'post').mockImplementationOnce(
        () =>
          new Promise((_resolve, reject) => {
            rejectRefresh = reject
          }),
      )

      const staleRequest = apiClient.get('/test')
      await vi.waitFor(() => expect(axios.post).toHaveBeenCalledTimes(1))

      localStorage.setItem('auth_token', 'user-b-access')
      localStorage.setItem('refresh_token', 'user-b-refresh')
      localStorage.setItem('token_expires_at', String(Date.now() + 3600_000))
      localStorage.setItem('auth_user', JSON.stringify({ id: 8 }))
      rejectRefresh(new Error('stale refresh failed'))

      await expect(staleRequest).rejects.toMatchObject({ code: 'AUTH_SESSION_CHANGED' })
      expect(localStorage.getItem('auth_token')).toBe('user-b-access')
      expect(localStorage.getItem('refresh_token')).toBe('user-b-refresh')
      expect(localStorage.getItem('auth_user')).toBe(JSON.stringify({ id: 8 }))
      expect(window.location.pathname).toBe('/')
    })
  })

  /**
   * Network failures.
   */

  describe('网络错误', () => {
    /**
     * A failed request has no HTTP response object. Rejection carries status
     * zero and the localized network message so consumers can distinguish
     * transport failure.
     */
    it('网络错误返回 status 0 的错误', async () => {
      const adapter = vi.fn().mockRejectedValue({
        code: 'ERR_NETWORK',
        message: 'Network Error',
        config: { url: '/test' },
        // The transport failed before any HTTP response arrived.
      })
      apiClient.defaults.adapter = adapter

      await expect(apiClient.get('/test')).rejects.toEqual(
        expect.objectContaining({
          status: 0,
          message: '网络连接失败，请检查网络后重试。',
        }),
      )
    })
  })

  /**
   * Request cancellation.
   */

  describe('请求取消', () => {
    /**
     * The adapter rejects with an Axios cancellation error. The request remains
     * rejected through the interceptor; this assertion exercises cancellation
     * handling without inspecting the error identity.
     */
    it('取消的请求保持原始取消错误', async () => {
      const source = axios.CancelToken.source()

      const adapter = vi.fn().mockRejectedValue(new axios.Cancel('Operation canceled'))
      apiClient.defaults.adapter = adapter

      await expect(apiClient.get('/test', { cancelToken: source.token })).rejects.toBeDefined()
    })
  })
})
