import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { AntigravityTokenInfo } from '@/api/admin/antigravity'

// useAntigravityOAuth.ts prepares Antigravity authorization requests and
// provider credentials. The UI receives a URL, session identifier, and state
// together, then submits the session information with a pasted authorization
// code. Request failures populate local error state, while URL generation and
// code exchange also notify the application store.
//
// Refresh validation may return an access token without a replacement refresh
// token. Credential construction carries the submitted refresh token into
// that result when the returned value is absent or whitespace, so saving the
// response retains the credential needed for a later refresh. Numeric
// expiration values become integer strings for storage.

// useAntigravityOAuth exposes Antigravity authorization state and credential
// builders for a provider form. Call it during component setup with an
// active application store and i18n context. URL and exchange operations
// show global failures, while refresh validation leaves feedback local for
// batch callers. Callers retain the submitted refresh token and pass it to
// buildCredentials when saving a validation response, because the response
// may omit a replacement credential.
export function useAntigravityOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()

  // Authorization refs belong to this form instance. Request helpers update
  // loading and error for the form, and URL generation publishes the session
  // fields used for exchange.
  const authUrl = ref('') // Displayed authorization URL.
  const sessionId = ref('') // Current backend session.
  const state = ref('') // Current callback state.
  const loading = ref(false) // Request activity indicator.
  const error = ref('') // Current form feedback.

  // resetState clears authorization fields and the current request feedback.
  // Callers use it when resetting the form; pending HTTP requests continue
  // and may subsequently publish their results.
  const resetState = () => {
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    loading.value = false
    error.value = ''
  }

  // generateAuthUrl requests a fresh authorization URL and replaces the
  // displayed session. A truthy proxy identifier is forwarded; request
  // failures update local feedback, show an error toast, and return false.
  const generateAuthUrl = async (proxyId: number | null | undefined): Promise<boolean> => {
    loading.value = true
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    error.value = ''

    try {
      const payload: Record<string, unknown> = {}
      if (proxyId) payload.proxy_id = proxyId

      const response = await adminAPI.antigravity.generateAuthUrl(payload as any)
      authUrl.value = response.auth_url
      sessionId.value = response.session_id
      state.value = response.state
      return true
    } catch (err: any) {
      // Prefer the generation response detail or unwrapped API message when
      // available; fall back to the localized generation error text.
      error.value =
        err.response?.data?.detail ||
        err.response?.data?.message ||
        err.message ||
        t('admin.providers.oauth.antigravity.failedToGenerateUrl')
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  // exchangeAuthCode submits the supplied code, session identifier, state, and
  // optional GCP Project ID for tokens. Missing required input returns null
  // with local feedback; request failures surface server diagnostics in place
  // so operators can enter a Project ID and retry without re-logging in.
  const exchangeAuthCode = async (params: {
    code: string // Pasted authorization code.
    sessionId: string // Backend session identifier.
    state: string // Callback correlation state.
    projectId?: string | null // Optional GCP Project ID override.
    proxyId?: number | null // Selected proxy identifier.
  }): Promise<AntigravityTokenInfo | null> => {
    const code = params.code?.trim()
    if (!code || !params.sessionId || !params.state) {
      error.value = t('admin.providers.oauth.antigravity.missingExchangeParams')
      return null
    }

    loading.value = true
    error.value = ''

    try {
      const payload: Record<string, unknown> = {
        session_id: params.sessionId,
        state: params.state,
        code,
      }
      const trimmedProjectID = params.projectId?.trim()
      if (trimmedProjectID) payload.project_id = trimmedProjectID
      if (params.proxyId) payload.proxy_id = params.proxyId

      const tokenInfo = await adminAPI.antigravity.exchangeCode(payload as any)
      return tokenInfo as AntigravityTokenInfo
    } catch (err: any) {
      // A rejected exchange keeps the entered session available for
      // correction. Server diagnostics take precedence over the generic toast.
      error.value =
        err.response?.data?.detail ||
        err.response?.data?.message ||
        err.message ||
        t('admin.providers.oauth.antigravity.failedToExchangeCode')
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  // validateRefreshToken exchanges a trimmed refresh token and optional GCP
  // Project ID for Antigravity token information. Blank input or request
  // failure returns null and updates local feedback; batch callers display
  // that feedback without a global toast.
  const validateRefreshToken = async (
    refreshToken: string,
    proxyId?: number | null,
    projectId?: string | null,
  ): Promise<AntigravityTokenInfo | null> => {
    if (!refreshToken.trim()) {
      error.value = t('admin.providers.oauth.antigravity.pleaseEnterRefreshToken')
      return null
    }

    loading.value = true
    error.value = ''

    try {
      const tokenInfo = await adminAPI.antigravity.refreshAntigravityToken(
        refreshToken.trim(),
        proxyId,
        projectId,
      )
      return tokenInfo as AntigravityTokenInfo
    } catch (err: any) {
      // Refresh validation can run across many submitted tokens. Store each
      // failure locally and return null so batch callers decide how to
      // present individual failures.
      error.value =
        err.response?.data?.detail ||
        err.response?.data?.message ||
        err.message ||
        t('admin.providers.oauth.antigravity.failedToValidateRT')
      return null
    } finally {
      loading.value = false
    }
  }

  // buildCredentials combines returned token fields with the submitted
  // refresh credential for saving. A nonblank returned refresh token takes
  // precedence, while an absent or whitespace response uses
  // fallbackRefreshToken. Numeric expiration values are floored and
  // stringified, nonblank strings are trimmed, and other values become
  // undefined. The selected refresh token keeps its supplied text so the
  // credential record carries the actual replacement or submitted value
  // through the refresh round trip.
  const buildCredentials = (
    tokenInfo: AntigravityTokenInfo,
    fallbackRefreshToken?: string,
  ): Record<string, unknown> => {
    let expiresAt: string | undefined
    // Numeric expiration is stored as an integer string, matching the
    // credential format consumed when the provider is saved.
    if (typeof tokenInfo.expires_at === 'number' && Number.isFinite(tokenInfo.expires_at)) {
      expiresAt = Math.floor(tokenInfo.expires_at).toString()
    } else if (typeof tokenInfo.expires_at === 'string' && tokenInfo.expires_at.trim()) {
      expiresAt = tokenInfo.expires_at.trim()
    }
    // A refresh response can omit rotation. Carry the submitted credential
    // forward so saving the validated token retains refresh access.
    const refreshToken = tokenInfo.refresh_token?.trim()
      ? tokenInfo.refresh_token
      : fallbackRefreshToken

    const creds: Record<string, unknown> = {
      access_token: tokenInfo.access_token,
      refresh_token: refreshToken,
      token_type: tokenInfo.token_type,
      expires_at: expiresAt,
      project_id: tokenInfo.project_id,
      email: tokenInfo.email,
    }
    if (typeof tokenInfo.plan_type === 'string' && tokenInfo.plan_type.trim()) {
      creds.plan_type = tokenInfo.plan_type.trim()
    }
    return creds
  }

  // buildExtraInfo extracts optional privacy metadata returned during token
  // exchange or refresh validation. It returns undefined when no metadata is
  // present so callers can omit empty extra patches.
  const buildExtraInfo = (tokenInfo: AntigravityTokenInfo): Record<string, string> | undefined => {
    const extra: Record<string, string> = {}
    if (typeof tokenInfo.privacy_mode === 'string' && tokenInfo.privacy_mode.trim()) {
      extra.privacy_mode = tokenInfo.privacy_mode.trim()
    }
    return Object.keys(extra).length > 0 ? extra : undefined
  }

  return {
    authUrl,
    sessionId,
    state,
    loading,
    error,
    resetState,
    generateAuthUrl,
    exchangeAuthCode,
    validateRefreshToken,
    buildCredentials,
    buildExtraInfo,
  }
}
