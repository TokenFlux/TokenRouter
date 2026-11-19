import type { SettingsForm } from './settingsState'
import type { UpdateSettingsRequest, WeChatConnectMode } from '@/api/admin/settings.types'
import { defaultWeChatConnectScopesForMode } from './settingsFormHelpers'

/**
 * identitySettingsPayload.ts serializes provider identity settings for the
 * administrator bulk update. OAuth controls retain ordinary text fields as supplied,
 * including empty strings, while blank replacement secrets become undefined so JSON
 * requests omit them. This distinction lets administrators edit callbacks and
 * provider options while keeping credentials that the settings response does not
 * reveal.
 *
 * WeChat stores both provider-specific credentials and compatibility fields. The
 * legacy application identifier selects the first nonempty open, public-account,
 * mobile, then legacy identifier. The caller supplies a mode derived from validated
 * capability switches, and the serializer chooses scopes from that mode so the
 * request carries matching mode and scope values.
 */

/**
 * buildIdentitySettingsPayload returns provider fields after capability validation
 * and mode normalization. The caller passes the derived WeChat mode; this function
 * reads the draft without changing it and sends ordinary empty text values as
 * supplied.
 *
 * Blank secrets become undefined, causing the HTTP JSON request to retain stored
 * credentials. WeChat compatibility application identity follows open, public-
 * account, mobile, then legacy precedence regardless of enabled switches, while its
 * scopes come from the supplied mode. Provider-specific credentials remain separate
 * fields in the same request.
 */
