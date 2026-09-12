// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	base64 "encoding/base64"
	errors "errors"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	oauthpkce "github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
	clientip "github.com/TokenFlux/TokenRouter/internal/server/clientip"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
	io "io"
	slog "log/slog"
	http "net/http"
	url "net/url"
	strings "strings"
)

// PendingHTTPOptions 只保留 HTTP 与测试观察，不承载注册事务。
type PendingHTTPOptions struct {
	AfterLogin        func(context.Context, *identity.PendingAuthSession, int64)
	AfterRegistration func(context.Context, *identity.PendingAuthSession, int64)

	ForceEmailOnSignup  func(context.Context) bool
	BeforeAccountCommit func(context.Context, *identity.PendingAuthSession) error
}
type PendingHandler struct {
	*SessionHandler
	flow           *identity.PendingFlow
	pendingOptions PendingHTTPOptions
}

func NewPendingHandler(session *SessionHandler, flow *identity.PendingFlow, options PendingHTTPOptions) *PendingHandler {
	return &PendingHandler{session, flow, options}
}
func (h *PendingHandler) pendingFlow() *identity.PendingFlow { return h.flow }
func (h *PendingHandler) pendingIdentityService() (identity.PendingStore, error) {
	if !h.flow.Available() {
		return nil, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}
	return h.flow.Store, nil
}
func (h *PendingHandler) isForceEmailOnThirdPartySignup(ctx context.Context) bool {
	return h.pendingOptions.ForceEmailOnSignup != nil && h.pendingOptions.ForceEmailOnSignup(ctx)
}
func (h *PendingHandler) ensurePendingOAuthAdoptionDecision(c *gin.Context, id int64, r OauthAdoptionDecisionRequest) (*identity.IdentityAdoptionDecision, error) {
	return h.flow.AdoptionDecision(c.Request.Context(), id, identity.OAuthAdoptionChoice{AdoptDisplayName: r.AdoptDisplayName, AdoptAvatar: r.AdoptAvatar}, true)
}
func (h *PendingHandler) upsertPendingOAuthAdoptionDecision(c *gin.Context, id int64, r OauthAdoptionDecisionRequest) (*identity.IdentityAdoptionDecision, error) {
	return h.flow.AdoptionDecision(c.Request.Context(), id, identity.OAuthAdoptionChoice{AdoptDisplayName: r.AdoptDisplayName, AdoptAvatar: r.AdoptAvatar}, false)
}
func (h *PendingHandler) transitionPendingOAuthAccountToChoiceState(c *gin.Context, p *identity.PendingAuthSession, u *identity.User, email string) (*identity.PendingAuthSession, error) {
	return h.flow.TransitionAccountToChoice(c.Request.Context(), p, u, email)
}
func (h *PendingHandler) shouldSkipPendingOAuthAdoptionPrompt(ctx context.Context, p *identity.PendingAuthSession, state map[string]any) (bool, error) {
	return h.flow.SkipAdoptionPrompt(ctx, p, state)
}

const oauthIntentBindCurrentUser = "bind_current_user"
const linuxDoOAuthDefaultRedirectTo = "/dashboard"
const linuxDoOAuthMaxRedirectLen = 2048
const (
	OauthPendingBrowserCookiePath = "/api/v1/auth/oauth"
	OauthPendingBrowserCookieName = "oauth_pending_browser_session"
	OauthPendingSessionCookiePath = "/api/v1/auth/oauth"
	OauthPendingSessionCookieName = "oauth_pending_session"
	OauthPromoCodeCookieName      = "oauth_promo_code"
	OauthPendingCookieMaxAgeSec   = 10 * 60
	OauthPendingChoiceStep        = identity.OAuthPendingChoiceStep

	OauthCompletionResponseKey = identity.OAuthCompletionResponseKey
	OauthPromoCodeStateKey     = identity.OAuthPromoCodeStateKey
)

type OauthPendingSessionPayload = identity.OAuthPendingDraft

type OauthAdoptionDecisionRequest struct {
	AdoptDisplayName *bool `json:"adopt_display_name,omitempty"`
	AdoptAvatar      *bool `json:"adopt_avatar,omitempty"`
}

type BindPendingOAuthLoginRequest struct {
	Email            string `json:"email" binding:"required,email"`
	Password         string `json:"password" binding:"required"`
	AdoptDisplayName *bool  `json:"adopt_display_name,omitempty"`
	AdoptAvatar      *bool  `json:"adopt_avatar,omitempty"`
}

