package httpapi

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// antigravity_oauth.go handles Antigravity authorization URL, code exchange,
// and refresh-token HTTP requests.
// Antigravity authorization binds Google OAuth session, state, code, and
// optional GCP Project ID hints before delegating token exchange or refresh
// validation. Invalid bodies are rejected here, URL-generation failures use
// 500, code-exchange failures use 400, and refresh-token validation keeps the
// common domain error mapping.

// AntigravityOAuthHandler binds administrator authorization inputs to the
// Antigravity authorization use case.
type AntigravityOAuthHandler struct {
	antigravityOAuthService AntigravityAuthorizationUseCase // borrowed Google authorization use case
}

// AntigravityGenerateAuthURLRequest optionally selects the proxy used for a
// Google authorization session.
type AntigravityGenerateAuthURLRequest struct {
	// ProxyID supplies session generation's proxy lookup. Omitted or unresolved
	// selection leaves the empty route retained with the generated Google session.
	ProxyID *int64 `json:"proxy_id"` // optional session proxy selection
}

// AntigravityExchangeCodeRequest carries the required session, state and code
// plus optional GCP project and proxy selections for Google token exchange.
type AntigravityExchangeCodeRequest struct {
	SessionID string `json:"session_id" binding:"required"` // authorization session identifier
	State     string `json:"state" binding:"required"`      // authorization state value
	Code      string `json:"code" binding:"required"`       // authorization exchange code
	// ProjectID supplies an explicit GCP Project ID when automatic companion
	// project discovery returns empty so retries can complete without re-login.
	ProjectID string `json:"project_id"` // optional GCP project identifier
	// ProxyID overrides the stored route only when its lookup resolves. Omitted
	// or unresolved selection retains the authorization session's proxy URL.
	ProxyID *int64 `json:"proxy_id"` // optional exchange proxy selection
}

// AntigravityRefreshTokenRequest carries a required refresh token and optional
// project and proxy hints for validation.
type AntigravityRefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"` // supplied OAuth refresh token
	// ProjectID supplies an explicit GCP Project ID fallback when automatic
	// companion project discovery returns empty during refresh validation.
	ProjectID string `json:"project_id"` // optional GCP project identifier
	// ProxyID supplies raw refresh validation's proxy lookup. Omitted or
	// unresolved selection leaves an empty URL for the token and discovery calls.
	ProxyID *int64 `json:"proxy_id"` // optional refresh proxy selection
}

// AntigravityAuthorizationUseCase supplies Google authorization sessions, code
// exchange and refresh-token validation.
type AntigravityAuthorizationUseCase interface {
	// GenerateAuthURL prepares a Google authorization URL and stored session for
	// the optional proxy. Its caller uses the returned session and state during
	// exchange, while session retention belongs to authorization.
	GenerateAuthURL(context.Context, *int64) (*provider.AntigravityAuthURLResult, error)
	// ExchangeCode validates a stored session and its state before exchanging the
	// supplied code. Proxy selection and session consumption follow authorization;
	// missing-project retries reuse cached session tokens within the session TTL.
	ExchangeCode(context.Context, *provider.AntigravityExchangeCodeInput) (*provider.AntigravityTokenInfo, error)
	// ValidateRefreshTokenWithProject exchanges the supplied refresh token and
	// resolves Antigravity metadata with an optional GCP Project ID hint through
	// the selected proxy. Its caller receives credentials for later provider
	// creation rather than a provider storage mutation.
	ValidateRefreshTokenWithProject(context.Context, string, string, *int64) (*provider.AntigravityTokenInfo, error)
}

// NewAntigravityOAuthHandler borrows the Google authorization use case for
// Antigravity administrator routes. Session storage and token metadata
// discovery remain with that instance, whose owner manages request shutdown and
// cleanup.
func NewAntigravityOAuthHandler(antigravityOAuthService AntigravityAuthorizationUseCase) *AntigravityOAuthHandler {
	return &AntigravityOAuthHandler{antigravityOAuthService: antigravityOAuthService}
}

// GenerateAuthURL binds the optional proxy and returns the generated Google
// authorization session. Invalid JSON returns 400, while every use-case
// generation error is reported with the endpoint's 500 message.
// POST /api/v1/admin/antigravity/oauth/auth-url
func (h *AntigravityOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req AntigravityGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	result, err := h.antigravityOAuthService.GenerateAuthURL(c.Request.Context(), req.ProxyID)
	if err != nil {
		response.InternalError(c, "Failed to generate authorization URL: "+err.Error())
		return
	}

	response.Success(c, result)
}

// ExchangeCode binds the required session, state and authorization code plus
// optional ProjectID before exchanging Google tokens. Invalid bodies and
// use-case exchange failures return 400; token information is returned after a
// successful exchange.
// POST /api/v1/admin/antigravity/oauth/exchange-code
func (h *AntigravityOAuthHandler) ExchangeCode(c *gin.Context) {
	var req AntigravityExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.antigravityOAuthService.ExchangeCode(c.Request.Context(), &provider.AntigravityExchangeCodeInput{
		SessionID: req.SessionID,
		State:     req.State,
		Code:      req.Code,
		ProjectID: req.ProjectID,
		ProxyID:   req.ProxyID,
	})
	if err != nil {
		response.BadRequest(c, "Failed to exchange token: "+err.Error())
		return
	}

	response.Success(c, tokenInfo)
}

// RefreshToken validates a supplied Antigravity refresh token and optional
// ProjectID through authorization and returns its token information. A missing
// required token is a binding error; authorization failures retain the common
// domain error mapping.
// POST /api/v1/admin/antigravity/oauth/refresh-token
func (h *AntigravityOAuthHandler) RefreshToken(c *gin.Context) {
	var req AntigravityRefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.antigravityOAuthService.ValidateRefreshTokenWithProject(c.Request.Context(), req.RefreshToken, req.ProjectID, req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, tokenInfo)
}
