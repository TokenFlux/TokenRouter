// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

type OIDCHandler struct {
	*PendingHandler
	binding    *OAuthBindHandler
	client     identity.OIDCOAuthClient
	loadConfig func(context.Context) (identity.OIDCOAuthOptions, error)
}

func NewOIDCHandler(p *PendingHandler, b *OAuthBindHandler, client identity.OIDCOAuthClient, load func(context.Context) (identity.OIDCOAuthOptions, error)) *OIDCHandler {
	return &OIDCHandler{p, b, client, load}
}

const (
	OidcOAuthCookiePath         = "/api/v1/auth/oauth/oidc"
	OidcOAuthStateCookieName    = "oidc_oauth_state"
	OidcOAuthVerifierCookie     = "oidc_oauth_verifier"
	OidcOAuthRedirectCookie     = "oidc_oauth_redirect"
	OidcOAuthNonceCookie        = "oidc_oauth_nonce"
	OidcOAuthIntentCookieName   = "oidc_oauth_intent"
	OidcOAuthBindUserCookieName = "oidc_oauth_bind_user"
	OidcOAuthCookieMaxAgeSec    = 10 * 60 // 10 minutes
	OidcOAuthDefaultRedirectTo  = "/dashboard"
	OidcOAuthDefaultFrontendCB  = "/auth/oidc/callback"
)

// OIDCOAuthStart 启动通用 OIDC OAuth 登录流程。
// GET /api/v1/auth/oauth/oidc/start?redirect=/dashboard
func (h *OIDCHandler) OIDCOAuthStart(c *gin.Context) {
	if !h.RequireActionCaptchaForOAuthLoginStart(c) {
		return
	}
	cfg, err := h.loadConfig(c.Request.Context())
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
		redirectTo = OidcOAuthDefaultRedirectTo
	}

	browserSessionKey, err := GenerateOAuthPendingBrowserSession()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BROWSER_SESSION_GEN_FAILED", "failed to generate oauth browser session").WithCause(err))
		return
	}

	secureCookie := IsRequestHTTPS(c)
	OIDCSetCookie(c, OidcOAuthStateCookieName, EncodeCookieValue(state), OidcOAuthCookieMaxAgeSec, secureCookie)
	OIDCSetCookie(c, OidcOAuthRedirectCookie, EncodeCookieValue(redirectTo), OidcOAuthCookieMaxAgeSec, secureCookie)
	intent := NormalizeOAuthIntent(c.Query("intent"))
	OIDCSetCookie(c, OidcOAuthIntentCookieName, EncodeCookieValue(intent), OidcOAuthCookieMaxAgeSec, secureCookie)
	CaptureOAuthPromoCode(c, secureCookie)
	SetOAuthPendingBrowserCookie(c, browserSessionKey, secureCookie)
	ClearOAuthPendingSessionCookie(c, secureCookie)
	if intent == OauthIntentBindCurrentUser {
		bindCookieValue, err := h.binding.BuildOAuthBindUserCookieFromContext(c)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		OIDCSetCookie(c, OidcOAuthBindUserCookieName, EncodeCookieValue(bindCookieValue), OidcOAuthCookieMaxAgeSec, secureCookie)
	} else {
		OIDCClearCookie(c, OidcOAuthBindUserCookieName, secureCookie)
	}

	codeChallenge := ""
	if cfg.UsePKCE {
		verifier, genErr := oauthpkce.Verifier()
		if genErr != nil {
			response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_PKCE_GEN_FAILED", "failed to generate pkce verifier").WithCause(genErr))
			return
		}
		codeChallenge = oauthpkce.Challenge(verifier)
		OIDCSetCookie(c, OidcOAuthVerifierCookie, EncodeCookieValue(verifier), OidcOAuthCookieMaxAgeSec, secureCookie)
	}

	nonce := ""
	if cfg.ValidateIDToken {
		nonce, err = oauthpkce.Verifier()
		if err != nil {
			response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_NONCE_GEN_FAILED", "failed to generate oauth nonce").WithCause(err))
			return
		}
		OIDCSetCookie(c, OidcOAuthNonceCookie, EncodeCookieValue(nonce), OidcOAuthCookieMaxAgeSec, secureCookie)
	}

	redirectURI := strings.TrimSpace(cfg.RedirectURL)
	if redirectURI == "" {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url not configured"))
		return
	}

	authURL, err := BuildOIDCAuthorizeURL(cfg, state, nonce, codeChallenge, redirectURI)
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BUILD_URL_FAILED", "failed to build oauth authorization url").WithCause(err))
		return
	}

	RespondOAuthStart(c, authURL)
}

