package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// routes_admin.go registers provider management, authorization, usage, and
// connection-test routes for administrators.
// Provider administration mounts management, credential exchange, usage and
// connection-test endpoints under the caller's administrator group. The group
// supplies authentication, rate limiting and auditing before registration, and
// the raw-credential export route adds its own supplied step-up middleware.

// ProviderRouteEndpoints holds constructed handler dependencies used when
// administrator routes are registered.
type ProviderRouteEndpoints struct {
	ProviderArchive     *ArchiveHandler          // raw credential backup transfers
	ProviderCRS         *CRSHandler              // external CRS source synchronization
	ProviderCodexImport *CodexImportHandler      // Codex credential session imports
	ProviderManagement  *ManagementHandler       // provider configuration and maintenance
	ProviderOAuthUsage  *OAuthUsageHandler       // OAuth usage and local statistics
	ProviderOllama      *OllamaUsageHandler      // managed Ollama usage and sessions
	ProviderTests       *TestHandler             // streaming provider connection tests
	CodexInviteReset    *CodexInviteResetHandler // Codex invitations and reset credits
	OAuth               *ClaudeOAuthHandler      // Claude scoped authorization sessions
	OpenAIOAuth         *OpenAIOAuthHandler      // OpenAI authorization and quota actions
	UpstreamUsage       *UpstreamUsageHandler    // manual API-key upstream usage
}

// RegisterAntigravityOAuthRoutes mounts Google session generation, code
// exchange and refresh-token validation under antigravity. The caller supplies
// an administrator group already protected by authentication, rate limiting and
// auditing before registration.
func RegisterAntigravityOAuthRoutes(admin *gin.RouterGroup, endpoint *AntigravityOAuthHandler) {
	antigravity := admin.Group("/antigravity")
	{
		adminapi.Register(antigravity, ManagementOperations["antigravity.oauth.auth_url.create"], endpoint.GenerateAuthURL)
		adminapi.Register(antigravity, ManagementOperations["antigravity.oauth.exchange_code.create"], endpoint.ExchangeCode)
		adminapi.Register(antigravity, ManagementOperations["antigravity.oauth.refresh_token.create"], endpoint.RefreshToken)
	}
}

// RegisterGeminiOAuthRoutes mounts Gemini session generation, exchange and
// capability display under gemini. The caller supplies the protected
// administrator group and a constructed authorization handler before route
// registration.
func RegisterGeminiOAuthRoutes(admin *gin.RouterGroup, endpoint *GeminiOAuthHandler) {
	gemini := admin.Group("/gemini")
	{
		adminapi.Register(gemini, ManagementOperations["gemini.oauth.auth_url.create"], endpoint.GenerateAuthURL)
		adminapi.Register(gemini, ManagementOperations["gemini.oauth.exchange_code.create"], endpoint.ExchangeCode)
		adminapi.Register(gemini, ManagementOperations["gemini.oauth.capabilities.list"], endpoint.GetCapabilities)
	}
}

// RegisterGrokOAuthRoutes mounts Grok authorization, SSO imports,
// reconciliation, quota and runtime diagnostics under grok. The caller supplies
// a constructed handler and an authenticated, rate-limited, audited
// administrator group.
func RegisterGrokOAuthRoutes(admin *gin.RouterGroup, endpoint *GrokOAuthHandler) {
	grok := admin.Group("/grok")
	{
		adminapi.Register(grok, ManagementOperations["grok.oauth.capabilities.list"], endpoint.GetCapabilities)
		adminapi.Register(grok, ManagementOperations["grok.oauth.auth_url.create"], endpoint.GenerateAuthURL)
		adminapi.Register(grok, ManagementOperations["grok.oauth.exchange_code.create"], endpoint.ExchangeCode)
		adminapi.Register(grok, ManagementOperations["grok.oauth.refresh_token.create"], endpoint.RefreshToken)
		adminapi.Register(grok, ManagementOperations["grok.oauth.sso_token.create"], endpoint.ValidateSSOToken)
		adminapi.Register(grok, ManagementOperations["grok.oauth.password.create"], endpoint.AuthorizePassword)
		adminapi.Register(grok, ManagementOperations["grok.oauth.create_from_oauth.create"], endpoint.CreateProviderFromOAuth)
		adminapi.Register(grok, ManagementOperations["grok.sso_to_oauth.create"], endpoint.CreateProvidersFromSSO)
		adminapi.Register(grok, ManagementOperations["grok.oauth.reconcile.create"], endpoint.ReconcileOAuthProviders)
		adminapi.Register(grok, ManagementOperations["grok.providers.refresh.create"], endpoint.RefreshProviderToken)
		adminapi.Register(grok, ManagementOperations["grok.providers.quota.list"], endpoint.QueryQuota)
		adminapi.Register(grok, ManagementOperations["grok.providers.reset_quota.create"], endpoint.ResetQuota)
		adminapi.Register(grok, ManagementOperations["grok.runtime_sanity.list"], endpoint.RuntimeSanity)
	}
}

