// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	fmt "fmt"
	http "net/http"
	url "net/url"
	strings "strings"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	oauthpkce "github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

type WeChatHTTPOptions struct {
	LoadConfig       func(context.Context, string) (identity.WeChatOAuthOptions, error)
	FrontendCallback func(context.Context) string
}
type WeChatHandler struct {
	*PendingHandler
	binding       *OAuthBindHandler
	client        identity.WeChatOAuthClient
	wechatOptions WeChatHTTPOptions
}

func NewWeChatHandler(p *PendingHandler, b *OAuthBindHandler, client identity.WeChatOAuthClient, options WeChatHTTPOptions) *WeChatHandler {
	return &WeChatHandler{p, b, client, options}
}
func (h *WeChatHandler) FrontendCallback(ctx context.Context) string {
	if h.wechatOptions.FrontendCallback != nil {
		return h.wechatOptions.FrontendCallback(ctx)
	}
	return WechatOAuthDefaultFrontendCB
}

// GetConfig 在读取动态配置前验证模式，保持 APIBaseURL 读取与提供方设置读取的原顺序。
func (h *WeChatHandler) GetConfig(ctx context.Context, raw string, c *gin.Context) (identity.WeChatOAuthOptions, error) {
	mode, e := ResolveWeChatOAuthMode(raw, c)
	if e != nil {
		return identity.WeChatOAuthOptions{}, e
	}
	if h == nil || h.wechatOptions.LoadConfig == nil {
		return identity.WeChatOAuthOptions{}, infraerrors.ServiceUnavailable("CONFIG_NOT_READY", "wechat oauth settings service not ready")
	}
	cfg, e := h.wechatOptions.LoadConfig(ctx, mode)
	if e != nil {
		return identity.WeChatOAuthOptions{}, e
	}
	if (mode == "mp" && !cfg.MPEnabled) || (mode == "open" && !cfg.OpenEnabled) {
		return identity.WeChatOAuthOptions{}, infraerrors.NotFound("OAUTH_DISABLED", "wechat oauth is disabled")
	}
	cfg.Mode = mode
	cfg.AppID = strings.TrimSpace(cfg.AppID)
	cfg.AppSecret = strings.TrimSpace(cfg.AppSecret)
	cfg.RedirectURI = identity.OAuthFirstNonEmpty(strings.TrimSpace(cfg.RedirectURI), ResolveWeChatOAuthAbsoluteURL(cfg.APIBaseURL, c, "/api/v1/auth/oauth/wechat/callback"))
	cfg.FrontendCallback = identity.OAuthFirstNonEmpty(strings.TrimSpace(cfg.FrontendCallback), WechatOAuthDefaultFrontendCB)
	if mode == "mp" {
		cfg.AuthorizeURL = "https://open.weixin.qq.com/connect/oauth2/authorize"
	} else {
		cfg.AuthorizeURL = "https://open.weixin.qq.com/connect/qrconnect"
	}
	if strings.TrimSpace(cfg.RedirectURI) == "" {
		return identity.WeChatOAuthOptions{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth redirect url not configured")
	}
	return cfg, nil
}

const (
	WechatOAuthCookiePath         = "/api/v1/auth/oauth/wechat"
	WechatOAuthCookieMaxAgeSec    = 10 * 60
	WechatOAuthStateCookieName    = "wechat_oauth_state"
	WechatOAuthRedirectCookieName = "wechat_oauth_redirect"
	WechatOAuthIntentCookieName   = "wechat_oauth_intent"
	WechatOAuthModeCookieName     = "wechat_oauth_mode"
	WechatOAuthBindUserCookieName = "wechat_oauth_bind_user"
	WechatOAuthDefaultRedirectTo  = "/dashboard"
	WechatOAuthDefaultFrontendCB  = "/auth/wechat/callback"
	WechatOAuthProviderKey        = "wechat-main"
	WechatOAuthLegacyProviderKey  = "wechat"

	WechatOAuthIntentLogin      = "login"
	WechatOAuthIntentBind       = "bind_current_user"
	WechatOAuthIntentAdoptEmail = "adopt_existing_user_by_email"
)

// WeChatOAuthStart 建立授权状态与统一 pending 流程所需的短期浏览器 cookie。
func (h *WeChatHandler) WeChatOAuthStart(c *gin.Context) {
	if !h.RequireActionCaptchaForOAuthLoginStart(c) {
		return
	}
	cfg, err := h.GetConfig(c.Request.Context(), c.Query("mode"), c)
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
		redirectTo = WechatOAuthDefaultRedirectTo
	}

	browserSessionKey, err := GenerateOAuthPendingBrowserSession()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BROWSER_SESSION_GEN_FAILED", "failed to generate oauth browser session").WithCause(err))
		return
	}

	intent := NormalizeWeChatOAuthIntent(c.Query("intent"))
	secureCookie := IsRequestHTTPS(c)
	WeChatSetCookie(c, WechatOAuthStateCookieName, EncodeCookieValue(state), WechatOAuthCookieMaxAgeSec, secureCookie)
	WeChatSetCookie(c, WechatOAuthRedirectCookieName, EncodeCookieValue(redirectTo), WechatOAuthCookieMaxAgeSec, secureCookie)
	WeChatSetCookie(c, WechatOAuthIntentCookieName, EncodeCookieValue(intent), WechatOAuthCookieMaxAgeSec, secureCookie)
	WeChatSetCookie(c, WechatOAuthModeCookieName, EncodeCookieValue(cfg.Mode), WechatOAuthCookieMaxAgeSec, secureCookie)
	CaptureOAuthPromoCode(c, secureCookie)
	SetOAuthPendingBrowserCookie(c, browserSessionKey, secureCookie)
	ClearOAuthPendingSessionCookie(c, secureCookie)
	if intent == OauthIntentBindCurrentUser {
		bindCookieValue, err := h.binding.BuildOAuthBindUserCookieFromContext(c)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		WeChatSetCookie(c, WechatOAuthBindUserCookieName, EncodeCookieValue(bindCookieValue), WechatOAuthCookieMaxAgeSec, secureCookie)
	} else {
		WeChatClearCookie(c, WechatOAuthBindUserCookieName, secureCookie)
	}

	authURL, err := BuildWeChatAuthorizeURL(cfg, state)
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BUILD_URL_FAILED", "failed to build oauth authorization url").WithCause(err))
		return
	}

	RespondOAuthStart(c, authURL)
}

