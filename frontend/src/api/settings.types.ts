import type { LocalizedUpdate } from '@/i18n/content'

// settings.types.ts defines public feature settings and localized site content consumed
// during application initialization. Older injected settings can omit newer switches, so
// individual fields document whether absence enables an existing feature or leaves a
// newer feature disabled.

// CustomMenuItem configures a localized navigation destination for users or
// administrators.
export interface CustomMenuItem {
  localization?: LocalizedUpdate<{
    label: string // Human-readable display label.
    url: string // Destination resource URL.
  }> // Localized content versions.
  id: string // Record string identifier.
  label: string // Human-readable display label.
  icon_svg: string // Inline SVG markup.
  url: string // Destination resource URL.
  page_slug?: string // Custom page identifier.
  visibility: 'user' | 'admin' // Permitted navigation audience.
  sort_order: number // Display sorting position.
}

// CustomEndpoint advertises a named API endpoint with localized explanatory text.
export interface CustomEndpoint {
  id?: string // Record string identifier.
  localization?: LocalizedUpdate<{
    name: string // Record display name.
    description: string // Human-readable record description.
  }> // Localized content versions.
  name: string // Record display name.
  endpoint: string // API endpoint path.
  description: string // Human-readable record description.
}

// FooterLink defines one localized footer destination and its displayed label.
export interface FooterLink {
  id?: string // Record string identifier.
  localization?: LocalizedUpdate<{
    label: string // Human-readable display label.
    url: string // Destination resource URL.
  }> // Localized content versions.
  label: string // Human-readable display label.
  url: string // Destination resource URL.
}

// FooterLinkGroup groups footer destinations under a shared localized section title.
export interface FooterLinkGroup {
  id?: string // Record string identifier.
  localization?: LocalizedUpdate<string> // Localized content versions.
  title: string // Displayed section title.
  links: FooterLink[] // Section destination links.
}

// LoginAgreementDocument supplies localized Markdown agreement content shown during
// authentication.
export interface LoginAgreementDocument {
  localization?: LocalizedUpdate<{
    title: string // Displayed section title.
    content_md: string // Agreement Markdown content.
  }> // Localized content versions.
  id: string // Record string identifier.
  title: string // Displayed section title.
  content_md: string // Agreement Markdown content.
}

