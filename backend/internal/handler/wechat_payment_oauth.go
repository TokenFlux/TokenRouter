// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	context "context"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	payment "github.com/TokenFlux/TokenRouter/internal/payment"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	oauth "github.com/TokenFlux/TokenRouter/internal/pkg/oauth"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
	http "net/http"
	url "net/url"
	strings "strings"
)

// WeChatPaymentHTTPOptions 仅提供支付授权所需的配置和令牌能力，避免构造完整认证图。
type WeChatPaymentHTTPOptions struct {
	Config      func(context.Context, string, *gin.Context) (identitycore.WeChatOAuthOptions, error)
	CallbackURL func(context.Context, *gin.Context) string
	Resume      func() *service.PaymentResumeService
	TokenURL    string
}
type WeChatPaymentHandler struct{ options WeChatPaymentHTTPOptions }

func NewWeChatPaymentHandler(options WeChatPaymentHTTPOptions) *WeChatPaymentHandler {
	return &WeChatPaymentHandler{options}
}

// WeChatPaymentOAuthStart 建立支付授权状态。
// GET /api/v1/auth/oauth/wechat/payment/start?payment_type=wxpay&redirect=/purchase
func (h *WeChatPaymentHandler) WeChatPaymentOAuthStart(c *gin.Context) {
	cfg, err := h.options.Config(c.Request.Context(), "mp", c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	paymentType := normalizeWeChatPaymentType(c.Query("payment_type"))
	if paymentType == "" {
		response.BadRequest(c, "Invalid payment type")
		return
	}

	state, err := oauth.GenerateState()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_STATE_GEN_FAILED", "failed to generate oauth state").WithCause(err))
		return
	}

	redirectTo := normalizeWeChatPaymentRedirectPath(sanitizeFrontendRedirectPath(c.Query("redirect")))
	if redirectTo == "" {
		redirectTo = wechatPaymentOAuthDefaultTo
	}
	rawContext, err := encodeWeChatPaymentOAuthContext(wechatPaymentOAuthContext{
		PaymentType: paymentType,
		Amount:      strings.TrimSpace(c.Query("amount")),
		OrderType:   strings.TrimSpace(c.Query("order_type")),
		PlanID:      parseWeChatPaymentPlanID(c.Query("plan_id")),
	})
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_CONTEXT_ENCODE_FAILED", "failed to encode oauth context").WithCause(err))
		return
	}

	scope := normalizeWeChatPaymentScope(c.Query("scope"))
	secureCookie := isRequestHTTPS(c)
	wechatPaymentSetCookie(c, wechatPaymentOAuthStateName, encodeCookieValue(state), wechatOAuthCookieMaxAgeSec, secureCookie)
	wechatPaymentSetCookie(c, wechatPaymentOAuthRedirect, encodeCookieValue(redirectTo), wechatOAuthCookieMaxAgeSec, secureCookie)
	wechatPaymentSetCookie(c, wechatPaymentOAuthContextName, encodeCookieValue(rawContext), wechatOAuthCookieMaxAgeSec, secureCookie)
	wechatPaymentSetCookie(c, wechatPaymentOAuthScope, encodeCookieValue(scope), wechatOAuthCookieMaxAgeSec, secureCookie)

	cfg.RedirectURI = h.options.CallbackURL(c.Request.Context(), c)
	cfg.Scope = scope
	authURL, err := identityhttp.BuildWeChatAuthorizeURL(cfg, state)
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BUILD_URL_FAILED", "failed to build oauth authorization url").WithCause(err))
		return
	}

	c.Redirect(http.StatusFound, authURL)
}

