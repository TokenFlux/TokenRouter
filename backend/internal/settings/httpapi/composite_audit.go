package httpapi

import (
	"encoding/json"
	"log/slog"
	"reflect"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// composite_audit.go computes changed settings labels and emits the
// administrator update audit event.
// Settings audit records list submitted or changed field names after the
// combined update has committed. Ordinary fields compare the prior readback with
// the prepared Snapshot; credential labels instead reflect nonempty submitted
// secret text, keeping credential content out of the log. Grant changes add
// provider-specific names from the two authentication-default values.
//
// Comparisons follow each stored representation: ordered lists retain positional
// meaning, legal records compare ID/title/body, and provider thresholds compare
// supported platforms with missing entries interpreted as disabled pausing.
// These helpers do not read committed values again, so a logged label describes
// preparation or submission evidence, and a later response read can still fail
// after the audit event is written.

// auditSettingsUpdate logs changed or submitted setting names after successful
// Commit, using authenticated context identity when available. Nil snapshots and
// an empty change list skip logging; the caller passes prepared values and its
// audit request before final response readback.
func (h *Handler) auditSettingsUpdate(c *gin.Context, before *composite.Snapshot, after *composite.Snapshot, beforeAuthSourceDefaults *identity.AuthSourceDefaultSettings, afterAuthSourceDefaults *identity.AuthSourceDefaultSettings, req UpdateSettingsRequest) {
	if before == nil || after == nil {
		return
	}

	changed := DiffSettings(before, after, beforeAuthSourceDefaults, afterAuthSourceDefaults, req)
	if len(changed) == 0 {
		return
	}

	subject, _ := authctx.GetAuthSubjectFromContext(c)
	role, _ := authctx.GetUserRoleFromContext(c)
	slog.Info("settings updated",
		"audit", true,
		"user_id", subject.UserID,
		"role", role,
		"changed", changed,
	)
}

// DiffSettings returns audited field names by comparing nonnil prior and
// prepared Snapshots with submitted credential metadata. Scalar and collection
// comparisons follow the explicit field sequence, while nonempty secret request
// values add credential labels even when the stored secret is unchanged.
//
// The result describes preparation and submission, not a second database read.
// Ordered collection helpers can treat nil and empty alike; legal comparison
// uses ID, title, and body, while supported provider thresholds use 100 for
// missing entries. Authentication-source grant differences are appended last
// with nil grant records interpreted as zero defaults.
func DiffSettings(before *composite.Snapshot, after *composite.Snapshot, beforeAuthSourceDefaults *identity.AuthSourceDefaultSettings, afterAuthSourceDefaults *identity.AuthSourceDefaultSettings, req UpdateSettingsRequest) []string {
	changed := make([]string, 0, 20)
	if before.RegistrationEnabled != after.RegistrationEnabled {
		changed = append(changed, "registration_enabled")
	}
	if before.EmailVerifyEnabled != after.EmailVerifyEnabled {
		changed = append(changed, "email_verify_enabled")
	}
	if !equalStringSlice(before.RegistrationEmailSuffixWhitelist, after.RegistrationEmailSuffixWhitelist) {
		changed = append(changed, "registration_email_suffix_whitelist")
	}
	if before.RegistrationEmailNormalization != after.RegistrationEmailNormalization {
		changed = append(changed, "registration_email_normalization")
	}
	if before.RegistrationEmailDomainQuotaEnabled != after.RegistrationEmailDomainQuotaEnabled {
		changed = append(changed, "registration_email_domain_quota_enabled")
	}
	if before.UserEmailChangeEnabled != after.UserEmailChangeEnabled {
		changed = append(changed, "user_email_change_enabled")
	}
	if before.PromoCodeEnabled != after.PromoCodeEnabled {
		changed = append(changed, "promo_code_enabled")
	}
	if before.InvitationCodeEnabled != after.InvitationCodeEnabled {
		changed = append(changed, "invitation_code_enabled")
	}
	if before.PasswordResetEnabled != after.PasswordResetEnabled {
		changed = append(changed, "password_reset_enabled")
	}
	if before.BStatsEnabled != after.BStatsEnabled {
		changed = append(changed, "bstats_enabled")
	}
	if before.BStatsLogErrors != after.BStatsLogErrors {
		changed = append(changed, "bstats_log_errors")
	}
	if before.BStatsLogSentData != after.BStatsLogSentData {
		changed = append(changed, "bstats_log_sent_data")
	}
	if before.BStatsLogResponseStatusText != after.BStatsLogResponseStatusText {
		changed = append(changed, "bstats_log_response_status_text")
	}
	if before.TeamEnabled != after.TeamEnabled {
		changed = append(changed, "team_enabled")
	}
	if before.CreativeEnabled != after.CreativeEnabled {
		changed = append(changed, "creative_enabled")
	}
	if !reflect.DeepEqual(before.CreativeModelSettings, after.CreativeModelSettings) {
		changed = append(changed, "creative_model_settings")
	}
	if before.CreativeWorkerCount != after.CreativeWorkerCount {
		changed = append(changed, "creative_worker_count")
	}
	if before.RiskControlEnabled != after.RiskControlEnabled {
		changed = append(changed, "risk_control_enabled")
	}
	if before.CyberSessionBlockEnabled != after.CyberSessionBlockEnabled {
		changed = append(changed, "cyber_session_block_enabled")
	}
	if before.CyberSessionBlockTTLSeconds != after.CyberSessionBlockTTLSeconds {
		changed = append(changed, "cyber_session_block_ttl_seconds")
	}
	if before.FrontendURL != after.FrontendURL {
		changed = append(changed, "frontend_url")
	}
	if before.BalanceUnitName != after.BalanceUnitName {
		changed = append(changed, "balance_unit_name")
	}
	if before.BalanceUnitSymbol != after.BalanceUnitSymbol {
		changed = append(changed, "balance_unit_symbol")
	}
	if before.BalanceIconSVG != after.BalanceIconSVG {
		changed = append(changed, "balance_icon_svg")
	}
	if before.ReasoningPointRMBUnitPrice != after.ReasoningPointRMBUnitPrice {
		changed = append(changed, "reasoning_point_rmb_unit_price")
	}
	if before.USDExchangeRate != after.USDExchangeRate {
		changed = append(changed, "usd_exchange_rate")
	}
	if before.MarketplaceAvailabilityWindowDays != after.MarketplaceAvailabilityWindowDays {
		changed = append(changed, "marketplace_availability_window_days")
	}
	if before.MarketplaceAvailabilityBucketMinutes != after.MarketplaceAvailabilityBucketMinutes {
		changed = append(changed, "marketplace_availability_bucket_minutes")
	}
	if before.TotpEnabled != after.TotpEnabled {
		changed = append(changed, "totp_enabled")
	}
	if before.SessionBindingEnabled != after.SessionBindingEnabled {
		changed = append(changed, "session_binding_enabled")
	}
	if before.StepUpEnabled != after.StepUpEnabled {
		changed = append(changed, "step_up_enabled")
	}
	if before.LoginAgreementEnabled != after.LoginAgreementEnabled {
		changed = append(changed, "login_agreement_enabled")
	}
	if before.LoginAgreementMode != after.LoginAgreementMode {
		changed = append(changed, "login_agreement_mode")
	}
	if before.LoginAgreementUpdatedAt != after.LoginAgreementUpdatedAt {
		changed = append(changed, "login_agreement_updated_at")
	}
	if !equalLoginAgreementDocuments(before.LoginAgreementDocuments, after.LoginAgreementDocuments) {
		changed = append(changed, "login_agreement_documents")
	}
	if before.SMTPHost != after.SMTPHost {
		changed = append(changed, "smtp_host")
	}
	if before.SMTPPort != after.SMTPPort {
		changed = append(changed, "smtp_port")
	}
	if before.SMTPUsername != after.SMTPUsername {
		changed = append(changed, "smtp_username")
	}
	if req.SMTPPassword != "" {
		changed = append(changed, "smtp_password")
	}
	if before.SMTPFrom != after.SMTPFrom {
		changed = append(changed, "smtp_from_email")
	}
	if before.SMTPFromName != after.SMTPFromName {
		changed = append(changed, "smtp_from_name")
	}
	if before.SMTPUseTLS != after.SMTPUseTLS {
		changed = append(changed, "smtp_use_tls")
	}
	if before.TurnstileEnabled != after.TurnstileEnabled {
		changed = append(changed, "turnstile_enabled")
	}
	if before.TurnstileSiteKey != after.TurnstileSiteKey {
		changed = append(changed, "turnstile_site_key")
	}
	if req.TurnstileSecretKey != "" {
		changed = append(changed, "turnstile_secret_key")
	}
	if before.TencentCaptchaEnabled != after.TencentCaptchaEnabled {
		changed = append(changed, "tencent_captcha_enabled")
	}
	if before.TencentCaptchaAppID != after.TencentCaptchaAppID {
		changed = append(changed, "tencent_captcha_app_id")
	}
	if req.TencentCaptchaAppSecretKey != "" {
		changed = append(changed, "tencent_captcha_app_secret_key")
	}
	if req.TencentCaptchaCloudSecretID != "" {
		changed = append(changed, "tencent_captcha_cloud_secret_id")
	}
	if req.TencentCaptchaCloudSecretKey != "" {
		changed = append(changed, "tencent_captcha_cloud_secret_key")
	}
	if before.TencentCaptchaRegion != after.TencentCaptchaRegion {
		changed = append(changed, "tencent_captcha_region")
	}
	if before.AliyunCaptchaEnabled != after.AliyunCaptchaEnabled {
		changed = append(changed, "aliyun_captcha_enabled")
	}
	if before.AliyunCaptchaAccessKeyID != after.AliyunCaptchaAccessKeyID {
		changed = append(changed, "aliyun_captcha_access_key_id")
	}
	if req.AliyunCaptchaAccessKeySecret != "" {
		changed = append(changed, "aliyun_captcha_access_key_secret")
	}
	if before.AliyunCaptchaSceneID != after.AliyunCaptchaSceneID {
		changed = append(changed, "aliyun_captcha_scene_id")
	}
	if before.AliyunCaptchaPrefix != after.AliyunCaptchaPrefix {
		changed = append(changed, "aliyun_captcha_prefix")
	}
	if before.AliyunCaptchaRegion != after.AliyunCaptchaRegion {
		changed = append(changed, "aliyun_captcha_region")
	}
	if before.APIKeyACLTrustForwardedIP != after.APIKeyACLTrustForwardedIP {
		changed = append(changed, "api_key_acl_trust_forwarded_ip")
	}
	if !equalStringSlice(before.ForwardedClientIPHeaders, after.ForwardedClientIPHeaders) {
		changed = append(changed, "forwarded_client_ip_headers")
	}
	if before.LinuxDoConnectEnabled != after.LinuxDoConnectEnabled {
		changed = append(changed, "linuxdo_connect_enabled")
	}
	if before.LinuxDoConnectClientID != after.LinuxDoConnectClientID {
		changed = append(changed, "linuxdo_connect_client_id")
	}
	if req.LinuxDoConnectClientSecret != "" {
		changed = append(changed, "linuxdo_connect_client_secret")
	}
	if before.LinuxDoConnectRedirectURL != after.LinuxDoConnectRedirectURL {
		changed = append(changed, "linuxdo_connect_redirect_url")
	}
	if before.DingTalkConnectEnabled != after.DingTalkConnectEnabled {
		changed = append(changed, "dingtalk_connect_enabled")
	}
	if before.DingTalkConnectClientID != after.DingTalkConnectClientID {
		changed = append(changed, "dingtalk_connect_client_id")
	}
	if req.DingTalkConnectClientSecret != "" {
		changed = append(changed, "dingtalk_connect_client_secret")
	}
	if before.DingTalkConnectRedirectURL != after.DingTalkConnectRedirectURL {
		changed = append(changed, "dingtalk_connect_redirect_url")
	}
	if before.DingTalkConnectCorpRestrictionPolicy != after.DingTalkConnectCorpRestrictionPolicy {
		changed = append(changed, "dingtalk_connect_corp_restriction_policy")
	}
	if before.DingTalkConnectInternalCorpID != after.DingTalkConnectInternalCorpID {
		changed = append(changed, "dingtalk_connect_internal_corp_id")
	}
	if before.DingTalkConnectBypassRegistration != after.DingTalkConnectBypassRegistration {
		changed = append(changed, "dingtalk_connect_bypass_registration")
	}
	if before.DingTalkConnectSyncCorpEmail != after.DingTalkConnectSyncCorpEmail {
		changed = append(changed, "dingtalk_connect_sync_corp_email")
	}
	if before.DingTalkConnectSyncDisplayName != after.DingTalkConnectSyncDisplayName {
		changed = append(changed, "dingtalk_connect_sync_display_name")
	}
	if before.DingTalkConnectSyncDept != after.DingTalkConnectSyncDept {
		changed = append(changed, "dingtalk_connect_sync_dept")
	}
	if before.DingTalkConnectSyncCorpEmailAttrKey != after.DingTalkConnectSyncCorpEmailAttrKey {
		changed = append(changed, "dingtalk_connect_sync_corp_email_attr_key")
	}
	if before.DingTalkConnectSyncDisplayNameAttrKey != after.DingTalkConnectSyncDisplayNameAttrKey {
		changed = append(changed, "dingtalk_connect_sync_display_name_attr_key")
	}
	if before.DingTalkConnectSyncDeptAttrKey != after.DingTalkConnectSyncDeptAttrKey {
		changed = append(changed, "dingtalk_connect_sync_dept_attr_key")
	}
	if before.WeChatConnectEnabled != after.WeChatConnectEnabled {
		changed = append(changed, "wechat_connect_enabled")
	}
	if before.WeChatConnectAppID != after.WeChatConnectAppID {
		changed = append(changed, "wechat_connect_app_id")
	}
	if req.WeChatConnectAppSecret != "" {
		changed = append(changed, "wechat_connect_app_secret")
	}
	if before.WeChatConnectOpenAppID != after.WeChatConnectOpenAppID {
		changed = append(changed, "wechat_connect_open_app_id")
	}
	if req.WeChatConnectOpenAppSecret != "" {
		changed = append(changed, "wechat_connect_open_app_secret")
	}
	if before.WeChatConnectMPAppID != after.WeChatConnectMPAppID {
		changed = append(changed, "wechat_connect_mp_app_id")
	}
	if req.WeChatConnectMPAppSecret != "" {
		changed = append(changed, "wechat_connect_mp_app_secret")
	}
	if before.WeChatConnectMobileAppID != after.WeChatConnectMobileAppID {
		changed = append(changed, "wechat_connect_mobile_app_id")
	}
	if req.WeChatConnectMobileAppSecret != "" {
		changed = append(changed, "wechat_connect_mobile_app_secret")
	}
	if before.WeChatConnectOpenEnabled != after.WeChatConnectOpenEnabled {
		changed = append(changed, "wechat_connect_open_enabled")
	}
	if before.WeChatConnectMPEnabled != after.WeChatConnectMPEnabled {
		changed = append(changed, "wechat_connect_mp_enabled")
	}
	if before.WeChatConnectMobileEnabled != after.WeChatConnectMobileEnabled {
		changed = append(changed, "wechat_connect_mobile_enabled")
	}
	if before.WeChatConnectMode != after.WeChatConnectMode {
		changed = append(changed, "wechat_connect_mode")
	}
	if before.WeChatConnectScopes != after.WeChatConnectScopes {
		changed = append(changed, "wechat_connect_scopes")
	}
	if before.WeChatConnectRedirectURL != after.WeChatConnectRedirectURL {
		changed = append(changed, "wechat_connect_redirect_url")
	}
	if before.WeChatConnectFrontendRedirectURL != after.WeChatConnectFrontendRedirectURL {
		changed = append(changed, "wechat_connect_frontend_redirect_url")
	}
	if before.OIDCConnectEnabled != after.OIDCConnectEnabled {
		changed = append(changed, "oidc_connect_enabled")
	}
	if before.OIDCConnectProviderName != after.OIDCConnectProviderName {
		changed = append(changed, "oidc_connect_provider_name")
	}
	if before.OIDCConnectClientID != after.OIDCConnectClientID {
		changed = append(changed, "oidc_connect_client_id")
	}
	if req.OIDCConnectClientSecret != "" {
		changed = append(changed, "oidc_connect_client_secret")
	}
	if before.OIDCConnectIssuerURL != after.OIDCConnectIssuerURL {
		changed = append(changed, "oidc_connect_issuer_url")
	}
	if before.OIDCConnectDiscoveryURL != after.OIDCConnectDiscoveryURL {
		changed = append(changed, "oidc_connect_discovery_url")
	}
	if before.OIDCConnectAuthorizeURL != after.OIDCConnectAuthorizeURL {
		changed = append(changed, "oidc_connect_authorize_url")
	}
	if before.OIDCConnectTokenURL != after.OIDCConnectTokenURL {
		changed = append(changed, "oidc_connect_token_url")
	}
	if before.OIDCConnectUserInfoURL != after.OIDCConnectUserInfoURL {
		changed = append(changed, "oidc_connect_userinfo_url")
	}
	if before.OIDCConnectJWKSURL != after.OIDCConnectJWKSURL {
		changed = append(changed, "oidc_connect_jwks_url")
	}
	if before.OIDCConnectScopes != after.OIDCConnectScopes {
		changed = append(changed, "oidc_connect_scopes")
	}
	if before.OIDCConnectRedirectURL != after.OIDCConnectRedirectURL {
		changed = append(changed, "oidc_connect_redirect_url")
	}
	if before.OIDCConnectFrontendRedirectURL != after.OIDCConnectFrontendRedirectURL {
		changed = append(changed, "oidc_connect_frontend_redirect_url")
	}
	if before.OIDCConnectTokenAuthMethod != after.OIDCConnectTokenAuthMethod {
		changed = append(changed, "oidc_connect_token_auth_method")
	}
	if before.OIDCConnectUsePKCE != after.OIDCConnectUsePKCE {
		changed = append(changed, "oidc_connect_use_pkce")
	}
	if before.OIDCConnectValidateIDToken != after.OIDCConnectValidateIDToken {
		changed = append(changed, "oidc_connect_validate_id_token")
	}
	if before.OIDCConnectAllowedSigningAlgs != after.OIDCConnectAllowedSigningAlgs {
		changed = append(changed, "oidc_connect_allowed_signing_algs")
	}
	if before.OIDCConnectClockSkewSeconds != after.OIDCConnectClockSkewSeconds {
		changed = append(changed, "oidc_connect_clock_skew_seconds")
	}
	if before.OIDCConnectRequireEmailVerified != after.OIDCConnectRequireEmailVerified {
		changed = append(changed, "oidc_connect_require_email_verified")
	}
	if before.OIDCConnectUserInfoEmailPath != after.OIDCConnectUserInfoEmailPath {
		changed = append(changed, "oidc_connect_userinfo_email_path")
	}
	if before.OIDCConnectUserInfoIDPath != after.OIDCConnectUserInfoIDPath {
		changed = append(changed, "oidc_connect_userinfo_id_path")
	}
	if before.OIDCConnectUserInfoUsernamePath != after.OIDCConnectUserInfoUsernamePath {
		changed = append(changed, "oidc_connect_userinfo_username_path")
	}
	if before.GitHubOAuthEnabled != after.GitHubOAuthEnabled {
		changed = append(changed, "github_oauth_enabled")
	}
	if before.GitHubOAuthClientID != after.GitHubOAuthClientID {
		changed = append(changed, "github_oauth_client_id")
	}
	if req.GitHubOAuthClientSecret != "" {
		changed = append(changed, "github_oauth_client_secret")
	}
	if before.GitHubOAuthRedirectURL != after.GitHubOAuthRedirectURL {
		changed = append(changed, "github_oauth_redirect_url")
	}
	if before.GitHubOAuthFrontendRedirectURL != after.GitHubOAuthFrontendRedirectURL {
		changed = append(changed, "github_oauth_frontend_redirect_url")
	}
	if before.GoogleOAuthEnabled != after.GoogleOAuthEnabled {
		changed = append(changed, "google_oauth_enabled")
	}
	if before.GoogleOneTapEnabled != after.GoogleOneTapEnabled {
		changed = append(changed, "google_one_tap_enabled")
	}
	if before.GoogleOAuthClientID != after.GoogleOAuthClientID {
		changed = append(changed, "google_oauth_client_id")
	}
	if req.GoogleOAuthClientSecret != "" {
		changed = append(changed, "google_oauth_client_secret")
	}
	if before.GoogleOAuthRedirectURL != after.GoogleOAuthRedirectURL {
		changed = append(changed, "google_oauth_redirect_url")
	}
	if before.GoogleOAuthFrontendRedirectURL != after.GoogleOAuthFrontendRedirectURL {
		changed = append(changed, "google_oauth_frontend_redirect_url")
	}
	if before.SiteName != after.SiteName {
		changed = append(changed, "site_name")
	}
	if before.SiteLogo != after.SiteLogo {
		changed = append(changed, "site_logo")
	}
	if !reflect.DeepEqual(before.SiteTexts, after.SiteTexts) {
		changed = append(changed, "site_texts")
	}
	if before.DefaultLocale != after.DefaultLocale {
		changed = append(changed, "default_locale")
	}
	if before.SiteSubtitle != after.SiteSubtitle {
		changed = append(changed, "site_subtitle")
	}
	if before.APIBaseURL != after.APIBaseURL {
		changed = append(changed, "api_base_url")
	}
	if before.ContactInfo != after.ContactInfo {
		changed = append(changed, "contact_info")
	}
	if before.DocURL != after.DocURL {
		changed = append(changed, "doc_url")
	}
	if before.HomeContent != after.HomeContent {
		changed = append(changed, "home_content")
	}
	if before.HideCcsImportButton != after.HideCcsImportButton {
		changed = append(changed, "hide_ccs_import_button")
	}
	if before.DefaultConcurrency != after.DefaultConcurrency {
		changed = append(changed, "default_concurrency")
	}
	if before.DefaultBalance != after.DefaultBalance {
		changed = append(changed, "default_balance")
	}
	if before.DefaultUserAPIKeyLimit != after.DefaultUserAPIKeyLimit {
		changed = append(changed, identity.SettingKeyDefaultUserAPIKeyLimit)
	}
	if before.AffiliateEnabled != after.AffiliateEnabled {
		changed = append(changed, "affiliate_enabled")
	}
	if before.AffiliateRebateRate != after.AffiliateRebateRate {
		changed = append(changed, "affiliate_rebate_rate")
	}
	if before.AffiliateRebateFreezeHours != after.AffiliateRebateFreezeHours {
		changed = append(changed, "affiliate_rebate_freeze_hours")
	}
	if before.AffiliateRebateDurationDays != after.AffiliateRebateDurationDays {
		changed = append(changed, "affiliate_rebate_duration_days")
	}
	if before.AffiliateRebatePerInviteeCap != after.AffiliateRebatePerInviteeCap {
		changed = append(changed, "affiliate_rebate_per_invitee_cap")
	}
	if before.AdminRechargeRebateEnabled != after.AdminRechargeRebateEnabled {
		changed = append(changed, "affiliate_admin_recharge_enabled")
	}
	if !equalDefaultSubscriptions(before.DefaultSubscriptions, after.DefaultSubscriptions) {
		changed = append(changed, "default_subscriptions")
	}
	if before.EnableModelFallback != after.EnableModelFallback {
		changed = append(changed, "enable_model_fallback")
	}
	if before.FallbackModelAnthropic != after.FallbackModelAnthropic {
		changed = append(changed, "fallback_model_anthropic")
	}
	if before.FallbackModelOpenAI != after.FallbackModelOpenAI {
		changed = append(changed, "fallback_model_openai")
	}
	if before.FallbackModelGemini != after.FallbackModelGemini {
		changed = append(changed, "fallback_model_gemini")
	}
	if before.FallbackModelAntigravity != after.FallbackModelAntigravity {
		changed = append(changed, "fallback_model_antigravity")
	}
	if before.EnableIdentityPatch != after.EnableIdentityPatch {
		changed = append(changed, "enable_identity_patch")
	}
	if before.IdentityPatchPrompt != after.IdentityPatchPrompt {
		changed = append(changed, "identity_patch_prompt")
	}
	if before.OpsMonitoringEnabled != after.OpsMonitoringEnabled {
		changed = append(changed, "ops_monitoring_enabled")
	}
	if before.OpsRealtimeMonitoringEnabled != after.OpsRealtimeMonitoringEnabled {
		changed = append(changed, "ops_realtime_monitoring_enabled")
	}
	if before.OpsMetricsIntervalSeconds != after.OpsMetricsIntervalSeconds {
		changed = append(changed, "ops_metrics_interval_seconds")
	}
	if before.MinClaudeCodeVersion != after.MinClaudeCodeVersion {
		changed = append(changed, "min_claude_code_version")
	}
	if before.MaxClaudeCodeVersion != after.MaxClaudeCodeVersion {
		changed = append(changed, "max_claude_code_version")
	}
	if before.BackendModeEnabled != after.BackendModeEnabled {
		changed = append(changed, "backend_mode_enabled")
	}
	if before.PurchaseSubscriptionEnabled != after.PurchaseSubscriptionEnabled {
		changed = append(changed, "purchase_subscription_enabled")
	}
	if before.PurchaseSubscriptionURL != after.PurchaseSubscriptionURL {
		changed = append(changed, "purchase_subscription_url")
	}
	if before.TableDefaultPageSize != after.TableDefaultPageSize {
		changed = append(changed, "table_default_page_size")
	}
	if !equalIntSlice(before.TablePageSizeOptions, after.TablePageSizeOptions) {
		changed = append(changed, "table_page_size_options")
	}
	if before.UsageRankingLimit != after.UsageRankingLimit {
		changed = append(changed, "usage_ranking_limit")
	}
	if before.UsageRankingEnabled != after.UsageRankingEnabled {
		changed = append(changed, "usage_ranking_enabled")
	}
	if before.UsageRankingSortBy != after.UsageRankingSortBy {
		changed = append(changed, "usage_ranking_sort_by")
	}
	if before.UsageRankingShowTotalTokens != after.UsageRankingShowTotalTokens {
		changed = append(changed, "usage_ranking_show_total_tokens")
	}
	if before.UsageRankingShowRequests != after.UsageRankingShowRequests {
		changed = append(changed, "usage_ranking_show_requests")
	}
	if before.UsageRankingShowActualCost != after.UsageRankingShowActualCost {
		changed = append(changed, "usage_ranking_show_actual_cost")
	}
	if before.CustomMenuItems != after.CustomMenuItems {
		changed = append(changed, "custom_menu_items")
	}
	if before.CustomEndpoints != after.CustomEndpoints {
		changed = append(changed, "custom_endpoints")
	}
	if before.FooterLinks != after.FooterLinks {
		changed = append(changed, "footer_links")
	}
	if before.FooterText != after.FooterText {
		changed = append(changed, "footer_text")
	}
	if before.HomeFeaturedModels != after.HomeFeaturedModels {
		changed = append(changed, "home_featured_models")
	}
	if before.EnableFingerprintUnification != after.EnableFingerprintUnification {
		changed = append(changed, "enable_fingerprint_unification")
	}
	if before.TTFTMode != after.TTFTMode {
		changed = append(changed, "ttft_mode")
	}
	if before.EnableMetadataPassthrough != after.EnableMetadataPassthrough {
		changed = append(changed, "enable_metadata_passthrough")
	}
	if before.EnableCCHSigning != after.EnableCCHSigning {
		changed = append(changed, "enable_cch_signing")
	}
	if before.EnableClaudeOAuthSystemPromptInjection != after.EnableClaudeOAuthSystemPromptInjection {
		changed = append(changed, "enable_claude_oauth_system_prompt_injection")
	}
	if before.ClaudeOAuthSystemPrompt != after.ClaudeOAuthSystemPrompt {
		changed = append(changed, "claude_oauth_system_prompt")
	}
	if before.ClaudeOAuthSystemPromptBlocks != after.ClaudeOAuthSystemPromptBlocks {
		changed = append(changed, "claude_oauth_system_prompt_blocks")
	}
	if before.EnableAnthropicCacheTTL1hInjection != after.EnableAnthropicCacheTTL1hInjection {
		changed = append(changed, "enable_anthropic_cache_ttl_1h_injection")
	}
	if before.RewriteMessageCacheControl != after.RewriteMessageCacheControl {
		changed = append(changed, "rewrite_message_cache_control")
	}
	if before.EnableClientDatelineNormalization != after.EnableClientDatelineNormalization {
		changed = append(changed, "enable_client_dateline_normalization")
	}
	if before.AntigravityUserAgentVersion != after.AntigravityUserAgentVersion {
		changed = append(changed, "antigravity_user_agent_version")
	}
	if before.OpenAICodexUserAgent != after.OpenAICodexUserAgent {
		changed = append(changed, "openai_codex_user_agent")
	}
	if before.OpenAIAllowClaudeCodeCodexPlugin != after.OpenAIAllowClaudeCodeCodexPlugin {
		changed = append(changed, "openai_allow_claude_code_codex_plugin")
	}
	if !equalUserPromptReplacementConfig(before.UserPromptReplacementConfig, after.UserPromptReplacementConfig) {
		changed = append(changed, "user_prompt_replacement_config")
	}
	if before.PaymentVisibleMethodAlipaySource != after.PaymentVisibleMethodAlipaySource {
		changed = append(changed, "payment_visible_method_alipay_source")
	}
	if before.PaymentVisibleMethodWxpaySource != after.PaymentVisibleMethodWxpaySource {
		changed = append(changed, "payment_visible_method_wxpay_source")
	}
	if before.PaymentVisibleMethodAlipayEnabled != after.PaymentVisibleMethodAlipayEnabled {
		changed = append(changed, "payment_visible_method_alipay_enabled")
	}
	if before.PaymentVisibleMethodWxpayEnabled != after.PaymentVisibleMethodWxpayEnabled {
		changed = append(changed, "payment_visible_method_wxpay_enabled")
	}
	if before.AdvancedSchedulerStickyWeightedEnabled != after.AdvancedSchedulerStickyWeightedEnabled {
		changed = append(changed, "advanced_scheduler_sticky_weighted_enabled")
	}
	if before.AdvancedSchedulerSubscriptionPriorityEnabled != after.AdvancedSchedulerSubscriptionPriorityEnabled {
		changed = append(changed, "advanced_scheduler_subscription_priority_enabled")
	}
	if before.AdvancedSchedulerEWMAErrorRateAlpha != after.AdvancedSchedulerEWMAErrorRateAlpha {
		changed = append(changed, "advanced_scheduler_ewma_error_rate_alpha")
	}
	if before.AdvancedSchedulerEWMATTFTAlpha != after.AdvancedSchedulerEWMATTFTAlpha {
		changed = append(changed, "advanced_scheduler_ewma_ttft_alpha")
	}
	if before.AdvancedSchedulerStickyEscapeEnabled != after.AdvancedSchedulerStickyEscapeEnabled {
		changed = append(changed, "advanced_scheduler_sticky_escape_enabled")
	}
	if before.AdvancedSchedulerStickyEscapeTTFTMs != after.AdvancedSchedulerStickyEscapeTTFTMs {
		changed = append(changed, "advanced_scheduler_sticky_escape_ttft_ms")
	}
	if before.AdvancedSchedulerStickyEscapeErrorRate != after.AdvancedSchedulerStickyEscapeErrorRate {
		changed = append(changed, "advanced_scheduler_sticky_escape_error_rate")
	}
	if before.AdvancedSchedulerLBTopK != after.AdvancedSchedulerLBTopK {
		changed = append(changed, "advanced_scheduler_lb_top_k")
	}
	if before.AdvancedSchedulerWeightPriority != after.AdvancedSchedulerWeightPriority {
		changed = append(changed, "advanced_scheduler_weight_priority")
	}
	if before.AdvancedSchedulerWeightLoad != after.AdvancedSchedulerWeightLoad {
		changed = append(changed, "advanced_scheduler_weight_load")
	}
	if before.AdvancedSchedulerWeightQueue != after.AdvancedSchedulerWeightQueue {
		changed = append(changed, "advanced_scheduler_weight_queue")
	}
	if before.AdvancedSchedulerWeightErrorRate != after.AdvancedSchedulerWeightErrorRate {
		changed = append(changed, "advanced_scheduler_weight_error_rate")
	}
	if before.AdvancedSchedulerWeightTTFT != after.AdvancedSchedulerWeightTTFT {
		changed = append(changed, "advanced_scheduler_weight_ttft")
	}
	if before.AdvancedSchedulerWeightReset != after.AdvancedSchedulerWeightReset {
		changed = append(changed, "advanced_scheduler_weight_reset")
	}
	if before.AdvancedSchedulerWeightQuotaHeadroom != after.AdvancedSchedulerWeightQuotaHeadroom {
		changed = append(changed, "advanced_scheduler_weight_quota_headroom")
	}
	if before.AdvancedSchedulerWeightPreviousResponse != after.AdvancedSchedulerWeightPreviousResponse {
		changed = append(changed, "advanced_scheduler_weight_previous_response")
	}
	if before.AdvancedSchedulerWeightSessionSticky != after.AdvancedSchedulerWeightSessionSticky {
		changed = append(changed, "advanced_scheduler_weight_session_sticky")
	}
	// Compare both global window defaults as one audited setting because these
	// fractions are saved together in the quota object within Ops advanced JSON.
	if before.OpenAIQuotaAutoPauseSettings.DefaultThreshold5h != after.OpenAIQuotaAutoPauseSettings.DefaultThreshold5h ||
		before.OpenAIQuotaAutoPauseSettings.DefaultThreshold7d != after.OpenAIQuotaAutoPauseSettings.DefaultThreshold7d {
		changed = append(changed, "openai_provider_quota_auto_pause")
	}
	// Compare notification controls after the quota object so their labels retain
	// the audit field sequence while recipient eligibility flags are checked by their helper.
	if before.BalanceLowNotifyEnabled != after.BalanceLowNotifyEnabled {
		changed = append(changed, "balance_low_notify_enabled")
	}
	if before.BalanceLowNotifyThreshold != after.BalanceLowNotifyThreshold {
		changed = append(changed, "balance_low_notify_threshold")
	}
	if before.BalanceLowNotifyRechargeURL != after.BalanceLowNotifyRechargeURL {
		changed = append(changed, "balance_low_notify_recharge_url")
	}
	if before.SubscriptionExpiryNotifyEnabled != after.SubscriptionExpiryNotifyEnabled {
		changed = append(changed, "subscription_expiry_notify_enabled")
	}
	if before.ProviderQuotaNotifyEnabled != after.ProviderQuotaNotifyEnabled {
		changed = append(changed, "provider_quota_notify_enabled")
	}
	if !equalNotifyEmailEntries(before.ProviderQuotaNotifyEmails, after.ProviderQuotaNotifyEmails) {
		changed = append(changed, "provider_quota_notify_emails")
	}
	if !equalProviderSchedulingThresholds(before.ProviderSchedulingThresholds, after.ProviderSchedulingThresholds) {
		changed = append(changed, provider.SettingKeyProviderSchedulingThresholds)
	}
	changed = AppendAuthSourceDefaultChanges(changed, beforeAuthSourceDefaults, afterAuthSourceDefaults)
	return changed
}

// AppendAuthSourceDefaultChanges appends provider-specific grant labels to the
// supplied list in its fixed source order. Nil grant values become zero defaults;
// callers use the returned slice because append can allocate or retain its existing
// backing storage.
func AppendAuthSourceDefaultChanges(changed []string, before *identity.AuthSourceDefaultSettings, after *identity.AuthSourceDefaultSettings) []string {
	if before == nil {
		before = &identity.AuthSourceDefaultSettings{}
	}
	if after == nil {
		after = &identity.AuthSourceDefaultSettings{}
	}

	// providerDefaultGrantField pairs one authentication source's before and after
	// grant values so audit labels retain the same source name during comparison.
	type providerDefaultGrantField struct {
		name   string                                // authentication source audit name
		before identity.ProviderDefaultGrantSettings // grants from prior settings
		after  identity.ProviderDefaultGrantSettings // grants from prepared update
	}

	fields := []providerDefaultGrantField{
		{name: "email", before: before.Email, after: after.Email},
		{name: "linuxdo", before: before.LinuxDo, after: after.LinuxDo},
		{name: "oidc", before: before.OIDC, after: after.OIDC},
		{name: "wechat", before: before.WeChat, after: after.WeChat},
		{name: "github", before: before.GitHub, after: after.GitHub},
		{name: "google", before: before.Google, after: after.Google},
		{name: "dingtalk", before: before.DingTalk, after: after.DingTalk},
	}
	for _, field := range fields {
		if field.before.Balance != field.after.Balance {
			changed = append(changed, "auth_source_default_"+field.name+"_balance")
		}
		if field.before.Concurrency != field.after.Concurrency {
			changed = append(changed, "auth_source_default_"+field.name+"_concurrency")
		}
		if !equalDefaultSubscriptions(field.before.Subscriptions, field.after.Subscriptions) {
			changed = append(changed, "auth_source_default_"+field.name+"_subscriptions")
		}
		if field.before.GrantOnSignup != field.after.GrantOnSignup {
			changed = append(changed, "auth_source_default_"+field.name+"_grant_on_signup")
		}
		if field.before.GrantOnFirstBind != field.after.GrantOnFirstBind {
			changed = append(changed, "auth_source_default_"+field.name+"_grant_on_first_bind")
		}
	}
	if before.ForceEmailOnThirdPartySignup != after.ForceEmailOnThirdPartySignup {
		changed = append(changed, "force_email_on_third_party_signup")
	}
	return changed
}

// normalizeDefaultSubscriptions copies positive plan selections in supplied
// order, keeping duplicates for later billing validation. Empty input returns nil;
// a nonempty list containing no positive IDs returns an allocated empty list.
func normalizeDefaultSubscriptions(input []billinghttp.DefaultSubscriptionSetting) []billinghttp.DefaultSubscriptionSetting {
	if len(input) == 0 {
		return nil
	}
	normalized := make([]billinghttp.DefaultSubscriptionSetting, 0, len(input))
	// Discard nonpositive editor selections before the retained list reaches
	// billing's duplicate checks and optional saved-plan lookup validation.
	for _, item := range input {
		if item.PlanID <= 0 {
			continue
		}
		normalized = append(normalized, item)
	}
	return normalized
}

// normalizeOptionalDefaultSubscriptions keeps a nil request pointer as nil and
// normalizes a supplied subscription list through the positive-plan filter. Its
// new pointer remains supplied even when the filtered slice is nil or empty.
func normalizeOptionalDefaultSubscriptions(input *[]billinghttp.DefaultSubscriptionSetting) *[]billinghttp.DefaultSubscriptionSetting {
	if input == nil {
		return nil
	}
	normalized := normalizeDefaultSubscriptions(*input)
	return &normalized
}

// float64ValueOrDefault returns the supplied scalar value or its prior default.
// Nil chooses fallback; nonnil zero and negative values stay supplied for the
// calling handler's separate normalization and validation rules.
func float64ValueOrDefault(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

// intValueOrDefault returns a pointed integer or the supplied prior default.
// Nil keeps fallback, while nonnil zero remains submitted input so the handler
// or owning module can apply its field-specific rule.
func intValueOrDefault(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

// boolValueOrDefault returns the submitted boolean or the supplied prior state.
// A nil pointer keeps fallback, and a pointed false remains a supplied update
// for the caller's subsequent field preparation.
func boolValueOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

// defaultSubscriptionsValueOrDefault returns the prior slice when input is nil,
// retaining its storage. A supplied list becomes a new slice of core plan-ID
// records, so an empty supplied value can clear selections after handler merging.
func defaultSubscriptionsValueOrDefault(input *[]billinghttp.DefaultSubscriptionSetting, fallback []billing.DefaultSubscriptionSetting) []billing.DefaultSubscriptionSetting {
	if input == nil {
		return fallback
	}
	result := make([]billing.DefaultSubscriptionSetting, 0, len(*input))
	for _, item := range *input {
		result = append(result, billing.DefaultSubscriptionSetting{
			PlanID: item.PlanID,
		})
	}
	return result
}

// equalStringSlice reports ordered equality of the two string lists. Nil and
// empty lists compare equal because both have zero length; nonempty entries
// compare exactly with no trimming, sorting, or case normalization.
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalDefaultSubscriptions reports equality of ordered plan-ID selections.
// Nil and empty lists compare equal, while plan order and duplicate positions
// remain meaningful in this audit comparison of the stored grant representation.
func equalDefaultSubscriptions(a, b []billing.DefaultSubscriptionSetting) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].PlanID != b[i].PlanID {
			return false
		}
	}
	return true
}

