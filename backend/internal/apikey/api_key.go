package apikey

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ipmatch"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// api_key.go defines credential views and evaluates their lifecycle, quota,
// and spending-window values. Credential ownership stays separate from the
// hydrated payer and acting member used during authentication. Total quota and
// spending windows describe different admission checks, while expiry uses a
// strict comparison after the configured timestamp and unbounded quota has a
// distinct remaining-value sentinel.
//
// Window display helpers return zero for absent or elapsed starts while
// leaving the saved usage counters intact. Remaining quota is clamped at zero
// for an exhausted positive allowance and returns minus one for an unbounded
// allowance. Copy helpers clone group configuration and binding records, while
// retaining other references according to their declaration-level copying
// rules.

const (
	// Key status values distinguish disabled credentials from expiry and quota
	// exhaustion, which identity authentication leaves for later spending admission.
	StatusAPIKeyActive         = "active"          // active credential state
	StatusAPIKeyDisabled       = "disabled"        // administratively disabled credential
	StatusAPIKeyQuotaExhausted = "quota_exhausted" // exhausted total quota
	StatusAPIKeyExpired        = "expired"         // expired spending credential

	// Key Fast policies select whether provider processing follows the request
	// or forces acceleration, subject to the gateway's other policy decisions.
	APIKeyFastModePolicyFollowRequest = "follow_request" // request-selected Fast mode
	APIKeyFastModePolicyForceOn       = "force_on"       // forced enabled acceleration
	APIKeyFastModePolicyForceOff      = "force_off"      // forced disabled acceleration

	// Funding modes let admission and settlement select subscriptions, wallet
	// balance, or automatic allocation using the Key's selected subscription ID.
	APIKeyBillingModeAuto         = "auto"         // subscription then wallet
	APIKeyBillingModeSubscription = "subscription" // selected subscription funding
	APIKeyBillingModeBalance      = "balance"      // wallet balance funding

	// Spending window durations define how long each saved start and cost
	// remain current for admission and the displayed reset time.
	RateLimitWindow5h = 5 * time.Hour      // five-hour spending duration
	RateLimitWindow1d = 24 * time.Hour     // one-day spending duration
	RateLimitWindow7d = 7 * 24 * time.Hour // seven-day spending duration
)

