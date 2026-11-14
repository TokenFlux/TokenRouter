package dto

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// key.go builds console Key responses from saved configuration, live-count
// observations, and current spending-window display values. The response
// includes group DTOs chosen by its supplied mapper, while payer user objects
// stay outside this format so team credentials do not expose the owner's
// account assets. Callers authorize the console operation before exposing the
// credential response.
//
// Conversion samples effective usage and window expiry independently through
// wall-clock helpers, then calculates reset timestamps for windows still
// current at those calls. Model redirects receive a copied map; the other
// scalar pointers and IP slices retain their input storage. Each composite
// binding receives its group presentation and displayed prefix, and TeamID
// presence selects the response scope.

// APIKey is the console credential response with caller-selected group
// presentation data.
type APIKey[G any] struct {
	ID     int64 `json:"id"`      // persisted credential record identifier
	UserID int64 `json:"user_id"` // persisted creator user identifier
	// TeamID and Scope describe the same presentation scope: any present TeamID
	// selects team, while nil selects personal. Conversion retains the supplied
	// TeamID pointer, so response consumers treat it as shared input.
	TeamID            *int64                    `json:"team_id"`             // credential team scope identifier
	Scope             string                    `json:"scope"`               // personal or team response scope
	TeamOwnerDisabled bool                      `json:"team_owner_disabled"` // persisted independent owner lock
	Key               string                    `json:"key"`                 // current authentication credential text
	Name              string                    `json:"name"`                // saved credential display name
	GroupID           *int64                    `json:"group_id"`            // ordinary or selected group identifier
	IsComposite       bool                      `json:"is_composite"`        // prefix selection mode flag
	CompositeGroups   []APIKeyCompositeGroup[G] `json:"composite_groups"`    // ordered presented composite group bindings
	Status            string                    `json:"status"`              // stored credential lifecycle state
	FastModePolicy    string                    `json:"fast_mode_policy"`    // stored Key acceleration preference
	// BillingMode and PreferredSubscriptionID describe the selected payer funding
	// choice. Configuration writes and request admission validate eligibility;
	// listing maps saved values, and the subscription ID remains a shared pointer.
	BillingMode                  string            `json:"billing_mode"`                    // selected funding source mode
	PreferredSubscriptionID      *int64            `json:"preferred_subscription_id"`       // selected payer subscription identifier
	ModelMapping                 map[string]string `json:"model_mapping"`                   // copied Key model redirects
	IPWhitelist                  []string          `json:"ip_whitelist"`                    // shared allowed IP patterns
	IPBlacklist                  []string          `json:"ip_blacklist"`                    // shared denied IP patterns
	LastUsedAt                   *time.Time        `json:"last_used_at"`                    // latest persisted Key touch
	LastUsedIP                   *string           `json:"last_used_ip"`                    // latest nonempty usage IP
	Quota                        float64           `json:"quota"`                           // total USD allowance; zero unbounded
	QuotaUsed                    float64           `json:"quota_used"`                      // saved accumulated USD spending
	ExpiresAt                    *time.Time        `json:"expires_at"`                      // nil: no expiry
	CreatedAt                    time.Time         `json:"created_at"`                      // persisted Key creation timestamp
	UpdatedAt                    time.Time         `json:"updated_at"`                      // latest persisted Key mutation
	FallbackWhenGroupUnavailable bool              `json:"fallback_when_group_unavailable"` // configured unavailable-target selection flag
	CurrentConcurrency           int               `json:"current_concurrency"`             // observed active request count
	// MaxTokenRateMultiplier caps the effective group Token multiplier. Zero
	// disables protection; positive values reject requests above the saved ceiling.
	MaxTokenRateMultiplier float64 `json:"max_token_rate_multiplier"` // maximum effective Token multiplier
	// ConcurrencyLimit and RPMLimit use zero for unbounded request allowances.
	// RateLimit5h, RateLimit1d and RateLimit7d describe separate USD allowances;
	// Usage5h, Usage1d and Usage7d display zero for ended or absent windows.
	ConcurrencyLimit int        `json:"concurrency_limit"` // maximum admitted active requests
	RPMLimit         int        `json:"rpm_limit"`         // maximum admitted requests per minute
	RateLimit5h      float64    `json:"rate_limit_5h"`     // five-hour USD spending allowance
	RateLimit1d      float64    `json:"rate_limit_1d"`     // one-day USD spending allowance
	RateLimit7d      float64    `json:"rate_limit_7d"`     // seven-day USD spending allowance
	Usage5h          float64    `json:"usage_5h"`          // current five-hour USD spending
	Usage1d          float64    `json:"usage_1d"`          // current one-day USD spending
	Usage7d          float64    `json:"usage_7d"`          // current seven-day USD spending
	Window5hStart    *time.Time `json:"window_5h_start"`   // saved five-hour window start
	Window1dStart    *time.Time `json:"window_1d_start"`   // saved one-day window start
	Window7dStart    *time.Time `json:"window_7d_start"`   // saved seven-day window start
	// Reset5hAt, Reset1dAt and Reset7dAt are present when their saved starts are
	// current at conversion time. They use start plus the corresponding duration
	// while the saved start pointers remain shared.
	Reset5hAt *time.Time `json:"reset_5h_at,omitempty"` // current five-hour reset timestamp
	Reset1dAt *time.Time `json:"reset_1d_at,omitempty"` // current one-day reset timestamp
	Reset7dAt *time.Time `json:"reset_7d_at,omitempty"` // current seven-day reset timestamp
	Group     *G         `json:"group,omitempty"`       // caller-presented selected group data
}

