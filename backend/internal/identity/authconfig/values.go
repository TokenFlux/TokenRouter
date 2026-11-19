package authconfig

// values.go defines startup credential, callback, and profile settings for
// supported OAuth providers.
// Startup OAuth settings provide defaults for later runtime reads, and each
// provider carries its own credential and callback fields. The resulting values
// feed browser authorization, backend code exchange, and profile interpretation;
// provider-specific readers validate the effective settings before the flow starts.
//
// OIDC includes presence flags because an omitted startup boolean and a supplied
// false value produce different compatibility defaults. WeChat retains legacy
// credentials alongside channel credentials so older deployment settings can
// supply a selected channel. DingTalk groups organization policy with profile
// synchronization options, whose effective enablement is constrained by that
// policy in the identity settings reader.

// LinuxDoConnectConfig holds startup and effective settings for LinuxDo authorization.
// OAuthSettings resolves runtime overrides before the HTTP flow uses these values.
type LinuxDoConnectConfig struct {
	Enabled             bool   `yaml:"enabled"`               // provider login enablement flag
	ClientID            string `yaml:"client_id"`             // registered OAuth client identifier
	ClientSecret        string `yaml:"client_secret"`         // OAuth token exchange secret
	AuthorizeURL        string `yaml:"authorize_url"`         // browser authorization request endpoint
	TokenURL            string `yaml:"token_url"`             // authorization code exchange endpoint
	UserInfoURL         string `yaml:"userinfo_url"`          // authenticated profile request endpoint
	Scopes              string `yaml:"scopes"`                // requested OAuth scope text
	RedirectURL         string `yaml:"redirect_url"`          // registered backend callback URL
	FrontendRedirectURL string `yaml:"frontend_redirect_url"` // frontend flow completion destination
	TokenAuthMethod     string `yaml:"token_auth_method"`     // token endpoint authentication method
	UsePKCE             bool   `yaml:"use_pkce"`              // authorization proof key enablement
	// UserInfoEmailPath, UserInfoIDPath, and UserInfoUsernamePath are tried before the
	// common field names in LinuxDoParseUserInfo. Empty or unfilled paths allow that
	// parser to continue through its ordered default candidates.
	UserInfoEmailPath    string `yaml:"userinfo_email_path"`    // preferred userinfo email path
	UserInfoIDPath       string `yaml:"userinfo_id_path"`       // preferred userinfo subject path
	UserInfoUsernamePath string `yaml:"userinfo_username_path"` // preferred userinfo username path
}

// WeChatConnectConfig holds credentials and enablement for the three WeChat login
// channels. OAuthSettings selects the effective channel and its backend callback.
type WeChatConnectConfig struct {
	Enabled bool `yaml:"enabled"` // provider login enablement flag
	// AppID and AppSecret are legacy defaults for OpenAppID/OpenAppSecret,
	// MPAppID/MPAppSecret, and MobileAppID/MobileAppSecret. OAuthSettings uses the
	// selected channel's nonempty credential first and then its resolved legacy value.
	// When a legacy value is missing, startup Open, MP, and Mobile credentials are
	// tried in that order to supply the shared legacy default.
	AppID               string `yaml:"app_id"`                // legacy application identifier value
	AppSecret           string `yaml:"app_secret"`            // legacy application secret value
	OpenAppID           string `yaml:"open_app_id"`           // open-platform application identifier value
	OpenAppSecret       string `yaml:"open_app_secret"`       // open-platform application secret value
	MPAppID             string `yaml:"mp_app_id"`             // public-account application identifier value
	MPAppSecret         string `yaml:"mp_app_secret"`         // public-account application secret value
	MobileAppID         string `yaml:"mobile_app_id"`         // mobile application identifier value
	MobileAppSecret     string `yaml:"mobile_app_secret"`     // mobile application secret value
	OpenEnabled         bool   `yaml:"open_enabled"`          // open-platform login enablement flag
	MPEnabled           bool   `yaml:"mp_enabled"`            // public-account login enablement flag
	MobileEnabled       bool   `yaml:"mobile_enabled"`        // mobile login enablement flag
	Mode                string `yaml:"mode"`                  // selected WeChat login mode
	Scopes              string `yaml:"scopes"`                // requested OAuth scope text
	RedirectURL         string `yaml:"redirect_url"`          // registered backend callback URL
	FrontendRedirectURL string `yaml:"frontend_redirect_url"` // frontend flow completion destination
}

