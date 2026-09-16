// 本文件保留 Claude 账号授权 HTTP 契约，平台交换与状态均由账号用例拥有。
package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

type ClaudeAuthorizationUseCase interface {
	GenerateAuthURL(context.Context, *int64) (*account.ClaudeGenerateAuthURLResult, error)
	GenerateSetupTokenURL(context.Context, *int64) (*account.ClaudeGenerateAuthURLResult, error)
	ExchangeCode(context.Context, *account.ClaudeExchangeCodeInput) (*account.ClaudeTokenInfo, error)
	CookieAuth(context.Context, *account.ClaudeCookieAuthInput) (*account.ClaudeTokenInfo, error)
}

// ClaudeOAuthHandler handles OAuth-related operations for accounts
type ClaudeOAuthHandler struct {
	oauthService ClaudeAuthorizationUseCase
}

// NewClaudeOAuthHandler creates a new OAuth handler
func NewClaudeOAuthHandler(oauthService ClaudeAuthorizationUseCase) *ClaudeOAuthHandler {
	return &ClaudeOAuthHandler{
		oauthService: oauthService,
	}
}

// ClaudeGenerateAuthURLRequest represents the request for generating auth URL
type ClaudeGenerateAuthURLRequest struct {
	ProxyID *int64 `json:"proxy_id"`
}

// GenerateAuthURL generates OAuth authorization URL with full scope
// POST /api/v1/admin/accounts/generate-auth-url
func (h *ClaudeOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req ClaudeGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body
		req = ClaudeGenerateAuthURLRequest{}
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
func (h *ClaudeOAuthHandler) GenerateSetupTokenURL(c *gin.Context) {
	var req ClaudeGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body
		req = ClaudeGenerateAuthURLRequest{}
	}

	result, err := h.oauthService.GenerateSetupTokenURL(c.Request.Context(), req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, result)
}

// ClaudeExchangeCodeRequest represents the request for exchanging auth code
type ClaudeExchangeCodeRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	Code      string `json:"code" binding:"required"`
	ProxyID   *int64 `json:"proxy_id"`
}

// ExchangeCode exchanges authorization code for tokens
// POST /api/v1/admin/accounts/exchange-code
func (h *ClaudeOAuthHandler) ExchangeCode(c *gin.Context) {
	var req ClaudeExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.oauthService.ExchangeCode(c.Request.Context(), &account.ClaudeExchangeCodeInput{
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
func (h *ClaudeOAuthHandler) ExchangeSetupTokenCode(c *gin.Context) {
	var req ClaudeExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.oauthService.ExchangeCode(c.Request.Context(), &account.ClaudeExchangeCodeInput{
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

// ClaudeCookieAuthRequest represents the request for cookie-based authentication
type ClaudeCookieAuthRequest struct {
	SessionKey string `json:"code" binding:"required"` // Using 'code' field as sessionKey (frontend sends it this way)
	ProxyID    *int64 `json:"proxy_id"`
}

// CookieAuth performs OAuth using sessionKey (cookie-based auto-auth)
// POST /api/v1/admin/accounts/cookie-auth
func (h *ClaudeOAuthHandler) CookieAuth(c *gin.Context) {
	var req ClaudeCookieAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.oauthService.CookieAuth(c.Request.Context(), &account.ClaudeCookieAuthInput{
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
func (h *ClaudeOAuthHandler) SetupTokenCookieAuth(c *gin.Context) {
	var req ClaudeCookieAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.oauthService.CookieAuth(c.Request.Context(), &account.ClaudeCookieAuthInput{
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
