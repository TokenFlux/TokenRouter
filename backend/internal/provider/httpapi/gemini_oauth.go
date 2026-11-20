package httpapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// gemini_oauth.go handles Gemini authorization requests and derives callback
// hints from HTTP request metadata.
// Gemini authorization accepts two OAuth variants (code_assist and ai_studio)
// and defaults blank type input to Code Assist. The HTTP callback hint is
// derived from Origin or forwarded request fields, while authorization chooses
// its built-in or custom-client callback and stores the effective variant with
// the session.
//
// URL-generation errors containing missing-client or invalid-input markers are
// reported as 400 Bad Request errors. Exchange validates the submitted variant
// and forwards an optional ProjectID override for same-session Project ID
// completion, while the production authorization service uses the session's
// stored variant when exchanging credentials and uses a submitted tier as
// discovery fallback.

// GeminiAuthorizationUseCase supplies OAuth capability display, session
// generation and code exchange.
type GeminiAuthorizationUseCase interface {
	// GetOAuthConfig returns the authorization configuration capabilities for
	// administrator display. The handler forwards this result directly, so the
	// implementation supplies the current enabled variants and required
	// client-configuration information.
	GetOAuthConfig() *provider.GeminiOAuthCapabilities
	// GenerateAuthURL creates a Google session from proxy, callback, project,
	// OAuth variant and tier hints. The implementation selects its effective
	// client callback, and callers retain the returned session and state for
	// exchange.
	GenerateAuthURL(context.Context, *int64, string, string, string, string) (*provider.GeminiAuthURLResult, error)
	// ExchangeCode validates the session and state and returns Google token
	// metadata after exchange. The production implementation takes its effective
	// OAuth variant from the stored session, applies an optional ProjectID
	// override, and uses a supplied tier as discovery fallback.
	ExchangeCode(context.Context, *provider.GeminiExchangeCodeInput) (*provider.GeminiTokenInfo, error)
}

// GeminiOAuthHandler validates Google authorization variants and binds
// administrator requests to authorization.
type GeminiOAuthHandler struct {
	geminiOAuthService GeminiAuthorizationUseCase // borrowed Google authorization use case
}

// GeminiGenerateAuthURLRequest carries optional proxy, project, variant and
// fallback-tier choices.
type GeminiGenerateAuthURLRequest struct {
	// ProxyID supplies session generation's proxy lookup. Omitted or unresolved
	// selection leaves an empty route retained for the later Google exchange.
	ProxyID *int64 `json:"proxy_id"` // optional session proxy selection
	// ProjectID is trimmed into the session and passed as a discovery hint when
	// nonempty. Blank input leaves project discovery to authorization.
	ProjectID string `json:"project_id"` // requested Google project identifier
	// OAuthType selects code_assist or ai_studio during session generation.
	// Blank input defaults to code_assist, and the selected variant is stored for
	// later exchange.
	OAuthType string `json:"oauth_type"` // requested OAuth variant selector
	// TierID supplies a fallback for later account tier discovery. Session
	// generation normalizes this choice according to its selected OAuth variant
	// before storing the hint.
	TierID string `json:"tier_id"` // requested fallback tier identifier
}

// GeminiExchangeCodeRequest binds session credentials and optional project,
// variant and tier values.
type GeminiExchangeCodeRequest struct {
	SessionID string `json:"session_id" binding:"required"` // authorization session identifier
	State     string `json:"state" binding:"required"`      // authorization state value
	Code      string `json:"code" binding:"required"`       // authorization exchange code
	// ProjectID overrides the stored session project hint when nonblank so
	// administrators can fill a GCP Project ID and retry exchange in place.
	ProjectID string `json:"project_id"` // optional GCP project override
	// ProxyID overrides the session route only when its lookup resolves. Omitted
	// or unresolved selection retains the stored proxy through Google exchange.
	ProxyID *int64 `json:"proxy_id"` // optional exchange proxy selection
	// OAuthType is validated here as code_assist or ai_studio. Blank input
	// selects code_assist; production exchange uses the variant stored in the
	// authorization session.
	OAuthType string `json:"oauth_type"` // requested OAuth variant selector
	// TierID supplies a fallback when account tier discovery cannot choose a tier.
	// During exchange, a blank supplied tier permits the session's stored tier
	// fallback.
	TierID string `json:"tier_id"` // requested fallback tier identifier
}

// NewGeminiOAuthHandler borrows Google authorization for capability display and
// session exchange. Its owner supplies current client configuration and manages
// the stored-session cleanup lifetime used by these endpoints.
func NewGeminiOAuthHandler(geminiOAuthService GeminiAuthorizationUseCase) *GeminiOAuthHandler {
	return &GeminiOAuthHandler{geminiOAuthService: geminiOAuthService}
}

// GetCapabilities returns the authorization use case's current Gemini client
// capability settings. The service supplies its configured values on this call,
// allowing the frontend to choose available authorization variants.
// GET /api/v1/admin/gemini/oauth/capabilities
func (h *GeminiOAuthHandler) GetCapabilities(c *gin.Context) {
	cfg := h.geminiOAuthService.GetOAuthConfig()
	response.Success(c, cfg)
}

