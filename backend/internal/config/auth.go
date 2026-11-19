package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
)

// auth.go defines JWT, WebAuthn passkey, OAuth/OIDC provider, and API-key
// authentication cache and abuse configuration types, normalizers, and read-only
// validators. Upfront normalization trims secrets, endpoints, and enabled
// WebAuthn origins so validation checks credentials and callback rules without
// mutating fields mid-pass.

const (
	// These values supply WeChat Open-mode and frontend callback defaults while
	// identity defaults normalize stored mode and scope choices for runtime readers.
	defaultWeChatConnectMode             = authconfig.DefaultWeChatConnectMode
	defaultWeChatConnectScopes           = authconfig.DefaultWeChatConnectScopes
	defaultWeChatConnectFrontendRedirect = "/auth/wechat/callback"
)

var (
	// ErrDingTalkV1AppTypeMismatch shares identity validation's sentinel for an
	// app type conflicting with its configured enterprise restriction in enabled
	// process login settings.
	ErrDingTalkV1AppTypeMismatch = authconfig.ErrDingTalkV1AppTypeMismatch

	// ErrDingTalkV4InvalidAppKind shares identity validation's sentinel for an
	// unsupported application kind, allowing facade callers to retain errors.Is identity.
	ErrDingTalkV4InvalidAppKind = authconfig.ErrDingTalkV4InvalidAppKind
)

// AuthConfig groups token, passkey, captcha, and OAuth process inputs for identity construction.
type AuthConfig struct {
	JWT            JWTConfig            `yaml:"jwt"`             // auth jwt settings
	OAuth          OAuthProvidersConfig `yaml:"oauth"`           // auth oauth settings
	RequireCaptcha bool                 `yaml:"require_captcha"` // default mandatory captcha verification flag
	WebAuthn       WebAuthnConfig       `yaml:"webauthn"`        // auth auth.webauthn settings
}

// JWTConfig supplies signing input and access-token and refresh-token lifetimes.
type JWTConfig struct {
	AccessTokenTTLMinutes int `yaml:"access_token_ttl_minutes"` // Access token lifetime in minutes.
	// RefreshTokenTTLDays supplies the refresh-token lifetime in days. Default
	// loading selects thirty days, and validation requires a positive lifetime
	// while warning when the supplied value exceeds ninety days.
	RefreshTokenTTLDays int `yaml:"refresh_token_ttl_days"`
	// Secret is signing input validated during ordinary Load. LoadForBootstrap can
	// return it empty until database bootstrap selects the persisted key, then
	// performs full validation before identity construction.
	Secret string `yaml:"secret"`
}

// WebAuthnConfig defines this deployment's fixed WebAuthn relying-party identity.
// RPID and RPOrigins must not be inferred from untrusted Host or Origin headers.
type WebAuthnConfig struct {
	Enabled bool `yaml:"enabled"` // passkey login availability switch
	// browser relying-party display name
	RPDisplayName string `yaml:"rp_display_name"`
	// RPID is the configured relying-party domain used for passkey checks. Enabled
	// validation normalizes it and requires each accepted RPOrigins host to equal
	// it or be its subdomain.
	RPID string `yaml:"rp_id"`
	// RPOrigins lists configured browser origins for this relying party. Enabled
	// validation permits HTTPS or local loopback HTTP and replaces accepted
	// entries with normalized scheme/host text before identity construction.
	RPOrigins []string `yaml:"rp_origins"`
}

// OAuthProvidersConfig groups per-provider login defaults supplied to database-backed identity settings readers.
type OAuthProvidersConfig struct {
	DingTalk DingTalkOAuthConfig      `yaml:"dingtalk"` // auth oauth dingtalk settings
	GitHub   EmailOAuthProviderConfig `yaml:"github"`   // auth oauth github settings
	Google   EmailOAuthProviderConfig `yaml:"google"`   // auth oauth google settings
	LinuxDo  LinuxDoConnectConfig     `yaml:"linuxdo"`  // auth oauth linuxdo settings
	OIDC     OIDCConnectConfig        `yaml:"oidc"`     // auth oauth oidc settings
	WeChat   WeChatOAuthConfig        `yaml:"wechat"`   // auth oauth wechat settings
}

// LinuxDoConnectConfig aliases the identity module's LinuxDo OAuth deployment
// settings.
type LinuxDoConnectConfig = authconfig.LinuxDoConnectConfig

// OIDCConnectConfig aliases identity settings for issuer discovery and OIDC
// login validation.
type OIDCConnectConfig = authconfig.OIDCConnectConfig