export function buildIdentitySettingsPayload(
  form: Pick<
    SettingsForm,
    | 'linuxdo_connect_enabled'
    | 'linuxdo_connect_client_id'
    | 'linuxdo_connect_client_secret'
    | 'linuxdo_connect_redirect_url'
    | 'dingtalk_connect_enabled'
    | 'dingtalk_connect_client_id'
    | 'dingtalk_connect_client_secret'
    | 'dingtalk_connect_redirect_url'
    | 'dingtalk_connect_corp_restriction_policy'
    | 'dingtalk_connect_internal_corp_id'
    | 'dingtalk_connect_bypass_registration'
    | 'dingtalk_connect_sync_corp_email'
    | 'dingtalk_connect_sync_display_name'
    | 'dingtalk_connect_sync_dept'
    | 'dingtalk_connect_sync_corp_email_attr_key'
    | 'dingtalk_connect_sync_display_name_attr_key'
    | 'dingtalk_connect_sync_dept_attr_key'
    | 'dingtalk_connect_sync_corp_email_attr_name'
    | 'dingtalk_connect_sync_display_name_attr_name'
    | 'dingtalk_connect_sync_dept_attr_name'
    | 'wechat_connect_enabled'
    | 'wechat_connect_open_app_id'
    | 'wechat_connect_mp_app_id'
    | 'wechat_connect_mobile_app_id'
    | 'wechat_connect_app_id'
    | 'wechat_connect_app_secret'
    | 'wechat_connect_open_app_secret'
    | 'wechat_connect_mp_app_secret'
    | 'wechat_connect_mobile_app_secret'
    | 'wechat_connect_open_enabled'
    | 'wechat_connect_mp_enabled'
    | 'wechat_connect_mobile_enabled'
    | 'wechat_connect_redirect_url'
    | 'wechat_connect_frontend_redirect_url'
    | 'oidc_connect_enabled'
    | 'oidc_connect_client_id'
    | 'oidc_connect_client_secret'
    | 'oidc_connect_issuer_url'
    | 'oidc_connect_discovery_url'
    | 'oidc_connect_authorize_url'
    | 'oidc_connect_token_url'
    | 'oidc_connect_userinfo_url'
    | 'oidc_connect_jwks_url'
    | 'oidc_connect_scopes'
    | 'oidc_connect_redirect_url'
    | 'oidc_connect_frontend_redirect_url'
    | 'oidc_connect_token_auth_method'
    | 'oidc_connect_use_pkce'
    | 'oidc_connect_validate_id_token'
    | 'oidc_connect_allowed_signing_algs'
    | 'oidc_connect_clock_skew_seconds'
    | 'oidc_connect_require_email_verified'
    | 'oidc_connect_userinfo_email_path'
    | 'oidc_connect_userinfo_id_path'
    | 'oidc_connect_userinfo_username_path'
    | 'github_oauth_enabled'
    | 'github_oauth_client_id'
    | 'github_oauth_client_secret'
    | 'github_oauth_redirect_url'
    | 'github_oauth_frontend_redirect_url'
    | 'google_oauth_enabled'
    | 'google_one_tap_enabled'
    | 'google_oauth_client_id'
    | 'google_oauth_client_secret'
    | 'google_oauth_redirect_url'
    | 'google_oauth_frontend_redirect_url'
  >,
  wechatStoredMode: WeChatConnectMode,
): Pick<
  UpdateSettingsRequest,
  | 'linuxdo_connect_enabled'
  | 'linuxdo_connect_client_id'
  | 'linuxdo_connect_client_secret'
  | 'linuxdo_connect_redirect_url'
  | 'dingtalk_connect_enabled'
  | 'dingtalk_connect_client_id'
  | 'dingtalk_connect_client_secret'
  | 'dingtalk_connect_redirect_url'
  | 'dingtalk_connect_corp_restriction_policy'
  | 'dingtalk_connect_internal_corp_id'
  | 'dingtalk_connect_bypass_registration'
  | 'dingtalk_connect_sync_corp_email'
  | 'dingtalk_connect_sync_display_name'
  | 'dingtalk_connect_sync_dept'
  | 'dingtalk_connect_sync_corp_email_attr_key'
  | 'dingtalk_connect_sync_display_name_attr_key'
  | 'dingtalk_connect_sync_dept_attr_key'
  | 'dingtalk_connect_sync_corp_email_attr_name'
  | 'dingtalk_connect_sync_display_name_attr_name'
  | 'dingtalk_connect_sync_dept_attr_name'
  | 'wechat_connect_enabled'
  | 'wechat_connect_app_id'
  | 'wechat_connect_app_secret'
  | 'wechat_connect_open_app_id'
  | 'wechat_connect_open_app_secret'
  | 'wechat_connect_mp_app_id'
  | 'wechat_connect_mp_app_secret'
  | 'wechat_connect_mobile_app_id'
  | 'wechat_connect_mobile_app_secret'
  | 'wechat_connect_open_enabled'
  | 'wechat_connect_mp_enabled'
  | 'wechat_connect_mobile_enabled'
  | 'wechat_connect_mode'
  | 'wechat_connect_scopes'
  | 'wechat_connect_redirect_url'
  | 'wechat_connect_frontend_redirect_url'
  | 'oidc_connect_enabled'
  | 'oidc_connect_client_id'
  | 'oidc_connect_client_secret'
  | 'oidc_connect_issuer_url'
  | 'oidc_connect_discovery_url'
  | 'oidc_connect_authorize_url'
  | 'oidc_connect_token_url'
  | 'oidc_connect_userinfo_url'
  | 'oidc_connect_jwks_url'
  | 'oidc_connect_scopes'
  | 'oidc_connect_redirect_url'
  | 'oidc_connect_frontend_redirect_url'
  | 'oidc_connect_token_auth_method'
  | 'oidc_connect_use_pkce'
  | 'oidc_connect_validate_id_token'
  | 'oidc_connect_allowed_signing_algs'
  | 'oidc_connect_clock_skew_seconds'
  | 'oidc_connect_require_email_verified'
  | 'oidc_connect_userinfo_email_path'
  | 'oidc_connect_userinfo_id_path'
  | 'oidc_connect_userinfo_username_path'
  | 'github_oauth_enabled'
  | 'github_oauth_client_id'
  | 'github_oauth_client_secret'
  | 'github_oauth_redirect_url'
  | 'github_oauth_frontend_redirect_url'
  | 'google_oauth_enabled'
  | 'google_one_tap_enabled'
  | 'google_oauth_client_id'
  | 'google_oauth_client_secret'
  | 'google_oauth_redirect_url'
  | 'google_oauth_frontend_redirect_url'