// GenerateAuthURL validates a Gemini variant and requests its authorization
// session with proxy, project and tier hints. Blank variant means code_assist;
// missing-client and invalid-input messages map to 400 and other generation
// failures to 500.
// POST /api/v1/admin/gemini/oauth/auth-url
func (h *GeminiOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req GeminiGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// Older clients omit oauth_type. Normalize blank input to Code Assist before
	// validating the variant names accepted by the administrator authorization flow.
	oauthType := strings.TrimSpace(req.OAuthType)
	if oauthType == "" {
		oauthType = "code_assist"
	}
	if oauthType != "code_assist" && oauthType != "ai_studio" {
		response.BadRequest(c, "Invalid oauth_type: must be 'code_assist' or 'ai_studio' (personal and Google One accounts must use Antigravity)")
		return
	}

	// The hosted callback is an HTTP-derived hint. Production authorization chooses
	// its built-in or custom-client callback before exchanging the stored session.
	redirectURI := deriveGeminiRedirectURI(c)
	result, err := h.geminiOAuthService.GenerateAuthURL(c.Request.Context(), req.ProxyID, redirectURI, req.ProjectID, oauthType, req.TierID)
	if err != nil {
		msg := err.Error()
		// Missing client configuration and malformed project or variant inputs can
		// be corrected by the administrator; classify them as 400 before generic 500.
		if strings.Contains(msg, "OAuth client not configured") ||
			strings.Contains(msg, "requires your own OAuth Client") ||
			strings.Contains(msg, "requires a custom OAuth Client") ||
			strings.Contains(msg, "GEMINI_CLI_OAUTH_CLIENT_SECRET_MISSING") ||
			strings.Contains(msg, "built-in Gemini CLI OAuth client_secret is not configured") ||
			strings.Contains(msg, "invalid project_id") ||
			strings.Contains(msg, "invalid oauth_type") {
			response.BadRequest(c, "Failed to generate auth URL: "+msg)
			return
		}
		response.InternalError(c, "Failed to generate auth URL: "+msg)
		return
	}

	response.Success(c, result)
}

// ExchangeCode binds required session, state and code, forwards an optional
// ProjectID override, and validates the frontend OAuth variant. Blank type
// defaults to code_assist, while production authorization derives the effective
// exchange variant from the stored session.
// POST /api/v1/admin/gemini/oauth/exchange-code
func (h *GeminiOAuthHandler) ExchangeCode(c *gin.Context) {
	var req GeminiExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// Older clients omit oauth_type. Normalize blank input to Code Assist before
	// validating the variant names accepted by the administrator authorization flow.
	oauthType := strings.TrimSpace(req.OAuthType)
	if oauthType == "" {
		oauthType = "code_assist"
	}
	if oauthType != "code_assist" && oauthType != "ai_studio" {
		response.BadRequest(c, "Invalid oauth_type: must be 'code_assist' or 'ai_studio' (personal and Google One accounts must use Antigravity)")
		return
	}

	tokenInfo, err := h.geminiOAuthService.ExchangeCode(c.Request.Context(), &provider.GeminiExchangeCodeInput{
		SessionID: req.SessionID,
		State:     req.State,
		Code:      req.Code,
		ProjectID: req.ProjectID,
		ProxyID:   req.ProxyID,
		OAuthType: oauthType,
		TierID:    req.TierID,
	})
	if err != nil {
		response.BadRequest(c, "Failed to exchange code: "+err.Error())
		return
	}

	response.Success(c, tokenInfo)
}

// deriveGeminiRedirectURI derives the hosted callback hint from the incoming
// request. Nonempty Origin wins and loses trailing slashes; otherwise forwarded
// headers supply their first comma-separated values before TLS and request-host
// defaults. The helper trusts these header values as supplied.
//
// The hint is passed into session generation, where GeminiAuthorization
// replaces it with the client's configured callback. Header parsing therefore
// describes the frontend hint, while the stored session determines the actual
// callback used for token exchange.
func deriveGeminiRedirectURI(c *gin.Context) string {
	origin := strings.TrimSpace(c.GetHeader("Origin"))
	if origin != "" {
		return strings.TrimRight(origin, "/") + "/auth/callback"
	}

	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	// Forwarded proto and host can contain comma-separated observations. Their
	// first tokens supply the hosted callback hint consumed by authorization session generation.
	if xfProto := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); xfProto != "" {
		scheme = strings.TrimSpace(strings.Split(xfProto, ",")[0])
	}

	host := strings.TrimSpace(c.Request.Host)
	if xfHost := strings.TrimSpace(c.GetHeader("X-Forwarded-Host")); xfHost != "" {
		host = strings.TrimSpace(strings.Split(xfHost, ",")[0])
	}

	return fmt.Sprintf("%s://%s/auth/callback", scheme, host)
}