// EmailOAuthProviderConfig configures email-based OAuth login such as GitHub and Google.
type EmailOAuthProviderConfig = authconfig.EmailOAuthProviderConfig

// DingTalkOAuthConfig supplies active DingTalk login, organization, and profile-synchronization defaults.
type DingTalkOAuthConfig struct {
	AppType      string `yaml:"app_type"`      // public or internal application
	AuthorizeURL string `yaml:"authorize_url"` // browser authorization request endpoint
	// BypassRegistration permits closed-registration signup for internal_only
	// organizations. OAuthSettings clears the value when another organization policy
	// is selected before AuthService considers the bypass.
	BypassRegistration    bool   `yaml:"bypass_registration"`     // internal organization registration bypass
	ClientID              string `yaml:"client_id"`               // registered OAuth client identifier
	ClientSecret          string `yaml:"client_secret"`           // OAuth token exchange secret
	CorpRestrictionPolicy string `yaml:"corp_restriction_policy"` // none or internal_only policy
	// DingTalkAppKind requires internal_app for enabled settings. AppType additionally
	// requires internal when CorpRestrictionPolicy is internal_only; OAuthSettings
	// sets that effective AppType before calling ValidateDingTalkConfig.
	DingTalkAppKind     string `yaml:"dingtalk_app_kind"`     // internal_app for enabled login
	Enabled             bool   `yaml:"enabled"`               // provider login enablement flag
	FrontendRedirectURL string `yaml:"frontend_redirect_url"` // frontend flow completion destination
	// InternalCorpID retains an organization identifier for settings readback.
	// DingTalkHandler checks internal_only membership through the application
	// directory; DingTalkCorpAllowed accepts missing or different corpID values.
	InternalCorpID string `yaml:"internal_corp_id"` // configured organization identifier value
	RedirectURL    string `yaml:"redirect_url"`     // registered backend callback URL
	// RequireEmail prevents synthetic-email signup for a new DingTalk identity.
	// DingTalkHandler requests email completion when staff email is missing and signup
	// is allowed; a supplied staff email goes into account selection.
	RequireEmail bool   `yaml:"require_email"` // new identity email requirement
	Scopes       string `yaml:"scopes"`        // requested OAuth scope text
	// SyncCorpEmail, SyncDisplayName, and SyncDept select local attribute updates in
	// DingTalkProfileSync. OAuthSettings clears these flags outside internal_only mode,
	// and the sync service applies the same organization-policy check before writing.
	SyncCorpEmail bool `yaml:"sync_corp_email"` // enterprise email attribute synchronization
	// SyncCorpEmailAttrKey, SyncDisplayNameAttrKey, and SyncDeptAttrKey name the
	// existing local attribute definitions updated during profile sync. Their AttrName
	// partners provide labels when settings administration prepares those definitions.
	SyncCorpEmailAttrKey    string `yaml:"sync_corp_email_attr_key"`    // enterprise email attribute key
	SyncCorpEmailAttrName   string `yaml:"sync_corp_email_attr_name"`   // enterprise email attribute label
	SyncDept                bool   `yaml:"sync_dept"`                   // department path attribute synchronization
	SyncDeptAttrKey         string `yaml:"sync_dept_attr_key"`          // department path attribute key
	SyncDeptAttrName        string `yaml:"sync_dept_attr_name"`         // department path attribute label
	SyncDisplayName         bool   `yaml:"sync_display_name"`           // display name attribute synchronization
	SyncDisplayNameAttrKey  string `yaml:"sync_display_name_attr_key"`  // display name attribute key
	SyncDisplayNameAttrName string `yaml:"sync_display_name_attr_name"` // display name attribute label
	TokenURL                string `yaml:"token_url"`                   // authorization code exchange endpoint
	UserInfoURL             string `yaml:"userinfo_url"`                // authenticated profile request endpoint
}