// OIDCConnectConfig holds provider endpoints and ID-token validation settings used
// by OAuthSettings and the OIDC authorization flow after runtime values are resolved.
type OIDCConnectConfig struct {
	Enabled      bool   `yaml:"enabled"`       // provider login enablement flag
	ProviderName string `yaml:"provider_name"` // displayed login provider name
	ClientID     string `yaml:"client_id"`     // registered OAuth client identifier
	ClientSecret string `yaml:"client_secret"` // OAuth token exchange secret
	IssuerURL    string `yaml:"issuer_url"`    // expected ID token issuer
	DiscoveryURL string `yaml:"discovery_url"` // provider metadata document URL
	AuthorizeURL string `yaml:"authorize_url"` // browser authorization request endpoint
	TokenURL     string `yaml:"token_url"`     // authorization code exchange endpoint
	// UserInfoURL may be empty when the flow can obtain identity claims from an
	// ID token. OidcFetchUserInfo then returns an empty claims object without a request.
	UserInfoURL         string `yaml:"userinfo_url"`          // authenticated profile request endpoint
	JWKSURL             string `yaml:"jwks_url"`              // signature verification key endpoint
	Scopes              string `yaml:"scopes"`                // requested OAuth scope text
	RedirectURL         string `yaml:"redirect_url"`          // registered backend callback URL
	FrontendRedirectURL string `yaml:"frontend_redirect_url"` // frontend flow completion destination
	TokenAuthMethod     string `yaml:"token_auth_method"`     // token endpoint authentication method
	UsePKCE             bool   `yaml:"use_pkce"`              // authorization proof key enablement
	ValidateIDToken     bool   `yaml:"validate_id_token"`     // ID token verification enablement
	// UsePKCEExplicit and ValidateIDTokenExplicit record startup input presence for
	// UsePKCE and ValidateIDToken. OAuthSettings compatibility reads enable omitted
	// security switches; its write-default selection distinguishes omission from a
	// supplied false value.
	UsePKCEExplicit         bool   `yaml:"-"`                    // startup PKCE value presence
	ValidateIDTokenExplicit bool   `yaml:"-"`                    // startup validation value presence
	AllowedSigningAlgs      string `yaml:"allowed_signing_algs"` // comma-separated accepted signature algorithms
	// ClockSkewSeconds becomes jwt validation leeway in OidcParseAndValidateIDToken.
	// OAuthSettings accepts zero through 600, so zero has no additional skew allowance.
	ClockSkewSeconds int `yaml:"clock_skew_seconds"` // seconds
	// RequireEmailVerified rejects unverified email when OIDCHandler reaches new-identity
	// account selection. Login through an existing stored identity is resolved earlier.
	RequireEmailVerified bool `yaml:"require_email_verified"` // verified email requirement flag
	// UserInfoEmailPath, UserInfoIDPath, and UserInfoUsernamePath choose the first
	// profile lookup candidates in OidcParseUserInfo. A missing or empty selected value
	// allows that parser to try its ordered common field names.
	UserInfoEmailPath    string `yaml:"userinfo_email_path"`    // preferred userinfo email path
	UserInfoIDPath       string `yaml:"userinfo_id_path"`       // preferred userinfo subject path
	UserInfoUsernamePath string `yaml:"userinfo_username_path"` // preferred userinfo username path
}

