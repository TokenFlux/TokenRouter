import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { QoderPollResponse, QoderTokenInfo } from '@/api/admin/qoder'

export function useQoderOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()

  const authUrl = ref('')
  const sessionId = ref('')
  const state = ref('')
  const pollInterval = ref(2)
  const loading = ref(false)
  const polling = ref(false)
  const error = ref('')

  const resetState = () => {
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    pollInterval.value = 2
    loading.value = false
    polling.value = false
    error.value = ''
  }

  const generateAuthUrl = async (proxyId: number | null | undefined): Promise<boolean> => {
    loading.value = true
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    error.value = ''

    try {
      const payload: Record<string, unknown> = {}
      if (proxyId) payload.proxy_id = proxyId

      const response = await adminAPI.qoder.generateAuthUrl(payload as any)
      authUrl.value = response.auth_url
      sessionId.value = response.session_id
      state.value = response.state
      pollInterval.value = response.interval || 2
      return true
    } catch (err: any) {
      error.value =
        err.response?.data?.detail ||
        err.message ||
        t('admin.accounts.oauth.qoder.failedToGenerateUrl')
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  const exchangeAuthCode = async (params: {
    code?: string
    callbackUrl?: string
    sessionId: string
    state: string
    proxyId?: number | null
  }): Promise<QoderTokenInfo | null> => {
    if (!params.sessionId || !params.state) {
      error.value = t('admin.accounts.oauth.qoder.missingExchangeParams')
      return null
    }

    loading.value = true
    error.value = ''

    try {
      const payload: Record<string, unknown> = {
        session_id: params.sessionId,
        state: params.state
      }
      const code = params.code?.trim()
      const callbackUrl = params.callbackUrl?.trim()
      if (code) payload.code = code
      if (callbackUrl) payload.callback_url = callbackUrl
      if (params.proxyId) payload.proxy_id = params.proxyId

      const tokenInfo = await adminAPI.qoder.exchangeCode(payload as any)
      return tokenInfo as QoderTokenInfo
    } catch (err: any) {
      error.value =
        err.response?.data?.detail ||
        err.message ||
        t('admin.accounts.oauth.qoder.failedToExchangeCode')
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const pollAuthorization = async (params: {
    sessionId: string
    state: string
    proxyId?: number | null
  }): Promise<QoderPollResponse | null> => {
    if (!params.sessionId || !params.state) {
      error.value = t('admin.accounts.oauth.qoder.missingExchangeParams')
      return null
    }

    polling.value = true
    loading.value = true
    error.value = ''

    try {
      const payload: Record<string, unknown> = {
        session_id: params.sessionId,
        state: params.state
      }
      if (params.proxyId) payload.proxy_id = params.proxyId

      return await adminAPI.qoder.poll(payload as any)
    } catch (err: any) {
      error.value =
        err.response?.data?.detail ||
        err.message ||
        t('admin.accounts.oauth.qoder.failedToExchangeCode')
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
      polling.value = false
    }
  }

  const buildCredentials = (tokenInfo: QoderTokenInfo): Record<string, unknown> => ({
    security_oauth_token: tokenInfo.security_oauth_token,
    refresh_token: tokenInfo.refresh_token,
    machine_id: tokenInfo.machine_id,
    machine_token: tokenInfo.machine_token,
    machine_type: tokenInfo.machine_type,
    uid: tokenInfo.uid,
    aid: tokenInfo.aid,
    organization_id: tokenInfo.organization_id,
    organization_name: tokenInfo.organization_name,
    name: tokenInfo.name,
    user_type: tokenInfo.user_type,
    extra: tokenInfo.extra
  })

  return {
    authUrl,
    sessionId,
    state,
    pollInterval,
    loading,
    polling,
    error,
    resetState,
    generateAuthUrl,
    exchangeAuthCode,
    pollAuthorization,
    buildCredentials
  }
}
