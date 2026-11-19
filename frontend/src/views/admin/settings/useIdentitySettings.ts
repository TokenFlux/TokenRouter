import { computed, watch } from 'vue'
import { type SettingsSegmentedOption } from '@/components/common/settings/SettingsSegmented.vue'
import { useI18n } from 'vue-i18n'
import {
  defaultWeChatConnectScopesForMode,
  deriveWeChatConnectStoredMode,
  resolveWeChatConnectModeCapabilities,
} from './settingsFormHelpers'
import type { WeChatConnectMode } from '@/api/admin/settings.types'
import { useClipboard } from '@/composables/useClipboard'
import type { SettingsForm } from './settingsState'
import { currentOrigin } from './settingsDefaults'
import { buildApiUrl } from '@/api/url'

/**
 * useIdentitySettings.ts manages callback URL suggestions and provider-specific
 * controls for administrator identity settings. Suggestions use the browser API
 * entry that initiates OAuth, while Google One Tap uses the browser origin.
 * Copy actions first write callback suggestions into the shared draft,
 * so clipboard feedback does not decide whether the field changes.
 *
 * WeChat exposes separate capability switches alongside a stored mode and scopes.
 * Its handlers reconcile mutually exclusive public-account and mobile capabilities
 * before deriving those stored values. A setup-scoped watcher clears DingTalk
 * registration bypass and identity synchronization switches when the enterprise-only
 * policy is left, keeping the hidden controls consistent with the next bulk save.
 */

/**
 * IdentitySettingsForm selects provider credentials and controls edited by the
 * identity panel.
 */
export type IdentitySettingsForm = Pick<
  SettingsForm,
  | 'dingtalk_connect_bypass_registration'
  | 'dingtalk_connect_client_id'
  | 'dingtalk_connect_client_secret'
  | 'dingtalk_connect_client_secret_configured'
  | 'dingtalk_connect_corp_restriction_policy'
  | 'dingtalk_connect_enabled'
  | 'dingtalk_connect_redirect_url'
  | 'dingtalk_connect_sync_corp_email'
  | 'dingtalk_connect_sync_corp_email_attr_key'
  | 'dingtalk_connect_sync_corp_email_attr_name'
  | 'dingtalk_connect_sync_dept'
  | 'dingtalk_connect_sync_dept_attr_key'
  | 'dingtalk_connect_sync_dept_attr_name'
  | 'dingtalk_connect_sync_display_name'
  | 'dingtalk_connect_sync_display_name_attr_key'
  | 'dingtalk_connect_sync_display_name_attr_name'
  | 'github_oauth_client_id'
  | 'github_oauth_client_secret'
  | 'github_oauth_client_secret_configured'
  | 'github_oauth_enabled'
  | 'github_oauth_frontend_redirect_url'
  | 'github_oauth_redirect_url'
  | 'google_oauth_client_id'
  | 'google_oauth_client_secret'
  | 'google_oauth_client_secret_configured'
  | 'google_oauth_enabled'
  | 'google_oauth_frontend_redirect_url'
  | 'google_oauth_redirect_url'
  | 'google_one_tap_enabled'
  | 'linuxdo_connect_client_id'
  | 'linuxdo_connect_client_secret'
  | 'linuxdo_connect_client_secret_configured'
  | 'linuxdo_connect_enabled'
  | 'linuxdo_connect_redirect_url'
  | 'localized_settings'
  | 'oidc_connect_allowed_signing_algs'
  | 'oidc_connect_authorize_url'
  | 'oidc_connect_client_id'
  | 'oidc_connect_client_secret'
  | 'oidc_connect_client_secret_configured'
  | 'oidc_connect_clock_skew_seconds'
  | 'oidc_connect_discovery_url'
  | 'oidc_connect_enabled'
  | 'oidc_connect_frontend_redirect_url'
  | 'oidc_connect_issuer_url'
  | 'oidc_connect_jwks_url'
  | 'oidc_connect_redirect_url'
  | 'oidc_connect_require_email_verified'
  | 'oidc_connect_scopes'
  | 'oidc_connect_token_auth_method'
  | 'oidc_connect_token_url'
  | 'oidc_connect_use_pkce'
  | 'oidc_connect_userinfo_email_path'
  | 'oidc_connect_userinfo_id_path'
  | 'oidc_connect_userinfo_url'
  | 'oidc_connect_userinfo_username_path'
  | 'oidc_connect_validate_id_token'
  | 'wechat_connect_enabled'
  | 'wechat_connect_frontend_redirect_url'
  | 'wechat_connect_mobile_app_id'
  | 'wechat_connect_mobile_app_secret'
  | 'wechat_connect_mobile_app_secret_configured'
  | 'wechat_connect_mobile_enabled'
  | 'wechat_connect_mode'
  | 'wechat_connect_mp_app_id'
  | 'wechat_connect_mp_app_secret'
  | 'wechat_connect_mp_app_secret_configured'
  | 'wechat_connect_mp_enabled'
  | 'wechat_connect_open_app_id'
  | 'wechat_connect_open_app_secret'
  | 'wechat_connect_open_app_secret_configured'
  | 'wechat_connect_open_enabled'
  | 'wechat_connect_redirect_url'
  | 'wechat_connect_scopes'
