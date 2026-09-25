// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	"github.com/TokenFlux/TokenRouter/internal/site"

	"context"

	"log/slog"

	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/payment"

	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/TokenFlux/TokenRouter/internal/identity"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	"github.com/TokenFlux/TokenRouter/internal/identity/provider"

	"github.com/TokenFlux/TokenRouter/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// identityHTTP 固定新身份 HTTP 和旧支付授权适配，路由保留原 URL 与中间件。
type identityHTTP struct {
	*identityhttp.AuthenticationHandler
	*paymenthttp.WeChatPaymentHandler
}

// identityHTTPSettings 只投影请求时读取的认证设置，保持旧入口的回退和错误语义。
type identityHTTPSettings struct {
	*identityAuthSettings
	*admission.BackendMode
	public    *site.PublicService
	composite *composite.Runtime
}

func (s identityHTTPSettings) ReadBackendMode(ctx context.Context) bool {
	value, err := s.public.GetPublicSettings(ctx)
	if err == nil && value != nil {
		return value.BackendModeEnabled
	}
	return s.Enabled(ctx)
}

func (s identityHTTPSettings) ForceEmail(ctx context.Context) bool {
	value, err := s.GetAuthSourceDefaultSettings(ctx)
	return err == nil && value != nil && value.ForceEmailOnThirdPartySignup
}

func (s identityHTTPSettings) LinuxDo(ctx context.Context) (identity.LinuxDoOAuthOptions, error) {
	value, err := s.oauth.GetLinuxDoConnectOAuthConfig(ctx)
	return identity.LinuxDoOAuthOptions(value), err
}

func (s identityHTTPSettings) OIDC(ctx context.Context) (identity.OIDCOAuthOptions, error) {
	value, err := s.oauth.GetOIDCConnectOAuthConfig(ctx)
	return identity.OIDCOAuthOptions(value), err
}

func (s identityHTTPSettings) Email(ctx context.Context, providerName string) (identity.EmailOAuthOptions, error) {
	value, err := s.oauth.GetEmailOAuthProviderConfig(ctx, providerName)
	return identity.EmailOAuthOptions(value), err
}

func (s identityHTTPSettings) DingTalk(ctx context.Context) (identity.DingTalkOAuthOptions, error) {
	value, err := s.oauth.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkOAuthOptions(value), err
}

func (s identityHTTPSettings) GoogleOneTap(ctx context.Context) (identityhttp.GoogleOneTapOptions, error) {
	value, err := s.oauth.GetGoogleOneTapConfig(ctx)
	return identityhttp.GoogleOneTapOptions{ClientID: value.ClientID, FrontendRedirectURL: value.FrontendRedirectURL}, err
}

func (s identityHTTPSettings) APIBaseURL(ctx context.Context) string {
	value, err := s.composite.GetAllSettings(ctx)
	if err == nil && value != nil {
		return strings.TrimSpace(value.APIBaseURL)
	}
	return ""
}

func (s identityHTTPSettings) WeChat(ctx context.Context, mode string) (identity.WeChatOAuthOptions, error) {
	base := s.APIBaseURL(ctx)
	value, err := s.oauth.GetWeChatConnectOAuthConfig(ctx)
	if err != nil {
		return identity.WeChatOAuthOptions{}, err
	}
	return identity.WeChatOAuthOptions{Mode: mode, AppID: value.AppIDForMode(mode), AppSecret: value.AppSecretForMode(mode), Scope: value.ScopeForMode(mode), RedirectURI: value.RedirectURL, FrontendCallback: value.FrontendRedirectURL, APIBaseURL: base, OpenEnabled: value.OpenEnabled, MPEnabled: value.MPEnabled}, nil
}

func (s identityHTTPSettings) WeChatFrontend(ctx context.Context) string {
	value, err := s.oauth.GetWeChatConnectOAuthConfig(ctx)
	if err == nil && strings.TrimSpace(value.FrontendRedirectURL) != "" {
		return strings.TrimSpace(value.FrontendRedirectURL)
	}
	return identityhttp.WechatOAuthDefaultFrontendCB
}

