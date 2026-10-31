package notification

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"maps"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/notification/contract"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// service.go renders notification templates, applies recipient preferences,
// and coordinates source-backed email delivery.
// Notification delivery renders event-specific placeholders using recipient
// language and stored template overrides. HTML variables are escaped except for
// the operations-report and escaped plugin-body fields admitted through a trusted
// map. Optional events additionally consult persisted unsubscribe preferences
// before sending.
//
// A source-backed request coordinates marker lookup, SMTP sending, and marker
// storage under one instance-local delivery key. Acceptance and persistence are
// separate operations, so a failed marker write after acceptance leaves an
// uncertain result. Signing-secret creation shares this local coordinator,
// while current preference and delivery identities also read legacy keys.

const (
	// Branding values identify current site copy and link bases used by
	// rendering and unsubscribe URLs, with a built-in site name for missing copy.
	defaultSiteName       = "TokenRouter"
	SettingKeySiteName    = "site_name"
	SettingKeyFrontendURL = "frontend_url"

	// Event identifiers select templates, placeholder rules, preference
	// eligibility, and source-backed delivery identities for the supported business notices.
	NotificationEmailEventAuthVerifyCode              = "auth.verify_code"                    // identity verification credential
	NotificationEmailEventAuthPasswordReset           = "auth.password_reset"                 // password recovery link
	NotificationEmailEventNotificationEmailVerifyCode = "notification_email.verify_code"      // additional address verification
	NotificationEmailEventTeamInvitation              = "team.invitation"                     // team membership invitation
	NotificationEmailEventSubscriptionPurchaseSuccess = "subscription.purchase_success"       // fulfilled subscription purchase
	NotificationEmailEventSubscriptionExpiryReminder  = "subscription.expiry_reminder"        // optional subscription reminder
	NotificationEmailEventBalanceLow                  = "balance.low"                         // optional low-balance notice
	NotificationEmailEventBalanceRechargeSuccess      = "balance.recharge_success"            // fulfilled balance recharge
	NotificationEmailEventProviderUpstreamBalanceLow  = "provider.upstream_balance_low"       // upstream wallet balance reminder
	NotificationEmailEventProviderQuotaAlert          = "provider.quota_alert"                // administrator provider-quota notice
	NotificationEmailEventContentModerationViolation  = "content_moderation.violation_notice" // content violation notice
	NotificationEmailEventContentModerationDisabled   = "content_moderation.account_disabled" // moderation account-disabled notice
	NotificationEmailEventOpsAlert                    = "ops.alert"                           // operations rule alert
	NotificationEmailEventTeamOwnershipTransfer       = "team.ownership_transfer"             // team ownership offer
	NotificationEmailEventOpsScheduledReport          = "ops.scheduled_report"                // scheduled operations report

	// Persistence prefixes separate templates, recipient preferences, delivery
	// markers, remembered language, and the shared token-signing secret in settings.
	notificationEmailTemplateKeyPrefix    = "notification_email_template:"
	notificationEmailPreferenceKeyPrefix  = "notification_email_preference:"
	notificationEmailDeliveryKeyPrefix    = "notification_email_delivery:"
	notificationEmailLocaleUserKeyPrefix  = "notification_email_locale:user:"
	notificationEmailLocaleEmailKeyPrefix = "notification_email_locale:email:"
	notificationEmailUnsubscribeSecretKey = "notification_email_unsubscribe_secret"
	// Language defaults, content limits, and token lifetime govern template
	// selection, administrator validation, and the validity of signed unsubscribe requests.
	notificationEmailDefaultLocale    = "en"
	notificationEmailLocaleChinese    = "zh-Hans"
	notificationEmailMaxSubjectLength = 200
	notificationEmailMaxHTMLLength    = 30000
	notificationEmailUnsubscribeTTL   = 365 * 24 * time.Hour
)

