package site

import (
	"context"
	"fmt"
	"strings"
)

// public_source.go loads public display values and module decisions from one
// settings batch.
// Public settings combine display values with authentication and usage decisions
// derived from the same stored batch. Display values are copied through an
// allowlist before returning, while authentication conversion can inspect the
// additional credential settings needed to decide available browser entry points.

// PublicInputStore loads the settings needed for public responses in one batch.
type PublicInputStore interface {
	// GetMultiple returns raw values for the requested setting keys or an error.
	GetMultiple(context.Context, []string) (map[string]string, error)
}

// PublicInputOptions contains the converters and version source supplied by app
// for public settings responses.
type PublicInputOptions struct {
	// FrontendURL supplies the process-configured address when the database value
	// is blank; nil leaves origin selection to the HTML response renderer.
	FrontendURL func() string // deployment frontend URL fallback

	// Auth and Usage receive the same raw storage map, in that order. InputSource
	// copies display values first, so callback mutations can affect the later
	// converter but cannot rewrite the Values map already prepared for PublicService.
	Auth  func(map[string]string) PublicAuth  // browser authentication conversion
	Usage func(map[string]string) PublicUsage // browser ranking conversion
	// Version supplies the current build label when PublicVersion is invoked;
	// this callback is independent of the stored settings batch used by LoadSitePublicInputs.
	Version func() string
}

// InputSource assembles filtered display values and module-owned decisions from one settings batch.
type InputSource struct {
	store   PublicInputStore   // batched setting reader
	options PublicInputOptions // module conversion callbacks
}

// NewInputSource creates the shared source for public API, HTML, and CSP settings.
// Construction retains dependencies without reading storage. Auth and Usage
// callbacks are required for loading inputs, and Version is required when
// consumers request the build label; missing callbacks are not validated here.
func NewInputSource(store PublicInputStore, options PublicInputOptions) *InputSource {
	return &InputSource{store: store, options: options}
}

// LoadSitePublicInputs returns allowlisted display values and authentication and usage decisions.
// A storage failure aborts loading. The Values map is newly allocated and
// includes each PublicValueKeys entry, while both converters inspect the full
// batch containing any credential settings needed for capability decisions.
func (s *InputSource) LoadSitePublicInputs(ctx context.Context) (PublicInputs, error) {
	raw, err := s.store.GetMultiple(ctx, PublicInputKeys())
	if err != nil {
		return PublicInputs{}, fmt.Errorf("get public settings: %w", err)
	}
	values := make(map[string]string)
	for _, key := range PublicValueKeys() {
		values[key] = raw[key]
	}
	// Database settings override deployment configuration. The fallback callback only
	// runs for a blank stored address, keeping both sources on the existing precedence rule.
	if strings.TrimSpace(values[SettingKeyFrontendURL]) == "" && s.options.FrontendURL != nil {
		values[SettingKeyFrontendURL] = s.options.FrontendURL()
	}
	return PublicInputs{Values: values, Auth: s.options.Auth(raw), Usage: s.options.Usage(raw)}, nil
}

// PublicVersion calls the injected build-label source when its value is requested.
func (s *InputSource) PublicVersion() string { return s.options.Version() }

// PublicInputKeys returns a fresh list of keys needed to assemble public settings,
// including credentials used by the authentication converter.
// InputSource filters those raw values through PublicValueKeys before exposing
// display data, so this storage-read list is broader than the returned Values map.
func PublicInputKeys() []string {
	return []string{
		SettingKeySiteShareImageMeta,
		SettingKeyFrontendURL,
		"balance_unit_name_localized", "oidc_connect_provider_name_localized", "balance_low_notify_recharge_url_localized",
		SettingKeySiteTexts,
		SettingKeyDefaultLocale,
		"site_title",
		"registration_enabled",
		"email_verify_enabled",
		"force_email_on_third_party_signup",
		"registration_email_suffix_whitelist",
		"registration_email_domain_quota_enabled",
		"user_email_change_enabled",
		"promo_code_enabled",
		"password_reset_enabled",
		"invitation_code_enabled",
		"affiliate_enabled",
		"totp_enabled",
		"login_agreement_enabled",
		"login_agreement_mode",
		"login_agreement_updated_at",
		"login_agreement_documents",
		"turnstile_enabled",
		"turnstile_site_key",
		"tencent_captcha_enabled",
		"tencent_captcha_app_id",
		"tencent_captcha_region",
		"aliyun_captcha_enabled",
		"aliyun_captcha_scene_id",
		"aliyun_captcha_prefix",
		"aliyun_captcha_region",
		"api_key_acl_trust_forwarded_ip",
		"site_name",
		"site_logo",
		"site_subtitle",
		"api_base_url",
		"contact_info",
		"doc_url",
		"home_content",
		"hide_ccs_import_button",
		"purchase_subscription_enabled",
		"purchase_subscription_url",
		"table_default_page_size",
		"table_page_size_options",
		"usage_ranking_limit",
		"usage_ranking_enabled",
		"usage_ranking_sort_by",
		"usage_ranking_show_total_tokens",
		"usage_ranking_show_requests",
		"usage_ranking_show_actual_cost",
		"custom_menu_items",
		"custom_endpoints",
		"footer_links",
		"footer_text",
		"home_featured_models",
		"linuxdo_connect_enabled",
		"dingtalk_connect_enabled",
		"wechat_connect_enabled",
		"wechat_connect_app_id",
		"wechat_connect_app_secret",
		"wechat_connect_open_app_id",
		"wechat_connect_open_app_secret",
		"wechat_connect_mp_app_id",
		"wechat_connect_mp_app_secret",
		"wechat_connect_mobile_app_id",
		"wechat_connect_mobile_app_secret",
		"wechat_connect_open_enabled",
		"wechat_connect_mp_enabled",
		"wechat_connect_mobile_enabled",
		"wechat_connect_mode",
		"wechat_connect_scopes",
		"wechat_connect_redirect_url",
		"wechat_connect_frontend_redirect_url",
		"backend_mode_enabled",
		"payment_enabled",
		"oidc_connect_enabled",
		"oidc_connect_provider_name",
		"github_oauth_enabled",
		"github_oauth_client_id",
		"github_oauth_client_secret",
		"github_oauth_redirect_url",
		"github_oauth_frontend_redirect_url",
		"google_oauth_enabled",
		"google_one_tap_enabled",
		"google_oauth_client_id",
		"google_oauth_client_secret",
		"google_oauth_redirect_url",
		"google_oauth_frontend_redirect_url",
		"balance_unit_name",
		"balance_unit_symbol",
		"balance_icon_svg",
		"balance_low_notify_enabled",
		"balance_low_notify_threshold",
		"balance_low_notify_recharge_url",
		"provider_quota_notify_enabled",
		"team_enabled",
		"creative_enabled",
		"risk_control_enabled",
		"allow_user_view_error_requests",
	}
}

// LoadSiteShareImage reads only the encoded cover for the public image endpoint.
// Ordinary public settings load its small descriptor instead. Storage failures
// propagate so callers can distinguish unavailable storage from a missing cover.
func (s *InputSource) LoadSiteShareImage(ctx context.Context) (string, error) {
	values, err := s.store.GetMultiple(ctx, []string{SettingKeySiteShareImage})
	return values[SettingKeySiteShareImage], err
}