// APIKey is a persisted credential with request identity and admission views.
type APIKey struct {
	ID int64 // persisted Key identifier
	// UserID identifies the credential's creator for team keys and the owner
	// for personal keys. Authentication resolves the team payer into User.
	UserID int64
	// TeamID identifies team scope when present. A team credential requires its
	// assigned member and current owner to pass lifecycle authentication.
	TeamID *int64
	// TeamOwnerDisabled records the owner's independent lock. IsActive rejects
	// this flag, and member updates cannot clear it by supplying active status.
	TeamOwnerDisabled bool
	Key               string // authentication credential text
	Name              string // saved console display name
	// GroupID binds an ordinary Key; IsComposite and CompositeGroups instead
	// store its ordered prefix choices. Request selection fills GroupID on an
	// effective composite view before the gateway checks that selected group.
	GroupID         *int64                 // ordinary or selected request group
	IsComposite     bool                   // prefix-selected group mode
	CompositeGroups []APIKeyCompositeGroup // ordered prefix group bindings
	Status          string                 // stored credential lifecycle state
	// FastModePolicy supplies request acceleration preference to gateway policy
	// processing. Higher priority group or global choices can affect forwarding.
	FastModePolicy string
	// BillingMode and PreferredSubscriptionID select the funding source. The
	// subscription mode requires a valid payer subscription; the other modes
	// clear the selected ID during service configuration validation.
	BillingMode             string // stored funding mode
	PreferredSubscriptionID *int64 // selected payer subscription identifier
	// ModelMapping redirects a client alias before group and provider mapping.
	// Authentication snapshot conversion copies this map for each request view.
	ModelMapping map[string]string
	IPWhitelist  []string // configured allowed IP patterns
	IPBlacklist  []string // configured denied IP patterns
	// CompiledIPWhitelist and CompiledIPBlacklist are derived from the stored IP
	// lists. Their retained PatternCount keeps wholly invalid allowlists restrictive
	// during authentication even when parsing produces no usable matches.
	CompiledIPWhitelist *ipmatch.CompiledIPRules `json:"-"` // parsed allowed IP rules
	CompiledIPBlacklist *ipmatch.CompiledIPRules `json:"-"` // parsed denied IP rules
	LastUsedAt          *time.Time               // latest persisted credential touch
	LastUsedIP          *string                  // latest nonempty usage IP
	// CreatedAt participates in team lifecycle validation: a member whose current
	// join time follows Key creation cannot reuse the earlier team credential.
	CreatedAt time.Time
	UpdatedAt time.Time // latest persisted Key mutation
	// User supplies the hydrated payer's permissions and funding data. Team
	// authentication replaces the creator view with the current team owner.
	User *User
	// ActorUser identifies the member assigned to a team credential. Personal
	// snapshot reconstruction uses the same user pointer as User when absent.
	ActorUser      *User
	Team           *Team           // hydrated team identity and state
	TeamMembership *TeamMembership // assigned member lifecycle and usage
	Group          *routing.Group  // effective request group configuration
	// FallbackWhenGroupUnavailable enables selection of the bound group's saved
	// unavailable target. The target still requires scope and payer permission.
	FallbackWhenGroupUnavailable bool
	// MaxTokenRateMultiplier caps the effective group Token multiplier. Zero
	// disables protection; positive values reject requests above the saved ceiling.
	MaxTokenRateMultiplier float64 // maximum effective Token multiplier
	// ConcurrencyLimit and RPMLimit apply across the Key's groups and instances.
	// Zero disables each independent request check, and consuming entry points
	// reserve configured capacity before forwarding to an upstream provider.
	ConcurrencyLimit int // maximum admitted active requests
	RPMLimit         int // maximum admitted requests per minute
	// CurrentConcurrency is filled for console reads when a live counter reader
	// is bound. Missing counters and failed reads leave a zero observation.
	CurrentConcurrency int
	// ManagedBy identifies a hidden server-provisioned credential when present.
	// User-facing service reads and mutation methods return missing-key errors.
	ManagedBy *string

	// Quota and QuotaUsed are USD amounts for total Key spending. A nonpositive
	// Quota is unbounded, while a positive limit is exhausted when usage reaches it.
	Quota     float64    // total spending allowance in USD
	QuotaUsed float64    // accumulated Key spending in USD
	ExpiresAt *time.Time // nil: no expiry

	// RateLimit5h, RateLimit1d and RateLimit7d are USD allowances; the matching
	// Usage5h, Usage1d and Usage7d counters are current only while Window5hStart,
	// Window1dStart and Window7dStart remain within their durations. Missing starts
	// make the display helpers return zero usage. Zero allowances disable checks.
	RateLimit5h   float64    // five-hour USD spending allowance
	RateLimit1d   float64    // one-day USD spending allowance
	RateLimit7d   float64    // seven-day USD spending allowance
	Usage5h       float64    // saved five-hour USD usage
	Usage1d       float64    // saved one-day USD usage
	Usage7d       float64    // saved seven-day USD usage
	Window5hStart *time.Time // saved five-hour window start
	Window1dStart *time.Time // saved one-day window start
	Window7dStart *time.Time // saved seven-day window start
}

// APIKeyCompositeGroup binds one normalized model prefix to a selectable group.
type APIKeyCompositeGroup struct {
	ID       int64 // persisted mapping row identifier
	APIKeyID int64 // owning composite Key identifier
	GroupID  int64 // configured target group identifier
	// Prefix and NormalizedPrefix retain the displayed label and lowercase lookup
	// value for the same binding. SortOrder records submitted order for storage
	// reads and aggregated model lists, which use these bindings in sequence.
	Prefix           string // displayed group selection prefix
	NormalizedPrefix string // lowercase prefix lookup text
	SortOrder        int    // configured mapping order index
	// UserGroupRPMOverride is payer-specific request data prefetched into the
	// authentication snapshot. Nil requests a later override lookup for admission.
	UserGroupRPMOverride *int
	Group                *routing.Group // hydrated target group configuration
}