// WeChatOAuthCallback 验证授权并解析 openid/unionid，随后进入统一 pending 流程。
func (h *WeChatHandler) WeChatOAuthCallback(c *gin.Context) {
	frontendCallback := h.FrontendCallback(c.Request.Context())

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
		WeChatClearCookie(c, WechatOAuthStateCookieName, secureCookie)
		WeChatClearCookie(c, WechatOAuthRedirectCookieName, secureCookie)
		WeChatClearCookie(c, WechatOAuthIntentCookieName, secureCookie)
		WeChatClearCookie(c, WechatOAuthModeCookieName, secureCookie)
		WeChatClearCookie(c, WechatOAuthBindUserCookieName, secureCookie)
		ClearOAuthPromoCodeCookie(c, secureCookie)
	}()

	expectedState, err := ReadCookieDecoded(c, WechatOAuthStateCookieName)
	if err != nil || expectedState == "" || state != expectedState {
		RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth state", "")
		return
	}

	redirectTo, _ := ReadCookieDecoded(c, WechatOAuthRedirectCookieName)
	redirectTo = SanitizeFrontendRedirectPath(redirectTo)
	if redirectTo == "" {
		redirectTo = WechatOAuthDefaultRedirectTo
	}
	browserSessionKey, _ := ReadOAuthPendingBrowserCookie(c)
	if strings.TrimSpace(browserSessionKey) == "" {
		RedirectOAuthError(c, frontendCallback, "missing_browser_session", "missing oauth browser session", "")
		return
	}

	intent, _ := ReadCookieDecoded(c, WechatOAuthIntentCookieName)
	mode, err := ReadCookieDecoded(c, WechatOAuthModeCookieName)
	if err != nil || strings.TrimSpace(mode) == "" {
		RedirectOAuthError(c, frontendCallback, "invalid_state", "missing oauth mode", "")
		return
	}

	cfg, err := h.GetConfig(c.Request.Context(), mode, c)
	if err != nil {
		RedirectOAuthError(c, frontendCallback, "provider_error", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}

	tokenResp, userInfo, err := h.client.FetchIdentity(c.Request.Context(), cfg, code)
	if err != nil {
		RedirectOAuthError(c, frontendCallback, "provider_error", "wechat_identity_fetch_failed", OAuthSingleLine(err.Error()))
		return
	}

	unionid := strings.TrimSpace(identity.OAuthFirstNonEmpty(userInfo.UnionID, tokenResp.UnionID))
	openid := strings.TrimSpace(identity.OAuthFirstNonEmpty(userInfo.OpenID, tokenResp.OpenID))
	providerSubject := unionid
	if providerSubject == "" {
		if cfg.RequiresUnionID() {
			RedirectOAuthError(c, frontendCallback, "provider_error", "wechat_missing_unionid", "")
			return
		}
		providerSubject = openid
	}
	if providerSubject == "" {
		RedirectOAuthError(c, frontendCallback, "provider_error", "wechat_missing_unionid", "")
		return
	}

	username := identity.OAuthFirstNonEmpty(userInfo.Nickname, identity.WeChatFallbackUsername(providerSubject))
	email := identity.WeChatSyntheticEmail(providerSubject)
	upstreamClaims := map[string]any{
		"email":                  email,
		"username":               username,
		"subject":                providerSubject,
		"openid":                 openid,
		"unionid":                unionid,
		"mode":                   cfg.Mode,
		"channel":                cfg.Mode,
		"channel_app_id":         strings.TrimSpace(cfg.AppID),
		"channel_subject":        openid,
		"suggested_display_name": strings.TrimSpace(userInfo.Nickname),
		"suggested_avatar_url":   strings.TrimSpace(userInfo.HeadImgURL),
	}
	identityRef := identity.PendingAuthIdentityKey{
		ProviderType:    "wechat",
		ProviderKey:     WechatOAuthProviderKey,
		ProviderSubject: providerSubject,
	}

	normalizedIntent := NormalizeWeChatOAuthIntent(intent)
	if normalizedIntent == WechatOAuthIntentBind {
		if err := h.CreateWeChatBindSession(c, cfg, providerSubject, openid, redirectTo, browserSessionKey, upstreamClaims); err != nil {
			switch response.ErrorCode(err) {
			case http.StatusConflict:
				RedirectOAuthError(c, frontendCallback, "ownership_conflict", infraerrors.Reason(err), infraerrors.Message(err))
			case http.StatusUnauthorized, http.StatusForbidden:
				RedirectOAuthError(c, frontendCallback, "auth_required", infraerrors.Reason(err), infraerrors.Message(err))
			default:
				RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
			}
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
	if existingIdentityUser == nil {
		existingIdentityUser, err = h.flow.Database.FindWeChatUserByLegacyOpenID(c.Request.Context(), identityRef, identity.WeChatIdentityChannel{Mode: cfg.Mode, AppID: cfg.AppID}, openid)
		if err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
			return
		}
	}
	if existingIdentityUser != nil {
		if err := h.flow.Database.EnsureWeChatRuntimeIdentityBinding(c.Request.Context(), existingIdentityUser.ID, identityRef, upstreamClaims); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
			return
		}
		if err := h.CreateWeChatSession(c, normalizedIntent, providerSubject, existingIdentityUser.Email, redirectTo, browserSessionKey, upstreamClaims, nil, nil, &existingIdentityUser.ID); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth login", "")
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	if h.isForceEmailOnThirdPartySignup(c.Request.Context()) {
		if err := h.CreateWeChatChoiceSession(
			c,
			identityRef,
			email,
			email,
			redirectTo,
			browserSessionKey,
			upstreamClaims,
			"",
			nil,
			true,
		); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth login", "")
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	if err := h.CreateWeChatChoiceSession(
		c,
		identityRef,
		email,
		email,
		redirectTo,
		browserSessionKey,
		upstreamClaims,
		"",
		nil,
		false,
	); err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth login", "")
		return
	}
	RedirectToFrontendCallback(c, frontendCallback)
}

// CompleteWeChatOAuthRegistration completes a pending WeChat OAuth registration by
// validating the invitation code and consuming the current pending browser session.
// POST /api/v1/auth/oauth/wechat/complete-registration
func (h *WeChatHandler) CompleteWeChatOAuthRegistration(c *gin.Context) {
	var req CompleteWeChatOAuthRequest
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

	tokenPair, user, err := h.authService.LoginOrRegisterOAuthWithTokenPairAndPromoCode(c.Request.Context(), email, username, req.InvitationCode, req.AffCode, PendingOAuthPromoCode(session), "wechat")
	if err != nil {
		response.ErrorFrom(c, err)
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
	if err := h.flow.Database.ApplyAdoption(c.Request.Context(), session, decision, &user.ID); err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("PENDING_AUTH_ADOPTION_APPLY_FAILED", "failed to apply oauth profile adoption").WithCause(err))
		return
	}
	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	if _, err := pendingSvc.ConsumeBrowserSession(c.Request.Context(), sessionToken, browserSessionKey); err != nil {
		ClearOAuthPendingSessionCookie(c, secureCookie)
		ClearOAuthPendingBrowserCookie(c, secureCookie)
		response.ErrorFrom(c, err)
		return
	}
	ClearOAuthPendingSessionCookie(c, secureCookie)
	ClearOAuthPendingBrowserCookie(c, secureCookie)

	c.JSON(http.StatusOK, gin.H{
		"access_token":  tokenPair.AccessToken,
		"refresh_token": tokenPair.RefreshToken,
		"expires_in":    tokenPair.ExpiresIn,
		"token_type":    "Bearer",
	})
}

