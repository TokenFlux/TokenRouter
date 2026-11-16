package usage

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// views.go defines loaded account, API-key, provider, and group views attached
// to usage records.
// Retained usage rows can be displayed beside current account, key, provider
// and group metadata. These views carry the fields consumed by nested HTTP
// DTO conversion, including optional associations and mutable collections
// whose sharing follows the owning mapper.

type (
	// GroupView names routing group configuration reused for nested usage
	// response display.
	GroupView = accessview.GroupConfig
	// ProviderView carries the provider identity used by administrator usage
	// response display.
	ProviderView struct {
		ID   int64  // loaded provider identifier
		Name string // loaded provider name
	}
)

// KeyCompositeGroupView carries one loaded composite-key binding for usage
// response conversion.
type KeyCompositeGroupView struct {
	ID, APIKeyID, GroupID    int64      // ID: binding identifier; APIKeyID: owning key; GroupID: selected group
	Prefix, NormalizedPrefix string     // Prefix: display prefix; NormalizedPrefix: comparison prefix
	SortOrder                int        // binding display order
	UserGroupRPMOverride     *int       // binding request rate override
	Group                    *GroupView // loaded binding group data
}

// UserView carries loaded account metadata consumed by shallow usage HTTP
// conversion.
type UserView struct {
	ID            int64   // loaded account identifier
	Email         string  // account contact email
	Username      string  // account display username
	Role          string  // stored account role
	Balance       float64 // wallet balance USD
	FrozenBalance float64 // reserved wallet amount USD
	Concurrency   int     // user concurrency limit
	Status        string  // stored account status
	// AllowedGroups and DisabledPublicGroups retain account visibility lists
	// supplied to userFromView. The shallow identity mapper passes these slices
	// through, so independent mutation needs separate storage.
	AllowedGroups        []int64    // permitted private group identifiers
	DisabledPublicGroups []int64    // disabled public group identifiers
	LastActiveAt         *time.Time // optional panel activity instant
	CreatedAt            time.Time  // account creation instant
	UpdatedAt            time.Time  // account update instant
	DeletedAt            *time.Time // optional soft deletion instant
	BalanceNotifyEnabled bool       // balance notification enable flag
	// BalanceNotifyThresholdType and BalanceNotifyThreshold select fixed or
	// percentage alert thresholds in billing. A nil threshold uses the global
	// setting; the view carries these stored choices into account display.
	BalanceNotifyThresholdType string          // balance notification threshold kind
	BalanceNotifyThreshold     *float64        // optional notification threshold value
	BalanceNotifyExtraEmails   []contact.Entry // additional notification contact entries
	TotalRecharged             float64         // lifetime recharge amount USD
	RPMLimit                   int             // user requests per minute; zero unlimited
	APIKeyLimit                int             // account key count limit; zero unlimited
}

// KeyView carries loaded key metadata consumed by nested usage HTTP
// conversion.
type KeyView struct {
	ID                int64  // loaded key identifier
	UserID            int64  // key actor user identifier
	TeamID            *int64 // optional owning team identifier
	TeamOwnerDisabled bool   // team owner disable flag
	Key               string // loaded key credential text
	Name              string // key display name
	GroupID           *int64 // optional direct group identifier
	IsComposite       bool   // composite binding enable flag
	// CompositeGroups holds loaded bindings that keyFromView copies into a new
	// binding slice. Nested group and optional override pointers retain their
	// loaded references during conversion.
	CompositeGroups         []KeyCompositeGroupView // loaded composite group bindings
	Status                  string                  // stored key status
	FastModePolicy          string                  // key Fast mode policy
	ForceClaude1hCache      bool                    // key Claude 1h cache override
	BillingMode             string                  // selected key funding mode
	PreferredSubscriptionID *int64                  // optional selected subscription identifier
	// ModelMapping supplies key alias mappings to keyFromView. The final API-key
	// DTO mapper copies this map before returning the nested response.
	ModelMapping map[string]string // key model alias mappings
	// IPWhitelist and IPBlacklist hold the key's access-list entries.
	// keyFromView and the API-key DTO mapper retain these slice references, so
	// response readers treat them as immutable.
	IPWhitelist                  []string   // permitted client address entries
	IPBlacklist                  []string   // blocked client address entries
	LastUsedAt                   *time.Time // optional latest use instant
	LastUsedIP                   *string    // optional latest client address
	CreatedAt                    time.Time  // key creation instant
	UpdatedAt                    time.Time  // key update instant
	Group                        *GroupView // loaded direct group data
	FallbackWhenGroupUnavailable bool       // group fallback enable flag
	CurrentConcurrency           int        // current key request count
	ManagedBy                    *string    // optional key management label
	Quota                        float64    // lifetime spending limit USD; zero unbounded
	QuotaUsed                    float64    // lifetime charged amount USD
	ExpiresAt                    *time.Time // optional key expiration instant
	// RateLimit5h, RateLimit1d and RateLimit7d hold window spending allowances
	// in USD. Positive values enable their respective limits; the view passes
	// these stored choices to the key DTO mapper.
	RateLimit5h float64 // five hour spending limit USD
	RateLimit1d float64 // one day spending limit USD
	RateLimit7d float64 // seven day spending limit USD
	// Usage5h, Usage1d and Usage7d pair with Window5hStart, Window1dStart and
	// Window7dStart respectively. The key DTO mapper derives effective spending
	// from those anchors, returning zero for absent or expired windows.
	Usage5h       float64    // stored five hour charge USD
	Usage1d       float64    // stored one day charge USD
	Usage7d       float64    // stored seven day charge USD
	Window5hStart *time.Time // optional five hour window anchor
	Window1dStart *time.Time // optional one day window anchor
	Window7dStart *time.Time // optional seven day window anchor
}
