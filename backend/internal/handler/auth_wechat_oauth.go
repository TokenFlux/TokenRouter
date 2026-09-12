// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	context "context"
	json "encoding/json"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	payment "github.com/TokenFlux/TokenRouter/internal/payment"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
	http "net/http"
	strconv "strconv"
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
	wechatPaymentOAuthCookiePath  = "/api/v1/auth/oauth/wechat/payment"
	wechatPaymentOAuthStateName   = "wechat_payment_oauth_state"
	wechatPaymentOAuthRedirect    = "wechat_payment_oauth_redirect"
	wechatPaymentOAuthContextName = "wechat_payment_oauth_context"
	wechatPaymentOAuthScope       = "wechat_payment_oauth_scope"
	wechatPaymentOAuthDefaultTo   = "/purchase"
	wechatPaymentOAuthFrontendCB  = "/auth/wechat/payment/callback"
)

var (
	wechatOAuthAccessTokenURL = provider.DefaultWeChatTokenURL
	wechatOAuthUserInfoURL    = provider.DefaultWeChatUserInfoURL
)

type wechatPaymentOAuthContext struct {
	PaymentType string `json:"payment_type"`
	Amount      string `json:"amount,omitempty"`
	OrderType   string `json:"order_type,omitempty"`
	PlanID      int64  `json:"plan_id,omitempty"`
}

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
	key, err := payment.ProvideEncryptionKey(h.cfg)
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

func normalizeWeChatPaymentType(raw string) string {
	switch strings.TrimSpace(raw) {
	case payment.TypeWxpay, payment.TypeWxpayDirect:
		return strings.TrimSpace(raw)
	default:
		return ""
	}
}

func normalizeWeChatPaymentScope(raw string) string {
	for _, part := range strings.FieldsFunc(strings.TrimSpace(raw), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		switch strings.TrimSpace(part) {
		case "snsapi_userinfo":
			return "snsapi_userinfo"
		case "snsapi_base":
			return "snsapi_base"
		}
	}
	return "snsapi_base"
}

func normalizeWeChatPaymentRedirectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return wechatPaymentOAuthDefaultTo
	}
	if path == "/payment" {
		return "/purchase"
	}
	if strings.HasPrefix(path, "/payment?") {
		return "/purchase" + strings.TrimPrefix(path, "/payment")
	}
	return path
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

func encodeWeChatPaymentOAuthContext(ctx wechatPaymentOAuthContext) (string, error) {
	data, err := json.Marshal(ctx)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeWeChatPaymentOAuthContext(raw string) (wechatPaymentOAuthContext, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return wechatPaymentOAuthContext{}, nil
	}
	var ctx wechatPaymentOAuthContext
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return wechatPaymentOAuthContext{}, err
	}
	return ctx, nil
}

func parseWeChatPaymentPlanID(raw string) int64 {
	id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return id
}

func wechatPaymentSetCookie(c *gin.Context, name string, value string, maxAgeSec int, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     wechatPaymentOAuthCookiePath,
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func wechatPaymentClearCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     wechatPaymentOAuthCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
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
