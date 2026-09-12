// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	errors "errors"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	oauthpkce "github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
	slog "log/slog"
	http "net/http"
	url "net/url"
	strings "strings"
)

// DingTalkHTTPOptions 保持配置热读取与注册开关的读取顺序。
type DingTalkHTTPOptions struct {
	LoadConfig          func(context.Context) (identity.DingTalkOAuthOptions, error)
	RegistrationEnabled func(context.Context) bool
}
type DingTalkHandler struct {
	*PendingHandler
	binding         *OAuthBindHandler
	syncer          *identity.DingTalkSyncRuntime
	dingTalkOptions DingTalkHTTPOptions
}

func NewDingTalkHandler(p *PendingHandler, b *OAuthBindHandler, s *identity.DingTalkSyncRuntime, o DingTalkHTTPOptions) *DingTalkHandler {
	return &DingTalkHandler{p, b, s, o}
}
func (h *DingTalkHandler) isDingTalkSignupBlocked(ctx context.Context, cfg identity.DingTalkOAuthOptions) bool {
	if h.dingTalkOptions.RegistrationEnabled == nil {
		return false
	}
	return identity.DingTalkSignupBlocked(cfg, h.dingTalkOptions.RegistrationEnabled(ctx))
}
func (h *DingTalkHandler) findDingTalkCompatEmailUser(ctx context.Context, email string) (*identity.User, error) {
	if !DingTalkLevelThreeEnabled {
		return nil, nil
	}
	return h.flow.Database.FindDingTalkCompatEmailUser(ctx, email)
}

const (
	DingTalkOAuthCookiePath         = "/api/v1/auth/oauth/dingtalk"
	DingTalkOAuthStateCookieName    = "dingtalk_oauth_state"
	DingTalkOAuthRedirectCookie     = "dingtalk_oauth_redirect"
	DingTalkOAuthIntentCookieName   = "dingtalk_oauth_intent"
	DingTalkOAuthBindUserCookieName = "dingtalk_oauth_bind_user"
	DingTalkOAuthCookieMaxAgeSec    = 600 // 10 分钟
	DingTalkOAuthDefaultRedirectTo  = "/dashboard"
	DingTalkOAuthDefaultFrontendCB  = "/auth/dingtalk/callback"

	DingTalkLevelThreeEnabled = true
)