type CreatePendingOAuthAccountRequest struct {
	Email                 string `json:"email" binding:"required,email"`
	VerifyCode            string `json:"verify_code,omitempty"`
	Password              string `json:"password" binding:"required,min=6"`
	TurnstileToken        string `json:"turnstile_token,omitempty"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket,omitempty"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr,omitempty"`
	InvitationCode        string `json:"invitation_code,omitempty"`
	AffCode               string `json:"aff_code,omitempty"`
	AdoptDisplayName      *bool  `json:"adopt_display_name,omitempty"`
	AdoptAvatar           *bool  `json:"adopt_avatar,omitempty"`
}

type SendPendingOAuthVerifyCodeRequest struct {
	Email                 string `json:"email" binding:"required,email"`
	TurnstileToken        string `json:"turnstile_token,omitempty"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket,omitempty"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr,omitempty"`
	PendingAuthToken      string `json:"pending_auth_token,omitempty"`
	PendingOAuthToken     string `json:"pending_oauth_token,omitempty"`
}

func (r BindPendingOAuthLoginRequest) adoptionDecision() OauthAdoptionDecisionRequest {
	return OauthAdoptionDecisionRequest{
		AdoptDisplayName: r.AdoptDisplayName,
		AdoptAvatar:      r.AdoptAvatar,
	}
}

func GenerateOAuthPendingBrowserSession() (string, error) {
	return oauthpkce.Verifier()
}

