<template>
  <BaseDialog
    :show="show"
    :title="t('admin.providers.reAuthorizeProvider')"
    width="normal"
    @close="handleClose"
  >
    <div v-if="provider" class="space-y-4">
      <!-- Provider Info -->
      <div
        class="rounded-surface border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800"
      >
        <div class="flex items-center gap-3">
          <div class="flex h-10 w-10 shrink-0 items-center justify-center">
            <!-- Antigravity uses the existing platform glyph because the shared brand catalog does not provide artwork for this platform. -->
            <PlatformIcon
              v-if="isAntigravity"
              :platform="provider.platform"
              size="lg"
              class="text-gray-500 dark:text-dark-400"
              aria-hidden="true"
            />
            <ProviderIcon v-else :brand="provider.platform" size="22px" />
          </div>
          <div>
            <span class="block font-semibold text-gray-900 dark:text-white">{{
              provider.name
            }}</span>
            <span class="text-sm text-gray-500 dark:text-gray-400">
              {{
                isOpenAI
                  ? t('admin.providers.openaiProvider')
                  : isGemini
                    ? t('admin.providers.geminiProvider')
                    : isAntigravity
                      ? t('admin.providers.antigravityProvider')
                      : isGrok
                        ? t('admin.providers.grokProvider')
                        : t('admin.providers.claudeCodeProvider')
              }}
            </span>
          </div>
        </div>
      </div>

      <!-- Add Method Selection (Claude only) -->
      <fieldset v-if="isAnthropic" class="border-0 p-0">
        <legend class="input-label">{{ t('admin.providers.oauth.authMethod') }}</legend>
        <div class="mt-2 flex gap-4">
          <label class="flex cursor-pointer items-center">
            <input
              v-model="addMethod"
              type="radio"
              value="oauth"
              class="mr-2 text-primary-600 focus:ring-primary-500"
            />
            <span class="text-sm text-gray-700 dark:text-gray-300">{{
              t('admin.providers.types.oauth')
            }}</span>
          </label>
          <label class="flex cursor-pointer items-center">
            <input
              v-model="addMethod"
              type="radio"
              value="setup-token"
              class="mr-2 text-primary-600 focus:ring-primary-500"
            />
            <span class="text-sm text-gray-700 dark:text-gray-300">{{
              t('admin.providers.setupTokenLongLived')
            }}</span>
          </label>
        </div>
      </fieldset>

      <!-- Gemini OAuth Type Display (read-only) -->
      <div
        v-if="isGemini"
        class="rounded-surface border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800"
      >
        <div class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">
          {{ t('admin.providers.oauth.gemini.oauthTypeLabel') }}
        </div>
        <div class="flex items-center gap-3">
          <div
            :class="[
              'flex h-8 w-8 shrink-0 items-center justify-center rounded-control',
              geminiOAuthType === 'google_one'
                ? 'bg-purple-500 text-white'
                : geminiOAuthType === 'code_assist'
                  ? 'bg-blue-500 text-white'
                  : 'bg-amber-500 text-white',
            ]"
          >
            <Icon v-if="geminiOAuthType === 'google_one'" name="user" size="sm" />
            <Icon v-else-if="geminiOAuthType === 'code_assist'" name="cloud" size="sm" />
            <Icon v-else name="sparkles" size="sm" />
          </div>
          <div>
            <span class="block text-sm font-medium text-gray-900 dark:text-white">
              {{
                geminiOAuthType === 'google_one'
                  ? 'Google One'
                  : geminiOAuthType === 'code_assist'
                    ? t('admin.providers.gemini.oauthType.builtInTitle')
                    : t('admin.providers.gemini.oauthType.customTitle')
              }}
            </span>
            <span class="text-xs text-gray-500 dark:text-gray-400">
              {{
                geminiOAuthType === 'google_one'
                  ? '个人提供商'
                  : geminiOAuthType === 'code_assist'
                    ? t('admin.providers.gemini.oauthType.builtInDesc')
                    : t('admin.providers.gemini.oauthType.customDesc')
              }}
            </span>
          </div>
        </div>
      </div>

      <OAuthAuthorizationFlow
        ref="oauthFlowRef"
        :add-method="addMethod"
        :auth-url="currentAuthUrl"
        :session-id="currentSessionId"
        :loading="currentLoading"
        :error="currentError"
        :show-help="isAnthropic"
        :show-proxy-warning="isAnthropic"
        :show-cookie-option="isAnthropic"
        :show-refresh-token-option="isOpenAI || isAntigravity || isGrok"
        :show-sso-option="isGrok"
        :show-email-password-option="false"
        :allow-multiple="false"
        :method-label="t('admin.providers.inputMethod')"
        :platform="
          isOpenAI
            ? 'openai'
            : isGemini
              ? 'gemini'
              : isAntigravity
                ? 'antigravity'
                : isGrok
                  ? 'grok'
                  : 'anthropic'
        "
        :show-project-id="isGemini && geminiOAuthType === 'code_assist'"
        :initial-input-method="grokInitialInputMethod"
        @generate-url="handleGenerateUrl"
        @cookie-auth="handleCookieAuth"
        @validate-refresh-token="handleValidateRefreshToken"
        @import-sso="handleGrokImportSSO"
      />
    </div>

    <template #footer>
      <div v-if="provider" class="flex justify-between gap-3">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          v-if="isManualInputMethod"
          type="button"
          :disabled="!canExchangeCode"
          class="btn btn-primary"
          @click="handleExchangeCode"
        >
          <Icon
            name="loader"
            size="sm"
            :animate-on-hover="false"
            v-if="currentLoading"
            class="-ml-1 mr-2 h-4 w-4 animate-spin"
          />
          {{
            currentLoading
              ? t('admin.providers.oauth.verifying')
              : t('admin.providers.oauth.completeAuth')
          }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import {
  useProviderOAuth,
  type AddMethod,
  type AuthInputMethod,
} from '@/composables/useProviderOAuth'
import { useOpenAIOAuth } from '@/composables/useOpenAIOAuth'
import { useGeminiOAuth } from '@/composables/useGeminiOAuth'
import { useAntigravityOAuth } from '@/composables/useAntigravityOAuth'
import { useGrokOAuth } from '@/composables/useGrokOAuth'
import type { Provider } from '@/api/admin/providers.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import OAuthAuthorizationFlow from '@/components/provider/OAuthAuthorizationFlow.vue'

/**
 * ReAuthProviderModal.vue reauthorizes an existing provider through its platform-specific
 * OAuth, cookie, SSO, or refresh-token flow. The embedded authorization component exposes
 * unwrapped input values, while dedicated composables own each platform's session state,
 * credential building, and error feedback. Reopening initializes authentication choices from
 * the current provider record.
 *
 * Exchanged authorization codes, Claude session cookies, Grok SSO cookies, and refresh tokens
 * persist through applyOAuthCredentials in a single request. That endpoint merges the new
 * credentials and extra metadata into the existing provider record, clears error state, and
 * invalidates cached tokens without overwriting unrelated fields such as model mappings or TLS
 * router bindings. Parent notification and dialog closure follow successful persistence.
 */

/**
 * OAuthFlowExposed describes the unwrapped fields and reset method exposed by the embedded
 * authorization form.
 */
interface OAuthFlowExposed {
  authCode: string // entered authorization code text
  oauthState: string // entered callback state text
  projectId: string // entered Code Assist project
  sessionKey: string // entered Claude session cookie
  inputMethod: AuthInputMethod // selected authorization input method
  /**
   * Clears the embedded authorization inputs when the owning dialog resets its platform session
   * state.
   */
  reset: () => void
}

/**
 * Props supplies dialog visibility and the existing provider selected for reauthorization.
 */
interface Props {
  show: boolean // parent-controlled reauthorization dialog visibility
  provider: Provider | null // selected provider or no selection
}

/**
 * props supplies the existing provider and parent-controlled visibility; the form emits a
 * persisted provider after successful reauthorization.
 */
const props = defineProps<Props>()
/**
 * emit reports completion and closure separately so the provider page can update its row before
 * dismissing the dialog.
 */
const emit = defineEmits<{
  close: [] // request parent dialog closure
  /**
   * Emits the successfully persisted provider before closure so the parent can replace the
   * selected row.
   */
  reauthorized: [provider: Provider]
}>()

/**
 * Application toasts and translation complement the selected OAuth composable error state
 * during authorization and credential persistence operations.
 */
const appStore = useAppStore()
const { t } = useI18n()

/**
 * Platform composables retain their own authorization URL, session, loading, and error state
 * while this dialog selects the active platform view.
 */
const claudeOAuth = useProviderOAuth()
const openaiOAuth = useOpenAIOAuth()
const geminiOAuth = useGeminiOAuth()
const antigravityOAuth = useAntigravityOAuth()
const grokOAuth = useGrokOAuth()

/**
 * oauthFlowRef exposes unwrapped embedded input values; authentication choices initialize from
 * the saved provider type or Gemini OAuth channel when opened.
 */
const oauthFlowRef = ref<OAuthFlowExposed | null>(null)

/**
 * addMethod and geminiOAuthType retain the selected authorization variant used by URL
 * generation, code exchange, and persisted provider credentials.
 */
const addMethod = ref<AddMethod>('oauth')
const geminiOAuthType = ref<'code_assist' | 'google_one' | 'ai_studio'>('code_assist')

/**
 * Platform predicates select OAuth composables and input capabilities, including OpenAI-like
 * behavior and the dedicated Grok token or SSO input paths.
 */
const isOpenAI = computed(() => props.provider?.platform === 'openai')
const isOpenAILike = computed(() => isOpenAI.value)
const isGemini = computed(() => props.provider?.platform === 'gemini')
const isAnthropic = computed(() => props.provider?.platform === 'anthropic')
const isAntigravity = computed(() => props.provider?.platform === 'antigravity')
const isGrok = computed(() => props.provider?.platform === 'grok')

/**
 * grokInitialInputMethod chooses refresh-token entry when credentials report an available
 * token, otherwise SSO-cookie entry; the reauthorization UI keeps password entry hidden.
 */
const grokInitialInputMethod = computed<AuthInputMethod>(() => {
  if (!isGrok.value) return 'manual'
  const creds = (props.provider?.credentials || {}) as Record<string, unknown>
  const hasRT =
    (typeof creds.refresh_token === 'string' && creds.refresh_token.trim() !== '') ||
    (typeof creds.has_refresh_token === 'boolean' && creds.has_refresh_token)
  if (hasRT) return 'refresh_token'
  return 'sso_cookie'
})

/**
 * Current authorization URL, session, loading, and error values delegate to the active platform
 * composable for the shared authorization form.
 */
const currentAuthUrl = computed(() => {
  if (isOpenAILike.value) return openaiOAuth.authUrl.value
  if (isGemini.value) return geminiOAuth.authUrl.value
  if (isAntigravity.value) return antigravityOAuth.authUrl.value
  if (isGrok.value) return grokOAuth.authUrl.value
  return claudeOAuth.authUrl.value
})
const currentSessionId = computed(() => {
  if (isOpenAILike.value) return openaiOAuth.sessionId.value
  if (isGemini.value) return geminiOAuth.sessionId.value
  if (isAntigravity.value) return antigravityOAuth.sessionId.value
  if (isGrok.value) return grokOAuth.sessionId.value
  return claudeOAuth.sessionId.value
})
const currentLoading = computed(() => {
  if (isOpenAILike.value) return openaiOAuth.loading.value
  if (isGemini.value) return geminiOAuth.loading.value
  if (isAntigravity.value) return antigravityOAuth.loading.value
  if (isGrok.value) return grokOAuth.loading.value
  return claudeOAuth.loading.value
})
const currentError = computed(() => {
  if (isOpenAILike.value) return openaiOAuth.error.value
  if (isGemini.value) return geminiOAuth.error.value
  if (isAntigravity.value) return antigravityOAuth.error.value
  if (isGrok.value) return grokOAuth.error.value
  return claudeOAuth.error.value
})

/**
 * isManualInputMethod shows the footer exchange action for authorization-code entry and
 * excludes SSO, password, and refresh-token handlers that submit through separate callbacks.
 */
const isManualInputMethod = computed(() => {
  const method = oauthFlowRef.value?.inputMethod
  if (method === 'sso_cookie' || method === 'email_password' || method === 'refresh_token') {
    return false
  }
  // These platform flows use the footer for manual authorization-code exchange; token and SSO
  // methods have already returned through their separate handlers.
  return (
    isOpenAILike.value ||
    isGemini.value ||
    isAntigravity.value ||
    isGrok.value ||
    method === 'manual'
  )
})

/**
 * canExchangeCode requires nonblank authorization code, a current session, and an idle platform
 * flow before enabling the footer exchange action.
 */
const canExchangeCode = computed(() => {
  const authCode = oauthFlowRef.value?.authCode || ''
  const sessionId = currentSessionId.value
  const loading = currentLoading.value
  return authCode.trim() && sessionId && !loading
})

// Visibility initializes the provider-specific authentication choices on open and clears shared
// inputs plus all platform session state when hidden.
watch(
  () => props.show,
  (newVal) => {
    if (newVal && props.provider) {
      // Claude reauthorization retains the existing OAuth or setup-token variant so an ordinary
      // reauthorization starts with the saved provider type.
      if (
        isAnthropic.value &&
        (props.provider.type === 'oauth' || props.provider.type === 'setup-token')
      ) {
        addMethod.value = props.provider.type as AddMethod
      }
      if (isGemini.value) {
        const creds = (props.provider.credentials || {}) as Record<string, unknown>
        geminiOAuthType.value =
          creds.oauth_type === 'google_one'
            ? 'google_one'
            : creds.oauth_type === 'ai_studio'
              ? 'ai_studio'
              : 'code_assist'
      }
    } else {
      resetState()
    }
  },
)

/**
 * resetState restores authentication choices, resets every platform composable, and clears the
 * embedded input form. It runs when the dialog hides; request cancellation behavior remains
 * with the platform composables.
 */
const resetState = () => {
  addMethod.value = 'oauth'
  geminiOAuthType.value = 'code_assist'
  claudeOAuth.resetState()
  openaiOAuth.resetState()
  geminiOAuth.resetState()
  antigravityOAuth.resetState()
  grokOAuth.resetState()
  oauthFlowRef.value?.reset()
}

/**
 * handleClose requests parent-controlled dismissal of the provider reauthorization dialog.
 */
const handleClose = () => {
  emit('close')
}

/**
 * handleGenerateUrl delegates authorization URL generation to the current platform flow using
 * the provider proxy. Gemini additionally carries the selected OAuth channel, saved tier, and
 * Code Assist project input when applicable.
 */
const handleGenerateUrl = async () => {
  if (!props.provider) return

  if (isOpenAILike.value) {
    await openaiOAuth.generateAuthUrl(props.provider.proxy_id)
  } else if (isGemini.value) {
    const creds = (props.provider.credentials || {}) as Record<string, unknown>
    const tierId = typeof creds.tier_id === 'string' ? creds.tier_id : undefined
    const projectId =
      geminiOAuthType.value === 'code_assist' ? oauthFlowRef.value?.projectId : undefined
    await geminiOAuth.generateAuthUrl(
      props.provider.proxy_id,
      projectId,
      geminiOAuthType.value,
      tierId,
    )
  } else if (isAntigravity.value) {
    await antigravityOAuth.generateAuthUrl(props.provider.proxy_id)
  } else if (isGrok.value) {
    await grokOAuth.generateAuthUrl(props.provider.proxy_id)
  } else {
    await claudeOAuth.generateAuthUrl(addMethod.value, props.provider.proxy_id)
  }
}

/**
 * handleExchangeCode exchanges the embedded authorization code through the selected platform
 * flow and persists the resulting credentials. Empty code, missing session, or required state
 * prevents exchange. Entered state takes precedence over the composable session state where
 * that platform uses one.
 *
 * All platform branches persist through applyOAuthCredentials so new credentials and extra
 * metadata merge into the existing provider record and clear its error state in one request.
 * Success emits the returned provider before closing; failures remain in the platform error
 * state and use its corresponding feedback path.
 */
const handleExchangeCode = async () => {
  if (!props.provider) return

  const authCode = oauthFlowRef.value?.authCode || ''
  if (!authCode.trim()) return

  if (isOpenAILike.value) {
    // OpenAI OAuth flow
    const oauthClient = openaiOAuth
    const sessionId = oauthClient.sessionId.value
    if (!sessionId) return
    const stateToUse = (oauthFlowRef.value?.oauthState || oauthClient.oauthState.value || '').trim()
    if (!stateToUse) {
      oauthClient.error.value = t('admin.providers.oauth.authFailed')
      appStore.showError(oauthClient.error.value)
      return
    }

    const tokenInfo = await oauthClient.exchangeAuthCode(
      authCode.trim(),
      sessionId,
      stateToUse,
      props.provider.proxy_id,
      props.provider.tls_fingerprint_router_id,
    )
    if (!tokenInfo) return

    // Build the credential payload and extra metadata from validated token information before
    // applying the provider update through its dedicated endpoint.
    const credentials = oauthClient.buildCredentials(tokenInfo)
    const extra = oauthClient.buildExtraInfo(tokenInfo)

    try {
      const updatedProvider = await adminAPI.providers.applyOAuthCredentials(props.provider.id, {
        type: 'oauth',
        credentials,
        extra,
      })

      appStore.showSuccess(t('admin.providers.reAuthorizedSuccess'))
      emit('reauthorized', updatedProvider)
      handleClose()
    } catch (error: any) {
      oauthClient.error.value =
        error.response?.data?.detail || t('admin.providers.oauth.authFailed')
      appStore.showError(oauthClient.error.value)
    }
  } else if (isGemini.value) {
    const sessionId = geminiOAuth.sessionId.value
    if (!sessionId) return

    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || geminiOAuth.state.value
    if (!stateToUse) return

    const tokenInfo = await geminiOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId,
      state: stateToUse,
      proxyId: props.provider.proxy_id,
      oauthType: geminiOAuthType.value,
      tierId:
        typeof (props.provider.credentials as any)?.tier_id === 'string'
          ? ((props.provider.credentials as any).tier_id as string)
          : undefined,
    })
    if (!tokenInfo) return

    const credentials = geminiOAuth.buildCredentials(tokenInfo)
    const extra = geminiOAuth.buildExtraInfo(tokenInfo)

    try {
      const updatedProvider = await adminAPI.providers.applyOAuthCredentials(props.provider.id, {
        type: 'oauth',
        credentials,
        extra,
      })
      appStore.showSuccess(t('admin.providers.reAuthorizedSuccess'))
      emit('reauthorized', updatedProvider)
      handleClose()
    } catch (error: any) {
      geminiOAuth.error.value =
        error.response?.data?.detail || t('admin.providers.oauth.authFailed')
      appStore.showError(geminiOAuth.error.value)
    }
  } else if (isAntigravity.value) {
    // Antigravity OAuth flow
    const sessionId = antigravityOAuth.sessionId.value
    if (!sessionId) return

    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || antigravityOAuth.state.value
    if (!stateToUse) return

    const tokenInfo = await antigravityOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId,
      state: stateToUse,
      proxyId: props.provider.proxy_id,
    })
    if (!tokenInfo) return

    const credentials = antigravityOAuth.buildCredentials(tokenInfo)

    try {
      const updatedProvider = await adminAPI.providers.applyOAuthCredentials(props.provider.id, {
        type: 'oauth',
        credentials,
      })
      appStore.showSuccess(t('admin.providers.reAuthorizedSuccess'))
      emit('reauthorized', updatedProvider)
      handleClose()
    } catch (error: any) {
      antigravityOAuth.error.value =
        error.response?.data?.detail || t('admin.providers.oauth.authFailed')
      appStore.showError(antigravityOAuth.error.value)
    }
  } else if (isGrok.value) {
    const sessionId = grokOAuth.sessionId.value
    if (!sessionId) return

    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || grokOAuth.state.value
    if (!stateToUse) return

    const tokenInfo = await grokOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId,
      state: stateToUse,
      proxyId: props.provider.proxy_id,
    })
    if (!tokenInfo) return

    const credentials = grokOAuth.buildCredentials(tokenInfo)
    const extra = grokOAuth.buildExtraInfo(tokenInfo)

    try {
      const updatedProvider = await adminAPI.providers.applyOAuthCredentials(props.provider.id, {
        type: 'oauth',
        credentials,
        extra,
      })

      appStore.showSuccess(t('admin.providers.reAuthorizedSuccess'))
      emit('reauthorized', updatedProvider)
      handleClose()
    } catch (error: any) {
      grokOAuth.error.value = error.response?.data?.detail || t('admin.providers.oauth.authFailed')
      appStore.showError(grokOAuth.error.value)
    }
  } else {
    // Claude OAuth flow
    const sessionId = claudeOAuth.sessionId.value
    if (!sessionId) return

    claudeOAuth.loading.value = true
    claudeOAuth.error.value = ''

    try {
      const proxyConfig = props.provider.proxy_id ? { proxy_id: props.provider.proxy_id } : {}
      const endpoint =
        addMethod.value === 'oauth'
          ? '/admin/providers/exchange-code'
          : '/admin/providers/exchange-setup-token-code'

      const tokenInfo = await adminAPI.providers.exchangeCode(endpoint, {
        session_id: sessionId,
        code: authCode.trim(),
        ...proxyConfig,
      })

      const extra = claudeOAuth.buildExtraInfo(tokenInfo)

      const updatedProvider = await adminAPI.providers.applyOAuthCredentials(props.provider.id, {
        type: addMethod.value,
        credentials: tokenInfo,
        extra,
      })

      appStore.showSuccess(t('admin.providers.reAuthorizedSuccess'))
      emit('reauthorized', updatedProvider)
      handleClose()
    } catch (error: any) {
      claudeOAuth.error.value =
        error.response?.data?.detail || t('admin.providers.oauth.authFailed')
      appStore.showError(claudeOAuth.error.value)
    } finally {
      claudeOAuth.loading.value = false
    }
  }
}

