import { reactive } from 'vue'
import type { CustomMenuItem, CustomEndpoint, FooterLinkGroup } from '@/api/settings.types'
import { originalContent, type LocalizedUpdate } from '@/i18n/content'
import { getLocale } from '@/i18n'
import { normalizeProviderSchedulingThresholdsMap } from '@/api/admin/settings'
import type {
  SystemSettings,
  OpenAIQuotaAutoPauseSettings,
  UsageRankingSortBy,
  CreativeModelSetting,
} from '@/api/admin/settings'
import type { NotifyEmailEntry } from '@/api/notifications.types'
import {
  tablePageSizeDefault,
  usageRankingLimitDefault,
  marketplaceAvailabilityWindowDaysDefault,
  marketplaceAvailabilityBucketMinutesDefault,
  defaultLoginAgreementDocuments,
  defaultUserPromptReplacementConfig,
} from './settingsDefaults'

/**
 * settingsState.ts defines the single editable bulk settings record used by the
 * administrator page. Domain controllers receive typed field selections of this
 * reactive object, so edits across hidden tabs contribute to the same request
 * and dirty snapshot. Secret inputs start empty and are populated by the user.
 */

/**
 * SettingsForm represents the editable bulk settings record, including transient
 * secret inputs and text overrides required by controls. Responses WebSocket
 * settings use their dedicated child target, and WeChat capability switches are
 * concrete booleans after response hydration.
 */
export type SettingsForm = Omit<
  SystemSettings,
  | 'responses_ws'
  | 'responses_ws_effective'
  | 'wechat_connect_open_enabled'
  | 'wechat_connect_mp_enabled'
  | 'wechat_connect_mobile_enabled'
> & {
  // Secret inputs are separate from server configured flags. Empty drafts are
  // omitted by serialization, and response hydration clears the relevant inputs
  // after loading or successful persistence.
  smtp_password: string // SMTP password input
  turnstile_secret_key: string // Turnstile secret input
  tencent_captcha_app_secret_key: string // Tencent application secret
  tencent_captcha_cloud_secret_id: string // Tencent cloud identifier
  tencent_captcha_cloud_secret_key: string // Tencent cloud secret
  aliyun_captcha_access_key_secret: string // Aliyun access secret
  linuxdo_connect_client_secret: string // LinuxDo client secret
  dingtalk_connect_client_secret: string // DingTalk client secret
  wechat_connect_app_secret: string // Legacy WeChat secret
  wechat_connect_open_app_secret: string // WeChat website secret
  wechat_connect_mp_app_secret: string // WeChat official-account secret
  wechat_connect_mobile_app_secret: string // WeChat mobile secret
  // wechat_connect_open_enabled, wechat_connect_mp_enabled, and
  // wechat_connect_mobile_enabled are concrete capabilities after response
  // hydration. Identity controls prevent official-account and mobile login
  // modes from remaining enabled together in an accepted settings update.
  wechat_connect_open_enabled: boolean // Website login capability
  wechat_connect_mp_enabled: boolean // Official-account login capability
  wechat_connect_mobile_enabled: boolean // Mobile login capability
  oidc_connect_client_secret: string // OIDC client secret
  github_oauth_client_secret: string // GitHub client secret
  google_oauth_client_secret: string // Google client secret
  force_email_on_third_party_signup: boolean // Third-party signup email requirement
  advanced_scheduler_sticky_weighted_enabled: boolean // sticky weighted enabled scheduling switch
  advanced_scheduler_subscription_priority_enabled: boolean // subscription priority enabled scheduling switch
  // Scheduler text overrides retain an empty string to request the effective
  // server value. Numeric parsing remains a backend setting rule after trimming
  // these user-entered strings during request construction.
  advanced_scheduler_ewma_error_rate_alpha: string // ewma error rate alpha override text
  advanced_scheduler_ewma_ttft_alpha: string // ewma ttft alpha override text
  advanced_scheduler_sticky_escape_enabled: boolean // sticky escape enabled scheduling switch
  advanced_scheduler_sticky_escape_ttft_ms: string // sticky escape ttft ms override text
  advanced_scheduler_sticky_escape_error_rate: string // sticky escape error rate override text
  advanced_scheduler_lb_top_k: string // lb top k override text
  advanced_scheduler_weight_priority: string // weight priority override text
  advanced_scheduler_weight_load: string // weight load override text
  advanced_scheduler_weight_queue: string // weight queue override text
  advanced_scheduler_weight_error_rate: string // weight error rate override text
  advanced_scheduler_weight_ttft: string // weight ttft override text
  advanced_scheduler_weight_reset: string // weight reset override text
  advanced_scheduler_weight_quota_headroom: string // weight quota headroom override text
  advanced_scheduler_weight_previous_response: string // weight previous response override text
  advanced_scheduler_weight_session_sticky: string // weight session sticky override text
  openai_provider_quota_auto_pause: OpenAIQuotaAutoPauseSettings // Provider quota thresholds
  // Platform controls index this normalized map directly, so the draft includes
  // every supported platform even when the server has no configured override.
  provider_scheduling_thresholds: ReturnType<typeof normalizeProviderSchedulingThresholdsMap>
}