var (
	// ErrSettingNotFound identifies absent notification configuration or markers.
	// Readers use this shared error to distinguish a missing key from other
	// storage failures before selecting another persisted or built-in value.
	ErrSettingNotFound = settings.ErrSettingNotFound

	// notificationEmailPlaceholderPattern recognizes named substitutions used
	// by both validation and rendering when checking each event's allowed variable names.
	notificationEmailPlaceholderPattern = regexp.MustCompile(`{{\s*([a-zA-Z][a-zA-Z0-9_]*)\s*}}`)
	// notificationEmailBalanceDollarPattern identifies saved dollar-prefixed balance
	// placeholders whose units follow the current account display settings during rendering.
	notificationEmailBalanceDollarPattern = regexp.MustCompile(`\$(\{\{\s*(?:current_balance|recharge_amount|threshold)\s*\}\})`)
	// notificationEmailLocales captures manifest language order for template
	// listing, allowing administrator responses to follow the declared language sequence.
	notificationEmailLocales = func() []string {
		codes := []string{}
		for _, item := range locale.Definitions() {
			codes = append(codes, item.Code)
		}
		return codes
	}()
	// notificationEmailCommonPlaceholders names branding and recipient values
	// admitted by every event before its event-specific variables are appended.
	notificationEmailCommonPlaceholders = []string{"site_name", "recipient_name", "recipient_email"}
	// notificationEmailOpsSummaryPlaceholders names independently selectable
	// summary metrics used by report validation, preview values, and runtime sample cleanup.
	notificationEmailOpsSummaryPlaceholders = contract.SummaryPlaceholders()

	// notificationEmailEvents defines listing order, optional-event eligibility,
	// and placeholder names; public metadata readers copy its slices before returning them.
	notificationEmailEvents = []NotificationEmailEventInfo{
		{
			Event:        NotificationEmailEventAuthVerifyCode,
			Label:        "Email verification code",
			Description:  "Sent for registration, email binding, OAuth pending email, and TOTP verification flows.",
			Category:     "auth",
			Optional:     false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "verification_code", "expires_in_minutes"),
		},
		{
			Event:        NotificationEmailEventAuthPasswordReset,
			Label:        "Password reset",
			Description:  "Sent when a user requests a password reset link.",
			Category:     "auth",
			Optional:     false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "reset_url", "expires_in_minutes"),
		},
		{
			Event:        NotificationEmailEventNotificationEmailVerifyCode,
			Label:        "Notification email verification code",
			Description:  "Sent when a user verifies an extra notification email address.",
			Category:     "auth",
			Optional:     false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "verification_code", "expires_in_minutes"),
		},
		{
			Event:        NotificationEmailEventTeamInvitation,
			Label:        "Team invitation",
			Description:  "Sent when a team owner invites an email address to join the team.",
			Category:     "team",
			Optional:     false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "team_name", "invitation_url", "expires_at"),
		},
		{
			Event:       NotificationEmailEventTeamOwnershipTransfer,
			Label:       "Team ownership transfer",
			Description: "Sent when team ownership is offered to a user.",
			Category:    "team", Optional: false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "team_name", "transfer_url"),
		},
		{
			Event:        NotificationEmailEventSubscriptionPurchaseSuccess,
			Label:        "Subscription purchase success",
			Description:  "Sent after a subscription purchase is fulfilled.",
			Category:     "subscription",
			Optional:     false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "subscription_group", "subscription_days", "expiry_time", "order_id"),
		},
		{
			Event:        NotificationEmailEventSubscriptionExpiryReminder,
			Label:        "Subscription expiry reminder",
			Description:  "Optional reminder sent before an active subscription expires.",
			Category:     "subscription",
			Optional:     true,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "subscription_group", "expiry_time", "days_remaining", "unsubscribe_url"),
		},
		{
			Event:        NotificationEmailEventBalanceLow,
			Label:        "Low balance alert",
			Description:  "Optional alert sent when balance crosses the configured low-balance threshold.",
			Category:     "billing",
			Optional:     true,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "current_balance", "threshold", "recharge_url", "unsubscribe_url", "balance_unit_name", "balance_unit_symbol"),
		},
		{
			Event:        NotificationEmailEventBalanceRechargeSuccess,
			Label:        "Balance recharge success",
			Description:  "Sent after a balance recharge order is fulfilled.",
			Category:     "billing",
			Optional:     false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "recharge_amount", "current_balance", "order_id", "balance_unit_name", "balance_unit_symbol"),
		},
		{
			Event:        NotificationEmailEventProviderUpstreamBalanceLow,
			Label:        "Provider upstream balance reminder",
			Description:  "Sent on every scheduled check with a wallet balance below the provider threshold.",
			Category:     "admin",
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "provider_id", "provider_name", "balance", "threshold", "unit", "observed_at"),
		},
		{
			Event:       NotificationEmailEventProviderQuotaAlert,
			Label:       "Provider quota alert",
			Description: "Sent to configured admin notification emails when an upstream provider quota threshold is crossed.",
			Category:    "admin",
			Optional:    false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...),
				"provider_id", "provider_name", "platform", "quota_dimension", "quota_used", "quota_limit", "quota_remaining", "quota_threshold"),
		},
		{
			Event:       NotificationEmailEventContentModerationViolation,
			Label:       "Risk control violation notice",
			Description: "Sent to users when a request triggers content moderation/risk control rules.",
			Category:    "risk_control",
			Optional:    false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...),
				"triggered_at", "group_name", "moderation_category", "moderation_score", "violation_count", "ban_threshold"),
		},
		{
			Event:       NotificationEmailEventContentModerationDisabled,
			Label:       "Risk control account disabled",
			Description: "Sent to users when content moderation automatically disables their account.",
			Category:    "risk_control",
			Optional:    false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...),
				"triggered_at", "group_name", "moderation_category", "moderation_score", "violation_count", "ban_threshold"),
		},
		{
			Event:       NotificationEmailEventOpsAlert,
			Label:       "Ops alert",
			Description: "Sent to configured operations recipients when an ops alert rule fires.",
			Category:    "ops",
			Optional:    false,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...),
				"rule_name", "severity", "alert_status", "metric_type", "operator", "metric_value", "threshold_value", "triggered_at", "alert_description"),
		},
		{
			Event:       NotificationEmailEventOpsScheduledReport,
			Label:       "Ops scheduled report",
			Description: "Sent to configured operations recipients for scheduled daily/weekly/error/provider-health reports.",
			Category:    "ops",
			Optional:    false,
			Placeholders: append(
				append(
					append([]string{}, notificationEmailCommonPlaceholders...),
					"report_name", "report_type", "report_start_time", "report_end_time",
				),
				append(append([]string{}, notificationEmailOpsSummaryPlaceholders...), "report_detail_display", "report_html")...,
			),
		},
		{
			Event:        NotificationEmailEventPluginMessage,
			Label:        "Plugin message",
			Description:  "Optional message sent by an administrator-installed plugin.",
			Category:     "plugin",
			Optional:     true,
			Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "plugin_name", "subject", "body_html", "unsubscribe_url"),
		},
	}

	// notificationEmailEventDefinitions indexes event metadata for normalized
	// sending, preference, validation, and administrator-template lookups throughout this service.
	notificationEmailEventDefinitions = indexNotificationEmailEvents(notificationEmailEvents)

	// notificationEmailOfficialTemplates supplies event/language defaults when
	// stored overrides are absent, unreadable, malformed, or invalid for the selected event.
	notificationEmailOfficialTemplates = map[string]map[string]notificationEmailOfficialTemplate{
		NotificationEmailEventPluginMessage: pluginMessageTemplates,
		NotificationEmailEventTeamOwnershipTransfer: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Team ownership transfer",
				HTML:    notificationEmailCard("#4f46e5", "Team ownership transfer", `<p>Hello {{recipient_name}},</p><p>You have been offered ownership of <strong>{{team_name}}</strong>.</p><p><a class="button" href="{{transfer_url}}">Review the transfer</a></p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 团队所有权转让",
				HTML:    notificationEmailChineseCard("#4f46e5", "团队所有权转让", `<p>{{recipient_name}}，您好：</p><p>您收到团队 <strong>{{team_name}}</strong> 的所有权转让请求。</p><p><a class="button" href="{{transfer_url}}">查看转让请求</a></p>`),
			},
		},

		NotificationEmailEventAuthVerifyCode: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Email verification code",
				HTML: notificationEmailCard("#4f46e5", "Email verification code", `
<p>Hello {{recipient_name}},</p>
<p>Your verification code is:</p>
<p style="font-size: 32px; font-weight: 700; letter-spacing: 8px; text-align: center;">{{verification_code}}</p>
<p>This code expires in <strong>{{expires_in_minutes}}</strong> minutes.</p>
<p>If you did not request this code, please ignore this email.</p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 邮箱验证码",
				HTML: notificationEmailChineseCard("#4f46e5", "邮箱验证码", `
<p>{{recipient_name}}，您好：</p>
<p>您的验证码是：</p>
<p style="font-size: 32px; font-weight: 700; letter-spacing: 8px; text-align: center;">{{verification_code}}</p>
<p>验证码将在 <strong>{{expires_in_minutes}}</strong> 分钟后失效。</p>
<p>如果不是您本人操作，请忽略此邮件。</p>`),
			},
		},
		NotificationEmailEventAuthPasswordReset: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Password reset request",
				HTML: notificationEmailCard("#7c3aed", "Password reset", `
<p>Hello {{recipient_name}},</p>
<p>We received a request to reset your password. Click the button below to set a new password.</p>
<p><a class="button" href="{{reset_url}}">Reset password</a></p>
<p>This link expires in <strong>{{expires_in_minutes}}</strong> minutes.</p>
<p class="muted">If the button does not work, copy this link into your browser:<br>{{reset_url}}</p>
<p>If you did not request this, you can safely ignore this email.</p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 密码重置请求",
				HTML: notificationEmailChineseCard("#7c3aed", "密码重置", `
<p>{{recipient_name}}，您好：</p>
<p>我们收到了您的密码重置请求，请点击下方按钮设置新密码。</p>
<p><a class="button" href="{{reset_url}}">重置密码</a></p>
<p>此链接将在 <strong>{{expires_in_minutes}}</strong> 分钟后失效。</p>
<p class="muted">如果按钮无法点击，请复制以下链接到浏览器中打开：<br>{{reset_url}}</p>
<p>如果不是您本人操作，请忽略此邮件。</p>`),
			},
		},
		NotificationEmailEventNotificationEmailVerifyCode: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Notification email verification code",
				HTML: notificationEmailCard("#0ea5e9", "Notification email verification", `
<p>Hello {{recipient_name}},</p>
<p>You are adding this address as an extra notification email.</p>
<p>Your verification code is:</p>
<p style="font-size: 32px; font-weight: 700; letter-spacing: 8px; text-align: center;">{{verification_code}}</p>
<p>This code expires in <strong>{{expires_in_minutes}}</strong> minutes.</p>
<p>If you did not request this code, please ignore this email.</p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 通知邮箱验证码",
				HTML: notificationEmailChineseCard("#0ea5e9", "通知邮箱验证", `
<p>{{recipient_name}}，您好：</p>
<p>您正在添加额外的通知邮箱，请输入以下验证码完成验证。</p>
<p style="font-size: 32px; font-weight: 700; letter-spacing: 8px; text-align: center;">{{verification_code}}</p>
<p>验证码将在 <strong>{{expires_in_minutes}}</strong> 分钟后失效。</p>
<p>如果不是您本人操作，请忽略此邮件。</p>`),
			},
		},
		NotificationEmailEventTeamInvitation: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Invitation to join {{team_name}}",
				HTML: notificationEmailCard("#0f766e", "Team invitation", `
<p>Hello {{recipient_name}},</p>
<p>You have been invited to join the team <strong>{{team_name}}</strong> on {{site_name}}.</p>
<p><a class="button" href="{{invitation_url}}">View invitation</a></p>
<p>This invitation is valid until <strong>{{expires_at}}</strong>.</p>
<p class="muted">If the button does not work, copy this link into your browser:<br>{{invitation_url}}</p>
<p>If you were not expecting this invitation, you can safely ignore this email.</p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 邀请您加入团队 {{team_name}}",
				HTML: notificationEmailChineseCard("#0f766e", "团队邀请", `
<p>{{recipient_name}}，您好：</p>
<p>您被邀请加入 {{site_name}} 上的团队 <strong>{{team_name}}</strong>。</p>
<p><a class="button" href="{{invitation_url}}">查看并处理邀请</a></p>
<p>此邀请有效期至 <strong>{{expires_at}}</strong>。</p>
<p class="muted">如果按钮无法点击，请复制以下链接到浏览器中打开：<br>{{invitation_url}}</p>
<p>如果您没有预期收到此邀请，可以忽略本邮件。</p>`),
			},
		},
		NotificationEmailEventSubscriptionPurchaseSuccess: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Subscription purchase successful",
				HTML: notificationEmailCard("#2563eb", "Subscription activated", `
<p>Hello {{recipient_name}},</p>
<p>Your subscription for <strong>{{subscription_group}}</strong> has been activated for <strong>{{subscription_days}}</strong> days.</p>
<p>Expiry time: <strong>{{expiry_time}}</strong></p>
<p>Order ID: {{order_id}}</p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 订阅购买成功",
				HTML: notificationEmailChineseCard("#2563eb", "订阅已开通", `
<p>{{recipient_name}}，您好：</p>
<p>您的 <strong>{{subscription_group}}</strong> 订阅已成功开通，有效期 <strong>{{subscription_days}}</strong> 天。</p>
<p>到期时间：<strong>{{expiry_time}}</strong></p>
<p>订单号：{{order_id}}</p>`),
			},
		},
		NotificationEmailEventSubscriptionExpiryReminder: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Subscription expires in {{days_remaining}} day(s)",
				HTML: notificationEmailCard("#f97316", "Subscription expiry reminder", `
<p>Hello {{recipient_name}},</p>
<p>Your <strong>{{subscription_group}}</strong> subscription will expire in <strong>{{days_remaining}}</strong> day(s).</p>
<p>Expiry time: <strong>{{expiry_time}}</strong></p>
<p class="muted"><a href="{{unsubscribe_url}}">Unsubscribe from optional subscription reminders</a></p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 订阅将在 {{days_remaining}} 天后到期",
				HTML: notificationEmailChineseCard("#f97316", "订阅到期提醒", `
<p>{{recipient_name}}，您好：</p>
<p>您的 <strong>{{subscription_group}}</strong> 订阅将在 <strong>{{days_remaining}}</strong> 天后到期。</p>
<p>到期时间：<strong>{{expiry_time}}</strong></p>
<p class="muted"><a href="{{unsubscribe_url}}">退订此类订阅提醒</a></p>`),
			},
		},
		NotificationEmailEventBalanceLow: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Low balance alert",
				HTML: notificationEmailCard("#d97706", "Low balance alert", `
<p>Hello {{recipient_name}},</p>
<p>Your current balance is <strong>{{balance_unit_symbol}}{{current_balance}}</strong>, below the configured alert threshold of <strong>{{balance_unit_symbol}}{{threshold}}</strong>.</p>
<p>Please recharge in time to avoid service interruption.</p>
<p><a class="button" href="{{recharge_url}}">Recharge now</a></p>
<p class="muted"><a href="{{unsubscribe_url}}">Unsubscribe from optional balance alerts</a></p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 余额不足提醒",
				HTML: notificationEmailChineseCard("#d97706", "余额不足提醒", `
<p>{{recipient_name}}，您好：</p>
<p>您当前余额为 <strong>{{balance_unit_symbol}}{{current_balance}}</strong>，已低于提醒阈值 <strong>{{balance_unit_symbol}}{{threshold}}</strong>。</p>
<p>请及时充值以免服务中断。</p>
<p><a class="button" href="{{recharge_url}}">立即充值</a></p>
<p class="muted"><a href="{{unsubscribe_url}}">退订此类余额提醒</a></p>`),
			},
		},
		NotificationEmailEventBalanceRechargeSuccess: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Balance recharge successful",
				HTML: notificationEmailCard("#16a34a", "Recharge successful", `
<p>Hello {{recipient_name}},</p>
<p>Your balance recharge of <strong>{{balance_unit_symbol}}{{recharge_amount}}</strong> has been completed.</p>
<p>Current balance: <strong>{{balance_unit_symbol}}{{current_balance}}</strong></p>
<p>Order ID: {{order_id}}</p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 余额充值成功",
				HTML: notificationEmailChineseCard("#16a34a", "余额充值成功", `
<p>{{recipient_name}}，您好：</p>
<p>您的余额充值 <strong>{{balance_unit_symbol}}{{recharge_amount}}</strong> 已完成。</p>
<p>当前余额：<strong>{{balance_unit_symbol}}{{current_balance}}</strong></p>
			<p>订单号：{{order_id}}</p>`),
			},
		},
		NotificationEmailEventProviderUpstreamBalanceLow: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Low upstream balance - {{provider_name}}",
				HTML:    notificationEmailCard("#dc2626", "Low upstream balance", `<p>Provider: {{provider_name}} ({{provider_id}})</p><p>Remaining balance: {{balance}} {{unit}}</p><p>Reminder threshold: {{threshold}}</p><p>Checked at: {{observed_at}}</p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 上游余额不足 - {{provider_name}}",
				HTML:    notificationEmailChineseCard("#dc2626", "上游余额不足", `<p>提供商：{{provider_name}}（{{provider_id}}）</p><p>剩余余额：{{balance}} {{unit}}</p><p>提醒阈值：{{threshold}}</p><p>查询时间：{{observed_at}}</p>`),
			},
		},
		NotificationEmailEventProviderQuotaAlert: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Provider quota alert - {{provider_name}}",
				HTML: notificationEmailCard("#dc2626", "Provider quota alert", `
<p>The upstream provider <strong>{{provider_name}}</strong> has crossed its configured quota alert threshold.</p>
<table style="width:100%;border-collapse:collapse;">
  <tr><td>Provider ID</td><td>{{provider_id}}</td></tr>
  <tr><td>Platform</td><td>{{platform}}</td></tr>
  <tr><td>Dimension</td><td>{{quota_dimension}}</td></tr>
  <tr><td>Used / Limit</td><td>{{quota_used}} / {{quota_limit}}</td></tr>
  <tr><td>Remaining</td><td>{{quota_remaining}}</td></tr>
  <tr><td>Threshold</td><td>{{quota_threshold}}</td></tr>
</table>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 提供商限额告警 - {{provider_name}}",
				HTML: notificationEmailChineseCard("#dc2626", "提供商限额告警", `
<p>上游提供商 <strong>{{provider_name}}</strong> 已触发配置的额度告警阈值。</p>
<table style="width:100%;border-collapse:collapse;">
  <tr><td>提供商 ID</td><td>{{provider_id}}</td></tr>
  <tr><td>平台</td><td>{{platform}}</td></tr>
  <tr><td>维度</td><td>{{quota_dimension}}</td></tr>
  <tr><td>已用 / 限额</td><td>{{quota_used}} / {{quota_limit}}</td></tr>
  <tr><td>剩余额度</td><td>{{quota_remaining}}</td></tr>
  <tr><td>告警阈值</td><td>{{quota_threshold}}</td></tr>
</table>`),
			},
		},
		NotificationEmailEventContentModerationViolation: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Risk control notice",
				HTML: notificationEmailCard("#ef4444", "Risk control notice", `
<p>Hello {{recipient_name}},</p>
<p>Your API request triggered the platform content moderation/risk-control policy.</p>
<table style="width:100%;border-collapse:collapse;">
  <tr><td>Triggered at</td><td>{{triggered_at}}</td></tr>
  <tr><td>Group</td><td>{{group_name}}</td></tr>
  <tr><td>Category / Score</td><td>{{moderation_category}} / {{moderation_score}}</td></tr>
  <tr><td>Violation count</td><td>{{violation_count}} / {{ban_threshold}}</td></tr>
</table>
<p>Please review your request content to avoid future service interruptions.</p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 账户风控提醒",
				HTML: notificationEmailChineseCard("#ef4444", "账户风控提醒", `
<p>{{recipient_name}}，您好：</p>
<p>您的 API 请求触发了平台内容审核/风控策略。</p>
<table style="width:100%;border-collapse:collapse;">
  <tr><td>触发时间</td><td>{{triggered_at}}</td></tr>
  <tr><td>所属分组</td><td>{{group_name}}</td></tr>
  <tr><td>命中类别 / 分数</td><td>{{moderation_category}} / {{moderation_score}}</td></tr>
  <tr><td>累计触发次数</td><td>{{violation_count}} / {{ban_threshold}}</td></tr>
</table>
<p>请检查请求内容，避免后续服务受到影响。</p>`),
			},
		},
		NotificationEmailEventContentModerationDisabled: {
			notificationEmailDefaultLocale: {
				Subject: "[{{site_name}}] Account disabled by risk control",
				HTML: notificationEmailCard("#b91c1c", "Account disabled", `
<p>Hello {{recipient_name}},</p>
<p>Your account has repeatedly triggered platform content moderation/risk-control rules and has been automatically disabled.</p>
<table style="width:100%;border-collapse:collapse;">
  <tr><td>Disabled at</td><td>{{triggered_at}}</td></tr>
  <tr><td>Group</td><td>{{group_name}}</td></tr>
  <tr><td>Category / Score</td><td>{{moderation_category}} / {{moderation_score}}</td></tr>
  <tr><td>Violation count</td><td>{{violation_count}} / {{ban_threshold}}</td></tr>
</table>
<p>Please contact the administrator if you need to appeal or restore access.</p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[{{site_name}}] 账户已被禁用",
				HTML: notificationEmailChineseCard("#b91c1c", "账户已被禁用", `
<p>{{recipient_name}}，您好：</p>
<p>您的账户在统计周期内多次触发平台内容审核/风控规则，系统已自动禁用该账户。</p>
<table style="width:100%;border-collapse:collapse;">
  <tr><td>禁用时间</td><td>{{triggered_at}}</td></tr>
  <tr><td>所属分组</td><td>{{group_name}}</td></tr>
  <tr><td>命中类别 / 分数</td><td>{{moderation_category}} / {{moderation_score}}</td></tr>
  <tr><td>累计触发次数</td><td>{{violation_count}} / {{ban_threshold}}</td></tr>
</table>
<p>如需申诉或恢复账号，请联系平台管理员处理。</p>`),
			},
		},
		NotificationEmailEventOpsAlert: {
			notificationEmailDefaultLocale: {
				Subject: "[Ops Alert][{{severity}}] {{rule_name}}",
				HTML: notificationEmailCard("#ea580c", "Ops alert", `
<p><strong>Rule</strong>: {{rule_name}}</p>
<p><strong>Severity</strong>: {{severity}}</p>
<p><strong>Status</strong>: {{alert_status}}</p>
<p><strong>Metric</strong>: {{metric_type}} {{operator}} {{metric_value}} (threshold {{threshold_value}})</p>
<p><strong>Fired at</strong>: {{triggered_at}}</p>
<p><strong>Description</strong>: {{alert_description}}</p>`),
			},
			notificationEmailLocaleChinese: {
				Subject: "[运维告警][{{severity}}] {{rule_name}}",
				HTML: notificationEmailChineseCard("#ea580c", "运维告警", `
<p><strong>规则</strong>：{{rule_name}}</p>
<p><strong>严重级别</strong>：{{severity}}</p>
<p><strong>状态</strong>：{{alert_status}}</p>
<p><strong>指标</strong>：{{metric_type}} {{operator}} {{metric_value}}（阈值 {{threshold_value}}）</p>
<p><strong>触发时间</strong>：{{triggered_at}}</p>
<p><strong>说明</strong>：{{alert_description}}</p>`),
			},
		},
		NotificationEmailEventOpsScheduledReport: {
			notificationEmailDefaultLocale: {
				Subject: "[Ops Report] {{report_name}}",
				HTML:    notificationEmailOpsScheduledReportTemplate(notificationEmailDefaultLocale),
			},
			notificationEmailLocaleChinese: {
				Subject: "[运维报表] {{report_name}}",
				HTML:    notificationEmailOpsScheduledReportTemplate(notificationEmailLocaleChinese),
			},
		},
	}
)

// SettingRepository reads and persists templates, preferences, markers, and notification configuration.
type SettingRepository = settings.Repository

type (
	// NotificationEmailSendInput is the event, recipient, identity, and rendering input for delivery.
	NotificationEmailSendInput = SendRequest
	// Sender delivers rendered subject and HTML values with caller cancellation.
	Sender interface {
		// SendEmail returns the transport outcome for a rendered message.
		// The notification service writes its success marker after this call succeeds;
		// the sender determines when SMTP acceptance becomes a successful result.
		SendEmail(context.Context, string, string, string) error
	}
)

// NotificationEmailService renders events and coordinates recipient preferences with source-backed delivery markers.
type NotificationEmailService struct {
	// frontendURL resolves the public site address on each send. Assembly
	// installs it before concurrent use; nil uses only the stored frontend_url.
	frontendURL func(context.Context) string
	// recipientLocale is installed during assembly and read before stored
	// language fallbacks. Configure it before concurrent lookups because this field
	// assignment and its readers are unsynchronized.
	recipientLocale func(context.Context, int64, string) string
	locks           keyCoordinator    // instance-local keyed coordination
	settingRepo     SettingRepository // persisted notification settings
	emailService    Sender            // rendered-message transport sender
}

// NotificationEmailEventInfo describes an editable event and its admitted template variable names.
type NotificationEmailEventInfo struct {
	Event       string `json:"event"`       // notification event identifier
	Label       string `json:"label"`       // administrator event label
	Description string `json:"description"` // administrator event explanation
	Category    string `json:"category"`    // administrator event category
	// Optional permits unsubscribe handling for this event. Transactional events
	// ignore recipient preference records and reject unsubscribe requests when this flag is false.
	Optional bool `json:"optional"` // recipient preference eligibility
	// Placeholders lists admitted names. ListEventInfos copies this slice for
	// callers, while internal event lookup retains the shared definition's backing storage.
	Placeholders []string `json:"placeholders"` // admitted variable names
}

// NotificationEmailTemplate contains selected subject and HTML content for one event and language.
type NotificationEmailTemplate struct {
	Event   string `json:"event"`   // notification event identifier
	Locale  string `json:"locale"`  // selected template language
	Subject string `json:"subject"` // template subject text
	HTML    string `json:"html"`    // template HTML body
	// IsCustom reports a successfully decoded and validated stored override;
	// failed override reads leave built-in content selected with this flag false.
	IsCustom bool `json:"is_custom"` // selected override indicator
	// UpdatedAt is copied from a valid override and stays nil for built-in
	// content, allowing administrator views to distinguish saved overrides from defaults.
	UpdatedAt *time.Time `json:"updated_at,omitempty"` // stored override timestamp
	// Placeholders is copied from event metadata by GetTemplate. The HTTP detail
	// mapper retains this returned slice, so independently mutable consumers copy it again.
	Placeholders []string `json:"placeholders"` // admitted variable names
}

// NotificationEmailPreview contains a rendered subject and HTML body for inspection or sending.
type NotificationEmailPreview struct {
	Subject string `json:"subject"` // template subject text
	HTML    string `json:"html"`    // template HTML body
}

// NotificationEmailPreviewInput supplies unsaved content and sample-variable replacements for administrator rendering.
type NotificationEmailPreviewInput struct {
	Event  string `json:"event"`  // notification event identifier
	Locale string `json:"locale"` // requested template language
	// Subject and HTML independently use the selected template when their
	// supplied text is whitespace-only. PreviewTemplate validates the resulting
	// pair after filling either missing companion.
	Subject string `json:"subject"` // template subject text
	HTML    string `json:"html"`    // template HTML body
	// Variables replaces localized preview samples. PreviewTemplate reads this
	// map synchronously; callers keep it stable until copying and rendering finish.
	Variables map[string]string `json:"variables,omitempty"` // escaped substitution values
}

// SendRequest supplies recipient, event, source identity, and substitution values for notification delivery.
type SendRequest struct {
	Event string // notification event identifier
	// Locale is negotiated before recipient preferences; an absent or unusable
	// hint permits the account, remembered-address, and site-language lookup sequence.
	Locale string // selected template language
	// RecipientEmail is trimmed for transport and delivery identity, while the
	// recipient_email variable retains this request's supplied address spelling.
	RecipientEmail string // recipient delivery address
	// RecipientName replaces the greeting sample when nonblank. A blank value
	// leaves the sample recipient name in the renderer's working variable map.
	RecipientName string // recipient greeting text
	// UserID is passed to the assembled language reader. The app looks up positive
	// IDs by account ID and otherwise looks up the supplied recipient address.
	UserID int64 // account language identifier
	// SourceType, SourceID, and ReminderKey identify a delivery with Event and
	// RecipientEmail. A blank source type or ID skips marker lookup and keyed
	// coordination; ReminderKey separates recurring notices for the same source.
	SourceType  string // delivery source category
	SourceID    string // delivery source identifier
	ReminderKey string // recurring notice discriminator
	// Variables is copied into a working map, while RawHTMLVariables is read
	// directly for admitted raw fields. Callers keep both maps stable during Send
	// so rendering does not race with a concurrent write to their input storage.
	Variables        map[string]string // escaped substitution values
	RawHTMLVariables map[string]string // trusted HTML substitutions
}

// SendResult separates known transport acceptance from skipped or unattempted delivery.
type SendResult struct {
	// Delivered is true after SMTP acceptance or a retained success marker, even if a later marker write returns an error.
	Delivered bool
	Attempted bool // transport sender was invoked
}

// NotificationEmailUnsubscribeResult identifies the optional event and recipient whose preference was saved.
type NotificationEmailUnsubscribeResult struct {
	Event string `json:"event"` // notification event identifier
	Email string `json:"email"` // unsubscribe recipient address
	Done  bool   `json:"done"`  // preference write completed
}

// notificationEmailStoredTemplate records an administrator override and its saved timestamp in settings JSON.
type notificationEmailStoredTemplate struct {
	Subject   string    `json:"subject"`    // template subject text
	HTML      string    `json:"html"`       // template HTML body
	UpdatedAt time.Time `json:"updated_at"` // stored override timestamp
}

// notificationEmailOfficialTemplate contains built-in subject and HTML content for one event/language pair.
type notificationEmailOfficialTemplate struct {
	Subject string // template subject text
	HTML    string // template HTML body
}

// notificationEmailTemplateError classifies rendering or event-definition failures used by caller fallback decisions.
type notificationEmailTemplateError struct {
	Err error // wrapped classified failure
}

// notificationEmailConfigError classifies unavailable sender configuration used by caller fallback decisions.
type notificationEmailConfigError struct {
	Err error // wrapped classified failure
}

// notificationEmailDeliveryError classifies a failed send operation for delivery-outcome inspection by callers.
type notificationEmailDeliveryError struct {
	Err error // wrapped classified failure
}

// notificationEmailUnsubscribeClaims encodes recipient, event, and expiration covered by the token signature.
type notificationEmailUnsubscribeClaims struct {
	Email string `json:"email"` // unsubscribe recipient address
	Event string `json:"event"` // notification event identifier
	Exp   int64  `json:"exp"`   // Unix expiration seconds
}

// Error returns the wrapped template failure text for this classified error.
func (e notificationEmailTemplateError) Error() string {
	return e.Err.Error()
}

// Unwrap exposes the template failure to error-chain inspection by callers.
func (e notificationEmailTemplateError) Unwrap() error {
	return e.Err
}

// Error returns the wrapped configuration failure text for this classified error.
func (e notificationEmailConfigError) Error() string {
	return e.Err.Error()
}

// Unwrap exposes the configuration failure to error-chain inspection by callers.
func (e notificationEmailConfigError) Unwrap() error {
	return e.Err
}

// Error returns the wrapped send failure text for this classified error.
func (e notificationEmailDeliveryError) Error() string {
	return e.Err.Error()
}

// Unwrap exposes the failed send outcome to error-chain inspection by callers.
func (e notificationEmailDeliveryError) Unwrap() error {
	return e.Err
}

// NewNotificationEmailService constructs an instance-local renderer and keyed delivery coordinator.
// When the sender implements SetNotificationEmailService, construction binds the
// new service back to it. Assemble shared dependencies before concurrent calls;
// construction performs no settings reads or SMTP operations.
func NewNotificationEmailService(settingRepo SettingRepository, emailService Sender) *NotificationEmailService {
	svc := &NotificationEmailService{settingRepo: settingRepo, emailService: emailService}
	if emailService != nil {
		if binder, ok := emailService.(interface {
			// SetNotificationEmailService binds the assembled renderer to its sender.
			// Construction invokes this before using the service, allowing mailer event
			// wrappers to share this instance's template and delivery-key coordination.
			SetNotificationEmailService(*NotificationEmailService)
		}); ok {
			binder.SetNotificationEmailService(svc)
		}
	}
	return svc
}

// NotificationEmailTemplateErr classifies a non-nil template failure for caller fallback selection.
// A nil input returns nil. The wrapper retains the original error for Unwrap,
// so callers can inspect both classification and the underlying failure.
func NotificationEmailTemplateErr(err error) error {
	if err == nil {
		return nil
	}
	return notificationEmailTemplateError{Err: err}
}

// NotificationEmailConfigErr classifies a non-nil configuration failure for caller fallback selection.
// A nil input returns nil. The underlying error remains available through Unwrap
// for callers inspecting why the notification configuration was unavailable.
func NotificationEmailConfigErr(err error) error {
	if err == nil {
		return nil
	}
	return notificationEmailConfigError{Err: err}
}

// NotificationEmailDeliveryErr classifies a non-nil send failure for transport-outcome inspection.
// A nil input returns nil. The underlying failure remains in its error chain;
// classification records the send phase rather than proving whether SMTP accepted bytes.
func NotificationEmailDeliveryErr(err error) error {
	if err == nil {
		return nil
	}
	return notificationEmailDeliveryError{Err: err}
}

// ShouldFallbackNotificationEmail reports whether the error chain contains template or configuration classification.
// Nil returns false. A delivery classification alone does not select fallback;
// callers select fallback from the recognized classification in that chain.
// The helper does not inspect transport acceptance, so callers and senders
// remain responsible for the accuracy of the error phase they classify.
func ShouldFallbackNotificationEmail(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := errors.AsType[notificationEmailTemplateError](err); ok {
		return true
	}
	var configErr notificationEmailConfigError
	return errors.As(err, &configErr)
}

// IsNotificationEmailDeliveryError reports whether a classified send failure occurs in err's chain.
// It recognizes wrapped delivery failures and returns false for nil or an error
// chain containing only other classifications, supporting caller delivery-outcome handling.
func IsNotificationEmailDeliveryError(err error) bool {
	var deliveryErr notificationEmailDeliveryError
	return errors.As(err, &deliveryErr)
}

// indexNotificationEmailEvents indexes event metadata by its configured event identifier.
// Later duplicate identifiers overwrite earlier metadata. Values are copied as
// structs, while their placeholder slices retain the source definitions' backing storage.
func indexNotificationEmailEvents(events []NotificationEmailEventInfo) map[string]NotificationEmailEventInfo {
	index := make(map[string]NotificationEmailEventInfo, len(events))
	for _, info := range events {
		index[info.Event] = info
	}
	return index
}

// ListEventInfos returns event metadata in configured declaration order with copied placeholder slices.
// The outer list and each placeholder collection are separately allocated, so
// administrator consumers can edit those returned values without changing subsequent metadata reads.
func (s *NotificationEmailService) ListEventInfos() []NotificationEmailEventInfo {
	infos := make([]NotificationEmailEventInfo, 0, len(notificationEmailEvents))
	for _, info := range notificationEmailEvents {
		info.Placeholders = append([]string(nil), info.Placeholders...)
		infos = append(infos, info)
	}
	return infos
}

// SupportedLocales returns a separately allocated copy of the captured language-manifest order.
// Callers can change the returned slice independently of later locale listings.
// The captured service list itself is initialized from locale.Definitions at package startup.
func (s *NotificationEmailService) SupportedLocales() []string {
	return append([]string(nil), notificationEmailLocales...)
}

// ListTemplates returns every configured event/language pair in event-major manifest order.
// It delegates each selection to GetTemplate and stops on a returned event error.
// Stored override failures are interpreted by that getter's built-in selection rules.
func (s *NotificationEmailService) ListTemplates(ctx context.Context) ([]NotificationEmailTemplate, error) {
	items := make([]NotificationEmailTemplate, 0, len(notificationEmailEvents)*len(notificationEmailLocales))
	for _, info := range notificationEmailEvents {
		for _, locale := range notificationEmailLocales {
			tmpl, err := s.GetTemplate(ctx, info.Event, locale)
			if err != nil {
				return nil, err
			}
			items = append(items, tmpl)
		}
	}
	return items, nil
}

// GetTemplate returns a validated stored override or the selected event's built-in content.
// Event names are normalized and rejected when unsupported; language hints are
// negotiated with the default language. A missing built-in language entry uses
// that event's English content while retaining the requested normalized locale.
//
// All override-read errors, blank values, decoding failures, and invalid template
// content leave the built-in result available with no returned storage error.
// A valid override sets IsCustom and receives its own copied timestamp pointer;
// returned placeholder names are separately allocated for the caller.
func (s *NotificationEmailService) GetTemplate(ctx context.Context, event, locale string) (NotificationEmailTemplate, error) {
	info, normalizedEvent, err := s.eventInfo(event)
	if err != nil {
		return NotificationEmailTemplate{}, err
	}
	normalizedLocale := normalizeNotificationLocale(locale)
	// A manifest language can lack a built-in event body. Select English content
	// then, while keeping the normalized locale for override lookup and response metadata.
	official, ok := notificationEmailOfficialTemplates[normalizedEvent][normalizedLocale]
	if !ok {
		official = notificationEmailOfficialTemplates[normalizedEvent][notificationEmailDefaultLocale]
	}

	tmpl := NotificationEmailTemplate{
		Event:        normalizedEvent,
		Locale:       normalizedLocale,
		Subject:      official.Subject,
		HTML:         official.HTML,
		Placeholders: append([]string(nil), info.Placeholders...),
	}

	// Override availability is optional to rendering. Missing or failed reads keep
	// the selected built-in body, including read failures caused by a canceled context.
	raw, err := s.settingRepo.GetValue(ctx, notificationEmailTemplateKey(normalizedEvent, normalizedLocale))
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return tmpl, nil
		}
		return tmpl, nil
	}
	if strings.TrimSpace(raw) == "" {
		return tmpl, nil
	}

	// Unusable stored JSON and event-invalid copy retain the built-in selection.
	// Only a decoded valid override publishes IsCustom and its copied save timestamp.
	var stored notificationEmailStoredTemplate
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return tmpl, nil
	}
	if err := validateNotificationEmailTemplate(normalizedEvent, stored.Subject, stored.HTML); err != nil {
		return tmpl, nil
	}
	tmpl.Subject = stored.Subject
	tmpl.HTML = stored.HTML
	tmpl.IsCustom = true
	updatedAt := stored.UpdatedAt
	tmpl.UpdatedAt = &updatedAt
	return tmpl, nil
}

// UpdateTemplate validates and persists a subject/HTML override for the normalized event and language.
// The stored subject is trimmed and its UTC save timestamp is assigned here.
// After persistence, GetTemplate reads the selected result using its ordinary
// built-in-on-read-failure behavior, so a successful write is not rolled back by that lookup.
func (s *NotificationEmailService) UpdateTemplate(ctx context.Context, event, locale, subject, htmlBody string) (NotificationEmailTemplate, error) {
	_, normalizedEvent, err := s.eventInfo(event)
	if err != nil {
		return NotificationEmailTemplate{}, err
	}
	normalizedLocale := normalizeNotificationLocale(locale)
	if err := validateNotificationEmailTemplate(normalizedEvent, subject, htmlBody); err != nil {
		return NotificationEmailTemplate{}, err
	}
	stored := notificationEmailStoredTemplate{
		Subject:   strings.TrimSpace(subject),
		HTML:      htmlBody,
		UpdatedAt: time.Now().UTC(),
	}
	payload, err := json.Marshal(stored)
	if err != nil {
		return NotificationEmailTemplate{}, err
	}
	// Save the complete validated override before reading it back for display.
	// A later default-selected response from GetTemplate does not undo this completed persistence step.
	if err := s.settingRepo.Set(ctx, notificationEmailTemplateKey(normalizedEvent, normalizedLocale), string(payload)); err != nil {
		return NotificationEmailTemplate{}, err
	}
	return s.GetTemplate(ctx, normalizedEvent, normalizedLocale)
}

// RestoreOfficialTemplate deletes the normalized event/language override and reads the selected template.
// A missing override is accepted as already restored. Other deletion errors stop
// before reading; GetTemplate then applies its ordinary selection rules to the
// current settings, including an override another caller may have saved meanwhile.
func (s *NotificationEmailService) RestoreOfficialTemplate(ctx context.Context, event, locale string) (NotificationEmailTemplate, error) {
	_, normalizedEvent, err := s.eventInfo(event)
	if err != nil {
		return NotificationEmailTemplate{}, err
	}
	normalizedLocale := normalizeNotificationLocale(locale)
	// Missing override rows are already restored. Accept that deletion result
	// before the normal getter selects built-in content; other failures stop this settings mutation.
	if err := s.settingRepo.Delete(ctx, notificationEmailTemplateKey(normalizedEvent, normalizedLocale)); err != nil && !errors.Is(err, ErrSettingNotFound) {
		return NotificationEmailTemplate{}, err
	}
	return s.GetTemplate(ctx, normalizedEvent, normalizedLocale)
}

// PreviewTemplate renders unsaved content with localized samples and caller-provided variable replacements.
// Blank subject or HTML fields are filled independently from GetTemplate before
// validation. Supplied variables overwrite a fresh sample map, and raw HTML
// substitution is unavailable through this preview input.
func (s *NotificationEmailService) PreviewTemplate(ctx context.Context, input NotificationEmailPreviewInput) (NotificationEmailPreview, error) {
	_, normalizedEvent, err := s.eventInfo(input.Event)
	if err != nil {
		return NotificationEmailPreview{}, err
	}
	normalizedLocale := normalizeNotificationLocale(input.Locale)
	subject := input.Subject
	htmlBody := input.HTML
	// An unsaved preview can provide either field alone. Fill just its blank
	// companion from the selected template so supplied content remains available for validation and rendering.
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(htmlBody) == "" {
		tmpl, err := s.GetTemplate(ctx, normalizedEvent, normalizedLocale)
		if err != nil {
			return NotificationEmailPreview{}, err
		}
		if strings.TrimSpace(subject) == "" {
			subject = tmpl.Subject
		}
		if strings.TrimSpace(htmlBody) == "" {
			htmlBody = tmpl.HTML
		}
	}
	if err := validateNotificationEmailTemplate(normalizedEvent, subject, htmlBody); err != nil {
		return NotificationEmailPreview{}, err
	}
	variables := s.sampleVariables(ctx, normalizedEvent, normalizedLocale)
	maps.Copy(variables, input.Variables)
	return RenderNotificationEmail(normalizedEvent, subject, htmlBody, variables, nil)
}

// @project-doc docs/interfaces/configuration.md#notification_delivery
// Send renders and delivers one notification through the shared result-aware path.
// It preserves the error-only interface used by existing business callers. Callers
// needing to distinguish transport acceptance from later persistence failure use
// SendWithResult and inspect its Delivered field even when an error is returned.
func (s *NotificationEmailService) Send(ctx context.Context, input SendRequest) error {
	_, err := s.SendWithResult(ctx, input)
	return err
}

// SendWithResult renders and sends while preserving transport acceptance as a separate result.
// Blank recipients and unsubscribe decisions return an unattempted, undelivered result.
// Existing delivery markers return known acceptance without another transport call.
//
// Callers inspect Delivered even when an error follows successful SMTP acceptance,
// because marker persistence cannot undo mail delivery. Attempted distinguishes
// errors before transport from failures whose delivery outcome may be uncertain.
// This method retains the same preference checks, keyed coordination, rendering,
// and marker order as Send so existing callers keep their established behavior.
func (s *NotificationEmailService) SendWithResult(ctx context.Context, input SendRequest) (SendResult, error) {
	info, normalizedEvent, err := s.eventInfo(input.Event)
	if err != nil {
		return SendResult{}, NotificationEmailTemplateErr(err)
	}
	recipient := strings.TrimSpace(input.RecipientEmail)
	if recipient == "" {
		return SendResult{}, nil
	}
	if info.Optional {
		unsubscribed, err := s.IsUnsubscribed(ctx, recipient, normalizedEvent)
		if err != nil {
			return SendResult{}, err
		}
		if unsubscribed {
			slog.Info("notification email suppressed by unsubscribe preference", "event", normalizedEvent, "recipient_hash", NotificationEmailHash(recipient))
			return SendResult{}, nil
		}
	}

	selectedLocale := locale.Negotiate(input.Locale, s.ResolveRecipientLocale(ctx, input.UserID, recipient))
	ctx = locale.WithLanguage(ctx, selectedLocale)
	tmpl, err := s.GetTemplate(ctx, normalizedEvent, selectedLocale)
	if err != nil {
		return SendResult{}, NotificationEmailTemplateErr(err)
	}
	variables := s.runtimeVariables(ctx, normalizedEvent, selectedLocale, input)
	rendered, err := RenderNotificationEmail(normalizedEvent, tmpl.Subject, tmpl.HTML, variables, input.RawHTMLVariables)
	if err != nil {
		return SendResult{}, NotificationEmailTemplateErr(err)
	}

	deliveryKey := notificationEmailDeliveryKey(normalizedEvent, input.SourceType, input.SourceID, recipient, input.ReminderKey)
	// Coordinate marker lookup, transport sending, and marker persistence under
	// the same entry so equal keys in this instance cannot both pass lookup before either stores success.
	if deliveryKey != "" {
		release, err := s.locks.acquire(ctx, deliveryKey)
		if err != nil {
			return SendResult{}, err
		}
		defer release()
	}
	// Current and legacy markers both suppress this source-backed notice. Keep
	// both lookups within the held entry so a successful older identity remains visible to this send.
	if deliveryKey != "" {
		sent, err := s.deliveryExists(ctx, deliveryKey, legacyNotificationEmailDeliveryKey(normalizedEvent, input.SourceType, input.SourceID, recipient, input.ReminderKey))
		if err != nil {
			return SendResult{}, err
		}
		if sent {
			return SendResult{Delivered: true}, nil
		}
	}

	if s.emailService == nil {
		return SendResult{}, NotificationEmailConfigErr(errors.New("email service is not configured"))
	}
	result := SendResult{Attempted: true}
	if err := s.emailService.SendEmail(ctx, recipient, rendered.Subject, rendered.HTML); err != nil {
		return result, NotificationEmailDeliveryErr(err)
	}
	result.Delivered = true
	// Transport acceptance precedes persistence. Store success only after the send
	// returns nil; a failure here leaves delivered mail possible even though no current marker was saved.
	if deliveryKey != "" {
		if err := s.settingRepo.Set(ctx, deliveryKey, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return result, err
		}
	}
	return result, nil
}

// RememberRecipientLocale stores a normalized language hint for the supplied email's hash.
// Blank language input, missing service/storage, and blank hashed address are ignored.
// The userID parameter is retained by the caller interface; this method writes
// only the email-key hint and discards persistence errors.
func (s *NotificationEmailService) RememberRecipientLocale(ctx context.Context, userID int64, email, acceptLanguage string) {
	locale := normalizeNotificationLocale(acceptLanguage)
	if strings.TrimSpace(acceptLanguage) == "" || s == nil || s.settingRepo == nil {
		return
	}
	if emailHash := NotificationEmailHash(email); emailHash != "" {
		// Language memory is best effort for later preference lookup. A failed
		// write leaves existing account/email/site sources in place and does not fail the triggering business operation.
		_ = s.settingRepo.Set(ctx, notificationEmailLocaleEmailKeyPrefix+emailHash, locale)
	}
}

// ResolveRecipientLocale selects account, legacy user, remembered-email, then site language.
// A missing service or settings reader selects English. A valid assembled account
// reader result wins; otherwise a positive userID permits legacy user-key lookup,
// followed by the normalized email-key hint and the stored site default.
//
// Read errors are ignored along this sequence. A nonblank remembered string is
// negotiated immediately, so an invalid remembered value can select English
// instead of reaching the later site default. Event-specific language negotiation
// in Send can take precedence over this recipient result.
func (s *NotificationEmailService) ResolveRecipientLocale(ctx context.Context, userID int64, email string) string {
	if s == nil || s.settingRepo == nil {
		return notificationEmailDefaultLocale
	}
	if s.recipientLocale != nil {
		if selected := locale.Normalize(s.recipientLocale(ctx, userID, email)); selected != "" {
			return selected
		}
	}

	// Account lookup can leave no selection. Legacy user and email reads then
	// ignore unavailable settings and use the next source; a nonblank found value is negotiated immediately.
	if userID > 0 {
		if locale, err := s.settingRepo.GetValue(ctx, notificationEmailLocaleUserKeyPrefix+strconv.FormatInt(userID, 10)); err == nil && strings.TrimSpace(locale) != "" {
			return normalizeNotificationLocale(locale)
		}
	}
	if emailHash := NotificationEmailHash(email); emailHash != "" {
		if locale, err := s.settingRepo.GetValue(ctx, notificationEmailLocaleEmailKeyPrefix+emailHash); err == nil && strings.TrimSpace(locale) != "" {
			return normalizeNotificationLocale(locale)
		}
	}
	// An unavailable site default supplies empty input to negotiation, selecting
	// the locale library's default while keeping recipient-language lookup usable after storage read failure.
	fallback, _ := s.settingRepo.GetValue(ctx, "default_locale")
	return locale.Negotiate("", fallback)
}

// IsUnsubscribed reports whether the first available preference value names unsubscribed.
// Unsupported events return an error, and mandatory events return false before
// storage access. Optional events try the current hashed key, then the legacy
// key only when the current key is absent.
//
// A successful current-key read is authoritative even when its trimmed value is
// empty or different from unsubscribed; it does not consult a legacy record then.
// Missing both records means subscribed. Other storage failures abort the check
// so Send can report an unavailable preference decision to its caller.
func (s *NotificationEmailService) IsUnsubscribed(ctx context.Context, email, event string) (bool, error) {
	info, normalizedEvent, err := s.eventInfo(event)
	if err != nil {
		return false, err
	}
	if !info.Optional {
		return false, nil
	}
	// A successful current-key read owns the preference decision even if its
	// value is not unsubscribed. The legacy key is consulted only after an absent current record.
	for _, key := range []string{notificationEmailPreferenceKey(normalizedEvent, email), legacyNotificationEmailPreferenceKey(normalizedEvent, email)} {
		if strings.TrimSpace(key) == "" {
			continue
		}
		value, err := s.settingRepo.GetValue(ctx, key)
		if err == nil {
			return strings.EqualFold(strings.TrimSpace(value), "unsubscribed"), nil
		}
		if !errors.Is(err, ErrSettingNotFound) {
			return false, err
		}
	}
	return false, nil
}

// Unsubscribe verifies a signed unexpired token and persists its recipient's optional-event preference.
// Mandatory events reject the request after token validation. A successful settings
// write returns Done with the normalized event and signed email; repeated requests
// can save the same preference value again.
func (s *NotificationEmailService) Unsubscribe(ctx context.Context, token string) (NotificationEmailUnsubscribeResult, error) {
	claims, err := s.parseUnsubscribeToken(ctx, token)
	if err != nil {
		return NotificationEmailUnsubscribeResult{}, err
	}
	info, normalizedEvent, err := s.eventInfo(claims.Event)
	if err != nil {
		return NotificationEmailUnsubscribeResult{}, err
	}
	if !info.Optional {
		return NotificationEmailUnsubscribeResult{}, fmt.Errorf("%s is transactional and cannot be unsubscribed", normalizedEvent)
	}
	// Persist the normalized optional-event preference after signature and event
	// eligibility checks, so the returned Done result describes a completed settings write.
	if err := s.settingRepo.Set(ctx, notificationEmailPreferenceKey(normalizedEvent, claims.Email), "unsubscribed"); err != nil {
		return NotificationEmailUnsubscribeResult{}, err
	}
	return NotificationEmailUnsubscribeResult{Event: normalizedEvent, Email: claims.Email, Done: true}, nil
}

// eventInfo returns declared metadata for a trimmed lowercase event identifier.
// Unsupported names return an error. The metadata struct retains the declared
// placeholder backing slice; callers that expose mutable metadata copy that slice before returning it.
func (s *NotificationEmailService) eventInfo(event string) (NotificationEmailEventInfo, string, error) {
	normalized := strings.ToLower(strings.TrimSpace(event))
	info, ok := notificationEmailEventDefinitions[normalized]
	if !ok {
		return NotificationEmailEventInfo{}, "", fmt.Errorf("unsupported email template event: %s", event)
	}
	return info, normalized, nil
}

// sampleVariables returns a fresh localized example map with current site branding.
// Balance previews resolve the configured units and recharge link, and optional events receive
// a sample unsubscribe destination when absent. This map initializes previews
// and runtime rendering; runtimeVariables removes unsupplied report and plugin
// content samples before delivery.
func (s *NotificationEmailService) sampleVariables(ctx context.Context, event, languageCode string) map[string]string {
	ctx = locale.WithLanguage(ctx, languageCode)
	info := notificationEmailEventDefinitions[event]
	variables := make(map[string]string, len(info.Placeholders))
	maps.Copy(variables, notificationEmailSampleVariables(languageCode))
	if event == NotificationEmailEventPluginMessage {
		variables["plugin_name"] = "Example plugin"
		variables["subject"] = "Message example"
		variables["body_html"] = "A message from your plugin."
		if languageCode == notificationEmailLocaleChinese {
			variables["plugin_name"] = "示例插件"
			variables["subject"] = "消息示例"
			variables["body_html"] = "这是一条来自插件的消息。"
		}
	}
	if event == NotificationEmailEventBalanceLow || event == NotificationEmailEventBalanceRechargeSuccess {
		variables["balance_unit_name"] = locale.ReadSettingText(ctx, s.settingRepo, "balance_unit_name", "USD")
		// Missing or unreadable symbol settings use the same dollar default as the
		// account UI; rendering also handles empty and whitespace-only stored values.
		variables["balance_unit_symbol"], _ = s.settingRepo.GetValue(ctx, "balance_unit_symbol")
	}
	if event == NotificationEmailEventBalanceLow {
		variables["recharge_url"] = locale.ReadSettingText(ctx, s.settingRepo, "balance_low_notify_recharge_url", variables["recharge_url"])
	}
	variables["site_name"] = s.siteName(ctx)
	if variables["unsubscribe_url"] == "" && info.Optional {
		variables["unsubscribe_url"] = "https://example.com/unsubscribe"
	}
	return variables
}

// runtimeVariables returns a fresh working map combining localized samples with event values.
// Caller substitutions are copied first, then current branding, recipient email,
// optional recipient name, and selected optional-event unsubscribe links are applied.
// Balance recharge links are resolved from localized settings after caller values.
//
// Operations reports replace missing summary values with dashes and remove an
// unsupplied sample report body. Missing display controls are inferred from supplied
// summary-field presence and the detail default, preventing preview metrics from appearing
// as measured runtime results. Plugin content samples are cleared before caller
// substitution. Other event samples remain available for placeholders the business
// caller did not supply.
func (s *NotificationEmailService) runtimeVariables(ctx context.Context, event, languageCode string, input NotificationEmailSendInput) map[string]string {
	ctx = locale.WithLanguage(ctx, languageCode)
	variables := s.sampleVariables(ctx, event, languageCode)
	if event == NotificationEmailEventPluginMessage {
		// Preview samples are presentation fixtures; clear them before copying runtime values so omitted plugin content cannot become a real notification.
		for _, name := range []string{"plugin_name", "subject", "body_html"} {
			variables[name] = ""
		}
	}
	maps.Copy(variables, input.Variables)
	if event == NotificationEmailEventOpsScheduledReport {
		// Runtime reports can supply only detailed HTML. Clear unsupplied preview
		// body and summary samples before deriving section visibility from caller field presence.
		if _, ok := input.Variables["report_html"]; !ok {
			variables["report_html"] = ""
		}
		if _, ok := input.Variables["report_detail_display"]; !ok {
			// A caller can supply detailed report HTML without a display control.
			// Keep that section visible by default while retaining any explicitly supplied display value.
			variables["report_detail_display"] = "block"
		}
		hasSummaryValues := false
		for _, placeholder := range notificationEmailOpsSummaryPlaceholders {
			if _, ok := input.Variables[placeholder]; ok {
				if placeholder != "report_summary_display" {
					hasSummaryValues = true
				}
				continue
			}
			variables[placeholder] = "-"
		}
		if _, ok := input.Variables["report_summary_display"]; !ok {
			if hasSummaryValues {
				variables["report_summary_display"] = "block"
			} else {
				variables["report_summary_display"] = "none"
			}
		}
	}
	if event == NotificationEmailEventBalanceLow {
		variables["recharge_url"] = locale.ReadSettingText(ctx, s.settingRepo, "balance_low_notify_recharge_url", variables["recharge_url"])
	}
	variables["site_name"] = s.siteName(ctx)
	variables["recipient_email"] = input.RecipientEmail
	if strings.TrimSpace(input.RecipientName) != "" {
		variables["recipient_name"] = input.RecipientName
	}
	if notificationEmailEventDefinitions[event].Optional {
		// Link generation failure leaves the supplied or sample unsubscribe value in
		// place; this preparation path continues rendering with its existing variables instead of returning that error.
		if unsubscribeURL, err := s.buildUnsubscribeURL(ctx, input.RecipientEmail, event); err == nil {
			variables["unsubscribe_url"] = unsubscribeURL
		}
	}
	return variables
}

// siteName resolves the grouped site branding for the language carried by ctx.
// A decoded grouped field is authoritative, including empty copy. Missing or
// malformed group data permits the legacy site_name value, then TokenRouter
// when that legacy value is empty or unavailable.
func (s *NotificationEmailService) siteName(ctx context.Context) string {
	return locale.ReadGroupedText(ctx, s.settingRepo, "site_texts", "site_name", defaultSiteName)
}

// @project-doc docs/interfaces/configuration.md#site_link_addresses
// frontendBaseURL returns the selected public site address without trailing slashes.
// An installed reader owns stored/deployment precedence, including an empty
// result. Standalone services read only frontend_url; missing or failed reads
// leave unsubscribe links relative. API client endpoints never supply this base.
func (s *NotificationEmailService) frontendBaseURL(ctx context.Context) string {
	if s == nil {
		return ""
	}
	if s.frontendURL != nil {
		return strings.TrimRight(strings.TrimSpace(s.frontendURL(ctx)), "/")
	}
	if s.settingRepo == nil {
		return ""
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyFrontendURL)
	// Standalone mailers have no deployment reader. A failed stored-address read
	// retains relative links without substituting the unrelated client API endpoint.
	if err != nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

// buildUnsubscribeURL signs a recipient/event token and appends its escaped value to the endpoint path.
// Signing failures propagate. A usable configured base prefixes the path; an
// empty base returns the relative unsubscribe endpoint for the caller's rendered link.
func (s *NotificationEmailService) buildUnsubscribeURL(ctx context.Context, email, event string) (string, error) {
	token, err := s.createUnsubscribeToken(ctx, email, event)
	if err != nil {
		return "", err
	}
	// Escape the complete signed token as one query value so payload punctuation
	// remains within token input when the unsubscribe handler reads the request parameter.
	path := "/api/v1/settings/email-unsubscribe?token=" + url.QueryEscape(token)
	baseURL := s.frontendBaseURL(ctx)
	// A missing link base leaves an endpoint-relative path available. The renderer
	// accepts slash-prefixed URL substitutions, so the optional notice can still carry that prepared link.
	if baseURL == "" {
		return path, nil
	}
	return baseURL + path, nil
}

// createUnsubscribeToken returns signed JSON claims with a 365-day expiration and trimmed recipient.
// It obtains the persisted signing secret before encoding URL-safe payload and
// signature parts. The event is stored as supplied here; optional-event and
// supported-event restrictions are enforced when Unsubscribe consumes the token.
//
// The signature covers the encoded payload exactly as returned before the dot
// separator. Recipient and event remain together in those signed claims, while
// expiration is represented as Unix seconds for the parser's inclusive expiry check.
func (s *NotificationEmailService) createUnsubscribeToken(ctx context.Context, email, event string) (string, error) {
	secret, err := s.unsubscribeSecret(ctx)
	if err != nil {
		return "", err
	}
	// Encode expiry in whole Unix seconds for token validation.
	claims := notificationEmailUnsubscribeClaims{Email: strings.TrimSpace(email), Event: event, Exp: time.Now().Add(notificationEmailUnsubscribeTTL).Unix()}
	// Serialize recipient, event, and expiry together before signing the encoded
	// payload, so the parser verifies the same claim representation before interpreting its fields.
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := signNotificationEmailToken(secret, encodedPayload)
	return encodedPayload + "." + signature, nil
}

// parseUnsubscribeToken verifies payload/signature structure, HMAC equality, claims, and expiration.
// It trims the token, requires two nonempty parts, and verifies the signature
// before decoding JSON. Expiration at or before current Unix seconds is rejected;
// returned event claims still require Unsubscribe's supported/optional-event checks.
//
// Signature comparison covers the encoded payload before JSON is interpreted,
// tying the claim fields to the supplied signature. unsubscribeSecret can
// initialize a missing secret during this lookup, so parsing may perform a
// settings write before accepting or rejecting the token signature.
func (s *NotificationEmailService) parseUnsubscribeToken(ctx context.Context, token string) (notificationEmailUnsubscribeClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return notificationEmailUnsubscribeClaims{}, errors.New("invalid unsubscribe token")
	}
	secret, err := s.unsubscribeSecret(ctx)
	if err != nil {
		return notificationEmailUnsubscribeClaims{}, err
	}
	// Verify the supplied encoded payload before decoding its claims. This order
	// ties every interpreted recipient, event, and expiry value to the successful signature comparison.
	expected := signNotificationEmailToken(secret, parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return notificationEmailUnsubscribeClaims{}, errors.New("invalid unsubscribe token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return notificationEmailUnsubscribeClaims{}, errors.New("invalid unsubscribe token payload")
	}
	var claims notificationEmailUnsubscribeClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return notificationEmailUnsubscribeClaims{}, errors.New("invalid unsubscribe token payload")
	}
	if strings.TrimSpace(claims.Email) == "" || strings.TrimSpace(claims.Event) == "" {
		return notificationEmailUnsubscribeClaims{}, errors.New("invalid unsubscribe token claims")
	}
	if claims.Exp <= time.Now().Unix() {
		return notificationEmailUnsubscribeClaims{}, errors.New("unsubscribe token expired")
	}
	return claims, nil
}

// unsubscribeSecret returns the current signing secret or persists a fresh random secret.
// A nonblank stored value is trimmed and reused. Missing or blank configuration
// permits generation, while other read errors stop before any replacement is written.
//
// Instance-local coordination spans read, random-byte generation, and persistence
// so concurrent first requests using this service share the saved secret. Lock
// waits obey ctx and release after the complete sequence. Other processes have
// separate coordinators, so the method establishes no cross-process creation lock.
func (s *NotificationEmailService) unsubscribeSecret(ctx context.Context) (string, error) {
	// All first-secret readers in this instance use one entry through storage.
	// Hold it until an existing secret is returned or a generated replacement has been persisted.
	release, lockErr := s.locks.acquire(ctx, notificationEmailUnsubscribeSecretKey)
	if lockErr != nil {
		return "", lockErr
	}
	defer release()
	secret, err := s.settingRepo.GetValue(ctx, notificationEmailUnsubscribeSecretKey)
	if err == nil && strings.TrimSpace(secret) != "" {
		return strings.TrimSpace(secret), nil
	}
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return "", err
	}
	// Thirty-two random bytes supply 256 bits of secret entropy.
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	secret = base64.RawURLEncoding.EncodeToString(buf)
	if err := s.settingRepo.Set(ctx, notificationEmailUnsubscribeSecretKey, secret); err != nil {
		return "", err
	}
	return secret, nil
}

// deliveryExists reports whether any supplied nonblank delivery key is present in storage.
// Stored contents do not affect presence. Missing keys allow later alternatives,
// while other read errors stop with an error; no available marker returns false.
func (s *NotificationEmailService) deliveryExists(ctx context.Context, keys ...string) (bool, error) {
	// Marker contents are irrelevant to suppression. Missing current keys leave
	// legacy alternatives eligible, while other read failures stop before another send can be admitted.
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		_, err := s.settingRepo.GetValue(ctx, key)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, ErrSettingNotFound) {
			return false, err
		}
	}
	return false, nil
}

// validateNotificationEmailTemplate checks nonblank copy, configured sizes, and event-admitted substitutions.
// Subject length counts Unicode characters after trimming, while HTML length
// counts bytes in the supplied body. Recognized placeholder spellings must belong
// to the selected event's definition; other text remains literal template content.
func validateNotificationEmailTemplate(event, subject, htmlBody string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return errors.New("email subject cannot be empty")
	}
	// Subject capacity counts characters so multibyte names share the same limit.
	if len([]rune(subject)) > notificationEmailMaxSubjectLength {
		return fmt.Errorf("email subject cannot exceed %d characters", notificationEmailMaxSubjectLength)
	}
	if strings.TrimSpace(htmlBody) == "" {
		return errors.New("email html cannot be empty")
	}
	// HTML capacity limits stored template bytes before substitution.
	if len([]byte(htmlBody)) > notificationEmailMaxHTMLLength {
		return fmt.Errorf("email html cannot exceed %d bytes", notificationEmailMaxHTMLLength)
	}
	allowed := notificationEmailAllowedPlaceholderSet(event)
	for _, placeholder := range notificationEmailPlaceholdersIn(subject + "\n" + htmlBody) {
		if _, ok := allowed[placeholder]; !ok {
			return fmt.Errorf("unsupported placeholder {{%s}} for event %s", placeholder, event)
		}
	}
	return nil
}

// RenderNotificationEmail validates copy and substitutes event variables into subject and HTML.
// HTML substitutions are escaped unless the admitted report_html field has a
// trusted raw-map value; literal template markup remains as supplied. Unsupported
// ordinary _url values become empty, and subject values have CR/LF removed.
// Callers supply an exact event identifier and keep both maps stable during
// rendering. Unknown events with no recognized placeholders can pass validation.
// Balance events replace dollar prefixes attached to amount placeholders in saved
// templates, and missing unit variables use the account display defaults.
func RenderNotificationEmail(event, subject, htmlBody string, variables map[string]string, rawHTMLVariables map[string]string) (NotificationEmailPreview, error) {
	if err := validateNotificationEmailTemplate(event, subject, htmlBody); err != nil {
		return NotificationEmailPreview{}, err
	}
	if event == NotificationEmailEventBalanceLow || event == NotificationEmailEventBalanceRechargeSuccess {
		// Saved templates can contain the built-in dollar prefix from before unit
		// placeholders existed. Normalize those amount prefixes without rewriting stored administrator content.
		subject = notificationEmailBalanceDollarPattern.ReplaceAllString(subject, "{{balance_unit_symbol}}${1}")
		htmlBody = notificationEmailBalanceDollarPattern.ReplaceAllString(htmlBody, "{{balance_unit_symbol}}${1}")
		values := map[string]string{"balance_unit_name": "USD", "balance_unit_symbol": "$"}
		for key, value := range variables {
			if key == "balance_unit_name" || key == "balance_unit_symbol" {
				value = strings.TrimSpace(value)
				if value == "" {
					continue
				}
			}
			values[key] = value
		}
		variables = values
	}
	renderedSubject, err := renderNotificationEmailString(event, subject, variables, nil, false)
	if err != nil {
		return NotificationEmailPreview{}, err
	}
	renderedHTML, err := renderNotificationEmailString(event, htmlBody, variables, rawHTMLVariables, true)
	if err != nil {
		return NotificationEmailPreview{}, err
	}
	return NotificationEmailPreview{Subject: SanitizeEmailHeader(renderedSubject), HTML: renderedHTML}, nil
}

// renderNotificationEmailString substitutes recognized event fields using subject or HTML escaping rules.
// Unknown recognized placeholders return an error; absent ordinary values become
// empty strings. A trusted raw value is used only for a report or plugin body field admitted
// by notificationEmailRawHTMLAllowed.
//
// Raw values are selected before ordinary URL and escaping checks. Otherwise,
// unsafe _url values become empty, HTML values are escaped, and subject values
// have line separators removed. Replacement matches are evaluated from the original
// template, so substitution text is inserted as content rather than recursively
// interpreted as another template placeholder.
func renderNotificationEmailString(event, raw string, variables map[string]string, rawHTMLVariables map[string]string, escapeHTML bool) (string, error) {
	allowed := notificationEmailAllowedPlaceholderSet(event)
	var renderErr error
	rendered := notificationEmailPlaceholderPattern.ReplaceAllStringFunc(raw, func(match string) string {
		if renderErr != nil {
			return ""
		}
		parts := notificationEmailPlaceholderPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return ""
		}
		name := parts[1]
		if _, ok := allowed[name]; !ok {
			renderErr = fmt.Errorf("unsupported placeholder {{%s}} for event %s", name, event)
			return ""
		}
		value := variables[name]
		// Raw substitution is confined to admitted report HTML and the plugin adapter
		// body that has already escaped every caller-supplied text character. Selecting that
		// map value here intentionally bypasses the ordinary escaping applied to other template variables.
		if escapeHTML && notificationEmailRawHTMLAllowed(event, name) {
			if rawHTMLVariables != nil {
				if rawValue, ok := rawHTMLVariables[name]; ok {
					return rawValue
				}
			}
		}
		// URL placeholders can otherwise place an unsupported scheme in HTML links.
		// Clear unsafe ordinary values before escaping while retaining the surrounding template structure.
		if strings.HasSuffix(name, "_url") && !isSafeNotificationEmailURL(value) {
			value = ""
		}
		// Escape the inserted value while leaving literal template markup intact.
		// Replacement scans the original template, so inserted brace text does not become another substitution.
		if escapeHTML {
			return html.EscapeString(value)
		}
		return SanitizeEmailHeader(value)
	})
	if renderErr != nil {
		return "", renderErr
	}
	return rendered, nil
}

// notificationEmailRawHTMLAllowed identifies the report and plugin body fields admitting trusted HTML.
func notificationEmailRawHTMLAllowed(event, placeholder string) bool {
	return (event == NotificationEmailEventOpsScheduledReport && placeholder == "report_html") || (event == NotificationEmailEventPluginMessage && placeholder == "body_html")
}

// notificationEmailAllowedPlaceholderSet returns a fresh set of declared names for the exact event.
// An unknown event yields an empty set. This helper expects prior event normalization
// when callers want case-insensitive public input to select the corresponding definition.
func notificationEmailAllowedPlaceholderSet(event string) map[string]struct{} {
	info := notificationEmailEventDefinitions[event]
	allowed := make(map[string]struct{}, len(info.Placeholders))
	for _, placeholder := range info.Placeholders {
		allowed[placeholder] = struct{}{}
	}
	return allowed
}

// notificationEmailPlaceholdersIn returns unique recognized variable names in first-occurrence order.
// Only spellings matched by the shared placeholder regexp participate, so malformed
// or unrecognized brace text remains ordinary content during validation and rendering.
func notificationEmailPlaceholdersIn(raw string) []string {
	matches := notificationEmailPlaceholderPattern.FindAllStringSubmatch(raw, -1)
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		if _, exists := seen[match[1]]; exists {
			continue
		}
		seen[match[1]] = struct{}{}
		out = append(out, match[1])
	}
	return out
}

// normalizeNotificationLocale negotiates a language hint using English as its fallback.
// It delegates aliases and Accept-Language selection to locale.Negotiate, producing
// one supported language code for template keys, samples, and remembered preferences.
func normalizeNotificationLocale(raw string) string {
	return locale.Negotiate(raw, notificationEmailDefaultLocale)
}

// SetFrontendURLReader installs the public site address resolver for unsubscribe links.
// Call during assembly before concurrent sends because assignment is unsynchronized.
// The reader owns deployment fallback; nil selects standalone stored-setting reads.
func (s *NotificationEmailService) SetFrontendURLReader(read func(context.Context) string) {
	s.frontendURL = read
}

// SetRecipientLocaleReader installs the account-language lookup used before persisted language hints.
// Call during assembly before concurrent recipient resolution; the field assignment
// is unsynchronized. A nil reader leaves legacy user/email records and site default
// as the service's available preference sources.
func (s *NotificationEmailService) SetRecipientLocaleReader(read func(context.Context, int64, string) string) {
	s.recipientLocale = read
}

// notificationEmailTemplateKey joins the selected event and language into its stored override key.
func notificationEmailTemplateKey(event, locale string) string {
	return notificationEmailTemplateKeyPrefix + event + ":" + locale
}

// notificationEmailPreferenceKey returns the current hashed preference identity or empty for missing inputs.
// It separates trimmed event and normalized recipient with NUL before hashing,
// then prefixes the digest with v2. NotificationEmailHash lowercases that complete
// identity, so event and recipient case differences share the resulting preference key.
// The digest keeps the persisted key size independent of email length while
// the NUL component separator distinguishes the normalized event/address pair
// from the older colon-separated preference representation read for compatibility.
func notificationEmailPreferenceKey(event, email string) string {
	if strings.TrimSpace(event) == "" || strings.TrimSpace(email) == "" {
		return ""
	}
	identity := strings.TrimSpace(event) + "\x00" + strings.ToLower(strings.TrimSpace(email))
	return notificationEmailPreferenceKeyPrefix + "v2:" + NotificationEmailHash(identity)
}

// legacyNotificationEmailPreferenceKey returns the prior event/address key used for existing preference lookups.
// Event text is retained as supplied, while the address uses NotificationEmailHash.
// An empty normalized address still leaves the event's colon prefix in this
// compatibility key, reproducing that format for legacy reads.
func legacyNotificationEmailPreferenceKey(event, email string) string {
	return notificationEmailPreferenceKeyPrefix + event + ":" + NotificationEmailHash(email)
}

// notificationEmailDeliveryKey returns a v2 digest identity for one source-backed recipient notice.
// Blank source type, source ID, or recipient returns empty and disables keyed
// delivery handling. Event and recipient are normalized, while source and reminder
// parts pass through their existing key-part normalization.
//
// NUL separators distinguish the component positions before hashing and keep the
// persisted key length independent of source identifier size. Normalization still
// maps unsupported source characters to underscores, so caller identifiers differing
// only through that transformation can share a delivery identity. The reminder
// component separates recurring notices after that normalization.
func notificationEmailDeliveryKey(event, sourceType, sourceID, recipient, reminderKey string) string {
	if strings.TrimSpace(sourceType) == "" || strings.TrimSpace(sourceID) == "" || strings.TrimSpace(recipient) == "" {
		return ""
	}
	identity := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(event)),
		safeNotificationEmailKeyPart(sourceType),
		safeNotificationEmailKeyPart(sourceID),
		strings.ToLower(strings.TrimSpace(recipient)),
		safeNotificationEmailKeyPart(reminderKey),
	}, "\x00")
	return notificationEmailDeliveryKeyPrefix + "v2:" + NotificationEmailHash(identity)
}

// legacyNotificationEmailDeliveryKey reconstructs the prior colon-separated marker identity for compatibility reads.
// Missing source type, source ID, or recipient returns empty. Source and reminder
// parts retain the same sanitizer, while recipient uses its normalized address hash;
// a nonblank reminder appends its older optional suffix.
func legacyNotificationEmailDeliveryKey(event, sourceType, sourceID, recipient, reminderKey string) string {
	if strings.TrimSpace(sourceType) == "" || strings.TrimSpace(sourceID) == "" || strings.TrimSpace(recipient) == "" {
		return ""
	}
	parts := []string{notificationEmailDeliveryKeyPrefix, event, ":", safeNotificationEmailKeyPart(sourceType), ":", safeNotificationEmailKeyPart(sourceID), ":", NotificationEmailHash(recipient)}
	if strings.TrimSpace(reminderKey) != "" {
		parts = append(parts, ":", safeNotificationEmailKeyPart(reminderKey))
	}
	return strings.Join(parts, "")
}

// NotificationEmailHash returns a hexadecimal SHA-256 digest of trimmed lowercase input.
// An empty normalized value returns empty. The helper hashes both addresses and
// composite key identities, so normalization applies to the complete value supplied by its caller.
func NotificationEmailHash(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}

// safeNotificationEmailKeyPart trims and lowercases a source or reminder component for both marker formats.
// ASCII letters, digits, underscore, hyphen, and dot remain; each other rune becomes
// underscore. Both current digests and legacy marker reconstruction use this value.
//
// For example, a/b and a:b both become a_b. Callers choose source identifiers
// that remain distinct after normalization, because adding separators or hashing
// afterward cannot recover a distinction already removed from one component.
func safeNotificationEmailKeyPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	// Builder rune writes target memory and return nil errors; ignoring those
	// error results leaves only the normalized identity text as this helper's output.
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			_, _ = builder.WriteRune(r)
		} else {
			_, _ = builder.WriteRune('_')
		}
	}
	return builder.String()
}

// signNotificationEmailToken returns the URL-safe unpadded HMAC-SHA256 signature of the encoded payload.
// The secret is used as supplied text bytes, including the stored base64 spelling.
// Payload bytes are signed exactly as supplied for comparison before claim decoding.
func signNotificationEmailToken(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	// Hash writes accept the entire payload and return nil errors. Sign its
	// exact encoded bytes so the parser can compare the same payload representation before decoding claims.
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// isSafeNotificationEmailURL reports whether a substitution uses an admitted URL form.
// Blank values are accepted; absolute links allow HTTP, HTTPS, and mailto schemes.
// A nonabsolute value must begin with slash after trimming, including a protocol-relative
// slash-prefixed link accepted by this check.
func isSafeNotificationEmailURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	if parsed.IsAbs() {
		scheme := strings.ToLower(parsed.Scheme)
		return scheme == "http" || scheme == "https" || scheme == "mailto"
	}
	return strings.HasPrefix(trimmed, "/")
}

// notificationEmailSampleVariables returns a fresh sample map for Chinese or English template rendering.
// The hint is normalized first; Chinese selects localized example text, while
// other supported selections use English examples. Report summary samples are added
// before the map reaches preview or runtime variable preparation.
func notificationEmailSampleVariables(locale string) map[string]string {
	if normalizeNotificationLocale(locale) == notificationEmailLocaleChinese {
		variables := map[string]string{
			"site_name":           defaultSiteName,
			"recipient_name":      "张三",
			"recipient_email":     "user@example.com",
			"verification_code":   "123456",
			"expires_in_minutes":  "15",
			"reset_url":           "https://example.com/reset-password?token=preview",
			"team_name":           "平台研发团队",
			"transfer_url":        "https://example.com/team?transfer=preview",
			"invitation_url":      "https://example.com/team?invitation=preview",
			"expires_at":          "2026-08-03T12:00:00+09:00",
			"subscription_group":  "Claude Pro",
			"subscription_days":   "30",
			"expiry_time":         "2026-06-18 12:00",
			"days_remaining":      "3",
			"current_balance":     "12.34",
			"balance":             "999",
			"unit":                "CNY",
			"observed_at":         "2026-10-16T00:00:00Z",
			"threshold":           "20.00",
			"recharge_url":        "https://example.com/recharge",
			"recharge_amount":     "50.00",
			"order_id":            "1024",
			"unsubscribe_url":     "https://example.com/unsubscribe",
			"provider_id":         "1001",
			"provider_name":       "openai-main",
			"platform":            "openai",
			"quota_dimension":     "每日额度",
			"quota_used":          "80.00",
			"quota_limit":         "100.00",
			"quota_remaining":     "20.00",
			"quota_threshold":     "20%",
			"triggered_at":        "2026-05-20 12:00:00",
			"group_name":          "示例分组",
			"moderation_category": "violence",
			"moderation_score":    "0.982",
			"violation_count":     "2",
			"ban_threshold":       "3",
			"rule_name":           "错误率过高",
			"severity":            "critical",
			"alert_status":        "firing",
			"metric_type":         "error_rate",
			"operator":            ">=",
			"metric_value":        "12.50",
			"threshold_value":     "10.00",
			"alert_description":   "最近 10 分钟错误率超过阈值",
			"report_name":         "日报",
			"report_type":         "daily_summary",
			"report_start_time":   "2026-07-18T01:00:26Z",
			"report_end_time":     "2026-07-19T01:00:26Z",
			"report_html":         "<h2>日报</h2><p>请求量：2,374</p>",
		}
		addNotificationEmailOpsSummarySampleVariables(variables)
		return variables
	}
	variables := map[string]string{
		"site_name":           defaultSiteName,
		"recipient_name":      "Alex",
		"recipient_email":     "user@example.com",
		"verification_code":   "123456",
		"expires_in_minutes":  "15",
		"reset_url":           "https://example.com/reset-password?token=preview",
		"team_name":           "Platform Engineering",
		"transfer_url":        "https://example.com/team?transfer=preview",
		"invitation_url":      "https://example.com/team?invitation=preview",
		"expires_at":          "2026-08-03T12:00:00+09:00",
		"subscription_group":  "Claude Pro",
		"subscription_days":   "30",
		"expiry_time":         "2026-06-18 12:00",
		"days_remaining":      "3",
		"current_balance":     "12.34",
		"balance":             "999",
		"unit":                "CNY",
		"observed_at":         "2026-10-16T00:00:00Z",
		"threshold":           "20.00",
		"recharge_url":        "https://example.com/recharge",
		"recharge_amount":     "50.00",
		"order_id":            "1024",
		"unsubscribe_url":     "https://example.com/unsubscribe",
		"provider_id":         "1001",
		"provider_name":       "openai-main",
		"platform":            "openai",
		"quota_dimension":     "Daily quota",
		"quota_used":          "80.00",
		"quota_limit":         "100.00",
		"quota_remaining":     "20.00",
		"quota_threshold":     "20%",
		"triggered_at":        "2026-05-20 12:00:00",
		"group_name":          "Default group",
		"moderation_category": "violence",
		"moderation_score":    "0.982",
		"violation_count":     "2",
		"ban_threshold":       "3",
		"rule_name":           "High error rate",
		"severity":            "critical",
		"alert_status":        "firing",
		"metric_type":         "error_rate",
		"operator":            ">=",
		"metric_value":        "12.50",
		"threshold_value":     "10.00",
		"alert_description":   "Error rate exceeded threshold in the last 10 minutes.",
		"report_name":         "Daily summary",
		"report_type":         "daily_summary",
		"report_start_time":   "2026-07-18T01:00:26Z",
		"report_end_time":     "2026-07-19T01:00:26Z",
		"report_html":         "<h2>Daily summary</h2><p>Requests: 2,374</p>",
	}
	addNotificationEmailOpsSummarySampleVariables(variables)
	return variables
}

// addNotificationEmailOpsSummarySampleVariables writes report-preview metrics and display controls into variables.
// The map must be writable. Existing values with these names are replaced;
// runtimeVariables subsequently clears or replaces unsupplied report samples before actual sending.
func addNotificationEmailOpsSummarySampleVariables(variables map[string]string) {
	variables["report_summary_display"] = "block"
	variables["report_detail_display"] = "none"
	variables["report_total_requests"] = "2,374"
	variables["report_success_count"] = "1,451"
	variables["report_sla_error_count"] = "2"
	variables["report_business_limited_count"] = "921"
	variables["report_sla"] = "99.86%"
	variables["report_error_rate"] = "0.14%"
	variables["report_upstream_error_rate"] = "0.28%"
	variables["report_upstream_error_count_excl_429_529"] = "4"
	variables["report_upstream_429_count"] = "0"
	variables["report_upstream_529_count"] = "0"
	variables["report_latency_p50"] = "8,231 ms"
	variables["report_latency_p99"] = "151,260 ms"
	variables["report_ttft_p50"] = "1,674 ms"
	variables["report_ttft_p99"] = "11,222 ms"
	variables["report_tokens"] = "121,550,190"
	variables["report_qps_current"] = "0.0"
	variables["report_qps_peak"] = "1.2"
	variables["report_qps_avg"] = "0.0"
	variables["report_tps_current"] = "0.0"
	variables["report_tps_peak"] = "133421.2"
	variables["report_tps_avg"] = "1406.8"
}

// notificationEmailOpsScheduledReportTemplate returns the built-in operations-report HTML for the normalized language.
// Chinese selects its translated report headings; other selections use English.
// The markup exposes summary and detail display controls as placeholders for later rendering.
func notificationEmailOpsScheduledReportTemplate(locale string) string {
	if normalizeNotificationLocale(locale) == notificationEmailLocaleChinese {
		return `<!DOCTYPE html>
<html lang="zh-Hans">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
    body { margin: 0; padding: 24px 12px; background: #f4f6f8; color: #1f2937; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif; }
    .container { width: 100%; max-width: 680px; margin: 0 auto; background: #ffffff; border: 1px solid #dfe7ea; border-radius: 8px; overflow: hidden; }
    .header { padding: 28px 32px 24px; background: #0f766e; color: #ffffff; }
    .eyebrow { margin: 0 0 8px; color: #ccfbf1; font-size: 12px; font-weight: 700; letter-spacing: 0; text-transform: uppercase; }
    h1 { margin: 0; font-size: 26px; line-height: 1.3; }
    .header p { margin: 8px 0 0; color: #e6fffb; font-size: 14px; }
    .content { padding: 28px 32px 32px; }
    .meta { width: 100%; margin: 0 0 20px; border-collapse: collapse; background: #f8fafc; border: 1px solid #e2e8f0; }
    .meta td { padding: 10px 12px; border-bottom: 1px solid #e2e8f0; font-size: 13px; vertical-align: top; }
    .meta tr:last-child td { border-bottom: 0; }
    .meta-label { width: 112px; color: #64748b; font-weight: 600; }
    .section-title { margin: 28px 0 12px; color: #0f172a; font-size: 16px; line-height: 1.4; }
    .metric-grid { width: 100%; border-collapse: separate; border-spacing: 8px; margin: -8px; }
    .metric-cell { width: 50%; padding: 14px 16px; border: 1px solid #e2e8f0; background: #ffffff; vertical-align: top; }
    .metric-label { display: block; color: #64748b; font-size: 12px; line-height: 1.4; }
    .metric-value { display: block; margin-top: 6px; color: #0f172a; font-size: 20px; font-weight: 700; line-height: 1.2; }
    .metric-value.good { color: #15803d; }
    .metric-value.alert { color: #b91c1c; }
    .detail { width: 100%; border-collapse: collapse; }
    .detail td { padding: 10px 12px; border-bottom: 1px solid #e2e8f0; font-size: 13px; }
    .detail td:first-child { width: 56%; color: #475569; }
    .detail td:last-child { color: #0f172a; font-weight: 600; text-align: right; }
    .report-detail { margin-top: 28px; }
    .report-detail:empty { display: none; }
    .footer { padding: 18px 32px; background: #f8fafc; border-top: 1px solid #e2e8f0; color: #64748b; font-size: 12px; line-height: 1.6; }
    @media only screen and (max-width: 620px) {
      body { padding: 0; }
      .container { border: 0; border-radius: 0; }
      .header, .content, .footer { padding-left: 20px; padding-right: 20px; }
      .metric-grid, .metric-grid tbody, .metric-grid tr, .metric-cell { display: block; width: 100% !important; box-sizing: border-box; }
      .metric-cell { margin: 8px 0; }
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <p class="eyebrow">运维报表</p>
      <h1>{{report_name}}</h1>
      <p>{{site_name}} 的运行概览</p>
    </div>
    <div class="content">
      <table class="meta" role="presentation">
        <tr><td class="meta-label">报表</td><td>{{report_name}}</td></tr>
        <tr><td class="meta-label">类型</td><td>{{report_type}}</td></tr>
        <tr><td class="meta-label">统计周期</td><td>{{report_start_time}} 至 {{report_end_time}} (UTC)</td></tr>
      </table>

      <div style="display: {{report_summary_display}};">
      <h2 class="section-title">请求概览</h2>
      <table class="metric-grid" role="presentation"><tr>
        <td class="metric-cell"><span class="metric-label">总请求数</span><span class="metric-value">{{report_total_requests}}</span></td>
        <td class="metric-cell"><span class="metric-label">成功请求</span><span class="metric-value good">{{report_success_count}}</span></td>
      </tr><tr>
        <td class="metric-cell"><span class="metric-label">SLA 错误</span><span class="metric-value alert">{{report_sla_error_count}}</span></td>
        <td class="metric-cell"><span class="metric-label">业务限流</span><span class="metric-value">{{report_business_limited_count}}</span></td>
      </tr></table>

      <h2 class="section-title">可靠性</h2>
      <table class="detail" role="presentation">
        <tr><td>SLA</td><td>{{report_sla}}</td></tr>
        <tr><td>错误率</td><td>{{report_error_rate}}</td></tr>
        <tr><td>上游错误率（不含 429 / 529）</td><td>{{report_upstream_error_rate}}</td></tr>
        <tr><td>上游错误（不含 429 / 529）</td><td>{{report_upstream_error_count_excl_429_529}}</td></tr>
        <tr><td>上游 429 / 529</td><td>{{report_upstream_429_count}} / {{report_upstream_529_count}}</td></tr>
      </table>

      <h2 class="section-title">延迟表现</h2>
      <table class="detail" role="presentation">
        <tr><td>请求延迟 p50 / p99</td><td>{{report_latency_p50}} / {{report_latency_p99}}</td></tr>
        <tr><td>首 Token 时间 p50 / p99</td><td>{{report_ttft_p50}} / {{report_ttft_p99}}</td></tr>
      </table>

      <h2 class="section-title">吞吐量</h2>
      <table class="detail" role="presentation">
        <tr><td>Token 消耗</td><td>{{report_tokens}}</td></tr>
        <tr><td>QPS（当前 / 峰值 / 平均）</td><td>{{report_qps_current}} / {{report_qps_peak}} / {{report_qps_avg}}</td></tr>
        <tr><td>TPS（当前 / 峰值 / 平均）</td><td>{{report_tps_current}} / {{report_tps_peak}} / {{report_tps_avg}}</td></tr>
      </table>

      </div>
      <div class="report-detail" style="display: {{report_detail_display}};">{{report_html}}</div>
    </div>
    <div class="footer">此邮件由 {{site_name}} 自动发送，请勿直接回复。</div>
  </div>
</body>
</html>`
	}

	return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
    body { margin: 0; padding: 24px 12px; background: #f4f6f8; color: #1f2937; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    .container { width: 100%; max-width: 680px; margin: 0 auto; background: #ffffff; border: 1px solid #dfe7ea; border-radius: 8px; overflow: hidden; }
    .header { padding: 28px 32px 24px; background: #0f766e; color: #ffffff; }
    .eyebrow { margin: 0 0 8px; color: #ccfbf1; font-size: 12px; font-weight: 700; letter-spacing: 0; text-transform: uppercase; }
    h1 { margin: 0; font-size: 26px; line-height: 1.3; }
    .header p { margin: 8px 0 0; color: #e6fffb; font-size: 14px; }
    .content { padding: 28px 32px 32px; }
    .meta { width: 100%; margin: 0 0 20px; border-collapse: collapse; background: #f8fafc; border: 1px solid #e2e8f0; }
    .meta td { padding: 10px 12px; border-bottom: 1px solid #e2e8f0; font-size: 13px; vertical-align: top; }
    .meta tr:last-child td { border-bottom: 0; }
    .meta-label { width: 112px; color: #64748b; font-weight: 600; }
    .section-title { margin: 28px 0 12px; color: #0f172a; font-size: 16px; line-height: 1.4; }
    .metric-grid { width: 100%; border-collapse: separate; border-spacing: 8px; margin: -8px; }
    .metric-cell { width: 50%; padding: 14px 16px; border: 1px solid #e2e8f0; background: #ffffff; vertical-align: top; }
    .metric-label { display: block; color: #64748b; font-size: 12px; line-height: 1.4; }
    .metric-value { display: block; margin-top: 6px; color: #0f172a; font-size: 20px; font-weight: 700; line-height: 1.2; }
    .metric-value.good { color: #15803d; }
    .metric-value.alert { color: #b91c1c; }
    .detail { width: 100%; border-collapse: collapse; }
    .detail td { padding: 10px 12px; border-bottom: 1px solid #e2e8f0; font-size: 13px; }
    .detail td:first-child { width: 56%; color: #475569; }
    .detail td:last-child { color: #0f172a; font-weight: 600; text-align: right; }
    .report-detail { margin-top: 28px; }
    .report-detail:empty { display: none; }
    .footer { padding: 18px 32px; background: #f8fafc; border-top: 1px solid #e2e8f0; color: #64748b; font-size: 12px; line-height: 1.6; }
    @media only screen and (max-width: 620px) {
      body { padding: 0; }
      .container { border: 0; border-radius: 0; }
      .header, .content, .footer { padding-left: 20px; padding-right: 20px; }
      .metric-grid, .metric-grid tbody, .metric-grid tr, .metric-cell { display: block; width: 100% !important; box-sizing: border-box; }
      .metric-cell { margin: 8px 0; }
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <p class="eyebrow">Operations report</p>
      <h1>{{report_name}}</h1>
      <p>{{site_name}} runtime overview</p>
    </div>
    <div class="content">
      <table class="meta" role="presentation">
        <tr><td class="meta-label">Report</td><td>{{report_name}}</td></tr>
        <tr><td class="meta-label">Type</td><td>{{report_type}}</td></tr>
        <tr><td class="meta-label">Reporting period</td><td>{{report_start_time}} to {{report_end_time}} (UTC)</td></tr>
      </table>

      <div style="display: {{report_summary_display}};">
      <h2 class="section-title">Request Overview</h2>
      <table class="metric-grid" role="presentation"><tr>
        <td class="metric-cell"><span class="metric-label">Total Requests</span><span class="metric-value">{{report_total_requests}}</span></td>
        <td class="metric-cell"><span class="metric-label">Successful Requests</span><span class="metric-value good">{{report_success_count}}</span></td>
      </tr><tr>
        <td class="metric-cell"><span class="metric-label">SLA Errors</span><span class="metric-value alert">{{report_sla_error_count}}</span></td>
        <td class="metric-cell"><span class="metric-label">Business Limited</span><span class="metric-value">{{report_business_limited_count}}</span></td>
      </tr></table>

      <h2 class="section-title">Reliability</h2>
      <table class="detail" role="presentation">
        <tr><td>SLA</td><td>{{report_sla}}</td></tr>
        <tr><td>Error Rate</td><td>{{report_error_rate}}</td></tr>
        <tr><td>Upstream Error Rate (excluding 429 / 529)</td><td>{{report_upstream_error_rate}}</td></tr>
        <tr><td>Upstream Errors (excluding 429 / 529)</td><td>{{report_upstream_error_count_excl_429_529}}</td></tr>
        <tr><td>Upstream 429 / 529</td><td>{{report_upstream_429_count}} / {{report_upstream_529_count}}</td></tr>
      </table>

      <h2 class="section-title">Latency</h2>
      <table class="detail" role="presentation">
        <tr><td>Request Latency p50 / p99</td><td>{{report_latency_p50}} / {{report_latency_p99}}</td></tr>
        <tr><td>Time to First Token p50 / p99</td><td>{{report_ttft_p50}} / {{report_ttft_p99}}</td></tr>
      </table>

      <h2 class="section-title">Throughput</h2>
      <table class="detail" role="presentation">
        <tr><td>Tokens Consumed</td><td>{{report_tokens}}</td></tr>
        <tr><td>QPS (current / peak / average)</td><td>{{report_qps_current}} / {{report_qps_peak}} / {{report_qps_avg}}</td></tr>
        <tr><td>TPS (current / peak / average)</td><td>{{report_tps_current}} / {{report_tps_peak}} / {{report_tps_avg}}</td></tr>
      </table>

      </div>
      <div class="report-detail" style="display: {{report_detail_display}};">{{report_html}}</div>
    </div>
    <div class="footer">This email was sent automatically by {{site_name}}. Please do not reply directly.</div>
  </div>
</body>
</html>`
}

// notificationEmailChineseCard wraps trusted event markup with the shared Chinese-language card footer.
// Callers supply accent, title, and content fragments suitable for direct HTML
// insertion; the delegated card builder leaves those fragments and their placeholders intact.
func notificationEmailChineseCard(accent, title, content string) string {
	return notificationEmailCard(accent, title, content, "zh-Hans")
}

// notificationEmailCard builds trusted built-in markup around accent, title, and content fragments.
// Only a first language argument exactly equal to zh-Hans selects the Chinese
// footer; other inputs use English. These constructor fragments are inserted as
// HTML directly and still contain placeholders for later event-variable rendering.
func notificationEmailCard(accent, title, content string, language ...string) string {
	selected := "en"
	footer := "This email was sent by {{site_name}}. Please do not reply directly."
	if len(language) > 0 && language[0] == "zh-Hans" {
		selected = "zh-Hans"
		footer = "此邮件由 {{site_name}} 自动发送，请勿直接回复。"
	}
	return `<!DOCTYPE html>
<html lang="` + selected + `">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
    body { margin: 0; padding: 24px; background: #f4f4f5; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: #18181b; }
    .container { max-width: 640px; margin: 0 auto; background: #ffffff; border-radius: 12px; overflow: hidden; box-shadow: 0 8px 30px rgba(15, 23, 42, 0.10); }
    .header { background: ` + accent + `; color: #ffffff; padding: 28px 32px; }
    .header h1 { margin: 0; font-size: 24px; line-height: 1.25; }
    .content { padding: 32px; font-size: 15px; line-height: 1.7; }
    .button { display: inline-block; margin-top: 12px; padding: 11px 18px; border-radius: 8px; background: ` + accent + `; color: #ffffff; text-decoration: none; font-weight: 600; }
    .muted { color: #71717a; font-size: 13px; }
    .footer { padding: 18px 32px; background: #fafafa; color: #a1a1aa; font-size: 12px; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header"><h1>` + title + `</h1></div>
    <div class="content">` + content + `</div>
    <div class="footer">` + footer + `</div>
  </div>
</body>
</html>`
}
