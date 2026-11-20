import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { GeminiOAuthCapabilities } from '@/api/admin/gemini'

// useGeminiOAuth.ts prepares Gemini authorization requests and provider
// credential records. Project, OAuth type, and tier selections accompany the
// supported request stages, while expiration values are normalized for
// storage and optional capability lookup failures leave capability data
// unavailable.

// GeminiTokenInfo represents token and account fields returned by Gemini
// authorization.
export interface GeminiTokenInfo {
  access_token?: string // API access credential.
  refresh_token?: string // Token refresh credential.
  token_type?: string // Access token scheme.
  scope?: string // Granted permission scopes.
  // expires_at uses Unix seconds when numeric. buildCredentials also accepts nonblank
  // strings, trimming and storing them without checking that they encode a timestamp.
  expires_at?: number | string // Credential expiration timestamp.
  project_id?: string // Selected cloud project.
  oauth_type?: string // Selected OAuth mode.
  tier_id?: string // Selected subscription tier.
  // extra carries response metadata whose object reference is returned
  // directly by buildExtraInfo for the caller to use.
  extra?: Record<string, unknown> // Additional response metadata.
  [key: string]: unknown // Additional response fields.
}

// useGeminiOAuth exposes reactive authorization fields and request helpers
// for the provider form. Call it during component setup with an active
// application store and i18n context. The caller saves returned token
// information.
export function useGeminiOAuth() {
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

  // generateAuthUrl starts Gemini authorization with optional project, type,
  // and tier selections. Project and tier identifiers are trimmed before
  // submission; failure shows local and global feedback and returns false.
  const generateAuthUrl = async (
    proxyId: number | null | undefined,
    projectId?: string | null,
    oauthType?: string,
    tierId?: string,
  ): Promise<boolean> => {
    loading.value = true
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    error.value = ''

    try {
      const payload: Record<string, unknown> = {}
      if (proxyId) payload.proxy_id = proxyId
      const trimmedProjectID = projectId?.trim()
      if (trimmedProjectID) payload.project_id = trimmedProjectID
      if (oauthType) payload.oauth_type = oauthType
      const trimmedTierID = tierId?.trim()
      if (trimmedTierID) payload.tier_id = trimmedTierID

      const response = await adminAPI.gemini.generateAuthUrl(payload as any)
      authUrl.value = response.auth_url
      sessionId.value = response.session_id
      state.value = response.state
      return true
    } catch (err: any) {
      // Prefer server detail or unwrapped API error message for URL-generation
      // failures; fall back to the localized generation message when absent.
      error.value =
        err.response?.data?.detail ||
        err.response?.data?.message ||
        err.message ||
        t('admin.providers.oauth.gemini.failedToGenerateUrl')
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  // exchangeAuthCode submits the supplied code, session identifier, state, and
  // optional Project ID override for tokens. Missing required input returns
  // null with local feedback; request failures surface structured server
  // diagnostics (activation URL, verification URL, or no-relogin Project ID
  // prompt) and show a toast before clearing loading.
  const exchangeAuthCode = async (params: {
    code: string // Pasted authorization code.
    sessionId: string // Backend session identifier.
    state: string // Callback correlation state.
    projectId?: string | null // Optional GCP Project ID override.
    proxyId?: number | null // Selected proxy identifier.
    oauthType?: string // Selected OAuth mode.
    tierId?: string // Selected subscription tier.
  }): Promise<GeminiTokenInfo | null> => {
    const code = params.code?.trim()
    if (!code || !params.sessionId || !params.state) {
      error.value = t('admin.providers.oauth.gemini.missingExchangeParams')
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
      if (params.oauthType) payload.oauth_type = params.oauthType
      const trimmedTierID = params.tierId?.trim()
      if (trimmedTierID) payload.tier_id = trimmedTierID

      const tokenInfo = await adminAPI.gemini.exchangeCode(payload as any)
      return tokenInfo as GeminiTokenInfo
    } catch (err: any) {
      // Preserve specific server diagnostic messages (activation links,
      // verification URLs, and same-session Project ID guidance) when present.
      const errorMessage =
        err.response?.data?.detail || err.response?.data?.message || err.message || ''
      error.value = errorMessage || t('admin.providers.oauth.gemini.failedToExchangeCode')
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  // buildCredentials selects Gemini token fields for provider credential
  // storage. Finite numeric expiration values become integer strings,
  // nonblank string values are trimmed, and unsupported expiration values
  // become undefined.
  const buildCredentials = (tokenInfo: GeminiTokenInfo): Record<string, unknown> => {
    let expiresAt: string | undefined
    // Numeric expiration is stored as an integer string, matching the
    // credential format consumed when the provider is saved.
    if (typeof tokenInfo.expires_at === 'number' && Number.isFinite(tokenInfo.expires_at)) {
      expiresAt = Math.floor(tokenInfo.expires_at).toString()
    } else if (typeof tokenInfo.expires_at === 'string' && tokenInfo.expires_at.trim()) {
      expiresAt = tokenInfo.expires_at.trim()
    }

    return {
      access_token: tokenInfo.access_token,
      refresh_token: tokenInfo.refresh_token,
      token_type: tokenInfo.token_type,
      expires_at: expiresAt,
      scope: tokenInfo.scope,
      project_id: tokenInfo.project_id,
      oauth_type: tokenInfo.oauth_type,
      tier_id: tokenInfo.tier_id,
    }
  }

  // buildExtraInfo returns the response extra object when it is present and
  // object-valued. The result shares the response reference, and absent or
  // primitive extra values produce undefined.
  const buildExtraInfo = (tokenInfo: GeminiTokenInfo): Record<string, unknown> | undefined => {
    if (!tokenInfo.extra || typeof tokenInfo.extra !== 'object') return undefined
    return tokenInfo.extra
  }

  // getCapabilities retrieves the optional Gemini OAuth capability response
  // for form configuration. Request failure returns null so callers can
  // continue displaying the form when capability discovery is unavailable.
  const getCapabilities = async (): Promise<GeminiOAuthCapabilities | null> => {
    try {
      return await adminAPI.gemini.getCapabilities()
    } catch {
      // Capability discovery is optional for form display. Treat an
      // unavailable endpoint as unknown capabilities so authorization remains
      // accessible.
      return null
    }
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
    buildCredentials,
    buildExtraInfo,
    getCapabilities,
  }
}