// RegisterOpenAIOAuthRoutes mounts OpenAI authorization, provider import and
// quota maintenance under openai. Authentication, rate limiting and auditing
// belong to the supplied administrator group, whose lifetime covers the
// constructed handler dependencies.
func RegisterOpenAIOAuthRoutes(admin *gin.RouterGroup, endpoint *OpenAIOAuthHandler) {
	openai := admin.Group("/openai")
	{
		adminapi.Register(openai, ManagementOperations["openai.generate_auth_url.create"], endpoint.GenerateAuthURL)
		adminapi.Register(openai, ManagementOperations["openai.exchange_code.create"], endpoint.ExchangeCode)
		adminapi.Register(openai, ManagementOperations["openai.refresh_token.create"], endpoint.RefreshToken)
		adminapi.Register(openai, ManagementOperations["openai.providers.refresh.create"], endpoint.RefreshProviderToken)
		adminapi.Register(openai, ManagementOperations["openai.create_from_oauth.create"], endpoint.CreateProviderFromOAuth)
		adminapi.Register(openai, ManagementOperations["openai.create_from_codex_pat.create"], endpoint.CreateProviderFromCodexPAT)
		adminapi.Register(openai, ManagementOperations["openai.providers.quota.list"], endpoint.QueryQuota)
		adminapi.Register(openai, ManagementOperations["openai.providers.quota.refresh.create"], endpoint.RefreshQuota)
		adminapi.Register(openai, ManagementOperations["openai.providers.reset_quota.create"], endpoint.ResetQuota)
	}
}

// RegisterQoderOAuthRoutes mounts Qoder device session generation, exchange and
// polling under qoder. The caller supplies a constructed device-authorization
// handler and an administrator group with authentication, rate limiting and
// auditing.
func RegisterQoderOAuthRoutes(admin *gin.RouterGroup, endpoint *QoderOAuthHandler) {
	qoder := admin.Group("/qoder")
	{
		adminapi.Register(qoder, ManagementOperations["qoder.oauth.auth_url.create"], endpoint.GenerateAuthURL)
		adminapi.Register(qoder, ManagementOperations["qoder.oauth.exchange_code.create"], endpoint.ExchangeCode)
		adminapi.Register(qoder, ManagementOperations["qoder.oauth.poll.create"], endpoint.Poll)
	}
}

// RegisterScheduledTestRoutes mounts plan mutations and result history plus the
// provider-nested plan list. The caller supplies a constructed schedule handler
// and an authenticated, rate-limited, audited administrator group.
func RegisterScheduledTestRoutes(admin *gin.RouterGroup, endpoint *ScheduledTestHandler) {
	plans := admin.Group("/scheduled-test-plans")
	{
		adminapi.Register(plans, ManagementOperations["scheduled_test_plans.create"], endpoint.Create)
		adminapi.Register(plans, ManagementOperations["scheduled_test_plans.update"], endpoint.Update)
		adminapi.Register(plans, ManagementOperations["scheduled_test_plans.delete"], endpoint.Delete)
		adminapi.Register(plans, ManagementOperations["scheduled_test_plans.results.list"], endpoint.ListResults)
	}
	// Nested under providers
	adminapi.Register(admin, ManagementOperations["providers.scheduled_test_plans.list"], endpoint.ListByProvider)
}

