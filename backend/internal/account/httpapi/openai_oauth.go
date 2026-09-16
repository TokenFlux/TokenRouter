package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/gin-gonic/gin"
)

// OpenAIOAuthHandler handles OpenAI OAuth-related operations
type OpenAIOAuthHandler struct {
	QuotaActions  *accountcore.OpenAIQuotaActions
	Import        *accountcore.OpenAIAccountImport
	Options       OpenAIHTTPOptions
	Authorization *accountcore.OpenAIAuthorization
	Admin         OpenAIAdminOperations
	Quota         OpenAIQuotaService
	Recovery      OpenAIAccountStateRecoverer
}

type OpenAIQuotaService interface {
	QueryUsage(ctx context.Context, accountID int64) (*wire.OpenAIQuotaUsage, error)
	CacheResetCreditsSnapshot(ctx context.Context, accountID int64, credits *wire.OpenAIRateLimitResetCredits) error
	CachePostResetSnapshot(ctx context.Context, accountID int64, usage *wire.OpenAIQuotaUsage) error
	ResetCredit(ctx context.Context, accountID int64) (*wire.OpenAIQuotaResetResult, error)
}

type OpenAIAccountStateRecoverer interface {
	RecoverAccountState(ctx context.Context, accountID int64, options accountcore.AccountRecoveryOptions) (*accountcore.SuccessfulTestRecovery, error)
}

const OpenAIQuotaResetWarningCacheRefreshFailed = accountcore.OpenAIQuotaResetWarningCacheRefreshFailed
const OpenAIQuotaResetWarningAccountRecoveryFailed = accountcore.OpenAIQuotaResetWarningAccountRecoveryFailed
const OpenAIQuotaResetWarningAccountRefreshFailed = accountcore.OpenAIQuotaResetWarningAccountRefreshFailed
const OpenAIQuotaResetPostProcessTimeout = accountcore.OpenAIQuotaResetPostProcessTimeout

type OpenAIQuotaResetResponse struct {
	wire.OpenAIQuotaResetResult
	Quota                 *wire.OpenAIQuotaUsage `json:"quota,omitempty"`
	Account               *dto.Account           `json:"account,omitempty"`
	CacheRefreshed        bool                   `json:"cache_refreshed"`
	AccountStateRecovered bool                   `json:"account_state_recovered"`
	WarningCode           string                 `json:"warning_code,omitempty"`
}

type OpenAIQuotaRefreshResponse struct {
	wire.OpenAIQuotaUsage
	CachePersisted bool `json:"cache_persisted"`
}

// OpenAIQuotaResetPostProcessContext 让已消费重置次数后的收尾工作不受客户端断开影响。
func OpenAIQuotaResetPostProcessContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, OpenAIQuotaResetPostProcessTimeout)
}

func oauthPlatformFromPath(c *gin.Context) string {
	return "openai"
}

// OpenAIGenerateAuthURLRequest represents the request for generating OpenAI auth URL
type OpenAIGenerateAuthURLRequest struct {
	ProxyID     *int64 `json:"proxy_id"`
	RedirectURI string `json:"redirect_uri"`
}

// GenerateAuthURL generates OpenAI OAuth authorization URL
// POST /api/v1/admin/openai/generate-auth-url
func (h *OpenAIOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req OpenAIGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body
		req = OpenAIGenerateAuthURLRequest{}
	}

	result, err := h.Authorization.GenerateAuthURL(
		c.Request.Context(),
		req.ProxyID,
		req.RedirectURI,
		oauthPlatformFromPath(c),
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, result)
}

// OpenAIExchangeCodeRequest represents the request for exchanging OpenAI auth code
type OpenAIExchangeCodeRequest struct {
	SessionID              string `json:"session_id" binding:"required"`
	Code                   string `json:"code" binding:"required"`
	State                  string `json:"state" binding:"required"`
	RedirectURI            string `json:"redirect_uri"`
	ProxyID                *int64 `json:"proxy_id"`
	TLSFingerprintRouterID *int64 `json:"tls_fingerprint_router_id"`
}