// WeChatOAuthConfig supplies explicit Open, MP, and Mobile WeChat login defaults.
type WeChatOAuthConfig struct {
	Enabled             bool   `yaml:"enabled"`               // provider login enablement flag
	FrontendRedirectURL string `yaml:"frontend_redirect_url"` // frontend flow completion destination
	MobileAppID         string `yaml:"mobile_app_id"`         // mobile application identifier value
	MobileAppSecret     string `yaml:"mobile_app_secret"`     // mobile application secret value
	MobileEnabled       bool   `yaml:"mobile_enabled"`        // mobile login enablement flag
	Mode                string `yaml:"mode"`                  // selected WeChat login mode
	MPAppID             string `yaml:"mp_app_id"`             // public-account application identifier value
	MPAppSecret         string `yaml:"mp_app_secret"`         // public-account application secret value
	MPEnabled           bool   `yaml:"mp_enabled"`            // public-account login enablement flag
	OpenAppID           string `yaml:"open_app_id"`           // open-platform application identifier value
	OpenAppSecret       string `yaml:"open_app_secret"`       // open-platform application secret value
	OpenEnabled         bool   `yaml:"open_enabled"`          // open-platform login enablement flag
	RedirectURL         string `yaml:"redirect_url"`          // registered backend callback URL
	Scopes              string `yaml:"scopes"`                // requested OAuth scope text
}

// APIKeyConfig groups key prefixes, lookup admission, authentication caches, and invalid-key abuse limits.
type APIKeyConfig struct {
	AuthCache       APIKeyAuthCacheConfig  `yaml:"auth_cache"`        // api key auth cache settings
	InvalidKeyAbuse InvalidAuthAbuseConfig `yaml:"invalid_key_abuse"` // api key invalid key abuse settings
	// authentication lookup admission limit
	LookupConcurrency int `yaml:"lookup_concurrency"`
	// generated API-key text prefix
	Prefix string `yaml:"prefix"`
}

// APIKeyAuthCacheConfig supplies authentication cache lifetimes, lookup
// admission, and invalid-key abuse limits.
type APIKeyAuthCacheConfig struct {
	// entry lifetime jitter percent
	JitterPercent int `yaml:"jitter_percent"`
	// process authentication entry capacity
	L1Size int `yaml:"l1_size"`
	// process authentication retention seconds
	L1TTLSeconds int `yaml:"l1_ttl_seconds"`
	// Redis authentication retention seconds
	L2TTLSeconds int `yaml:"l2_ttl_seconds"`
	// missing-key observation retention seconds
	NegativeTTLSeconds int `yaml:"negative_ttl_seconds"`
	// same-key authentication load coalescing switch
	Singleflight bool `yaml:"singleflight"`
}

// InvalidAuthAbuseConfig supplies process-local repeated invalid authentication
// observation and blocking limits.
type InvalidAuthAbuseConfig struct {
	// invalid-auth observation availability switch
	Enabled   bool `yaml:"enabled"`
	Threshold int  `yaml:"threshold"` // invalid-auth count triggering block
	// invalid-auth observation window seconds
	WindowSeconds int `yaml:"window_seconds"`
	// invalid-auth blocking duration seconds
	BlockSeconds int `yaml:"block_seconds"`
	Capacity     int `yaml:"capacity"` // observed invalid-auth source capacity
}

