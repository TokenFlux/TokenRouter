// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	"encoding/json"
	http "net/http"
	url "net/url"
	"strconv"
	strings "strings"

	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	payment "github.com/TokenFlux/TokenRouter/internal/payment"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	oauth "github.com/TokenFlux/TokenRouter/internal/pkg/oauth"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// WeChatPaymentHTTPOptions 仅提供支付授权所需的配置和令牌能力，避免构造完整认证图。
type WeChatPaymentHTTPOptions struct {
	Config      func(context.Context, string, *gin.Context) (identitycore.WeChatOAuthOptions, error)
	CallbackURL func(context.Context, *gin.Context) string
	Resume      func() *payment.PaymentResumeService
	TokenURL    string
	Exchange    func(context.Context, identitycore.WeChatOAuthOptions, string) (WeChatPaymentToken, error)
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

	paymentType := NormalizeWeChatPaymentType(c.Query("payment_type"))
	if paymentType == "" {
		response.BadRequest(c, "Invalid payment type")
		return
	}

	state, err := oauth.GenerateState()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_STATE_GEN_FAILED", "failed to generate oauth state").WithCause(err))
		return
	}

	redirectTo := NormalizeWeChatPaymentRedirectPath(identityhttp.SanitizeFrontendRedirectPath(c.Query("redirect")))
	if redirectTo == "" {
		redirectTo = WechatPaymentOAuthDefaultTo
	}
	rawContext, err := EncodeWeChatPaymentOAuthContext(WechatPaymentOAuthContext{
		PaymentType: paymentType,
		Amount:      strings.TrimSpace(c.Query("amount")),
		OrderType:   strings.TrimSpace(c.Query("order_type")),
		PlanID:      ParseWeChatPaymentPlanID(c.Query("plan_id")),
	})
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_CONTEXT_ENCODE_FAILED", "failed to encode oauth context").WithCause(err))
		return
	}

	scope := NormalizeWeChatPaymentScope(c.Query("scope"))
	secureCookie := identityhttp.IsRequestHTTPS(c)
	WechatPaymentSetCookie(c, WechatPaymentOAuthStateName, identityhttp.EncodeCookieValue(state), identityhttp.WechatOAuthCookieMaxAgeSec, secureCookie)
	WechatPaymentSetCookie(c, WechatPaymentOAuthRedirect, identityhttp.EncodeCookieValue(redirectTo), identityhttp.WechatOAuthCookieMaxAgeSec, secureCookie)
	WechatPaymentSetCookie(c, WechatPaymentOAuthContextName, identityhttp.EncodeCookieValue(rawContext), identityhttp.WechatOAuthCookieMaxAgeSec, secureCookie)
	WechatPaymentSetCookie(c, WechatPaymentOAuthScope, identityhttp.EncodeCookieValue(scope), identityhttp.WechatOAuthCookieMaxAgeSec, secureCookie)

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
	frontendCallback := WechatPaymentOAuthFrontendCB

	if providerErr := strings.TrimSpace(c.Query("error")); providerErr != "" {
		identityhttp.RedirectOAuthError(c, frontendCallback, "provider_error", providerErr, c.Query("error_description"))
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" {
		identityhttp.RedirectOAuthError(c, frontendCallback, "missing_params", "missing code/state", "")
		return
	}

	secureCookie := identityhttp.IsRequestHTTPS(c)
	defer func() {
		WechatPaymentClearCookie(c, WechatPaymentOAuthStateName, secureCookie)
		WechatPaymentClearCookie(c, WechatPaymentOAuthRedirect, secureCookie)
		WechatPaymentClearCookie(c, WechatPaymentOAuthContextName, secureCookie)
		WechatPaymentClearCookie(c, WechatPaymentOAuthScope, secureCookie)
	}()

	expectedState, err := identityhttp.ReadCookieDecoded(c, WechatPaymentOAuthStateName)
	if err != nil || expectedState == "" || state != expectedState {
		identityhttp.RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth state", "")
		return
	}

	redirectTo, _ := identityhttp.ReadCookieDecoded(c, WechatPaymentOAuthRedirect)
	redirectTo = NormalizeWeChatPaymentRedirectPath(identityhttp.SanitizeFrontendRedirectPath(redirectTo))
	if redirectTo == "" {
		redirectTo = WechatPaymentOAuthDefaultTo
	}

	rawContext, _ := identityhttp.ReadCookieDecoded(c, WechatPaymentOAuthContextName)
	paymentContext, err := DecodeWeChatPaymentOAuthContext(rawContext)
	if err != nil {
		identityhttp.RedirectOAuthError(c, frontendCallback, "invalid_context", "invalid oauth context", "")
		return
	}
	if paymentContext.PaymentType == "" {
		paymentContext.PaymentType = payment.TypeWxpay
	}

	scope, _ := identityhttp.ReadCookieDecoded(c, WechatPaymentOAuthScope)
	scope = NormalizeWeChatPaymentScope(scope)

	cfg, err := h.options.Config(c.Request.Context(), "mp", c)
	if err != nil {
		identityhttp.RedirectOAuthError(c, frontendCallback, "provider_error", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}
	cfg.RedirectURI = h.options.CallbackURL(c.Request.Context(), c)
	tokenResp, err := h.options.Exchange(c.Request.Context(), cfg, code)
	if err != nil {
		identityhttp.RedirectOAuthError(c, frontendCallback, "token_exchange_failed", "failed to exchange oauth code", err.Error())
		return
	}

	openid := strings.TrimSpace(tokenResp.OpenID)
	if openid == "" {
		identityhttp.RedirectOAuthError(c, frontendCallback, "missing_openid", "missing openid", "")
		return
	}
	if strings.TrimSpace(tokenResp.Scope) != "" {
		scope = strings.TrimSpace(tokenResp.Scope)
	}

	resumeToken, err := h.options.Resume().CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      openid,
		PaymentType: paymentContext.PaymentType,
		Amount:      paymentContext.Amount,
		OrderType:   paymentContext.OrderType,
		PlanID:      paymentContext.PlanID,
		RedirectTo:  redirectTo,
		Scope:       scope,
	})
	if err != nil {
		identityhttp.RedirectOAuthError(c, frontendCallback, "invalid_context", "failed to encode payment resume context", "")
		return
	}

	fragment := url.Values{}
	fragment.Set("wechat_resume_token", resumeToken)
	fragment.Set("redirect", redirectTo)
	identityhttp.RedirectOAuthFragment(c, frontendCallback, fragment)
}

