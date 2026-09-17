package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterAntigravityOAuthRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterAntigravityOAuthRoutes(admin *gin.RouterGroup, endpoint *AntigravityOAuthHandler) {
	antigravity := admin.Group("/antigravity")
	{
		antigravity.POST("/oauth/auth-url", endpoint.GenerateAuthURL)
		antigravity.POST("/oauth/exchange-code", endpoint.ExchangeCode)
		antigravity.POST("/oauth/refresh-token", endpoint.RefreshToken)
	}
}

// RegisterGeminiOAuthRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterGeminiOAuthRoutes(admin *gin.RouterGroup, endpoint *GeminiOAuthHandler) {
	gemini := admin.Group("/gemini")
	{
		gemini.POST("/oauth/auth-url", endpoint.GenerateAuthURL)
		gemini.POST("/oauth/exchange-code", endpoint.ExchangeCode)
		gemini.GET("/oauth/capabilities", endpoint.GetCapabilities)
	}
}

// RegisterGrokOAuthRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterGrokOAuthRoutes(admin *gin.RouterGroup, endpoint *GrokOAuthHandler) {
	grok := admin.Group("/grok")
	{
		grok.GET("/oauth/capabilities", endpoint.GetCapabilities)
		grok.POST("/oauth/auth-url", endpoint.GenerateAuthURL)
		grok.POST("/oauth/exchange-code", endpoint.ExchangeCode)
		grok.POST("/oauth/refresh-token", endpoint.RefreshToken)
		grok.POST("/oauth/sso-token", endpoint.ValidateSSOToken)
		grok.POST("/oauth/password", endpoint.AuthorizePassword)
		grok.POST("/oauth/create-from-oauth", endpoint.CreateAccountFromOAuth)
		grok.POST("/sso-to-oauth", endpoint.CreateAccountsFromSSO)
		grok.POST("/oauth/reconcile", endpoint.ReconcileOAuthAccounts)
		grok.POST("/accounts/:id/refresh", endpoint.RefreshAccountToken)
		grok.GET("/accounts/:id/quota", endpoint.QueryQuota)
		grok.POST("/accounts/:id/reset-quota", endpoint.ResetQuota)
		grok.GET("/runtime-sanity", endpoint.RuntimeSanity)
	}
}

// RegisterOpenAIOAuthRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterOpenAIOAuthRoutes(admin *gin.RouterGroup, endpoint *OpenAIOAuthHandler) {
	openai := admin.Group("/openai")
	{
		openai.POST("/generate-auth-url", endpoint.GenerateAuthURL)
		openai.POST("/exchange-code", endpoint.ExchangeCode)
		openai.POST("/refresh-token", endpoint.RefreshToken)
		openai.POST("/accounts/:id/refresh", endpoint.RefreshAccountToken)
		openai.POST("/create-from-oauth", endpoint.CreateAccountFromOAuth)
		openai.POST("/create-from-codex-pat", endpoint.CreateAccountFromCodexPAT)
		openai.GET("/accounts/:id/quota", endpoint.QueryQuota)
		openai.POST("/accounts/:id/quota/refresh", endpoint.RefreshQuota)
		openai.POST("/accounts/:id/reset-quota", endpoint.ResetQuota)
	}
}

// RegisterQoderOAuthRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterQoderOAuthRoutes(admin *gin.RouterGroup, endpoint *QoderOAuthHandler) {
	qoder := admin.Group("/qoder")
	{
		qoder.POST("/oauth/auth-url", endpoint.GenerateAuthURL)
		qoder.POST("/oauth/exchange-code", endpoint.ExchangeCode)
		qoder.POST("/oauth/poll", endpoint.Poll)
	}
}

// RegisterScheduledTestRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterScheduledTestRoutes(admin *gin.RouterGroup, endpoint *ScheduledTestHandler) {
	plans := admin.Group("/scheduled-test-plans")
	{
		plans.POST("", endpoint.Create)
		plans.PUT("/:id", endpoint.Update)
		plans.DELETE("/:id", endpoint.Delete)
		plans.GET("/:id/results", endpoint.ListResults)
	}
	// Nested under accounts
	admin.GET("/accounts/:id/scheduled-test-plans", endpoint.ListByAccount)
}

// AccountRouteEndpoints 只汇总账号模块已经构造的 HTTP 实例。
type AccountRouteEndpoints struct {
	AccountArchive     *ArchiveHandler
	AccountCRS         *CRSHandler
	AccountCodexImport *CodexImportHandler
	AccountManagement  *ManagementHandler
	AccountOAuthUsage  *OAuthUsageHandler
	AccountOllama      *OllamaUsageHandler
	AccountTests       *TestHandler
	CodexInviteReset   *CodexInviteResetHandler
	OAuth              *ClaudeOAuthHandler
	OpenAIOAuth        *OpenAIOAuthHandler
	UpstreamUsage      *UpstreamUsageHandler
}

// RegisterAccountRoutes 注册账号管理路径；诊断注册由 scheduler 注入，保持原位置。
func RegisterAccountRoutes(admin *gin.RouterGroup, endpoints AccountRouteEndpoints, stepUp gin.HandlerFunc, registerDiagnostics func(*gin.RouterGroup)) {
	accounts := admin.Group("/accounts")
	{
		accounts.GET("", endpoints.AccountManagement.List)
		accounts.GET("/ollama-cloud-usage/settings", endpoints.AccountOllama.GetOllamaCloudUsageSettings)
		accounts.PUT("/ollama-cloud-usage/settings", endpoints.AccountOllama.UpdateOllamaCloudUsageSettings)
		accounts.GET("/:id", endpoints.AccountManagement.GetByID)
		accounts.POST("", endpoints.AccountManagement.Create)
		accounts.POST("/:id/duplicate", endpoints.AccountManagement.Duplicate)
		accounts.POST("/check-mixed-channel", endpoints.AccountManagement.CheckMixedChannel)
		accounts.POST("/import/codex-session", endpoints.AccountCodexImport.ImportCodexSession)
		accounts.POST("/sync/crs", endpoints.AccountCRS.SyncFromCRS)
		accounts.POST("/sync/crs/preview", endpoints.AccountCRS.PreviewFromCRS)
		accounts.PUT("/:id", endpoints.AccountManagement.Update)
		registerDiagnostics(accounts)
		accounts.GET("/:id/ollama-cloud-usage", endpoints.AccountOllama.GetOllamaCloudUsage)
		accounts.PUT("/:id/ollama-cloud-usage/session", endpoints.AccountOllama.SaveOllamaCloudUsageSession)
		accounts.DELETE("/:id/ollama-cloud-usage/session", endpoints.AccountOllama.DeleteOllamaCloudUsageSession)
		accounts.PUT("/:id/ollama-cloud-usage/auto-refresh", endpoints.AccountOllama.SetOllamaCloudUsageAutoRefresh)
		accounts.POST("/:id/ollama-cloud-usage/refresh", endpoints.AccountOllama.RefreshOllamaCloudUsage)
		accounts.DELETE("/:id", endpoints.AccountManagement.Delete)
		accounts.POST("/:id/test", endpoints.AccountTests.Test)
		accounts.POST("/:id/recover-state", endpoints.AccountManagement.RecoverState)
		accounts.POST("/:id/refresh", endpoints.AccountManagement.Refresh)
		accounts.POST("/:id/apply-oauth-credentials", endpoints.AccountManagement.ApplyOAuthCredentials)
		accounts.POST("/:id/set-privacy", endpoints.AccountManagement.SetPrivacy)
		accounts.POST("/:id/refresh-tier", endpoints.AccountManagement.RefreshTier)
		accounts.GET("/:id/stats", endpoints.AccountManagement.GetStats)
		accounts.POST("/:id/clear-error", endpoints.AccountManagement.ClearError)
		accounts.POST("/:id/revert-proxy-fallback", endpoints.AccountManagement.RevertProxyFallback)
		accounts.GET("/:id/usage", endpoints.AccountOAuthUsage.GetUsage)
		accounts.GET("/:id/today-stats", endpoints.AccountOAuthUsage.GetTodayStats)
		accounts.POST("/usage/batch", endpoints.AccountOAuthUsage.GetBatchUsage)
		accounts.POST("/today-stats/batch", endpoints.AccountOAuthUsage.GetBatchTodayStats)
		accounts.POST("/:id/clear-rate-limit", endpoints.AccountManagement.ClearRateLimit)
		accounts.POST("/:id/reset-quota", endpoints.AccountManagement.ResetQuota)
		accounts.POST("/:id/upstream-usage/query", endpoints.UpstreamUsage.QueryUpstreamUsage)
		accounts.POST("/upstream-usage/query/batch", endpoints.UpstreamUsage.QueryBatchUpstreamUsage)
		accounts.GET("/:id/temp-unschedulable", endpoints.AccountManagement.GetTempUnschedulable)
		accounts.DELETE("/:id/temp-unschedulable", endpoints.AccountManagement.ClearTempUnschedulable)
		accounts.POST("/:id/schedulable", endpoints.AccountManagement.SetSchedulable)
		accounts.POST("/models/sync-upstream-preview", endpoints.AccountManagement.SyncUpstreamModelsPreview)
		accounts.GET("/:id/models", endpoints.AccountManagement.GetAvailableModels)
		accounts.POST("/:id/models/sync-upstream", endpoints.AccountManagement.SyncUpstreamModels)
		accounts.POST("/batch", endpoints.AccountManagement.BatchCreate)
		// 账号导出泄露上游凭证原文——要求 step-up 2FA
		accounts.GET("/data", stepUp, endpoints.AccountArchive.ExportData)
		accounts.POST("/data", endpoints.AccountArchive.ImportData)
		accounts.POST("/batch-update-credentials", endpoints.AccountManagement.BatchUpdateCredentials)
		accounts.POST("/batch-refresh-tier", endpoints.AccountManagement.BatchRefreshTier)
		accounts.POST("/bulk-update", endpoints.AccountManagement.BulkUpdate)
		accounts.POST("/batch-delete", endpoints.AccountManagement.BatchDelete)
		accounts.POST("/batch-clear-error", endpoints.AccountManagement.BatchClearError)
		accounts.POST("/batch-refresh", endpoints.AccountManagement.BatchRefresh)
		accounts.GET("/:id/codex/invite-reset/status", endpoints.CodexInviteReset.GetStatus)
		accounts.POST("/:id/codex/invite-reset/invite", endpoints.CodexInviteReset.SendInvite)
		accounts.POST("/:id/codex/invite-reset/consume", endpoints.CodexInviteReset.Consume)

		// Antigravity 默认模型映射
		accounts.GET("/antigravity/default-model-mapping", endpoints.AccountManagement.GetAntigravityDefaultModelMapping)

		// Spark 影子账号
		accounts.POST("/:id/shadow", endpoints.OpenAIOAuth.CreateShadow)

		// Claude OAuth routes
		accounts.POST("/generate-auth-url", endpoints.OAuth.GenerateAuthURL)
		accounts.POST("/generate-setup-token-url", endpoints.OAuth.GenerateSetupTokenURL)
		accounts.POST("/exchange-code", endpoints.OAuth.ExchangeCode)
		accounts.POST("/exchange-setup-token-code", endpoints.OAuth.ExchangeSetupTokenCode)
		accounts.POST("/cookie-auth", endpoints.OAuth.CookieAuth)
		accounts.POST("/setup-token-cookie-auth", endpoints.OAuth.SetupTokenCookieAuth)
	}
}