// DingTalkUpstreamRedirect 在 4 步链上游调用失败时记录详细错误日志并跳错误页。
// 把钉钉 errcode/errmsg 写进 backend log + URL fragment，避免被泛 "internal error" 吞掉。
func DingTalkUpstreamRedirect(c *gin.Context, frontendCallback, step string, err error) {
	var apiErr *identity.DingTalkAPIError
	dtCode := ""
	dtMsg := ""
	dtHTTP := 0
	if errors.As(err, &apiErr) {
		dtCode = apiErr.Code
		dtMsg = apiErr.Message
		dtHTTP = apiErr.HTTP
	}
	slog.Error("dingtalk upstream call failed",
		"step", step,
		"dingtalk_code", dtCode,
		"dingtalk_msg", dtMsg,
		"http_status", dtHTTP,
		"error", err.Error(),
	)
	msg := dtMsg
	if strings.TrimSpace(msg) == "" {
		msg = infraerrors.Message(err)
	}
	if strings.TrimSpace(dtCode) != "" {
		msg = "dingtalk[" + dtCode + "] " + msg
	}
	RedirectOAuthError(c, frontendCallback, identity.DingTalkErrorCode(err), msg, "")
}
func SetDingTalkCookie(c *gin.Context, name string, value string, maxAgeSec int, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     DingTalkOAuthCookiePath,
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
func ClearDingTalkCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     DingTalkOAuthCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// DingTalkOAuthStart 启动 DingTalk Connect OAuth 登录流程。
// GET /api/v1/auth/oauth/dingtalk/start?redirect=/dashboard&intent=login
func (h *DingTalkHandler) DingTalkOAuthStart(c *gin.Context) {
	if !h.RequireActionCaptchaForOAuthLoginStart(c) {
		return
	}
	cfg, err := h.dingTalkOptions.LoadConfig(c.Request.Context())
	if err != nil {
		frontendCB := DingTalkOAuthDefaultFrontendCB
		RedirectOAuthError(c, frontendCB, "dingtalk_not_enabled", "", "")
		return
	}

	state, err := oauthpkce.Verifier()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_STATE_GEN_FAILED", "failed to generate oauth state").WithCause(err))
		return
	}

	redirectTo := SanitizeFrontendRedirectPath(c.Query("redirect"))
	if redirectTo == "" {
		redirectTo = DingTalkOAuthDefaultRedirectTo
	}

	browserSessionKey, err := GenerateOAuthPendingBrowserSession()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BROWSER_SESSION_GEN_FAILED", "failed to generate oauth browser session").WithCause(err))
		return
	}

	secureCookie := IsRequestHTTPS(c)
	SetDingTalkCookie(c, DingTalkOAuthStateCookieName, EncodeCookieValue(state), DingTalkOAuthCookieMaxAgeSec, secureCookie)
	SetDingTalkCookie(c, DingTalkOAuthRedirectCookie, EncodeCookieValue(redirectTo), DingTalkOAuthCookieMaxAgeSec, secureCookie)

	intent := NormalizeOAuthIntent(c.Query("intent"))
	SetDingTalkCookie(c, DingTalkOAuthIntentCookieName, EncodeCookieValue(intent), DingTalkOAuthCookieMaxAgeSec, secureCookie)
	CaptureOAuthPromoCode(c, secureCookie)

	SetOAuthPendingBrowserCookie(c, browserSessionKey, secureCookie)
	ClearOAuthPendingSessionCookie(c, secureCookie)

	if intent == OauthIntentBindCurrentUser {
		bindCookieValue, err := h.binding.BuildOAuthBindUserCookieFromContext(c)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		SetDingTalkCookie(c, DingTalkOAuthBindUserCookieName, EncodeCookieValue(bindCookieValue), DingTalkOAuthCookieMaxAgeSec, secureCookie)
	} else {
		ClearDingTalkCookie(c, DingTalkOAuthBindUserCookieName, secureCookie)
	}

	authURL, err := BuildDingTalkAuthorizeURL(cfg, state)
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BUILD_URL_FAILED", "failed to build dingtalk authorization url").WithCause(err))
		return
	}

	RespondOAuthStart(c, authURL)
}