// normalizeAuth trims JWT, LinuxDo, WeChat, OIDC, and enabled WebAuthn fields
// before validation inspects authentication settings.
func (c *Config) normalizeAuth() {
	c.Auth.JWT.Secret = strings.TrimSpace(c.Auth.JWT.Secret)
	c.Auth.OAuth.LinuxDo.ClientID = strings.TrimSpace(c.Auth.OAuth.LinuxDo.ClientID)
	c.Auth.OAuth.LinuxDo.ClientSecret = strings.TrimSpace(c.Auth.OAuth.LinuxDo.ClientSecret)
	c.Auth.OAuth.LinuxDo.AuthorizeURL = strings.TrimSpace(c.Auth.OAuth.LinuxDo.AuthorizeURL)
	c.Auth.OAuth.LinuxDo.TokenURL = strings.TrimSpace(c.Auth.OAuth.LinuxDo.TokenURL)
	c.Auth.OAuth.LinuxDo.UserInfoURL = strings.TrimSpace(c.Auth.OAuth.LinuxDo.UserInfoURL)
	c.Auth.OAuth.LinuxDo.Scopes = strings.TrimSpace(c.Auth.OAuth.LinuxDo.Scopes)
	c.Auth.OAuth.LinuxDo.RedirectURL = strings.TrimSpace(c.Auth.OAuth.LinuxDo.RedirectURL)
	c.Auth.OAuth.LinuxDo.FrontendRedirectURL = strings.TrimSpace(c.Auth.OAuth.LinuxDo.FrontendRedirectURL)
	c.Auth.OAuth.LinuxDo.TokenAuthMethod = strings.ToLower(strings.TrimSpace(c.Auth.OAuth.LinuxDo.TokenAuthMethod))
	c.Auth.OAuth.LinuxDo.UserInfoEmailPath = strings.TrimSpace(c.Auth.OAuth.LinuxDo.UserInfoEmailPath)
	c.Auth.OAuth.LinuxDo.UserInfoIDPath = strings.TrimSpace(c.Auth.OAuth.LinuxDo.UserInfoIDPath)
	c.Auth.OAuth.LinuxDo.UserInfoUsernamePath = strings.TrimSpace(c.Auth.OAuth.LinuxDo.UserInfoUsernamePath)
	normalizeWeChatOAuthConfig(&c.Auth.OAuth.WeChat)
	c.Auth.OAuth.OIDC.ProviderName = strings.TrimSpace(c.Auth.OAuth.OIDC.ProviderName)
	c.Auth.OAuth.OIDC.ClientID = strings.TrimSpace(c.Auth.OAuth.OIDC.ClientID)
	c.Auth.OAuth.OIDC.ClientSecret = strings.TrimSpace(c.Auth.OAuth.OIDC.ClientSecret)
	c.Auth.OAuth.OIDC.IssuerURL = strings.TrimSpace(c.Auth.OAuth.OIDC.IssuerURL)
	c.Auth.OAuth.OIDC.DiscoveryURL = strings.TrimSpace(c.Auth.OAuth.OIDC.DiscoveryURL)
	c.Auth.OAuth.OIDC.AuthorizeURL = strings.TrimSpace(c.Auth.OAuth.OIDC.AuthorizeURL)
	c.Auth.OAuth.OIDC.TokenURL = strings.TrimSpace(c.Auth.OAuth.OIDC.TokenURL)
	c.Auth.OAuth.OIDC.UserInfoURL = strings.TrimSpace(c.Auth.OAuth.OIDC.UserInfoURL)
	c.Auth.OAuth.OIDC.JWKSURL = strings.TrimSpace(c.Auth.OAuth.OIDC.JWKSURL)
	c.Auth.OAuth.OIDC.Scopes = strings.TrimSpace(c.Auth.OAuth.OIDC.Scopes)
	c.Auth.OAuth.OIDC.RedirectURL = strings.TrimSpace(c.Auth.OAuth.OIDC.RedirectURL)
	c.Auth.OAuth.OIDC.FrontendRedirectURL = strings.TrimSpace(c.Auth.OAuth.OIDC.FrontendRedirectURL)
	c.Auth.OAuth.OIDC.TokenAuthMethod = strings.ToLower(strings.TrimSpace(c.Auth.OAuth.OIDC.TokenAuthMethod))
	c.Auth.OAuth.OIDC.AllowedSigningAlgs = strings.TrimSpace(c.Auth.OAuth.OIDC.AllowedSigningAlgs)
	c.Auth.OAuth.OIDC.UserInfoEmailPath = strings.TrimSpace(c.Auth.OAuth.OIDC.UserInfoEmailPath)
	c.Auth.OAuth.OIDC.UserInfoIDPath = strings.TrimSpace(c.Auth.OAuth.OIDC.UserInfoIDPath)
	c.Auth.OAuth.OIDC.UserInfoUsernamePath = strings.TrimSpace(c.Auth.OAuth.OIDC.UserInfoUsernamePath)
	c.normalizeWebAuthn()
}

// normalizeWebAuthn trims enabled passkey relying-party fields and canonicalizes
// valid origins in a newly allocated slice while leaving invalid entries trimmed
// for validateWebAuthn to report.
func (c *Config) normalizeWebAuthn() {
	if !c.Auth.WebAuthn.Enabled {
		return
	}
	c.Auth.WebAuthn.RPDisplayName = strings.TrimSpace(c.Auth.WebAuthn.RPDisplayName)
	c.Auth.WebAuthn.RPID = strings.ToLower(strings.TrimSpace(c.Auth.WebAuthn.RPID))
	c.Auth.WebAuthn.RPOrigins = normalizeStringSlice(c.Auth.WebAuthn.RPOrigins)
	if c.Auth.WebAuthn.RPDisplayName == "" || c.Auth.WebAuthn.RPID == "" ||
		strings.Contains(c.Auth.WebAuthn.RPID, "://") || strings.ContainsAny(c.Auth.WebAuthn.RPID, "/:") {
		return
	}
	for i, origin := range c.Auth.WebAuthn.RPOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" {
			continue
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			continue
		}
		scheme := strings.ToLower(u.Scheme)
		hostWithPort := strings.ToLower(u.Host)
		host := strings.ToLower(u.Hostname())
		localDevelopment := host == "localhost" || host == "127.0.0.1" || host == "::1"
		if scheme != "https" && (scheme != "http" || !localDevelopment) {
			continue
		}
		if host != c.Auth.WebAuthn.RPID && !strings.HasSuffix(host, "."+c.Auth.WebAuthn.RPID) {
			continue
		}
		c.Auth.WebAuthn.RPOrigins[i] = scheme + "://" + hostWithPort
	}
}

