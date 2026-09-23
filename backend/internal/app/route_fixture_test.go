package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	apikeyhttpapi "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	audithttpapi "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	backuphttpapi "github.com/TokenFlux/TokenRouter/internal/backup/httpapi"
	batchimagehttpapi "github.com/TokenFlux/TokenRouter/internal/batchimage/httpapi"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	creativehttpapi "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	egresshttpapi "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	gatewayhttpapi "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	identityhttpapi "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	moderationhttpapi "github.com/TokenFlux/TokenRouter/internal/moderation/httpapi"
	notificationhttpapi "github.com/TokenFlux/TokenRouter/internal/notification/httpapi"
	opshttpapi "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	paymenthttpapi "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	promotionhttpapi "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	routinghttpapi "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	schedulerhttpapi "github.com/TokenFlux/TokenRouter/internal/scheduler/httpapi"
	searchhttpapi "github.com/TokenFlux/TokenRouter/internal/search/httpapi"
	sitehttpapi "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	teamhttpapi "github.com/TokenFlux/TokenRouter/internal/team/httpapi"
	usagehttpapi "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	usageadmin "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"
)

// 路由迁移夹具保留旧测试构造形状；生产不存在这些聚合。
type routeTestAdminHandlers struct {
	APIKey                *apikeyhttpapi.AdminAPIKeyHandler[routingdto.Group]
	AccountArchive        *httpapi.ArchiveHandler
	AccountCRS            *httpapi.CRSHandler
	AccountCodexImport    *httpapi.CodexImportHandler
	AccountManagement     *httpapi.ManagementHandler
	AccountOAuthUsage     *httpapi.OAuthUsageHandler
	AccountOllama         *httpapi.OllamaUsageHandler
	AccountTests          *httpapi.TestHandler
	Affiliate             *promotionhttpapi.AffiliateHandler
	Announcement          *sitehttpapi.AdminAnnouncementHandler
	AntigravityOAuth      *httpapi.AntigravityOAuthHandler
	AuditLog              *audithttpapi.AuditLogHandler
	Backup                *backuphttpapi.BackupHandler
	Channel               *routinghttpapi.ChannelHandler
	CodexInviteReset      *httpapi.CodexInviteResetHandler
	ContentModeration     *moderationhttpapi.ContentModerationHandler
	Dashboard             *usageadmin.DashboardHandler
	DataManagement        *backuphttpapi.DataManagementHandler
	ErrorPassthrough      *gatewayhttpapi.ErrorPassthroughHandler
	GeminiOAuth           *httpapi.GeminiOAuthHandler
	GrokOAuth             *httpapi.GrokOAuthHandler
	Group                 *routinghttpapi.GroupHandler
	OAuth                 *httpapi.ClaudeOAuthHandler
	OpenAIOAuth           *httpapi.OpenAIOAuthHandler
	Ops                   *opshttpapi.OpsHandler
	Payment               *paymenthttpapi.AdminHandler
	Promo                 *promotionhttpapi.PromoHandler
	Proxy                 *egresshttpapi.ProxyHandler
	QoderOAuth            *httpapi.QoderOAuthHandler
	Redeem                *billinghttpapi.AdminRedeemHandler
	ScheduledTest         *httpapi.ScheduledTestHandler
	SchedulerDiagnostics  *schedulerhttpapi.DiagnosticsHandler
	Subscription          *billinghttpapi.AdminSubscriptionHandler
	System                *opshttpapi.SystemHandler
	TLSFingerprintProfile *egresshttpapi.TLSFingerprintProfileHandler
	TLSFingerprintRouter  *egresshttpapi.TLSFingerprintRouterHandler
	Team                  *teamhttpapi.AdminHandler
	UpstreamUsage         *httpapi.UpstreamUsageHandler
	Usage                 *usageadmin.UsageHandler
	User                  *identityhttpapi.AdminUserHandler[dto.APIKey[routingdto.Group]]
	UserAttribute         *identityhttpapi.UserAttributeHandler
}
type routeTestHandlers struct {
	APIKey       *apikeyhttpapi.APIKeyHandler[routingdto.Group]
	Admin        *routeTestAdminHandlers
	Announcement *sitehttpapi.AnnouncementHandler
	Auth         interface {
		identityhttpapi.AuthEndpoints
		paymenthttpapi.WeChatAuthEndpoints
	}
	AuxiliaryHTTP       *gatewayhttpapi.AuxiliaryHandler
	BatchImage          *batchimagehttpapi.BatchImageHandler
	CompatibleTextHTTP  *gatewayhttpapi.CompatibleTextHandler
	CountTokensHTTP     *gatewayhttpapi.CountTokensHandler
	Creative            *creativehttpapi.CreativeHandler
	TextEnabled         bool
	GeminiNativeHTTP    *gatewayhttpapi.GeminiNativeHandler
	LiveHTTP            *gatewayhttpapi.LiveHandler
	MediaHTTP           *gatewayhttpapi.MediaHandler
	MessagesHTTP        *gatewayhttpapi.MessagesHandler
	ModelMarketplace    *routinghttpapi.MarketplaceHandler
	ModelsHTTP          *gatewayhttpapi.ModelsHandler
	Notification        *notificationhttpapi.Handler
	OpenAIEnabled       bool
	OpenAITextHTTP      *gatewayhttpapi.OpenAITextHandler
	OpenAITokensHTTP    *gatewayhttpapi.OpenAITokensHandler
	Passkey             *identityhttpapi.PasskeyHandler
	Payment             *paymenthttpapi.PaymentHandler
	PaymentWebhook      *paymenthttpapi.PaymentWebhookHandler
	Plans               *billinghttpapi.PlanHandler
	PlatformQuota       *billinghttpapi.QuotaHandler
	PublicSettings      *sitehttpapi.PublicHandler
	PublicUsage         *usagehttpapi.PublicUsageHandler
	QoderChat           *gatewayhttpapi.QoderChatHandler
	QoderCompatibleHTTP *gatewayhttpapi.QoderCompatibleHandler
	Redeem              *billinghttpapi.RedeemHandler
	ResponsesWSHTTP     *gatewayhttpapi.ResponsesWSHandler
	Search              *searchhttpapi.Handler
	SearchHTTP          *gatewayhttpapi.SearchHandler
	Subscription        *billinghttpapi.SubscriptionHandler
	Team                *teamhttpapi.UserHandler
	Totp                *identityhttpapi.TotpHandler
	Usage               *usagehttpapi.UsageHandler
	PromotionUser       *promotionhttpapi.UserHandler
	User                *identityhttpapi.UserHandler
}