// ExchangeCode exchanges OpenAI authorization code for tokens
// POST /api/v1/admin/openai/exchange-code
func (h *OpenAIOAuthHandler) ExchangeCode(c *gin.Context) {
	var req OpenAIExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.Authorization.ExchangeCode(c.Request.Context(), &accountcore.OpenAIExchangeCodeInput{

		SessionID: req.SessionID,

		Code: req.Code,

		State: req.State,

		RedirectURI: req.RedirectURI,

		ProxyID: req.ProxyID,

		TLSFingerprintRouterID: req.TLSFingerprintRouterID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, tokenInfo)
}

// OpenAIRefreshTokenRequest represents the request for refreshing OpenAI token
type OpenAIRefreshTokenRequest struct {
	RefreshToken           string `json:"refresh_token"`
	RT                     string `json:"rt"`
	ClientID               string `json:"client_id"`
	ProxyID                *int64 `json:"proxy_id"`
	TLSFingerprintRouterID *int64 `json:"tls_fingerprint_router_id"`
}

type OpenAICodexPATCreateRequest struct {
	AccessToken             string         `json:"access_token" binding:"required"`
	Name                    string         `json:"name"`
	Notes                   *string        `json:"notes"`
	GroupIDs                []int64        `json:"group_ids"`
	ProxyID                 *int64         `json:"proxy_id"`
	Concurrency             *int           `json:"concurrency"`
	Priority                *int           `json:"priority"`
	RateMultiplier          *float64       `json:"rate_multiplier"`
	LoadFactor              *int           `json:"load_factor"`
	ExpiresAt               *int64         `json:"expires_at"`
	AutoPauseOnExpired      *bool          `json:"auto_pause_on_expired"`
	CredentialExtras        map[string]any `json:"credential_extras"`
	Extra                   map[string]any `json:"extra"`
	SkipDefaultGroupBind    *bool          `json:"skip_default_group_bind"`
	ConfirmMixedChannelRisk *bool          `json:"confirm_mixed_channel_risk"`
}

// RefreshToken refreshes an OpenAI OAuth token
// POST /api/v1/admin/openai/refresh-token
func (h *OpenAIOAuthHandler) RefreshToken(c *gin.Context) {
	var req OpenAIRefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	refreshToken := strings.TrimSpace(req.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(req.RT)
	}
	if refreshToken == "" {
		response.BadRequest(c, "refresh_token is required")
		return
	}

	var proxyURL string
	if req.ProxyID != nil {
		proxyURLValue, found, err := h.Options.ProxyURL(c.Request.Context(), *req.ProxyID)
		if err == nil && found {
			proxyURL = proxyURLValue
		}
	}

	// 未指定 client_id 时，根据请求路径平台自动设置默认值，避免 repository 层盲猜
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		platform := oauthPlatformFromPath(c)
		clientID, _ = h.Options.ClientID(platform)
	}

	tokenInfo, err := h.Authorization.RefreshTokenWithClientIDAndRouter(c.Request.Context(), refreshToken, proxyURL, clientID, req.TLSFingerprintRouterID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, tokenInfo)
}

// RefreshAccountToken refreshes token for a specific OpenAI account
// POST /api/v1/admin/openai/accounts/:id/refresh
func (h *OpenAIOAuthHandler) RefreshAccountToken(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	value, err := h.accountImport().RefreshAccount(c.Request.Context(), accountID, oauthPlatformFromPath(c))
	if err != nil {
		writeOpenAIAccountImportError(c, err)
		return
	}
	response.Success(c, dto.AccountFromRecord(value))
}