// APIKeyListFilters selects console keys by text, state, group, and scope.
type APIKeyListFilters struct {
	Search string // case-insensitive name or credential
	Status string // requested stored lifecycle state
	// GroupID selects unbound ordinary keys at zero, all groups when nil, and
	// direct or composite bindings when a nonzero ID is supplied.
	GroupID *int64
	// Scope accepts personal or team in the PostgreSQL query. Other values,
	// including an omitted value, leave both scopes eligible for listing.
	Scope string
}

// NormalizeAPIKeyFastModePolicy validates the supplied Key acceleration choice
// and reports success. Empty input selects follow_request; accepted strings are
// matched as supplied, so whitespace and alternate casing are rejected.
func NormalizeAPIKeyFastModePolicy(value string) (string, bool) {
	switch value {
	case "", APIKeyFastModePolicyFollowRequest:
		return APIKeyFastModePolicyFollowRequest, true
	case APIKeyFastModePolicyForceOn, APIKeyFastModePolicyForceOff:
		return value, true
	default:
		return "", false
	}
}

// NormalizeAPIKeyBillingMode delegates funding-mode normalization and
// validation to billing. The normalizer trims and lowercases text, accepts an
// omitted value as auto, and reports false for unsupported modes.
func NormalizeAPIKeyBillingMode(value string) (string, bool) {
	return billing.NormalizeAPIKeyBillingMode(value)
}

// APIKeyEffectiveBillingMode returns the normalized funding mode of key for
// request processing. Nil keys and missing or invalid values select auto, so
// callers needing validation use NormalizeAPIKeyBillingMode directly.
func APIKeyEffectiveBillingMode(key *APIKey) string {
	if key == nil {
		return APIKeyBillingModeAuto
	}
	mode, ok := NormalizeAPIKeyBillingMode(key.BillingMode)
	if !ok {
		return APIKeyBillingModeAuto
	}
	return mode
}

// IsWindowExpired reports whether start is absent or the elapsed wall-clock
// duration has reached window. The delegated billing helper samples time.Since
// at the call, so equality expires a present window.
func IsWindowExpired(start *time.Time, window time.Duration) bool {
	return billing.IsWindowExpired(start, window)
}

// IsActive reports whether the stored active state permits this credential's
// identity check. A team owner lock returns false even with active status;
// callers perform expiry and quota admission separately.
func (k *APIKey) IsActive() bool {
	return k.Status == StatusActive && !k.TeamOwnerDisabled
}

// HasRateLimits reports whether any positive spending window allowance is
// configured.
func (k *APIKey) HasRateLimits() bool {
	return k.RateLimit5h > 0 || k.RateLimit1d > 0 || k.RateLimit7d > 0
}

// IsExpired reports whether wall-clock time is strictly after the configured
// expiry. A nil ExpiresAt is unexpired and returns before reading time;
// timestamp equality remains unexpired for the credential.
func (k *APIKey) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return k.IsExpiredAt(time.Now())
}

// IsQuotaExhausted reports whether saved usage reaches a positive total quota.
// Nonpositive quota is unbounded and returns false; this check uses the current
// view's fields, so live settlement can advance usage separately.
func (k *APIKey) IsQuotaExhausted() bool {
	if k.Quota <= 0 {
		return false
	}
	return k.QuotaUsed >= k.Quota
}