// validateAPIKeyAbuse checks invalid-key abuse thresholds and window bounds when
// abuse tracking is enabled.
func (c *Config) validateAPIKeyAbuse() error {
	// Apply the configured abuse observation limits when tracking is enabled. Its
	// threshold, observation window, block interval, and capacity determine the
	// process-local invalid-auth counter behavior.
	if c.APIKey.InvalidKeyAbuse.Enabled {
		// The counter threshold governs repeated invalid credentials from one
		// observed client address.
		if c.APIKey.InvalidKeyAbuse.Threshold < 10 {
			return fmt.Errorf("api_key.invalid_key_abuse.threshold must be at least 10")
		}
		// Bound the seconds during which invalid-auth observations contribute to that
		// client counter.
		if c.APIKey.InvalidKeyAbuse.WindowSeconds < 1 || c.APIKey.InvalidKeyAbuse.WindowSeconds > 3600 {
			return fmt.Errorf("api_key.invalid_key_abuse.window_seconds must be between 1 and 3600")
		}
		// The blocking interval starts after the observation threshold has been
		// reached.
		if c.APIKey.InvalidKeyAbuse.BlockSeconds < 1 || c.APIKey.InvalidKeyAbuse.BlockSeconds > 3600 {
			return fmt.Errorf("api_key.invalid_key_abuse.block_seconds must be between 1 and 3600")
		}
		// This entry capacity bounds client-address tracking in the process-local
		// abuse cache.
		if c.APIKey.InvalidKeyAbuse.Capacity < 256 || c.APIKey.InvalidKeyAbuse.Capacity > 1_000_000 {
			return fmt.Errorf("api_key.invalid_key_abuse.capacity must be between 256 and 1000000")
		}
	}
	return nil
}

// validateJWTSecret checks that auth.jwt.secret is present and at least 32
// UTF-8 bytes long.
func (c *Config) validateJWTSecret() error {
	jwtSecret := strings.TrimSpace(c.Auth.JWT.Secret)
	if jwtSecret == "" {
		return fmt.Errorf("auth.jwt.secret is required")
	}
	// The signing input minimum is measured as encoded bytes rather than rune
	// count. Check the trimmed secret before identity construction so multibyte
	// text contributes its UTF-8 byte length.
	if len([]byte(jwtSecret)) < 32 {
		return fmt.Errorf("auth.jwt.secret must be at least 32 bytes")
	}
	return nil
}

// validateJWTLifetimes checks positive JWT access and refresh lifetimes and
// logs security warnings when configured durations are unusually long.
func (c *Config) validateJWTLifetimes() error {
	if c.Auth.JWT.AccessTokenTTLMinutes <= 0 {
		return fmt.Errorf("auth.jwt.access_token_ttl_minutes must be positive")
	}
	// Warn above the ordinary one-day default without rejecting an explicit longer lifetime.
	if c.Auth.JWT.AccessTokenTTLMinutes > 1440 {
		slog.Warn("auth.jwt.access_token_ttl_minutes is high; consider shorter expiration for security")
	}
	if c.Auth.JWT.RefreshTokenTTLDays <= 0 {
		return fmt.Errorf("auth.jwt.refresh_token_ttl_days must be positive")
	}
	// Long refresh-token lifetimes remain accepted after this warning is emitted.
	if c.Auth.JWT.RefreshTokenTTLDays > 90 {
		slog.Warn("auth.jwt.refresh_token_ttl_days is high; consider shorter expiration for security", "refresh_token_expire_days", c.Auth.JWT.RefreshTokenTTLDays)
	}
	return nil
}

