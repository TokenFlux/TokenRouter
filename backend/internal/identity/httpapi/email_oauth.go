// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	errors "errors"
	fmt "fmt"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	oauthpkce "github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
	http "net/http"
	url "net/url"
	strings "strings"
)

type EmailOAuthHandler struct {
	*PendingHandler
	client     identity.EmailOAuthClient
	loadConfig func(context.Context, string) (identity.EmailOAuthOptions, error)
}

func NewEmailOAuthHandler(p *PendingHandler, client identity.EmailOAuthClient, load func(context.Context, string) (identity.EmailOAuthOptions, error)) *EmailOAuthHandler {
	return &EmailOAuthHandler{p, client, load}
}

const (
	EmailOAuthCookiePath        = "/api/v1/auth/oauth"
	EmailOAuthStateCookieName   = "email_oauth_state"
	EmailOAuthRedirectCookie    = "email_oauth_redirect"
	EmailOAuthProviderCookie    = "email_oauth_provider"
	EmailOAuthAffCookie         = "email_oauth_aff"
	EmailOAuthCookieMaxAgeSec   = 10 * 60
	EmailOAuthDefaultRedirect   = "/dashboard"
	EmailOAuthDefaultFrontendCB = "/auth/oauth/callback"
)

func (h *EmailOAuthHandler) GitHubOAuthStart(c *gin.Context)    { h.EmailOAuthStart(c, "github") }
func (h *EmailOAuthHandler) GoogleOAuthStart(c *gin.Context)    { h.EmailOAuthStart(c, "google") }
func (h *EmailOAuthHandler) GitHubOAuthCallback(c *gin.Context) { h.EmailOAuthCallback(c, "github") }
func (h *EmailOAuthHandler) GoogleOAuthCallback(c *gin.Context) { h.EmailOAuthCallback(c, "google") }
func (h *EmailOAuthHandler) CompleteGitHubOAuthRegistration(c *gin.Context) {
	h.CompleteEmailOAuthRegistration(c, "github")
}
func (h *EmailOAuthHandler) CompleteGoogleOAuthRegistration(c *gin.Context) {
	h.CompleteEmailOAuthRegistration(c, "google")
}
func (h *EmailOAuthHandler) EmailOAuthStart(c *gin.Context, provider string) {
	if !h.RequireActionCaptchaForOAuthLoginStart(c) {
		return
	}
	cfg, err := h.loadConfig(c.Request.Context(), provider)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	state, err := oauthpkce.Verifier()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_STATE_GEN_FAILED", "failed to generate oauth state").WithCause(err))
		return
	}
	redirectTo := SanitizeFrontendRedirectPath(c.Query("redirect"))
	if redirectTo == "" {
		redirectTo = EmailOAuthDefaultRedirect
	}

	secureCookie := IsRequestHTTPS(c)
	EmailOAuthSetCookie(c, EmailOAuthStateCookieName, EncodeCookieValue(state), secureCookie)
	EmailOAuthSetCookie(c, EmailOAuthRedirectCookie, EncodeCookieValue(redirectTo), secureCookie)
	EmailOAuthSetCookie(c, EmailOAuthProviderCookie, EncodeCookieValue(provider), secureCookie)
	CaptureOAuthPromoCode(c, secureCookie)
	// 兼容直接访问邮箱 OAuth start URL 的 aff_code；邀请页仍优先使用更短的 aff。
	if affCode := strings.TrimSpace(identity.OAuthFirstNonEmpty(c.Query("aff"), c.Query("aff_code"))); affCode != "" {
		EmailOAuthSetCookie(c, EmailOAuthAffCookie, EncodeCookieValue(affCode), secureCookie)
	} else {
		EmailOAuthClearCookie(c, EmailOAuthAffCookie, secureCookie)
	}

	authURL, err := BuildEmailOAuthAuthorizeURL(cfg, state)
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BUILD_URL_FAILED", "failed to build oauth authorization url").WithCause(err))
		return
	}
	RespondOAuthStart(c, authURL)
}
func (h *EmailOAuthHandler) EmailOAuthCallback(c *gin.Context, provider string) {
	cfg, cfgErr := h.loadConfig(c.Request.Context(), provider)
	if cfgErr != nil {
		response.ErrorFrom(c, cfgErr)
		return
	}
	frontendCallback := strings.TrimSpace(cfg.FrontendRedirectURL)
	if frontendCallback == "" {
		frontendCallback = EmailOAuthDefaultFrontendCB
	}
	if providerErr := strings.TrimSpace(c.Query("error")); providerErr != "" {
		RedirectOAuthError(c, frontendCallback, "provider_error", providerErr, c.Query("error_description"))
		return
	}
	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" {
		RedirectOAuthError(c, frontendCallback, "missing_params", "missing code/state", "")
		return
	}

	secureCookie := IsRequestHTTPS(c)
	defer func() {
		EmailOAuthClearCookie(c, EmailOAuthStateCookieName, secureCookie)
		EmailOAuthClearCookie(c, EmailOAuthRedirectCookie, secureCookie)
		EmailOAuthClearCookie(c, EmailOAuthProviderCookie, secureCookie)
		EmailOAuthClearCookie(c, EmailOAuthAffCookie, secureCookie)
		ClearOAuthPromoCodeCookie(c, secureCookie)
	}()
	expectedState, err := ReadCookieDecoded(c, EmailOAuthStateCookieName)
	if err != nil || expectedState == "" || expectedState != state {
		RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth state", "")
		return
	}
	expectedProvider, _ := ReadCookieDecoded(c, EmailOAuthProviderCookie)
	if !strings.EqualFold(strings.TrimSpace(expectedProvider), provider) {
		RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth provider", "")
		return
	}
	redirectTo, _ := ReadCookieDecoded(c, EmailOAuthRedirectCookie)
	redirectTo = SanitizeFrontendRedirectPath(redirectTo)
	if redirectTo == "" {
		redirectTo = EmailOAuthDefaultRedirect
	}

	tokenResp, err := h.client.ExchangeCode(c.Request.Context(), cfg, code)
	if err != nil {
		RedirectOAuthError(c, frontendCallback, "token_exchange_failed", "failed to exchange oauth code", OAuthSingleLine(err.Error()))
		return
	}
	profile, err := h.client.FetchProfile(c.Request.Context(), provider, cfg, tokenResp)
	if err != nil {
		RedirectOAuthError(c, frontendCallback, "userinfo_failed", "failed to fetch verified email", OAuthSingleLine(err.Error()))
		return
	}
	h.EmailOAuthCallbackWithProfile(c, provider, cfg, frontendCallback, redirectTo, profile)
}
func (h *EmailOAuthHandler) EmailOAuthCallbackWithProfile(
	c *gin.Context,
	provider string,
	cfg identity.EmailOAuthOptions,
	frontendCallback string,
	redirectTo string,
	profile *identity.EmailOAuthProfile,
) {
	input := identity.EmailOAuthIdentityFromProfile(provider, profile)
	affCode := h.EmailOAuthAffCode(c)
	if shouldCreate, err := h.flow.EmailShouldCreatePendingRegistration(c.Request.Context(), input); err != nil {
		RedirectOAuthError(c, frontendCallback, infraerrors.Reason(err), infraerrors.Message(err), "")
		return
	} else if shouldCreate {
		if pendingErr := h.CreateEmailRegistrationSession(c, provider, frontendCallback, redirectTo, profile, affCode, ReadOAuthPromoCode(c)); pendingErr != nil {
			RedirectOAuthError(c, frontendCallback, infraerrors.Reason(pendingErr), infraerrors.Message(pendingErr), "")
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	tokenPair, user, err := h.authService.LoginOrRegisterVerifiedEmailOAuthWithSignupCodes(c.Request.Context(), input, "", affCode, ReadOAuthPromoCode(c))
	if err != nil {
		if errors.Is(err, identity.ErrOAuthInvitationRequired) {
			if pendingErr := h.CreateEmailRegistrationSession(c, provider, frontendCallback, redirectTo, profile, affCode, ReadOAuthPromoCode(c)); pendingErr != nil {
				RedirectOAuthError(c, frontendCallback, infraerrors.Reason(pendingErr), infraerrors.Message(pendingErr), "")
				return
			}
			RedirectToFrontendCallback(c, frontendCallback)
			return
		}
		RedirectOAuthError(c, frontendCallback, infraerrors.Reason(err), infraerrors.Message(err), "")
		return
	}
	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		RedirectOAuthError(c, frontendCallback, "login_blocked", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}

	fragment := url.Values{}
	fragment.Set("access_token", tokenPair.AccessToken)
	fragment.Set("refresh_token", tokenPair.RefreshToken)
	fragment.Set("expires_in", fmt.Sprintf("%d", tokenPair.ExpiresIn))
	fragment.Set("token_type", "Bearer")
	fragment.Set("redirect", redirectTo)
	RedirectOAuthFragment(c, frontendCallback, fragment)
}
func (h *EmailOAuthHandler) EmailOAuthAffCode(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if code, err := ReadCookieDecoded(c, EmailOAuthAffCookie); err == nil {
		return strings.TrimSpace(code)
	}
	return ""
}
func (h *EmailOAuthHandler) CompleteEmailOAuthRegistration(c *gin.Context, provider string) {
	var req CompleteEmailOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	_, session, clearCookies, err := ReadPendingOAuthBrowserSession(c, h.PendingHandler)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := EnsurePendingOAuthCompleteRegistrationSession(session); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(session.ProviderType), provider) {
		response.BadRequest(c, "Pending oauth session provider mismatch")
		return
	}
	if err := h.ensureBackendModeAllowsNewUserLogin(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	affCode := strings.TrimSpace(req.AffCode)
	if affCode == "" {
		affCode = strings.TrimSpace(PendingSessionStringValue(session.UpstreamIdentityClaims, "aff_code"))
	}

	tokenPair, user, err := h.authService.RegisterVerifiedOAuthEmailAccount(
		c.Request.Context(),
		strings.TrimSpace(session.ResolvedEmail),
		req.Password,
		strings.TrimSpace(req.InvitationCode),
		strings.TrimSpace(session.ProviderType),
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	userProjection := user
	request := identity.PendingAccountFinalization{Session: session, User: userProjection, InvitationCode: strings.TrimSpace(req.InvitationCode), AffiliateCode: affCode}
	if err := h.pendingFlow().FinalizeVerifiedAccount(c.Request.Context(), request); err != nil {
		var failure *identity.PendingWriteError
		if errors.As(err, &failure) {
			switch failure.Phase {
			case "binding":
				RespondPendingOAuthBindingApplyError(c, failure.Cause)
			case "consume":
				clearCookies()
				response.ErrorFrom(c, failure.Cause)
			case "begin", "commit":
				response.ErrorFrom(c, infraerrors.InternalServer("PENDING_AUTH_BIND_APPLY_FAILED", "failed to consume pending oauth session").WithCause(failure.Cause))
			default:
				response.ErrorFrom(c, failure.Cause)
			}
		} else {
			response.ErrorFrom(c, err)
		}
		return
	}

	h.authService.ApplyOAuthSignupPromoCode(c.Request.Context(), user.ID, PendingOAuthPromoCode(session))
	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	clearCookies()
	WriteOAuthTokenPairResponse(c, tokenPair)
}

type CompleteEmailOAuthRequest struct {
	Password       string `json:"password" binding:"required,min=6"`
	InvitationCode string `json:"invitation_code,omitempty"`
	AffCode        string `json:"aff_code,omitempty"`
}

func BuildEmailOAuthAuthorizeURL(cfg identity.EmailOAuthOptions, state string) (string, error) {
	u, err := url.Parse(cfg.AuthorizeURL)
	if err != nil {
		return "", fmt.Errorf("parse authorize_url: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", cfg.RedirectURL)
	q.Set("state", state)
	if strings.TrimSpace(cfg.Scopes) != "" {
		q.Set("scope", cfg.Scopes)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func EmailOAuthSetCookie(c *gin.Context, name, value string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     EmailOAuthCookiePath,
		MaxAge:   EmailOAuthCookieMaxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
func EmailOAuthClearCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     EmailOAuthCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