// OIDCOAuthCallback 处理 OIDC 回调：校验 id_token、创建/登录用户并重定向到前端。
// GET /api/v1/auth/oauth/oidc/callback?code=...&state=...
func (h *OIDCHandler) OIDCOAuthCallback(c *gin.Context) {
	cfg, cfgErr := h.loadConfig(c.Request.Context())
	if cfgErr != nil {
		response.ErrorFrom(c, cfgErr)
		return
	}

	frontendCallback := strings.TrimSpace(cfg.FrontendRedirectURL)
	if frontendCallback == "" {
		frontendCallback = OidcOAuthDefaultFrontendCB
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
		OIDCClearCookie(c, OidcOAuthStateCookieName, secureCookie)
		OIDCClearCookie(c, OidcOAuthVerifierCookie, secureCookie)
		OIDCClearCookie(c, OidcOAuthRedirectCookie, secureCookie)
		OIDCClearCookie(c, OidcOAuthNonceCookie, secureCookie)
		OIDCClearCookie(c, OidcOAuthIntentCookieName, secureCookie)
		OIDCClearCookie(c, OidcOAuthBindUserCookieName, secureCookie)
		ClearOAuthPromoCodeCookie(c, secureCookie)
	}()

	expectedState, err := ReadCookieDecoded(c, OidcOAuthStateCookieName)
	if err != nil || expectedState == "" || state != expectedState {
		RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth state", "")
		return
	}

	redirectTo, _ := ReadCookieDecoded(c, OidcOAuthRedirectCookie)
	redirectTo = SanitizeFrontendRedirectPath(redirectTo)
	if redirectTo == "" {
		redirectTo = OidcOAuthDefaultRedirectTo
	}
	browserSessionKey, _ := ReadOAuthPendingBrowserCookie(c)
	if strings.TrimSpace(browserSessionKey) == "" {
		RedirectOAuthError(c, frontendCallback, "missing_browser_session", "missing oauth browser session", "")
		return
	}
	intent, _ := ReadCookieDecoded(c, OidcOAuthIntentCookieName)
	intent = NormalizeOAuthIntent(intent)

	codeVerifier := ""
	if cfg.UsePKCE {
		codeVerifier, _ = ReadCookieDecoded(c, OidcOAuthVerifierCookie)
		if codeVerifier == "" {
			RedirectOAuthError(c, frontendCallback, "missing_verifier", "missing pkce verifier", "")
			return
		}
	}

	expectedNonce := ""
	if cfg.ValidateIDToken {
		expectedNonce, _ = ReadCookieDecoded(c, OidcOAuthNonceCookie)
		if expectedNonce == "" {
			RedirectOAuthError(c, frontendCallback, "missing_nonce", "missing oauth nonce", "")
			return
		}
	}

	redirectURI := strings.TrimSpace(cfg.RedirectURL)
	if redirectURI == "" {
		RedirectOAuthError(c, frontendCallback, "config_error", "oauth redirect url not configured", "")
		return
	}

	tokenResp, err := h.client.ExchangeCode(c.Request.Context(), cfg, code, redirectURI, codeVerifier)
	if err != nil {
		description := ""
		var exchangeErr *identity.OIDCTokenExchangeError
		if errors.As(err, &exchangeErr) && exchangeErr != nil {
			log.Printf(
				"[OIDC OAuth] token exchange failed: status=%d provider_error=%q provider_description=%q body=%s",
				exchangeErr.StatusCode,
				exchangeErr.ProviderError,
				exchangeErr.ProviderDescription,
				logredact.TruncateUTF8Value(exchangeErr.Body, 2048),
			)
			description = exchangeErr.Error()
		} else {
			log.Printf("[OIDC OAuth] token exchange failed: %v", err)
			description = err.Error()
		}
		RedirectOAuthError(c, frontendCallback, "token_exchange_failed", "failed to exchange oauth code", OAuthSingleLine(description))
		return
	}

	var idClaims *identity.OIDCVerifiedClaims
	if cfg.ValidateIDToken {
		if strings.TrimSpace(tokenResp.IDToken) == "" {
			RedirectOAuthError(c, frontendCallback, "missing_id_token", "missing id_token", "")
			return
		}

		idClaims, err = h.client.ValidateIDToken(c.Request.Context(), cfg, tokenResp.IDToken, expectedNonce)
		if err != nil {
			log.Printf("[OIDC OAuth] id_token validation failed: %v", err)
			RedirectOAuthError(c, frontendCallback, "invalid_id_token", "failed to validate id_token", "")
			return
		}
	}

	userInfoClaims, err := h.client.FetchUserInfo(c.Request.Context(), cfg, tokenResp)
	if err != nil {
		log.Printf("[OIDC OAuth] userinfo fetch failed: %v", err)
		RedirectOAuthError(c, frontendCallback, "userinfo_failed", "failed to fetch user info", "")
		return
	}

	subject := ""
	if idClaims != nil {
		subject = strings.TrimSpace(idClaims.Subject)
	}
	if subject == "" {
		subject = strings.TrimSpace(userInfoClaims.Subject)
	}
	if subject == "" {
		RedirectOAuthError(c, frontendCallback, "missing_subject", "missing subject claim", "")
		return
	}
	issuer := ""
	if idClaims != nil {
		issuer = strings.TrimSpace(idClaims.Issuer)
	}
	if issuer == "" {
		issuer = strings.TrimSpace(cfg.IssuerURL)
	}
	if issuer == "" {
		RedirectOAuthError(c, frontendCallback, "missing_issuer", "missing issuer claim", "")
		return
	}

	emailVerified := userInfoClaims.EmailVerified
	if emailVerified == nil && idClaims != nil {
		emailVerified = idClaims.EmailVerified
	}
	if idClaims != nil && userInfoClaims.Subject != "" && idClaims.Subject != "" && strings.TrimSpace(userInfoClaims.Subject) != strings.TrimSpace(idClaims.Subject) {
		RedirectOAuthError(c, frontendCallback, "subject_mismatch", "userinfo subject does not match id_token", "")
		return
	}

	identityKey := identity.OIDCIdentityKey(issuer, subject)
	compatEmail := strings.TrimSpace(userInfoClaims.Email)
	if compatEmail == "" && idClaims != nil {
		compatEmail = strings.TrimSpace(idClaims.Email)
	}
	email := identity.OIDCSyntheticEmailFromIdentityKey(identityKey)
	username := identity.OAuthFirstNonEmpty(
		userInfoClaims.Username,
		func() string {
			if idClaims != nil {
				return idClaims.PreferredUsername
			}
			return ""
		}(),
		func() string {
			if idClaims != nil {
				return idClaims.Name
			}
			return ""
		}(),
		identity.OIDCFallbackUsername(subject),
	)
	identityRef := identity.PendingAuthIdentityKey{
		ProviderType:    "oidc",
		ProviderKey:     issuer,
		ProviderSubject: subject,
	}
	upstreamClaims := map[string]any{
		"email":             email,
		"username":          username,
		"subject":           subject,
		"issuer":            issuer,
		"email_verified":    emailVerified != nil && *emailVerified,
		"provider_fallback": strings.TrimSpace(cfg.ProviderName),
		"suggested_display_name": identity.OAuthFirstNonEmpty(userInfoClaims.DisplayName, func() string {
			if idClaims != nil {
				return idClaims.Name
			}
			return ""
		}(), username),
		"suggested_avatar_url": userInfoClaims.AvatarURL,
	}
	if compatEmail != "" && !strings.EqualFold(strings.TrimSpace(compatEmail), strings.TrimSpace(email)) {
		upstreamClaims["compat_email"] = compatEmail
	}
	if intent == OauthIntentBindCurrentUser {
		targetUserID, err := h.binding.ReadOAuthBindUserIDFromCookie(c, OidcOAuthBindUserCookieName)
		if err != nil {
			RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth bind target", "")
			return
		}
		if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
			Intent:                 OauthIntentBindCurrentUser,
			Identity:               identityRef,
			TargetUserID:           &targetUserID,
			ResolvedEmail:          email,
			RedirectTo:             redirectTo,
			BrowserSessionKey:      browserSessionKey,
			UpstreamIdentityClaims: upstreamClaims,
			CompletionResponse: map[string]any{
				"redirect": redirectTo,
			},
		}); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth bind", "")
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	existingIdentityUser, err := h.flow.Database.FindOAuthIdentityUser(c.Request.Context(), identityRef)
	if err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}
	if existingIdentityUser != nil {
		if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
			Intent:                 OauthIntentLogin,
			Identity:               identityRef,
			TargetUserID:           &existingIdentityUser.ID,
			ResolvedEmail:          existingIdentityUser.Email,
			RedirectTo:             redirectTo,
			BrowserSessionKey:      browserSessionKey,
			UpstreamIdentityClaims: upstreamClaims,
			CompletionResponse: map[string]any{
				"redirect": redirectTo,
			},
		}); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth login", "")
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	compatEmailUser, err := h.flow.Database.FindOIDCCompatEmailUser(c.Request.Context(), compatEmail)
	if err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}

	if cfg.RequireEmailVerified {
		if emailVerified == nil || !*emailVerified {
			RedirectOAuthError(c, frontendCallback, "email_not_verified", "email is not verified", "")
			return
		}
	}

	// 快捷路径：当上游返回已验证邮箱、部署不要求额外确认且本地没有同邮箱账号时，
	// 直接信任上游身份完成注册/登录，避免展示 choice 页。
	if compatEmailUser == nil &&
		strings.TrimSpace(compatEmail) != "" &&
		emailVerified != nil && *emailVerified {
		if handled := h.TryOIDCVerifiedEmailFastPath(
			c,
			frontendCallback,
			redirectTo,
			identityRef,
			compatEmail,
			username,
			upstreamClaims,
		); handled {
			return
		}
	}

	if h.isForceEmailOnThirdPartySignup(c.Request.Context()) {
		if err := h.CreateOIDCChoiceSession(
			c,
			identityRef,
			email,
			email,
			redirectTo,
			browserSessionKey,
			upstreamClaims,
			compatEmail,
			compatEmailUser,
			true,
		); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth login", "")
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	if err := h.CreateOIDCChoiceSession(
		c,
		identityRef,
		email,
		email,
		redirectTo,
		browserSessionKey,
		upstreamClaims,
		compatEmail,
		compatEmailUser,
		h.isForceEmailOnThirdPartySignup(c.Request.Context()),
	); err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth login", "")
		return
	}
	RedirectToFrontendCallback(c, frontendCallback)
}