>

/**
 * EmailOAuthProvider identifies the callback field selected by email-based OAuth
 * actions.
 */
export type EmailOAuthProvider = 'github' | 'google'
/**
 * useIdentitySettings binds provider controls to the shared settings draft during
 * component setup. Keep the returned values with that draft for the page lifetime so
 * its DingTalk watcher and callback suggestions observe the same object.
 */
export function useIdentitySettings(form: IdentitySettingsForm) {
  /**
   * t and copyToClipboard supply translated copy feedback for generated callback
   * URLs and the browser origin.
   */
  const { t } = useI18n()
  const { copyToClipboard } = useClipboard()

  /**
   * oidcTokenAuthMethodOptions lists the token endpoint credential modes exposed by
   * the OpenID Connect selector in the identity panel.
   */
  const oidcTokenAuthMethodOptions = [
    { value: 'client_secret_post', label: 'client_secret_post' }, // credentials in request body
    { value: 'client_secret_basic', label: 'client_secret_basic' }, // credentials in Authorization header
    { value: 'none', label: 'none' }, // no token endpoint authentication
  ]

  /**
   * dingtalkCorpPolicyOptions translates the unrestricted and enterprise-only policy
   * values used by the DingTalk controls and watcher.
   */
  const dingtalkCorpPolicyOptions = computed<SettingsSegmentedOption<string>[]>(() => [
    { value: 'none', label: t('admin.settings.dingtalk.corpPolicy.none') },
    { value: 'internal_only', label: t('admin.settings.dingtalk.corpPolicy.internalOnly') },
  ])

  /**
   * buildApiCallbackUrl resolves a provider callback against the browser API entry.
   * It shares the API base used by OAuth start requests so host-only state cookies
   * return to their issuing host. Relative bases resolve against the current origin.
   */
  function buildApiCallbackUrl(path: string): string {
    return new URL(buildApiUrl(path), currentOrigin).toString()
  }

  /**
   * linuxdoRedirectUrlSuggestion supplies the LinuxDo backend callback using
   * the browser API entry used for OAuth start.
   */
  const linuxdoRedirectUrlSuggestion = computed(() => {
    return buildApiCallbackUrl('/auth/oauth/linuxdo/callback')
  })

  /**
   * setAndCopyLinuxdoRedirectUrl writes the suggested LinuxDo callback into the
   * shared draft before copying it. An empty suggestion does nothing; clipboard
   * feedback comes from useClipboard after the field is already updated.
   */
  async function setAndCopyLinuxdoRedirectUrl() {
    const url = linuxdoRedirectUrlSuggestion.value
    if (!url) return

    // Keep the generated callback in the editable draft even when copying fails, so
    // the administrator can copy it manually.
    form.linuxdo_connect_redirect_url = url
    await copyToClipboard(url, t('admin.settings.linuxdo.redirectUrlSetAndCopied'))
  }

  /**
   * githubOAuthRedirectUrlSuggestion supplies the GitHub backend callback using
   * the browser API entry used for OAuth start.
   */
  const githubOAuthRedirectUrlSuggestion = computed(() => {
    return buildApiCallbackUrl('/auth/oauth/github/callback')
  })

  /**
   * googleOAuthRedirectUrlSuggestion supplies the Google backend callback using
   * the browser API entry used for OAuth start.
   */
  const googleOAuthRedirectUrlSuggestion = computed(() => {
    return buildApiCallbackUrl('/auth/oauth/google/callback')
  })

  /**
   * googleOneTapOriginSuggestion uses the browser origin for JavaScript registration
   * and becomes empty when no window is available.
   */
  const googleOneTapOriginSuggestion = computed(() => {
    if (typeof window === 'undefined') return ''
    return window.location.origin
  })

  /**
   * copyGoogleOneTapOrigin copies the current browser origin with translated
   * clipboard feedback. An empty origin does nothing, and this action leaves the
   * configured site addresses and OAuth callback fields unchanged.
   */
  async function copyGoogleOneTapOrigin() {
    const origin = googleOneTapOriginSuggestion.value
    if (!origin) return
    await copyToClipboard(origin, t('admin.settings.emailOAuth.originCopied'))
  }

  /**
   * setAndCopyEmailOAuthRedirectUrl writes the selected GitHub or Google callback
   * before copying it. Empty suggestions do nothing, and clipboard feedback does not
   * undo the value already placed in that provider field.
   */
  async function setAndCopyEmailOAuthRedirectUrl(provider: EmailOAuthProvider) {
    const url =
      provider === 'github'
        ? githubOAuthRedirectUrlSuggestion.value
        : googleOAuthRedirectUrlSuggestion.value
    if (!url) return

    // Keep the generated callback in the editable draft even when copying fails, so
    // the administrator can copy it manually.
    if (provider === 'github') {
      form.github_oauth_redirect_url = url
    } else {
      form.google_oauth_redirect_url = url
    }
    await copyToClipboard(url, t('admin.settings.emailOAuth.callbackCopied'))
  }

  /**
   * wechatRedirectUrlSuggestion supplies the shared browser callback address used by
   * the WeChat credential controls and copy action.
   */
  const wechatRedirectUrlSuggestion = computed(() => {
    return buildApiCallbackUrl('/auth/oauth/wechat/callback')
  })

  /**
   * syncWeChatConnectMode reconciles WeChat capability switches and updates the
   * stored mode and scope draft. Public-account and mobile capabilities cannot
   * remain enabled together; a mobile preference keeps mobile, while other
   * preferences keep the public-account capability.
   *
   * The API helpers resolve the switches before deriving a single stored mode, with
   * public-account, mobile, then open priority. With every capability disabled, the
   * existing mode supplies the default scope choice, so this operation also updates
   * scopes after disabling the last switch.
   */
  function syncWeChatConnectMode(preferredMode?: WeChatConnectMode) {
    // Direct callers may supply conflicting capability flags. Resolve that pair before
    // deriving the legacy mode, with a mobile preference taking priority.
    if (form.wechat_connect_mp_enabled && form.wechat_connect_mobile_enabled) {
      if (preferredMode === 'mobile') {
        form.wechat_connect_mp_enabled = false
      } else {
        form.wechat_connect_mobile_enabled = false
      }
    }

    // Derive mode after resolving the capability flags so persisted mode and scopes
    // describe the same selected authentication entry.
    const capabilities = resolveWeChatConnectModeCapabilities(
      form.wechat_connect_open_enabled,
      form.wechat_connect_mp_enabled,
      form.wechat_connect_mobile_enabled,
      form.wechat_connect_mode,
    )
    form.wechat_connect_open_enabled = capabilities.openEnabled
    form.wechat_connect_mp_enabled = capabilities.mpEnabled
    form.wechat_connect_mobile_enabled = capabilities.mobileEnabled
    form.wechat_connect_mode = deriveWeChatConnectStoredMode(
      capabilities.openEnabled,
      capabilities.mpEnabled,
      capabilities.mobileEnabled,
      form.wechat_connect_mode,
    )
    form.wechat_connect_scopes = defaultWeChatConnectScopesForMode(form.wechat_connect_mode)
  }

  /**
   * handleWeChatOpenEnabledChange updates the open-platform capability and
   * reconciles the stored mode and scopes. Enabling this capability can coexist with
   * public-account or mobile access; the common synchronizer handles their mutual
   * exclusion.
   */
  function handleWeChatOpenEnabledChange(value: boolean) {
    form.wechat_connect_open_enabled = value
    syncWeChatConnectMode(value ? 'open' : undefined)
  }

  /**
   * handleWeChatMPEnabledChange updates public-account access and reconciles the
   * stored mode and scopes. Enabling it first disables mobile access, while
   * disabling it lets the remaining capabilities determine the stored selection.
   */
  function handleWeChatMPEnabledChange(value: boolean) {
    form.wechat_connect_mp_enabled = value
    if (value) {
      form.wechat_connect_mobile_enabled = false
    }
    syncWeChatConnectMode(value ? 'mp' : undefined)
  }

  /**
   * handleWeChatMobileEnabledChange updates mobile access and reconciles the stored
   * mode and scopes. Enabling it first disables public-account access, while
   * disabling it lets the remaining capabilities determine the stored selection.
   */
  function handleWeChatMobileEnabledChange(value: boolean) {
    form.wechat_connect_mobile_enabled = value
    if (value) {
      form.wechat_connect_mp_enabled = false
    }
    syncWeChatConnectMode(value ? 'mobile' : undefined)
  }

  /**
   * setAndCopyWeChatRedirectUrl writes the suggested WeChat browser callback before
   * copying it. Empty suggestions do nothing; the draft update remains in place when
   * the clipboard helper reports a copy failure.
   */
  async function setAndCopyWeChatRedirectUrl() {
    const url = wechatRedirectUrlSuggestion.value
    if (!url) return

    // Keep the generated callback in the editable draft even when copying fails, so
    // the administrator can copy it manually.
    form.wechat_connect_redirect_url = url
    await copyToClipboard(url, t('admin.settings.wechatConnect.redirectUrlSetAndCopied'))
  }

  /**
   * oidcRedirectUrlSuggestion supplies the backend callback address displayed and
   * copied by the OpenID Connect provider controls.
   */
  const oidcRedirectUrlSuggestion = computed(() => {
    return buildApiCallbackUrl('/auth/oauth/oidc/callback')
  })

  /**
   * setAndCopyOIDCRedirectUrl writes the suggested OpenID Connect backend callback
   * before copying it. Empty suggestions do nothing; the configured draft value
   * remains available even when the clipboard helper reports failure.
   */
  async function setAndCopyOIDCRedirectUrl() {
    const url = oidcRedirectUrlSuggestion.value
    if (!url) return

    // Keep the generated callback in the editable draft even when copying fails, so
    // the administrator can copy it manually.
    form.oidc_connect_redirect_url = url
    await copyToClipboard(url, t('admin.settings.oidc.redirectUrlSetAndCopied'))
  }

  // Leaving enterprise-only policy hides these controls. Clear their shared draft
  // values so the next bulk save cannot submit stale enabled switches.
  watch(
    () => form.dingtalk_connect_corp_restriction_policy,
    (policy) => {
      if (policy !== 'internal_only') {
        if (form.dingtalk_connect_bypass_registration)
          form.dingtalk_connect_bypass_registration = false
        if (form.dingtalk_connect_sync_corp_email) form.dingtalk_connect_sync_corp_email = false
        if (form.dingtalk_connect_sync_display_name) form.dingtalk_connect_sync_display_name = false
        if (form.dingtalk_connect_sync_dept) form.dingtalk_connect_sync_dept = false
      }
    },
  )
  /**
   * The returned provider controls share the page draft and supply callback
   * suggestions, clipboard actions, and capability updates to IdentitySettings.
   */
  return {
    oidcTokenAuthMethodOptions,
    dingtalkCorpPolicyOptions,
    buildApiCallbackUrl,
    linuxdoRedirectUrlSuggestion,
    setAndCopyLinuxdoRedirectUrl,
    githubOAuthRedirectUrlSuggestion,
    googleOAuthRedirectUrlSuggestion,
    googleOneTapOriginSuggestion,
    copyGoogleOneTapOrigin,
    setAndCopyEmailOAuthRedirectUrl,
    wechatRedirectUrlSuggestion,
    syncWeChatConnectMode,
    handleWeChatOpenEnabledChange,
    handleWeChatMPEnabledChange,
    handleWeChatMobileEnabledChange,
    setAndCopyWeChatRedirectUrl,
    oidcRedirectUrlSuggestion,
    setAndCopyOIDCRedirectUrl,
  }
}
