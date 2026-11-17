package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/routing/modelmap"
)

// record.go represents provider configuration and runtime observations and
// evaluates their routing and scheduling rules.
// A provider record carries both administrator configuration and runtime
// observations consumed by scheduling, forwarding, and management display. Its
// accessors interpret stored JSON representations while retaining distinctions
// that affect routing decisions, such as omitted retry overrides, explicitly
// empty selections, and legacy identity mappings.
//
// Scheduling combines active state, its manual switch, expiration policy,
// temporary windows, and applicable quota dimensions. Shadow credential health
// deliberately reads the shared parent's credential conditions separately from
// that parent's global scheduling state. Fixed quotas use local calendar resets
// and an injected clock, while rolling quotas use real elapsed time.
// Configuration maps and hydrated relationships belong to the caller's
// snapshot; CloneRecord supplies supported mutable copies when ownership
// crosses a cache or request. The helpers read these values and leave
// persistence with the dedicated configuration, health, and billing writers.

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

	// Compaction mode values keep legacy and native V2 administrator switches
	// independent, with each accessor treating force_off as its disabling value.
	// OpenAICompactModeForceOn enables the corresponding compaction capability.
	OpenAICompactModeForceOn = "force_on"
	// OpenAICompactModeForceOff disables the corresponding compaction capability.
	OpenAICompactModeForceOff            = "force_off"
	OpenAINativeCompactionV2ModeExtraKey = "openai_native_compaction_v2_mode"

	// Pool-mode counts supply the ordinary budget for additional attempts against
	// one provider. Gateway failover evaluates error-specific caps and independent
	// deadline rules before deciding whether another attempt is allowed.
	DefaultPoolModeRetryCount = 3  // missing same-provider retry count
	MaxPoolModeRetryCount     = 10 // maximum same-provider retry count

	// OpenAI OAuth policy values select unrestricted clients, Codex identities,
	// or a TLS-router match after administrator and legacy settings are resolved.
	// OpenAIOAuthClientPolicyAny allows any client to use the OpenAI OAuth provider.
	OpenAIOAuthClientPolicyAny = "any"
	// OpenAIOAuthClientPolicyCodexOnly restricts access to official Codex clients.
	OpenAIOAuthClientPolicyCodexOnly = "codex_only"
	// OpenAIOAuthClientPolicyTLSRouterMatchedOnly requires a UA matched by the TLS
	// router.
	OpenAIOAuthClientPolicyTLSRouterMatchedOnly = "tls_router_matched_only"

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

	// DefaultJevBaseURL supplies the SystemOne service root when a provider has
	// no configured endpoint override; operation builders append their request path.
	DefaultJevBaseURL = "https://api.typesafe.ai"

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

	// Notification labels construct dimension-specific Extra keys and supply the
	// missing threshold interpretation used by daily, weekly, monthly, and total quota
	// readers.
	thresholdTypeFixed = "fixed"   // fixed quota notification threshold
	quotaDimDaily      = "daily"   // daily quota notification dimension
	quotaDimWeekly     = "weekly"  // weekly quota notification dimension
	quotaDimMonthly    = "monthly" // monthly quota notification dimension
	quotaDimTotal      = "total"   // total quota notification dimension
)

// DefaultPoolModeRetryableStatusCodes supplies the shared membership list for
// pool-mode retry-status decisions. A nil GetPoolModeRetryStatusCodes result
// delegates membership to this list; every configured []any value uses its own
// normalized result.
// Runtime readers share the exported slice, so callers synchronize any changes
// to these defaults with request handling.
var DefaultPoolModeRetryableStatusCodes = []int{401, 403, 429}

// Record holds administrator provider configuration and observed runtime state.
// Callers own mutable maps and relationships and keep them stable while accessors
// read them. Scalar accessors require a nonnil record unless documented
// otherwise. Fixed-window readers require LoadLocation when their stored start
// is nonzero. CloneRecord copies supported mutable fields before this snapshot
// is passed to another owner.
type Record struct {
	// QoderCatalogIdentityHint is derived by the scheduler codec before dropping
	// credentials. It is private transport metadata, never administrator input;
	// full database records leave it empty and derive identity from credentials.
	QoderCatalogIdentityHint string `json:"-"`
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
	// Now supplies provider-clock reads when injected. The now
	// helper selects time.Now when absent; rolling periods use time.Since
	// separately.
	Now func() time.Time `json:"-"`
	// LoadLocation resolves fixed quota timezone names when a stored start is
	// nonzero. A zero start expires before lookup; provider storage installs the
	// configured loader for hydrated records.
	LoadLocation func(string) (*time.Location, error) `json:"-"`
}

