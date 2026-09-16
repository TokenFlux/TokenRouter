package httpapi

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

type GrokGenerateAuthURLRequest struct {
	ProxyID     *int64 `json:"proxy_id"`
	RedirectURI string `json:"redirect_uri"`
}

func (h *GrokOAuthHandler) GetCapabilities(c *gin.Context) {
	response.Success(c, h.grokOAuthService.GetCapabilities())
}

func (h *GrokOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req GrokGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = GrokGenerateAuthURLRequest{}
	}
	result, err := h.grokOAuthService.GenerateAuthURL(c.Request.Context(), req.ProxyID, req.RedirectURI)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

type GrokExchangeCodeRequest struct {
	SessionID   string `json:"session_id" binding:"required"`
	Code        string `json:"code" binding:"required"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri"`
	ProxyID     *int64 `json:"proxy_id"`
}

func (h *GrokOAuthHandler) ExchangeCode(c *gin.Context) {
	var req GrokExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	tokenInfo, err := h.grokOAuthService.ExchangeCode(c.Request.Context(), &accountcore.GrokExchangeCodeInput{

		SessionID: req.SessionID,

		Code: req.Code,

		State: req.State,

		RedirectURI: req.RedirectURI,

		ProxyID: req.ProxyID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, tokenInfo)
}

type GrokRefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
	RT           string `json:"rt"`
	ClientID     string `json:"client_id"`
	ProxyID      *int64 `json:"proxy_id"`
}

type GrokSSOTokenRequest struct {
	SSOToken string `json:"sso_token"`
	ProxyID  *int64 `json:"proxy_id"`
}

type GrokPasswordAuthorizeRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	ProxyID  *int64 `json:"proxy_id"`
}

func (h *GrokOAuthHandler) RefreshToken(c *gin.Context) {
	var req GrokRefreshTokenRequest
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
		value, found, err := h.options.ProxyURL(c.Request.Context(), *req.ProxyID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if !found {
			response.BadRequest(c, "GROK_OAUTH_PROXY_NOT_FOUND: proxy not found")
			return
		}
		proxyURL = value
	}
	tokenInfo, err := h.grokOAuthService.RefreshToken(c.Request.Context(), refreshToken, proxyURL, req.ClientID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, tokenInfo)
}

// ValidateSSOToken 将 Web SSO Cookie 转换为 Build OAuth 令牌。
// 响应只包含 OAuth 令牌信息，绝不回显 sso_token。
func (h *GrokOAuthHandler) ValidateSSOToken(c *gin.Context) {
	var req GrokSSOTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	tokenInfo, err := h.grokOAuthService.ValidateSSOToken(c.Request.Context(), req.SSOToken, req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, tokenInfo)
}

// AuthorizePassword 通过 SSO 转换，用邮箱和密码换取 Build OAuth 令牌。
// 响应绝不包含密码或原始 sso_token。
func (h *GrokOAuthHandler) AuthorizePassword(c *gin.Context) {
	var req GrokPasswordAuthorizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	tokenInfo, err := h.grokOAuthService.AuthorizePassword(c.Request.Context(), req.Email, req.Password, req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, tokenInfo)
}

func (h *GrokOAuthHandler) RefreshAccountToken(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	value, err := h.imports.RefreshAccount(c.Request.Context(), id)
	if err != nil {
		var input *accountcore.GrokImportInputError
		if errors.As(err, &input) {
			response.BadRequest(c, input.Message)
		} else {
			response.ErrorFrom(c, err)
		}
		return
	}
	response.Success(c, dto.AccountFromRecord(value))
}

type GrokOAuthReconcileRequest struct {
	DryRun               *bool `json:"dry_run"`
	Apply                bool  `json:"apply"`
	AfterID              int64 `json:"after_id"`
	Limit                int   `json:"limit"`
	RefreshWindowSeconds int64 `json:"refresh_window_seconds"`
}

