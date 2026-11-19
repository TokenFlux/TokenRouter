package provider

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// record.go defines the core provider Record, platform and authentication type
// aliases, and identity and availability accessors.
// A provider record carries both administrator configuration and runtime
// observations consumed by scheduling, forwarding, and management display.
// Scalar and map accessors interpret stored JSON representations while keeping
// private scheduler hints out of public JSON encoding.
//
// Scheduling combines active state, its manual switch, expiration policy,
// temporary windows, and applicable quota dimensions. Shadow credential health
// deliberately reads the shared parent's credential conditions separately from
// that parent's global scheduling state. Injected clock and timezone loaders
// default to standard-library functions when absent, and CloneRecord supplies
// supported mutable copies when ownership crosses a cache or request.

const (
	// Legacy workload input records enabled endpoint families before conversion
	// to the provider's current protocol set during configuration normalization.
	OpenAIWorkloadCapabilitiesCredentialKey = "openai_workload_capabilities"

	// Gemini provider labels distinguish third-party API Keys from the official
	// host when local official-quota simulation evaluates the saved connection.
	GeminiProviderTypeCredentialKey = "provider_type"
	GeminiProviderTypeThirdParty    = "third_party"
	GeminiOfficialAPIHost           = "generativelanguage.googleapis.com"

	// GrokMediaEligibleExtraKey records an optional administrator media selection.
	// A boolean overrides observed eligibility; missing or null values let the
	// media scheduler read the provider's upstream observations.
	GrokMediaEligibleExtraKey = "grok_media_eligible"

	// PAT mode labels and canonical/legacy key names identify Codex authentication
	// in existing OpenAI OAuth records for token and routing consumers.
	OpenAIAuthModePersonalAccessToken = "personalAccessToken"
	OpenAIAuthModeCredentialKey       = "auth_mode"
	OpenAIAuthModeLegacyCredentialKey = "openai_auth_mode"

	// Stored status labels control provider availability independently of the
	// scheduling switch and transient rate-limit, overload, or suspension windows.
	StatusActive   = "active"   // active provider status
	StatusDisabled = "disabled" // disabled provider status
	StatusError    = "error"    // recorded provider failure status

	// Platform aliases identify upstream implementations used by provider
	// classification, credential dispatch, and endpoint selection throughout the
	// gateway.
	PlatformAnthropic   = capability.PlatformAnthropic
	PlatformOpenAI      = capability.PlatformOpenAI
	PlatformGemini      = capability.PlatformGemini
	PlatformAntigravity = capability.PlatformAntigravity
	PlatformQoder       = capability.PlatformQoder
	PlatformGrok        = capability.PlatformGrok
	PlatformKimi        = capability.PlatformKimi
	PlatformZhipu       = capability.PlatformZhipu
	PlatformDeepseek    = capability.PlatformDeepseek
	PlatformOpenCode    = capability.PlatformOpenCode
	PlatformJev         = capability.PlatformJev

	// Kimi, Zhipu, and DeepSeek access modes select the provider's endpoint
	// defaults and usage policy. Coding mode remains a supplier classification
	// even through a custom relay.
	ProviderModePayG   = "payg"   // usage-based payment access mode
	ProviderModeCoding = "coding" // subscription coding-plan access mode

	// Compatibility protocol labels select upstream request formats for stored
	// legacy credentials and administrator tests before current route resolution.
	APIProtocolChatCompletions = "chat_completions"                   // legacy Chat Completions protocol
	APIProtocolAnthropic       = "anthropic"                          // legacy Anthropic Messages protocol
	APIProtocolResponses       = "responses"                          // legacy Responses text protocol
	APIProtocolAdaptive        = "adaptive"                           // automatic legacy protocol selection
	APIProtocolSystemOne       = string(capability.ProtocolSystemOne) // synchronous SystemOne decision protocol

	// Provider type aliases select the credential lifecycle and authentication
	// implementation used by platform-specific token, signature, and request
	// consumers.
	ProviderTypeOAuth      = capability.ProviderTypeOAuth      // refreshable OAuth credential type
	ProviderTypeSetupToken = capability.ProviderTypeSetupToken // stored Setup Token type
	ProviderTypeAPIKey     = capability.ProviderTypeAPIKey     // API Key authentication.
	ProviderTypeUpstream   = capability.ProviderTypeUpstream   // legacy relay credential type
	// ProviderTypeBedrock selects AWS credentials whose auth_mode chooses SigV4
	// signing or Bedrock API Key authentication at the downstream forwarder.
	ProviderTypeBedrock        = capability.ProviderTypeBedrock        // AWS Bedrock credential type
	ProviderTypeServiceAccount = capability.ProviderTypeServiceAccount // Google Service Account for Vertex AI.
	ProviderTypeCosy           = capability.ProviderTypeCosy           // Qoder COSY protocol.

	// Quota dimensions distinguish a provider's ordinary consumption window from
	// the independent Spark window used by a credential-sharing shadow record.
	QuotaDimensionGlobal = "global" // ordinary provider quota dimension
	QuotaDimensionSpark  = "spark"  // shadow Spark quota dimension

	// Antigravity privacy observations identify successful configuration or a
	// failed attempt for management display and the platform privacy-success check.
	AntigravityPrivacySet    = "privacy_set"        // successful Antigravity privacy setting
	AntigravityPrivacyFailed = "privacy_set_failed" // failed Antigravity privacy setting

	// OpenAI privacy observations distinguish disabled training, failed writes,
	// and an upstream Cloudflare block for management display and privacy
	// eligibility.
	PrivacyModeTrainingOff = "training_off"            // successful training opt-out setting
	PrivacyModeFailed      = "training_set_failed"     // failed training opt-out attempt
	PrivacyModeCFBlocked   = "training_set_cf_blocked" // Cloudflare-blocked training opt-out attempt

	// Legacy image labels identify basic and native image requirements passed to
	// SupportsOpenAIImageCapability when a caller filters the available provider
	// types.
	OpenAIImagesCapabilityBasic  OpenAIImagesCapability = "images-basic"  // basic image capability label
	OpenAIImagesCapabilityNative OpenAIImagesCapability = "images-native" // native image capability label
)

