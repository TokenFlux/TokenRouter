package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/BrandonVee/TokenRouter/internal/pkg/ip"
	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	servermiddleware "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/idtoken"
)

const (
	googleOneTapCredentialMaxBytes  = 16 * 1024
	googleOneTapContextMaxBytes     = 256
	googleOneTapRequestMaxBytes     = 24 * 1024
	googleOneTapStatusAuthenticated = "authenticated"
	googleOneTapStatusRegistration  = "registration_required"
)

type googleOneTapRequest struct {
	Credential string `json:"credential" binding:"required"`
	Redirect   string `json:"redirect,omitempty"`
	AffCode    string `json:"aff_code,omitempty"`
	PromoCode  string `json:"promo_code,omitempty"`
}

type googleOneTapResponse struct {
	Status       string `json:"status"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	Redirect     string `json:"redirect,omitempty"`
}

type googleIDTokenClaims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	GivenName     string
	Picture       string
	Locale        string
	HostedDomain  string
}

type googleIDTokenVerifier interface {
	Verify(ctx context.Context, credential string, audience string) (*googleIDTokenClaims, error)
}

type googleAPIIDTokenVerifier struct{}

// Verify 使用 Google 官方验证器校验签名和标准声明，再收紧本站依赖的身份字段。
func (googleAPIIDTokenVerifier) Verify(ctx context.Context, credential string, audience string) (*googleIDTokenClaims, error) {
	payload, err := idtoken.Validate(ctx, credential, audience)
	if err != nil {
		return nil, err
	}
	return validateGoogleIDTokenPayload(payload, audience, time.Now())
}

// validateGoogleIDTokenPayload 对官方验证器的结果再做本站所需的严格声明检查。
func validateGoogleIDTokenPayload(payload *idtoken.Payload, audience string, now time.Time) (*googleIDTokenClaims, error) {
	if payload == nil {
		return nil, errors.New("google id token payload is missing")
	}
	if payload.Issuer != "accounts.google.com" && payload.Issuer != "https://accounts.google.com" {
		return nil, errors.New("google id token issuer is invalid")
	}
	if payload.Audience != strings.TrimSpace(audience) {
		return nil, errors.New("google id token audience is invalid")
	}
	if payload.Expires <= now.Unix() {
		return nil, errors.New("google id token is expired")
	}

	verified, ok := payload.Claims["email_verified"].(bool)
	if !ok || !verified {
		return nil, errors.New("google verified email is missing")
	}
	claims := &googleIDTokenClaims{
		Subject:       strings.TrimSpace(payload.Subject),
		Email:         strings.TrimSpace(googleIDTokenStringClaim(payload.Claims, "email")),
		EmailVerified: true,
		Name:          strings.TrimSpace(googleIDTokenStringClaim(payload.Claims, "name")),
		GivenName:     strings.TrimSpace(googleIDTokenStringClaim(payload.Claims, "given_name")),
		Picture:       strings.TrimSpace(googleIDTokenStringClaim(payload.Claims, "picture")),
		Locale:        strings.TrimSpace(googleIDTokenStringClaim(payload.Claims, "locale")),
		HostedDomain:  strings.TrimSpace(googleIDTokenStringClaim(payload.Claims, "hd")),
	}
	if claims.Subject == "" || claims.Email == "" {
		return nil, errors.New("google id token identity is incomplete")
	}
	return claims, nil
}

func googleIDTokenStringClaim(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return value
}

func (h *AuthHandler) verifyGoogleOneTapCredential(ctx context.Context, credential string, audience string) (*googleIDTokenClaims, error) {
	verifier := h.googleIDTokenVerifier
	if verifier == nil {
		verifier = googleAPIIDTokenVerifier{}
	}
	return verifier.Verify(ctx, credential, audience)
}

// GoogleOneTap 使用浏览器取得的 Google ID Token 建立现有面板会话。
func (h *AuthHandler) GoogleOneTap(c *gin.Context) {
	// 在 JSON 解码前限制整个匿名请求，避免超长 credential 或未知字段造成大额内存分配。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, googleOneTapRequestMaxBytes)
	var req googleOneTapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	req.Credential = strings.TrimSpace(req.Credential)
	req.AffCode = strings.TrimSpace(req.AffCode)
	req.PromoCode = strings.TrimSpace(req.PromoCode)
	if req.Credential == "" || len(req.Credential) > googleOneTapCredentialMaxBytes {
		response.BadRequest(c, "Google credential is invalid")
		return
	}
	if len(req.AffCode) > googleOneTapContextMaxBytes || len(req.PromoCode) > googleOneTapContextMaxBytes {
		response.BadRequest(c, "OAuth context is invalid")
		return
	}
	if h == nil || h.authService == nil || h.settingSvc == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("AUTH_SERVICE_NOT_READY", "authentication service is not ready"))
		return
	}

	cfg, err := h.settingSvc.GetGoogleOneTapConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// One Tap 没有动作验证码交互；启用腾讯或阿里云验证码时必须拒绝该入口。
	if err := h.authService.VerifyActionCaptchaIfEnabled(c.Request.Context(), service.CaptchaProof{}, ip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	claims, err := h.verifyGoogleOneTapCredential(c.Request.Context(), req.Credential, cfg.ClientID)
	if err != nil || claims == nil || claims.Subject == "" || claims.Email == "" || !claims.EmailVerified {
		response.ErrorFrom(c, infraerrors.Unauthorized("GOOGLE_ONE_TAP_INVALID_CREDENTIAL", "google credential is invalid"))
		return
	}
	servermiddleware.SetAuditActor(c, 0, claims.Email)

	metadata := map[string]any{"email_verified": true}
	if claims.Locale != "" {
		metadata["locale"] = claims.Locale
	}
	if claims.HostedDomain != "" {
		metadata["hosted_domain"] = claims.HostedDomain
	}
	profile := &emailOAuthProfile{
		Subject:       claims.Subject,
		Email:         claims.Email,
		EmailVerified: true,
		Username:      firstNonEmpty(claims.GivenName, claims.Name, claims.Email),
		DisplayName:   claims.Name,
		AvatarURL:     claims.Picture,
		Metadata:      metadata,
	}
	input := newEmailOAuthIdentityInput("google", profile)
	redirectTo := sanitizeFrontendRedirectPath(req.Redirect)
	if redirectTo == "" {
		redirectTo = emailOAuthDefaultRedirect
	}
	frontendCallback := strings.TrimSpace(cfg.FrontendRedirectURL)
	if frontendCallback == "" {
		frontendCallback = emailOAuthDefaultFrontendCB
	}

	createPending := func() error {
		return h.createEmailOAuthRegistrationPendingSession(
			c,
			"google",
			frontendCallback,
			redirectTo,
			profile,
			req.AffCode,
			req.PromoCode,
		)
	}
	if shouldCreate, pendingErr := h.emailOAuthShouldCreatePendingRegistration(c.Request.Context(), input); pendingErr != nil {
		response.ErrorFrom(c, pendingErr)
		return
	} else if shouldCreate {
		if !h.settingSvc.IsRegistrationEnabled(c.Request.Context()) {
			response.ErrorFrom(c, service.ErrRegistrationDisabled)
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
		writeGoogleOneTapResponse(c, googleOneTapResponse{Status: googleOneTapStatusRegistration, Redirect: redirectTo})
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
		if errors.Is(err, service.ErrOAuthInvitationRequired) {
			if pendingErr := createPending(); pendingErr != nil {
				response.ErrorFrom(c, pendingErr)
				return
			}
			writeGoogleOneTapResponse(c, googleOneTapResponse{Status: googleOneTapStatusRegistration, Redirect: redirectTo})
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
	servermiddleware.SetAuditActor(c, user.ID, user.Email)
	writeGoogleOneTapResponse(c, googleOneTapResponse{
		Status:       googleOneTapStatusAuthenticated,
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresIn:    tokenPair.ExpiresIn,
		TokenType:    "Bearer",
	})
}

func writeGoogleOneTapResponse(c *gin.Context, payload googleOneTapResponse) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	response.Success(c, payload)
}

var _ googleIDTokenVerifier = googleAPIIDTokenVerifier{}
