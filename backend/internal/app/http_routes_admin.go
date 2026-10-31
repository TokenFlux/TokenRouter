package app

import (
	"github.com/gin-gonic/gin"

	routeapikey "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	routeaudit "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	routebackup "github.com/TokenFlux/TokenRouter/internal/backup/httpapi"
	routebilling "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	routecreative "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	routeegress "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	routegateway "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	routeidentity "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	routemoderation "github.com/TokenFlux/TokenRouter/internal/moderation/httpapi"
	routenotification "github.com/TokenFlux/TokenRouter/internal/notification/httpapi"
	routeops "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	routeplugin "github.com/TokenFlux/TokenRouter/internal/plugin/httpapi"
	routepromotion "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	routeprovider "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	routerouting "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	routescheduler "github.com/TokenFlux/TokenRouter/internal/scheduler/httpapi"
	routesearch "github.com/TokenFlux/TokenRouter/internal/search/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
	serverhttp "github.com/TokenFlux/TokenRouter/internal/server/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	routesettings "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	routesite "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/team/httpapi"
	routeusageadmin "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"
)

// http_routes_admin.go mounts administrator endpoints and assigns their shared
// and operation-specific guards. Administrator routes inherit administrator
// authentication, panel limits, audit, and provider terminology handling before
// module registration. Sensitive plugin, provider, proxy, backup, data-management, and
// team operations additionally receive the shared step-up guard at the
// registrar or individual route that requires it.

