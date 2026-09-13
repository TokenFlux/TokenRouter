// Package admin provides HTTP handlers for administrative operations.
package admin

import (
	"context"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/response"
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

// OAuthHandler handles OAuth-related operations for accounts
type OAuthHandler struct {
	oauthService *service.OAuthService
}

// NewOAuthHandler creates a new OAuth handler
func NewOAuthHandler(oauthService *service.OAuthService) *OAuthHandler {
	return &OAuthHandler{
		oauthService: oauthService,
	}
}

// AccountHandler handles admin account management
type AccountHandler struct {
	managedRefresh          *accountcore.ManagedRefreshService
	importProbes            *accountcore.GrokImportProbeScheduler
	adminService            service.AdminService
	settingService          *service.SettingService
	oauthService            *service.OAuthService
	openaiOAuthService      *service.OpenAIOAuthService
	geminiOAuthService      *service.GeminiOAuthService
	antigravityOAuthService *service.AntigravityOAuthService
	grokOAuthService        service.GrokOAuthTokenService
	rateLimitService        *service.RateLimitService
	accountUsageService     *service.AccountUsageService
	upstreamUsageService    *service.UpstreamUsageService
	accountTestService      *service.AccountTestService
	concurrencyService      *service.ConcurrencyService
	crsSyncService          *service.CRSSyncService
	sessionLimitCache       service.SessionLimitCache
	rpmCache                service.RPMCache
	tokenCacheInvalidator   service.TokenCacheInvalidator
	grokImportProber        grokImportProber
	ollamaCloudUsage        *service.OllamaCloudUsageService
	advancedSchedulerScores *service.AdvancedSchedulerScoreDiagnosticService
}

// SetOllamaCloudUsageService 注入 Ollama Cloud 用量服务。
func (h *AccountHandler) SetOllamaCloudUsageService(usage *service.OllamaCloudUsageService) {
	h.ollamaCloudUsage = usage
}

// SetAdvancedSchedulerScoreDiagnosticService 注入账号高级调度评分诊断服务。
func (h *AccountHandler) SetAdvancedSchedulerScoreDiagnosticService(diagnostics *service.AdvancedSchedulerScoreDiagnosticService) {
	h.advancedSchedulerScores = diagnostics
}

// SetUpstreamUsageService 注入 API Key 上游用量查询服务。
// 使用 setter 保持现有单元测试构造函数的参数兼容性。
func (h *AccountHandler) SetUpstreamUsageService(usage *service.UpstreamUsageService) {
	if h != nil {
		h.upstreamUsageService = usage
	}
}

type qoderAdminTokenRefresher interface {
	Refresh(ctx context.Context, account *service.Account) (map[string]any, error)
}

var newQoderTokenRefresherForAdmin = func(adminService service.AdminService, qoderOAuthService *service.QoderOAuthService) qoderAdminTokenRefresher {
	return service.NewQoderTokenRefresherForAdmin(adminService, qoderOAuthService)
}

func NewAccountHandler(
	adminService service.AdminService,
	settingService *service.SettingService,
	oauthService *service.OAuthService,
	openaiOAuthService *service.OpenAIOAuthService,
	geminiOAuthService *service.GeminiOAuthService,
	antigravityOAuthService *service.AntigravityOAuthService,
	rateLimitService *service.RateLimitService,
	accountUsageService *service.AccountUsageService,
	accountTestService *service.AccountTestService,
	concurrencyService *service.ConcurrencyService,
	crsSyncService *service.CRSSyncService,
	sessionLimitCache service.SessionLimitCache,
	rpmCache service.RPMCache,
	tokenCacheInvalidator service.TokenCacheInvalidator,
	grokOAuthServices ...service.GrokOAuthTokenService,
) *AccountHandler {
	return newAccountHandlerWithImportProbes(newStandaloneImportProbes(), adminService, settingService, oauthService, openaiOAuthService, geminiOAuthService, antigravityOAuthService, rateLimitService, accountUsageService, accountTestService, concurrencyService, crsSyncService, sessionLimitCache, rpmCache, tokenCacheInvalidator, grokOAuthServices...)
}

// NewAccountHandler creates a new admin account handler
func newAccountHandlerWithImportProbes(
	importProbes *accountcore.GrokImportProbeScheduler,
	adminService service.AdminService,
	settingService *service.SettingService,
	oauthService *service.OAuthService,
	openaiOAuthService *service.OpenAIOAuthService,
	geminiOAuthService *service.GeminiOAuthService,
	antigravityOAuthService *service.AntigravityOAuthService,
	rateLimitService *service.RateLimitService,
	accountUsageService *service.AccountUsageService,
	accountTestService *service.AccountTestService,
	concurrencyService *service.ConcurrencyService,
	crsSyncService *service.CRSSyncService,
	sessionLimitCache service.SessionLimitCache,
	rpmCache service.RPMCache,
	tokenCacheInvalidator service.TokenCacheInvalidator,
	grokOAuthServices ...service.GrokOAuthTokenService,
) *AccountHandler {
	var grokOAuthService service.GrokOAuthTokenService
	if len(grokOAuthServices) > 0 {
		grokOAuthService = grokOAuthServices[0]
	}
	return &AccountHandler{importProbes: importProbes,
		adminService:            adminService,
		settingService:          settingService,
		oauthService:            oauthService,
		openaiOAuthService:      openaiOAuthService,
		geminiOAuthService:      geminiOAuthService,
		antigravityOAuthService: antigravityOAuthService,
		grokOAuthService:        grokOAuthService,
		rateLimitService:        rateLimitService,
		accountUsageService:     accountUsageService,
		accountTestService:      accountTestService,
		concurrencyService:      concurrencyService,
		crsSyncService:          crsSyncService,
		sessionLimitCache:       sessionLimitCache,
		rpmCache:                rpmCache,
		tokenCacheInvalidator:   tokenCacheInvalidator,
	}
}

type CreateAccountRequest = accounthttp.CreateAccountRequest

type UpdateAccountRequest = accounthttp.UpdateAccountRequest

type BulkUpdateAccountsRequest = accounthttp.BulkUpdateAccountsRequest

type BulkUpdateAccountFilters = accounthttp.BulkUpdateAccountFilters

type CheckMixedChannelRequest = accounthttp.CheckMixedChannelRequest

type AccountWithConcurrency = accounthttp.AccountWithConcurrency

type AccountSchedulerScore = accounthttp.AccountSchedulerScore

type AccountSchedulerGroupScore = accounthttp.AccountSchedulerGroupScore

// List 委托账号列表用例与新 HTTP 展示。
func (h *AccountHandler) List(c *gin.Context) { h.managementHTTP().List(c) }

func (h *AccountHandler) GetByID(c *gin.Context) { h.managementHTTP().GetByID(c) }

func (h *AccountHandler) CheckMixedChannel(c *gin.Context) { h.managementHTTP().CheckMixedChannel(c) }

func (h *AccountHandler) Create(c *gin.Context) { h.managementHTTP().Create(c) }

func (h *AccountHandler) Duplicate(c *gin.Context) { h.managementHTTP().Duplicate(c) }

func (h *AccountHandler) Update(c *gin.Context) { h.managementHTTP().Update(c) }

func (h *AccountHandler) Delete(c *gin.Context) { h.managementHTTP().Delete(c) }

type TestAccountRequest = accounthttp.TestAccountRequest

type SyncFromCRSRequest = accounthttp.SyncFromCRSRequest

type PreviewFromCRSRequest = accounthttp.PreviewFromCRSRequest

func (h *AccountHandler) Test(c *gin.Context) {
	var recover func(context.Context, int64) error
	if h.rateLimitService != nil {
		recover = func(ctx context.Context, id int64) error {
			_, err := h.rateLimitService.RecoverAccountAfterSuccessfulTest(ctx, id)
			return err
		}
	}
	accounthttp.NewTestHandler(h.accountTestService.Tester(), recover).Test(c)
}

// RecoverState 委托新的账号 HTTP Adapter。
func (h *AccountHandler) RecoverState(c *gin.Context) { h.managementHTTP().RecoverState(c) }

func (h *AccountHandler) SyncFromCRS(c *gin.Context) {
	accounthttp.NewCRSHandler(h.crsSyncService.Core()).SyncFromCRS(c)
}

func (h *AccountHandler) PreviewFromCRS(c *gin.Context) {
	accounthttp.NewCRSHandler(h.crsSyncService.Core()).PreviewFromCRS(c)
}

// refreshSingleAccount 仅投影旧 HTTP 输入并委托同一个管理刷新用例。
func (h *AccountHandler) refreshSingleAccount(ctx context.Context, value *service.Account) (*service.Account, string, error) {
	core := h.managedRefresh
	if core == nil {
		core = h.legacyManagedRefresh(value)
	}
	out, warning, err := core.Refresh(ctx, service.AccountRecordView(value))
	return service.AccountFromRecord(out), warning, err
}

func (h *AccountHandler) Refresh(c *gin.Context) { h.managementHTTP().Refresh(c) }

type ApplyOAuthCredentialsRequest = accounthttp.ApplyOAuthCredentialsRequest

func (h *AccountHandler) ApplyOAuthCredentials(c *gin.Context) {
	h.managementHTTP().ApplyOAuthCredentials(c)
}

// GetStats 委托原详细用量投影的新 HTTP 入口。
func (h *AccountHandler) GetStats(c *gin.Context) { h.managementHTTP().GetStats(c) }

func (h *AccountHandler) ClearError(c *gin.Context) { h.managementHTTP().ClearError(c) }

func (h *AccountHandler) RevertProxyFallback(c *gin.Context) {
	h.managementHTTP().RevertProxyFallback(c)
}

func (h *AccountHandler) BatchDelete(c *gin.Context) { h.managementHTTP().BatchDelete(c) }

func (h *AccountHandler) BatchClearError(c *gin.Context) { h.managementHTTP().BatchClearError(c) }

func (h *AccountHandler) BatchRefresh(c *gin.Context) { h.managementHTTP().BatchRefresh(c) }

func (h *AccountHandler) BatchCreate(c *gin.Context) { h.managementHTTP().BatchCreate(c) }

type BatchUpdateCredentialsRequest = accounthttp.BatchUpdateCredentialsRequest

func (h *AccountHandler) BatchUpdateCredentials(c *gin.Context) {
	h.managementHTTP().BatchUpdateCredentials(c)
}

func (h *AccountHandler) BulkUpdate(c *gin.Context) { h.managementHTTP().BulkUpdate(c) }

// ========== OAuth Handlers ==========

// GenerateAuthURLRequest represents the request for generating auth URL
type GenerateAuthURLRequest struct {
	ProxyID *int64 `json:"proxy_id"`
}

// GenerateAuthURL generates OAuth authorization URL with full scope
// POST /api/v1/admin/accounts/generate-auth-url
func (h *OAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req GenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body
		req = GenerateAuthURLRequest{}
	}

	result, err := h.oauthService.GenerateAuthURL(c.Request.Context(), req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, result)
}