// CreateAccountFromOAuth creates a new OpenAI OAuth account from token info
// POST /api/v1/admin/openai/create-from-oauth
func (h *OpenAIOAuthHandler) CreateAccountFromOAuth(c *gin.Context) {
	var req struct {
		SessionID              string  `json:"session_id" binding:"required"`
		Code                   string  `json:"code" binding:"required"`
		State                  string  `json:"state" binding:"required"`
		RedirectURI            string  `json:"redirect_uri"`
		ProxyID                *int64  `json:"proxy_id"`
		TLSFingerprintRouterID *int64  `json:"tls_fingerprint_router_id"`
		Name                   string  `json:"name"`
		Concurrency            int     `json:"concurrency"`
		Priority               int     `json:"priority"`
		GroupIDs               []int64 `json:"group_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	value, err := h.accountImport().CreateOAuthAccount(c.Request.Context(), accountcore.OpenAIOAuthAccountCreateInput{

		SessionID: req.SessionID,

		Code: req.Code,

		State: req.State,

		RedirectURI: req.RedirectURI,

		ProxyID: req.ProxyID,

		TLSFingerprintRouterID: req.TLSFingerprintRouterID,

		Name: req.Name,

		Concurrency: req.Concurrency,

		Priority: req.Priority,

		GroupIDs: req.GroupIDs,
	}, oauthPlatformFromPath(c))
	if err != nil {
		writeOpenAIAccountImportError(c, err)
		return
	}
	response.Success(c, dto.AccountFromRecord(value))
}

// CreateAccountFromCodexPAT 使用 Codex at-* Personal Access Token 创建 OpenAI OAuth 账号。
// POST /api/v1/admin/openai/create-from-codex-pat
func (h *OpenAIOAuthHandler) CreateAccountFromCodexPAT(c *gin.Context) {
	var req OpenAICodexPATCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	value, err := h.accountImport().CreatePATAccount(c.Request.Context(), accountcore.OpenAICodexPATCreateInput(req))
	if err != nil {
		writeOpenAIAccountImportError(c, err)
		return
	}
	response.Success(c, dto.AccountFromRecord(value))
}

func BuildOpenAICodexPATAccountName(name string, tokenInfo *accountcore.OpenAITokenInfo) string {
	return accountcore.BuildOpenAICodexPATAccountName(name, tokenInfo)
}

// QueryQuota 查询 OpenAI OAuth 账号的上游限流窗口和可用重置次数。
// GET /api/v1/admin/openai/accounts/:id/quota
func (h *OpenAIOAuthHandler) QueryQuota(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if h.Quota == nil {
		response.BadRequest(c, "openai quota service is not enabled")
		return
	}
	usage, err := h.Quota.QueryUsage(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, usage)
}

// RefreshQuota 查询上游额度，并把带到期时间的重置次数快照持久化到账号 extra。
// POST /api/v1/admin/openai/accounts/:id/quota/refresh
func (h *OpenAIOAuthHandler) RefreshQuota(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if h.Quota == nil {
		response.BadRequest(c, "openai quota service is not enabled")
		return
	}

	value, err := h.quotaActions().Refresh(c.Request.Context(), accountID)
	if err != nil {
		var missing *accountcore.OpenAIQuotaOutcomeError
		if errors.As(err, &missing) {
			response.Error(c, http.StatusInternalServerError, missing.Message)
		} else {
			response.ErrorFrom(c, err)
		}
		return
	}
	output := OpenAIQuotaRefreshResponse{OpenAIQuotaUsage: value.OpenAIQuotaUsage, CachePersisted: value.CachePersisted}
	response.Success(c, output)
}

// CreateShadowRequest 是创建 Spark 影子账号的请求体。
type CreateShadowRequest struct {
	Name        string  `json:"name"`
	Priority    int     `json:"priority"`
	Concurrency int     `json:"concurrency"`
	GroupIDs    []int64 `json:"group_ids"`
}

// CreateShadow 为母 OpenAI OAuth 账号创建 spark 维度影子账号。
// POST /api/v1/admin/accounts/:id/shadow
func (h *OpenAIOAuthHandler) CreateShadow(c *gin.Context) {
	parentID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req CreateShadowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	shadow, err := h.Admin.CreateShadow(c.Request.Context(), parentID, accountcore.ShadowOptions{
		Name:        req.Name,
		Priority:    req.Priority,
		Concurrency: req.Concurrency,
		GroupIDs:    req.GroupIDs,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.AccountFromRecordShallow(shadow))
}

// ResetQuota 消耗一次 OpenAI OAuth 账号的上游限流重置次数。
// POST /api/v1/admin/openai/accounts/:id/reset-quota
func (h *OpenAIOAuthHandler) ResetQuota(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if h.Quota == nil {
		response.BadRequest(c, "openai quota service is not enabled")
		return
	}
	value, err := h.quotaActions().Reset(c.Request.Context(), accountID)
	if err != nil {
		var missing *accountcore.OpenAIQuotaOutcomeError
		if errors.As(err, &missing) {
			response.Error(c, http.StatusInternalServerError, missing.Message)
		} else {
			response.ErrorFrom(c, err)
		}
		return
	}
	output := OpenAIQuotaResetResponse{
		OpenAIQuotaResetResult: value.OpenAIQuotaResetResult,
		Quota:                  value.Quota,
		Account:                dto.AccountFromRecord(value.Account),
		CacheRefreshed:         value.CacheRefreshed,
		AccountStateRecovered:  value.AccountStateRecovered,
		WarningCode:            value.WarningCode,
	}
	response.Success(c, output)
}

type OpenAIAdminOperations interface {
	GetAccount(context.Context, int64) (*accountcore.Record, error)
	CreateAccount(context.Context, *accountcore.CreateAccountInput) (*accountcore.Record, error)
	UpdateAccount(context.Context, int64, *accountcore.UpdateAccountInput) (*accountcore.Record, error)
	CreateShadow(context.Context, int64, accountcore.ShadowOptions) (*accountcore.Record, error)
}
type OpenAIHTTPOptions struct {
	ProxyURL func(context.Context, int64) (string, bool, error)
	ClientID func(string) (string, bool)
}

func NewOpenAIOAuthHandler(auth *accountcore.OpenAIAuthorization, admin OpenAIAdminOperations, quota OpenAIQuotaService, recovery OpenAIAccountStateRecoverer, options OpenAIHTTPOptions) *OpenAIOAuthHandler {
	return &OpenAIOAuthHandler{
		Authorization: auth,
		Admin:         admin,
		Quota:         quota,
		Recovery:      recovery,
		Options:       options,
		Import:        accountcore.NewOpenAIAccountImport(auth, admin, options.ProxyURL),
		QuotaActions:  accountcore.NewOpenAIQuotaActions(quota, recovery, admin, slog.Warn),
	}
}

// 无缓存用例只复用 handler 已持有的唯一账号/授权依赖。
func (h *OpenAIOAuthHandler) accountImport() *accountcore.OpenAIAccountImport {
	if h.Import != nil {
		return h.Import
	}
	// 兼容原白盒测试直接构造的部分 handler；生产构造始终持有同一用例实例。
	return accountcore.NewOpenAIAccountImport(h.Authorization, h.Admin, h.Options.ProxyURL)
}
func writeOpenAIAccountImportError(c *gin.Context, err error) {
	var inputError *accountcore.OpenAIAccountInputError
	if errors.As(err, &inputError) {
		response.BadRequest(c, inputError.Message)
		return
	}
	response.ErrorFrom(c, err)
}

func (h *OpenAIOAuthHandler) quotaActions() *accountcore.OpenAIQuotaActions {
	if h.QuotaActions != nil {
		return h.QuotaActions
	}
	// 兼容原白盒测试的部分构造；生产始终复用构造时装配的用例。
	return accountcore.NewOpenAIQuotaActions(h.Quota, h.Recovery, h.Admin, slog.Warn)
}