// DingTalkOAuthCallback 处理钉钉授权回调。
// GET /api/v1/auth/oauth/dingtalk/callback?code=...&state=...
func (h *DingTalkHandler) DingTalkOAuthCallback(c *gin.Context) {
	cfg, cfgErr := h.dingTalkOptions.LoadConfig(c.Request.Context())
	if cfgErr != nil {
		response.ErrorFrom(c, cfgErr)
		return
	}

	frontendCallback := strings.TrimSpace(cfg.FrontendRedirectURL)
	if frontendCallback == "" {
		frontendCallback = DingTalkOAuthDefaultFrontendCB
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
		ClearDingTalkCookie(c, DingTalkOAuthStateCookieName, secureCookie)
		ClearDingTalkCookie(c, DingTalkOAuthRedirectCookie, secureCookie)
		ClearDingTalkCookie(c, DingTalkOAuthIntentCookieName, secureCookie)
		ClearOAuthPromoCodeCookie(c, secureCookie)
	}()

	expectedState, err := ReadCookieDecoded(c, DingTalkOAuthStateCookieName)
	if err != nil || state != expectedState {
		RedirectOAuthError(c, frontendCallback, "csrf", "state mismatch", "")
		return
	}
	redirectTo, _ := ReadCookieDecoded(c, DingTalkOAuthRedirectCookie)
	intent, _ := ReadCookieDecoded(c, DingTalkOAuthIntentCookieName)
	intent = NormalizeOAuthIntent(intent)
	browserSessionKey, _ := ReadOAuthPendingBrowserCookie(c)
	if strings.TrimSpace(browserSessionKey) == "" {
		RedirectOAuthError(c, frontendCallback, "missing_browser_session", "missing browser session cookie", "")
		return
	}
	forceEmailOnSignup := h.isForceEmailOnThirdPartySignup(c.Request.Context())

	// ─── 4 步链（Step 1 + Step 2 必须；Step 3/4 按需 + 跨组织降级）───
	client := h.syncer.Client(cfg)
	userToken, err := client.ExchangeCodeForUserToken(c.Request.Context(), code)
	if err != nil {
		DingTalkUpstreamRedirect(c, frontendCallback, "exchange_code", err)
		return
	}

	// D：corp 校验提前到第 1 步之后、第 2 步之前，减少不必要的上游调用
	corpID := strings.TrimSpace(userToken.CorpID)
	if !identity.DingTalkCorpAllowed(cfg, corpID) {
		// 不在 URL 中透传 corpID，避免内部企业标识泄露给前端
		RedirectOAuthError(c, frontendCallback, "corp_rejected", "", "")
		return
	}

	// 第 2 步：必须执行；UnionID 全局唯一，可作为 subject 与合成邮箱种子；nick 是用户在 App 自设的昵称
	unionID, oauthNick, err := client.GetUnionIdByUserToken(c.Request.Context(), userToken.AccessToken)
	if err != nil {
		DingTalkUpstreamRedirect(c, frontendCallback, "get_union_id", err)
		return
	}

	identityKey := identity.PendingAuthIdentityKey{ProviderType: "dingtalk", ProviderKey: "dingtalk", ProviderSubject: unionID}

	// 第 3/4 步调用策略由 policy 决定，与 require_email 解耦。
	// policy=internal_only → 必须成功（硬失败），因为 AppType=internal 已保证用户在应用企业。
	// policy=none / "" → 尝试，失败降级（公网场景跨组织用户属正常预期）。
	// require_email 只影响 Step 3/4 结果后的邮箱处理路径，不影响是否调用。
	var staff *identity.DingTalkProfileSnapshot
	switch cfg.CorpRestrictionPolicy {
	case "internal_only":
		// AppType=internal 已保证用户在应用企业，第 3/4 步必须成功。
		// 失败表示钉钉 OAPI 故障或应用配置错误，应硬失败。
		upstreamUserID, errStep3 := client.GetUserIdByUnionId(c.Request.Context(), unionID)
		if errStep3 != nil {
			DingTalkUpstreamRedirect(c, frontendCallback, "get_user_id", errStep3)
			return
		}
		staffInfo, errStep4 := client.GetStaffInfoByUserId(c.Request.Context(), upstreamUserID)
		if errStep4 != nil {
			DingTalkUpstreamRedirect(c, frontendCallback, "get_staff_info", errStep4)
			return
		}
		staff = staffInfo

	default: // "none" or ""
		// 公网登录，跨组织用户第 3/4 步可能失败（设计预期），尝试调用，失败降级。
		// 即使 require_email=false 也尝试拿 name（用于 upstreamClaims.username），失败就空着。
		upstreamUserID, errStep3 := client.GetUserIdByUnionId(c.Request.Context(), unionID)
		if errStep3 != nil {
			slog.Debug("dingtalk step3 fallback (none/cross-org)",
				"corp_id", corpID, "union_id", unionID, "err", errStep3.Error())
			staff = &identity.DingTalkProfileSnapshot{}
			break
		}
		staffInfo, errStep4 := client.GetStaffInfoByUserId(c.Request.Context(), upstreamUserID)
		if errStep4 != nil {
			slog.Debug("dingtalk step4 fallback (none/cross-org)",
				"corp_id", corpID, "union_id", unionID, "err", errStep4.Error())
			staff = &identity.DingTalkProfileSnapshot{}
			break
		}
		staff = staffInfo
	}

	// nick 来自 OIDC /contact/users/me，优先作为钉钉昵称（user/get.nickname 多数为空）。
	if staff != nil && strings.TrimSpace(oauthNick) != "" {
		staff.Nickname = strings.TrimSpace(oauthNick)
	}

	upstreamClaims := identity.DingTalkUpstreamClaims(staff, unionID, corpID)

	// ─── S1 主动绑定分支（PR-3 才走到这里）───
	if intent == OauthIntentBindCurrentUser {
		targetUserID, err := h.binding.ReadOAuthBindUserIDFromCookie(c, DingTalkOAuthBindUserCookieName)
		if err != nil {
			RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid bind user cookie", "")
			return
		}
		// policy=none 跨组织用户绑定时 staff.Email=""，用合成邮箱占位（用于 audit log，不用于注册）
		bindResolvedEmail := staff.Email
		if bindResolvedEmail == "" {
			bindResolvedEmail = identity.DingTalkSyntheticEmail(unionID)
		}
		if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
			Intent: OauthIntentBindCurrentUser, Identity: identityKey,
			TargetUserID: &targetUserID, ResolvedEmail: bindResolvedEmail,
			RedirectTo: redirectTo, BrowserSessionKey: browserSessionKey,
			UpstreamIdentityClaims: upstreamClaims,
			CompletionResponse:     map[string]any{"redirect": redirectTo},
		}); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
			return
		}
		ClearDingTalkCookie(c, DingTalkOAuthBindUserCookieName, secureCookie)
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	// ─── 第一级：命中 auth_identities ───
	if existing, _ := h.flow.Database.FindOAuthIdentityUser(c.Request.Context(), identityKey); existing != nil {
		// 身份同步：已登录用户，直接同步（user_id 已知）。
		// 异步执行避免上游钉钉接口（GetStaffInfoByUserId / 部门递归）阻塞登录跳转。
		h.syncer.Async(c.Request.Context(), cfg, client, existing.ID, staff, false)
		if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
			Intent: OauthIntentLogin, Identity: identityKey, TargetUserID: &existing.ID,
			ResolvedEmail: existing.Email, RedirectTo: redirectTo, BrowserSessionKey: browserSessionKey,
			UpstreamIdentityClaims: upstreamClaims,
			CompletionResponse:     map[string]any{"redirect": redirectTo},
		}); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	signupBlocked := h.isDingTalkSignupBlocked(c.Request.Context(), cfg)

	// ─── 非命中：require_email=false 走 synthetic email 直接登录 ───
	if !cfg.RequireEmail {
		if signupBlocked {
			// 注册被拦 + 无邮箱可输：唯一出路是绑定已有账户
			if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
				Intent: OauthIntentLogin, Identity: identityKey, TargetUserID: nil,
				ResolvedEmail: "", RedirectTo: redirectTo, BrowserSessionKey: browserSessionKey,
				UpstreamIdentityClaims: upstreamClaims,
				CompletionResponse:     identity.DingTalkBindLoginCompletionResponse(redirectTo),
			}); err != nil {
				RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
				return
			}
			RedirectToFrontendCallback(c, frontendCallback)
			return
		}
		syntheticEmail := identity.DingTalkSyntheticEmail(unionID)
		if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
			Intent: OauthIntentLogin, Identity: identityKey, TargetUserID: nil,
			ResolvedEmail: syntheticEmail, RedirectTo: redirectTo, BrowserSessionKey: browserSessionKey,
			UpstreamIdentityClaims: upstreamClaims,
			CompletionResponse:     map[string]any{"redirect": redirectTo, "synthetic_email": syntheticEmail},
		}); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	// ─── require_email=true 且 staff.Email 空 → 补邮箱（默认）或直接 bind_login（注册被拦时） ───
	if staff.Email == "" {
		completionResponse := map[string]any{
			"step":                      "email_completion",
			"requires_email_completion": true,
			"redirect":                  redirectTo,
		}
		if signupBlocked {
			// 注册被全局关闭且未豁免：跳过补邮箱页，直接进 bind_login 让用户输入已有账户
			completionResponse = identity.DingTalkBindLoginCompletionResponse(redirectTo)
		}
		if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
			Intent: OauthIntentLogin, Identity: identityKey, TargetUserID: nil,
			ResolvedEmail: "", RedirectTo: redirectTo, BrowserSessionKey: browserSessionKey,
			UpstreamIdentityClaims: upstreamClaims,
			CompletionResponse:     completionResponse,
		}); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	// ─── L3/L4 有邮箱：统一 choice pending session ───
	var compatEmailUser *identity.User
	if DingTalkLevelThreeEnabled && staff.Email != "" {
		compatEmailUser, _ = h.findDingTalkCompatEmailUser(c.Request.Context(), staff.Email)
	}
	if err := h.CreateDingTalkChoiceSession(
		c, identityKey, staff.Email, staff.Email,
		redirectTo, browserSessionKey, upstreamClaims,
		staff.Email, compatEmailUser, forceEmailOnSignup,
		signupBlocked,
	); err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}
	RedirectToFrontendCallback(c, frontendCallback)
}