/**
 * handleCookieAuth exchanges a trimmed Claude session key for the selected OAuth or setup-token
 * method when a provider is selected and is not OpenAI-like. It stores the returned tokenInfo
 * directly as credentials and builds extra metadata through the Claude composable.
 *
 * Persistence calls applyOAuthCredentials so the exchanged credentials and extra metadata merge
 * into the existing provider record and clear its error state in one request. Failures populate
 * the Claude error state and leave the dialog open. Successful completion emits the recovered
 * provider before closing, and the loading flag resets after either outcome.
 */
const handleCookieAuth = async (sessionKey: string) => {
  if (!props.provider || isOpenAILike.value) return

  claudeOAuth.loading.value = true
  claudeOAuth.error.value = ''

  try {
    const proxyConfig = props.provider.proxy_id ? { proxy_id: props.provider.proxy_id } : {}
    const endpoint =
      addMethod.value === 'oauth'
        ? '/admin/providers/cookie-auth'
        : '/admin/providers/setup-token-cookie-auth'

    const tokenInfo = await adminAPI.providers.exchangeCode(endpoint, {
      session_id: '',
      code: sessionKey.trim(),
      ...proxyConfig,
    })

    const extra = claudeOAuth.buildExtraInfo(tokenInfo)

    const updatedProvider = await adminAPI.providers.applyOAuthCredentials(props.provider.id, {
      type: addMethod.value,
      credentials: tokenInfo,
      extra,
    })

    appStore.showSuccess(t('admin.providers.reAuthorizedSuccess'))
    emit('reauthorized', updatedProvider)
    handleClose()
  } catch (error: any) {
    claudeOAuth.error.value =
      error.response?.data?.detail || t('admin.providers.oauth.cookieAuthFailed')
  } finally {
    claudeOAuth.loading.value = false
  }
}