// PublicSettings describes site presentation and public feature switches used by clients.
export interface PublicSettings {
  locale?: string // Resolved interface locale.
  site_text_overrides?: string[] // Overridden site text keys.
  text_languages?: Record<
    string,
    {
      locale: string | null // Resolved interface locale.
      fallback: boolean // Fallback-language selection flag.
    }
  > // Content language resolution details.
  default_locale?: string // Default content locale.
  site_title?: string // Browser page title.
  registration_enabled: boolean // Account registration availability.
  email_verify_enabled: boolean // Registration email verification switch.
  force_email_on_third_party_signup: boolean // Email requirement for external signup.
  registration_email_suffix_whitelist: string[] // Permitted registration email suffixes.
  registration_email_domain_quota_enabled: boolean // Per-domain registration quota switch.
  user_email_change_enabled: boolean // Whether existing email users may change their primary address.
  promo_code_enabled: boolean // Registration promotion availability.
  password_reset_enabled: boolean // Password recovery availability.
  invitation_code_enabled: boolean // Invitation code availability.
  affiliate_enabled: boolean // Referral program availability.
  login_agreement_enabled?: boolean // Login agreement presentation switch.
  login_agreement_mode?: 'modal' | 'checkbox' | string // Login agreement presentation mode.
  login_agreement_updated_at?: string // Login agreement updated timestamp.
  login_agreement_revision?: string // Current agreement revision identifier.
  login_agreement_documents?: LoginAgreementDocument[] // Published login agreement documents.
  turnstile_enabled: boolean // Turnstile verification availability.
  tencent_captcha_enabled?: boolean // Tencent verification availability.
  tencent_captcha_app_id?: string // Tencent verification application identifier.
  tencent_captcha_region?: string // Tencent verification service region.
  passkey_enabled?: boolean // Passkey authentication availability.
  turnstile_site_key: string // Public Turnstile site key.
  aliyun_captcha_enabled?: boolean // Aliyun verification availability.
  aliyun_captcha_scene_id?: string // Aliyun verification scene identifier.
  aliyun_captcha_prefix?: string // Aliyun verification endpoint prefix.
  aliyun_captcha_region?: string // Aliyun verification service region.
  site_name: string // Site display name.
  site_logo: string // Site logo resource.
  site_share_title?: string // Resolved sharing title.
  site_share_description?: string // Resolved sharing description.
  site_share_image_url?: string // Public cover resource path.
  site_share_image_type?: string // Validated cover MIME type.
  site_share_image_width?: number // Cover width in pixels.
  site_share_image_height?: number // Cover height in pixels.
  frontend_url?: string // Configured frontend base URL.
  site_subtitle: string // Site subtitle text.
  api_base_url: string // Public API base URL.
  contact_info: string // Site contact details.
  doc_url: string // Site documentation URL.
  home_content: string // Homepage content markup.
  hide_ccs_import_button: boolean // CCS import hiding switch.
  payment_enabled: boolean // Payment checkout availability.
  team_enabled?: boolean // Team feature availability.
  team_self_service_enabled?: boolean // Self-service team creation availability.
  // Older public settings may omit creative_enabled; callers disable the creative entry
  // only when its value is false.
  creative_enabled?: boolean // Creative studio availability.
  table_default_page_size: number // Initial table page size.
  table_page_size_options: number[] // Selectable table page sizes.
  usage_ranking_limit: number // Maximum ranking row count.
  // usage_ranking_enabled and the usage_ranking_show_* switches may be absent in older
  // public settings. Callers disable the ranking or its corresponding field only when
  // that switch is false.
  usage_ranking_enabled?: boolean // Usage ranking page availability.
  usage_ranking_sort_by?: 'total_tokens' | 'requests' | 'actual_cost' // Ranking ordering metric.
  usage_ranking_show_total_tokens?: boolean // Token total column visibility.
  usage_ranking_show_requests?: boolean // Request count column visibility.
  usage_ranking_show_actual_cost?: boolean // Actual spending column visibility.
  custom_menu_items: CustomMenuItem[] // Configured navigation destinations.
  custom_endpoints: CustomEndpoint[] // Advertised API endpoints.
  footer_links?: FooterLinkGroup[] // Footer destination groups.
  footer_text?: string // Additional footer text.
  home_featured_models?: string[] // Featured homepage model identifiers.
  linuxdo_oauth_enabled: boolean // Linux.do authentication availability.
  dingtalk_oauth_enabled?: boolean // DingTalk authentication availability.
  wechat_oauth_enabled: boolean // WeChat authentication availability.
  wechat_oauth_open_enabled?: boolean // WeChat Open Platform authentication.
  wechat_oauth_mp_enabled?: boolean // WeChat public-account authentication.
  wechat_oauth_mobile_enabled?: boolean // WeChat mobile authentication availability.
  oidc_oauth_enabled: boolean // OIDC authentication availability.
  oidc_oauth_provider_name: string // OIDC provider display name.
  github_oauth_enabled: boolean // GitHub authentication availability.
  google_oauth_enabled: boolean // Google authentication availability.
  // Older injected HTML caches may omit google_one_tap_enabled; callers treat its absence
  // as disabled.
  google_one_tap_enabled?: boolean // Google One Tap availability.
  google_oauth_client_id?: string // Google OAuth client identifier.
  backend_mode_enabled: boolean // Backend mode activation switch.
  version: string // Running application version.
  // server_timezone and server_utc_offset describe the server's timezone and current
  // offset. Older HTML settings caches may omit both observations, so clients retain
  // optional access to them.
  server_timezone?: string // Server IANA timezone.
  server_utc_offset?: string // Current server UTC offset.
  balance_unit_name: string // Displayed balance unit name.
  balance_unit_symbol: string // Displayed balance unit symbol.
  balance_icon_svg: string // Balance display SVG icon.
  balance_low_notify_enabled: boolean // Low-balance notification availability.
  provider_quota_notify_enabled: boolean // Provider quota notification availability.
  risk_control_enabled: boolean // Risk-control feature availability.
  service_quota_enabled?: boolean // Service quota feature availability.
  balance_low_notify_threshold: number // Site low-balance notification threshold.
  balance_low_notify_recharge_url?: string // Low-balance notification recharge URL.
  allow_user_view_error_requests?: boolean // User request-failure viewing permission.
}