// TempUnschedulableRule selects an upstream failure that triggers a temporary
// scheduling pause.
type TempUnschedulableRule struct {
	// ErrorCode and Keywords select a failure together. The health matcher
	// requires the exact status and a usable keyword in the response body;
	// GetTempUnschedulableRules discards rules missing either required component.
	ErrorCode int      `json:"error_code"` // upstream HTTP status code
	Keywords  []string `json:"keywords"`   // response text match keywords
	// DurationMinutes sets the pause length. GetTempUnschedulableRules skips a
	// stored rule when this converted duration is nonpositive, before health handling.
	DurationMinutes int    `json:"duration_minutes"` // minutes
	Description     string `json:"description"`      // administrator rule display description
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
	tierID := strings.TrimSpace(r.GetCredential("tier_id"))
	return tierID
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
	// path instead of inventing a time from malformed metadata.
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

// IsTempUnschedulableEnabled reads the temporary scheduling-rule switch from
// Credentials. Missing maps, absent values, nil values, and nonboolean
// representations return false, so a string such as true does not enable these
// rules.
func (r *Record) IsTempUnschedulableEnabled() bool {
	if r.Credentials == nil {
		return false
	}
	raw, ok := r.Credentials["temp_unschedulable_enabled"]
	if !ok || raw == nil {
		return false
	}
	enabled, ok := raw.(bool)
	return ok && enabled
}

// GetTempUnschedulableRules parses the JSON-shaped temporary suspension rules
// stored in Credentials. The top-level value must be []any; missing or other
// container types return nil.
//
// Each object supplies status, keywords, duration, and description through
// their dedicated parsers. Entries with a nonpositive status or duration, or no
// usable keywords, are skipped. Valid entries retain input order for the
// health-rule matcher, while an accepted empty or wholly rejected array returns
// an empty allocated rule slice.
func (r *Record) GetTempUnschedulableRules() []TempUnschedulableRule {
	if r.Credentials == nil {
		return nil
	}
	raw, ok := r.Credentials["temp_unschedulable_rules"]
	if !ok || raw == nil {
		return nil
	}

	arr, ok := raw.([]any)
	if !ok {
		return nil
	}

	rules := make([]TempUnschedulableRule, 0, len(arr))
	for _, item := range arr {
		entry, ok := item.(map[string]any)
		if !ok || entry == nil {
			continue
		}

		rule := TempUnschedulableRule{
			ErrorCode:       ParseExtraInt(entry["error_code"]),
			Keywords:        ParseTempUnschedStrings(entry["keywords"]),
			DurationMinutes: ParseExtraInt(entry["duration_minutes"]),
			Description:     ParseTempUnschedString(entry["description"]),
		}

		// The matcher needs a status, usable keyword, and positive pause duration.
		// Skip incomplete stored objects so they cannot create an unusable
		// suspension result in later health handling.
		if rule.ErrorCode <= 0 || rule.DurationMinutes <= 0 || len(rule.Keywords) == 0 {
			continue
		}

		rules = append(rules, rule)
	}

	return rules
}

// ParseTempUnschedString returns trimmed string input for suspension-rule
// fields. Unsupported dynamic values return an empty string, allowing keyword
// and description parsing to ignore unusable JSON values without manufacturing
// text.
func ParseTempUnschedString(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// ParseTempUnschedStrings returns nonblank trimmed strings from []string or
// []any input. Mixed arrays skip nonstrings, and missing or unsupported
// containers return nil; supplied supported arrays can produce an empty result.
func ParseTempUnschedStrings(value any) []string {
	if value == nil {
		return nil
	}

	var raw []string
	switch v := value.(type) {
	case []string:
		raw = v
	case []any:
		raw = make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				raw = append(raw, s)
			}
		}
	default:
		return nil
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s := strings.TrimSpace(item)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

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

// StringMappingFromRaw returns a copied string-to-string model mapping from
// either supported map representation. Mixed map[string]any entries retain just
// string values, and empty or unusable input returns nil for the mapping
// consumer.
func StringMappingFromRaw(raw any) map[string]string {
	switch mapping := raw.(type) {
	case map[string]any:
		if len(mapping) == 0 {
			return nil
		}
		result := make(map[string]string, len(mapping))
		for key, value := range mapping {
			if str, ok := value.(string); ok {
				result[key] = str
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	case map[string]string:
		if len(mapping) == 0 {
			return nil
		}
		result := make(map[string]string, len(mapping))
		maps.Copy(result, mapping)
		return result
	default:
		return nil
	}
}

// ResolveRequestedModelInMapping delegates exact and trailing-wildcard lookup
// to modelmap.Resolve. Exact keys win, wildcard matches use the longest pattern
// with lexical ties, and an unmatched request returns its model with matched
// false.
func ResolveRequestedModelInMapping(mapping map[string]string, requestedModel string) (mappedModel string, matched bool) {
	return modelmap.Resolve(mapping, requestedModel)
}

// ExtractFinalModelWhitelist derives the legacy whitelist from exact identity
// mapping entries. Source keys containing any wildcard are ignored; both names
// are trimmed and must be nonblank and equal, otherwise no whitelist entry is
// produced.
func ExtractFinalModelWhitelist(mapping map[string]string) map[string]struct{} {
	if len(mapping) == 0 {
		return nil
	}
	whitelist := make(map[string]struct{})
	for rawFrom, rawTo := range mapping {
		if strings.Contains(rawFrom, "*") {
			continue
		}
		from := strings.TrimSpace(rawFrom)
		to := strings.TrimSpace(rawTo)
		if from == "" || to == "" || from != to {
			continue
		}
		whitelist[to] = struct{}{}
	}
	if len(whitelist) == 0 {
		return nil
	}
	return whitelist
}

// ExtractExplicitFinalModelWhitelist normalizes a separate model_whitelist
// array into a set. It accepts []string or []any; nil mixed entries are
// skipped, and other mixed values use their printed text before trimming.
//
// Blank names and patterns containing an asterisk before the optional final
// asterisk are ignored. Exact names and one trailing wildcard remain, including
// the bare wildcard. Empty or wholly unusable input returns nil, leaving the
// caller to retain the separate configured-field presence decision.
func ExtractExplicitFinalModelWhitelist(rawWhitelist any) map[string]struct{} {
	if rawWhitelist == nil {
		return nil
	}
	values := make([]string, 0)
	switch typed := rawWhitelist.(type) {
	case []string:
		values = append(values, typed...)
	case []any:
		for _, raw := range typed {
			if raw == nil {
				continue
			}
			if model, ok := raw.(string); ok {
				values = append(values, strings.TrimSpace(model))
			} else {
				values = append(values, strings.TrimSpace(fmt.Sprint(raw)))
			}
		}
	default:
		return nil
	}
	whitelist := make(map[string]struct{})
	for _, rawModel := range values {
		model := strings.TrimSpace(rawModel)
		if model == "" || strings.Contains(strings.TrimSuffix(model, "*"), "*") {
			continue
		}
		whitelist[model] = struct{}{}
	}
	if len(whitelist) == 0 {
		return nil
	}
	return whitelist
}

// ResolveFinalModelWhitelist selects explicit whitelist input before legacy
// identity mappings. The second result records that model_whitelist exists even
// when its contents produce no usable set, keeping presence separate from the
// parsed result.
//
// With no explicit field, Qoder returns nil and false because its identity
// mappings do not define this whitelist. Other platforms derive the legacy set
// and return false. Callers use the presence flag to distinguish a submitted
// explicit selection from the older mapping representation.
func ResolveFinalModelWhitelist(platform string, credentials map[string]any, mapping map[string]string) (map[string]struct{}, bool) {
	if credentials != nil {
		if rawWhitelist, exists := credentials["model_whitelist"]; exists {
			return ExtractExplicitFinalModelWhitelist(rawWhitelist), true
		}
	}
	if platform == PlatformQoder {
		return nil, false
	}
	return ExtractFinalModelWhitelist(mapping), false
}

// GetOpenAICompactMode returns the legacy compaction switch for an OpenAI
// record. Trimmed case-insensitive force_off disables it; every other saved
// value enables it, while nil or other platforms return force_off.
func (r *Record) GetOpenAICompactMode() string {
	if r == nil || !r.IsOpenAI() {
		return OpenAICompactModeForceOff
	}
	mode, _ := r.Extra["openai_compact_mode"].(string)
	if strings.EqualFold(strings.TrimSpace(mode), OpenAICompactModeForceOff) {
		return OpenAICompactModeForceOff
	}
	return OpenAICompactModeForceOn
}

// AllowsOpenAICompact reports whether an OpenAI record enables legacy
// compaction. Nil records and other platforms return false; this setting check
// leaves provider-type and protocol eligibility to the caller.
func (r *Record) AllowsOpenAICompact() bool {
	return r != nil && r.IsOpenAI() && r.GetOpenAICompactMode() == OpenAICompactModeForceOn
}

// GetOpenAINativeCompactionV2Mode reads the separate native V2 compaction
// switch. OpenAI records disable it only for trimmed case-insensitive
// force_off; nil or other platforms return force_off, independently of the
// legacy compaction switch.
func (r *Record) GetOpenAINativeCompactionV2Mode() string {
	if r == nil || !r.IsOpenAI() {
		return OpenAICompactModeForceOff
	}
	mode, _ := r.Extra[OpenAINativeCompactionV2ModeExtraKey].(string)
	if strings.EqualFold(strings.TrimSpace(mode), OpenAICompactModeForceOff) {
		return OpenAICompactModeForceOff
	}
	return OpenAICompactModeForceOn
}

// AllowsOpenAINativeCompactionV2 reports whether an OpenAI record enables
// native V2 compaction. Nil records and other platforms return false, and the
// separate legacy compaction switch has no effect on this result.
func (r *Record) AllowsOpenAINativeCompactionV2() bool {
	return r != nil && r.IsOpenAI() && r.GetOpenAINativeCompactionV2Mode() == OpenAICompactModeForceOn
}

// GetCompactModelMapping returns copied compact-only model mapping entries from
// Credentials. Nil records, missing credentials, and unusable mappings return
// nil; consumers apply this mapping to Compact requests independently of
// ordinary text mappings.
func (r *Record) GetCompactModelMapping() map[string]string {
	if r == nil || r.Credentials == nil {
		return nil
	}
	return StringMappingFromRaw(r.Credentials["compact_model_mapping"])
}

// ResolveCompactMappedModel applies compact-specific exact or trailing-wildcard
// remapping. Missing or unmatched configuration returns the requested model and
// false; an identity rule still returns true so callers know a configured rule
// matched.
func (r *Record) ResolveCompactMappedModel(requestedModel string) (mappedModel string, matched bool) {
	mapping := r.GetCompactModelMapping()
	if len(mapping) == 0 {
		return requestedModel, false
	}
	if mappedModel, matched := ResolveRequestedModelInMapping(mapping, requestedModel); matched {
		return mappedModel, true
	}
	return requestedModel, false
}

// GetBaseURL returns the saved base_url for an API Key record. An empty value
// selects the Anthropic default endpoint, and other provider types return
// empty; the accessor does not validate or trim a supplied URL.
func (r *Record) GetBaseURL() string {
	if r.Type != ProviderTypeAPIKey {
		return ""
	}
	baseURL := r.GetCredential("base_url")
	if baseURL == "" {
		return "https://api.anthropic.com"
	}
	return baseURL
}

// GetGeminiBaseURL returns the trimmed saved endpoint or the supplied default
// URL. The supplied default is used when the stored base is missing or
// whitespace only; protocol-specific service endpoints are selected by
// ProtocolTarget.BaseURL before constructing a request.
func (r *Record) GetGeminiBaseURL(defaultBaseURL string) string {
	baseURL := strings.TrimSpace(r.GetCredential("base_url"))
	if baseURL == "" {
		return defaultBaseURL
	}
	return baseURL
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

// GetClaudeUserID selects the first nonblank trimmed Claude identity hint.
// Extra claude_user_id and anthropic_user_id take priority, followed by the
// corresponding credential keys; all missing or blank candidates produce an
// empty string.
func (r *Record) GetClaudeUserID() string {
	if v := strings.TrimSpace(r.GetExtraString("claude_user_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.GetExtraString("anthropic_user_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.GetCredential("claude_user_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.GetCredential("anthropic_user_id")); v != "" {
		return v
	}
	return ""
}

// MatchWildcard delegates exact-name or trailing-asterisk matching to the model
// mapping helper.
func MatchWildcard(pattern, str string) bool { return modelmap.Matches(pattern, str) }

// IsCustomErrorCodesEnabled reads the API Key custom-error handling switch.
// Other types, absent credentials, missing keys, and nonboolean values return
// false, leaving ShouldHandleErrorCode on its unrestricted handling path.
func (r *Record) IsCustomErrorCodesEnabled() bool {
	if r.Type != ProviderTypeAPIKey || r.Credentials == nil {
		return false
	}
	if v, ok := r.Credentials["custom_error_codes_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// IsPoolMode reads the saved boolean pool_mode switch for API Key or Bedrock
// types. Missing credentials or nonboolean values return false; request
// handlers separately apply pool retry and error-policy decisions.
func (r *Record) IsPoolMode() bool {
	if !r.IsAPIKeyOrBedrock() || r.Credentials == nil {
		return false
	}
	if v, ok := r.Credentials["pool_mode"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetPoolModeRetryCount returns the ordinary same-provider retry budget when
// pool mode applies. Missing, disabled, or unparseable settings select
// DefaultPoolModeRetryCount; parsed values below zero or above
// MaxPoolModeRetryCount are clamped to those limits.
//
// Gateway failover can narrow this count with an error-specific maximum. An
// uncapped retry deadline is evaluated independently and can admit retries after
// the ordinary count is spent, so this accessor alone does not impose a limit
// on every retry path.
func (r *Record) GetPoolModeRetryCount() int {
	if r == nil || !r.IsPoolMode() || r.Credentials == nil {
		return DefaultPoolModeRetryCount
	}
	raw, ok := r.Credentials["pool_mode_retry_count"]
	if !ok || raw == nil {
		return DefaultPoolModeRetryCount
	}
	count := ParsePoolModeRetryCount(raw)
	// A negative saved count selects the ordinary zero retry budget. Failover
	// separately evaluates deadline rules before deciding whether to keep this
	// provider or proceed with another selection.
	if count < 0 {
		return 0
	}
	// Share the configured ceiling across entry points so each handler receives
	// the same bounded ordinary count before applying its failure-specific rules.
	if count > MaxPoolModeRetryCount {
		return MaxPoolModeRetryCount
	}
	return count
}

// ParsePoolModeRetryCount reads supported integer, float64, json.Number, or
// trimmed string representations. Failed parsing or unsupported input returns
// DefaultPoolModeRetryCount, while range clamping belongs to
// GetPoolModeRetryCount after this conversion.
func ParsePoolModeRetryCount(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		// A retry count uses whole attempts after fractional JSON input is truncated.
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	// A failed number or string parse has no usable configured count. Return
	// the shared default before the accessor applies its bounds to successful values.
	return DefaultPoolModeRetryCount
}

// IsPoolModeRetryableStatus reports membership in the shared default pool retry
// status list.
func IsPoolModeRetryableStatus(statusCode int) bool {
	return slices.Contains(DefaultPoolModeRetryableStatusCodes, statusCode)
}

// GetPoolModeRetryStatusCodes normalizes a provider's optional retry-status
// override. Nil records, absent values, and values outside the []any
// representation return nil, instructing the caller to inherit the shared
// defaults.
//
// A supplied []any remains configured even if empty or every element is
// rejected; its empty nonnil result yields no IsPoolModeRetryableStatus matches.
// Accepted numeric and string elements are deduplicated, restricted to HTTP
// statuses 100 through 599, and sorted. Keeping container presence separate from
// valid entries prevents an empty configured selection from restoring default
// status matches.
func (r *Record) GetPoolModeRetryStatusCodes() []int {
	if r == nil || r.Credentials == nil {
		return nil
	}
	raw, ok := r.Credentials["pool_mode_retry_status_codes"]
	if !ok || raw == nil {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	seen := make(map[int]struct{}, len(arr))
	codes := make([]int, 0, len(arr))
	for _, v := range arr {
		var code int
		switch n := v.(type) {
		case float64:
			// Convert decoded JSON numbers to whole HTTP status values before filtering.
			code = int(n)
		case int:
			code = n
		case int64:
			code = int(n)
		case json.Number:
			// Exact-number text must encode an integer HTTP status. Reject a failed
			// parse locally so one unusable element cannot discard the other
			// configured statuses or select an invented integral value.
			i, err := n.Int64()
			if err != nil {
				continue
			}
			code = int(i)
		case string:
			// Legacy text entries accept surrounding whitespace. Skip unreadable
			// status text while keeping the supplied array configured, so the
			// surviving entries still define this provider's override.
			i, err := strconv.Atoi(strings.TrimSpace(n))
			if err != nil {
				continue
			}
			code = i
		default:
			continue
		}
		// Keep the supplied array configured while discarding unusable HTTP codes.
		// If none survive, the allocated empty result still reaches membership
		// testing and matches no status. An unavailable override instead returns
		// nil, which selects the shared default list.
		if code < 100 || code > 599 {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	sort.Ints(codes)
	return codes
}

// IsPoolModeRetryableStatus checks the provider override when one is
// configured. A nil normalized override uses the shared defaults, while an
// empty configured slice disables all status matches independently of whether
// pool mode is enabled.
func (r *Record) IsPoolModeRetryableStatus(statusCode int) bool {
	codes := r.GetPoolModeRetryStatusCodes()
	// Both absent overrides and configured empty results have zero length.
	// Test nil here so an empty []any cannot silently restore default status
	// matches when its normalized result contains no codes.
	if codes == nil {
		return IsPoolModeRetryableStatus(statusCode)
	}
	return slices.Contains(codes, statusCode)
}

// GetCustomErrorCodes reads the custom handling statuses from a JSON []any
// array. It accepts float64 elements and converts them to integers; other
// elements are skipped, and missing or differently typed containers return nil.
func (r *Record) GetCustomErrorCodes() []int {
	if r.Credentials == nil {
		return nil
	}
	raw, ok := r.Credentials["custom_error_codes"]
	if !ok || raw == nil {
		return nil
	}
	if arr, ok := raw.([]any); ok {
		result := make([]int, 0, len(arr))
		// This stored compatibility list accepts ordinary JSON numeric elements.
		// Convert those values to integer statuses and leave other representations
		// out of the resulting custom handling selection.
		for _, v := range arr {
			if f, ok := v.(float64); ok {
				result = append(result, int(f))
			}
		}
		return result
	}
	return nil
}

// ShouldHandleErrorCode applies the API Key custom status selection to an
// upstream error. Disabled configuration or an empty parsed list handles every
// status; a nonempty list handles only its members.
func (r *Record) ShouldHandleErrorCode(statusCode int) bool {
	if !r.IsCustomErrorCodesEnabled() {
		return true
	}
	codes := r.GetCustomErrorCodes()
	// This compatibility policy treats an empty parsed selection as unrestricted.
	// Keep health handling available for enabled-but-empty forms, whose lack of
	// status entries differs from a configured empty pool retry override.
	if len(codes) == 0 {
		return true
	}
	return slices.Contains(codes, statusCode)
}

// IsInterceptWarmupEnabled reads the saved warmup-interception boolean from
// Credentials. Missing maps, absent values, and nonboolean representations
// return false; this accessor leaves provider eligibility and warmup
// classification with its consumers.
func (r *Record) IsInterceptWarmupEnabled() bool {
	if r.Credentials == nil {
		return false
	}
	if v, ok := r.Credentials["intercept_warmup_requests"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
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

// StripAnthropicPathSuffix trims a relay address and removes a final decoded
// anthropic path segment when present. Parse errors return the trimmed input;
// callers still apply the request path's transport validation to the result.
//
// Removing the suffix also clears RawPath, and URL.String escapes the resulting
// decoded path while carrying the parsed query and fragment. Trailing slashes
// are trimmed from the entire input and output strings, including one at the
// end of query or fragment text. A caller needing byte-for-byte retention of
// those suffixes therefore keeps its original URL instead of using this
// compatibility rewrite.
func StripAnthropicPathSuffix(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	// Malformed compatibility input retains its trimmed text for the egress
	// validator. A parse failure here cannot establish a safe scheme or host for
	// the caller's eventual request.
	if err != nil {
		return trimmed
	}
	// Edit the decoded path and discard its encoded hint together. URL.String
	// then derives an escaped path from that modified value; the final whole-URL
	// trim still applies to any trailing query or fragment slash.
	path := strings.TrimRight(parsed.Path, "/")
	if path == "/anthropic" {
		parsed.Path = ""
		parsed.RawPath = ""
	} else if before, ok := strings.CutSuffix(path, "/anthropic"); ok {
		parsed.Path = before
		parsed.RawPath = ""
	}
	return strings.TrimRight(parsed.String(), "/")
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

// IsOpenAIPassthroughEnabled reads the OpenAI passthrough selection used by
// forwarding. A typed openai_passthrough value takes priority, including false;
// otherwise the legacy openai_oauth_passthrough boolean is used, with absent or
// unusable input selecting false.
func (r *Record) IsOpenAIPassthroughEnabled() bool {
	if r == nil || !r.IsOpenAI() || r.Extra == nil {
		return false
	}
	if enabled, ok := r.Extra["openai_passthrough"].(bool); ok {
		return enabled
	}
	if enabled, ok := r.Extra["openai_oauth_passthrough"].(bool); ok {
		return enabled
	}
	return false
}

// IsOpenAIResponsesFlattenNamespacesEnabled reads the namespace-flattening
// opt-in for OpenAI OAuth. Nil records, other types, missing configuration, and
// nonboolean values return false, leaving namespace encoding with the standard
// forwarding path.
func (r *Record) IsOpenAIResponsesFlattenNamespacesEnabled() bool {
	if r == nil || !r.IsOpenAIOAuth() || r.Extra == nil {
		return false
	}
	enabled, ok := r.Extra["openai_responses_flatten_namespaces"].(bool)
	return ok && enabled
}

// IsOpenAIWSAllowStoreRecoveryEnabled reads the OpenAI WebSocket store-recovery
// opt-in from Extra. Nil records, other platforms, missing configuration, and
// nonboolean values return false before the consumer applies its recovery
// handling.
func (r *Record) IsOpenAIWSAllowStoreRecoveryEnabled() bool {
	if r == nil || !r.IsOpenAI() || r.Extra == nil {
		return false
	}
	enabled, ok := r.Extra["openai_ws_allow_store_recovery"].(bool)
	return ok && enabled
}

// IsOpenAIOAuthPassthroughEnabled restricts the shared passthrough setting to
// OpenAI OAuth records. Nil records and other provider kinds return false;
// eligible records use the current setting before its legacy alias.
func (r *Record) IsOpenAIOAuthPassthroughEnabled() bool {
	return r != nil && r.IsOpenAIOAuth() && r.IsOpenAIPassthroughEnabled()
}

// IsAnthropicAPIKeyPassthroughEnabled reads passthrough for Anthropic API Key
// forwarding. Nil records, other platform/type combinations, missing
// configuration, and nonboolean values return false, so the option needs both
// eligibility and a true boolean.
func (r *Record) IsAnthropicAPIKeyPassthroughEnabled() bool {
	if r == nil || r.Platform != PlatformAnthropic || r.Type != ProviderTypeAPIKey || r.Extra == nil {
		return false
	}
	enabled, ok := r.Extra["anthropic_passthrough"].(bool)
	return ok && enabled
}

// IsCodexCLIOnlyEnabled reports whether the resolved OpenAI OAuth client policy
// selects Codex.
func (r *Record) IsCodexCLIOnlyEnabled() bool {
	return r.GetOpenAIOAuthClientPolicy() == OpenAIOAuthClientPolicyCodexOnly
}

// GetOpenAIOAuthClientPolicy selects the recognized OpenAI OAuth client access
// policy. A trimmed supported string takes priority over the legacy
// codex_cli_only flag, including the unrestricted any value.
//
// If the string is absent, invalid, or differently cased, a true legacy boolean
// selects codex_only. Other cases use any, including nil records and providers
// outside OpenAI OAuth. Callers separately enforce preset client identities or
// a TLS-router match according to this returned policy.
func (r *Record) GetOpenAIOAuthClientPolicy() string {
	if r == nil || !r.IsOpenAIOAuth() || r.Extra == nil {
		return OpenAIOAuthClientPolicyAny
	}
	if policy, ok := r.Extra["openai_oauth_client_policy"].(string); ok {
		switch strings.TrimSpace(policy) {
		case OpenAIOAuthClientPolicyCodexOnly:
			return OpenAIOAuthClientPolicyCodexOnly
		case OpenAIOAuthClientPolicyTLSRouterMatchedOnly:
			return OpenAIOAuthClientPolicyTLSRouterMatchedOnly
		case OpenAIOAuthClientPolicyAny:
			return OpenAIOAuthClientPolicyAny
		}
	}
	enabled, ok := r.Extra["codex_cli_only"].(bool)
	if ok && enabled {
		return OpenAIOAuthClientPolicyCodexOnly
	}
	return OpenAIOAuthClientPolicyAny
}

// IsOpenAIOAuthTLSRouterMatchedOnly reports whether the resolved policy
// requires a TLS-router match.
func (r *Record) IsOpenAIOAuthTLSRouterMatchedOnly() bool {
	return r.GetOpenAIOAuthClientPolicy() == OpenAIOAuthClientPolicyTLSRouterMatchedOnly
}

// GetCodexCLIOnlyAllowedClients returns a new slice of preset client IDs for
// OpenAI OAuth. Supported arrays skip nonstrings and whitespace-only values
// while retaining each accepted string's original bytes; unsupported providers
// or containers return nil.
func (r *Record) GetCodexCLIOnlyAllowedClients() []string {
	if r == nil || !r.IsOpenAIOAuth() || r.Extra == nil {
		return nil
	}
	raw, ok := r.Extra["codex_cli_only_allowed_clients"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		result := make([]string, 0, len(v))
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				result = append(result, s)
			}
		}
		return result
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

// IsAnthropicOAuthOrSetupToken identifies Anthropic OAuth and Setup Token
// records used by forwarding. Nil records return false; the type classification
// leaves credential availability and refresh eligibility to the consuming path.
func (r *Record) IsAnthropicOAuthOrSetupToken() bool {
	if r == nil {
		return false
	}
	return r.Platform == PlatformAnthropic && (r.Type == ProviderTypeOAuth || r.Type == ProviderTypeSetupToken)
}

// SupportsTLSFingerprint classifies provider types whose outbound transport can
// emulate a TLS profile. Anthropic OAuth/Setup Token, OpenAI OAuth, Kimi,
// Zhipu, and DeepSeek API Key, and Qoder COSY records qualify; nil and other
// combinations return false.
func (r *Record) SupportsTLSFingerprint() bool {
	if r == nil {
		return false
	}
	if r.IsAnthropicOAuthOrSetupToken() {
		return true
	}
	return (r.Platform == PlatformOpenAI && r.Type == ProviderTypeOAuth) ||
		(r.rules().apiKeyTLSFingerprint && r.Type == ProviderTypeAPIKey) ||
		r.IsQoderCosy()
}

// IsTLSFingerprintEnabled requires both a supported provider type and a true
// Extra boolean. Missing maps, missing keys, or nonboolean values leave
// emulation disabled even when the provider type supports it.
func (r *Record) IsTLSFingerprintEnabled() bool {
	if !r.SupportsTLSFingerprint() {
		return false
	}
	if r.Extra == nil {
		return false
	}
	if v, ok := r.Extra["enable_tls_fingerprint"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetTLSFingerprintProfileID reads a supported numeric profile ID from Extra.
// Missing or unsupported values and unparseable json.Number text return zero;
// the transport consumer treats zero as its built-in default profile.
func (r *Record) GetTLSFingerprintProfileID() int64 {
	if r.Extra == nil {
		return 0
	}
	v, ok := r.Extra["tls_fingerprint_profile_id"]
	if !ok {
		return 0
	}
	switch id := v.(type) {
	case float64:
		// JSON numeric identifiers lose fractions when converted to integer record IDs.
		return int64(id)
	case int64:
		return id
	case int:
		return int64(id)
	case json.Number:
		if i, err := id.Int64(); err == nil {
			return i
		}
	}
	// Unsupported or unreadable profile metadata uses the zero selection that
	// the transport consumer already interprets for an absent configuration value.
	return 0
}

// GetTLSFingerprintRouterID reads a supported numeric TLS-router ID from Extra.
// Missing or unsupported values and unparseable json.Number text return zero,
// indicating that no router was selected by this setting.
func (r *Record) GetTLSFingerprintRouterID() int64 {
	if r.Extra == nil {
		return 0
	}
	v, ok := r.Extra["tls_fingerprint_router_id"]
	if !ok {
		return 0
	}
	switch id := v.(type) {
	case float64:
		// JSON numeric identifiers lose fractions when converted to integer record IDs.
		return int64(id)
	case int64:
		return id
	case int:
		return int64(id)
	case json.Number:
		if i, err := id.Int64(); err == nil {
			return i
		}
	}
	// Unsupported or unreadable profile metadata uses the zero selection that
	// the transport consumer already interprets for an absent configuration value.
	return 0
}

// IsSessionIDMaskingEnabled reads the saved masking opt-in for Anthropic OAuth
// or Setup Token. Other provider kinds, missing Extra, and nonboolean values
// return false; session lifetime and renewal belong to the masking consumer.
func (r *Record) IsSessionIDMaskingEnabled() bool {
	if !r.IsAnthropicOAuthOrSetupToken() {
		return false
	}
	if r.Extra == nil {
		return false
	}
	if v, ok := r.Extra["session_id_masking_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// IsCustomBaseURLEnabled reads the relay endpoint opt-in for Anthropic OAuth or
// Setup Token. Other provider kinds, absent Extra, and nonboolean values return
// false before the caller considers GetCustomBaseURL.
func (r *Record) IsCustomBaseURLEnabled() bool {
	if !r.IsAnthropicOAuthOrSetupToken() {
		return false
	}
	if r.Extra == nil {
		return false
	}
	if v, ok := r.Extra["custom_base_url_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetCustomBaseURL reads the configured relay address string from provider
// Extra.
func (r *Record) GetCustomBaseURL() string {
	return r.GetExtraString("custom_base_url")
}

// IsCacheTTLOverrideEnabled reads the cache-creation TTL opt-in for Anthropic
// OAuth or Setup Token. Other provider kinds, missing configuration, and
// nonboolean values return false; the selected category comes from
// GetCacheTTLOverrideTarget.
func (r *Record) IsCacheTTLOverrideEnabled() bool {
	if !r.IsAnthropicOAuthOrSetupToken() {
		return false
	}
	if r.Extra == nil {
		return false
	}
	if v, ok := r.Extra["cache_ttl_override_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetCacheTTLOverrideTarget returns the configured cache TTL category when it
// is exactly 5m or 1h. Missing or unsupported values return 5m; enablement is
// checked separately by the caller using the category.
func (r *Record) GetCacheTTLOverrideTarget() string {
	if r.Extra == nil {
		return "5m"
	}
	if v, ok := r.Extra["cache_ttl_override_target"]; ok {
		if target, ok := v.(string); ok && (target == "5m" || target == "1h") {
			return target
		}
	}
	return "5m"
}

// GetQuotaLimit reads the total provider quota limit in USD. Nonpositive limits
// leave the total dimension inactive in IsQuotaExceeded, while periodic limits
// are evaluated independently against their own usage.
func (r *Record) GetQuotaLimit() float64 {
	return r.GetExtraFloat64("quota_limit")
}

// GetQuotaUsed reads the stored total provider consumption in USD.
func (r *Record) GetQuotaUsed() float64 {
	return r.GetExtraFloat64("quota_used")
}

// GetQuotaDailyLimit reads the provider's daily quota limit in USD. Nonpositive
// limits leave that dimension inactive; a positive limit is compared with usage
// only while the daily period remains current.
func (r *Record) GetQuotaDailyLimit() float64 {
	return r.GetExtraFloat64("quota_daily_limit")
}

// GetQuotaDailyUsed reads stored daily provider consumption in USD before
// expiration handling.
func (r *Record) GetQuotaDailyUsed() float64 {
	return r.GetExtraFloat64("quota_daily_used")
}

// GetQuotaWeeklyLimit reads the provider's weekly quota limit in USD.
// Nonpositive limits leave that dimension inactive; a positive limit applies to
// the stored consumption only while its weekly period remains current.
func (r *Record) GetQuotaWeeklyLimit() float64 {
	return r.GetExtraFloat64("quota_weekly_limit")
}

// GetQuotaMonthlyLimit reads the provider's monthly quota limit in USD.
// Nonpositive limits leave that dimension inactive; a positive limit applies to
// the stored consumption only while its monthly period remains current.
func (r *Record) GetQuotaMonthlyLimit() float64 {
	return r.GetExtraFloat64("quota_monthly_limit")
}

// GetQuotaWeeklyUsed reads stored weekly provider consumption in USD before
// expiration handling.
func (r *Record) GetQuotaWeeklyUsed() float64 {
	return r.GetExtraFloat64("quota_weekly_used")
}

// GetQuotaMonthlyUsed reads stored monthly provider consumption in USD before
// expiration handling.
func (r *Record) GetQuotaMonthlyUsed() float64 {
	return r.GetExtraFloat64("quota_monthly_used")
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

// GetExtraStringDefault returns the saved nonempty string or the supplied
// default. Whitespace-only strings remain nonempty here, so callers requiring
// trimmed input normalize it before assigning meaning to the value.
func (r *Record) GetExtraStringDefault(key, defaultVal string) string {
	if v := r.GetExtraString(key); v != "" {
		return v
	}
	return defaultVal
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

// GetQuotaDailyResetMode returns fixed for the exact saved reset-mode label.
// Every other value selects rolling, including missing configuration, allowing
// stored records to retain the default duration-based daily window.
func (r *Record) GetQuotaDailyResetMode() string {
	if m := r.GetExtraString("quota_daily_reset_mode"); m == "fixed" {
		return "fixed"
	}
	return "rolling"
}

// GetQuotaDailyResetHour reads the configured local daily reset hour as an
// integer. Missing values return zero; this accessor leaves range checking to
// validation and normalization to the billing wrapper where applicable.
func (r *Record) GetQuotaDailyResetHour() int {
	return r.GetExtraInt("quota_daily_reset_hour")
}

// GetQuotaWeeklyResetMode returns fixed for the exact saved reset-mode label.
// Missing, invalid, or differently cased values select rolling, allowing weekly
// consumers to use their duration-based default calculation.
func (r *Record) GetQuotaWeeklyResetMode() string {
	if m := r.GetExtraString("quota_weekly_reset_mode"); m == "fixed" {
		return "fixed"
	}
	return "rolling"
}

// GetQuotaMonthlyResetMode returns fixed for the exact saved reset-mode label.
// Missing, invalid, or differently cased values select rolling, allowing monthly
// consumers to use their duration-based default calculation.
func (r *Record) GetQuotaMonthlyResetMode() string {
	if m := r.GetExtraString("quota_monthly_reset_mode"); m == "fixed" {
		return "fixed"
	}
	return "rolling"
}

// GetQuotaWeeklyResetDay reads the configured Sunday-zero weekday for fixed
// weekly resets. A missing key selects Monday, while a present unusable value
// can parse to zero and is left to validation.
func (r *Record) GetQuotaWeeklyResetDay() int {
	if r.Extra == nil {
		return 1
	}
	if _, ok := r.Extra["quota_weekly_reset_day"]; !ok {
		return 1
	}
	return r.GetExtraInt("quota_weekly_reset_day")
}

// GetQuotaMonthlyResetDay reads the configured day of month for fixed
// monthly resets. A missing key selects the first day, while a present unusable
// value
// can parse to zero and is left to validation.
func (r *Record) GetQuotaMonthlyResetDay() int {
	if r.Extra == nil {
		return 1
	}
	if _, ok := r.Extra["quota_monthly_reset_day"]; !ok {
		return 1
	}
	return r.GetExtraInt("quota_monthly_reset_day")
}

// GetQuotaWeeklyResetHour reads the configured local weekly reset hour as an
// integer. Missing values return zero; range checks belong to configuration
// validation or the billing wrapper before calendar calculations.
func (r *Record) GetQuotaWeeklyResetHour() int {
	return r.GetExtraInt("quota_weekly_reset_hour")
}

// GetQuotaMonthlyResetHour reads the configured local monthly reset hour as an
// integer. Missing values return zero; range checks belong to configuration
// validation or the billing wrapper before calendar calculations.
func (r *Record) GetQuotaMonthlyResetHour() int {
	return r.GetExtraInt("quota_monthly_reset_hour")
}

// GetQuotaResetTimezone returns the saved nonempty timezone text, defaulting to
// UTC. It does not validate or trim that string; the supplied location loader
// and each reset consumer decide how invalid names are handled.
func (r *Record) GetQuotaResetTimezone() string {
	if tz := r.GetExtraString("quota_reset_timezone"); tz != "" {
		return tz
	}
	return "UTC"
}

// QuotaNotifyConfig reads the enabled flag, threshold, and threshold type for a
// daily, weekly, monthly, or total dimension. Missing values select false, zero, and
// fixed; supplied nonempty threshold-type text is returned for the notification
// consumer to interpret.
func (r *Record) QuotaNotifyConfig(dim string) (enabled bool, threshold float64, thresholdType string) {
	enabled = r.GetExtraBool("quota_notify_" + dim + "_enabled")
	threshold = r.GetExtraFloat64("quota_notify_" + dim + "_threshold")
	thresholdType = r.GetExtraStringDefault("quota_notify_"+dim+"_threshold_type", thresholdTypeFixed)
	return
}

// GetQuotaNotifyDailyEnabled reads the daily quota notification switch from
// Extra.
func (r *Record) GetQuotaNotifyDailyEnabled() bool {
	e, _, _ := r.QuotaNotifyConfig(quotaDimDaily)
	return e
}

// GetQuotaNotifyDailyThreshold reads the configured daily quota notification
// threshold value.
func (r *Record) GetQuotaNotifyDailyThreshold() float64 {
	_, t, _ := r.QuotaNotifyConfig(quotaDimDaily)
	return t
}

// GetQuotaNotifyDailyThresholdType returns the daily threshold interpretation
// from QuotaNotifyConfig. Missing or empty text selects fixed, while supplied
// nonempty values remain for the notification consumer to validate or
// interpret.
func (r *Record) GetQuotaNotifyDailyThresholdType() string {
	_, _, tt := r.QuotaNotifyConfig(quotaDimDaily)
	return tt
}

// GetQuotaNotifyWeeklyEnabled reads the weekly quota notification switch from
// Extra.
func (r *Record) GetQuotaNotifyWeeklyEnabled() bool {
	e, _, _ := r.QuotaNotifyConfig(quotaDimWeekly)
	return e
}

// GetQuotaNotifyMonthlyEnabled reads the monthly quota notification switch from
// Extra.
func (r *Record) GetQuotaNotifyMonthlyEnabled() bool {
	e, _, _ := r.QuotaNotifyConfig(quotaDimMonthly)
	return e
}

// GetQuotaNotifyWeeklyThreshold reads the configured weekly quota notification
// threshold value.
func (r *Record) GetQuotaNotifyWeeklyThreshold() float64 {
	_, t, _ := r.QuotaNotifyConfig(quotaDimWeekly)
	return t
}

// GetQuotaNotifyMonthlyThreshold reads the configured monthly quota notification
// threshold value.
func (r *Record) GetQuotaNotifyMonthlyThreshold() float64 {
	_, t, _ := r.QuotaNotifyConfig(quotaDimMonthly)
	return t
}

// GetQuotaNotifyWeeklyThresholdType returns the weekly threshold interpretation
// from QuotaNotifyConfig. Missing or empty text selects fixed; other nonempty
// saved strings are returned for the notification consumer's own threshold
// handling.
func (r *Record) GetQuotaNotifyWeeklyThresholdType() string {
	_, _, tt := r.QuotaNotifyConfig(quotaDimWeekly)
	return tt
}

// GetQuotaNotifyMonthlyThresholdType returns the monthly threshold interpretation
// from QuotaNotifyConfig. Missing or empty text selects fixed; other nonempty
// saved strings are returned for the notification consumer's own threshold
// handling.
func (r *Record) GetQuotaNotifyMonthlyThresholdType() string {
	_, _, tt := r.QuotaNotifyConfig(quotaDimMonthly)
	return tt
}

// GetQuotaNotifyTotalEnabled reads the total quota notification switch from
// Extra.
func (r *Record) GetQuotaNotifyTotalEnabled() bool {
	e, _, _ := r.QuotaNotifyConfig(quotaDimTotal)
	return e
}

// GetQuotaNotifyTotalThreshold reads the configured total quota notification
// threshold value.
func (r *Record) GetQuotaNotifyTotalThreshold() float64 {
	_, t, _ := r.QuotaNotifyConfig(quotaDimTotal)
	return t
}

// GetQuotaNotifyTotalThresholdType returns the total threshold interpretation
// from QuotaNotifyConfig. Missing or empty text selects fixed; the notification
// consumer interprets other supplied nonempty strings when applying the
// threshold.
func (r *Record) GetQuotaNotifyTotalThresholdType() string {
	_, _, tt := r.QuotaNotifyConfig(quotaDimTotal)
	return tt
}

// LastFixedDailyReset delegates the preceding local daily reset date to
// billing. Supply a nonnil timezone and the intended clock hour; equality with
// today's reset selects today and earlier times select the preceding calendar
// day.
func LastFixedDailyReset(hour int, tz *time.Location, now time.Time) time.Time {
	return billing.LastFixedDailyReset(hour, tz, now)
}

// LastFixedWeeklyReset delegates the preceding local weekly reset date to
// billing. Supply a nonnil timezone, Sunday-zero weekday, and intended clock
// hour; the selected boundary moves back a week before its matching-day reset.
func LastFixedWeeklyReset(day, hour int, tz *time.Location, now time.Time) time.Time {
	return billing.LastFixedWeeklyReset(day, hour, tz, now)
}

// IsFixedDailyPeriodExpired compares a stored start with the most recent
// configured local daily reset. A zero start is expired, an invalid timezone
// uses UTC, and equality with the reset remains current. LoadLocation is
// required once a nonzero start reaches the timezone lookup.
func (r *Record) IsFixedDailyPeriodExpired(periodStart time.Time) bool {
	if periodStart.IsZero() {
		return true
	}
	tz, err := r.LoadLocation(r.GetQuotaResetTimezone())
	if err != nil {
		// A failed saved location lookup cannot define the calendar reset. Use
		// UTC to compare the stored start with a usable reset date for admission.
		tz = time.UTC
	}
	lastReset := LastFixedDailyReset(r.GetQuotaDailyResetHour(), tz, r.now())
	return periodStart.Before(lastReset)
}

// IsFixedWeeklyPeriodExpired compares a stored start with the most recent
// configured local weekly reset. A zero start is expired, invalid timezone text
// uses UTC, and an equal or later start remains current. LoadLocation is
// required once a nonzero start reaches the timezone lookup.
func (r *Record) IsFixedWeeklyPeriodExpired(periodStart time.Time) bool {
	if periodStart.IsZero() {
		return true
	}
	tz, err := r.LoadLocation(r.GetQuotaResetTimezone())
	if err != nil {
		// A failed saved location lookup cannot define the calendar reset. Use
		// UTC to compare the stored start with a usable reset date for admission.
		tz = time.UTC
	}
	lastReset := LastFixedWeeklyReset(r.GetQuotaWeeklyResetDay(), r.GetQuotaWeeklyResetHour(), tz, r.now())
	return periodStart.Before(lastReset)
}

// ValidateQuotaResetConfig checks supplied reset modes, numeric ranges, and
// nonempty string timezone names. Nil and empty Extra succeed; a loader is
// needed when timezone validation is active.
//
// Mode checks apply to strings. Daily and weekly numeric fields use
// ParseExtraFloat64 and integer conversion before their bounds are tested.
// Monthly dates and hours reject fractional values before persistence. Storage
// helpers apply their documented defaults when reading saved configuration for
// reset calculations.
func ValidateQuotaResetConfig(extra map[string]any, loadLocation func(string) (*time.Location, error)) error {
	if extra == nil {
		return nil
	}
	if tz, ok := extra["quota_reset_timezone"].(string); ok && tz != "" {
		if _, err := loadLocation(tz); err != nil {
			return errors.New("invalid quota_reset_timezone: must be a valid IANA timezone name")
		}
	}
	if mode, ok := extra["quota_daily_reset_mode"].(string); ok {
		if mode != "rolling" && mode != "fixed" {
			return errors.New("quota_daily_reset_mode must be 'rolling' or 'fixed'")
		}
	}
	// Bounds apply after the shared numeric conversion. Fractional JSON values
	// are truncated before range checking, and unsupported representations can
	// become zero, matching the readers that consume these saved clock fields.
	if v, ok := extra["quota_daily_reset_hour"]; ok {
		hour := int(ParseExtraFloat64(v))
		if hour < 0 || hour > 23 {
			return errors.New("quota_daily_reset_hour must be between 0 and 23")
		}
	}
	if mode, ok := extra["quota_weekly_reset_mode"].(string); ok {
		if mode != "rolling" && mode != "fixed" {
			return errors.New("quota_weekly_reset_mode must be 'rolling' or 'fixed'")
		}
	}
	// Sunday is zero and Saturday is six in the weekly calendar helpers.
	if v, ok := extra["quota_weekly_reset_day"]; ok {
		day := int(ParseExtraFloat64(v))
		if day < 0 || day > 6 {
			return errors.New("quota_weekly_reset_day must be between 0 (Sunday) and 6 (Saturday)")
		}
	}
	if v, ok := extra["quota_weekly_reset_hour"]; ok {
		hour := int(ParseExtraFloat64(v))
		if hour < 0 || hour > 23 {
			return errors.New("quota_weekly_reset_hour must be between 0 and 23")
		}
	}
	if mode, ok := extra["quota_monthly_reset_mode"].(string); ok && mode != "rolling" && mode != "fixed" {
		return errors.New("quota_monthly_reset_mode must be 'rolling' or 'fixed'")
	}
	if v, ok := extra["quota_monthly_reset_hour"]; ok {
		hour := ParseExtraFloat64(v)
		if hour < 0 || hour > 23 || hour != float64(int(hour)) {
			return errors.New("quota_monthly_reset_hour must be an integer between 0 and 23")
		}
	}
	if v, ok := extra["quota_monthly_reset_day"]; ok {
		day := ParseExtraFloat64(v)
		if day < 1 || day > 31 || day != float64(int(day)) {
			return errors.New("quota_monthly_reset_day must be an integer between 1 and 31")
		}
	}
	return nil
}

// HasAnyQuotaLimit reports whether total, daily, weekly, or monthly provider
// quota is
// positive.
func (r *Record) HasAnyQuotaLimit() bool {
	return r.GetQuotaLimit() > 0 ||
		r.GetQuotaDailyLimit() > 0 ||
		r.GetQuotaWeeklyLimit() > 0 ||
		r.GetQuotaMonthlyLimit() > 0
}

// IsPeriodExpired compares real elapsed time since periodStart with dur. A zero
// start is expired so the next usage increment can initialize it; this helper
// uses time.Since independently of Record.Now.
func IsPeriodExpired(periodStart time.Time, dur time.Duration) bool {
	if periodStart.IsZero() {
		return true // The next usage increment initializes an unused period.
	}
	return time.Since(periodStart) >= dur
}

// IsDailyQuotaPeriodExpired reports whether the stored daily usage window has
// elapsed. Fixed mode uses the configured provider clock and timezone; rolling
// mode uses a twenty-four-hour real elapsed duration and a zero start expires.
func (r *Record) IsDailyQuotaPeriodExpired() bool {
	start := r.GetExtraTime("quota_daily_start")
	if r.GetQuotaDailyResetMode() == "fixed" {
		return r.IsFixedDailyPeriodExpired(start)
	}
	// Rolling admission measures twenty-four elapsed hours across local clock
	// changes; fixed mode above instead follows the configured calendar reset.
	return IsPeriodExpired(start, 24*time.Hour)
}

// IsWeeklyQuotaPeriodExpired reports whether the stored weekly usage window has
// elapsed. Fixed mode uses the provider clock and local weekday schedule;
// rolling mode uses seven real elapsed days and treats a zero start as expired.
func (r *Record) IsWeeklyQuotaPeriodExpired() bool {
	start := r.GetExtraTime("quota_weekly_start")
	if r.GetQuotaWeeklyResetMode() == "fixed" {
		return r.IsFixedWeeklyPeriodExpired(start)
	}
	// Seven elapsed days keep the rolling window duration independent of local
	// timezone transitions; calendar-week rules apply only in the fixed branch.
	return IsPeriodExpired(start, 7*24*time.Hour)
}

// IsMonthlyQuotaPeriodExpired reports whether monthly consumption has expired.
// Fixed mode follows the configured local date, clamping short months to their
// final date. Rolling mode expires thirty elapsed days after its recorded start;
// missing starts expire immediately so the next increment initializes usage.
// Fixed mode requires LoadLocation for a nonzero start, uses UTC after a loader
// failure, and uses the record clock when selecting the current calendar date.
func (r *Record) IsMonthlyQuotaPeriodExpired() bool {
	start := r.GetExtraTime("quota_monthly_start")
	if r.GetQuotaMonthlyResetMode() == "fixed" {
		if start.IsZero() {
			return true
		}
		tz, err := r.LoadLocation(r.GetQuotaResetTimezone())
		if err != nil {
			// Invalid saved timezone names use UTC so admission can still evaluate
			// the configured calendar window consistently with provider configuration
			// reconciliation.
			tz = time.UTC
		}
		last := billing.LastFixedMonthlyReset(r.GetQuotaMonthlyResetDay(), r.GetQuotaMonthlyResetHour(), tz, r.now())
		return start.Before(last)
	}
	// Thirty elapsed days match the monthly duration used by billing SQL,
	// including when local clock transitions shorten or lengthen calendar days.
	return IsPeriodExpired(start, 30*24*time.Hour)
}

// IsQuotaExceeded combines the provider's total, daily, weekly, and monthly
// consumption
// limits. A positive limit is exhausted at equality or above; total consumption
// has no expiration window.
//
// Daily, weekly, and monthly consumption count only while their stored periods
// remain
// current. Fixed periods compare against local calendar resets, while rolling
// periods use real elapsed durations. An expired or missing start makes the
// periodic amount irrelevant to admission until the usage writer initializes
// its next period. The first exhausted active dimension rejects the provider,
// so one current limit can still block it when another dimension's stored usage
// has expired.
func (r *Record) IsQuotaExceeded() bool {
	// Total consumption remains applicable across periodic rollovers. Its limit
	// can therefore reject a provider even when the daily and weekly windows
	// have already expired and their usage is ignored below.
	if limit := r.GetQuotaLimit(); limit > 0 && r.GetQuotaUsed() >= limit {
		return true
	}
	// Daily expiration affects the admission comparison without rewriting Extra.
	// A later billing increment owns resetting the counter and start together,
	// so this reader leaves the persisted amount available for that writer.
	if limit := r.GetQuotaDailyLimit(); limit > 0 {
		if !r.IsDailyQuotaPeriodExpired() && r.GetQuotaDailyUsed() >= limit {
			return true
		}
	}
	// Weekly usage has its own start and reset policy. Evaluate that period
	// separately so expiration of the daily window cannot release a still
	// exhausted weekly allowance.
	if limit := r.GetQuotaWeeklyLimit(); limit > 0 {
		if !r.IsWeeklyQuotaPeriodExpired() && r.GetQuotaWeeklyUsed() >= limit {
			return true
		}
	}
	// Monthly consumption remains an independent restriction when shorter quota
	// periods expire; this read leaves counter rollover to the next billing increment.
	if limit := r.GetQuotaMonthlyLimit(); limit > 0 &&
		!r.IsMonthlyQuotaPeriodExpired() && r.GetQuotaMonthlyUsed() >= limit {
		return true
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

// IsOpenAICompatible classifies OpenAI, Grok, Kimi, Zhipu, and DeepSeek, and
// OpenCode as the platforms whose text requests use the OpenAI-compatible
// gateway family.
func IsOpenAICompatible(platform string) bool {
	return platform == PlatformOpenAI || platform == PlatformGrok || platform == PlatformOpenCode || platformRuleRegistry[platform].text != nil
}