// validateLinuxDoOAuth checks enabled LinuxDo OAuth credentials and URLs.
func (c *Config) validateLinuxDoOAuth() error {
	if c.Auth.OAuth.LinuxDo.Enabled {
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.ClientID) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.client_id is required when auth.oauth.linuxdo.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.AuthorizeURL) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.authorize_url is required when auth.oauth.linuxdo.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.TokenURL) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.token_url is required when auth.oauth.linuxdo.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.UserInfoURL) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.userinfo_url is required when auth.oauth.linuxdo.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.RedirectURL) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.redirect_url is required when auth.oauth.linuxdo.enabled=true")
		}
		method := strings.ToLower(strings.TrimSpace(c.Auth.OAuth.LinuxDo.TokenAuthMethod))
		switch method {
		case "", "client_secret_post", "client_secret_basic", "none":
		default:
			return fmt.Errorf("auth.oauth.linuxdo.token_auth_method must be one of: client_secret_post/client_secret_basic/none")
		}
		if (method == "" || method == "client_secret_post" || method == "client_secret_basic") &&
			strings.TrimSpace(c.Auth.OAuth.LinuxDo.ClientSecret) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.client_secret is required when auth.oauth.linuxdo.enabled=true and token_auth_method is client_secret_post/client_secret_basic")
		}
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.FrontendRedirectURL) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.frontend_redirect_url is required when auth.oauth.linuxdo.enabled=true")
		}

		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.LinuxDo.AuthorizeURL); err != nil {
			return fmt.Errorf("auth.oauth.linuxdo.authorize_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.LinuxDo.TokenURL); err != nil {
			return fmt.Errorf("auth.oauth.linuxdo.token_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.LinuxDo.UserInfoURL); err != nil {
			return fmt.Errorf("auth.oauth.linuxdo.userinfo_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.LinuxDo.RedirectURL); err != nil {
			return fmt.Errorf("auth.oauth.linuxdo.redirect_url invalid: %w", err)
		}
		if err := ValidateFrontendRedirectURL(c.Auth.OAuth.LinuxDo.FrontendRedirectURL); err != nil {
			return fmt.Errorf("auth.oauth.linuxdo.frontend_redirect_url invalid: %w", err)
		}

		warnIfInsecureURL("auth.oauth.linuxdo.authorize_url", c.Auth.OAuth.LinuxDo.AuthorizeURL)
		warnIfInsecureURL("auth.oauth.linuxdo.token_url", c.Auth.OAuth.LinuxDo.TokenURL)
		warnIfInsecureURL("auth.oauth.linuxdo.userinfo_url", c.Auth.OAuth.LinuxDo.UserInfoURL)
		warnIfInsecureURL("auth.oauth.linuxdo.redirect_url", c.Auth.OAuth.LinuxDo.RedirectURL)
		warnIfInsecureURL("auth.oauth.linuxdo.frontend_redirect_url", c.Auth.OAuth.LinuxDo.FrontendRedirectURL)
	}
	return nil
}

// validateWeChatOAuth checks enabled WeChat channel credentials and callback URLs.
func (c *Config) validateWeChatOAuth() error {
	if c.Auth.OAuth.WeChat.Enabled {
		weChat := c.Auth.OAuth.WeChat

		if weChat.OpenEnabled {
			if strings.TrimSpace(weChat.OpenAppID) == "" {
				return fmt.Errorf("auth.oauth.wechat.open_app_id is required when auth.oauth.wechat.open_enabled=true")
			}
			if strings.TrimSpace(weChat.OpenAppSecret) == "" {
				return fmt.Errorf("auth.oauth.wechat.open_app_secret is required when auth.oauth.wechat.open_enabled=true")
			}
		}
		if weChat.MPEnabled {
			if strings.TrimSpace(weChat.MPAppID) == "" {
				return fmt.Errorf("auth.oauth.wechat.mp_app_id is required when auth.oauth.wechat.mp_enabled=true")
			}
			if strings.TrimSpace(weChat.MPAppSecret) == "" {
				return fmt.Errorf("auth.oauth.wechat.mp_app_secret is required when auth.oauth.wechat.mp_enabled=true")
			}
		}
		if weChat.MobileEnabled {
			if strings.TrimSpace(weChat.MobileAppID) == "" {
				return fmt.Errorf("auth.oauth.wechat.mobile_app_id is required when auth.oauth.wechat.mobile_enabled=true")
			}
			if strings.TrimSpace(weChat.MobileAppSecret) == "" {
				return fmt.Errorf("auth.oauth.wechat.mobile_app_secret is required when auth.oauth.wechat.mobile_enabled=true")
			}
		}
		if v := strings.TrimSpace(weChat.RedirectURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.wechat.redirect_url invalid: %w", err)
			}
			warnIfInsecureURL("auth.oauth.wechat.redirect_url", v)
		}
		if err := ValidateFrontendRedirectURL(weChat.FrontendRedirectURL); err != nil {
			return fmt.Errorf("auth.oauth.wechat.frontend_redirect_url invalid: %w", err)
		}
		warnIfInsecureURL("auth.oauth.wechat.frontend_redirect_url", weChat.FrontendRedirectURL)
	}
	return nil
}

