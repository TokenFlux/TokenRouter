// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	errors "errors"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	clientip "github.com/TokenFlux/TokenRouter/internal/server/clientip"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
	http "net/http"
	strings "strings"
)

type GoogleOneTapOptions struct{ ClientID, FrontendRedirectURL string }
type GoogleOneTapHTTPOptions struct {
	LoadConfig          func(context.Context) (GoogleOneTapOptions, error)
	RegistrationEnabled func(context.Context) bool
}
type GoogleOneTapHandler struct {
	*PendingHandler
	Verifier      identity.GoogleIDTokenVerifier
	googleOptions GoogleOneTapHTTPOptions
}

func NewGoogleOneTapHandler(pending *PendingHandler, verifier identity.GoogleIDTokenVerifier, options GoogleOneTapHTTPOptions) *GoogleOneTapHandler {
	return &GoogleOneTapHandler{pending, verifier, options}
}
func (h *GoogleOneTapHandler) verifyGoogleOneTapCredential(ctx context.Context, credential, audience string) (*identity.GoogleIDTokenClaims, error) {
	return h.Verifier.Verify(ctx, credential, audience)
}

const emailOAuthDefaultRedirect = "/dashboard"
const emailOAuthDefaultFrontendCB = "/auth/oauth/callback"
const (
	GoogleOneTapCredentialMaxBytes  = 16 * 1024
	GoogleOneTapContextMaxBytes     = 256
	GoogleOneTapRequestMaxBytes     = 24 * 1024
	GoogleOneTapStatusAuthenticated = "authenticated"
	GoogleOneTapStatusRegistration  = "registration_required"
)

type GoogleOneTapRequest struct {
	Credential string `json:"credential" binding:"required"`
	Redirect   string `json:"redirect,omitempty"`
	AffCode    string `json:"aff_code,omitempty"`
	PromoCode  string `json:"promo_code,omitempty"`
}
type GoogleOneTapResponse struct {
	Status       string `json:"status"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	Redirect     string `json:"redirect,omitempty"`
}

// GoogleOneTap 使用浏览器取得的 Google ID Token 建立现有面板会话。
func (h *GoogleOneTapHandler) GoogleOneTap(c *gin.Context) {
	// 在 JSON 解码前限制整个匿名请求，避免超长 credential 或未知字段造成大额内存分配。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, GoogleOneTapRequestMaxBytes)
	var req GoogleOneTapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	req.Credential = strings.TrimSpace(req.Credential)
	req.AffCode = strings.TrimSpace(req.AffCode)
	req.PromoCode = strings.TrimSpace(req.PromoCode)
	if req.Credential == "" || len(req.Credential) > GoogleOneTapCredentialMaxBytes {
		response.BadRequest(c, "Google credential is invalid")
		return
	}
	if len(req.AffCode) > GoogleOneTapContextMaxBytes || len(req.PromoCode) > GoogleOneTapContextMaxBytes {
		response.BadRequest(c, "OAuth context is invalid")
		return
	}
	if h == nil || h.PendingHandler == nil || h.SessionHandler == nil || h.authService == nil || h.googleOptions.LoadConfig == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("AUTH_SERVICE_NOT_READY", "authentication service is not ready"))
		return
	}

	cfg, err := h.googleOptions.LoadConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// One Tap 没有动作验证码交互；启用腾讯或阿里云验证码时必须拒绝该入口。
	if err := h.authService.VerifyActionCaptchaIfEnabled(c.Request.Context(), identity.CaptchaProof{}, clientip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	claims, err := h.verifyGoogleOneTapCredential(c.Request.Context(), req.Credential, cfg.ClientID)
	if err != nil || claims == nil || claims.Subject == "" || claims.Email == "" || !claims.EmailVerified {
		response.ErrorFrom(c, infraerrors.Unauthorized("GOOGLE_ONE_TAP_INVALID_CREDENTIAL", "google credential is invalid"))
		return
	}
	h.auditActor(c, 0, claims.Email)

	profile := identity.GoogleEmailProfile(claims)
	input := identity.EmailOAuthIdentityFromProfile("google", profile)
	redirectTo := SanitizeFrontendRedirectPath(req.Redirect)
	if redirectTo == "" {
		redirectTo = emailOAuthDefaultRedirect
	}
	frontendCallback := strings.TrimSpace(cfg.FrontendRedirectURL)
	if frontendCallback == "" {
		frontendCallback = emailOAuthDefaultFrontendCB
	}

	createPending := func() error {
		return h.CreateEmailRegistrationSession(
			c,
			"google",
			frontendCallback,
			redirectTo,
			profile,
			req.AffCode,
			req.PromoCode,
		)
	}
	if shouldCreate, pendingErr := h.flow.EmailShouldCreatePendingRegistration(c.Request.Context(), input); pendingErr != nil {
		response.ErrorFrom(c, pendingErr)
		return
	} else if shouldCreate {
		if !h.googleOptions.RegistrationEnabled(c.Request.Context()) {
			response.ErrorFrom(c, identity.ErrRegDisabled)
			return
		}
		if pendingErr := h.ensureBackendModeAllowsNewUserLogin(c.Request.Context()); pendingErr != nil {
			response.ErrorFrom(c, pendingErr)
			return
		}
		if pendingErr := createPending(); pendingErr != nil {
			response.ErrorFrom(c, pendingErr)
			return
		}
		WriteGoogleOneTapResponse(c, GoogleOneTapResponse{Status: GoogleOneTapStatusRegistration, Redirect: redirectTo})
		return
	}

	tokenPair, user, err := h.authService.LoginOrRegisterVerifiedEmailOAuthWithSignupCodes(
		c.Request.Context(),
		input,
		"",
		req.AffCode,
		req.PromoCode,
	)
	if err != nil {
		if errors.Is(err, identity.ErrOAuthInvitationRequired) {
			if pendingErr := createPending(); pendingErr != nil {
				response.ErrorFrom(c, pendingErr)
				return
			}
			WriteGoogleOneTapResponse(c, GoogleOneTapResponse{Status: GoogleOneTapStatusRegistration, Redirect: redirectTo})
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if tokenPair == nil || user == nil {
		response.ErrorFrom(c, infraerrors.InternalServer("TOKEN_GENERATION_FAILED", "failed to generate token pair"))
		return
	}
	h.auditActor(c, user.ID, user.Email)
	WriteGoogleOneTapResponse(c, GoogleOneTapResponse{
		Status:       GoogleOneTapStatusAuthenticated,
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresIn:    tokenPair.ExpiresIn,
		TokenType:    "Bearer",
	})
}
func WriteGoogleOneTapResponse(c *gin.Context, payload GoogleOneTapResponse) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	response.Success(c, payload)
}
