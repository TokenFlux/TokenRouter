import {
  buildAuthSourceDefaultsState,
  defaultWeChatConnectScopesForMode,
  deriveWeChatConnectStoredMode,
  normalizeDefaultSubscriptionSettings,
  resolveWeChatConnectModeCapabilities,
} from './settingsFormHelpers'
import { normalizeRegistrationEmailSuffixDomains } from '@/utils/registrationEmailPolicy'
import {
  defaultLoginAgreementDocuments,
  normalizeUserPromptReplacementConfig,
} from './settingsDefaults'
import type { useGeneralSettings } from './useGeneralSettings'
import type { useSecuritySettings } from './useSecuritySettings'
import type { useUsersSettings } from './useUsersSettings'
import type { useGatewaySettings } from './useGatewaySettings'
import type { useEmailSettings } from './useEmailSettings'
import type { SettingsForm } from './settingsState'
import type { SystemSettings } from '@/api/admin/settings.types'

/**
 * settingsHydration.ts applies loaded and saved bulk settings to the shared draft.
 * A server response can omit fields, contain null values, or use legacy WeChat
 * credentials. Initial hydration keeps existing defaults for omitted values and
 * expands legacy WeChat credentials into enabled login capabilities before the
 * controls render, while domain-owned auxiliary drafts receive their corresponding
 * response values alongside the bulk record.
 *
 * Initial loads and save responses share collection normalization and secret-input
 * clearing so SMTP, Turnstile, Tencent captcha, Aliyun captcha, and OAuth secrets
 * never linger in reactive state after persistence. Fast policy rules retain a
 * separate loaded flag and editable collection so an omitted policy field leaves
 * its previous state available and prevents uninitialized rules from being
 * serialized by the payload builder.
 */

/** SettingsHydrationControllers selects the domain actions needed by the bulk settings response conversion. */
export interface SettingsHydrationControllers {
  /** general formats page-size text after loaded or saved values replace the shared draft. */
  general: Pick<
    ReturnType<typeof useGeneralSettings>,
    'tablePageSizeOptionsInput' | 'formatTablePageSizeOptions'
  >
  /** security synchronizes captcha selection, whitelist tags, and forwarded-header drafts after response application. */
  security: Pick<
    ReturnType<typeof useSecuritySettings>,
    | 'registrationEmailSuffixWhitelistTags'
    | 'registrationEmailSuffixWhitelistDraft'
    | 'forwardedClientIpHeaderDraft'
    | 'syncCaptchaProviderSelection'
    | 'normalizeForwardedClientIpHeaders'
  >
  /** users holds authentication-source defaults rebuilt from each successful bulk response before snapshots are accepted. */
  users: Pick<ReturnType<typeof useUsersSettings>, 'authSourceDefaults'>
  /** gateway holds Fast policy rules and its loaded flag used to decide whether serialization includes that field. */
  gateway: Pick<
    ReturnType<typeof useGatewaySettings>,
    'openaiFastPolicyForm' | 'openaiFastPolicyLoaded'
  >
  /** email tracks whether the SMTP password was manually edited before a response clears that input. */
  email: Pick<ReturnType<typeof useEmailSettings>, 'smtpPasswordManuallyEdited'>
}

/**
 * createSettingsHydration binds response application to one bulk draft and its
 * domain-owned auxiliary controls. Call it during page setup, then apply loaded
 * or saved responses before the persistence coordinator records clean snapshots.
 */