// Record holds administrator provider configuration and observed runtime state.
// Callers own mutable maps and relationships and keep them stable while accessors
// read them. Scalar accessors require a nonnil record unless documented
// otherwise. Missing clock or timezone hooks default to time.Now and
// time.LoadLocation. CloneRecord copies supported mutable fields before this
// snapshot is passed to another owner.
type Record struct {
	// QoderCatalogIdentityHint and UsageMonitorIdentityHint are derived by the
	// scheduler codec before dropping private credential and proxy details. Both
	// are internal transport metadata, never administrator input; full database
	// records leave them empty and derive identity from stored configuration.
	QoderCatalogIdentityHint string `json:"-"` // precomputed Qoder catalog identity
	UsageMonitorIdentityHint string `json:"-"` // precomputed usage-monitor identity
	// Proxy is the hydrated outbound proxy selected by ProxyID. Consumers that
	// need connection details require hydration before reading its URL or identity.
	Proxy *egress.Proxy `json:"-"`
	// Groups and ProviderGroups contain hydrated routing relationships owned by
	// this snapshot. A membership's Provider may refer back to the record itself;
	// CloneRecord redirects that link into its copied record graph.
	Groups         []*accessview.GroupConfig // hydrated provider routing groups
	ProviderGroups []GroupMembership         // hydrated provider-group association records
	ID             int64                     // persisted provider record identity
	Name           string                    // administrator provider display name
	// Notes contains optional administrator text. Configuration writes use nil
	// to clear the note, and NormalizeProviderNotes removes blank submitted text.
	Notes    *string
	Platform string // upstream platform dispatch identifier
	Type     string // upstream credential authentication type
	// Credentials and Extra contain caller-owned mutable configuration objects.
	// Their values can have decoded JSON or normalized Go representations, and
	// CloneValues isolates supported containers before passing them to another
	// owner.
	Credentials map[string]any `json:"-"` // authentication and provider routing settings
	Extra       map[string]any `json:"-"` // settings and maintained runtime observations
	// ProxyID selects the record-specific outbound proxy; nil leaves it unselected.
	// Proxy hydration supplies connection details separately from this stored ID.
	ProxyID *int64
	// ProxyFallbackOriginID and ProxyFallbackOriginName identify the pre-fallback
	// proxy for management display and reversion. Storage persists the ID and
	// hydrates its name when the origin proxy is still available.
	ProxyFallbackOriginID   *int64  // optional original proxy identity
	ProxyFallbackOriginName *string // hydrated original proxy name
	// Concurrency configures the provider request limit. A positive value also
	// supplies EffectiveLoadFactor when no positive separate load divisor exists.
	Concurrency int // zero means unlimited
	Priority    int // lower values receive scheduling preference
	// RateMultiplier adjusts provider-side costs. BillingRateMultiplier uses one
	// for missing or negative values and retains zero as a valid zero-cost choice.
	RateMultiplier *float64
	// LoadFactor supplies a positive scheduling load divisor when set; otherwise
	// EffectiveLoadFactor uses positive Concurrency, then one for unlimited records.
	LoadFactor   *int
	Status       string // persisted provider availability status
	ErrorMessage string // persisted provider status explanation
	// LastUsedAt is maintained by activity writes independently of configuration.
	// A nil value means storage has no last-used timestamp for this snapshot.
	LastUsedAt *time.Time
	// ExpiresAt and AutoPauseOnExpired control scheduling expiration together.
	// A missing expiration or disabled policy leaves that check inactive; an
	// enabled policy rejects equality with the record's current clock.
	ExpiresAt          *time.Time // optional provider expiration cutoff
	AutoPauseOnExpired bool       // enable expiration scheduling check
	CreatedAt          time.Time  // persisted record creation timestamp
	UpdatedAt          time.Time  // persisted record modification timestamp
	Schedulable        bool       // administrator scheduling admission switch
	// RateLimitedAt and RateLimitResetAt record the global rate-limit observation.
	// Admission reads a future RateLimitResetAt; a missing start timestamp alone
	// does not remove a still-active reset deadline.
	RateLimitedAt    *time.Time // optional global rate-limit observation time
	RateLimitResetAt *time.Time // optional global rate-limit reset time
	// OverloadUntil excludes scheduling before its timestamp. Equality or a nil
	// value leaves this particular runtime backoff inactive for the current record.
	OverloadUntil *time.Time
	// TempUnschedulableUntil and TempUnschedulableReason describe a temporary
	// suspension. The future deadline blocks both ordinary scheduling and shared
	// parent credential health, while the reason supports maintenance diagnostics.
	TempUnschedulableUntil  *time.Time // optional temporary suspension deadline
	TempUnschedulableReason string     // recorded temporary suspension explanation
	// QuotaAutoPaused is the runtime reader's observed threshold-pause result.
	// It is display metadata; IsSchedulable evaluates its own quota and window
	// rules.
	QuotaAutoPaused bool `json:"-"`
	// SessionWindowStart, SessionWindowEnd, and SessionWindowStatus describe the
	// maintained session usage window. Dedicated runtime writers update these
	// columns independently of ordinary administrator configuration replacement.
	SessionWindowStart  *time.Time // optional session usage window start
	SessionWindowEnd    *time.Time // optional session usage window end
	SessionWindowStatus string     // observed session usage window status
	// ParentProviderID makes this record a credential shadow when nonnil.
	// Credential resolution requires a nonshadow OpenAI OAuth parent for that link.
	ParentProviderID *int64
	// QuotaDimension identifies the usage window for this record. A blank value
	// defaults to global, while the Spark shadow preset selects its separate
	// dimension.
	QuotaDimension string
	GroupIDs       []int64 // persisted provider group associations
	// Now supplies provider-clock reads when injected; the now helper selects
	// time.Now when absent.
	Now func() time.Time `json:"-"`
	// LoadLocation resolves fixed quota timezone names when injected; the
	// loadLocation helper selects time.LoadLocation when absent.
	LoadLocation func(string) (*time.Location, error) `json:"-"`
}

