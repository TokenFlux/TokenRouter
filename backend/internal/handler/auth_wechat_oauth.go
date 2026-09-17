// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	"log/slog"

	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"

	context "context"

	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"

	payment "github.com/TokenFlux/TokenRouter/internal/payment"

	service "github.com/TokenFlux/TokenRouter/internal/service"

	gin "github.com/gin-gonic/gin"

	strings "strings"
)

const (
	wechatOAuthCookiePath         = identityhttp.WechatOAuthCookiePath
	wechatOAuthCookieMaxAgeSec    = identityhttp.WechatOAuthCookieMaxAgeSec
	wechatOAuthStateCookieName    = identityhttp.WechatOAuthStateCookieName
	wechatOAuthRedirectCookieName = identityhttp.WechatOAuthRedirectCookieName
	wechatOAuthIntentCookieName   = identityhttp.WechatOAuthIntentCookieName
	wechatOAuthModeCookieName     = identityhttp.WechatOAuthModeCookieName
	wechatOAuthBindUserCookieName = identityhttp.WechatOAuthBindUserCookieName
	wechatOAuthDefaultRedirectTo  = identityhttp.WechatOAuthDefaultRedirectTo
	wechatOAuthDefaultFrontendCB  = identityhttp.WechatOAuthDefaultFrontendCB
	wechatOAuthProviderKey        = identityhttp.WechatOAuthProviderKey
	wechatOAuthLegacyProviderKey  = identityhttp.WechatOAuthLegacyProviderKey
	wechatPaymentOAuthCookiePath  = paymenthttp.WechatPaymentOAuthCookiePath
	wechatPaymentOAuthStateName   = paymenthttp.WechatPaymentOAuthStateName
	wechatPaymentOAuthRedirect    = paymenthttp.WechatPaymentOAuthRedirect
	wechatPaymentOAuthContextName = paymenthttp.WechatPaymentOAuthContextName
	wechatPaymentOAuthScope       = paymenthttp.WechatPaymentOAuthScope
	wechatPaymentOAuthDefaultTo   = paymenthttp.WechatPaymentOAuthDefaultTo
	wechatPaymentOAuthFrontendCB  = paymenthttp.WechatPaymentOAuthFrontendCB
)

var (
	wechatOAuthAccessTokenURL = provider.DefaultWeChatTokenURL
	wechatOAuthUserInfoURL    = provider.DefaultWeChatUserInfoURL
)

func (h *AuthHandler) WeChatOAuthStart(c *gin.Context) { h.wechatHTTP().WeChatOAuthStart(c) }

func (h *AuthHandler) WeChatOAuthCallback(c *gin.Context) { h.wechatHTTP().WeChatOAuthCallback(c) }

func (h *AuthHandler) WeChatPaymentOAuthStart(c *gin.Context) {
	h.wechatPaymentHTTP().WeChatPaymentOAuthStart(c)
}

func (h *AuthHandler) WeChatPaymentOAuthCallback(c *gin.Context) {
	h.wechatPaymentHTTP().WeChatPaymentOAuthCallback(c)
}

func (h *AuthHandler) wechatPaymentResumeService() *service.PaymentResumeService {
	var legacyKey []byte
	var raw string
	var configured bool
	if h.cfg != nil {
		raw = h.cfg.Totp.EncryptionKey
		configured = h.cfg.Totp.EncryptionKeyConfigured
	}
	key, warning, err := payment.ConfiguredEncryptionKey(raw, configured)
	if h.cfg == nil {
		warning = "payment encryption key not configured — encrypted payment config and resume signing will be unavailable"
	}
	if warning != "" {
		slog.Warn(warning)
	}
	if err == nil {
		legacyKey = []byte(key)
	}
	return service.NewLegacyAwarePaymentResumeService(legacyKey)
}

func (h *AuthHandler) CompleteWeChatOAuthRegistration(c *gin.Context) {
	h.wechatHTTP().CompleteWeChatOAuthRegistration(c)
}

func (h *AuthHandler) wechatOAuthFrontendCallback(ctx context.Context) string {
	if h != nil && h.settingSvc != nil {
		cfg, err := h.settingSvc.GetWeChatConnectOAuthConfig(ctx)
		if err == nil && strings.TrimSpace(cfg.FrontendRedirectURL) != "" {
			return strings.TrimSpace(cfg.FrontendRedirectURL)
		}
	}
	return wechatOAuthDefaultFrontendCB
}

func resolveWeChatOAuthAbsoluteURL(apiBaseURL string, c *gin.Context, callbackPath string) string {
	return identityhttp.ResolveWeChatOAuthAbsoluteURL(apiBaseURL, c, callbackPath)
}

func (h *AuthHandler) resolveWeChatPaymentOAuthCallbackURL(ctx context.Context, c *gin.Context) string {
	apiBaseURL := ""
	if h != nil && h.settingSvc != nil {
		if settings, err := h.settingSvc.GetAllSettings(ctx); err == nil && settings != nil {
			apiBaseURL = strings.TrimSpace(settings.APIBaseURL)
		}
	}
	return resolveWeChatOAuthAbsoluteURL(apiBaseURL, c, "/api/v1/auth/oauth/wechat/payment/callback")
}

// wechatHTTP 维持两次配置读取的独立时机，支付流程仍由旧支付适配负责。
func (h *AuthHandler) wechatHTTP() *identityhttp.WeChatHandler {
	options := identityhttp.WeChatHTTPOptions{FrontendCallback: h.wechatOAuthFrontendCallback}
	if h != nil && h.settingSvc != nil {
		options.LoadConfig = func(ctx context.Context, mode string) (identitycore.WeChatOAuthOptions, error) {
			apiBase := ""
			if v, e := h.settingSvc.GetAllSettings(ctx); e == nil && v != nil {
				apiBase = strings.TrimSpace(v.APIBaseURL)
			}
			v, e := h.settingSvc.GetWeChatConnectOAuthConfig(ctx)
			if e != nil {
				return identitycore.WeChatOAuthOptions{}, e
			}
			return identitycore.WeChatOAuthOptions{Mode: mode, AppID: v.AppIDForMode(mode), AppSecret: v.AppSecretForMode(mode), Scope: v.ScopeForMode(mode), RedirectURI: v.RedirectURL, FrontendCallback: v.FrontendRedirectURL, APIBaseURL: apiBase, OpenEnabled: v.OpenEnabled, MPEnabled: v.MPEnabled}, nil
		}
	}
	return identityhttp.NewWeChatHandler(h.pendingHTTP(), h.oauthBindHTTP(), provider.WeChatClient{TokenURL: wechatOAuthAccessTokenURL, UserInfoURL: wechatOAuthUserInfoURL}, options)
}

// wechatPaymentHTTP 保留测试及旧入口，只委托独立的支付授权适配。
func (h *AuthHandler) wechatPaymentHTTP() *WeChatPaymentHandler {
	return NewWeChatPaymentHandler(WeChatPaymentHTTPOptions{Config: h.wechatHTTP().GetConfig, CallbackURL: h.resolveWeChatPaymentOAuthCallbackURL, Resume: h.wechatPaymentResumeService, TokenURL: wechatOAuthAccessTokenURL})
}
