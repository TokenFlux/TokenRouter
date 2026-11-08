package composite

import (
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// snapshot.go defines the combined settings value used for administrator reads
// and updates.
// Administrator reads and partial updates need raw values, normalized module
// fields, and selected presence metadata at the same time. A Snapshot keeps its
// stored batch for revision checks while the owning readers interpret display
// and control values. Preparation then changes selected typed fields before
// building the strings passed to a settings update session.
//
// Responses overrides remain distinct from effective WebSocket parameters, and
// scheduler overrides remain visible beside resolved inherited values. Secret
// text stays available internally while configured flags feed administrator
// responses. Module extraction and assignment copy scalar fields but retain
// collection and pointer references, so callers keep the Snapshot stable while
// preparation or application reads those values.

// Snapshot carries combined administrator values and mutable partial-update preparation state.
// Callers keep its collections stable through preparation and application; module
// value transfers retain slice, map, and pointer references unless a helper
// performs its own copy.
type Snapshot struct {
	// ResponsesWS keeps override JSON separate from ResponsesWSEffective. Parse
	// retains valid overrides even after resolution fails, then selects deployed or
	// current runtime parameters for the effective field.
	ResponsesWS          json.RawMessage // raw Responses WebSocket override JSON
	ResponsesWSEffective ws.Parameters   // resolved Responses WebSocket runtime parameters
	// StoredValues is cloned by Parse before module interpretation; nil input
	// retains a nil map. The handler supplies this readback to participants for
	// raw-value expectations while normalizing the separate typed fields.
	StoredValues map[string]string `json:"-"` // raw batch used for revision checks

	RegistrationEnabled                 bool     // new account registration switch
	EmailVerifyEnabled                  bool     // registration email verification switch
	RegistrationEmailSuffixWhitelist    []string // allowed registration email domain suffixes
	RegistrationEmailNormalization      bool     // registration mailbox normalization switch
	RegistrationEmailDomainQuotaEnabled bool     // outside-whitelist registration quota switch
	UserEmailChangeEnabled              bool     // primary email replacement policy switch
	PromoCodeEnabled                    bool     // registration promotional code switch
	PasswordResetEnabled                bool     // password recovery policy switch
	FrontendURL                         string   // notification and redirect frontend address
	InvitationCodeEnabled               bool     // registration invitation requirement switch
	TotpEnabled                         bool     // TOTP authentication availability switch
	SessionBindingEnabled               bool     // IP and user-agent session checks
	StepUpEnabled                       bool     // sensitive operation verification switch
	// AuditLogRetentionDays selects the saved retention period. Nonpositive values
	// keep audit records indefinitely when the audit cleanup reader interprets it.
	AuditLogRetentionDays   int    // days
	LoginAgreementEnabled   bool   // login agreement acceptance requirement
	LoginAgreementMode      string // modal or checkbox consent mode
	LoginAgreementUpdatedAt string // administrator agreement update marker
	// LoginAgreementDocuments can retain nested Localization pointers during
	// value conversion and unchanged site preparation. Callers keep those editor
	// objects stable while the update or response mapper reads them.
	LoginAgreementDocuments []site.LoginAgreementDocument // editable login agreement document records

	SMTPHost     string // outgoing mail server hostname
	SMTPPort     int    // SMTP connection port number
	SMTPUsername string // outgoing mail authentication username
	// SMTPPassword is an internal credential read beside SMTPPasswordConfigured.
	// Preparation can reuse its text; buildSystemSettingsResponse exposes the configured
	// flag while retaining host, user, and sender fields for administration.
	SMTPPassword           string // internal SMTP authentication secret
	SMTPPasswordConfigured bool   // SMTP secret presence display flag
	SMTPFrom               string // outgoing mail sender address
	SMTPFromName           string // outgoing mail sender display name
	SMTPUseTLS             bool   // SMTP secure connection preference

	TurnstileEnabled                       bool     // Cloudflare challenge verification switch
	TurnstileSiteKey                       string   // public Turnstile widget key
	TurnstileSecretKey                     string   // internal Turnstile verification secret
	TurnstileSecretKeyConfigured           bool     // Turnstile secret presence display flag
	TencentCaptchaEnabled                  bool     // Tencent challenge verification switch
	TencentCaptchaAppID                    string   // Tencent CAPTCHA application identifier
	TencentCaptchaAppSecretKey             string   // Tencent application verification secret
	TencentCaptchaAppSecretKeyConfigured   bool     // application secret presence display flag
	TencentCaptchaCloudSecretID            string   // Tencent cloud credential identifier
	TencentCaptchaCloudSecretIDConfigured  bool     // cloud credential identifier presence flag
	TencentCaptchaCloudSecretKey           string   // Tencent cloud signing secret
	TencentCaptchaCloudSecretKeyConfigured bool     // cloud signing secret presence flag
	TencentCaptchaRegion                   string   // cn or intl verification region
	AliyunCaptchaEnabled                   bool     // Aliyun challenge verification switch
	AliyunCaptchaAccessKeyID               string   // Aliyun verification credential identifier
	AliyunCaptchaAccessKeySecret           string   // Aliyun verification credential secret
	AliyunCaptchaAccessKeySecretConfigured bool     // Aliyun secret presence display flag
	AliyunCaptchaSceneID                   string   // Aliyun CAPTCHA scene identifier
	AliyunCaptchaPrefix                    string   // Aliyun client widget resource prefix
	AliyunCaptchaRegion                    string   // cn or sgp verification region
	APIKeyACLTrustForwardedIP              bool     // API-key forwarded-address trust switch
	ForwardedClientIPHeaders               []string // ordered forwarded-address header names

	LinuxDoConnectEnabled                bool   // LinuxDo login availability switch
	LinuxDoConnectClientID               string // LinuxDo registered OAuth client identifier
	LinuxDoConnectClientSecret           string // LinuxDo internal token exchange secret
	LinuxDoConnectClientSecretConfigured bool   // LinuxDo token secret presence display flag
	LinuxDoConnectRedirectURL            string // LinuxDo registered backend callback URL

	DingTalkConnectEnabled                 bool   // DingTalk login availability switch
	DingTalkConnectClientID                string // DingTalk registered OAuth client identifier
	DingTalkConnectClientSecret            string // DingTalk internal token exchange secret
	DingTalkConnectClientSecretConfigured  bool   // DingTalk token secret presence display flag
	DingTalkConnectRedirectURL             string // DingTalk registered backend callback URL
	DingTalkConnectCorpRestrictionPolicy   string // selected organization admission policy
	DingTalkConnectInternalCorpID          string // configured corporation identifier text
	DingTalkConnectBypassRegistration      bool   // closed-registration organization bypass switch
	DingTalkConnectSyncCorpEmail           bool   // enterprise email attribute synchronization
	DingTalkConnectSyncDisplayName         bool   // display name attribute synchronization
	DingTalkConnectSyncDept                bool   // department path attribute synchronization
	DingTalkConnectSyncCorpEmailAttrKey    string // enterprise email attribute key
	DingTalkConnectSyncDisplayNameAttrKey  string // display name attribute key
	DingTalkConnectSyncDeptAttrKey         string // department path attribute key
	DingTalkConnectSyncCorpEmailAttrName   string // enterprise email attribute label
	DingTalkConnectSyncDisplayNameAttrName string // display name attribute label
	DingTalkConnectSyncDeptAttrName        string // department path attribute label

	WeChatConnectEnabled                   bool   // WeChat login availability switch
	WeChatConnectAppID                     string // legacy WeChat application identifier
	WeChatConnectAppSecret                 string // legacy WeChat exchange secret
	WeChatConnectAppSecretConfigured       bool   // legacy secret presence display flag
	WeChatConnectOpenAppID                 string // open-platform WeChat application identifier
	WeChatConnectOpenAppSecret             string // open-platform WeChat exchange secret
	WeChatConnectOpenAppSecretConfigured   bool   // open-platform secret presence display flag
	WeChatConnectMPAppID                   string // public-account WeChat application identifier
	WeChatConnectMPAppSecret               string // public-account WeChat exchange secret
	WeChatConnectMPAppSecretConfigured     bool   // public-account secret presence display flag
	WeChatConnectMobileAppID               string // mobile WeChat application identifier
	WeChatConnectMobileAppSecret           string // mobile WeChat exchange secret
	WeChatConnectMobileAppSecretConfigured bool   // mobile secret presence display flag
	WeChatConnectOpenEnabled               bool   // open-platform WeChat login switch
	WeChatConnectMPEnabled                 bool   // public-account WeChat login switch
	WeChatConnectMobileEnabled             bool   // mobile WeChat login switch
	WeChatConnectMode                      string // open, mp, or mobile choice
	WeChatConnectScopes                    string // requested WeChat authorization scope text
	WeChatConnectRedirectURL               string // WeChat backend web callback URL
	WeChatConnectFrontendRedirectURL       string // WeChat frontend authorization completion destination

	OIDCConnectEnabled                bool   // OIDC login availability switch
	OIDCConnectProviderName           string // source login provider display name
	OIDCConnectClientID               string // OIDC registered OAuth client identifier
	OIDCConnectClientSecret           string // OIDC internal token exchange secret
	OIDCConnectClientSecretConfigured bool   // OIDC token secret presence display flag
	OIDCConnectIssuerURL              string // expected signed token issuer URL
	OIDCConnectDiscoveryURL           string // provider endpoint discovery document URL
	OIDCConnectAuthorizeURL           string // browser authorization endpoint URL
	OIDCConnectTokenURL               string // authorization code exchange endpoint URL
	OIDCConnectUserInfoURL            string // optional authenticated profile endpoint URL
	OIDCConnectJWKSURL                string // token signature verification key URL
	OIDCConnectScopes                 string // requested authorization scope text
	OIDCConnectRedirectURL            string // OIDC registered backend callback URL
	OIDCConnectFrontendRedirectURL    string // OIDC frontend authorization completion destination
	OIDCConnectTokenAuthMethod        string // token endpoint client authentication method
	OIDCConnectUsePKCE                bool   // authorization proof-key enablement switch
	OIDCConnectValidateIDToken        bool   // signed identity token validation switch
	OIDCConnectAllowedSigningAlgs     string // accepted comma-separated token algorithms
	OIDCConnectClockSkewSeconds       int    // seconds
	OIDCConnectRequireEmailVerified   bool   // new-identity verified-email requirement
	OIDCConnectUserInfoEmailPath      string // preferred userinfo email field path
	OIDCConnectUserInfoIDPath         string // preferred userinfo subject field path
	OIDCConnectUserInfoUsernamePath   string // preferred userinfo username field path

	GitHubOAuthEnabled                bool   // GitHub login availability switch
	GitHubOAuthClientID               string // GitHub registered OAuth client identifier
	GitHubOAuthClientSecret           string // GitHub internal token exchange secret
	GitHubOAuthClientSecretConfigured bool   // GitHub token secret presence display flag
	GitHubOAuthRedirectURL            string // GitHub registered backend callback URL
	GitHubOAuthFrontendRedirectURL    string // GitHub frontend authorization completion destination
	GoogleOAuthEnabled                bool   // Google login availability switch
	GoogleOneTapEnabled               bool   // Google One Tap credential switch
	GoogleOAuthClientID               string // Google registered OAuth client identifier
	GoogleOAuthClientSecret           string // Google internal token exchange secret
	GoogleOAuthClientSecretConfigured bool   // Google token secret presence display flag
	GoogleOAuthRedirectURL            string // Google registered backend callback URL
	GoogleOAuthFrontendRedirectURL    string // Google frontend authorization completion destination

	LocalizedSettings           settings.LocalizedTexts         `json:"localized_settings"` // localized operational text editor updates
	SiteTexts                   site.LocalizedTexts             `json:"site_texts"`         // localized site text editor updates
	DefaultLocale               string                          `json:"default_locale"`     // default user-facing language code
	SiteName                    string                          // source site display name
	SiteLogo                    string                          // site logo reference text
	SiteShareImage              string                          `json:"site_share_image"` // Saved cover image data URL.
	SiteSubtitle                string                          // source site subtitle text
	APIBaseURL                  string                          // displayed gateway API address
	ContactInfo                 string                          // source administrator contact text
	DocURL                      string                          // source documentation destination text
	HomeContent                 string                          // source custom homepage content
	HideCcsImportButton         bool                            // CCS import control visibility switch
	PurchaseSubscriptionEnabled bool                            // subscription purchase entry switch
	PurchaseSubscriptionURL     string                          // subscription purchase destination address
	TableDefaultPageSize        int                             // default table row count
	TablePageSizeOptions        []int                           // selectable table row counts
	UsageRankingLimit           int                             // maximum user ranking row count
	UsageRankingEnabled         bool                            // user usage ranking availability switch
	UsageRankingSortBy          string                          // selected user ranking metric
	UsageRankingShowTotalTokens bool                            // ranking token metric visibility
	UsageRankingShowRequests    bool                            // ranking request metric visibility
	UsageRankingShowActualCost  bool                            // ranking cost metric visibility
	CustomMenuItems             string                          // saved custom menu JSON
	CustomEndpoints             string                          // saved custom endpoint JSON
	FooterLinks                 string                          // saved footer group JSON
	FooterText                  string                          // source footer display text
	HomeFeaturedModels          string                          // saved featured model JSON
	CreativeModelSettings       []creative.CreativeModelSetting // Creative group/model operation selections
	CreativeWorkerCount         int                             // configured Creative task worker count

	DefaultConcurrency          int     // default registration concurrency allowance
	DefaultBalance              float64 // default registration balance grant amount
	BStatsEnabled               bool    // reporting enablement control
	BStatsLogErrors             bool    // report failure logging
	BStatsLogSentData           bool    // outgoing payload logging
	BStatsLogResponseStatusText bool    // received response logging
	TeamEnabled                 bool    // team runtime availability switch
	// CreativeEnabled supplies the runtime access switch. Creative execution also
	// requires the deployment creative.enabled capability before tasks can be admitted.
	CreativeEnabled                      bool                                 // Creative Studio runtime access switch
	RiskControlEnabled                   bool                                 // risk-center and auditing runtime switch
	CyberSessionBlockEnabled             bool                                 // cyber-session blocking runtime switch
	CyberSessionBlockTTLSeconds          int                                  // seconds
	AffiliateEnabled                     bool                                 // affiliate program availability switch
	AffiliateRebateRate                  float64                              // affiliate order rebate percentage
	AffiliateRebateFreezeHours           int                                  // hours
	AffiliateRebateDurationDays          int                                  // days
	AffiliateRebatePerInviteeCap         float64                              // maximum rebate per invited user
	AdminRechargeRebateEnabled           bool                                 // administrator recharge rebate switch
	DefaultUserRPMLimit                  int                                  // default user request-per-minute allowance
	DefaultUserAPIKeyLimit               int                                  // default user credential-count allowance
	DefaultSubscriptions                 []billing.DefaultSubscriptionSetting // default granted subscription plan selections
	BalanceUnitName                      string                               // source balance display unit name
	BalanceUnitSymbol                    string                               // balance display currency symbol
	BalanceIconSVG                       string                               // balance display SVG markup
	ReasoningPointRMBUnitPrice           float64                              // reasoning-point unit price in RMB
	USDExchangeRate                      float64                              // RMB amount per USD conversion
	MarketplaceAvailabilityWindowDays    int                                  // days
	MarketplaceAvailabilityBucketMinutes int                                  // minutes

	EnableModelFallback      bool   `json:"enable_model_fallback"`      // global fallback model selection switch
	FallbackModelAnthropic   string `json:"fallback_model_anthropic"`   // Anthropic fallback model identifier
	FallbackModelOpenAI      string `json:"fallback_model_openai"`      // OpenAI fallback model identifier
	FallbackModelGemini      string `json:"fallback_model_gemini"`      // Gemini fallback model identifier
	FallbackModelAntigravity string `json:"fallback_model_antigravity"` // Antigravity fallback model identifier

	EnableIdentityPatch bool   `json:"enable_identity_patch"` // Claude-to-Gemini identity patch switch
	IdentityPatchPrompt string `json:"identity_patch_prompt"` // configured identity replacement prompt

	GrokDefaultTextModel   string `json:"grok_default_text_model"`    // default model for model-omitted requests
	GrokDefaultBaseURLMode string `json:"grok_default_base_url_mode"` // unconfigured Grok endpoint selection mode

	OpsMonitoringEnabled         bool // Ops monitoring runtime switch
	OpsRealtimeMonitoringEnabled bool // Ops realtime collector runtime switch
	OpsMetricsIntervalSeconds    int  // seconds

	MinClaudeCodeVersion string // minimum admitted Claude Code version
	MaxClaudeCodeVersion string // maximum admitted Claude Code version

	BackendModeEnabled bool // administrator panel restriction switch

	// TTFTMode is normalized by the gateway reader for administrator display.
	// Empty or unsupported text selects the first useful upstream output at that consumer.
	TTFTMode string // global upstream latency selection
	// EnableFingerprintUnification, EnableClaudeOAuthSystemPromptInjection, and
	// EnableClientDatelineNormalization use true after missing or empty stored values
	// in the gateway reader. EnableMetadataPassthrough, EnableAnthropicCacheTTL1hInjection,
	// RewriteMessageCacheControl, and OpenAIAllowClaudeCodeCodexPlugin default false there.
	EnableFingerprintUnification bool // OAuth fingerprint header normalization switch
	EnableMetadataPassthrough    bool // client metadata forwarding switch
	// EnableCCHSigning retains a deprecated setting in administrator readback.
	// Gateway forwarding does not inject or sign cch from this stored switch.
	EnableCCHSigning                       bool // deprecated CCH signing setting
	EnableClaudeOAuthSystemPromptInjection bool // Claude OAuth system-block injection switch
	// ClaudeOAuthSystemPrompt retains configured extension text for the Claude
	// OAuth mimic path. Its runtime reader uses built-in prompt text when empty.
	ClaudeOAuthSystemPrompt string // source Claude OAuth extension prompt
	// ClaudeOAuthSystemPromptBlocks retains configured system-block JSON for the
	// Claude OAuth mimic path. Empty text lets its execution reader select built-in blocks.
	ClaudeOAuthSystemPromptBlocks      string // configured Claude OAuth system-block JSON
	EnableAnthropicCacheTTL1hInjection bool   // Anthropic one-hour cache TTL switch
	EnableClientDatelineNormalization  bool   // Anthropic dateline normalization switch
	RewriteMessageCacheControl         bool   // message cache-control rewriting switch
	// AntigravityUserAgentVersion is normalized by the platform version reader.
	// Empty overrides allow that consumer to use its deployment/default version choice.
	AntigravityUserAgentVersion string // configured Antigravity user-agent version
	// OpenAICodexUserAgent is the full configured upstream header value. Empty
	// text lets the runtime client reader select the built-in TUI user-agent string.
	OpenAICodexUserAgent             string                                    // configured full Codex user-agent text
	OpenAIAllowClaudeCodeCodexPlugin bool                                      // Claude Code Codex plugin admission
	UserPromptReplacementConfig      *promptpolicy.UserPromptReplacementConfig // administrator prompt replacement rule object

	WebSearchEmulationEnabled bool // gateway search tool emulation switch

	PaymentVisibleMethodAlipaySource  string // Alipay instance routing source choice
	PaymentVisibleMethodWxpaySource   string // WeChat Pay instance routing source
	PaymentVisibleMethodAlipayEnabled bool   // Alipay purchase-method visibility switch
	PaymentVisibleMethodWxpayEnabled  bool   // WeChat Pay purchase-method visibility switch

	// AdvancedScheduler fields keep configured numeric text and switches beside
	// the AdvancedSchedulerEffective values filled by the scheduler reader. Empty
	// overrides can inherit process defaults while their displayed raw strings stay empty.
	AdvancedSchedulerStickyWeightedEnabled       bool   // sticky result weighting switch
	AdvancedSchedulerSubscriptionPriorityEnabled bool   // subscription priority preference switch
	AdvancedSchedulerEWMAErrorRateAlpha          string // configured error-rate smoothing coefficient
	AdvancedSchedulerEWMATTFTAlpha               string // configured TTFT smoothing coefficient
	AdvancedSchedulerStickyEscapeEnabled         bool   // configured sticky escape enablement
	// AdvancedSchedulerStickyEscapeEnabledSet tracks a nonempty stored switch or
	// a submitted boolean. False makes scheduler preparation use inherited-setting
	// encoding and makes ParticipantFields omit the switch from registered input.
	AdvancedSchedulerStickyEscapeEnabledSet          bool   // stored sticky escape switch presence
	AdvancedSchedulerStickyEscapeTTFTMs              string // configured TTFT escape milliseconds
	AdvancedSchedulerStickyEscapeErrorRate           string // configured error-rate escape threshold
	AdvancedSchedulerLBTopK                          string // configured load-balanced candidate count
	AdvancedSchedulerWeightPriority                  string // configured provider priority score weight
	AdvancedSchedulerWeightLoad                      string // configured provider load score weight
	AdvancedSchedulerWeightQueue                     string // configured wait queue score weight
	AdvancedSchedulerWeightErrorRate                 string // configured observed error rate score weight
	AdvancedSchedulerWeightTTFT                      string // configured observed first-token time score weight
	AdvancedSchedulerWeightReset                     string // configured quota reset score weight
	AdvancedSchedulerWeightQuotaHeadroom             string // configured remaining quota score weight
	AdvancedSchedulerWeightPreviousResponse          string // configured previous response affinity score weight
	AdvancedSchedulerWeightSessionSticky             string // configured session affinity score weight
	AdvancedSchedulerEffectiveLBTopK                 string // resolved load-balanced candidate count
	AdvancedSchedulerEffectiveWeightPriority         string // resolved provider priority score weight
	AdvancedSchedulerEffectiveWeightLoad             string // resolved provider load score weight
	AdvancedSchedulerEffectiveWeightQueue            string // resolved wait queue score weight
	AdvancedSchedulerEffectiveWeightErrorRate        string // resolved observed error rate score weight
	AdvancedSchedulerEffectiveWeightTTFT             string // resolved observed first-token time score weight
	AdvancedSchedulerEffectiveWeightReset            string // resolved quota reset score weight
	AdvancedSchedulerEffectiveWeightQuotaHeadroom    string // resolved remaining quota score weight
	AdvancedSchedulerEffectiveWeightPreviousResponse string // resolved previous response affinity score weight
	AdvancedSchedulerEffectiveWeightSessionSticky    string // resolved session affinity score weight
	AdvancedSchedulerEffectiveEWMAErrorRateAlpha     string // resolved error-rate smoothing coefficient
	AdvancedSchedulerEffectiveEWMATTFTAlpha          string // resolved TTFT smoothing coefficient
	AdvancedSchedulerEffectiveStickyEscapeEnabled    bool   // resolved sticky escape enablement
	AdvancedSchedulerEffectiveStickyEscapeTTFTMs     string // resolved TTFT escape milliseconds
	AdvancedSchedulerEffectiveStickyEscapeErrorRate  string // resolved error-rate escape threshold
	// OpenAIQuotaAutoPauseSettings carries global five-hour and seven-day fractions
	// from ops_advanced_settings. Zero disables the corresponding global threshold default.
	OpenAIQuotaAutoPauseSettings provider.QuotaAutoPauseSettings // global OpenAI quota pause thresholds
	// OpenAIQuotaAutoPauseSettingsSet selects preparation of supplied quota
	// thresholds inside the wider Ops JSON. False leaves that merge path uncalled
	// for an ordinary update that omits the threshold object.
	OpenAIQuotaAutoPauseSettingsSet bool // submitted quota threshold object presence

	BalanceLowNotifyEnabled     bool    // low-balance mail notification switch
	BalanceLowNotifyThreshold   float64 // low-balance mail trigger amount
	BalanceLowNotifyRechargeURL string  // source low-balance recharge destination

	SubscriptionExpiryNotifyEnabled bool // subscription expiry mail notification switch

	ProviderQuotaNotifyEnabled bool            // provider quota mail notification switch
	ProviderQuotaNotifyEmails  []contact.Entry // provider quota mail recipient records

	// ProviderSchedulingThresholds carries supported platform percentages for
	// provider pause decisions. A value of 100 disables automatic pausing for that platform.
	ProviderSchedulingThresholds map[string]int `json:"provider_scheduling_thresholds"` // platform provider pause threshold percentages

	AllowUserViewErrorRequests bool // own failed-request visibility switch
}
