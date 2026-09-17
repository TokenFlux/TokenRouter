package site

import (
	"context"
	"fmt"
)

// PublicInputStore 保持一次批量读取，避免将公开设置拆成逐字段查询。
type PublicInputStore interface {
	GetMultiple(context.Context, []string) (map[string]string, error)
}

// PublicInputOptions 的投影回调由 app 静态组合，不把完整配置或原始敏感值交给 web。
type PublicInputOptions struct {
	Auth    func(map[string]string) PublicAuth
	Usage   func(map[string]string) PublicUsage
	Version func() string
}
type InputSource struct {
	store   PublicInputStore
	options PublicInputOptions
}

// NewInputSource 构造不回源，公开 API/embed/CSP 复用同一来源。
func NewInputSource(store PublicInputStore, options PublicInputOptions) *InputSource {
	return &InputSource{store: store, options: options}
}

// LoadSitePublicInputs 在返回前仅保留公开键，敏感值不会进入 HTML 渲染输入。
func (s *InputSource) LoadSitePublicInputs(ctx context.Context) (PublicInputs, error) {
	raw, err := s.store.GetMultiple(ctx, PublicInputKeys())
	if err != nil {
		return PublicInputs{}, fmt.Errorf("get public settings: %w", err)
	}
	values := make(map[string]string)
	for _, key := range PublicValueKeys() {
		values[key] = raw[key]
	}
	return PublicInputs{Values: values, Auth: s.options.Auth(raw), Usage: s.options.Usage(raw)}, nil
}
func (s *InputSource) PublicVersion() string { return s.options.Version() }

// PublicInputKeys 保留旧公开聚合的查询集合和顺序；返回独立列表。
func PublicInputKeys() []string {
	return []string{
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
		"site_name_zh",
		"site_name_en",
		"site_title_zh",
		"site_title_en",
		"site_subtitle_zh",
		"site_subtitle_en",
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
		"account_quota_notify_enabled",
		"team_enabled",
		"creative_enabled",
		"risk_control_enabled",
		"allow_user_view_error_requests",
	}
}