func (h *GrokOAuthHandler) ReconcileOAuthAccounts(c *gin.Context) {
	var req GrokOAuthReconcileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	if req.Apply == dryRun {
		response.ErrorFrom(c, accountcore.ErrGrokOAuthReconcileMode)
		return
	}
	if req.RefreshWindowSeconds < 0 || req.RefreshWindowSeconds > int64((24*time.Hour)/time.Second) {
		response.ErrorFrom(c, accountcore.ErrGrokOAuthReconcileWindow)
		return
	}
	if h.reconciler == nil {
		response.InternalError(c, "Grok OAuth reconciliation service is unavailable")
		return
	}
	result, err := h.reconciler.ReconcileGrokOAuth(c.Request.Context(), accountcore.GrokOAuthReconcileInput{

		DryRun: dryRun,

		Apply: req.Apply,

		AfterID: req.AfterID,

		Limit: req.Limit,

		RefreshWindow: time.Duration(req.RefreshWindowSeconds) * time.Second,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *GrokOAuthHandler) CreateAccountFromOAuth(c *gin.Context) {
	var req struct {
		SessionID   string  `json:"session_id" binding:"required"`
		Code        string  `json:"code" binding:"required"`
		State       string  `json:"state"`
		RedirectURI string  `json:"redirect_uri"`
		ProxyID     *int64  `json:"proxy_id"`
		Name        string  `json:"name"`
		Concurrency int     `json:"concurrency"`
		Priority    int     `json:"priority"`
		GroupIDs    []int64 `json:"group_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	value, err := h.imports.CreateFromOAuth(c.Request.Context(), accountcore.GrokOAuthAccountCreateInput{
		SessionID:   req.SessionID,
		Code:        req.Code,
		State:       req.State,
		RedirectURI: req.RedirectURI,
		ProxyID:     req.ProxyID,
		Name:        req.Name,
		Concurrency: req.Concurrency,
		Priority:    req.Priority,
		GroupIDs:    req.GroupIDs,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AccountFromRecord(value))
}

type GrokSSOToOAuthRequest = accountcore.GrokSSOToOAuthRequest

type GrokSSOToOAuthItemResult struct {
	Index   int          `json:"index"`
	Name    string       `json:"name,omitempty"`
	Email   string       `json:"email,omitempty"`
	Account *dto.Account `json:"account,omitempty"`
	Error   string       `json:"error,omitempty"`
}

type GrokSSOToOAuthResponse struct {
	Created []GrokSSOToOAuthItemResult `json:"created"`
	Failed  []GrokSSOToOAuthItemResult `json:"failed"`
}

func (h *GrokOAuthHandler) CreateAccountsFromSSO(c *gin.Context) {
	var req GrokSSOToOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := h.imports.CreateFromSSO(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, accountcore.ErrGrokSSOInput) {
			response.BadRequest(c, err.Error())
		} else {
			response.ErrorFrom(c, err)
		}
		return
	}
	out := GrokSSOToOAuthResponse{Created: make([]GrokSSOToOAuthItemResult, 0, len(result.Created)), Failed: make([]GrokSSOToOAuthItemResult, 0, len(result.Failed))}
	convert := func(item accountcore.GrokSSOToOAuthItemResult) GrokSSOToOAuthItemResult {
		return GrokSSOToOAuthItemResult{Index: item.Index, Name: item.Name, Email: item.Email, Error: item.Error, Account: dto.AccountFromRecord(item.Account)}
	}
	for _, v := range result.Created {
		out.Created = append(out.Created, convert(v))
	}
	for _, v := range result.Failed {
		out.Failed = append(out.Failed, convert(v))
	}
	response.Success(c, out)
}

func (h *GrokOAuthHandler) QueryQuota(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if h.quotaService == nil {
		response.BadRequest(c, "grok quota service is not enabled")
		return
	}
	result, err := h.quotaService.QueryQuota(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *GrokOAuthHandler) ResetQuota(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if h.quotaService == nil {
		response.BadRequest(c, "grok quota service is not enabled")
		return
	}
	// ResetQuota 恒返回 GROK_QUOTA_RESET_UNSUPPORTED（xAI 无 OAuth 配额重置接口），err != nil 恒真为预期。
	//nolint:staticcheck // SA4023
	result, err := h.quotaService.ResetQuota(c.Request.Context(), accountID)
	if err != nil { //nolint:staticcheck // SA4023
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *GrokOAuthHandler) RuntimeSanity(c *gin.Context) {
	response.Success(c, h.options.RuntimeSanity())
}

// 账号授权 HTTP 只使用同模块用例、代理只读投影及运行时诊断端口。
type GrokOAuthReconciler interface {
	ReconcileGrokOAuth(context.Context, accountcore.GrokOAuthReconcileInput) (*accountcore.GrokOAuthReconcileResult, error)
}
type GrokOAuthHTTPOptions struct {
	ProxyURL      func(context.Context, int64) (string, bool, error)
	RuntimeSanity func() any
	Reconciler    GrokOAuthReconciler
}
type GrokOAuthHandler struct {
	grokOAuthService *accountcore.GrokAuthorization
	imports          *accountcore.GrokAccountImport
	quotaService     *accountcore.GrokQuotaService
	reconciler       GrokOAuthReconciler
	options          GrokOAuthHTTPOptions
}

func NewGrokOAuthHandler(auth *accountcore.GrokAuthorization, imports *accountcore.GrokAccountImport, quota *accountcore.GrokQuotaService, options GrokOAuthHTTPOptions) *GrokOAuthHandler {
	return &GrokOAuthHandler{grokOAuthService: auth, imports: imports, quotaService: quota, reconciler: options.Reconciler, options: options}
}
