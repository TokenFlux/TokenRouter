package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	identityadapter "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	promotionhttp "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// identity_http.go assembles login, binding, pending-session, and WeChat
// payment authorization handlers. These handlers share identity services and
// settings readers. WeChat payment authorization reuses credential exchange but
// returns payment token data, and DingTalk profile completion runs through the
// application's tracked background tasks.

// identityVerificationDelivery combines verification mail delivery with
// identity challenge generation.
type identityVerificationDelivery struct {
	mail       *notification.Mailer      // verification email delivery
	challenges *identity.EmailChallenges // identity challenge generation
}

// identityHTTP supplies authentication endpoints and WeChat payment
// authorization methods.
type identityHTTP struct {
	*identityhttp.AuthenticationHandler // identity authentication endpoints
	*paymenthttp.WeChatPaymentHandler   // WeChat payment authorization
}

// identityHTTPSettings adapts shared settings for session and OAuth HTTP
// consumers.
type identityHTTPSettings struct {
	*identityAuthSettings                      // shared authentication settings
	*admission.BackendMode                     // backend admission reader
	public                 *site.PublicService // public setting reads
}

// SendNotifyVerification delegates the prepared identity notice to notification
// mail delivery.
func (d identityVerificationDelivery) SendNotifyVerification(ctx context.Context, n identity.NotifyVerificationNotice) error {
	return d.mail.SendNotifyVerification(ctx, n.UserID, n.Email, n.Code, n.Locale, n.SiteName)
}

// providePanelUserHTTP binds user profiles, authentication, verification
// delivery, and email cache.
func providePanelUserHTTP(users *identity.UserService, g *identityAuthGraph, mail *notification.Mailer, cache identity.EmailCache, challenges *identity.EmailChallenges) *identityhttp.UserHandler {
	return identityhttp.NewUserHandler(users, g.Core, identityVerificationDelivery{mail: mail, challenges: challenges}, cache)
}

// providePromotionUserHTTP binds affiliate operations to the user promotion
// handler.
func providePromotionUserHTTP(s *promotion.AffiliateService) *promotionhttp.UserHandler {
	return promotionhttp.NewUserHandler(s)
}

// GenerateVerifyCode delegates verification-code creation to the identity
// challenge service.
func (d identityVerificationDelivery) GenerateVerifyCode() (string, error) {
	return d.challenges.GenerateVerifyCode()
}

// ReadBackendMode prefers backend mode from a successful public settings read.
// Failed or absent public data falls through to the BackendMode runtime reader,
// which interprets its own cached setting.
func (s identityHTTPSettings) ReadBackendMode(ctx context.Context) bool {
	value, err := s.public.GetPublicSettings(ctx)
	if err == nil && value != nil {
		return value.BackendModeEnabled
	}
	// Public settings may be unavailable during a read failure. Consult the
	// backend runtime's own cached rule so session decisions still have a concrete setting source.
	return s.Enabled(ctx)
}

// ForceEmail reports third-party signup enforcement from authentication-source
// defaults. Read errors, absent defaults, or a false flag return false, letting
// pending HTTP apply the current force-email choice.
func (s identityHTTPSettings) ForceEmail(ctx context.Context) bool {
	value, err := s.GetAuthSourceDefaultSettings(ctx)
	// Unreadable source defaults disable this force-email choice. The pending
	// handler receives false rather than a settings error from its boolean callback.
	return err == nil && value != nil && value.ForceEmailOnThirdPartySignup
}

// LinuxDo converts shared OAuth settings into LinuxDo HTTP flow options.
func (s identityHTTPSettings) LinuxDo(ctx context.Context) (identity.LinuxDoOAuthOptions, error) {
	value, err := s.oauth.GetLinuxDoConnectOAuthConfig(ctx)
	return identity.LinuxDoOAuthOptions(value), err
}

// OIDC converts shared OAuth settings into OIDC HTTP flow options.
func (s identityHTTPSettings) OIDC(ctx context.Context) (identity.OIDCOAuthOptions, error) {
	value, err := s.oauth.GetOIDCConnectOAuthConfig(ctx)
	return identity.OIDCOAuthOptions(value), err
}