// BuildDingTalkAuthorizeURL 根据配置和 state 构建钉钉 OAuth 授权 URL。
func BuildDingTalkAuthorizeURL(cfg identity.DingTalkOAuthOptions, state string) (string, error) {
	base := strings.TrimSpace(cfg.AuthorizeURL)
	if base == "" {
		return "", infraerrors.InternalServer("DINGTALK_AUTHORIZE_URL_EMPTY", "dingtalk authorize_url not configured")
	}
	redirectURI := strings.TrimSpace(cfg.RedirectURL)
	if redirectURI == "" {
		return "", infraerrors.InternalServer("DINGTALK_REDIRECT_URL_EMPTY", "dingtalk redirect_url not configured")
	}

	u, err := url.Parse(base)
	if err != nil {
		return "", infraerrors.InternalServer("DINGTALK_AUTHORIZE_URL_PARSE_FAILED", "failed to parse dingtalk authorize_url").WithCause(err)
	}

	scopes := strings.TrimSpace(cfg.Scopes)
	if scopes == "" {
		scopes = "openid"
	}

	q := u.Query()
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", scopes)
	q.Set("state", state)
	q.Set("prompt", "consent")
	u.RawQuery = q.Encode()

	return u.String(), nil
}

type CompleteDingTalkOAuthRequest struct {
	InvitationCode   string `json:"invitation_code" binding:"required"`
	AffCode          string `json:"aff_code,omitempty"`
	AdoptDisplayName *bool  `json:"adopt_display_name,omitempty"`
	AdoptAvatar      *bool  `json:"adopt_avatar,omitempty"`
}