// validateWebAuthn checks the enabled relying-party identity and browser origins
// without mutating receiver fields. Disabled passkey settings pass unchanged.
func (c *Config) validateWebAuthn() error {
	if c.Auth.WebAuthn.Enabled {
		if strings.TrimSpace(c.Auth.WebAuthn.RPDisplayName) == "" {
			return fmt.Errorf("auth.webauthn.rp_display_name is required when passkeys are enabled")
		}
		rpID := strings.ToLower(strings.TrimSpace(c.Auth.WebAuthn.RPID))
		if rpID == "" {
			return fmt.Errorf("auth.webauthn.rp_id is required when passkeys are enabled")
		}
		if strings.Contains(rpID, "://") || strings.ContainsAny(rpID, "/:") {
			return fmt.Errorf("auth.webauthn.rp_id must be a domain without scheme, port, or path")
		}
		if len(c.Auth.WebAuthn.RPOrigins) == 0 {
			return fmt.Errorf("auth.webauthn.rp_origins must contain at least one origin when passkeys are enabled")
		}
		for _, origin := range c.Auth.WebAuthn.RPOrigins {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return fmt.Errorf("auth.webauthn.rp_origins contains invalid origin %q", origin)
			}
			if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
				return fmt.Errorf("auth.webauthn.rp_origins entry %q must not include userinfo, path, query, or fragment", origin)
			}
			scheme := strings.ToLower(u.Scheme)
			host := strings.ToLower(u.Hostname())
			localDevelopment := host == "localhost" || host == "127.0.0.1" || host == "::1"
			// Passkey origins require HTTPS in the deployment rule. Permit HTTP for the
			// exact localhost and loopback host forms so a local development relying
			// party can use a plain HTTP origin.
			if scheme != "https" && (scheme != "http" || !localDevelopment) {
				return fmt.Errorf("auth.webauthn.rp_origins entry %q must use HTTPS (HTTP is allowed only for localhost)", origin)
			}
			if host != rpID && !strings.HasSuffix(host, "."+rpID) {
				return fmt.Errorf("auth.webauthn.rp_origins entry %q is not within relying party ID %q", origin, rpID)
			}
		}
	}
	return nil
}