// DingTalkConnectConfig holds application settings and organization policy for
// DingTalk login, including the profile fields copied to local user attributes.
type DingTalkConnectConfig struct {
	Enabled             bool   `yaml:"enabled"`               // provider login enablement flag
	ClientID            string `yaml:"client_id"`             // registered OAuth client identifier
	ClientSecret        string `yaml:"client_secret"`         // OAuth token exchange secret
	AuthorizeURL        string `yaml:"authorize_url"`         // browser authorization request endpoint
	TokenURL            string `yaml:"token_url"`             // authorization code exchange endpoint
	UserInfoURL         string `yaml:"userinfo_url"`          // authenticated profile request endpoint
	Scopes              string `yaml:"scopes"`                // requested OAuth scope text
	RedirectURL         string `yaml:"redirect_url"`          // registered backend callback URL
	FrontendRedirectURL string `yaml:"frontend_redirect_url"` // frontend flow completion destination
	// DingTalkAppKind requires internal_app for enabled settings. AppType additionally
	// requires internal when CorpRestrictionPolicy is internal_only; OAuthSettings
	// sets that effective AppType before calling ValidateDingTalkConfig.
	DingTalkAppKind       string `yaml:"dingtalk_app_kind"`       // internal_app for enabled login
	AppType               string `yaml:"app_type"`                // public or internal application
	CorpRestrictionPolicy string `yaml:"corp_restriction_policy"` // none or internal_only policy
	// InternalCorpID retains an organization identifier for settings readback.
	// DingTalkHandler checks internal_only membership through the application
	// directory; DingTalkCorpAllowed accepts missing or different corpID values.
	InternalCorpID string `yaml:"internal_corp_id"` // configured organization identifier value
	// BypassRegistration permits closed-registration signup for internal_only
	// organizations. OAuthSettings clears the value when another organization policy
	// is selected before AuthService considers the bypass.
	BypassRegistration bool `yaml:"bypass_registration"` // internal organization registration bypass
	// SyncCorpEmail, SyncDisplayName, and SyncDept select local attribute updates in
	// DingTalkProfileSync. OAuthSettings clears these flags outside internal_only mode,
	// and the sync service applies the same organization-policy check before writing.
	SyncCorpEmail   bool `yaml:"sync_corp_email"`   // enterprise email attribute synchronization
	SyncDisplayName bool `yaml:"sync_display_name"` // display name attribute synchronization
	SyncDept        bool `yaml:"sync_dept"`         // department path attribute synchronization
	// SyncCorpEmailAttrKey, SyncDisplayNameAttrKey, and SyncDeptAttrKey name the
	// existing local attribute definitions updated during profile sync. Their AttrName
	// partners provide labels when settings administration prepares those definitions.
	SyncCorpEmailAttrKey    string `yaml:"sync_corp_email_attr_key"`    // enterprise email attribute key
	SyncDisplayNameAttrKey  string `yaml:"sync_display_name_attr_key"`  // display name attribute key
	SyncDeptAttrKey         string `yaml:"sync_dept_attr_key"`          // department path attribute key
	SyncCorpEmailAttrName   string `yaml:"sync_corp_email_attr_name"`   // enterprise email attribute label
	SyncDisplayNameAttrName string `yaml:"sync_display_name_attr_name"` // display name attribute label
	SyncDeptAttrName        string `yaml:"sync_dept_attr_name"`         // department path attribute label
	// RequireEmail prevents synthetic-email signup for a new DingTalk identity.
	// DingTalkHandler requests email completion when staff email is missing and signup
	// is allowed; a supplied staff email goes into account selection.
	RequireEmail            bool   `yaml:"require_email"`             // new identity email requirement
	UsernameOverwritePolicy string `yaml:"username_overwrite_policy"` // configured username overwrite policy
	UsernameAttributeKey    string `yaml:"username_attribute_key"`    // configured username attribute key
	EnableAttributeMatching bool   `yaml:"enable_attribute_matching"` // configured attribute matching flag
	EnableAttributeSync     bool   `yaml:"enable_attribute_sync"`     // configured attribute synchronization flag
	// AttributeSyncFields retains caller-owned slice storage in ordinary struct
	// copies. NewOAuthSettings and GetDingTalkConnectOAuthConfig clone this slice when
	// copying defaults into their settings results.
	AttributeSyncFields          []string `yaml:"attribute_sync_fields"`           // configured attribute synchronization names
	AttributeSyncOverwritePolicy string   `yaml:"attribute_sync_overwrite_policy"` // configured attribute overwrite policy
}

// EmailOAuthProviderConfig holds credentials, callbacks, and profile endpoints
// for GitHub or Google authorization resolved by the identity settings reader.
type EmailOAuthProviderConfig struct {
	Enabled             bool   `yaml:"enabled"`               // provider login enablement flag
	ClientID            string `yaml:"client_id"`             // registered OAuth client identifier
	ClientSecret        string `yaml:"client_secret"`         // OAuth token exchange secret
	AuthorizeURL        string `yaml:"authorize_url"`         // browser authorization request endpoint
	TokenURL            string `yaml:"token_url"`             // authorization code exchange endpoint
	UserInfoURL         string `yaml:"userinfo_url"`          // authenticated profile request endpoint
	EmailsURL           string `yaml:"emails_url"`            // verified email listing endpoint
	Scopes              string `yaml:"scopes"`                // requested OAuth scope text
	RedirectURL         string `yaml:"redirect_url"`          // registered backend callback URL
	FrontendRedirectURL string `yaml:"frontend_redirect_url"` // frontend flow completion destination
}