// equalLoginAgreementDocuments compares ordered legal records by ID, title,
// and Markdown body. Nil and empty lists compare equal; localization pointers
// and their revision metadata are outside this helper's selected audit comparison.
func equalLoginAgreementDocuments(a, b []site.LoginAgreementDocument) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Title != b[i].Title || a[i].ContentMD != b[i].ContentMD {
			return false
		}
	}
	return true
}

// equalIntSlice reports ordered equality for table page-size choices. Nil and
// empty lists compare equal, while each supplied integer and its position
// remain significant when the audit decides whether the list changed.
func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalNotifyEmailEntries compares ordered recipient addresses with Disabled
// and Verified flags. Nil and empty lists compare equal; a difference in any
// selected flag or exact address adds the recipient-setting audit label.
func equalNotifyEmailEntries(a, b []contact.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Email != b[i].Email || a[i].Verified != b[i].Verified || a[i].Disabled != b[i].Disabled {
			return false
		}
	}
	return true
}

// equalProviderSchedulingThresholds compares percentages for supported platform
// keys, using 100 when an entry is missing from either map. Nil or empty
// maps therefore represent disabled automatic pausing for every platform in this
// comparison, while supplied supported values are compared directly.
//
// Unsupported keys are outside the selected platform iteration, so their differences
// do not change this result. The comparison preserves the provider module's
// missing-entry meaning for audit labels; it performs no range validation or
// normalization of values, which occurs during settings preparation.
func equalProviderSchedulingThresholds(before, after map[string]int) bool {
	// Compare the provider-supported platform set with absent entries selected as
	// 100, keeping sparse maps consistent with disabled-pausing meaning for audit decisions.
	for _, platform := range provider.AllowedSchedulingThresholdPlatforms {
		beforeValue := 100
		if before != nil {
			if value, ok := before[platform]; ok {
				beforeValue = value
			}
		}
		afterValue := 100
		if after != nil {
			if value, ok := after[platform]; ok {
				afterValue = value
			}
		}
		if beforeValue != afterValue {
			return false
		}
	}
	return true
}

// stringSetting returns pointed text or the supplied prior value for merging.
// Nil keeps fallback, while a supplied empty string stays empty so scheduler
// preparation can interpret it as inherited configuration.
func stringSetting(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

// equalUserPromptReplacementConfig compares serialized prompt rule objects,
// including nested fields and their order. Nil serializes as null and differs
// from an empty nonnil object; encoding errors are discarded before comparing
// the available serialized text.
func equalUserPromptReplacementConfig(a, b *promptpolicy.UserPromptReplacementConfig) bool {
	// The current prompt configuration consists of JSON-supported scalar/rule
	// fields. Encoding errors are still discarded here, and comparison uses whatever
	// serialized bytes each attempted encoding returned for the audited object.
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	return string(rawA) == string(rawB)
}