// validateOIDC checks enabled login credentials, scopes, and endpoints in their
// startup order. Disabled settings pass unchanged. Validation reads trimmed
// views of strings, keeps the configured values, and emits HTTP warnings only
// after all OIDC checks succeed.
func (c *Config) validateOIDC() error {
	if c.Auth.OAuth.OIDC.Enabled {
		if strings.TrimSpace(c.Auth.OAuth.OIDC.ClientID) == "" {
			return fmt.Errorf("auth.oauth.oidc.client_id is required when auth.oauth.oidc.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.OIDC.IssuerURL) == "" {
			return fmt.Errorf("auth.oauth.oidc.issuer_url is required when auth.oauth.oidc.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.OIDC.RedirectURL) == "" {
			return fmt.Errorf("auth.oauth.oidc.redirect_url is required when auth.oauth.oidc.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.OIDC.FrontendRedirectURL) == "" {
			return fmt.Errorf("auth.oauth.oidc.frontend_redirect_url is required when auth.oauth.oidc.enabled=true")
		}
		if !scopeContainsOpenID(c.Auth.OAuth.OIDC.Scopes) {
			return fmt.Errorf("auth.oauth.oidc.scopes must contain openid")
		}

		method := strings.ToLower(strings.TrimSpace(c.Auth.OAuth.OIDC.TokenAuthMethod))
		switch method {
		case "", "client_secret_post", "client_secret_basic", "none":
		default:
			return fmt.Errorf("auth.oauth.oidc.token_auth_method must be one of: client_secret_post/client_secret_basic/none")
		}
		if (method == "" || method == "client_secret_post" || method == "client_secret_basic") &&
			strings.TrimSpace(c.Auth.OAuth.OIDC.ClientSecret) == "" {
			return fmt.Errorf("auth.oauth.oidc.client_secret is required when auth.oauth.oidc.enabled=true and token_auth_method is client_secret_post/client_secret_basic")
		}
		// Enabled OIDC bounds this tolerance even when ID-token validation is
		// disabled.
		if c.Auth.OAuth.OIDC.ClockSkewSeconds < 0 || c.Auth.OAuth.OIDC.ClockSkewSeconds > 600 {
			return fmt.Errorf("auth.oauth.oidc.clock_skew_seconds must be between 0 and 600")
		}
		if c.Auth.OAuth.OIDC.ValidateIDToken && strings.TrimSpace(c.Auth.OAuth.OIDC.AllowedSigningAlgs) == "" {
			return fmt.Errorf("auth.oauth.oidc.allowed_signing_algs is required when auth.oauth.oidc.validate_id_token=true")
		}

		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.OIDC.IssuerURL); err != nil {
			return fmt.Errorf("auth.oauth.oidc.issuer_url invalid: %w", err)
		}
		if v := strings.TrimSpace(c.Auth.OAuth.OIDC.DiscoveryURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.oidc.discovery_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.Auth.OAuth.OIDC.AuthorizeURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.oidc.authorize_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.Auth.OAuth.OIDC.TokenURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.oidc.token_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.Auth.OAuth.OIDC.UserInfoURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.oidc.userinfo_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.Auth.OAuth.OIDC.JWKSURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.oidc.jwks_url invalid: %w", err)
			}
		}
		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.OIDC.RedirectURL); err != nil {
			return fmt.Errorf("auth.oauth.oidc.redirect_url invalid: %w", err)
		}
		if err := ValidateFrontendRedirectURL(c.Auth.OAuth.OIDC.FrontendRedirectURL); err != nil {
			return fmt.Errorf("auth.oauth.oidc.frontend_redirect_url invalid: %w", err)
		}

		warnIfInsecureURL("auth.oauth.oidc.issuer_url", c.Auth.OAuth.OIDC.IssuerURL)
		warnIfInsecureURL("auth.oauth.oidc.discovery_url", c.Auth.OAuth.OIDC.DiscoveryURL)
		warnIfInsecureURL("auth.oauth.oidc.authorize_url", c.Auth.OAuth.OIDC.AuthorizeURL)
		warnIfInsecureURL("auth.oauth.oidc.token_url", c.Auth.OAuth.OIDC.TokenURL)
		warnIfInsecureURL("auth.oauth.oidc.userinfo_url", c.Auth.OAuth.OIDC.UserInfoURL)
		warnIfInsecureURL("auth.oauth.oidc.jwks_url", c.Auth.OAuth.OIDC.JWKSURL)
		warnIfInsecureURL("auth.oauth.oidc.redirect_url", c.Auth.OAuth.OIDC.RedirectURL)
		warnIfInsecureURL("auth.oauth.oidc.frontend_redirect_url", c.Auth.OAuth.OIDC.FrontendRedirectURL)
	}
	return nil
}

// isWeakJWTSecret reports blank or known weak-secret spellings after trimming
// and lowercasing. It is a warning-list comparison rather than an entropy or
// cryptographic strength measurement; other text returns false.
func isWeakJWTSecret(secret string) bool {
	lower := strings.ToLower(strings.TrimSpace(secret))
	if lower == "" {
		return true
	}
	weak := map[string]struct{}{
		"change-me-in-production": {},
		"changeme":                {},
		"secret":                  {},
		"password":                {},
		"123456":                  {},
		"12345678":                {},
		"admin":                   {},
		"jwt-secret":              {},
	}
	_, exists := weak[lower]
	return exists
}

// generateJWTSecret returns lowercase hexadecimal text from byteLength random
// bytes, selecting thirty-two bytes for nonpositive input. Encoded text
// contains twice the selected byte count and returns with nil error.
// crypto/rand.Read terminates the process when io.ReadFull reports an error.
func generateJWTSecret(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 32
	}
	buf := make([]byte, byteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// scopeContainsOpenID reports whether lowercased whitespace-separated scope
// text contains the exact openid token.
func scopeContainsOpenID(scopes string) bool {
	return slices.Contains(strings.Fields(strings.ToLower(strings.TrimSpace(scopes))), "openid")
}

// ValidateDingTalkConfig delegates DingTalk deployment rule checking to
// identity authconfig. Disabled settings are accepted, and recognized mode/type
// failures retain shared sentinel identity for callers using errors.Is across
// this facade.
func ValidateDingTalkConfig(value DingTalkOAuthConfig) error {
	return authconfig.ValidateDingTalkConfig(value.IdentityDefaults())
}