// OpenAIImagesCapability names a legacy image requirement used by provider
// eligibility filters.
type OpenAIImagesCapability string

// GroupMembership represents one provider's association with a routing group.
type GroupMembership struct {
	ProviderID int64     // associated provider record identity
	GroupID    int64     // associated routing group identity
	CreatedAt  time.Time // association record creation timestamp
	// Provider and Group contain hydrated association endpoints when available.
	// Provider can point to the owning record itself, and CloneRecord redirects
	// that endpoint to its destination while copying group configuration separately.
	Provider *Record                 // optional hydrated provider endpoint
	Group    *accessview.GroupConfig // optional hydrated routing-group endpoint
}

// now returns the injected provider clock when one is configured. Nil records
// and absent clocks use time.Now, allowing runtime readers to share the
// production clock while tests supply a controlled time.
func (r *Record) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// loadLocation resolves name through the injected timezone loader or defaults
// to time.LoadLocation when r or r.LoadLocation is nil.
func (r *Record) loadLocation(name string) (*time.Location, error) {
	if r != nil && r.LoadLocation != nil {
		return r.LoadLocation(name)
	}
	return time.LoadLocation(name)
}

// String formats the provider identity for logs and fmt consumers. Nil
// receivers produce the nil-provider label, and populated records expose their
// ID while credential and proxy contents stay outside the formatted value.
func (r *Record) String() string {
	if r == nil {
		return "provider <nil>"
	}
	return fmt.Sprintf("provider (id=%d)", r.ID)
}

// GoString formats Go-syntax output through the provider's identity-only String
// method. Nil records use String's nil-provider label, and formatted values keep
// private credential and proxy contents out of diagnostic output.
func (r *Record) GoString() string { return r.String() }

// NormalizeProviderNotes returns a separately allocated trimmed note when text
// remains. Nil input and whitespace-only notes return nil, allowing management
// writes to clear notes instead of persisting an empty label.
func NormalizeProviderNotes(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// IsOpenAIPersonalAccessTokenAuthMode recognizes the supported Codex PAT mode
// spellings. Comparison trims whitespace and ignores case, accepting
// personalAccessToken and personal_access_token while other strings return
// false for ordinary OAuth handling.
func IsOpenAIPersonalAccessTokenAuthMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "personalaccesstoken", "personal_access_token":
		return true
	default:
		return false
	}
}

// IsActive reports whether the provider has the persisted active status.
func (r *Record) IsActive() bool {
	return r.Status == StatusActive
}

// BillingRateMultiplier returns the multiplier used for provider-side costs.
// Nil records, missing multipliers, and negative values select one; a
// configured zero remains valid and produces zero provider charges.
func (r *Record) BillingRateMultiplier() float64 {
	if r == nil || r.RateMultiplier == nil {
		return 1.0
	}
	if *r.RateMultiplier < 0 {
		return 1.0
	}
	return *r.RateMultiplier
}

// EffectiveLoadFactor returns the scheduling load divisor for this provider.
// A positive configured LoadFactor takes priority over positive Concurrency;
// otherwise the result is one, including for a nil record or unlimited
// concurrency.
//
// A record with Concurrency five and LoadFactor twenty returns twenty while
// retaining five in its concurrency field. Without a positive override, that
// record returns five. Scheduler and Ops load readers receive this selected
// divisor separately from the record's configured capacity, so changing the
// divisor does not rewrite the concurrency limit.
func (r *Record) EffectiveLoadFactor() int {
	if r == nil {
		return 1
	}
	if r.LoadFactor != nil && *r.LoadFactor > 0 {
		return *r.LoadFactor
	}
	if r.Concurrency > 0 {
		return r.Concurrency
	}
	return 1
}