> {
  // JSON serialization removes undefined properties. Apply that representation to
  // each secret below so a blank replacement draft cannot erase its stored
  // credential.
  return {
    linuxdo_connect_enabled: form.linuxdo_connect_enabled,

    linuxdo_connect_client_id: form.linuxdo_connect_client_id,

    linuxdo_connect_client_secret: form.linuxdo_connect_client_secret || undefined,

    linuxdo_connect_redirect_url: form.linuxdo_connect_redirect_url,

    dingtalk_connect_enabled: form.dingtalk_connect_enabled,

    dingtalk_connect_client_id: form.dingtalk_connect_client_id,

    dingtalk_connect_client_secret: form.dingtalk_connect_client_secret || undefined,

    dingtalk_connect_redirect_url: form.dingtalk_connect_redirect_url,

    dingtalk_connect_corp_restriction_policy: form.dingtalk_connect_corp_restriction_policy,

    dingtalk_connect_internal_corp_id: form.dingtalk_connect_internal_corp_id,

    dingtalk_connect_bypass_registration: form.dingtalk_connect_bypass_registration,

    dingtalk_connect_sync_corp_email: form.dingtalk_connect_sync_corp_email,

    dingtalk_connect_sync_display_name: form.dingtalk_connect_sync_display_name,

    dingtalk_connect_sync_dept: form.dingtalk_connect_sync_dept,

    dingtalk_connect_sync_corp_email_attr_key: form.dingtalk_connect_sync_corp_email_attr_key,

    dingtalk_connect_sync_display_name_attr_key: form.dingtalk_connect_sync_display_name_attr_key,

    dingtalk_connect_sync_dept_attr_key: form.dingtalk_connect_sync_dept_attr_key,

    dingtalk_connect_sync_corp_email_attr_name: form.dingtalk_connect_sync_corp_email_attr_name,

    dingtalk_connect_sync_display_name_attr_name: form.dingtalk_connect_sync_display_name_attr_name,

    dingtalk_connect_sync_dept_attr_name: form.dingtalk_connect_sync_dept_attr_name,

    wechat_connect_enabled: form.wechat_connect_enabled,

    // The compatibility identifier follows populated credential slots in this order,
    // independently of which WeChat capabilities are currently enabled.
    wechat_connect_app_id:
      form.wechat_connect_open_app_id ||
      form.wechat_connect_mp_app_id ||
      form.wechat_connect_mobile_app_id ||
      form.wechat_connect_app_id,

    wechat_connect_app_secret: form.wechat_connect_app_secret || undefined,

    wechat_connect_open_app_id: form.wechat_connect_open_app_id,

    wechat_connect_open_app_secret: form.wechat_connect_open_app_secret || undefined,

    wechat_connect_mp_app_id: form.wechat_connect_mp_app_id,

    wechat_connect_mp_app_secret: form.wechat_connect_mp_app_secret || undefined,

    wechat_connect_mobile_app_id: form.wechat_connect_mobile_app_id,

    wechat_connect_mobile_app_secret: form.wechat_connect_mobile_app_secret || undefined,

    wechat_connect_open_enabled: form.wechat_connect_open_enabled,

    wechat_connect_mp_enabled: form.wechat_connect_mp_enabled,

    wechat_connect_mobile_enabled: form.wechat_connect_mobile_enabled,

    wechat_connect_mode: wechatStoredMode,

    // Scopes follow the validated stored mode, including an empty mobile scope, so
    // stale draft scopes do not enter the request.
    wechat_connect_scopes: defaultWeChatConnectScopesForMode(wechatStoredMode),

    wechat_connect_redirect_url: form.wechat_connect_redirect_url,

    wechat_connect_frontend_redirect_url: form.wechat_connect_frontend_redirect_url,

    oidc_connect_enabled: form.oidc_connect_enabled,

    oidc_connect_client_id: form.oidc_connect_client_id,

    oidc_connect_client_secret: form.oidc_connect_client_secret || undefined,

    oidc_connect_issuer_url: form.oidc_connect_issuer_url,

    oidc_connect_discovery_url: form.oidc_connect_discovery_url,

    oidc_connect_authorize_url: form.oidc_connect_authorize_url,

    oidc_connect_token_url: form.oidc_connect_token_url,

    oidc_connect_userinfo_url: form.oidc_connect_userinfo_url,

    oidc_connect_jwks_url: form.oidc_connect_jwks_url,

    oidc_connect_scopes: form.oidc_connect_scopes,

    oidc_connect_redirect_url: form.oidc_connect_redirect_url,

    oidc_connect_frontend_redirect_url: form.oidc_connect_frontend_redirect_url,

    oidc_connect_token_auth_method: form.oidc_connect_token_auth_method,

    oidc_connect_use_pkce: form.oidc_connect_use_pkce,

    oidc_connect_validate_id_token: form.oidc_connect_validate_id_token,

    oidc_connect_allowed_signing_algs: form.oidc_connect_allowed_signing_algs,

    oidc_connect_clock_skew_seconds: form.oidc_connect_clock_skew_seconds,

    oidc_connect_require_email_verified: form.oidc_connect_require_email_verified,

    oidc_connect_userinfo_email_path: form.oidc_connect_userinfo_email_path,

    oidc_connect_userinfo_id_path: form.oidc_connect_userinfo_id_path,

    oidc_connect_userinfo_username_path: form.oidc_connect_userinfo_username_path,

    github_oauth_enabled: form.github_oauth_enabled,

    github_oauth_client_id: form.github_oauth_client_id,

    github_oauth_client_secret: form.github_oauth_client_secret || undefined,

    github_oauth_redirect_url: form.github_oauth_redirect_url,

    github_oauth_frontend_redirect_url: form.github_oauth_frontend_redirect_url,

    google_oauth_enabled: form.google_oauth_enabled,

    google_one_tap_enabled: form.google_one_tap_enabled,

    google_oauth_client_id: form.google_oauth_client_id,

    google_oauth_client_secret: form.google_oauth_client_secret || undefined,

    google_oauth_redirect_url: form.google_oauth_redirect_url,

    google_oauth_frontend_redirect_url: form.google_oauth_frontend_redirect_url,
  }
}