// RegisterProviderRoutes mounts provider administration and invokes the
// supplied scheduler diagnostics registration. All endpoint pointers and
// registration callbacks are required by the routes that use them; the caller
// supplies the protected administrator group and raw-export step-up middleware.
func RegisterProviderRoutes(admin *gin.RouterGroup, endpoints ProviderRouteEndpoints, stepUp gin.HandlerFunc, registerDiagnostics func(*gin.RouterGroup)) {
	providers := admin.Group("/providers")
	{
		adminapi.Register(providers, ManagementOperations["providers.list"], endpoints.ProviderManagement.List)
		adminapi.Register(providers, ManagementOperations["providers.ollama_cloud_usage.settings.list"], endpoints.ProviderOllama.GetOllamaCloudUsageSettings)
		adminapi.Register(providers, ManagementOperations["providers.ollama_cloud_usage.settings.update"], endpoints.ProviderOllama.UpdateOllamaCloudUsageSettings)
		adminapi.Register(providers, ManagementOperations["providers.get"], endpoints.ProviderManagement.GetByID)
		adminapi.Register(providers, ManagementOperations["providers.create"], endpoints.ProviderManagement.Create)
		adminapi.Register(providers, ManagementOperations["providers.duplicate.create"], endpoints.ProviderManagement.Duplicate)
		adminapi.Register(providers, ManagementOperations["providers.import.codex_session.create"], endpoints.ProviderCodexImport.ImportCodexSession)
		adminapi.Register(providers, ManagementOperations["providers.sync.crs.create"], endpoints.ProviderCRS.SyncFromCRS)
		adminapi.Register(providers, ManagementOperations["providers.sync.crs.preview.create"], endpoints.ProviderCRS.PreviewFromCRS)
		adminapi.Register(providers, ManagementOperations["providers.update"], endpoints.ProviderManagement.Update)
		registerDiagnostics(providers)
		adminapi.Register(providers, ManagementOperations["providers.ollama_cloud_usage.list"], endpoints.ProviderOllama.GetOllamaCloudUsage)
		adminapi.Register(providers, ManagementOperations["providers.ollama_cloud_usage.session.update"], endpoints.ProviderOllama.SaveOllamaCloudUsageSession)
		adminapi.Register(providers, ManagementOperations["providers.ollama_cloud_usage.session.delete"], endpoints.ProviderOllama.DeleteOllamaCloudUsageSession)
		adminapi.Register(providers, ManagementOperations["providers.ollama_cloud_usage.auto_refresh.update"], endpoints.ProviderOllama.SetOllamaCloudUsageAutoRefresh)
		adminapi.Register(providers, ManagementOperations["providers.ollama_cloud_usage.refresh.create"], endpoints.ProviderOllama.RefreshOllamaCloudUsage)
		adminapi.Register(providers, ManagementOperations["providers.delete"], endpoints.ProviderManagement.Delete)
		adminapi.Register(providers, ManagementOperations["providers.test.create"], endpoints.ProviderTests.Test)
		adminapi.Register(providers, ManagementOperations["providers.recover_state.create"], endpoints.ProviderManagement.RecoverState)
		adminapi.Register(providers, ManagementOperations["providers.refresh.create"], endpoints.ProviderManagement.Refresh)
		adminapi.Register(providers, ManagementOperations["providers.apply_oauth_credentials.create"], endpoints.ProviderManagement.ApplyOAuthCredentials)
		adminapi.Register(providers, ManagementOperations["providers.set_privacy.create"], endpoints.ProviderManagement.SetPrivacy)
		adminapi.Register(providers, ManagementOperations["providers.stats.list"], endpoints.ProviderManagement.GetStats)
		adminapi.Register(providers, ManagementOperations["providers.clear_error.create"], endpoints.ProviderManagement.ClearError)
		adminapi.Register(providers, ManagementOperations["providers.revert_proxy_fallback.create"], endpoints.ProviderManagement.RevertProxyFallback)
		adminapi.Register(providers, ManagementOperations["providers.usage.list"], endpoints.ProviderOAuthUsage.GetUsage)
		adminapi.Register(providers, ManagementOperations["providers.usage.passive.list"], endpoints.ProviderOAuthUsage.GetPassiveUsage)
		adminapi.Register(providers, ManagementOperations["providers.today_stats.list"], endpoints.ProviderOAuthUsage.GetTodayStats)
		adminapi.Register(providers, ManagementOperations["providers.usage.batch.create"], endpoints.ProviderOAuthUsage.GetBatchUsage)
		adminapi.Register(providers, ManagementOperations["providers.today_stats.batch.create"], endpoints.ProviderOAuthUsage.GetBatchTodayStats)
		adminapi.Register(providers, ManagementOperations["providers.clear_rate_limit.create"], endpoints.ProviderManagement.ClearRateLimit)
		adminapi.Register(providers, ManagementOperations["providers.reset_quota.create"], endpoints.ProviderManagement.ResetQuota)
		adminapi.Register(providers, ManagementOperations["providers.upstream_usage.query.create"], endpoints.UpstreamUsage.QueryUpstreamUsage)
		adminapi.Register(providers, ManagementOperations["providers.upstream_balance_alert.list"], endpoints.UpstreamUsage.BalanceAlertStatus)
		adminapi.Register(providers, ManagementOperations["providers.upstream_usage.query.batch.create"], endpoints.UpstreamUsage.QueryBatchUpstreamUsage)
		adminapi.Register(providers, ManagementOperations["providers.temp_unschedulable.list"], endpoints.ProviderManagement.GetTempUnschedulable)
		adminapi.Register(providers, ManagementOperations["providers.temp_unschedulable.delete"], endpoints.ProviderManagement.ClearTempUnschedulable)
		adminapi.Register(providers, ManagementOperations["providers.schedulable.create"], endpoints.ProviderManagement.SetSchedulable)
		adminapi.Register(providers, ManagementOperations["providers.models.sync_upstream_preview.create"], endpoints.ProviderManagement.SyncUpstreamModelsPreview)
		adminapi.Register(providers, ManagementOperations["providers.models.list"], endpoints.ProviderManagement.GetAvailableModels)
		adminapi.Register(providers, ManagementOperations["providers.models.sync_upstream.create"], endpoints.ProviderManagement.SyncUpstreamModels)
		adminapi.Register(providers, ManagementOperations["providers.batch.create"], endpoints.ProviderManagement.BatchCreate)
		// Raw provider and proxy secrets are part of the backup format. Add the
		// supplied step-up middleware here before the archive exporter can return those values.
		adminapi.Register(providers, ManagementOperations["providers.data.list"], stepUp, endpoints.ProviderArchive.ExportData)
		adminapi.Register(providers, ManagementOperations["providers.data.create"], endpoints.ProviderArchive.ImportData)
		adminapi.Register(providers, ManagementOperations["providers.batch_update_credentials.create"], endpoints.ProviderManagement.BatchUpdateCredentials)
		adminapi.Register(providers, ManagementOperations["providers.bulk_update.create"], endpoints.ProviderManagement.BulkUpdate)
		adminapi.Register(providers, ManagementOperations["providers.batch_delete.create"], endpoints.ProviderManagement.BatchDelete)
		adminapi.Register(providers, ManagementOperations["providers.batch_clear_error.create"], endpoints.ProviderManagement.BatchClearError)
		adminapi.Register(providers, ManagementOperations["providers.batch_refresh.create"], endpoints.ProviderManagement.BatchRefresh)
		adminapi.Register(providers, ManagementOperations["providers.codex.invite_reset.status.list"], endpoints.CodexInviteReset.GetStatus)
		adminapi.Register(providers, ManagementOperations["providers.codex.invite_reset.invite.create"], endpoints.CodexInviteReset.SendInvite)
		adminapi.Register(providers, ManagementOperations["providers.codex.invite_reset.consume.create"], endpoints.CodexInviteReset.Consume)

		// Antigravity default model mapping.
		adminapi.Register(providers, ManagementOperations["providers.antigravity.default_model_mapping.list"], endpoints.ProviderManagement.GetAntigravityDefaultModelMapping)

		// Spark shadow providers.
		adminapi.Register(providers, ManagementOperations["providers.shadow.create"], endpoints.OpenAIOAuth.CreateShadow)

		// Claude OAuth routes
		adminapi.Register(providers, ManagementOperations["providers.generate_auth_url.create"], endpoints.OAuth.GenerateAuthURL)
		adminapi.Register(providers, ManagementOperations["providers.generate_setup_token_url.create"], endpoints.OAuth.GenerateSetupTokenURL)
		adminapi.Register(providers, ManagementOperations["providers.exchange_code.create"], endpoints.OAuth.ExchangeCode)
		adminapi.Register(providers, ManagementOperations["providers.exchange_setup_token_code.create"], endpoints.OAuth.ExchangeSetupTokenCode)
		adminapi.Register(providers, ManagementOperations["providers.cookie_auth.create"], endpoints.OAuth.CookieAuth)
		adminapi.Register(providers, ManagementOperations["providers.setup_token_cookie_auth.create"], endpoints.OAuth.SetupTokenCookieAuth)
	}
}