// IsSchedulable combines persisted availability, expiration policy, temporary
// windows, and applicable quota checks for this record. The receiver is nonnil,
// and its configured clock supplies the time used for expiration and
// runtime-window comparisons.
//
// Inactive status or a disabled scheduling switch rejects the provider first.
// Expiration blocks only when AutoPauseOnExpired is enabled; overload, global
// rate-limit, and temporary suspension block until their respective future
// timestamps elapse. API Key and Bedrock types also evaluate IsQuotaExceeded,
// while other types keep their platform-specific quota decisions outside this
// check. Reaching a window's end permits the subsequent checks to decide
// availability.
func (r *Record) IsSchedulable() bool {
	if !r.IsActive() || !r.Schedulable {
		return false
	}
	now := r.now()
	if r.AutoPauseOnExpired && r.ExpiresAt != nil && !now.Before(*r.ExpiresAt) {
		return false
	}
	if r.OverloadUntil != nil && now.Before(*r.OverloadUntil) {
		return false
	}
	if r.RateLimitResetAt != nil && now.Before(*r.RateLimitResetAt) {
		return false
	}
	if r.TempUnschedulableUntil != nil && now.Before(*r.TempUnschedulableUntil) {
		return false
	}
	if r.IsAPIKeyOrBedrock() && r.IsQuotaExceeded() {
		return false
	}
	return true
}

// IsCredentialUsableForShadow evaluates the parent state shared by shadow
// authentication and transport. Nil or inactive parents fail; expiration
// matters when AutoPauseOnExpired is enabled, and a future temporary cooldown
// also fails.
//
// The parent clock supplies one time for these comparisons. A shadow can still
// use active credentials while the parent is manually unschedulable, globally
// rate limited, or overloaded, because those fields govern the parent's
// separate scheduling state. The shadow's own switch and quota are checked by
// its scheduling path after parent credential health succeeds.
func (r *Record) IsCredentialUsableForShadow() bool {
	if r == nil || !r.IsActive() {
		return false
	}
	now := r.now()
	if r.AutoPauseOnExpired && r.ExpiresAt != nil && !now.Before(*r.ExpiresAt) {
		return false
	}
	if r.TempUnschedulableUntil != nil && now.Before(*r.TempUnschedulableUntil) {
		return false
	}
	return true
}

// IsRateLimited reports whether the provider's global rate-limit reset lies in
// the future. A missing timestamp returns false, and equality with the provider
// clock means that this rate-limit window has elapsed.
func (r *Record) IsRateLimited() bool {
	if r.RateLimitResetAt == nil {
		return false
	}
	return r.now().Before(*r.RateLimitResetAt)
}

// IsOAuth reports whether the record uses OAuth or Setup Token credentials.
func (r *Record) IsOAuth() bool {
	return r.Type == ProviderTypeOAuth || r.Type == ProviderTypeSetupToken
}

// IsPrivacySet reports whether the platform's saved privacy result is
// successful. OpenAI requires training_off and Antigravity requires
// privacy_set; other platforms return true because this check imposes no saved
// privacy requirement for them.
func (r *Record) IsPrivacySet() bool {
	switch r.Platform {
	case PlatformOpenAI:
		return r.GetExtraString("privacy_mode") == PrivacyModeTrainingOff
	case PlatformAntigravity:
		return r.GetExtraString("privacy_mode") == AntigravityPrivacySet
	default:
		return true
	}
}

// IsGemini reports whether the provider belongs to the Gemini platform.
func (r *Record) IsGemini() bool {
	return r.Platform == PlatformGemini
}

// IsGrok reports whether the provider belongs to the Grok platform.
func (r *Record) IsGrok() bool {
	return r.Platform == PlatformGrok
}

// IsGrokOAuth reports whether the provider combines Grok platform and OAuth
// authentication.
func (r *Record) IsGrokOAuth() bool {
	return r.IsGrok() && r.Type == ProviderTypeOAuth
}

// IsOpenAICompatible identifies OpenAI, Grok, Kimi, Zhipu, and DeepSeek, and
// OpenCode gateway families. Nil records return false; the platform
// classification leaves provider-type and endpoint eligibility to the
// route-specific checks.
func (r *Record) IsOpenAICompatible() bool {
	return r != nil && IsOpenAICompatible(r.Platform)
}

// IsOpenAICompatible classifies OpenAI, Grok, Kimi, Zhipu, and DeepSeek, and
// OpenCode as the platforms whose text requests use the OpenAI-compatible
// gateway family.
func IsOpenAICompatible(platform string) bool {
	return platform == PlatformOpenAI || platform == PlatformGrok || platform == PlatformOpenCode || platformRuleRegistry[platform].text != nil
}

// GeminiOAuthType returns the trimmed channel label for Gemini OAuth records.
// An absent label with a nonblank project_id infers code_assist; other
// platform/type combinations return an empty string before reading that
// channel.
func (r *Record) GeminiOAuthType() string {
	if r.Platform != PlatformGemini || r.Type != ProviderTypeOAuth {
		return ""
	}
	oauthType := strings.TrimSpace(r.GetCredential("oauth_type"))
	if oauthType == "" && strings.TrimSpace(r.GetCredential("project_id")) != "" {
		return "code_assist"
	}
	return oauthType
}

// GeminiTierID returns the provider's trimmed tier_id credential for quota
// selection.
func (r *Record) GeminiTierID() string {
	return strings.TrimSpace(r.GetCredential("tier_id"))
}

