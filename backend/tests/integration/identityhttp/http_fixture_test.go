package identityhttp_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// authHTTPFixture 只保存测试输入和原生端点，所有请求直接进入唯一 HTTP 实现。
type authHTTPFixture struct {
	*identityhttp.AuthenticationHandler
	*paymenthttp.WeChatPaymentHandler
	cfg                   *config.Config
	authDB                *dbent.Client
	authService           *identity.AuthService
	userService           *identity.UserService
	settingSvc            *authSettingsFixture
	promoService          *promotion.PromoService
	redeemService         *billing.RedeemService
	totpService           *identity.TotpService
	userAttributeService  *identity.UserAttributeService
	googleIDTokenVerifier provider.GoogleIDTokenVerifier
}

var pendingOAuthCreateAccountPreCommitHook func(context.Context, *dbent.PendingAuthSession) error
var wechatOAuthAccessTokenURL = provider.DefaultWeChatTokenURL
var wechatOAuthUserInfoURL = provider.DefaultWeChatUserInfoURL

// authBackgroundFixture 的后台工作由测试拥有，数据库释放前先等待已接受操作。
func authBackgroundFixture(t *testing.T) func(string, func()) bool {
	t.Helper()
	tasks := lifecycle.NewTasks()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := tasks.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	return tasks.Go
}
func newAuthHTTPFixture(t *testing.T, input *authHTTPFixture) *authHTTPFixture {
	t.Helper()
	bindAuthHTTPFixture(t, input)
	return input
}