// GetQuotaRemaining returns the saved remaining USD quota, clamped at zero
// after exhaustion. Nonpositive quota returns -1 as the unbounded sentinel,
// which callers distinguish from an exhausted positive allowance.
func (k *APIKey) GetQuotaRemaining() float64 {
	if k.Quota <= 0 {
		return -1
	}
	remaining := k.Quota - k.QuotaUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetDaysUntilExpiry returns whole 24-hour periods remaining until the
// configured expiry. Nil expiry returns -1, elapsed expiry returns zero, and a
// future expiry less than one day away also returns zero.
func (k *APIKey) GetDaysUntilExpiry() int {
	if k.ExpiresAt == nil {
		return -1
	}
	duration := time.Until(*k.ExpiresAt)
	if duration < 0 {
		return 0
	}
	// Fractional days are truncated, so a future expiry within 24 hours has
	// the same displayed zero as an already elapsed expiry.
	return int(duration.Hours() / 24)
}

// EffectiveUsage5h returns saved five-hour USD spending while its window
// remains current. A missing or expired window returns zero; the stored counter
// is retained for persistence and later accounting.
func (k *APIKey) EffectiveUsage5h() float64 {
	if IsWindowExpired(k.Window5hStart, RateLimitWindow5h) {
		return 0
	}
	return k.Usage5h
}

// EffectiveUsage1d returns saved one-day USD spending while its window remains
// current. A missing or expired window returns zero; this display decision
// leaves the Key's persisted counter and start unchanged.
func (k *APIKey) EffectiveUsage1d() float64 {
	if IsWindowExpired(k.Window1dStart, RateLimitWindow1d) {
		return 0
	}
	return k.Usage1d
}

// EffectiveUsage7d returns saved seven-day USD spending while its window
// remains current. A missing or expired window returns zero, allowing callers
// to present current usage separately from the saved counter.
func (k *APIKey) EffectiveUsage7d() float64 {
	if IsWindowExpired(k.Window7dStart, RateLimitWindow7d) {
		return 0
	}
	return k.Usage7d
}

// IsExpiredAt reports whether now is strictly after a present expiry timestamp.
// Missing expiry and timestamp equality remain unexpired; the supplied time
// lets service callers apply their configured clock.
func (k *APIKey) IsExpiredAt(now time.Time) bool {
	return k.ExpiresAt != nil && now.After(*k.ExpiresAt)
}

// CopyAPIKey copies the Key record, shallow user records, group configuration,
// and composite binding records with cloned groups. Nil input remains nil.
// Other Key references, user slices and pointers, and composite RPM override
// pointers remain shared; callers needing cache isolation use the
// authentication snapshot conversion helpers.
func CopyAPIKey(value *APIKey) *APIKey {
	if value == nil {
		return nil
	}
	out := *value
	out.User = identity.CopyUser(value.User)
	out.ActorUser = identity.CopyUser(value.ActorUser)
	out.Group = routing.CloneGroup(value.Group)
	out.CompositeGroups = CopyCompositeGroups(value.CompositeGroups)
	return &out
}

// CopyCompositeGroup clones the binding's group configuration and retains its
// scalar fields. UserGroupRPMOverride remains the same pointer, so callers
// treat that request override as shared input after this copy.
func CopyCompositeGroup(value APIKeyCompositeGroup) APIKeyCompositeGroup {
	value.Group = routing.CloneGroup(value.Group)
	return value
}

// CopyCompositeGroups allocates binding records and clones each referenced
// group configuration. Nil input remains nil and empty input remains nonnil;
// each binding's RPM override pointer stays shared with its source.
func CopyCompositeGroups(values []APIKeyCompositeGroup) []APIKeyCompositeGroup {
	if values == nil {
		return nil
	}
	out := make([]APIKeyCompositeGroup, len(values))
	for i, value := range values {
		out[i] = CopyCompositeGroup(value)
	}
	return out
}

// GroupFromRouting returns a cloned group configuration for authentication and
// management. Nil input remains nil; the routing copier isolates its supported
// mutable configuration fields before callers change that view.
func GroupFromRouting(group *routing.Group) *routing.Group {
	return routing.CloneGroup(group)
}

// RoutingGroup returns a cloned group configuration for callers presenting
// routing data. Nil input remains nil, and the routing copier allocates its
// supported maps, slices, and scalar configuration pointers.
func RoutingGroup(group *routing.Group) *routing.Group {
	return routing.CloneGroup(group)
}