// IsGeminiThirdPartyProvider identifies Gemini API Keys whose provider_type
// marks a third-party supplier. The label comparison ignores case; nil records
// and other authentication types return false for this local official-quota
// classification.
func (r *Record) IsGeminiThirdPartyProvider() bool {
	if r == nil || r.Platform != PlatformGemini || r.Type != ProviderTypeAPIKey {
		return false
	}
	return strings.EqualFold(r.GetCredential(GeminiProviderTypeCredentialKey), GeminiProviderTypeThirdParty)
}

// HasGeminiThirdPartyBaseURL reports whether a third-party Gemini API Key names
// a parsed nonofficial endpoint. It requires a scheme and hostname, then
// compares the hostname to GeminiOfficialAPIHost; broader transport validation
// belongs to the request path.
func (r *Record) HasGeminiThirdPartyBaseURL() bool {
	if !r.IsGeminiThirdPartyProvider() {
		return false
	}
	baseURL := strings.TrimSpace(r.GetCredential("base_url"))
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return false
	}
	return !strings.EqualFold(parsed.Hostname(), GeminiOfficialAPIHost)
}

// IsGeminiCodeAssist reports whether a Gemini OAuth record selects code_assist.
// GeminiOAuthType can infer that label from an absent oauth_type and a nonblank
// project_id; other platforms, types, and supplied channel labels return false.
func (r *Record) IsGeminiCodeAssist() bool {
	if r.Platform != PlatformGemini || r.Type != ProviderTypeOAuth {
		return false
	}
	oauthType := r.GeminiOAuthType()
	if oauthType == "" {
		return strings.TrimSpace(r.GetCredential("project_id")) != ""
	}
	return oauthType == "code_assist"
}

// IsGeminiGoogleOne identifies a Gemini OAuth record on the google_one channel.
func (r *Record) IsGeminiGoogleOne() bool {
	return r.Platform == PlatformGemini && r.Type == ProviderTypeOAuth && r.GeminiOAuthType() == "google_one"
}

// CanGetUsage reports whether this record has the OAuth provider type.
func (r *Record) CanGetUsage() bool {
	return r.Type == ProviderTypeOAuth
}

// GetCredential returns a string representation of a supported credential
// value. Missing maps, keys, nil values, and unsupported dynamic types produce
// an empty string for accessor consumers.
//
// Strings retain their bytes and json.Number retains its decimal text. Integer
// forms become decimal strings, while representable float64 values lose their
// fractions through int64 conversion before formatting. This accommodates historic numeric
// timestamps and metadata alongside string credentials, but callers that need
// fractional values or numeric validation use a dedicated parser instead of
// relying on this string accessor.
func (r *Record) GetCredential(key string) string {
	if r.Credentials == nil {
		return ""
	}
	v, ok := r.Credentials[key]
	if !ok || v == nil {
		return ""
	}

	// Stored metadata can arrive through ordinary JSON or UseNumber decoding.
	// Keep supported numeric text available to timestamp and version consumers
	// while leaving unsupported objects as an empty credential value.
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		// Retain the decoded decimal text for consumers that parse Unix timestamps.
		return val.String()
	case float64:
		// Historical JSON timestamps use whole seconds; conversion drops fractions
		// before formatting, so this branch is unsuitable for fractional credentials.
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case int:
		return strconv.Itoa(val)
	default:
		return ""
	}
}

// GetCredentialAsTime parses a supported credential as RFC3339 text or decimal
// Unix seconds. RFC3339 is tried first; missing or unparseable values return
// nil so callers can distinguish an absent usable timestamp.
func (r *Record) GetCredentialAsTime(key string) *time.Time {
	s := r.GetCredential(key)
	if s == "" {
		return nil
	}
	// Try the timestamp's structured text before decimal seconds. This keeps
	// an existing RFC3339 offset usable while allowing older Unix-second
	// credentials to follow the numeric parse below.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	// A decimal credential is interpreted in Unix seconds. Failed numeric
	// parsing leaves no usable timestamp, matching the caller's missing-expiry
	// path instead of inventing a time from metadata.
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		t := time.Unix(ts, 0)
		return &t
	}
	return nil
}

// GetCredentialAsInt64 reads supported integer representations for fields such
// as token versions. Nil records, missing values, unsupported types, and failed
// string or json.Number parsing return zero. Representable float64 values lose
// their fractional component through integer conversion.
func (r *Record) GetCredentialAsInt64(key string) int64 {
	if r == nil || r.Credentials == nil {
		return 0
	}
	val, ok := r.Credentials[key]
	if !ok || val == nil {
		return 0
	}
	switch v := val.(type) {
	case int64:
		return v
	case float64:
		// Numeric metadata is interpreted as whole version units after truncation.
		return int64(v)
	case int:
		return int64(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return i
		}
	}
	// Missing or unreadable integer metadata supplies the absent-version value
	// used by refresh consumers; a failed parse cannot manufacture a newer version.
	return 0
}