// bindAuthHTTPFixture 只在测试显式替换输入后重新装配，不在请求路径重建图。
func bindAuthHTTPFixture(t *testing.T, h *authHTTPFixture) {
	t.Helper()
	var client *dbent.Client
	if h.authService != nil {
		client = h.authDB
	}
	flow := &identity.PendingFlow{Store: identitypostgres.NewPendingRepository(client), Database: &identitypostgres.PendingFlowDatabase{Client: client, Auth: h.authService, Profiles: h.userService}, Auth: h.authService, Profiles: h.userService}
	var sessionSettings identityhttp.SessionHTTPSettings
	if h.settingSvc != nil {
		sessionSettings = h.settingSvc
	}
	mode := config.RunModeStandard
	secret := ""
	if h.cfg != nil {
		mode = h.cfg.RunMode
		secret = strings.TrimSpace(h.cfg.JWT.Secret)
	}
	var pending *identityhttp.PendingHandler
	session := identityhttp.NewSessionHandler(h.authService, h.userService, sessionSettings, h.redeemService, h.totpService, flow, identityhttp.SessionHTTPOptions{RunMode: mode, AuditActor: middleware.SetAuditActor, BackendMode: func(ctx context.Context) bool {
		if h.settingSvc == nil {
			return false
		}
		v, e := h.settingSvc.public.GetPublicSettings(ctx)
		if e == nil && v != nil {
			return v.BackendModeEnabled
		}
		return h.settingSvc.IsBackendModeEnabled(ctx)
	}, ClearPendingCookies: func(c *gin.Context) {
		secure := identityhttp.IsRequestHTTPS(c)
		identityhttp.ClearOAuthPendingSessionCookie(c, secure)
		identityhttp.ClearOAuthPendingBrowserCookie(c, secure)
	}, LogoutPending: func(c *gin.Context) {
		pending.ConsumePendingOAuthSessionOnLogout(c)
		identityhttp.ClearOAuthLoginCookies(c)
		paymenthttp.ClearWeChatPaymentCookies(c)
	}, PreviewPromotion: func(ctx context.Context, code string) identityhttp.PromotionPreview {
		v := h.promoService.PreviewRegistrationPromotion(ctx, code)
		return identityhttp.PromotionPreview{Valid: v.Valid, BonusAmount: v.BonusAmount, ErrorCode: v.ErrorCode}
	}})
	bind := identityhttp.NewOAuthBindHandler(session, identity.NewOAuthBindingSigner(secret))
	linux := func(ctx context.Context) (identity.LinuxDoOAuthOptions, error) {
		if h.settingSvc != nil {
			v, e := h.settingSvc.oauth.GetLinuxDoConnectOAuthConfig(ctx)
			return identity.LinuxDoOAuthOptions(v), e
		}
		if h.cfg == nil {
			return identity.LinuxDoOAuthOptions{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
		}
		if !h.cfg.LinuxDo.Enabled {
			return identity.LinuxDoOAuthOptions{}, apperror.NotFound("OAUTH_DISABLED", "oauth login is disabled")
		}
		return identity.LinuxDoOAuthOptions(h.cfg.LinuxDo), nil
	}
	oidc := func(ctx context.Context) (identity.OIDCOAuthOptions, error) {
		if h.settingSvc != nil {
			v, e := h.settingSvc.oauth.GetOIDCConnectOAuthConfig(ctx)
			return identity.OIDCOAuthOptions(v), e
		}
		if h.cfg == nil {
			return identity.OIDCOAuthOptions{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
		}
		if !h.cfg.OIDC.Enabled {
			return identity.OIDCOAuthOptions{}, apperror.NotFound("OAUTH_DISABLED", "oauth login is disabled")
		}
		return identity.OIDCOAuthOptions(h.cfg.OIDC), nil
	}
	dingConfig := func(ctx context.Context) (identity.DingTalkOAuthOptions, error) {
		if h.settingSvc != nil {
			v, e := h.settingSvc.oauth.GetDingTalkConnectOAuthConfig(ctx)
			return identity.DingTalkOAuthOptions(v), e
		}
		if h.cfg == nil {
			return identity.DingTalkOAuthOptions{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
		}
		if !h.cfg.DingTalk.Enabled {
			return identity.DingTalkOAuthOptions{}, apperror.NotFound("OAUTH_DISABLED", "dingtalk oauth login is disabled")
		}
		return identity.DingTalkOAuthOptions(h.cfg.DingTalk), nil
	}
	clients := &provider.DingTalkClients{}
	syncer := &identity.DingTalkSyncRuntime{LoadConfig: dingConfig, Client: func(v identity.DingTalkOAuthOptions) identity.DingTalkOAuthClient {
		return clients.ForConfig(provider.DingTalkClientConfig{ClientID: v.ClientID, ClientSecret: v.ClientSecret, TokenURL: v.TokenURL, UserInfoURL: v.UserInfoURL})
	}, Profiles: &identity.DingTalkProfileSync{Users: h.userService, Attributes: h.userAttributeService}, Run: authBackgroundFixture(t)}
	pending = identityhttp.NewPendingHandler(session, flow, identityhttp.PendingHTTPOptions{ForceEmailOnSignup: func(ctx context.Context) bool {
		if h.settingSvc == nil {
			return false
		}
		v, e := h.settingSvc.GetAuthSourceDefaultSettings(ctx)
		return e == nil && v != nil && v.ForceEmailOnThirdPartySignup
	}, AfterLogin: func(ctx context.Context, p *identity.PendingAuthSession, id int64) { syncer.Pending(ctx, p, id, false) }, AfterRegistration: func(ctx context.Context, p *identity.PendingAuthSession, id int64) { syncer.Pending(ctx, p, id, true) }, BeforeAccountCommit: func(ctx context.Context, p *identity.PendingAuthSession) error {
		if pendingOAuthCreateAccountPreCommitHook == nil {
			return nil
		}
		return pendingOAuthCreateAccountPreCommitHook(ctx, identitypostgres.PendingAuthSessionToEntity(p))
	}})
	email := identityhttp.NewEmailOAuthHandler(pending, provider.EmailOAuthClientAdapter{}, func(ctx context.Context, name string) (identity.EmailOAuthOptions, error) {
		if h.settingSvc == nil {
			return identity.EmailOAuthOptions{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
		}
		v, e := h.settingSvc.oauth.GetEmailOAuthProviderConfig(ctx, name)
		return identity.EmailOAuthOptions(v), e
	})
	var registration func(context.Context) bool
	if h.settingSvc != nil {
		registration = h.settingSvc.IsRegistrationEnabled
	}
	google := identityhttp.NewGoogleOneTapHandler(pending, googleVerifierFixture{h}, identityhttp.GoogleOneTapHTTPOptions{RegistrationEnabled: registration, LoadConfig: func(ctx context.Context) (identityhttp.GoogleOneTapOptions, error) {
		if h.settingSvc == nil {
			return identityhttp.GoogleOneTapOptions{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
		}
		v, e := h.settingSvc.oauth.GetGoogleOneTapConfig(ctx)
		return identityhttp.GoogleOneTapOptions{ClientID: v.ClientID, FrontendRedirectURL: v.FrontendRedirectURL}, e
	}})
	apiBase := func(ctx context.Context) string {
		if h.settingSvc == nil {
			return ""
		}
		v, e := h.settingSvc.composite.GetAllSettings(ctx)
		if e != nil || v == nil {
			return ""
		}
		return strings.TrimSpace(v.APIBaseURL)
	}
	wechatOptions := identityhttp.WeChatHTTPOptions{FrontendCallback: func(ctx context.Context) string {
		if h.settingSvc != nil {
			v, e := h.settingSvc.oauth.GetWeChatConnectOAuthConfig(ctx)
			if e == nil && strings.TrimSpace(v.FrontendRedirectURL) != "" {
				return strings.TrimSpace(v.FrontendRedirectURL)
			}
		}
		return identityhttp.WechatOAuthDefaultFrontendCB
	}}
	if h.settingSvc != nil {
		wechatOptions.LoadConfig = func(ctx context.Context, mode string) (identity.WeChatOAuthOptions, error) {
			base := apiBase(ctx)
			v, e := h.settingSvc.oauth.GetWeChatConnectOAuthConfig(ctx)
			if e != nil {
				return identity.WeChatOAuthOptions{}, e
			}
			return identity.WeChatOAuthOptions{Mode: mode, AppID: v.AppIDForMode(mode), AppSecret: v.AppSecretForMode(mode), Scope: v.ScopeForMode(mode), RedirectURI: v.RedirectURL, FrontendCallback: v.FrontendRedirectURL, APIBaseURL: base, OpenEnabled: v.OpenEnabled, MPEnabled: v.MPEnabled}, nil
		}
	}
	wechat := identityhttp.NewWeChatHandler(pending, bind, provider.WeChatClient{TokenURL: wechatOAuthAccessTokenURL, UserInfoURL: wechatOAuthUserInfoURL}, wechatOptions)
	h.AuthenticationHandler = &identityhttp.AuthenticationHandler{Session: session, Pending: pending, Bind: bind, LinuxDo: identityhttp.NewLinuxDoHandler(pending, bind, provider.LinuxDoClient{}, linux), OIDC: identityhttp.NewOIDCHandler(pending, bind, provider.OIDCClient{}, oidc), Email: email, Google: google, WeChat: wechat, DingTalk: identityhttp.NewDingTalkHandler(pending, bind, syncer, identityhttp.DingTalkHTTPOptions{LoadConfig: dingConfig, RegistrationEnabled: registration})}
	h.WeChatPaymentHandler = paymenthttp.NewWeChatPaymentHandler(paymenthttp.WeChatPaymentHTTPOptions{Config: wechat.GetConfig, CallbackURL: func(ctx context.Context, c *gin.Context) string {
		return identityhttp.ResolveWeChatOAuthAbsoluteURL(apiBase(ctx), c, "/api/v1/auth/oauth/wechat/payment/callback")
	}, Resume: h.paymentResume, Exchange: func(ctx context.Context, v identity.WeChatOAuthOptions, code string) (paymenthttp.WeChatPaymentToken, error) {
		value, err := provider.ExchangeWeChatOAuthCode(ctx, provider.WeChatOptions{AppID: v.AppID, AppSecret: v.AppSecret, TokenURL: wechatOAuthAccessTokenURL}, code)
		if err != nil {
			return paymenthttp.WeChatPaymentToken{}, err
		}
		return paymenthttp.WeChatPaymentToken{OpenID: value.OpenID, Scope: value.Scope}, nil
	}})
}

// googleVerifierFixture 只转交测试注入的验证库替身，生产默认仍使用官方验证器。
type googleVerifierFixture struct{ h *authHTTPFixture }

func (v googleVerifierFixture) Verify(ctx context.Context, credential, audience string) (*provider.GoogleIDTokenClaims, error) {
	verifier := v.h.googleIDTokenVerifier
	if verifier == nil {
		verifier = provider.GoogleAPIIDTokenVerifier{}
	}
	return verifier.Verify(ctx, credential, audience)
}

// paymentResume 仅投影原显式及历史测试密钥，算法由 payment 唯一实现。
func (h *authHTTPFixture) paymentResume() *payment.PaymentResumeService {
	var raw string
	var configured bool
	if h.cfg != nil {
		raw = h.cfg.Totp.EncryptionKey
		configured = h.cfg.Totp.EncryptionKeyConfigured
	}
	key, warning, err := payment.ConfiguredEncryptionKey(raw, configured)
	if warning != "" {
		slog.Warn(warning)
	}
	var legacy []byte
	if err == nil {
		legacy = []byte(key)
	}
	signing, fallbacks := payment.ResolvePaymentResumeSigningKeys(os.Getenv("PAYMENT_RESUME_SIGNING_KEY"), legacy)
	return payment.NewPaymentResumeService(signing, fallbacks...)
}
