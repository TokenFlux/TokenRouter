import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'

export interface OpenAITokenInfo {
  access_token?: string
  refresh_token?: string
  client_id?: string
  id_token?: string
  token_type?: string
  expires_in?: number
  expires_at?: number
  scope?: string
  email?: string
  name?: string
  plan_type?: string
  privacy_mode?: string
  // OpenAI specific IDs (extracted from ID Token)
  chatgpt_account_id?: string
  chatgpt_user_id?: string
  organization_id?: string
  [key: string]: unknown
}

export type OpenAIOAuthPlatform = 'openai'

export interface OpenAIOAuthSession {
  authUrl: string
  sessionId: string
  state: string
}

export function useOpenAIOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()
  const endpointPrefix = '/admin/openai'

  // State
  const authUrl = ref('')
  const sessionId = ref('')
  const oauthState = ref('')
  const authSessions = ref<OpenAIOAuthSession[]>([])
  const loading = ref(false)
  const error = ref('')

  const extractStateFromAuthUrl = (urlValue: string): string => {
    try {
      const parsed = new URL(urlValue)
      return parsed.searchParams.get('state') || ''
    } catch {
      return ''
    }
  }

  const setCurrentAuthSession = (session: OpenAIOAuthSession | null) => {
    authUrl.value = session?.authUrl || ''
    sessionId.value = session?.sessionId || ''
    oauthState.value = session?.state || ''
  }

  const requestAuthSession = async (
    proxyId?: number | null,
    redirectUri?: string
  ): Promise<OpenAIOAuthSession | null> => {
    const payload: Record<string, unknown> = {}
    if (proxyId) {
      payload.proxy_id = proxyId
    }
    if (redirectUri) {
      payload.redirect_uri = redirectUri
    }

    const response = await adminAPI.accounts.generateAuthUrl(
      `${endpointPrefix}/generate-auth-url`,
      payload
    )
    return {
      authUrl: response.auth_url,
      sessionId: response.session_id,
      state: extractStateFromAuthUrl(response.auth_url)
    }
  }

  // Reset state
  const resetState = () => {
    authUrl.value = ''
    sessionId.value = ''
    oauthState.value = ''
    authSessions.value = []
    loading.value = false
    error.value = ''
  }

  // Generate auth URL for OpenAI OAuth
  const generateAuthUrl = async (
    proxyId?: number | null,
    redirectUri?: string
  ): Promise<boolean> => {
    loading.value = true
    authUrl.value = ''
    sessionId.value = ''
    oauthState.value = ''
    authSessions.value = []
    error.value = ''

    try {
      const session = await requestAuthSession(proxyId, redirectUri)
      if (!session) return false
      authSessions.value = [session]
      setCurrentAuthSession(session)
      return true
    } catch (err: any) {
      error.value = extractApiErrorMessage(err, t('admin.accounts.oauth.openai.failedToGenerateUrl'))
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  // 追加生成一条授权链接，用于 OpenAI OAuth 批量导入时保持多组 session/state。
  const appendAuthUrl = async (
    proxyId?: number | null,
    redirectUri?: string
  ): Promise<OpenAIOAuthSession | null> => {
    loading.value = true
    error.value = ''

    try {
      const session = await requestAuthSession(proxyId, redirectUri)
      if (!session) return null
      authSessions.value = [...authSessions.value, session]
      setCurrentAuthSession(session)
      return session
    } catch (err: any) {
      error.value = extractApiErrorMessage(err, t('admin.accounts.oauth.openai.failedToGenerateUrl'))
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const removeAuthSession = (targetSessionId: string) => {
    authSessions.value = authSessions.value.filter((session) => session.sessionId !== targetSessionId)
    if (sessionId.value === targetSessionId) {
      setCurrentAuthSession(authSessions.value[authSessions.value.length - 1] || null)
    }
  }

  // Exchange auth code for tokens
  const exchangeAuthCode = async (
    code: string,
    currentSessionId: string,
    state: string,
    proxyId?: number | null
  ): Promise<OpenAITokenInfo | null> => {
    if (!code.trim() || !currentSessionId || !state.trim()) {
      error.value = 'Missing auth code, session ID, or state'
      return null
    }

    loading.value = true
    error.value = ''

    try {
      const payload: { session_id: string; code: string; state: string; proxy_id?: number } = {
        session_id: currentSessionId,
        code: code.trim(),
        state: state.trim()
      }
      if (proxyId) {
        payload.proxy_id = proxyId
      }

      const tokenInfo = await adminAPI.accounts.exchangeCode(`${endpointPrefix}/exchange-code`, payload)
      return tokenInfo as OpenAITokenInfo
    } catch (err: any) {
      error.value = extractI18nErrorMessage(
        err,
        t,
        'admin.accounts.oauth.openai.errors',
        t('admin.accounts.oauth.openai.failedToExchangeCode')
      )
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  // Validate refresh token and get full token info
  // clientId: 指定 OAuth client_id（用于第三方渠道获取的 RT，如 app_LlGpXReQgckcGGUo2JrYvtJK）
  const validateRefreshToken = async (
    refreshToken: string,
    proxyId?: number | null,
    clientId?: string
  ): Promise<OpenAITokenInfo | null> => {
    if (!refreshToken.trim()) {
      error.value = 'Missing refresh token'
      return null
    }

    loading.value = true
    error.value = ''

    try {
      // Use dedicated refresh-token endpoint
      const tokenInfo = await adminAPI.accounts.refreshOpenAIToken(
        refreshToken.trim(),
        proxyId,
        `${endpointPrefix}/refresh-token`,
        clientId
      )
      return tokenInfo as OpenAITokenInfo
    } catch (err: any) {
      error.value = extractI18nErrorMessage(
        err,
        t,
        'admin.accounts.oauth.openai.errors',
        t('admin.accounts.oauth.openai.failedToValidateRT')
      )
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  // Build credentials for OpenAI OAuth account (aligned with backend BuildAccountCredentials)
  const buildCredentials = (tokenInfo: OpenAITokenInfo): Record<string, unknown> => {
    const creds: Record<string, unknown> = {
      access_token: tokenInfo.access_token,
      expires_at: tokenInfo.expires_at
    }

    // 仅在返回了新的 refresh_token 时才写入，防止用空值覆盖已有令牌
    if (tokenInfo.refresh_token) {
      creds.refresh_token = tokenInfo.refresh_token
    }
    if (tokenInfo.id_token) {
      creds.id_token = tokenInfo.id_token
    }
    if (tokenInfo.email) {
      creds.email = tokenInfo.email
    }
    if (tokenInfo.chatgpt_account_id) {
      creds.chatgpt_account_id = tokenInfo.chatgpt_account_id
    }
    if (tokenInfo.chatgpt_user_id) {
      creds.chatgpt_user_id = tokenInfo.chatgpt_user_id
    }
    if (tokenInfo.organization_id) {
      creds.organization_id = tokenInfo.organization_id
    }
    if (tokenInfo.plan_type) {
      creds.plan_type = tokenInfo.plan_type
    }
    if (tokenInfo.client_id) {
      creds.client_id = tokenInfo.client_id
    }

    return creds
  }

  // Build extra info from token response
  const buildExtraInfo = (tokenInfo: OpenAITokenInfo): Record<string, string> | undefined => {
    const extra: Record<string, string> = {}
    if (tokenInfo.email) {
      extra.email = tokenInfo.email
    }
    if (tokenInfo.name) {
      extra.name = tokenInfo.name
    }
    if (tokenInfo.privacy_mode) {
      extra.privacy_mode = tokenInfo.privacy_mode
    }
    return Object.keys(extra).length > 0 ? extra : undefined
  }

  return {
    // State
    authUrl,
    sessionId,
    oauthState,
    authSessions,
    loading,
    error,
    // Methods
    resetState,
    generateAuthUrl,
    appendAuthUrl,
    removeAuthSession,
    exchangeAuthCode,
    validateRefreshToken,
    buildCredentials,
    buildExtraInfo
  }
}