// Email converts the named OAuth email provider settings into flow options.
func (s identityHTTPSettings) Email(ctx context.Context, providerName string) (identity.EmailOAuthOptions, error) {
	value, err := s.oauth.GetEmailOAuthProviderConfig(ctx, providerName)
	return identity.EmailOAuthOptions(value), err
}

// DingTalk converts shared OAuth settings into DingTalk HTTP flow options.
func (s identityHTTPSettings) DingTalk(ctx context.Context) (identity.DingTalkOAuthOptions, error) {
	value, err := s.oauth.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkOAuthOptions(value), err
}

// GoogleOneTap returns client identity and frontend redirect with the OAuth
// read error.
func (s identityHTTPSettings) GoogleOneTap(ctx context.Context) (identityhttp.GoogleOneTapOptions, error) {
	value, err := s.oauth.GetGoogleOneTapConfig(ctx)
	return identityhttp.GoogleOneTapOptions{ClientID: value.ClientID, FrontendRedirectURL: value.FrontendRedirectURL}, err
}

// WeChat combines the requested mode with shared WeChat application
// credentials. OAuth read failures return an error; mode-specific AppID,
// secret, and scope come from the settings object's selectors.
func (s identityHTTPSettings) WeChat(ctx context.Context, mode string) (identity.WeChatOAuthOptions, error) {
	value, err := s.oauth.GetWeChatConnectOAuthConfig(ctx)
	if err != nil {
		return identity.WeChatOAuthOptions{}, err
	}
	return identity.WeChatOAuthOptions{
		Mode:             mode,
		AppID:            value.AppIDForMode(mode),
		AppSecret:        value.AppSecretForMode(mode),
		Scope:            value.ScopeForMode(mode),
		RedirectURI:      value.RedirectURL,
		FrontendCallback: value.FrontendRedirectURL,
		OpenEnabled:      value.OpenEnabled,
		MPEnabled:        value.MPEnabled,
	}, nil
}

// WeChatFrontend returns a nonblank trimmed frontend callback from shared OAuth
// settings. Failed reads or blank configured callbacks select the identity HTTP
// default used when completing the WeChat redirect.
func (s identityHTTPSettings) WeChatFrontend(ctx context.Context) string {
	value, err := s.oauth.GetWeChatConnectOAuthConfig(ctx)
	if err == nil && strings.TrimSpace(value.FrontendRedirectURL) != "" {
		return strings.TrimSpace(value.FrontendRedirectURL)
	}
	// A failed or blank callback setting uses the flow's default frontend
	// destination, allowing WeChat completion to return to the configured default page.
	return identityhttp.WechatOAuthDefaultFrontendCB
}