// ClearWeChatPaymentCookies 保持安全登出时原有的支付 Cookie 清理。
func ClearWeChatPaymentCookies(c *gin.Context) {
	secureCookie := identityhttp.IsRequestHTTPS(c)
	WechatPaymentClearCookie(c, WechatPaymentOAuthStateName, secureCookie)
	WechatPaymentClearCookie(c, WechatPaymentOAuthRedirect, secureCookie)
	WechatPaymentClearCookie(c, WechatPaymentOAuthContextName, secureCookie)
	WechatPaymentClearCookie(c, WechatPaymentOAuthScope, secureCookie)
}

type WeChatPaymentToken struct {
	OpenID string
	Scope  string
}

const WechatPaymentOAuthFrontendCB = "/auth/wechat/payment/callback"
const WechatPaymentOAuthDefaultTo = "/purchase"
const WechatPaymentOAuthScope = "wechat_payment_oauth_scope"
const WechatPaymentOAuthContextName = "wechat_payment_oauth_context"
const WechatPaymentOAuthRedirect = "wechat_payment_oauth_redirect"
const WechatPaymentOAuthStateName = "wechat_payment_oauth_state"
const WechatPaymentOAuthCookiePath = "/api/v1/auth/oauth/wechat/payment"

type WechatPaymentOAuthContext struct {
	PaymentType string `json:"payment_type"`
	Amount      string `json:"amount,omitempty"`
	OrderType   string `json:"order_type,omitempty"`
	PlanID      int64  `json:"plan_id,omitempty"`
}

func NormalizeWeChatPaymentType(raw string) string {
	switch strings.TrimSpace(raw) {
	case payment.TypeWxpay, payment.TypeWxpayDirect:
		return strings.TrimSpace(raw)
	default:
		return ""
	}
}
func NormalizeWeChatPaymentScope(raw string) string {
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
func NormalizeWeChatPaymentRedirectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return WechatPaymentOAuthDefaultTo
	}
	if path == "/payment" {
		return "/purchase"
	}
	if strings.HasPrefix(path, "/payment?") {
		return "/purchase" + strings.TrimPrefix(path, "/payment")
	}
	return path
}
func EncodeWeChatPaymentOAuthContext(ctx WechatPaymentOAuthContext) (string, error) {
	data, err := json.Marshal(ctx)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
func DecodeWeChatPaymentOAuthContext(raw string) (WechatPaymentOAuthContext, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return WechatPaymentOAuthContext{}, nil
	}
	var ctx WechatPaymentOAuthContext
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return WechatPaymentOAuthContext{}, err
	}
	return ctx, nil
}
func ParseWeChatPaymentPlanID(raw string) int64 {
	id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return id
}
func WechatPaymentSetCookie(c *gin.Context, name string, value string, maxAgeSec int, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     WechatPaymentOAuthCookiePath,
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
func WechatPaymentClearCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     WechatPaymentOAuthCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