// CompleteDingTalkOAuthRegistration 校验邀请码并创建用户，完成待处理的钉钉 OAuth 注册。
// POST /api/v1/auth/oauth/dingtalk/complete-registration
func (h *DingTalkHandler) CompleteDingTalkOAuthRegistration(c *gin.Context) {
	var req CompleteDingTalkOAuthRequest
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
	// E：username 空时退到 email local part（跨组织用户没拿到 staff.Name 也能注册）
	if username == "" {
		if at := strings.Index(email, "@"); at > 0 {
			username = email[:at]
		} else {
			username = email
		}
	}
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
	tokenPair, user, err := h.authService.LoginOrRegisterOAuthWithTokenPairAndPromoCode(c.Request.Context(), email, username, req.InvitationCode, req.AffCode, PendingOAuthPromoCode(session), "dingtalk")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.flow.Database.ApplyAdoptionAndConsume(c.Request.Context(), session, decision, user.ID); err != nil {
		RespondPendingOAuthBindingApplyError(c, err)
		return
	}
	// 新用户注册完成后执行身份同步（user_id 现在已知）。
	// 异步执行避免阻塞 token 响应。
	if completionCfg, cfgErr := h.dingTalkOptions.LoadConfig(c.Request.Context()); cfgErr == nil {
		dtClient := h.syncer.Client(completionCfg)
		claims := session.UpstreamIdentityClaims
		h.syncer.FromClaims(c.Request.Context(), completionCfg, dtClient, user.ID, claims, true)
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

// CreateDingTalkOAuthAccount 从待处理的钉钉 OAuth 会话创建新用户。
// POST /api/v1/auth/oauth/dingtalk/create-account
func (h *DingTalkHandler) CreateDingTalkOAuthAccount(c *gin.Context) {
	h.CreatePendingAccountForProvider(c, "dingtalk")
}

// BindDingTalkOAuthLogin 处理已有账户绑定钉钉 OAuth 登录。
// POST /api/v1/auth/oauth/dingtalk/bind-login
func (h *DingTalkHandler) BindDingTalkOAuthLogin(c *gin.Context) {
	h.BindPendingLoginForProvider(c, "dingtalk")
}
func (h *DingTalkHandler) CreateDingTalkChoiceSession(
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
	signupBlocked bool,
) error {
	draft := identity.PrepareDingTalkChoice(identityKey, suggestedEmail, resolvedEmail, redirectTo, browserSessionKey, upstreamClaims, compatEmail, compatEmailUser, forceEmailOnSignup, signupBlocked)
	return h.CreateOAuthPendingSession(c, draft)
}