type CompleteWeChatOAuthRequest struct {
	InvitationCode   string `json:"invitation_code" binding:"required"`
	AffCode          string `json:"aff_code,omitempty"` // 邀请返利码，仅注册新用户时绑定邀请关系。
	AdoptDisplayName *bool  `json:"adopt_display_name,omitempty"`
	AdoptAvatar      *bool  `json:"adopt_avatar,omitempty"`
}

func (h *WeChatHandler) CreateWeChatBindSession(
	c *gin.Context,
	cfg identity.WeChatOAuthOptions,
	providerSubject string,
	channelSubject string,
	redirectTo string,
	browserSessionKey string,
	upstreamClaims map[string]any,
) error {
	currentUser, err := h.binding.ReadTargetUser(c, WechatOAuthBindUserCookieName, h.flow.Database)
	if err != nil {
		return err
	}
	if err := h.flow.Database.EnsureWeChatBindOwnership(c.Request.Context(), currentUser.ID, providerSubject, identity.WeChatIdentityChannel{Mode: cfg.Mode, AppID: cfg.AppID}, channelSubject); err != nil {
		return err
	}
	return h.CreateWeChatSession(
		c,
		WechatOAuthIntentBind,
		providerSubject,
		currentUser.Email,
		redirectTo,
		browserSessionKey,
		upstreamClaims,
		nil,
		nil,
		&currentUser.ID,
	)
}
func ResolveWeChatOAuthMode(rawMode string, c *gin.Context) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(rawMode))
	if mode == "" {
		if IsWeChatBrowserRequest(c) {
			return "mp", nil
		}
		return "open", nil
	}
	if mode != "open" && mode != "mp" {
		return "", infraerrors.BadRequest("INVALID_MODE", "wechat oauth mode must be open or mp")
	}
	return mode, nil
}
func IsWeChatBrowserRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(c.GetHeader("User-Agent"))), "micromessenger")
}
func NormalizeWeChatOAuthIntent(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "login":
		return WechatOAuthIntentLogin
	case "bind", "bind_current_user":
		return WechatOAuthIntentBind
	case "adopt", "adopt_existing_user_by_email":
		return WechatOAuthIntentAdoptEmail
	default:
		return WechatOAuthIntentLogin
	}
}
func BuildWeChatAuthorizeURL(cfg identity.WeChatOAuthOptions, state string) (string, error) {
	u, err := url.Parse(cfg.AuthorizeURL)
	if err != nil {
		return "", fmt.Errorf("parse authorize url: %w", err)
	}
	query := u.Query()
	query.Set("appid", cfg.AppID)
	query.Set("redirect_uri", cfg.RedirectURI)
	query.Set("response_type", "code")
	query.Set("scope", cfg.Scope)
	query.Set("state", state)
	u.RawQuery = query.Encode()
	u.Fragment = "wechat_redirect"
	return u.String(), nil
}
func ResolveWeChatOAuthAbsoluteURL(apiBaseURL string, c *gin.Context, callbackPath string) string {
	callbackPath = strings.TrimSpace(callbackPath)
	if callbackPath == "" {
		return ""
	}

	if raw := strings.TrimSpace(apiBaseURL); raw != "" {
		if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			basePath := strings.TrimRight(parsed.EscapedPath(), "/")
			targetPath := callbackPath
			if basePath != "" && strings.HasSuffix(basePath, "/api/v1") && strings.HasPrefix(callbackPath, "/api/v1") {
				targetPath = basePath + strings.TrimPrefix(callbackPath, "/api/v1")
			} else if basePath != "" {
				targetPath = basePath + callbackPath
			}
			return parsed.Scheme + "://" + parsed.Host + targetPath
		}
	}

	if c == nil || c.Request == nil {
		return ""
	}
	scheme := "http"
	if IsRequestHTTPS(c) {
		scheme = "https"
	}
	host := strings.TrimSpace(c.Request.Host)
	if forwardedHost := strings.TrimSpace(c.GetHeader("X-Forwarded-Host")); forwardedHost != "" {
		host = forwardedHost
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host + callbackPath
}
func WeChatSetCookie(c *gin.Context, name string, value string, maxAgeSec int, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     WechatOAuthCookiePath,
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
func WeChatClearCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     WechatOAuthCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
func (h *WeChatHandler) CreateWeChatSession(
	c *gin.Context,
	intent string,
	providerSubject string,
	email string,
	redirectTo string,
	browserSessionKey string,
	upstreamClaims map[string]any,
	tokenPair *identity.TokenPair,
	authErr error,
	targetUserID *int64,
) error {
	draft, err := identity.PrepareWeChatPending(intent, providerSubject, email, redirectTo, browserSessionKey, upstreamClaims, tokenPair, authErr, targetUserID)
	if err != nil {
		return err
	}
	return h.CreateOAuthPendingSession(c, draft)
}
func (h *WeChatHandler) CreateWeChatChoiceSession(
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
	draft := identity.PrepareWeChatChoice(identityKey, suggestedEmail, resolvedEmail, redirectTo, browserSessionKey, upstreamClaims, compatEmail, compatEmailUser, forceEmailOnSignup)
	return h.CreateOAuthPendingSession(c, draft)
}