// GetExtraString reads a string value from the provider's Extra object. Missing
// maps, absent keys, and nonstrings return empty; valid strings retain their
// bytes so consumers decide whether trimming is appropriate.
func (r *Record) GetExtraString(key string) string {
	if r.Extra == nil {
		return ""
	}
	if v, ok := r.Extra[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetExtraStringDefault returns the saved nonempty string or the supplied
// default. Whitespace-only strings remain nonempty here, so callers requiring
// trimmed input normalize it before assigning meaning to the value.
func (r *Record) GetExtraStringDefault(key, defaultVal string) string {
	if v := r.GetExtraString(key); v != "" {
		return v
	}
	return defaultVal
}

// GetExtraFloat64 parses a provider Extra number through ParseExtraFloat64.
// Missing entries return zero, and supported dynamic representations follow
// that parser's conversion rules for quotas and other numeric settings.
func (r *Record) GetExtraFloat64(key string) float64 {
	if r.Extra == nil {
		return 0
	}
	if v, ok := r.Extra[key]; ok {
		return ParseExtraFloat64(v)
	}
	return 0
}

// GetExtraTime reads an RFC3339 timestamp through ParseExtraTime from provider
// Extra. Missing entries return the zero time, letting quota consumers treat an
// absent stored window start as expired.
func (r *Record) GetExtraTime(key string) time.Time {
	if r.Extra == nil {
		return time.Time{}
	}
	if v, ok := r.Extra[key]; ok {
		return ParseExtraTime(v)
	}
	return time.Time{}
}

// GetExtraBool reads a boolean value from provider Extra. Missing maps, absent
// keys, and nonboolean values return false, so decoded strings or numbers do
// not enable a boolean setting.
func (r *Record) GetExtraBool(key string) bool {
	if r.Extra == nil {
		return false
	}
	if v, ok := r.Extra[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// GetExtraInt converts ParseExtraFloat64's Extra result to an integer. Missing
// entries return zero, and fractional values truncate through Go's conversion
// rules; callers validate the acceptable range for their setting.
func (r *Record) GetExtraInt(key string) int {
	if r.Extra == nil {
		return 0
	}
	if v, ok := r.Extra[key]; ok {
		// Calendar readers consume integer clock values. Fractional Extra input
		// loses its subunit portion before those callers apply their reset-date
		// validation or calculation rules.
		return int(ParseExtraFloat64(v))
	}
	return 0
}

// IsBedrock identifies Anthropic records using the AWS Bedrock provider type.
func (r *Record) IsBedrock() bool {
	return r.Platform == PlatformAnthropic && r.Type == ProviderTypeBedrock
}

// IsBedrockAPIKey identifies Bedrock records selecting the exact apikey
// authentication mode.
func (r *Record) IsBedrockAPIKey() bool {
	return r.IsBedrock() && r.GetCredential("auth_mode") == "apikey"
}

// IsAPIKeyOrBedrock identifies the provider types used by generic quota and
// pool checks.
func (r *Record) IsAPIKeyOrBedrock() bool {
	return r.Type == ProviderTypeAPIKey || r.Type == ProviderTypeBedrock
}

// IsOpenAI reports whether the record belongs to the OpenAI platform.
func (r *Record) IsOpenAI() bool {
	return r.Platform == PlatformOpenAI
}

// IsAnthropic reports whether the record belongs to the Anthropic platform.
func (r *Record) IsAnthropic() bool {
	return r.Platform == PlatformAnthropic
}

// IsQoder reports whether the record belongs to the Qoder platform.
func (r *Record) IsQoder() bool {
	return r.Platform == PlatformQoder
}

// IsQoderCosy identifies Qoder records using the COSY credential type.
func (r *Record) IsQoderCosy() bool {
	return r.IsQoder() && r.Type == ProviderTypeCosy
}

// IsOpenAIOAuth identifies OpenAI records using the refreshable OAuth provider
// type.
func (r *Record) IsOpenAIOAuth() bool {
	return r.IsOpenAI() && r.Type == ProviderTypeOAuth
}

// IsOpenAIOAuthLike identifies OpenAI OAuth and Setup Token inference records.
// Nil records return false; the shared forwarding classification does not grant
// Setup Tokens the refresh lifecycle used by full OAuth credentials.
func (r *Record) IsOpenAIOAuthLike() bool {
	return r != nil && r.IsOpenAI() && (r.Type == ProviderTypeOAuth || r.Type == ProviderTypeSetupToken)
}

// UsesOpenAICodexProtocol accepts every OAuth record through its compatibility
// branch and OpenAI Setup Token records through IsOpenAIOAuthLike. Nil records
// return false; callers use separate platform checks when their route requires
// an OpenAI provider.
func (r *Record) UsesOpenAICodexProtocol() bool {
	return r != nil && (r.Type == ProviderTypeOAuth || r.IsOpenAIOAuthLike())
}

// IsOpenAIChatGPTSubscription classifies an OpenAI OAuth record by trimmed,
// case-insensitive plan_type. Missing, free, and abnormal labels return false;
// every other nonblank plan label counts as a subscription for this accessor.
func (r *Record) IsOpenAIChatGPTSubscription() bool {
	if !r.IsOpenAIOAuth() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.GetCredential("plan_type"))) {
	case "", "free", "abnormal":
		return false
	default:
		return true
	}
}

// IsOpenAIPersonalAccessToken identifies OpenAI OAuth using a supported PAT
// auth-mode spelling. Canonical and legacy credential keys are both checked, so
// either recognized value selects PAT even when the other is present.
func (r *Record) IsOpenAIPersonalAccessToken() bool {
	if r == nil || !r.IsOpenAIOAuth() {
		return false
	}
	return IsOpenAIPersonalAccessTokenAuthMode(r.GetCredential(OpenAIAuthModeCredentialKey)) ||
		IsOpenAIPersonalAccessTokenAuthMode(r.GetCredential(OpenAIAuthModeLegacyCredentialKey))
}

// IsOpenAIApiKey identifies OpenAI records using the API Key provider type.
func (r *Record) IsOpenAIApiKey() bool {
	return r.IsOpenAI() && r.Type == ProviderTypeAPIKey
}

// GetProviderMode returns the platform's interpreted access mode for endpoint
// and usage lookup. Nil records and platforms without mode declarations return
// empty; missing, blank, or unknown values select the platform's read default.
func (r *Record) GetProviderMode() string {
	rules := r.rules().text
	if rules == nil {
		return ""
	}
	mode := strings.TrimSpace(r.GetCredential("provider_mode"))
	if _, found := rules.modes[mode]; found {
		return mode
	}
	return rules.defaultMode
}

// IsCodingPlan reports whether the platform's interpreted access mode is
// coding. Nil records and platforms without mode declarations return false;
// mode reads retain historical compatibility independently of save validation.
func (r *Record) IsCodingPlan() bool {
	return r.GetProviderMode() == ProviderModeCoding
}

// GetCodingPlanProvider returns the supplier identity declared for coding quota
// windows. Nil records and modes without coding usage return empty, including
// historical modes that remain readable but cannot be saved.
func (r *Record) GetCodingPlanProvider() string {
	return r.textMode().codingProvider
}

// GetOpenAIAccessToken reads access_token for the OpenAI platform and returns
// empty otherwise. It does not restrict the provider type; callers select the
// authentication path before consuming this credential.
func (r *Record) GetOpenAIAccessToken() string {
	if !r.IsOpenAI() {
		return ""
	}
	return r.GetCredential("access_token")
}

// GetOpenAIRefreshToken returns the saved refresh_token for an OpenAI OAuth
// record. Other platforms or provider types return empty, allowing callers to
// keep Setup Token inference separate from full OAuth renewal.
func (r *Record) GetOpenAIRefreshToken() string {
	if !r.IsOpenAIOAuth() {
		return ""
	}
	return r.GetCredential("refresh_token")
}

// GetGrokAccessToken reads access_token for a Grok record and returns empty
// otherwise. The accessor checks platform while the consuming token path
// decides its provider-type and refresh requirements.
func (r *Record) GetGrokAccessToken() string {
	if !r.IsGrok() {
		return ""
	}
	return r.GetCredential("access_token")
}

// GetGrokRefreshToken reads refresh_token for a Grok OAuth record. Other
// platform/type combinations return empty, allowing refresh consumers to test
// availability after choosing the appropriate credential lifecycle.
func (r *Record) GetGrokRefreshToken() string {
	if !r.IsGrokOAuth() {
		return ""
	}
	return r.GetCredential("refresh_token")
}

// GetOpenAIApiKey reads api_key for an OpenAI API Key record. Other
// platform/type combinations return empty; OpenAI-compatible Kimi, Zhipu, and
// DeepSeek forwarding uses the separate GetOpenAIProtocolAPIKey accessor for
// its platform classification.
func (r *Record) GetOpenAIApiKey() string {
	if !r.IsOpenAIApiKey() {
		return ""
	}
	return r.GetCredential("api_key")
}

// GetOpenAIProtocolAPIKey returns an API Key for OpenAI, Kimi, Zhipu, and
// DeepSeek, or OpenCode forwarding. Nil records and compatible records using
// another authentication type return empty; OpenAI delegates to its
// platform-specific API Key accessor.
func (r *Record) GetOpenAIProtocolAPIKey() string {
	if r == nil {
		return ""
	}
	if r.HasProtocolEndpointDefaults() || r.IsOpenCode() {
		if r.Type != ProviderTypeAPIKey {
			return ""
		}
		return r.GetCredential("api_key")
	}
	return r.GetOpenAIApiKey()
}

// GetOpenAIUserAgent returns the saved user_agent for an OpenAI record. Other
// platforms return empty, and the saved string retains its formatting for the
// outgoing header consumer to interpret.
func (r *Record) GetOpenAIUserAgent() string {
	if !r.IsOpenAI() {
		return ""
	}
	return r.GetCredential("user_agent")
}

// GetChatGPTAccountID returns the saved account identifier for OpenAI
// OAuth-like inference records. Other platform/type combinations return empty;
// absent credentials also remain empty for callers that can omit this identity
// hint.
func (r *Record) GetChatGPTAccountID() string {
	if !r.IsOpenAIOAuthLike() {
		return ""
	}
	return r.GetCredential("chatgpt_account_id")
}

// IsChatGPTAccountFedRAMP interprets the account environment flag for OpenAI
// OAuth-like records. Booleans, supported boolean text, json.Number boolean
// text, and nonzero numeric values are recognized; nil, missing, unsupported,
// or unparseable values return false.
func (r *Record) IsChatGPTAccountFedRAMP() bool {
	if !r.IsOpenAIOAuthLike() || r.Credentials == nil {
		return false
	}
	v, ok := r.Credentials["chatgpt_account_is_fedramp"]
	if !ok || v == nil {
		return false
	}
	// Stored environment metadata mixes boolean text with numeric flags. Failed
	// text parsing supplies false so unsupported spellings cannot enable the
	// FedRAMP account header used by OpenAI request consumers.
	switch value := v.(type) {
	case bool:
		return value
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		return err == nil && parsed
	case json.Number:
		parsed, err := strconv.ParseBool(value.String())
		return err == nil && parsed
	case float64:
		return value != 0
	case int:
		return value != 0
	case int64:
		return value != 0
	default:
		return false
	}
}

// GetOpenAIDeviceID returns the trimmed openai_device_id Extra value for OpenAI
// OAuth. Other platform/type combinations and missing values return empty,
// letting the fingerprint consumer select its seed-based installation
// identifier.
func (r *Record) GetOpenAIDeviceID() string {
	if !r.IsOpenAIOAuth() {
		return ""
	}
	return strings.TrimSpace(r.GetExtraString("openai_device_id"))
}

// GrokMediaEligibilityOverride returns a boolean media selection plus whether
// it was usable input. Missing, null, and nonboolean values return configured
// false; a typed false remains a configured disabling choice.
func GrokMediaEligibilityOverride(extra map[string]any) (bool, bool) {
	if extra == nil {
		return false, false
	}
	raw, exists := extra[GrokMediaEligibleExtraKey]
	if !exists || raw == nil {
		return false, false
	}
	value, ok := raw.(bool)
	return value, ok
}

// OpenAIWorkloadCapabilitySet parses legacy workload containers and retains
// whether they were configured. Missing or null values return nil and false;
// empty supported containers also mean unconfigured for OpenAI OAuth-like
// records.
//
// API Key empty containers instead return an empty configured set. Nonempty
// arrays keep trimmed lowercase string labels, and maps keep keys whose values
// are true booleans. Unsupported supplied values also produce an empty set with
// true presence, so callers can distinguish absent configuration from a
// supplied unusable selection before applying workload defaults.
func (r *Record) OpenAIWorkloadCapabilitySet() (map[string]bool, bool) {
	if r == nil || r.Credentials == nil {
		return nil, false
	}
	raw, found := r.Credentials[OpenAIWorkloadCapabilitiesCredentialKey]
	if !found || raw == nil {
		return nil, false
	}

	result := make(map[string]bool)
	add := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return
		}
		result[value] = true
	}

	// Legacy OAuth-like empty containers request defaults, but an API Key empty
	// set is a configured disabling choice. Retain this presence distinction
	// when the same container representation appears under different provider types.
	switch capabilities := raw.(type) {
	case []any:
		if len(capabilities) == 0 && r.IsOpenAIOAuthLike() {
			return nil, false
		}
		for _, item := range capabilities {
			if value, ok := item.(string); ok {
				add(value)
			}
		}
	case []string:
		if len(capabilities) == 0 && r.IsOpenAIOAuthLike() {
			return nil, false
		}
		for _, value := range capabilities {
			add(value)
		}
	case map[string]any:
		if len(capabilities) == 0 && r.IsOpenAIOAuthLike() {
			return nil, false
		}
		for key, value := range capabilities {
			enabled, ok := value.(bool)
			if ok && enabled {
				add(key)
			}
		}
	case map[string]bool:
		if len(capabilities) == 0 && r.IsOpenAIOAuthLike() {
			return nil, false
		}
		for key, enabled := range capabilities {
			if enabled {
				add(key)
			}
		}
	}

	return result, true
}