func SetOAuthPendingBrowserCookie(c *gin.Context, sessionKey string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     OauthPendingBrowserCookieName,
		Value:    EncodeCookieValue(sessionKey),
		Path:     OauthPendingBrowserCookiePath,
		MaxAge:   OauthPendingCookieMaxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearOAuthPendingBrowserCookie(c *gin.Context, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     OauthPendingBrowserCookieName,
		Value:    "",
		Path:     OauthPendingBrowserCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func ReadOAuthPendingBrowserCookie(c *gin.Context) (string, error) {
	return ReadCookieDecoded(c, OauthPendingBrowserCookieName)
}

func SetOAuthPendingSessionCookie(c *gin.Context, sessionToken string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     OauthPendingSessionCookieName,
		Value:    EncodeCookieValue(sessionToken),
		Path:     OauthPendingSessionCookiePath,
		MaxAge:   OauthPendingCookieMaxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearOAuthPendingSessionCookie(c *gin.Context, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     OauthPendingSessionCookieName,
		Value:    "",
		Path:     OauthPendingSessionCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func ReadOAuthPendingSessionCookie(c *gin.Context) (string, error) {
	return ReadCookieDecoded(c, OauthPendingSessionCookieName)
}

func CaptureOAuthPromoCode(c *gin.Context, secure bool) {
	promoCode := strings.TrimSpace(c.Query("promo_code"))
	if promoCode == "" {
		ClearOAuthPromoCodeCookie(c, secure)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     OauthPromoCodeCookieName,
		Value:    EncodeCookieValue(promoCode),
		Path:     OauthPendingBrowserCookiePath,
		MaxAge:   OauthPendingCookieMaxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearOAuthPromoCodeCookie(c *gin.Context, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     OauthPromoCodeCookieName,
		Value:    "",
		Path:     OauthPendingBrowserCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func ReadOAuthPromoCode(c *gin.Context) string {
	if c == nil {
		return ""
	}
	promoCode, err := ReadCookieDecoded(c, OauthPromoCodeCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(promoCode)
}

func PendingOAuthPromoCode(session *identity.PendingAuthSession) string {
	return identity.PendingOAuthPromoCode(session)
}

func RedirectToFrontendCallback(c *gin.Context, frontendCallback string) {
	u, err := url.Parse(frontendCallback)
	if err != nil {
		c.Redirect(http.StatusFound, linuxDoOAuthDefaultRedirectTo)
		return
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		c.Redirect(http.StatusFound, linuxDoOAuthDefaultRedirectTo)
		return
	}
	u.Fragment = ""
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Redirect(http.StatusFound, u.String())
}

func (h *PendingHandler) CreateOAuthPendingSession(c *gin.Context, payload OauthPendingSessionPayload) error {
	svc, err := h.pendingIdentityService()
	if err != nil {
		return err
	}

	localFlowState := map[string]any{
		OauthCompletionResponseKey: payload.CompletionResponse,
	}
	if promoCode := strings.TrimSpace(identity.OAuthFirstNonEmpty(payload.PromoCode, ReadOAuthPromoCode(c))); promoCode != "" {
		localFlowState[OauthPromoCodeStateKey] = promoCode
	}

	session, err := svc.CreatePendingSession(c.Request.Context(), identity.CreatePendingAuthSessionInput{
		Intent:                 strings.TrimSpace(payload.Intent),
		Identity:               payload.Identity,
		TargetUserID:           payload.TargetUserID,
		ResolvedEmail:          strings.TrimSpace(payload.ResolvedEmail),
		RedirectTo:             strings.TrimSpace(payload.RedirectTo),
		BrowserSessionKey:      strings.TrimSpace(payload.BrowserSessionKey),
		UpstreamIdentityClaims: payload.UpstreamIdentityClaims,
		LocalFlowState:         localFlowState,
	})
	if err != nil {
		slog.Error("pending auth session create failed",
			"intent", strings.TrimSpace(payload.Intent),
			"provider_type", strings.TrimSpace(payload.Identity.ProviderType),
			"provider_key", strings.TrimSpace(payload.Identity.ProviderKey),
			"provider_subject_len", len(strings.TrimSpace(payload.Identity.ProviderSubject)),
			"resolved_email_len", len(strings.TrimSpace(payload.ResolvedEmail)),
			"has_target_user", payload.TargetUserID != nil,
			"error", err.Error())
		return infraerrors.InternalServer("PENDING_AUTH_SESSION_CREATE_FAILED", "failed to create pending auth session").WithCause(err)
	}

	SetOAuthPendingSessionCookie(c, session.SessionToken, IsRequestHTTPS(c))
	return nil
}

func ReadCompletionResponse(session map[string]any) (map[string]any, bool) {
	return identity.ReadCompletionResponse(session)
}

func ClonePendingMap(values map[string]any) map[string]any {
	return identity.ClonePendingMap(values)
}

func MergePendingCompletionResponse(session *identity.PendingAuthSession, overrides map[string]any) map[string]any {
	return identity.MergePendingCompletionResponse(session, overrides)
}

func PendingSessionStringValue(values map[string]any, key string) string {
	return identity.PendingSessionStringValue(values, key)
}

func PendingSessionWantsInvitation(payload map[string]any) bool {
	return identity.PendingSessionWantsInvitation(payload)
}

func PendingSessionRequiresEmailCompletion(payload map[string]any) bool {
	return identity.PendingSessionRequiresEmailCompletion(payload)
}

func PendingSessionRequiresBindLogin(payload map[string]any) bool {
	return identity.PendingSessionRequiresBindLogin(payload)
}

func PendingOAuthCompletionCanIssueTokenPair(session *identity.PendingAuthSession, payload map[string]any) bool {
	return identity.PendingOAuthCompletionCanIssueTokenPair(session, payload)
}

func EnsurePendingOAuthCompleteRegistrationSession(session *identity.PendingAuthSession) error {
	return identity.EnsurePendingOAuthCompleteRegistrationSession(session)
}

func BuildLegacyCompleteRegistrationPendingResponse(
	session *identity.PendingAuthSession,
	forceEmailOnSignup bool,
	emailVerificationRequired bool,
) map[string]any {
	return identity.BuildLegacyCompleteRegistrationPendingResponse(session, forceEmailOnSignup, emailVerificationRequired)
}

func (r OauthAdoptionDecisionRequest) hasDecision() bool {
	return r.AdoptDisplayName != nil || r.AdoptAvatar != nil
}

func BindOptionalOAuthAdoptionDecision(c *gin.Context) (OauthAdoptionDecisionRequest, error) {
	var req OauthAdoptionDecisionRequest
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return req, nil
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return req, nil
		}
		return req, err
	}
	return req, nil
}

func CloneOAuthMetadata(values map[string]any) map[string]any {
	return identity.CloneOAuthMetadata(values)
}

func MergeOAuthMetadata(base map[string]any, overlay map[string]any) map[string]any {
	return identity.MergeOAuthMetadata(base, overlay)
}

func NormalizeAdoptedOAuthDisplayName(value string) string {
	return identity.NormalizeAdoptedOAuthDisplayName(value)
}

func (h *PendingHandler) BindLinuxDoOAuthLogin(c *gin.Context) {
	h.BindPendingLoginForProvider(c, "linuxdo")
}

func (h *PendingHandler) BindOIDCOAuthLogin(c *gin.Context) { h.BindPendingLoginForProvider(c, "oidc") }

func (h *PendingHandler) BindWeChatOAuthLogin(c *gin.Context) {
	h.BindPendingLoginForProvider(c, "wechat")
}

func (h *PendingHandler) BindPendingOAuthLogin(c *gin.Context) { h.BindPendingLoginForProvider(c, "") }

func (h *PendingHandler) CreateLinuxDoOAuthAccount(c *gin.Context) {
	h.CreatePendingAccountForProvider(c, "linuxdo")
}

func (h *PendingHandler) CreateOIDCOAuthAccount(c *gin.Context) {
	h.CreatePendingAccountForProvider(c, "oidc")
}

func (h *PendingHandler) CreateWeChatOAuthAccount(c *gin.Context) {
	h.CreatePendingAccountForProvider(c, "wechat")
}

func (h *PendingHandler) CreatePendingOAuthAccount(c *gin.Context) {
	h.CreatePendingAccountForProvider(c, "")
}

// SendPendingOAuthVerifyCode sends a verification code for a browser-bound
// pending OAuth account-creation flow.
// POST /api/v1/auth/oauth/pending/send-verify-code
func (h *PendingHandler) SendPendingOAuthVerifyCode(c *gin.Context) {
	var req SendPendingOAuthVerifyCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	proof := captchaProof(req.TurnstileToken, req.TencentCaptchaTicket, req.TencentCaptchaRandstr)
	if err := h.authService.VerifyCaptcha(c.Request.Context(), proof, clientip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	_, session, _, err := ReadPendingOAuthBrowserSession(c, h)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := EnsurePendingOAuthCompleteRegistrationSession(session); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if !h.flow.Available() {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready"))
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if existingUser, err := h.flow.Database.FindUserByNormalizedEmail(c.Request.Context(), email); err == nil && existingUser != nil {
		session, err = h.transitionPendingOAuthAccountToChoiceState(c, session, existingUser, email)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		c.JSON(http.StatusOK, BuildPendingOAuthSessionStatusPayload(session))
		return
	} else if err != nil && !errors.Is(err, identity.ErrUserNotFound) {
		response.ErrorFrom(c, err)
		return
	}

	result, err := h.authService.SendPendingOAuthVerifyCode(c.Request.Context(), req.Email, c.GetHeader("Accept-Language"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, SendVerifyCodeResponse{
		Message:   "Verification code sent successfully",
		Countdown: result.Countdown,
	})
}

func ShouldBindPendingOAuthIdentity(session *identity.PendingAuthSession, decision *identity.IdentityAdoptionDecision) bool {
	return identity.ShouldBindPendingOAuthIdentity(session, decision)
}

func ShouldSkipAvatarAdoption(err error) bool { return identity.ShouldSkipAvatarAdoption(err) }

func ApplySuggestedProfileToCompletionResponse(payload map[string]any, upstream map[string]any) {
	identity.ApplySuggestedProfileToCompletionResponse(payload, upstream)
}

func ReadPendingOAuthBrowserSession(c *gin.Context, h *PendingHandler) (identity.PendingStore, *identity.PendingAuthSession, func(), error) {
	secureCookie := IsRequestHTTPS(c)
	clearCookies := func() {
		ClearOAuthPendingSessionCookie(c, secureCookie)
		ClearOAuthPendingBrowserCookie(c, secureCookie)
	}

	sessionToken, err := ReadOAuthPendingSessionCookie(c)
	if err != nil || strings.TrimSpace(sessionToken) == "" {
		clearCookies()
		return nil, nil, clearCookies, identity.ErrPendingAuthSessionNotFound
	}
	browserSessionKey, err := ReadOAuthPendingBrowserCookie(c)
	if err != nil || strings.TrimSpace(browserSessionKey) == "" {
		clearCookies()
		return nil, nil, clearCookies, identity.ErrPendingAuthBrowserMismatch
	}

	svc, err := h.pendingIdentityService()
	if err != nil {
		clearCookies()
		return nil, nil, clearCookies, err
	}

	session, err := svc.GetBrowserSession(c.Request.Context(), sessionToken, browserSessionKey)
	if err != nil {
		clearCookies()
		return nil, nil, clearCookies, err
	}

	return svc, session, clearCookies, nil
}

func (h *PendingHandler) ConsumePendingOAuthSessionOnLogout(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}

	sessionToken, err := ReadOAuthPendingSessionCookie(c)
	if err != nil || strings.TrimSpace(sessionToken) == "" {
		return
	}
	browserSessionKey, err := ReadOAuthPendingBrowserCookie(c)
	if err != nil || strings.TrimSpace(browserSessionKey) == "" {
		return
	}

	svc, err := h.pendingIdentityService()
	if err != nil {
		return
	}
	_, _ = svc.ConsumeBrowserSession(c.Request.Context(), sessionToken, browserSessionKey)
}

func BuildPendingOAuthSessionStatusPayload(session *identity.PendingAuthSession) gin.H {
	completionResponse := NormalizePendingOAuthCompletionResponse(MergePendingCompletionResponse(session, nil))
	payload := gin.H{
		"auth_result": "pending_session",
		"provider":    strings.TrimSpace(session.ProviderType),
		"intent":      strings.TrimSpace(session.Intent),
	}
	for key, value := range completionResponse {
		payload[key] = value
	}
	if email := strings.TrimSpace(session.ResolvedEmail); email != "" {
		payload["email"] = email
	}
	return payload
}

func NormalizePendingOAuthCompletionResponse(payload map[string]any) map[string]any {
	return identity.NormalizePendingOAuthCompletionResponse(payload)
}

func PendingOAuthChoiceCompletionResponse(session *identity.PendingAuthSession, email string) map[string]any {
	return identity.PendingOAuthChoiceCompletionResponse(session, email)
}

func WriteOAuthTokenPairResponse(c *gin.Context, tokenPair *identity.TokenPair) {
	c.JSON(http.StatusOK, gin.H{
		"access_token":  tokenPair.AccessToken,
		"refresh_token": tokenPair.RefreshToken,
		"expires_in":    tokenPair.ExpiresIn,
		"token_type":    "Bearer",
	})
}

func (h *PendingHandler) BindPendingLoginForProvider(c *gin.Context, provider string) {
	var req BindPendingOAuthLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	pendingSvc, session, clearCookies, err := ReadPendingOAuthBrowserSession(c, h)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if strings.TrimSpace(provider) != "" && !strings.EqualFold(strings.TrimSpace(session.ProviderType), provider) {
		response.BadRequest(c, "Pending oauth session provider mismatch")
		return
	}

	user, err := h.authService.ValidatePasswordCredentials(c.Request.Context(), strings.TrimSpace(req.Email), req.Password)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if session.TargetUserID != nil && *session.TargetUserID > 0 && user.ID != *session.TargetUserID {
		response.ErrorFrom(c, infraerrors.Conflict("PENDING_AUTH_TARGET_USER_MISMATCH", "pending oauth session must be completed by the targeted user"))
		return
	}
	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	decision, err := h.ensurePendingOAuthAdoptionDecision(c, session.ID, req.adoptionDecision())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.totpService != nil && h.settingSvc.IsTotpEnabled(c.Request.Context()) && user.TotpEnabled {
		tempToken, err := h.totpService.CreatePendingOAuthBindLoginSession(
			c.Request.Context(),
			user.ID,
			user.Email,
			session.SessionToken,
			session.BrowserSessionKey,
		)
		if err != nil {
			response.InternalError(c, "Failed to create 2FA session")
			return
		}
		response.Success(c, TotpLoginResponse{
			Requires2FA:     true,
			TempToken:       tempToken,
			UserEmailMasked: identity.MaskEmail(user.Email),
		})
		return
	}
	if err := h.flow.Database.ApplyBinding(c.Request.Context(), identity.PendingBinding{Session: session, Decision: decision, OverrideUserID: &user.ID, ForceBind: true, ApplyFirstBindDefaults: true}); err != nil {
		RespondPendingOAuthBindingApplyError(c, err)
		return
	}

	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	// bindPendingOAuthLogin = 绑定已有账户登录，不动 users.username（用户已有自己的名字）
	if h.pendingOptions.AfterLogin != nil {
		h.pendingOptions.AfterLogin(c.Request.Context(), session, user.ID)
	}
	tokenPair, err := h.authService.GenerateTokenPair(c.Request.Context(), user, "")
	if err != nil {
		response.InternalError(c, "Failed to generate token pair")
		return
	}
	if _, err := pendingSvc.ConsumeBrowserSession(c.Request.Context(), session.SessionToken, session.BrowserSessionKey); err != nil {
		clearCookies()
		response.ErrorFrom(c, err)
		return
	}

	clearCookies()
	WriteOAuthTokenPairResponse(c, tokenPair)
}

func RespondPendingOAuthBindingApplyError(c *gin.Context, err error) {
	if code := response.ErrorCode(err); code >= http.StatusBadRequest && code < http.StatusInternalServerError {
		response.ErrorFrom(c, err)
		return
	}
	response.ErrorFrom(c, infraerrors.InternalServer("PENDING_AUTH_BIND_APPLY_FAILED", "failed to bind pending oauth identity").WithCause(err))
}

func (h *PendingHandler) CreatePendingAccountForProvider(c *gin.Context, provider string) {
	var req CreatePendingOAuthAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	_, session, clearCookies, err := ReadPendingOAuthBrowserSession(c, h)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := EnsurePendingOAuthCompleteRegistrationSession(session); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if strings.TrimSpace(provider) != "" && !strings.EqualFold(strings.TrimSpace(session.ProviderType), provider) {
		response.BadRequest(c, "Pending oauth session provider mismatch")
		return
	}

	if !h.flow.Available() {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready"))
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	existingUser, err := h.flow.Database.FindUserByNormalizedEmail(c.Request.Context(), email)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrUserNotFound):
			existingUser = nil
		case response.ErrorCode(err) >= http.StatusBadRequest && response.ErrorCode(err) < http.StatusInternalServerError:
			response.ErrorFrom(c, err)
			return
		default:
			response.ErrorFrom(c, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "service temporarily unavailable"))
			return
		}
	}
	if existingUser != nil {
		session, err = h.transitionPendingOAuthAccountToChoiceState(c, session, existingUser, email)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		c.JSON(http.StatusOK, BuildPendingOAuthSessionStatusPayload(session))
		return
	}
	if err := h.ensureBackendModeAllowsNewUserLogin(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	proof := captchaProof(req.TurnstileToken, req.TencentCaptchaTicket, req.TencentCaptchaRandstr)
	if err := h.authService.VerifyCaptcha(c.Request.Context(), proof, clientip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	tokenPair, user, err := h.authService.RegisterOAuthEmailAccount(
		c.Request.Context(),
		email,
		req.Password,
		strings.TrimSpace(req.VerifyCode),
		strings.TrimSpace(req.InvitationCode),
		strings.TrimSpace(session.ProviderType),
	)
	if err != nil {
		if errors.Is(err, identity.ErrEmailExists) {
			existingUser, lookupErr := h.flow.Database.FindUserByNormalizedEmail(c.Request.Context(), email)
			if lookupErr != nil {
				response.ErrorFrom(c, lookupErr)
				return
			}
			session, err = h.transitionPendingOAuthAccountToChoiceState(c, session, existingUser, email)
			if err != nil {
				response.ErrorFrom(c, err)
				return
			}
			c.JSON(http.StatusOK, BuildPendingOAuthSessionStatusPayload(session))
			return
		}
		response.ErrorFrom(c, err)
		return
	}

	finalization := identity.PendingAccountFinalization{Session: session, User: user, InvitationCode: strings.TrimSpace(req.InvitationCode), AffiliateCode: strings.TrimSpace(req.AffCode), BeforeCommit: h.pendingOptions.BeforeAccountCommit}
	if err := h.flow.FinalizeCreatedAccountWithChoice(c.Request.Context(), finalization, identity.OAuthAdoptionChoice{AdoptDisplayName: req.AdoptDisplayName, AdoptAvatar: req.AdoptAvatar}); err != nil {
		var failure *identity.PendingWriteError
		if errors.As(err, &failure) {
			switch failure.Phase {
			case "binding", "hook":
				RespondPendingOAuthBindingApplyError(c, failure.Cause)
			case "consume":
				clearCookies()
				response.ErrorFrom(c, failure.Cause)
			case "begin", "commit":
				response.ErrorFrom(c, infraerrors.InternalServer("PENDING_AUTH_BIND_APPLY_FAILED", "failed to bind pending oauth identity").WithCause(failure.Cause))
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
	// createPendingOAuthAccount = 注册新账户，需要把钉钉昵称同步到 users.username 作为初始值
	if h.pendingOptions.AfterRegistration != nil {
		h.pendingOptions.AfterRegistration(c.Request.Context(), session, user.ID)
	}
	clearCookies()
	WriteOAuthTokenPairResponse(c, tokenPair)
}

// ExchangePendingOAuthCompletion redeems a pending OAuth browser session into a frontend-safe payload.
// POST /api/v1/auth/oauth/pending/exchange
func (h *PendingHandler) ExchangePendingOAuthCompletion(c *gin.Context) {
	secureCookie := IsRequestHTTPS(c)
	clearCookies := func() {
		ClearOAuthPendingSessionCookie(c, secureCookie)
		ClearOAuthPendingBrowserCookie(c, secureCookie)
	}
	adoptionDecision, err := BindOptionalOAuthAdoptionDecision(c)
	if err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	sessionToken, err := ReadOAuthPendingSessionCookie(c)
	if err != nil || strings.TrimSpace(sessionToken) == "" {
		clearCookies()
		response.ErrorFrom(c, identity.ErrPendingAuthSessionNotFound)
		return
	}
	browserSessionKey, err := ReadOAuthPendingBrowserCookie(c)
	if err != nil || strings.TrimSpace(browserSessionKey) == "" {
		clearCookies()
		response.ErrorFrom(c, identity.ErrPendingAuthBrowserMismatch)
		return
	}

	svc, err := h.pendingIdentityService()
	if err != nil {
		clearCookies()
		response.ErrorFrom(c, err)
		return
	}

	session, err := svc.GetBrowserSession(c.Request.Context(), sessionToken, browserSessionKey)
	if err != nil {
		clearCookies()
		response.ErrorFrom(c, err)
		return
	}

	payload, ok := ReadCompletionResponse(session.LocalFlowState)
	if !ok {
		clearCookies()
		response.ErrorFrom(c, infraerrors.InternalServer("PENDING_AUTH_COMPLETION_INVALID", "pending auth completion payload is invalid"))
		return
	}
	payload = NormalizePendingOAuthCompletionResponse(payload)
	if strings.TrimSpace(session.RedirectTo) != "" {
		if _, exists := payload["redirect"]; !exists {
			payload["redirect"] = session.RedirectTo
		}
	}
	ApplySuggestedProfileToCompletionResponse(payload, session.UpstreamIdentityClaims)

	canIssueTokenPair := PendingOAuthCompletionCanIssueTokenPair(session, payload)
	var loginUser *identity.User
	if canIssueTokenPair {
		loginUser, err = h.userService.GetByID(c.Request.Context(), *session.TargetUserID)
		if err != nil {
			clearCookies()
			response.ErrorFrom(c, err)
			return
		}
		if err := EnsureLoginUserActive(loginUser); err != nil {
			clearCookies()
			response.ErrorFrom(c, err)
			return
		}
		if err := h.ensureBackendModeAllowsUser(c.Request.Context(), loginUser); err != nil {
			clearCookies()
			response.ErrorFrom(c, err)
			return
		}
	}
	skipAdoptionPrompt, err := h.shouldSkipPendingOAuthAdoptionPrompt(c.Request.Context(), session, payload)
	if err != nil {
		clearCookies()
		response.ErrorFrom(c, err)
		return
	}
	if skipAdoptionPrompt {
		delete(payload, "adoption_required")
	}

	if PendingSessionWantsInvitation(payload) {
		if adoptionDecision.hasDecision() {
			decision, err := h.upsertPendingOAuthAdoptionDecision(c, session.ID, adoptionDecision)
			if err != nil {
				response.ErrorFrom(c, err)
				return
			}
			_ = decision
		}
		response.Success(c, payload)
		return
	}
	if PendingSessionRequiresEmailCompletion(payload) {
		response.Success(c, payload)
		return
	}
	if PendingSessionRequiresBindLogin(payload) {
		response.Success(c, payload)
		return
	}
	// ─── 安全修复（账号接管 0day）────────────────────────────────────────────
	// 非终态 session（如 choose_account_action_required）的 TargetUserID 可能来自
	// 攻击者提交的他人邮箱：createPendingOAuthAccount / SendPendingOAuthVerifyCode
	// 发现邮箱已存在时会把本 session 指向该邮箱用户，全程无密码、无邮箱验证码、
	// 无账号所有权证明。若此时带着 adoption decision 继续执行，下方的
	// applyPendingOAuthAdoption 会把本 OAuth identity 直接绑定到 TargetUserID，
	// 攻击者随后再次 OAuth 登录即被系统识别为受害者本人（完整账号接管）。
	// 只有两类 session 允许在此处执行 adoption/binding：
	//   1. canIssueTokenPair == true —— 登录终态，identity 已安全绑定该用户；
	//   2. intent == bind_current_user —— 已登录用户主动发起绑定（绑定目标来自登录态 cookie）。
	// 其余状态一律只返回 payload，不绑定、不消费 session。
	if !canIssueTokenPair && !strings.EqualFold(strings.TrimSpace(session.Intent), oauthIntentBindCurrentUser) {
		response.Success(c, payload)
		return
	}
	if !adoptionDecision.hasDecision() {
		adoptionRequired, _ := payload["adoption_required"].(bool)
		if adoptionRequired {
			response.Success(c, payload)
			return
		}
	}

	decisionReq := adoptionDecision
	if !decisionReq.hasDecision() {
		adoptDisplayName := false
		adoptAvatar := false
		decisionReq = OauthAdoptionDecisionRequest{
			AdoptDisplayName: &adoptDisplayName,
			AdoptAvatar:      &adoptAvatar,
		}
	}

	decision, err := h.ensurePendingOAuthAdoptionDecision(c, session.ID, decisionReq)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.flow.Database.ApplyAdoption(c.Request.Context(), session, decision, session.TargetUserID); err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("PENDING_AUTH_ADOPTION_APPLY_FAILED", "failed to apply oauth profile adoption").WithCause(err))
		return
	}

	if _, err := svc.ConsumeBrowserSession(c.Request.Context(), sessionToken, browserSessionKey); err != nil {
		clearCookies()
		response.ErrorFrom(c, err)
		return
	}

	if canIssueTokenPair {
		tokenPair, err := h.authService.GenerateTokenPair(c.Request.Context(), loginUser, "")
		if err != nil {
			clearCookies()
			response.InternalError(c, "Failed to generate token pair")
			return
		}
		h.authService.RecordSuccessfulLogin(c.Request.Context(), loginUser.ID)
		payload["access_token"] = tokenPair.AccessToken
		payload["refresh_token"] = tokenPair.RefreshToken
		payload["expires_in"] = tokenPair.ExpiresIn
		payload["token_type"] = "Bearer"
	}

	clearCookies()
	response.Success(c, payload)
}

func SanitizeFrontendRedirectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if len(path) > linuxDoOAuthMaxRedirectLen {
		return ""
	}
	// 只允许同源相对路径（避免开放重定向）。
	if !strings.HasPrefix(path, "/") {
		return ""
	}
	if strings.HasPrefix(path, "//") {
		return ""
	}
	if strings.Contains(path, "://") {
		return ""
	}
	if strings.ContainsAny(path, "\r\n") {
		return ""
	}
	return path
}

func IsRequestHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	proto := strings.ToLower(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")))
	return proto == "https"
}

func EncodeCookieValue(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func DecodeCookieValue(value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func ReadCookieDecoded(c *gin.Context, name string) (string, error) {
	ck, err := c.Request.Cookie(name)
	if err != nil {
		return "", err
	}
	return DecodeCookieValue(ck.Value)
}

// CreateEmailRegistrationSession 在生成浏览器随机状态后准备草稿，保持原 cookie 设置时机。
func (h *PendingHandler) CreateEmailRegistrationSession(c *gin.Context, provider, frontendCallback, redirectTo string, profile *identity.EmailOAuthProfile, affCode, promoCode string) error {
	if h == nil || profile == nil {
		return infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}
	browser, e := GenerateOAuthPendingBrowserSession()
	if e != nil {
		return infraerrors.InternalServer("PENDING_AUTH_SESSION_CREATE_FAILED", "failed to create pending auth session").WithCause(e)
	}
	SetOAuthPendingBrowserCookie(c, browser, IsRequestHTTPS(c))
	invitation := h.settingSvc != nil && h.settingSvc.IsInvitationCodeEnabled(c.Request.Context())
	draft := identity.PrepareEmailRegistrationDraft(provider, frontendCallback, redirectTo, browser, profile, affCode, promoCode, invitation)
	return h.CreateOAuthPendingSession(c, draft)
}