/**
 * applyGrokReauthTokenInfo builds Grok credential and extra fields from validated token
 * information and applies them to the selected provider. Successful persistence emits the
 * updated record before closing; request errors propagate to the input handler.
 */
const applyGrokReauthTokenInfo = async (tokenInfo: {
  access_token?: string // validated OAuth access token
  refresh_token?: string // validated OAuth refresh token
  email?: string // validated account email address
  [key: string]: unknown
}) => {
  if (!props.provider) return
  const credentials = grokOAuth.buildCredentials(tokenInfo as any)
  const extra = grokOAuth.buildExtraInfo(tokenInfo as any)
  const updatedProvider = await adminAPI.providers.applyOAuthCredentials(props.provider.id, {
    type: 'oauth',
    credentials,
    extra,
  })
  appStore.showSuccess(t('admin.providers.reAuthorizedSuccess'))
  emit('reauthorized', updatedProvider)
  handleClose()
}

/**
 * handleValidateRefreshToken uses the first nonempty input line for one existing provider. Grok
 * delegates to its dedicated handler; OpenAI and Antigravity validate through their own
 * builders before applying credentials and reporting success.
 */
const handleValidateRefreshToken = async (refreshTokenInput: string) => {
  if (!props.provider) return
  if (isGrok.value) {
    await handleGrokValidateRefreshToken(refreshTokenInput)
    return
  }

  const refreshToken = refreshTokenInput
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)[0]
  if (!refreshToken) return

  if (isOpenAILike.value) {
    openaiOAuth.loading.value = true
    openaiOAuth.error.value = ''
    try {
      const tokenInfo = await openaiOAuth.validateRefreshToken(
        refreshToken,
        props.provider.proxy_id,
      )
      if (!tokenInfo) return

      const updatedProvider = await adminAPI.providers.applyOAuthCredentials(props.provider.id, {
        type: 'oauth',
        credentials: openaiOAuth.buildCredentials(tokenInfo),
        extra: openaiOAuth.buildExtraInfo(tokenInfo),
      })
      appStore.showSuccess(t('admin.providers.reAuthorizedSuccess'))
      emit('reauthorized', updatedProvider)
      handleClose()
    } catch (error: any) {
      openaiOAuth.error.value =
        error.response?.data?.detail ||
        error.response?.data?.message ||
        error.message ||
        t('admin.providers.oauth.authFailed')
      appStore.showError(openaiOAuth.error.value)
    } finally {
      openaiOAuth.loading.value = false
    }
    return
  }

  if (!isAntigravity.value) return
  antigravityOAuth.loading.value = true
  antigravityOAuth.error.value = ''
  try {
    const tokenInfo = await antigravityOAuth.validateRefreshToken(
      refreshToken,
      props.provider.proxy_id,
    )
    if (!tokenInfo) return

    const updatedProvider = await adminAPI.providers.applyOAuthCredentials(props.provider.id, {
      type: 'oauth',
      credentials: antigravityOAuth.buildCredentials(tokenInfo, refreshToken),
    })
    appStore.showSuccess(t('admin.providers.reAuthorizedSuccess'))
    emit('reauthorized', updatedProvider)
    handleClose()
  } catch (error: any) {
    antigravityOAuth.error.value =
      error.response?.data?.detail ||
      error.response?.data?.message ||
      error.message ||
      t('admin.providers.oauth.authFailed')
    appStore.showError(antigravityOAuth.error.value)
  } finally {
    antigravityOAuth.loading.value = false
  }
}