// SupportsOpenAIImageCapability evaluates the legacy image-capability filter.
// An empty requirement passes every platform; known basic and native labels
// require OpenAI OAuth, Setup Token, or API Key, while unknown labels pass an
// OpenAI record.
func (r *Record) SupportsOpenAIImageCapability(capability OpenAIImagesCapability) bool {
	if capability == "" {
		return true
	}
	if !r.IsOpenAI() {
		return false
	}
	switch capability {
	case OpenAIImagesCapabilityBasic, OpenAIImagesCapabilityNative:
		return r.Type == ProviderTypeOAuth || r.Type == ProviderTypeSetupToken || r.Type == ProviderTypeAPIKey
	default:
		return true
	}
}

// IsOveragesEnabled reads the Antigravity allow_overages boolean from Extra.
// Other platforms, missing maps or keys, and nonboolean values return false,
// leaving the provider on its normal credit allowance path.
func (r *Record) IsOveragesEnabled() bool {
	if r.Platform != PlatformAntigravity {
		return false
	}
	if r.Extra == nil {
		return false
	}
	if v, ok := r.Extra["allow_overages"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// IsShadow reports whether a provider record names a credential parent. Nil
// records return false, and a nonnil parent pointer counts before the linked
// parent's identity or health has been validated.
func (r *Record) IsShadow() bool { return r != nil && r.ParentProviderID != nil }

// IsCredentialShadow identifies records whose credential operations resolve
// through their parent. Nil records return false; this classification follows
// any nonnil ParentProviderID, leaving parent validation to ResolveCredentialRecord.
func (r *Record) IsCredentialShadow() bool { return r.IsShadow() }

// QuotaDimensionOrDefault returns the provider's saved usage dimension. Nil
// records and blank or whitespace-only dimensions select global; any nonblank
// value retains its original text for the usage consumer.
func (r *Record) QuotaDimensionOrDefault() string {
	if r == nil || strings.TrimSpace(r.QuotaDimension) == "" {
		return QuotaDimensionGlobal
	}
	return r.QuotaDimension
}