// APIKeyCompositeGroup presents a composite prefix and its caller-mapped target
// group.
type APIKeyCompositeGroup[G any] struct {
	GroupID int64  `json:"group_id"`        // configured target group identifier
	Prefix  string `json:"prefix"`          // displayed group selection prefix
	Group   *G     `json:"group,omitempty"` // caller-presented target group data
}

// APIKeyFromKey builds a console Key response using the supplied group mapper,
// returning nil for nil input. Callers provide a mapper that accepts a nil
// group and authorize the operation before exposing the stored secret.
//
// ModelMapping is copied, while scalar pointers and IP lists retain their input
// storage. Effective usage and reset expiry are sampled in separate wall-clock
// calls, so a window can end during conversion. Current windows receive
// start-plus-duration reset timestamps; composite groups preserve input order,
// and any nonnil TeamID selects team scope. The mapper controls group DTO
// ownership and details.
func APIKeyFromKey[G any](k *apikey.APIKey, group func(*routing.Group) *G) *APIKey[G] {
	if k == nil {
		return nil
	}
	out := &APIKey[G]{
		ID:                           k.ID,
		UserID:                       k.UserID,
		TeamID:                       k.TeamID,
		TeamOwnerDisabled:            k.TeamOwnerDisabled,
		Key:                          k.Key,
		Name:                         k.Name,
		GroupID:                      k.GroupID,
		IsComposite:                  k.IsComposite,
		Status:                       k.Status,
		FastModePolicy:               k.FastModePolicy,
		BillingMode:                  k.BillingMode,
		PreferredSubscriptionID:      k.PreferredSubscriptionID,
		ModelMapping:                 apikey.CloneModelMapping(k.ModelMapping),
		IPWhitelist:                  k.IPWhitelist,
		IPBlacklist:                  k.IPBlacklist,
		LastUsedAt:                   k.LastUsedAt,
		LastUsedIP:                   k.LastUsedIP,
		Quota:                        k.Quota,
		QuotaUsed:                    k.QuotaUsed,
		ExpiresAt:                    k.ExpiresAt,
		CreatedAt:                    k.CreatedAt,
		UpdatedAt:                    k.UpdatedAt,
		MaxTokenRateMultiplier:       k.MaxTokenRateMultiplier,
		ConcurrencyLimit:             k.ConcurrencyLimit,
		RPMLimit:                     k.RPMLimit,
		RateLimit5h:                  k.RateLimit5h,
		RateLimit1d:                  k.RateLimit1d,
		RateLimit7d:                  k.RateLimit7d,
		Usage5h:                      k.EffectiveUsage5h(),
		Usage1d:                      k.EffectiveUsage1d(),
		Usage7d:                      k.EffectiveUsage7d(),
		Window5hStart:                k.Window5hStart,
		Window1dStart:                k.Window1dStart,
		Window7dStart:                k.Window7dStart,
		FallbackWhenGroupUnavailable: k.FallbackWhenGroupUnavailable,
		CurrentConcurrency:           k.CurrentConcurrency,
		Group:                        group(k.Group),
	}
	out.CompositeGroups = make([]APIKeyCompositeGroup[G], 0, len(k.CompositeGroups))
	for _, binding := range k.CompositeGroups {
		out.CompositeGroups = append(out.CompositeGroups, APIKeyCompositeGroup[G]{
			GroupID: binding.GroupID,
			Prefix:  binding.Prefix,
			Group:   group(binding.Group),
		})
	}
	if k.TeamID != nil {
		out.Scope = "team"
	} else {
		out.Scope = "personal"
	}
	// Effective usage and these reset checks sample time independently. A
	// window can end between them, so reset timestamps follow each check's current state.
	if k.Window5hStart != nil && !apikey.IsWindowExpired(k.Window5hStart, apikey.RateLimitWindow5h) {
		t := k.Window5hStart.Add(apikey.RateLimitWindow5h)
		out.Reset5hAt = &t
	}
	if k.Window1dStart != nil && !apikey.IsWindowExpired(k.Window1dStart, apikey.RateLimitWindow1d) {
		t := k.Window1dStart.Add(apikey.RateLimitWindow1d)
		out.Reset1dAt = &t
	}
	if k.Window7dStart != nil && !apikey.IsWindowExpired(k.Window7dStart, apikey.RateLimitWindow7d) {
		t := k.Window7dStart.Add(apikey.RateLimitWindow7d)
		out.Reset7dAt = &t
	}
	return out
}