// CompleteOIDCOAuthRegistration completes a pending OAuth registration by validating
// the invitation code and creating the user account.
// POST /api/v1/auth/oauth/oidc/complete-registration
func (h *OIDCHandler) CompleteOIDCOAuthRegistration(c *gin.Context) {
	var req CompleteOIDCOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	secureCookie := IsRequestHTTPS(c)
	sessionToken, err := ReadOAuthPendingSessionCookie(c)
	if err != nil {
		ClearOAuthPendingSessionCookie(c, secureCookie)
		ClearOAuthPendingBrowserCookie(c, secureCookie)
		response.ErrorFrom(c, identity.ErrPendingAuthSessionNotFound)
		return
	}
	browserSessionKey, err := ReadOAuthPendingBrowserCookie(c)
	if err != nil {
		ClearOAuthPendingSessionCookie(c, secureCookie)
		ClearOAuthPendingBrowserCookie(c, secureCookie)
		response.ErrorFrom(c, identity.ErrPendingAuthBrowserMismatch)
		return
	}
	pendingSvc, err := h.pendingIdentityService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	session, err := pendingSvc.GetBrowserSession(c.Request.Context(), sessionToken, browserSessionKey)
	if err != nil {
		ClearOAuthPendingSessionCookie(c, secureCookie)
		ClearOAuthPendingBrowserCookie(c, secureCookie)
		response.ErrorFrom(c, err)
		return
	}
	if err := EnsurePendingOAuthCompleteRegistrationSession(session); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if updatedSession, handled, err := h.flow.LegacyRegistrationStatus(c.Request.Context(), session, h.authService != nil && h.authService.IsEmailVerifyEnabled(c.Request.Context()), h.isForceEmailOnThirdPartySignup(c.Request.Context())); err != nil {
		response.ErrorFrom(c, err)
		return
	} else if handled {
		c.JSON(http.StatusOK, BuildPendingOAuthSessionStatusPayload(updatedSession))
		return
	} else {
		session = updatedSession
	}
	if err := h.ensureBackendModeAllowsNewUserLogin(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	email := strings.TrimSpace(session.ResolvedEmail)
	username := PendingSessionStringValue(session.UpstreamIdentityClaims, "username")
	if email == "" || username == "" {
		response.ErrorFrom(c, infraerrors.BadRequest("PENDING_AUTH_SESSION_INVALID", "pending auth registration context is invalid"))
		return
	}

	if !h.flow.Available() {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready"))
		return
	}
	if err := h.flow.Database.EnsureRegistrationIdentityAvailable(c.Request.Context(), session); err != nil {
		RespondPendingOAuthBindingApplyError(c, err)
		return
	}
	decision, err := h.ensurePendingOAuthAdoptionDecision(c, session.ID, OauthAdoptionDecisionRequest{
		AdoptDisplayName: req.AdoptDisplayName,
		AdoptAvatar:      req.AdoptAvatar,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	tokenPair, user, err := h.authService.LoginOrRegisterOAuthWithTokenPairAndPromoCode(c.Request.Context(), email, username, req.InvitationCode, req.AffCode, PendingOAuthPromoCode(session), "oidc")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.flow.Database.ApplyAdoptionAndConsume(c.Request.Context(), session, decision, user.ID); err != nil {
		RespondPendingOAuthBindingApplyError(c, err)
		return
	}
	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	ClearOAuthPendingSessionCookie(c, secureCookie)
	ClearOAuthPendingBrowserCookie(c, secureCookie)

	c.JSON(http.StatusOK, gin.H{
		"access_token":  tokenPair.AccessToken,
		"refresh_token": tokenPair.RefreshToken,
		"expires_in":    tokenPair.ExpiresIn,
		"token_type":    "Bearer",
	})
}

type CompleteOIDCOAuthRequest struct {
	InvitationCode   string `json:"invitation_code" binding:"required"`
	AffCode          string `json:"aff_code,omitempty"` // 邀请返利码，仅注册新用户时绑定邀请关系。
	AdoptDisplayName *bool  `json:"adopt_display_name,omitempty"`
	AdoptAvatar      *bool  `json:"adopt_avatar,omitempty"`
}

func BuildOIDCAuthorizeURL(cfg identity.OIDCOAuthOptions, state, nonce, codeChallenge, redirectURI string) (string, error) {
	u, err := url.Parse(cfg.AuthorizeURL)
	if err != nil {
		return "", fmt.Errorf("parse authorize_url: %w", err)
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	if strings.TrimSpace(cfg.Scopes) != "" {
		q.Set("scope", cfg.Scopes)
	}
	q.Set("state", state)
	if strings.TrimSpace(nonce) != "" {
		q.Set("nonce", nonce)
	}
	if strings.TrimSpace(codeChallenge) != "" {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "S256")
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}
func OIDCSetCookie(c *gin.Context, name, value string, maxAgeSec int, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     OidcOAuthCookiePath,
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
func OIDCClearCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     OidcOAuthCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// TryOIDCVerifiedEmailFastPath 在 OIDC 上游已返回已验证邮箱时尝试跳过 choice/pending 页。
// 返回 true 表示已经写出重定向响应；返回 false 表示调用方应继续回退到常规 choice 流程。
func (h *OIDCHandler) TryOIDCVerifiedEmailFastPath(
	c *gin.Context,
	frontendCallback string,
	redirectTo string,
	identityKey identity.PendingAuthIdentityKey,
	compatEmail string,
	username string,
	upstreamClaims map[string]any,
) bool {
	if h == nil || h.authService == nil || h.settingSvc == nil {
		return false
	}
	ctx := c.Request.Context()
	if h.isForceEmailOnThirdPartySignup(ctx) {
		return false
	}
	if h.settingSvc.IsInvitationCodeEnabled(ctx) {
		return false
	}
	if err := h.ensureBackendModeAllowsNewUserLogin(ctx); err != nil {
		log.Printf("[OIDC OAuth] verified-email fast path blocked by backend mode: reason=%s", infraerrors.Reason(err))
		ClearOAuthPendingSessionCookie(c, IsRequestHTTPS(c))
		ClearOAuthPendingBrowserCookie(c, IsRequestHTTPS(c))
		RedirectOAuthError(c, frontendCallback, "login_blocked", infraerrors.Reason(err), infraerrors.Message(err))
		return true
	}

	input := identity.OIDCVerifiedEmailIdentity(identityKey, compatEmail, username, upstreamClaims)

	tokenPair, _, err := h.authService.LoginOrRegisterVerifiedEmailOAuthWithSignupCodes(ctx, input, "", "", ReadOAuthPromoCode(c))
	if err != nil {
		log.Printf("[OIDC OAuth] verified-email fast path skipped: reason=%s", infraerrors.Reason(err))
		return false
	}

	fragment := url.Values{}
	fragment.Set("access_token", tokenPair.AccessToken)
	fragment.Set("refresh_token", tokenPair.RefreshToken)
	fragment.Set("expires_in", fmt.Sprintf("%d", tokenPair.ExpiresIn))
	fragment.Set("token_type", "Bearer")
	fragment.Set("redirect", redirectTo)
	ClearOAuthPendingSessionCookie(c, IsRequestHTTPS(c))
	ClearOAuthPendingBrowserCookie(c, IsRequestHTTPS(c))
	RedirectOAuthFragment(c, frontendCallback, fragment)
	return true
}
func (h *OIDCHandler) CreateOIDCChoiceSession(
	c *gin.Context,
	identityKey identity.PendingAuthIdentityKey,
	suggestedEmail string,
	resolvedEmail string,
	redirectTo string,
	browserSessionKey string,
	upstreamClaims map[string]any,
	compatEmail string,
	compatEmailUser *identity.User,
	forceEmailOnSignup bool,
) error {
	draft := identity.PrepareOIDCChoice(identityKey, suggestedEmail, resolvedEmail, redirectTo, browserSessionKey, upstreamClaims, compatEmail, compatEmailUser, forceEmailOnSignup)
	return h.CreateOAuthPendingSession(c, draft)
}