export function createSettingsHydration(
  form: SettingsForm,
  controllers: SettingsHydrationControllers,
) {
  /**
   * These domain references update auxiliary editor drafts alongside form fields before the page accepts the corresponding response baseline.
   */
  const { tablePageSizeOptionsInput, formatTablePageSizeOptions } = controllers.general
  const {
    registrationEmailSuffixWhitelistTags,
    registrationEmailSuffixWhitelistDraft,
    forwardedClientIpHeaderDraft,
    syncCaptchaProviderSelection,
    normalizeForwardedClientIpHeaders,
  } = controllers.security
  const { authSourceDefaults } = controllers.users
  const { openaiFastPolicyForm, openaiFastPolicyLoaded } = controllers.gateway
  const { smtpPasswordManuallyEdited } = controllers.email

  /**
   * clearSettingsSecretInputs resets every write-only secret input on the shared
   * draft after initial loading or successful persistence so plaintext credentials
   * never remain in reactive form state.
   */
  function clearSettingsSecretInputs() {
    form.smtp_password = ''
    smtpPasswordManuallyEdited.value = false
    form.turnstile_secret_key = ''
    form.tencent_captcha_app_secret_key = ''
    form.tencent_captcha_cloud_secret_id = ''
    form.tencent_captcha_cloud_secret_key = ''
    form.aliyun_captcha_access_key_secret = ''
    form.linuxdo_connect_client_secret = ''
    form.dingtalk_connect_client_secret = ''
    form.wechat_connect_app_secret = ''
    form.wechat_connect_open_app_secret = ''
    form.wechat_connect_mp_app_secret = ''
    form.wechat_connect_mobile_app_secret = ''
    form.oidc_connect_client_secret = ''
    form.github_oauth_client_secret = ''
    form.google_oauth_client_secret = ''
  }

  /**
   * applySharedResponseState normalizes authentication-source defaults, quota
   * thresholds, whitelist tags, forwarded-IP headers, page-size inputs, WeChat
   * capabilities, and optional OpenAI Fast policy rules across loaded and saved
   * bulk settings responses.
   */
  function applySharedResponseState(response: SystemSettings) {
    Object.assign(authSourceDefaults, buildAuthSourceDefaultsState(response))
    form.openai_provider_quota_auto_pause = {
      default_threshold_5h: response.openai_provider_quota_auto_pause?.default_threshold_5h ?? 0,
      default_threshold_7d: response.openai_provider_quota_auto_pause?.default_threshold_7d ?? 0,
    }
    if (Array.isArray(response.default_subscriptions)) {
      form.default_subscriptions = normalizeDefaultSubscriptionSettings(
        response.default_subscriptions,
      )
    }
    if (response.user_prompt_replacement_config !== undefined) {
      form.user_prompt_replacement_config = normalizeUserPromptReplacementConfig(
        response.user_prompt_replacement_config,
      )
    }
    registrationEmailSuffixWhitelistTags.value = normalizeRegistrationEmailSuffixDomains(
      response.registration_email_suffix_whitelist,
    )
    form.forwarded_client_ip_headers = normalizeForwardedClientIpHeaders(
      response.forwarded_client_ip_headers,
    )
    forwardedClientIpHeaderDraft.value = ''
    tablePageSizeOptionsInput.value = formatTablePageSizeOptions(
      Array.isArray(response.table_page_size_options)
        ? response.table_page_size_options
        : [10, 20, 50, 100],
    )
    registrationEmailSuffixWhitelistDraft.value = ''
    clearSettingsSecretInputs()

    const wechatCapabilities = resolveWeChatConnectModeCapabilities(
      response.wechat_connect_open_enabled,
      response.wechat_connect_mp_enabled,
      response.wechat_connect_mobile_enabled,
      response.wechat_connect_mode,
    )
    form.wechat_connect_open_enabled = wechatCapabilities.openEnabled
    form.wechat_connect_mp_enabled = wechatCapabilities.mpEnabled
    form.wechat_connect_mobile_enabled = wechatCapabilities.mobileEnabled
    form.wechat_connect_mode = deriveWeChatConnectStoredMode(
      wechatCapabilities.openEnabled,
      wechatCapabilities.mpEnabled,
      wechatCapabilities.mobileEnabled,
      response.wechat_connect_mode,
    )
    form.wechat_connect_scopes = defaultWeChatConnectScopesForMode(form.wechat_connect_mode)

    // An omitted policy field leaves its loaded flag untouched. This prevents
    // the payload builder from replacing unknown server rules with an empty draft.
    if (
      response.openai_fast_policy_settings &&
      Array.isArray(response.openai_fast_policy_settings.rules)
    ) {
      openaiFastPolicyForm.rules = response.openai_fast_policy_settings.rules.map((rule) => ({
        ...rule,
        user_ids: rule.user_ids ? [...rule.user_ids] : [],
        model_whitelist: rule.model_whitelist ? [...rule.model_whitelist] : [],
      }))
      openaiFastPolicyLoaded.value = true
    }

    return wechatCapabilities
  }

  /**
   * applyLoadedSettings fills the shared draft from an initial or discard response.
   * Non-null fields replace current values, then document defaults, subscriptions,
   * secret inputs, captcha selection, and legacy WeChat capabilities are normalized
   * for the mounted controls. Legacy application identifiers and configured-secret
   * flags fill enabled WeChat modes whose specific values are absent.
   */
  function applyLoadedSettings(settings: SystemSettings) {
    settings.payment_load_balance_strategy = settings.payment_load_balance_strategy || 'round-robin'

    // Null and omitted server values keep the draft defaults, allowing older
    // responses to populate known fields while leaving unconfigured controls usable.
    for (const [key, value] of Object.entries(settings)) {
      if (key === 'openai_fast_policy_settings') continue
      if (value !== null && value !== undefined) {
        ;(form as Record<string, unknown>)[key] = value
      }
    }

    syncCaptchaProviderSelection()
    form.login_agreement_mode = settings.login_agreement_mode === 'checkbox' ? 'checkbox' : 'modal'
    form.login_agreement_updated_at = settings.login_agreement_updated_at || '2026-03-31'
    form.login_agreement_documents =
      Array.isArray(settings.login_agreement_documents) &&
      settings.login_agreement_documents.length > 0
        ? settings.login_agreement_documents.map((doc) => ({
            id: doc.id || '',
            title: doc.title || '',
            content_md: doc.content_md || '',
            localization: doc.localization,
          }))
        : defaultLoginAgreementDocuments()
    form.user_prompt_replacement_config = normalizeUserPromptReplacementConfig(
      settings.user_prompt_replacement_config,
    )
    form.backend_mode_enabled = settings.backend_mode_enabled
    form.default_subscriptions = normalizeDefaultSubscriptionSettings(
      settings.default_subscriptions,
    )

    const wechatCapabilities = applySharedResponseState(settings)

    // Legacy responses may provide one credential pair for enabled WeChat modes.
    // Fill missing mode-specific identifiers and configured flags from that pair.
    const legacyWeChatAppID = String(settings.wechat_connect_app_id || '').trim()
    const legacyWeChatSecretConfigured = Boolean(settings.wechat_connect_app_secret_configured)

    if (!form.wechat_connect_open_app_id && wechatCapabilities.openEnabled) {
      form.wechat_connect_open_app_id = legacyWeChatAppID
    }
    if (!form.wechat_connect_mp_app_id && wechatCapabilities.mpEnabled) {
      form.wechat_connect_mp_app_id = legacyWeChatAppID
    }
    if (!form.wechat_connect_mobile_app_id && wechatCapabilities.mobileEnabled) {
      form.wechat_connect_mobile_app_id = legacyWeChatAppID
    }
    if (!form.wechat_connect_open_app_secret_configured && wechatCapabilities.openEnabled) {
      form.wechat_connect_open_app_secret_configured = legacyWeChatSecretConfigured
    }
    if (!form.wechat_connect_mp_app_secret_configured && wechatCapabilities.mpEnabled) {
      form.wechat_connect_mp_app_secret_configured = legacyWeChatSecretConfigured
    }
    if (!form.wechat_connect_mobile_app_secret_configured && wechatCapabilities.mobileEnabled) {
      form.wechat_connect_mobile_app_secret_configured = legacyWeChatSecretConfigured
    }
  }

  /**
   * applyUpdatedSettings applies the successful bulk write response and clears all
   * write-only secret inputs, including SMTP, Turnstile, Tencent captcha, Aliyun
   * captcha, and OAuth secrets, before the persistence coordinator records a new
   * clean baseline.
   */
  function applyUpdatedSettings(updated: SystemSettings) {
    // Fast policy has a separately tracked collection. Apply its returned rules
    // in applySharedResponseState so draft edits and the loaded flag stay aligned.
    for (const [key, value] of Object.entries(updated)) {
      if (key === 'openai_fast_policy_settings') continue
      if (value !== null && value !== undefined) {
        ;(form as Record<string, unknown>)[key] = value
      }
    }
    applySharedResponseState(updated)
  }

  return { applyLoadedSettings, applyUpdatedSettings }
}
