// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

const (
	oauthPendingBrowserCookiePath = identityhttp.OauthPendingBrowserCookiePath
	oauthPendingBrowserCookieName = identityhttp.OauthPendingBrowserCookieName
	oauthPendingSessionCookiePath = identityhttp.OauthPendingSessionCookiePath
	oauthPendingSessionCookieName = identityhttp.OauthPendingSessionCookieName
	oauthPromoCodeCookieName      = identityhttp.OauthPromoCodeCookieName
	oauthPendingCookieMaxAgeSec   = identityhttp.OauthPendingCookieMaxAgeSec
	oauthPendingChoiceStep        = identityhttp.OauthPendingChoiceStep

	oauthCompletionResponseKey = identityhttp.OauthCompletionResponseKey
	oauthPromoCodeStateKey     = identityhttp.OauthPromoCodeStateKey
)

var pendingOAuthCreateAccountPreCommitHook func(context.Context, *dbent.PendingAuthSession) error

func clearOAuthPendingBrowserCookie(c *gin.Context, secure bool) {
	identityhttp.ClearOAuthPendingBrowserCookie(c, secure)
}

func setOAuthPendingSessionCookie(c *gin.Context, sessionToken string, secure bool) {
	identityhttp.SetOAuthPendingSessionCookie(c, sessionToken, secure)
}

func clearOAuthPendingSessionCookie(c *gin.Context, secure bool) {
	identityhttp.ClearOAuthPendingSessionCookie(c, secure)
}

func pendingOAuthPromoCode(session *dbent.PendingAuthSession) string {
	return identityhttp.PendingOAuthPromoCode(identitypostgres.PendingAuthSessionFromEntity(session))
}

func readCompletionResponse(session map[string]any) (map[string]any, bool) {
	return identityhttp.ReadCompletionResponse(session)
}

func pendingSessionStringValue(values map[string]any, key string) string {
	return identityhttp.PendingSessionStringValue(values, key)
}

func (h *AuthHandler) entClient() *dbent.Client {
	if h == nil || h.authService == nil {
		return nil
	}
	return h.authService.EntClient()
}

func (h *AuthHandler) isForceEmailOnThirdPartySignup(ctx context.Context) bool {
	if h == nil || h.settingSvc == nil {
		return false
	}
	defaults, err := h.settingSvc.GetAuthSourceDefaultSettings(ctx)
	if err != nil || defaults == nil {
		return false
	}
	return defaults.ForceEmailOnThirdPartySignup
}

func (h *AuthHandler) BindLinuxDoOAuthLogin(c *gin.Context) { h.pendingHTTP().BindLinuxDoOAuthLogin(c) }
func (h *AuthHandler) BindOIDCOAuthLogin(c *gin.Context)    { h.pendingHTTP().BindOIDCOAuthLogin(c) }
func (h *AuthHandler) BindWeChatOAuthLogin(c *gin.Context)  { h.pendingHTTP().BindWeChatOAuthLogin(c) }
func (h *AuthHandler) BindPendingOAuthLogin(c *gin.Context) { h.pendingHTTP().BindPendingOAuthLogin(c) }

func (h *AuthHandler) CreateLinuxDoOAuthAccount(c *gin.Context) {
	h.pendingHTTP().CreateLinuxDoOAuthAccount(c)
}

func (h *AuthHandler) CreateOIDCOAuthAccount(c *gin.Context) {
	h.pendingHTTP().CreateOIDCOAuthAccount(c)
}

func (h *AuthHandler) CreateWeChatOAuthAccount(c *gin.Context) {
	h.pendingHTTP().CreateWeChatOAuthAccount(c)
}

func (h *AuthHandler) CreatePendingOAuthAccount(c *gin.Context) {
	h.pendingHTTP().CreatePendingOAuthAccount(c)
}

func (h *AuthHandler) SendPendingOAuthVerifyCode(c *gin.Context) {
	h.pendingHTTP().SendPendingOAuthVerifyCode(c)
}

func resolvePendingOAuthTargetUserID(ctx context.Context, client *dbent.Client, session *dbent.PendingAuthSession) (int64, error) {
	return identitypostgres.ResolvePendingOAuthTargetUserID(ctx, client, session)
}

func applySuggestedProfileToCompletionResponse(payload map[string]any, upstream map[string]any) {
	identityhttp.ApplySuggestedProfileToCompletionResponse(payload, upstream)
}

func (h *AuthHandler) consumePendingOAuthSessionOnLogout(c *gin.Context) {
	h.pendingHTTP().ConsumePendingOAuthSessionOnLogout(c)
}

func clearOAuthLogoutCookies(c *gin.Context) {
	identityhttp.ClearOAuthLoginCookies(c)
	ClearWeChatPaymentCookies(c)
}

func normalizePendingOAuthCompletionResponse(payload map[string]any) map[string]any {
	return identityhttp.NormalizePendingOAuthCompletionResponse(payload)
}

func (h *AuthHandler) ExchangePendingOAuthCompletion(c *gin.Context) {
	h.pendingHTTP().ExchangePendingOAuthCompletion(c)
}

// identityUserCore 归一化旧指针空值，避免 typed-nil 改变可选回调行为。
func identityUserCore(u *service.UserService) *identitycore.UserService {
	if u == nil {
		return nil
	}
	return u.UserService
}

// pendingFlow 只构造过渡投影，无后台状态；完整迁移后由 app 提供唯一实例。
func (h *AuthHandler) pendingFlow() *identitycore.PendingFlow {
	client := h.entClient()
	var auth *identitycore.AuthService
	var profiles *identitycore.UserService
	if h != nil {
		auth = h.authService.IdentityCore()
		profiles = identityUserCore(h.userService)
	}
	return &identitycore.PendingFlow{Store: identitypostgres.NewPendingRepository(client), Database: &identitypostgres.PendingFlowDatabase{Client: client, Auth: auth, Profiles: profiles}, Auth: auth, Profiles: profiles}
}

// pendingHTTP 仅投影未迁构造器；所有 pending HTTP 处理调用所属模块。
func (h *AuthHandler) pendingHTTP() *identityhttp.PendingHandler {
	options := identityhttp.PendingHTTPOptions{ForceEmailOnSignup: h.isForceEmailOnThirdPartySignup,
		AfterLogin: func(ctx context.Context, p *identitycore.PendingAuthSession, id int64) {
			h.maybeSyncDingTalkAfterLogin(ctx, identitypostgres.PendingAuthSessionToEntity(p), id)
		},
		AfterRegistration: func(ctx context.Context, p *identitycore.PendingAuthSession, id int64) {
			h.maybeSyncDingTalkAfterRegistration(ctx, identitypostgres.PendingAuthSessionToEntity(p), id)
		}}
	if pendingOAuthCreateAccountPreCommitHook != nil {
		options.BeforeAccountCommit = func(ctx context.Context, p *identitycore.PendingAuthSession) error {
			return pendingOAuthCreateAccountPreCommitHook(ctx, identitypostgres.PendingAuthSessionToEntity(p))
		}
	}
	return identityhttp.NewPendingHandler(h.sessionHTTP(), h.pendingFlow(), options)
}
