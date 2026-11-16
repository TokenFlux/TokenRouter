package apikey

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/team"
)

// options.go defines authentication configuration and dependency interfaces
// and resolves the bound group Fast-policy callback. Startup captures cache
// sizes, lifetimes, and lookup protection, while time and group policy can use
// bound callbacks. User, subscription, team and live-count interfaces supply
// the specific current data required by Key use cases, and the caller binds
// optional dependencies before concurrent service use.

// Identity and team lifecycle labels reuse the owning modules' values so
// authentication compares the same active and owner states as those services.
const (
	StatusActive = "active"

	TeamStatusActive = team.TeamStatusActive
	TeamRoleOwner    = team.TeamRoleOwner
)

// Identity, team and billing errors retain their owning modules' identities
// so HTTP adapters and Key callers can classify delegated rejection causes.
var (
	ErrUserNotFound = identity.ErrUserNotFound

	ErrInsufficientPerms         = identity.ErrInsufficientPerms
	ErrTeamFeatureDisabled       = team.ErrTeamFeatureDisabled
	ErrTeamMembershipRequired    = team.ErrTeamMembershipRequired
	ErrTeamNotFound              = team.ErrTeamNotFound
	ErrTeamSuspended             = team.ErrTeamSuspended
	ErrTeamMemberDailyExceeded   = billing.ErrTeamMemberDailyExceeded
	ErrTeamMemberWeeklyExceeded  = billing.ErrTeamMemberWeeklyExceeded
	ErrTeamMemberMonthlyExceeded = billing.ErrTeamMemberMonthlyExceeded
)

// APIKeyAuthCacheConfig supplies startup cache sizes, lifetimes and lookup
// protection settings.
type APIKeyAuthCacheConfig struct {
	// L1Size and L1TTLSeconds jointly enable positive local caching when both
	// are positive. L2TTLSeconds independently enables shared caching, while
	// NegativeTTLSeconds enables bounded local misses; nonpositive lifetimes
	// disable their layer when the service checks its captured cache configuration.
	L1Size             int `mapstructure:"l1_size"`              // positive local entry capacity
	L1TTLSeconds       int `mapstructure:"l1_ttl_seconds"`       // local lifetime in seconds
	L2TTLSeconds       int `mapstructure:"l2_ttl_seconds"`       // shared lifetime in seconds
	NegativeTTLSeconds int `mapstructure:"negative_ttl_seconds"` // negative lifetime in seconds
	// JitterPercent controls expiration sampling in KeyJitterTTL. Nonpositive
	// values leave lifetimes unchanged; positive values are capped at one hundred.
	JitterPercent int  `mapstructure:"jitter_percent"` // lifetime variation percentage allowance
	Singleflight  bool `mapstructure:"singleflight"`   // shared credential-load enablement flag
	// LookupConcurrency overrides the constructor's default when positive.
	// NewAPIKeyService captures the selected capacity in its bounded storage-load channel.
	LookupConcurrency int                    `mapstructure:"lookup_concurrency"` // maximum direct credential loads
	InvalidAbuse      InvalidAuthAbuseConfig `mapstructure:"invalid_abuse"`      // per-client failed-authentication tracking settings
}

// InvalidAuthAbuseConfig configures bounded per-client failure tracking and
// temporary admission blocks.
type InvalidAuthAbuseConfig struct {
	// Enabled selects tracking only when Threshold, WindowSeconds, BlockSeconds
	// and Capacity are all positive. KeyNewInvalidAuthAbuseLimiter rejects the
	// entire configuration when any required value is nonpositive.
	Enabled       bool `mapstructure:"enabled"`        // invalid-client tracking enablement flag
	Threshold     int  `mapstructure:"threshold"`      // failures before temporary blocking
	WindowSeconds int  `mapstructure:"window_seconds"` // failure window in seconds
	BlockSeconds  int  `mapstructure:"block_seconds"`  // temporary block in seconds
	Capacity      int  `mapstructure:"capacity"`       // maximum individually tracked clients
}

// Options supplies authentication startup values and retained callbacks for Key
// operations.
type Options struct {
	// TokenRateScale reads the current group Token peak factor for admission.
	// Nil uses one; read failures reject protected requests before forwarding.
	TokenRateScale func(context.Context, int64, time.Time) (float64, error)

	// Now is read at each expiry or outbox scheduling decision when supplied.
	// Install a concurrency-safe callback before concurrent use of the retained Options.
	Now func() time.Time
	// Calendar supplies the location captured by NewAPIKeyService for daily
	// counter names. The constructed service calendar reads the wall clock in that location.
	Calendar   timezone.Calendar     // calendar for daily usage dates
	APIKeyAuth APIKeyAuthCacheConfig // captured authentication cache configuration
	// Default is retained for later credential generation. An empty or
	// whitespace-only prefix selects sk-; otherwise generation uses the supplied text.
	Default struct {
		APIKeyPrefix string // ordinary generated credential prefix
	}
	// Team supplies the team access switch read by lifecycle and scope
	// methods. Callers finish configuring these retained values before concurrent service use.
	Team struct {
		Enabled bool // configured team access enablement
	}
	// GroupFastPolicy normalizes raw and legacy group acceleration inputs when
	// the service setter is absent. Callers bind it before concurrent snapshot reads.
	GroupFastPolicy func(string, bool) string
}