// WeChatPaymentOAuthCallback 取得 OpenID 后签发支付续接令牌。
func (h *WeChatPaymentHandler) WeChatPaymentOAuthCallback(c *gin.Context) {
	frontendCallback := wechatPaymentOAuthFrontendCB

	if providerErr := strings.TrimSpace(c.Query("error")); providerErr != "" {
		redirectOAuthError(c, frontendCallback, "provider_error", providerErr, c.Query("error_description"))
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" {
		redirectOAuthError(c, frontendCallback, "missing_params", "missing code/state", "")
		return
	}

	secureCookie := isRequestHTTPS(c)
	defer func() {
		wechatPaymentClearCookie(c, wechatPaymentOAuthStateName, secureCookie)
		wechatPaymentClearCookie(c, wechatPaymentOAuthRedirect, secureCookie)
		wechatPaymentClearCookie(c, wechatPaymentOAuthContextName, secureCookie)
		wechatPaymentClearCookie(c, wechatPaymentOAuthScope, secureCookie)
	}()

	expectedState, err := readCookieDecoded(c, wechatPaymentOAuthStateName)
	if err != nil || expectedState == "" || state != expectedState {
		redirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth state", "")
		return
	}

	redirectTo, _ := readCookieDecoded(c, wechatPaymentOAuthRedirect)
	redirectTo = normalizeWeChatPaymentRedirectPath(sanitizeFrontendRedirectPath(redirectTo))
	if redirectTo == "" {
		redirectTo = wechatPaymentOAuthDefaultTo
	}

	rawContext, _ := readCookieDecoded(c, wechatPaymentOAuthContextName)
	paymentContext, err := decodeWeChatPaymentOAuthContext(rawContext)
	if err != nil {
		redirectOAuthError(c, frontendCallback, "invalid_context", "invalid oauth context", "")
		return
	}
	if paymentContext.PaymentType == "" {
		paymentContext.PaymentType = payment.TypeWxpay
	}

	scope, _ := readCookieDecoded(c, wechatPaymentOAuthScope)
	scope = normalizeWeChatPaymentScope(scope)

	cfg, err := h.options.Config(c.Request.Context(), "mp", c)
	if err != nil {
		redirectOAuthError(c, frontendCallback, "provider_error", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}
	cfg.RedirectURI = h.options.CallbackURL(c.Request.Context(), c)
	tokenResp, err := provider.ExchangeWeChatOAuthCode(c.Request.Context(), provider.WeChatOptions{AppID: cfg.AppID, AppSecret: cfg.AppSecret, TokenURL: h.options.TokenURL}, code)
	if err != nil {
		redirectOAuthError(c, frontendCallback, "token_exchange_failed", "failed to exchange oauth code", err.Error())
		return
	}

	openid := strings.TrimSpace(tokenResp.OpenID)
	if openid == "" {
		redirectOAuthError(c, frontendCallback, "missing_openid", "missing openid", "")
		return
	}
	if strings.TrimSpace(tokenResp.Scope) != "" {
		scope = strings.TrimSpace(tokenResp.Scope)
	}

	resumeToken, err := h.options.Resume().CreateWeChatPaymentResumeToken(service.WeChatPaymentResumeClaims{
		OpenID:      openid,
		PaymentType: paymentContext.PaymentType,
		Amount:      paymentContext.Amount,
		OrderType:   paymentContext.OrderType,
		PlanID:      paymentContext.PlanID,
		RedirectTo:  redirectTo,
		Scope:       scope,
	})
	if err != nil {
		redirectOAuthError(c, frontendCallback, "invalid_context", "failed to encode payment resume context", "")
		return
	}

	fragment := url.Values{}
	fragment.Set("wechat_resume_token", resumeToken)
	fragment.Set("redirect", redirectTo)
	redirectWithFragment(c, frontendCallback, fragment)
}

// ClearWeChatPaymentCookies 保持安全登出时原有的支付 Cookie 清理。
func ClearWeChatPaymentCookies(c *gin.Context) {
	secureCookie := isRequestHTTPS(c)
	wechatPaymentClearCookie(c, wechatPaymentOAuthStateName, secureCookie)
	wechatPaymentClearCookie(c, wechatPaymentOAuthRedirect, secureCookie)
	wechatPaymentClearCookie(c, wechatPaymentOAuthContextName, secureCookie)
	wechatPaymentClearCookie(c, wechatPaymentOAuthScope, secureCookie)
}
