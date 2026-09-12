package handler

import (
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/handler/admin"
	sitehttpapi "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
)

// AdminHandlers contains all admin-related HTTP handlers
type AdminHandlers struct {
	Dashboard             *admin.DashboardHandler
	User                  *admin.UserHandler
	Group                 *admin.GroupHandler
	Account               *admin.AccountHandler
	Announcement          *sitehttpapi.AdminAnnouncementHandler
	DataManagement        *admin.DataManagementHandler
	Backup                *admin.BackupHandler
	OAuth                 *admin.OAuthHandler
	OpenAIOAuth           *admin.OpenAIOAuthHandler
	GeminiOAuth           *admin.GeminiOAuthHandler
	AntigravityOAuth      *admin.AntigravityOAuthHandler
	QoderOAuth            *admin.QoderOAuthHandler
	GrokOAuth             *admin.GrokOAuthHandler
	Proxy                 *admin.ProxyHandler
	Redeem                *billinghttpapi.AdminRedeemHandler
	Promo                 *admin.PromoHandler
	Setting               *admin.SettingHandler
	Ops                   *admin.OpsHandler
	System                *admin.SystemHandler
	Subscription          *billinghttpapi.AdminSubscriptionHandler
	Usage                 *admin.UsageHandler
	UserAttribute         *admin.UserAttributeHandler
	ErrorPassthrough      *admin.ErrorPassthroughHandler
	TLSFingerprintProfile *admin.TLSFingerprintProfileHandler
	TLSFingerprintRouter  *admin.TLSFingerprintRouterHandler
	APIKey                *admin.AdminAPIKeyHandler
	ScheduledTest         *admin.ScheduledTestHandler
	Channel               *admin.ChannelHandler
	ContentModeration     *admin.ContentModerationHandler
	Payment               *admin.PaymentHandler
	Affiliate             *admin.AffiliateHandler
	CodexInviteReset      *admin.CodexInviteResetHandler
	AuditLog              *admin.AuditLogHandler
	Team                  *admin.TeamHandler
}

// Handlers contains all HTTP handlers
type Handlers struct {
	Plans            *billinghttpapi.PlanHandler
	PlatformQuota    *billinghttpapi.QuotaHandler
	Auth             AuthEndpoints
	User             *UserHandler
	APIKey           *APIKeyHandler
	Usage            *UsageHandler
	Redeem           *billinghttpapi.RedeemHandler
	Subscription     *billinghttpapi.SubscriptionHandler
	Announcement     *sitehttpapi.AnnouncementHandler
	ModelMarketplace *ModelMarketplaceHandler
	Admin            *AdminHandlers
	Gateway          *GatewayHandler
	OpenAIGateway    *OpenAIGatewayHandler
	QoderGateway     *QoderGatewayHandler
	Setting          *SettingHandler
	Totp             *TotpHandler
	Passkey          *PasskeyHandler
	Payment          *PaymentHandler
	PaymentWebhook   *PaymentWebhookHandler
	BatchImage       *BatchImageHandler
	Creative         *CreativeHandler
	Team             *TeamHandler
}

// BuildInfo contains build-time information
type BuildInfo struct {
	Version   string
	BuildType string // "source" for manual builds, "release" for CI builds
}