// provideIdentityHTTP assembles session, pending, binding, and provider
// handlers around the shared graph. cfg, settings, tasks, and g are required;
// logout captures pending before it is assigned but is invoked after assembly,
// and DingTalk completion uses tracked profile tasks. WeChat payment callbacks
// use the same credential reader with payment-only token conversion.
func provideIdentityHTTP(
	g *identityAuthGraph,
	users *identity.UserService,
	cfg *config.Config,
	settings *identityAuthSettings,
	backend *admission.BackendMode,
	public *site.PublicService,
	promo *promotion.PromoService,
	redeems *billing.RedeemService,
	totp *identity.TotpService,
	attributes *identity.UserAttributeService,
	tasks *lifecycle.Tasks,
	payments *payment.Runtime,
) *identityHTTP {
	runtime := identityHTTPSettings{identityAuthSettings: settings, BackendMode: backend, public: public}
	flow := &identity.PendingFlow{Store: identitypostgres.NewPendingRepository(g.Client, time.Now), Database: &identitypostgres.PendingFlowDatabase{Client: g.Client, Auth: g.Core, Profiles: users}, Auth: g.Core, Profiles: users}
	var pending *identityhttp.PendingHandler
	session := identityhttp.NewSessionHandler(g.Core, users, runtime, redeems, totp, flow, identityhttp.SessionHTTPOptions{
		BackendMode: runtime.ReadBackendMode, AuditActor: middleware.SetAuditActor,
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
	bind := identityhttp.NewOAuthBindHandler(session, identity.NewOAuthBindingSigner(strings.TrimSpace(cfg.Auth.JWT.Secret)))
	clients := &identityadapter.DingTalkClients{}
	syncer := &identity.DingTalkSyncRuntime{LoadConfig: runtime.DingTalk, Client: func(v identity.DingTalkOAuthOptions) identity.DingTalkOAuthClient {
		return clients.ForConfig(identityadapter.DingTalkClientConfig{ClientID: v.ClientID, ClientSecret: v.ClientSecret, TokenURL: v.TokenURL, UserInfoURL: v.UserInfoURL})
	}, Profiles: &identity.DingTalkProfileSync{Users: users, Attributes: attributes, Observe: identityProfileObserve}, Run: tasks.Go, Observe: identityProfileObserve}
	pending = identityhttp.NewPendingHandler(session, flow, identityhttp.PendingHTTPOptions{ForceEmailOnSignup: runtime.ForceEmail, AfterLogin: func(ctx context.Context, p *identity.PendingAuthSession, id int64) { syncer.Pending(ctx, p, id, false) }, AfterRegistration: func(ctx context.Context, p *identity.PendingAuthSession, id int64) { syncer.Pending(ctx, p, id, true) }})
	wechat := identityhttp.NewWeChatHandler(pending, bind, identityadapter.WeChatClient{TokenURL: identityadapter.DefaultWeChatTokenURL, UserInfoURL: identityadapter.DefaultWeChatUserInfoURL}, identityhttp.WeChatHTTPOptions{LoadConfig: runtime.WeChat, FrontendCallback: runtime.WeChatFrontend})
	auth := &identityhttp.AuthenticationHandler{
		Session: session, Pending: pending, Bind: bind,
		LinuxDo: identityhttp.NewLinuxDoHandler(pending, bind, identityadapter.LinuxDoClient{}, runtime.LinuxDo),
		OIDC:    identityhttp.NewOIDCHandler(pending, bind, identityadapter.OIDCClient{}, runtime.OIDC),
		Email:   identityhttp.NewEmailOAuthHandler(pending, identityadapter.EmailOAuthClientAdapter{}, runtime.Email),
		Google:  identityhttp.NewGoogleOneTapHandler(pending, identityadapter.GoogleAPIIDTokenVerifier{}, identityhttp.GoogleOneTapHTTPOptions{LoadConfig: runtime.GoogleOneTap, RegistrationEnabled: settings.IsRegistrationEnabled}),
		WeChat:  wechat, DingTalk: identityhttp.NewDingTalkHandler(pending, bind, syncer, identityhttp.DingTalkHTTPOptions{LoadConfig: runtime.DingTalk, RegistrationEnabled: settings.IsRegistrationEnabled}),
	}
	pay := paymenthttp.NewWeChatPaymentHandler(paymenthttp.WeChatPaymentHTTPOptions{
		Config: wechat.GetConfig, CallbackURL: func(_ context.Context, c *gin.Context) string {
			return identityhttp.ResolveWeChatOAuthAbsoluteURL(c, "/api/v1/auth/oauth/wechat/payment/callback")
		}, Exchange: func(ctx context.Context, cfg identity.WeChatOAuthOptions, code string) (paymenthttp.WeChatPaymentToken, error) {
			token, err := identityadapter.ExchangeWeChatOAuthCode(ctx, identityadapter.WeChatOptions{AppID: cfg.AppID, AppSecret: cfg.AppSecret, TokenURL: identityadapter.DefaultWeChatTokenURL}, code)
			if err != nil {
				return paymenthttp.WeChatPaymentToken{}, err
			}
			return paymenthttp.WeChatPaymentToken{OpenID: token.OpenID, Scope: token.Scope}, nil
		}, Resume: func() *payment.PaymentResumeService { return payments.ResumeService() },
	})
	return &identityHTTP{auth, pay}
}

// identityProfileObserve writes profile diagnostics using error, debug, or
// warning levels. Any other level selects info, and the supplied arguments are
// passed unchanged to the selected slog call.
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