func provideIdentityHTTP(g *identityAuthGraph, users *identity.UserService, cfg *config.Config, settings *identityAuthSettings, backend *admission.BackendMode, public *site.PublicService, composite *composite.Runtime, promo *promotion.PromoService, redeems *billing.RedeemService, totp *identity.TotpService, attributes *identity.UserAttributeService, tasks *lifecycle.Tasks, payments *payment.Runtime) *identityHTTP {
	runtime := identityHTTPSettings{identityAuthSettings: settings, BackendMode: backend, public: public, composite: composite}
	flow := &identity.PendingFlow{Store: identitypostgres.NewPendingRepository(g.Client, time.Now), Database: &identitypostgres.PendingFlowDatabase{Client: g.Client, Auth: g.Core, Profiles: users}, Auth: g.Core, Profiles: users}
	var pending *identityhttp.PendingHandler
	session := identityhttp.NewSessionHandler(g.Core, users, runtime, redeems, totp, flow, identityhttp.SessionHTTPOptions{
		RunMode: cfg.RunMode, BackendMode: runtime.ReadBackendMode, AuditActor: middleware.SetAuditActor,
		ClearPendingCookies: func(c *gin.Context) {
			secure := identityhttp.IsRequestHTTPS(c)
			identityhttp.ClearOAuthPendingSessionCookie(c, secure)
			identityhttp.ClearOAuthPendingBrowserCookie(c, secure)
		},
		LogoutPending: func(c *gin.Context) {
			pending.ConsumePendingOAuthSessionOnLogout(c)
			identityhttp.ClearOAuthLoginCookies(c)
			paymenthttp.ClearWeChatPaymentCookies(c)
		},
		PreviewPromotion: identityPromotionPreview(promo),
	})
	bind := identityhttp.NewOAuthBindHandler(session, identity.NewOAuthBindingSigner(strings.TrimSpace(cfg.JWT.Secret)))
	clients := &provider.DingTalkClients{}
	syncer := &identity.DingTalkSyncRuntime{LoadConfig: runtime.DingTalk, Client: func(v identity.DingTalkOAuthOptions) identity.DingTalkOAuthClient {
		return clients.ForConfig(provider.DingTalkClientConfig{ClientID: v.ClientID, ClientSecret: v.ClientSecret, TokenURL: v.TokenURL, UserInfoURL: v.UserInfoURL})
	}, Profiles: &identity.DingTalkProfileSync{Users: users, Attributes: attributes, Observe: identityProfileObserve}, Run: tasks.Go, Observe: identityProfileObserve}
	pending = identityhttp.NewPendingHandler(session, flow, identityhttp.PendingHTTPOptions{ForceEmailOnSignup: runtime.ForceEmail, AfterLogin: func(ctx context.Context, p *identity.PendingAuthSession, id int64) { syncer.Pending(ctx, p, id, false) }, AfterRegistration: func(ctx context.Context, p *identity.PendingAuthSession, id int64) { syncer.Pending(ctx, p, id, true) }})
	wechat := identityhttp.NewWeChatHandler(pending, bind, provider.WeChatClient{TokenURL: provider.DefaultWeChatTokenURL, UserInfoURL: provider.DefaultWeChatUserInfoURL}, identityhttp.WeChatHTTPOptions{LoadConfig: runtime.WeChat, FrontendCallback: runtime.WeChatFrontend})
	auth := &identityhttp.AuthenticationHandler{Session: session, Pending: pending, Bind: bind,
		LinuxDo: identityhttp.NewLinuxDoHandler(pending, bind, provider.LinuxDoClient{}, runtime.LinuxDo),
		OIDC:    identityhttp.NewOIDCHandler(pending, bind, provider.OIDCClient{}, runtime.OIDC),
		Email:   identityhttp.NewEmailOAuthHandler(pending, provider.EmailOAuthClientAdapter{}, runtime.Email),
		Google:  identityhttp.NewGoogleOneTapHandler(pending, provider.GoogleAPIIDTokenVerifier{}, identityhttp.GoogleOneTapHTTPOptions{LoadConfig: runtime.GoogleOneTap, RegistrationEnabled: settings.IsRegistrationEnabled}),
		WeChat:  wechat, DingTalk: identityhttp.NewDingTalkHandler(pending, bind, syncer, identityhttp.DingTalkHTTPOptions{LoadConfig: runtime.DingTalk, RegistrationEnabled: settings.IsRegistrationEnabled}),
	}
	pay := paymenthttp.NewWeChatPaymentHandler(paymenthttp.WeChatPaymentHTTPOptions{Config: wechat.GetConfig, CallbackURL: func(ctx context.Context, c *gin.Context) string {
		return identityhttp.ResolveWeChatOAuthAbsoluteURL(runtime.APIBaseURL(ctx), c, "/api/v1/auth/oauth/wechat/payment/callback")
	}, Exchange: func(ctx context.Context, cfg identity.WeChatOAuthOptions, code string) (paymenthttp.WeChatPaymentToken, error) {
		token, err := provider.ExchangeWeChatOAuthCode(ctx, provider.WeChatOptions{AppID: cfg.AppID, AppSecret: cfg.AppSecret, TokenURL: provider.DefaultWeChatTokenURL}, code)
		if err != nil {
			return paymenthttp.WeChatPaymentToken{}, err
		}
		return paymenthttp.WeChatPaymentToken{OpenID: token.OpenID, Scope: token.Scope}, nil
	}, Resume: func() *payment.PaymentResumeService { return payments.ResumeService() },
	})
	return &identityHTTP{auth, pay}
}

// identityProfileObserve 保留资料同步原有日志级别，不安装第二个日志后端。
func identityProfileObserve(level, message string, args ...any) {
	switch level {
	case "error":
		slog.Error(message, args...)
	case "debug":
		slog.Debug(message, args...)
	case "warn":
		slog.Warn(message, args...)
	default:
		slog.Info(message, args...)
	}
}