/**
 * handleGrokImportSSO validates the first nonempty SSO input line for a Grok provider, then
 * applies the returned OAuth tokens. Empty input or another platform does nothing; validation
 * and persistence errors remain visible.
 */
const handleGrokImportSSO = async (ssoInput: string) => {
  if (!props.provider || !isGrok.value) return
  const ssoToken = ssoInput
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)[0]
  if (!ssoToken) return

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''
  try {
    const tokenInfo = await grokOAuth.validateSSOToken(ssoToken, props.provider.proxy_id)
    if (!tokenInfo) return
    await applyGrokReauthTokenInfo(tokenInfo)
  } catch (error: any) {
    grokOAuth.error.value =
      error.response?.data?.detail ||
      error.message ||
      t('admin.providers.oauth.grok.failedToValidateSSO', 'Failed to validate Grok SSO')
    appStore.showError(grokOAuth.error.value)
  } finally {
    grokOAuth.loading.value = false
  }
}

/**
 * handleGrokValidateRefreshToken validates the first nonempty refresh-token line for the
 * current Grok provider. Empty input sets a localized error; successful validation applies
 * built credentials, and request activity clears after settlement.
 */
const handleGrokValidateRefreshToken = async (refreshTokenInput: string) => {
  if (!props.provider || !isGrok.value) return
  const refreshToken = refreshTokenInput
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)[0]
  if (!refreshToken) {
    grokOAuth.error.value = t('admin.providers.oauth.grok.pleaseEnterRefreshToken')
    return
  }

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''
  try {
    const tokenInfo = await grokOAuth.validateRefreshToken(refreshToken, props.provider.proxy_id)
    if (!tokenInfo) return
    await applyGrokReauthTokenInfo(tokenInfo)
  } catch (error: any) {
    grokOAuth.error.value =
      error.response?.data?.detail ||
      error.message ||
      t('admin.providers.oauth.grok.failedToValidateRT')
    appStore.showError(grokOAuth.error.value)
  } finally {
    grokOAuth.loading.value = false
  }
}
</script>