type (
	// User is the identity user view read for permissions and payer state.
	User = identity.User
	// NotifyEmailEntry is the contact recipient record carried in payer notice
	// snapshots.
	NotifyEmailEntry = contact.Entry
	// Team is the team identity and lifecycle view required by authentication.
	Team = team.Team
	// TeamMembership is the current member relationship and allowance data read by
	// authentication.
	TeamMembership = team.TeamMembership
	// UserSubscription is the billing subscription view used to validate specified
	// funding.
	UserSubscription = billing.UserSubscription
	// UserSubscriptionRepository reads specified and active payer subscriptions
	// for Key configuration.
	UserSubscriptionRepository interface {
		// GetByID loads one current subscription for ownership and effectiveness
		// checks. A missing or failed read is treated as an invalid specified
		// funding choice by Key configuration validation.
		GetByID(context.Context, int64) (*UserSubscription, error)
		// ListActiveByUserID returns subscription candidates for the current payer.
		// Key option listing filters them again for current effectiveness and a
		// present plan before presenting available funding choices.
		ListActiveByUserID(context.Context, int64) ([]UserSubscription, error)
	}
)

type (
	// UserGroupRateRepository is the billing reader for payer-group multipliers
	// and RPM overrides.
	UserGroupRateRepository = billing.UserGroupRateRepository
	// UserRepository reads current identity user views required by Key operations.
	UserRepository interface {
		// GetByID loads the user view for a creator or payer identifier.
		// Callers check the error before using it; Key authentication depends on
		// the returned status and team owner permission data.
		GetByID(context.Context, int64) (*User, error)
	}
)

// GroupRepository loads group views for binding validation and runtime
// selection.
type GroupRepository interface {
	// GetByID returns a group with configuration needed to validate a saved Key
	// binding. Missing or failed loads return an error; callers separately check
	// binding permission or any required active state.
	GetByID(context.Context, int64) (*routing.Group, error)
	// GetByIDLite loads the target configuration used during runtime group
	// selection. Callers check that the returned group is present and active, then
	// verify payer permission before binding a request view.
	GetByIDLite(context.Context, int64) (*routing.Group, error)
	// ListActive returns group candidates whose stored state permits listing as
	// active. Key callers apply payer binding permission and selected-plan
	// restrictions before these candidates are presented for configuration.
	ListActive(context.Context) ([]routing.Group, error)
}

// TeamRepository reads a current member context including its team and payer.
type TeamRepository interface {
	// GetContextByUserID loads the current team, assigned membership and owner for
	// userID. Successful production contexts contain all three records; Key
	// hydration checks team identity and join time before reading actor and payer
	// users.
	GetContextByUserID(context.Context, int64) (*team.TeamContext, error)
}

// ConcurrencyReader supplies observed active request counts for Key console
// presentation.
type ConcurrencyReader interface {
	// GetAPIKeyConcurrencyBatch reads current active counts keyed by supplied Key
	// IDs. Missing map entries represent zero; service console reads suppress
	// errors and leave zero or existing observations rather than rejecting the
	// underlying Key list.
	GetAPIKeyConcurrencyBatch(context.Context, []int64) (map[int64]int, error)
}

// cloneGroupClientProtocols allocates a copy of the configured protocol
// identifiers. Nil input remains nil and an empty nonnil input remains
// allocated, retaining the admission distinction between omitted and empty
// lists.
func cloneGroupClientProtocols(values []protocol.ProtocolID) []protocol.ProtocolID {
	if values == nil {
		return nil
	}
	out := make([]protocol.ProtocolID, len(values))
	copy(out, values)
	return out
}

// subscriptionPlanIncludesGroup delegates group coverage to billing using the
// supplied plan. A nil plan or nonpositive group ID rejects; an empty plan
// group list permits every positive group, leaving payer permission checks to
// the caller.
func subscriptionPlanIncludesGroup(plan *billing.SubscriptionPlan, id int64) bool {
	return billing.SubscriptionAllowsGroup(&billing.UserSubscription{Plan: plan}, id)
}

// SetGroupFastPolicy binds the group acceleration normalizer used during
// ordinary snapshot construction. The service retains this callback, and the
// setter is unsynchronized; callers install it before concurrent snapshot
// creation.
func (s *APIKeyService) SetGroupFastPolicy(policy func(string, bool) string) {
	s.groupFastPolicy = policy
}

// groupPolicy resolves a group's acceleration choice through the bound service
// callback, then Options callback. With neither present it returns the supplied
// raw group choice; callers provide a nonnil group and normalize standalone
// inputs themselves.
func (s *APIKeyService) groupPolicy(g *routing.Group) string {
	if s.groupFastPolicy != nil {
		return s.groupFastPolicy(g.OpenAIFastPolicy, g.ForceOpenAIFast)
	}
	if s.cfg != nil && s.cfg.GroupFastPolicy != nil {
		return s.cfg.GroupFastPolicy(g.OpenAIFastPolicy, g.ForceOpenAIFast)
	}
	// Standalone assembly can omit both callbacks. Return the supplied group
	// value in that case, so its caller owns any required normalization.
	return g.OpenAIFastPolicy
}