// GenerateSetupTokenURL generates OAuth authorization URL for setup token (inference only)
// POST /api/v1/admin/accounts/generate-setup-token-url
func (h *OAuthHandler) GenerateSetupTokenURL(c *gin.Context) {
	var req GenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body
		req = GenerateAuthURLRequest{}
	}

	result, err := h.oauthService.GenerateSetupTokenURL(c.Request.Context(), req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, result)
}

// ExchangeCodeRequest represents the request for exchanging auth code
type ExchangeCodeRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	Code      string `json:"code" binding:"required"`
	ProxyID   *int64 `json:"proxy_id"`
}

// ExchangeCode exchanges authorization code for tokens
// POST /api/v1/admin/accounts/exchange-code
func (h *OAuthHandler) ExchangeCode(c *gin.Context) {
	var req ExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.oauthService.ExchangeCode(c.Request.Context(), &service.ExchangeCodeInput{
		SessionID: req.SessionID,
		Code:      req.Code,
		ProxyID:   req.ProxyID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, tokenInfo)
}

// ExchangeSetupTokenCode exchanges authorization code for setup token
// POST /api/v1/admin/accounts/exchange-setup-token-code
func (h *OAuthHandler) ExchangeSetupTokenCode(c *gin.Context) {
	var req ExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.oauthService.ExchangeCode(c.Request.Context(), &service.ExchangeCodeInput{
		SessionID: req.SessionID,
		Code:      req.Code,
		ProxyID:   req.ProxyID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, tokenInfo)
}

// CookieAuthRequest represents the request for cookie-based authentication
type CookieAuthRequest struct {
	SessionKey string `json:"code" binding:"required"` // Using 'code' field as sessionKey (frontend sends it this way)
	ProxyID    *int64 `json:"proxy_id"`
}

// CookieAuth performs OAuth using sessionKey (cookie-based auto-auth)
// POST /api/v1/admin/accounts/cookie-auth
func (h *OAuthHandler) CookieAuth(c *gin.Context) {
	var req CookieAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.oauthService.CookieAuth(c.Request.Context(), &service.CookieAuthInput{
		SessionKey: req.SessionKey,
		ProxyID:    req.ProxyID,
		Scope:      "full",
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, tokenInfo)
}

// SetupTokenCookieAuth performs OAuth using sessionKey for setup token (inference only)
// POST /api/v1/admin/accounts/setup-token-cookie-auth
func (h *OAuthHandler) SetupTokenCookieAuth(c *gin.Context) {
	var req CookieAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.oauthService.CookieAuth(c.Request.Context(), &service.CookieAuthInput{
		SessionKey: req.SessionKey,
		ProxyID:    req.ProxyID,
		Scope:      "inference",
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, tokenInfo)
}

func (h *AccountHandler) GetUsage(c *gin.Context) {
	accounthttp.NewOAuthUsageHandler(h.accountUsageService.Core(), h.accountUsageService.LocalStatistics()).GetUsage(c)
}

func (h *AccountHandler) QueryUpstreamUsage(c *gin.Context) {
	var queries accounthttp.UpstreamUsageQueries
	if h.upstreamUsageService != nil {
		queries = h.upstreamUsageService.Core()
	}
	accounthttp.NewUpstreamUsageHandler(queries).QueryUpstreamUsage(c)
}

type UpstreamUsageBatchRequest = accounthttp.UpstreamUsageBatchRequest

func (h *AccountHandler) QueryBatchUpstreamUsage(c *gin.Context) {
	var queries accounthttp.UpstreamUsageQueries
	if h.upstreamUsageService != nil {
		queries = h.upstreamUsageService.Core()
	}
	accounthttp.NewUpstreamUsageHandler(queries).QueryBatchUpstreamUsage(c)
}

// ClearRateLimit 委托新的账号 HTTP Adapter。
func (h *AccountHandler) ClearRateLimit(c *gin.Context) { h.managementHTTP().ClearRateLimit(c) }

func (h *AccountHandler) ResetQuota(c *gin.Context) { h.managementHTTP().ResetQuota(c) }

// GetTempUnschedulable 委托新的账号 HTTP Adapter。
func (h *AccountHandler) GetTempUnschedulable(c *gin.Context) {
	h.managementHTTP().GetTempUnschedulable(c)
}

// ClearTempUnschedulable 委托新的账号 HTTP Adapter。
func (h *AccountHandler) ClearTempUnschedulable(c *gin.Context) {
	h.managementHTTP().ClearTempUnschedulable(c)
}

func (h *AccountHandler) GetTodayStats(c *gin.Context) {
	accounthttp.NewOAuthUsageHandler(h.accountUsageService.Core(), h.accountUsageService.LocalStatistics()).GetTodayStats(c)
}

type BatchTodayStatsRequest = accounthttp.BatchTodayStatsRequest

type BatchUsageRequest = accounthttp.BatchUsageRequest

func (h *AccountHandler) GetBatchTodayStats(c *gin.Context) {
	accounthttp.NewOAuthUsageHandler(h.accountUsageService.Core(), h.accountUsageService.LocalStatistics()).GetBatchTodayStats(c)
}

func (h *AccountHandler) GetBatchUsage(c *gin.Context) {
	accounthttp.NewOAuthUsageHandler(h.accountUsageService.Core(), h.accountUsageService.LocalStatistics()).GetBatchUsage(c)
}

type SetSchedulableRequest = accounthttp.SetSchedulableRequest

func (h *AccountHandler) SetSchedulable(c *gin.Context) { h.managementHTTP().SetSchedulable(c) }

// GetAvailableModels 只保留原独立调用者转接。
func (h *AccountHandler) GetAvailableModels(c *gin.Context) { h.managementHTTP().GetAvailableModels(c) }

// SyncUpstreamModels 只保留旧入口转接。
func (h *AccountHandler) SyncUpstreamModels(c *gin.Context) { h.managementHTTP().SyncUpstreamModels(c) }

// SyncUpstreamModelsPreview 只保留旧入口转接。
func (h *AccountHandler) SyncUpstreamModelsPreview(c *gin.Context) {
	h.managementHTTP().SyncUpstreamModelsPreview(c)
}

// SetPrivacy 保留旧 HTTP 转接。
func (h *AccountHandler) SetPrivacy(c *gin.Context) { h.managementHTTP().SetPrivacy(c) }

// RefreshTier 委托账号 tier 管理用例。
func (h *AccountHandler) RefreshTier(c *gin.Context) { h.managementHTTP().RefreshTier(c) }

type BatchRefreshTierRequest = accounthttp.BatchRefreshTierRequest

// BatchRefreshTier 委托账号 tier 管理用例。
func (h *AccountHandler) BatchRefreshTier(c *gin.Context) { h.managementHTTP().BatchRefreshTier(c) }

// GetAntigravityDefaultModelMapping 委托默认表的只读展示。
func (h *AccountHandler) GetAntigravityDefaultModelMapping(c *gin.Context) {
	h.managementHTTP().GetAntigravityDefaultModelMapping(c)
}