/**
 * createSettingsForm creates one reactive bulk draft with editable page defaults.
 * Each invocation owns its nested arrays and objects; domain controllers receive
 * field selections of this same object, and response hydration updates it in place.
 */
export function createSettingsForm() {
  const form = reactive<SettingsForm>({
    registration_enabled: true,
    email_verify_enabled: false,
    registration_email_suffix_whitelist: [],
    registration_email_normalization: false,
    registration_email_domain_quota_enabled: false,
    user_email_change_enabled: false,
    promo_code_enabled: true,
    invitation_code_enabled: false,
    password_reset_enabled: false,
    totp_enabled: false,
    totp_encryption_key_configured: false,
    session_binding_enabled: false,
    step_up_enabled: false,
    audit_log_retention_days: 180,
    login_agreement_enabled: false,
    login_agreement_mode: 'modal',
    login_agreement_updated_at: '2026-03-31',
    login_agreement_documents: defaultLoginAgreementDocuments(),
    default_balance: 0,
    affiliate_enabled: false,
    provider_scheduling_thresholds: normalizeProviderSchedulingThresholdsMap(),
    affiliate_rebate_rate: 20,
    affiliate_rebate_freeze_hours: 0,
    affiliate_rebate_duration_days: 0,
    affiliate_rebate_per_invitee_cap: 0,
    affiliate_admin_recharge_enabled: false,
    default_concurrency: 1,
    default_subscriptions: [],
    balance_unit_name: 'USD',
    balance_unit_symbol: '$',
    balance_icon_svg: '',
    reasoning_point_rmb_unit_price: 0,
    usd_exchange_rate: 0,
    marketplace_availability_window_days: marketplaceAvailabilityWindowDaysDefault,
    marketplace_availability_bucket_minutes: marketplaceAvailabilityBucketMinutesDefault,
    force_email_on_third_party_signup: false,
    default_user_rpm_limit: 0,
    default_user_api_key_limit: 100,
    localized_settings: Object.fromEntries(
      [
        'balance_unit_name',
        'balance_low_notify_recharge_url',
        'oidc_connect_provider_name',
        'payment_help_text',
        'payment_help_image_url',
        'payment_product_name_prefix',
        'payment_product_name_suffix',
        'smtp_from_name',
      ].map((key) => [key, originalContent(key === 'balance_unit_name' ? 'USD' : '', getLocale())]),
    ) as Record<string, LocalizedUpdate<string>>,
    default_locale: 'en',
    site_texts: Object.fromEntries(
      [
        'site_name',
        'site_title',
        'site_subtitle',
        'site_share_title',
        'site_share_description',
        'contact_info',
        'doc_url',
        'home_content',
        'purchase_subscription_url',
        'footer_text',
      ].map((key) => [key, originalContent(key === 'site_name' ? 'TokenRouter' : '', getLocale())]),
    ) as Record<string, LocalizedUpdate<string>>,
    site_name: 'TokenRouter',
    site_logo: '',
    site_share_image: '',
    site_subtitle: 'Subscription to API Conversion Platform',
    api_base_url: '',
    contact_info: '',
    doc_url: '',
    home_content: '',
    backend_mode_enabled: false,
    hide_ccs_import_button: false,
    payment_enabled: false,
    payment_min_amount: 1,
    payment_max_amount: 10000,
    payment_daily_limit: 50000,
    payment_max_pending_orders: 3,
    payment_order_timeout_minutes: 30,
    payment_balance_disabled: false,
    payment_balance_recharge_multiplier: 1,
    payment_subscription_usd_to_cny_rate: 0,
    payment_recharge_fee_rate: 0,
    payment_method_fees: {},
    payment_enabled_types: [],
    payment_help_image_url: '',
    payment_help_text: '',
    payment_product_name_prefix: '',
    payment_product_name_suffix: '',
    payment_load_balance_strategy: 'round-robin',
    payment_cancel_rate_limit_enabled: false,
    payment_cancel_rate_limit_max: 10,
    payment_cancel_rate_limit_window: 1,
    payment_cancel_rate_limit_unit: 'day',
    payment_cancel_rate_limit_window_mode: 'rolling',
    payment_alipay_force_qrcode: false,
    payment_alipay_mobile_precreate_deep_link: false,
    table_default_page_size: tablePageSizeDefault,
    table_page_size_options: [10, 20, 50, 100],
    usage_ranking_limit: usageRankingLimitDefault,
    usage_ranking_enabled: true,
    usage_ranking_sort_by: 'total_tokens' as UsageRankingSortBy,
    usage_ranking_show_total_tokens: true,
    usage_ranking_show_requests: true,
    usage_ranking_show_actual_cost: true,
    custom_menu_items: [] as CustomMenuItem[],
    custom_endpoints: [] as CustomEndpoint[],
    footer_links: [] as FooterLinkGroup[],
    footer_text: '',
    home_featured_models: [] as string[],
    frontend_url: '',
    smtp_host: '',
    smtp_port: 587,
    smtp_username: '',
    smtp_password: '',
    smtp_password_configured: false,
    smtp_from_email: '',
    smtp_from_name: '',
    smtp_use_tls: true,
    // Cloudflare Turnstile
    turnstile_enabled: false,
    turnstile_site_key: '',
    turnstile_secret_key: '',
    turnstile_secret_key_configured: false,
    tencent_captcha_enabled: false,
    tencent_captcha_app_id: '',
    tencent_captcha_app_secret_key: '',
    tencent_captcha_app_secret_key_configured: false,
    tencent_captcha_cloud_secret_id: '',
    tencent_captcha_cloud_secret_id_configured: false,
    tencent_captcha_cloud_secret_key: '',
    tencent_captcha_cloud_secret_key_configured: false,
    tencent_captcha_region: 'cn',
    aliyun_captcha_enabled: false,
    aliyun_captcha_access_key_id: '',
    aliyun_captcha_access_key_secret: '',
    aliyun_captcha_access_key_secret_configured: false,
    aliyun_captcha_scene_id: '',
    aliyun_captcha_prefix: '',
    aliyun_captcha_region: 'cn',
    api_key_acl_trust_forwarded_ip: true,
    forwarded_client_ip_headers: [],
    linuxdo_connect_enabled: false,
    linuxdo_connect_client_id: '',
    linuxdo_connect_client_secret: '',
    linuxdo_connect_client_secret_configured: false,
    linuxdo_connect_redirect_url: '',
    dingtalk_connect_enabled: false,
    dingtalk_connect_client_id: '',
    dingtalk_connect_client_secret: '',
    dingtalk_connect_client_secret_configured: false,
    dingtalk_connect_redirect_url: '',
    dingtalk_connect_corp_restriction_policy: 'none',
    dingtalk_connect_internal_corp_id: '',
    dingtalk_connect_bypass_registration: false,
    dingtalk_connect_sync_corp_email: false,
    dingtalk_connect_sync_display_name: false,
    dingtalk_connect_sync_dept: false,
    dingtalk_connect_sync_corp_email_attr_key: 'dingtalk_email',
    dingtalk_connect_sync_display_name_attr_key: 'dingtalk_name',
    dingtalk_connect_sync_dept_attr_key: 'dingtalk_department',
    dingtalk_connect_sync_corp_email_attr_name: '钉钉企业邮箱',
    dingtalk_connect_sync_display_name_attr_name: '钉钉姓名',
    dingtalk_connect_sync_dept_attr_name: '钉钉部门',
    wechat_connect_enabled: false,
    wechat_connect_app_id: '',
    wechat_connect_app_secret: '',
    wechat_connect_app_secret_configured: false,
    wechat_connect_open_app_id: '',
    wechat_connect_open_app_secret: '',
    wechat_connect_open_app_secret_configured: false,
    wechat_connect_mp_app_id: '',
    wechat_connect_mp_app_secret: '',
    wechat_connect_mp_app_secret_configured: false,
    wechat_connect_mobile_app_id: '',
    wechat_connect_mobile_app_secret: '',
    wechat_connect_mobile_app_secret_configured: false,
    wechat_connect_open_enabled: false,
    wechat_connect_mp_enabled: false,
    wechat_connect_mobile_enabled: false,
    wechat_connect_mode: 'open',
    wechat_connect_scopes: 'snsapi_login',
    wechat_connect_redirect_url: '',
    wechat_connect_frontend_redirect_url: '/auth/wechat/callback',
    oidc_connect_enabled: false,
    oidc_connect_provider_name: 'OIDC',
    oidc_connect_client_id: '',
    oidc_connect_client_secret: '',
    oidc_connect_client_secret_configured: false,
    oidc_connect_issuer_url: '',
    oidc_connect_discovery_url: '',
    oidc_connect_authorize_url: '',
    oidc_connect_token_url: '',
    oidc_connect_userinfo_url: '',
    oidc_connect_jwks_url: '',
    oidc_connect_scopes: 'openid email profile',
    oidc_connect_redirect_url: '',
    oidc_connect_frontend_redirect_url: '/auth/oidc/callback',
    oidc_connect_token_auth_method: 'client_secret_post',
    oidc_connect_use_pkce: false,
    oidc_connect_validate_id_token: false,
    oidc_connect_allowed_signing_algs: 'RS256,ES256,PS256',
    oidc_connect_clock_skew_seconds: 120,
    oidc_connect_require_email_verified: false,
    oidc_connect_userinfo_email_path: '',
    oidc_connect_userinfo_id_path: '',
    oidc_connect_userinfo_username_path: '',
    github_oauth_enabled: false,
    github_oauth_client_id: '',
    github_oauth_client_secret: '',
    github_oauth_client_secret_configured: false,
    github_oauth_redirect_url: '',
    github_oauth_frontend_redirect_url: '/auth/oauth/callback',
    google_oauth_enabled: false,
    google_one_tap_enabled: false,
    google_oauth_client_id: '',
    google_oauth_client_secret: '',
    google_oauth_client_secret_configured: false,
    google_oauth_redirect_url: '',
    google_oauth_frontend_redirect_url: '/auth/oauth/callback',
    // Model fallback
    enable_model_fallback: false,
    fallback_model_anthropic: 'claude-3-5-sonnet-20241022',
    fallback_model_openai: 'gpt-4o',
    fallback_model_gemini: 'gemini-2.5-pro',
    fallback_model_antigravity: 'gemini-2.5-pro',
    grok_default_text_model: 'grok-4.5',
    grok_default_base_url_mode: 'cli',
    // Identity patch (Claude -> Gemini)
    enable_identity_patch: true,
    identity_patch_prompt: '',
    // Ops monitoring (vNext)
    ops_monitoring_enabled: true,
    ops_realtime_monitoring_enabled: true,
    ops_metrics_interval_seconds: 60,
    // Claude Code version check
    min_claude_code_version: '',
    max_claude_code_version: '',
    advanced_scheduler_sticky_weighted_enabled: false,
    advanced_scheduler_subscription_priority_enabled: false,
    advanced_scheduler_ewma_error_rate_alpha: '',
    advanced_scheduler_ewma_ttft_alpha: '',
    advanced_scheduler_sticky_escape_enabled: true,
    advanced_scheduler_sticky_escape_ttft_ms: '',
    advanced_scheduler_sticky_escape_error_rate: '',
    advanced_scheduler_lb_top_k: '',
    advanced_scheduler_weight_priority: '',
    advanced_scheduler_weight_load: '',
    advanced_scheduler_weight_queue: '',
    advanced_scheduler_weight_error_rate: '',
    advanced_scheduler_weight_ttft: '',
    advanced_scheduler_weight_reset: '',
    advanced_scheduler_weight_quota_headroom: '',
    advanced_scheduler_weight_previous_response: '',
    advanced_scheduler_weight_session_sticky: '',
    openai_provider_quota_auto_pause: {
      default_threshold_5h: 0,
      default_threshold_7d: 0,
    },
    // Gateway forwarding behavior
    ttft_mode: 'first_output',
    enable_fingerprint_unification: true,
    enable_metadata_passthrough: false,
    enable_cch_signing: false,
    enable_claude_oauth_system_prompt_injection: true,
    claude_oauth_system_prompt: '',
    claude_oauth_system_prompt_blocks: '',
    enable_anthropic_cache_ttl_1h_injection: false,
    rewrite_message_cache_control: false,
    enable_client_dateline_normalization: true,
    bstats_enabled: true,
    bstats_log_errors: false,
    bstats_log_sent_data: false,
    bstats_log_response_status_text: false,
    team_enabled: true,
    creative_enabled: true,
    creative_model_settings: [] as CreativeModelSetting[],
    creative_worker_count: 128,
    risk_control_enabled: false,
    cyber_session_block_enabled: false,
    cyber_session_block_ttl_seconds: 3600,
    antigravity_user_agent_version: '',
    openai_codex_user_agent: '',
    openai_allow_claude_code_codex_plugin: false,
    user_prompt_replacement_config: defaultUserPromptReplacementConfig(),
    balance_low_notify_enabled: false,
    balance_low_notify_threshold: 0,
    balance_low_notify_recharge_url: '',
    subscription_expiry_notify_enabled: true,
    provider_quota_notify_enabled: false,
    provider_quota_notify_emails: [] as NotifyEmailEntry[],
    allow_user_view_error_requests: false,
  })
  return form
}