// provideAdminRouteMount returns administrator registration under /api/v1/admin
// using constructed handlers. The callback installs administrator, panel,
// audit, and terminology middleware first, then passes step-up into sensitive
// module registrars and team transfer or dissolution routes.
func provideAdminRouteMount(eAdminTLSFingerprintProfile *routeegress.TLSFingerprintProfileHandler,
	eAdminSchedulerDiagnostics *routescheduler.DiagnosticsHandler,
	eAdminTLSFingerprintRouter *routeegress.TLSFingerprintRouterHandler,
	eAdminProviderCodexImport *routeprovider.CodexImportHandler,
	eAdminProviderOAuthUsage *routeprovider.OAuthUsageHandler,
	eAdminProviderManagement *routeprovider.ManagementHandler,
	eAdminContentModeration *routemoderation.ContentModerationHandler,
	eAdminAntigravityOAuth *routeprovider.AntigravityOAuthHandler,
	eAdminErrorPassthrough *routegateway.ErrorPassthroughHandler,
	eAdminCodexInviteReset *routeprovider.CodexInviteResetHandler,
	eAdminDataManagement *routebackup.DataManagementHandler,
	eAdminProviderArchive *routeprovider.ArchiveHandler,
	eAdminUserAttribute *routeidentity.UserAttributeHandler,
	eAdminUpstreamUsage *routeprovider.UpstreamUsageHandler,
	eAdminScheduledTest *routeprovider.ScheduledTestHandler,
	eAdminProviderOllama *routeprovider.OllamaUsageHandler,
	eAdminSubscription *routebilling.AdminSubscriptionHandler,
	eAdminAnnouncement *routesite.AdminAnnouncementHandler,
	eAdminProviderTests *routeprovider.TestHandler,
	eAdminOpenAIOAuth *routeprovider.OpenAIOAuthHandler,
	eAdminGeminiOAuth *routeprovider.GeminiOAuthHandler,
	eAdminProviderCRS *routeprovider.CRSHandler,
	eAdminQoderOAuth *routeprovider.QoderOAuthHandler,
	eAdminDashboard *routeusageadmin.DashboardHandler,
	eAdminAffiliate *routepromotion.AffiliateHandler,
	eAdminGrokOAuth *routeprovider.GrokOAuthHandler,
	eAdminAuditLog *routeaudit.AuditLogHandler,
	eAdminPricing *routerouting.PricingHandler,
	eModelAttributes *routerouting.ModelAttributeHandler,
	eAdminSetting *routesettings.Handler,
	ePreAggregation *routesettings.PreAggregationHandler,
	eCreativeSettings *routecreative.SettingsHandler,
	eGatewaySettings *routegateway.RuntimeSettingsHandler,
	eIdentitySettings *routeidentity.AdminKeySettingsHandler,
	eProviderSettings *routeprovider.RuntimeSettingsHandler,
	ePanelSettings *serverhttp.PanelSettingsHandler,
	eAdminSystem *routeops.SystemHandler,
	eAdminRedeem *routebilling.AdminRedeemHandler,
	eNotification *routenotification.Handler,
	eAdminBackup *routebackup.BackupHandler,
	eAdminAPIKey *routeapikey.AdminAPIKeyHandler[routingdto.Group],
	eAdminProxy *routeegress.ProxyHandler,
	eAdminGroup *routerouting.GroupHandler,
	eAdminOAuth *routeprovider.ClaudeOAuthHandler,
	eAdminUsage *routeusageadmin.UsageHandler,
	eAdminPromo *routepromotion.PromoHandler,
	eAdminTeam *httpapi.AdminHandler,
	eAdminUser *routeidentity.AdminUserHandler[dto.APIKey[routingdto.Group]],
	eAdminOps *routeops.OpsHandler,
	ePlugins *routeplugin.Handler,
	eSearch *routesearch.Handler,
) adminRouteMount {
	return func(v1 *gin.RouterGroup, security httpRouteSecurity, protocolCatalog gin.HandlerFunc) {
		admin := v1.Group("/admin")
		admin.Use(security.Admin)
		// Panel limits are per user; administrators are exempt by default unless settings disable the exemption.
		admin.Use(security.Panel.Global())
		// Audit runs after authentication and records administrative mutations and sensitive reads.
		admin.Use(security.Audit)
		admin.Use(middleware.ProviderTerminology())
		{
			// Provider and group forms share this read-only capability catalogue.
			adminapi.Register(admin, ManagementOperations["protocol_capabilities.list"], protocolCatalog)
			// Plugin routes share administrator guards and enforce step-up for executable and capability-policy changes before their handlers run.
			routeplugin.RegisterAdminRoutes(admin, ePlugins, security.StepUp)

			// Dashboard routes.
			{
				routeusageadmin.RegisterDashboardRoutes(admin, eAdminDashboard)
			}

			// User management.
			{
				routeidentity.RegisterUserManagementRoutes(admin, eAdminUser, eAdminUserAttribute)
			}

			// Group management.
			{
				routerouting.RegisterGroupRoutes(admin, eAdminGroup)
			}

			// Provider management.
			{
				routeprovider.RegisterProviderRoutes(admin, routeprovider.ProviderRouteEndpoints{
					ProviderArchive:     eAdminProviderArchive,
					ProviderCRS:         eAdminProviderCRS,
					ProviderCodexImport: eAdminProviderCodexImport,
					ProviderManagement:  eAdminProviderManagement,
					ProviderOAuthUsage:  eAdminProviderOAuthUsage,
					ProviderOllama:      eAdminProviderOllama,
					ProviderTests:       eAdminProviderTests,
					CodexInviteReset:    eAdminCodexInviteReset,
					OAuth:               eAdminOAuth,
					OpenAIOAuth:         eAdminOpenAIOAuth,
					UpstreamUsage:       eAdminUpstreamUsage,
				}, security.StepUp, func(providers *gin.RouterGroup) {
					routescheduler.RegisterProviderDiagnostics(providers, eAdminSchedulerDiagnostics)
				})
			}

			// Announcements.
			{
				routesite.RegisterAnnouncementRoutes(admin, eAdminAnnouncement)
			}

			// OpenAI OAuth management.
			{
				routeprovider.RegisterOpenAIOAuthRoutes(admin, eAdminOpenAIOAuth)
			}

			// Gemini OAuth management.
			{
				routeprovider.RegisterGeminiOAuthRoutes(admin, eAdminGeminiOAuth)
			}

			// Antigravity OAuth management.
			{
				routeprovider.RegisterAntigravityOAuthRoutes(admin, eAdminAntigravityOAuth)
			}

			// Qoder OAuth management.
			{
				routeprovider.RegisterQoderOAuthRoutes(admin, eAdminQoderOAuth)
			}

			// Grok OAuth management.
			{
				routeprovider.RegisterGrokOAuthRoutes(admin, eAdminGrokOAuth)
			}

			// Proxy management.
			{
				routeegress.RegisterProxyRoutes(admin, eAdminProxy, security.StepUp)
			}

			// Redeem codes.
			{
				routebilling.RegisterRedeemCodeRoutes(admin, eAdminRedeem)
			}

			// Promo codes.
			{
				routepromotion.RegisterPromoCodeRoutes(admin, eAdminPromo)
			}

			// System settings.
			{
				adminSettings := admin.Group("/settings")
				routesettings.RegisterSettingsSettingsRoutes(adminSettings, eAdminSetting, ePreAggregation)
				routecreative.RegisterCreativeSettingsRoutes(adminSettings, eCreativeSettings)
				routeidentity.RegisterIdentitySettingsRoutes(adminSettings, eIdentitySettings)
				routeprovider.RegisterProviderSettingsRoutes(adminSettings, eProviderSettings)
				serverhttp.RegisterPanelSettingsRoutes(adminSettings, ePanelSettings)
				routegateway.RegisterGatewaySettingsRoutes(adminSettings, eGatewaySettings)
				routenotification.RegisterSettingsRoutes(adminSettings, eNotification)
				routesearch.RegisterSettingsRoutes(adminSettings, eSearch)
			}

			// Data management.
			{
				routebackup.RegisterDataManagementRoutes(admin, eAdminDataManagement, security.StepUp)
			}

			// Database backup and restore.
			{
				routebackup.RegisterBackupRoutes(admin, eAdminBackup, security.StepUp)
			}

			// Operations monitoring.
			{
				routeops.RegisterOpsRoutes(admin, eAdminOps)
			}

			// System administration.
			{
				routeops.RegisterSystemRoutes(admin, eAdminSystem)
			}

			// Subscription management.
			{
				routebilling.RegisterSubscriptionRoutes(admin, eAdminSubscription)
			}

			// Usage records.
			{
				routeusageadmin.RegisterUsageRoutes(admin, eAdminUsage)
			}

			// User attributes.
			{
				routeidentity.RegisterUserAttributeRoutes(admin, eAdminUserAttribute)
			}

			// Error passthrough rules.
			{
				routegateway.RegisterErrorPassthroughRoutes(admin, eAdminErrorPassthrough)
			}

			// TLS fingerprint profiles.
			{
				routeegress.RegisterTLSFingerprintProfileRoutes(admin, eAdminTLSFingerprintProfile)
			}

			// TLS fingerprint routers.
			{
				routeegress.RegisterTLSFingerprintRouterRoutes(admin, eAdminTLSFingerprintRouter)
			}

			// API Key management.
			{
				routeapikey.RegisterAdminAPIKeyRoutes(admin, eAdminAPIKey)
			}

			// Scheduled provider tests.
			{
				routeprovider.RegisterScheduledTestRoutes(admin, eAdminScheduledTest)
			}

			// Pricing and model attributes.
			{
				routerouting.RegisterPricingRoutes(admin, eAdminPricing)
				routerouting.RegisterModelAttributeRoutes(admin, eModelAttributes)
			}

			// Content moderation.
			{
				routemoderation.RegisterContentModerationRoutes(admin, eAdminContentModeration)
			}

			// Referral rebates.
			{
				routepromotion.RegisterAffiliateRoutes(admin, eAdminAffiliate)
			}

			// Audit logs.
			{
				routeaudit.RegisterAuditLogRoutes(admin, eAdminAuditLog)
			}

			// Team administration.
			teams := admin.Group("/teams")
			{
				adminapi.Register(teams, httpapi.ManagementOperations["teams.list"], eAdminTeam.List)
				adminapi.Register(teams, httpapi.ManagementOperations["teams.create"], eAdminTeam.Create)
				adminapi.Register(teams, httpapi.ManagementOperations["teams.get"], eAdminTeam.Get)
				adminapi.Register(teams, httpapi.ManagementOperations["teams.members.list"], eAdminTeam.ListMembers)
				adminapi.Register(teams, httpapi.ManagementOperations["teams.usage.list"], eAdminTeam.GetUsage)
				adminapi.Register(teams, httpapi.ManagementOperations["teams.update"], eAdminTeam.Update)
				adminapi.Register(teams, httpapi.ManagementOperations["teams.force_transfer.create"], security.StepUp, eAdminTeam.ForceTransfer)
				adminapi.Register(teams, httpapi.ManagementOperations["teams.delete"], security.StepUp, eAdminTeam.Dissolve)
			}
		}
	}
}
