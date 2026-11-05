package dto

import (
	"time"

	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// user.go constructs ordinary and administrator account responses from
// identity data.
// User JSON combines account fields with caller-supplied API Key data and
// converted subscription values. Ordinary responses use User, while
// administrator responses add account notes, last use, and group rate
// overrides; callers choose the response type appropriate for their route.

// User represents panel account data with the caller-selected API Key element
// type.
type User[K any] struct {
	// PreferredLocale retains the account language pointer. Nil serializes as
	// null so clients can distinguish an unset preference from a selected
	// language.
	PreferredLocale *string `json:"preferred_locale"`
	ID              int64   `json:"id"`             // account row identifier
	Email           string  `json:"email"`          // account email address
	Username        string  `json:"username"`       // account display name
	Role            string  `json:"role"`           // global account role
	Balance         float64 `json:"balance"`        // USD
	FrozenBalance   float64 `json:"frozen_balance"` // USD
	Concurrency     int     `json:"concurrency"`    // account concurrency allowance
	Status          string  `json:"status"`         // account lifecycle status
	// AllowedGroups retains the account slice of exclusive-group grants. Callers
	// treating response data as mutable must copy this slice before changing its
	// elements.
	AllowedGroups []int64 `json:"allowed_groups"`
	// DisabledPublicGroups retains the account slice of administrator-denied
	// public group IDs. Mutating the response slice changes the supplied account
	// slice unless the caller copies it.
	DisabledPublicGroups []int64 `json:"disabled_public_groups"`
	// LastActiveAt shares the account timestamp pointer. Copy the pointed-to time
	// before modifying a response that needs independent timestamp storage.
	LastActiveAt *time.Time `json:"last_active_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"` // account creation timestamp
	UpdatedAt    time.Time  `json:"updated_at"` // account update timestamp
	// DeletedAt shares the account deletion-time pointer. Nil omits the JSON
	// field; copy the time before changing it through a mutable response.
	DeletedAt *time.Time `json:"deleted_at,omitempty"`

	// BalanceNotifyEnabled gates account alerts. BalanceNotifyThresholdType
	// selects fixed or percentage interpretation of BalanceNotifyThreshold;
	// TotalRecharged supplies the percentage base, and BalanceNotifyExtraEmails
	// lists additional saved recipients.
	BalanceNotifyEnabled bool `json:"balance_notify_enabled"` // balance alert switch
	// Fixed or percentage interpretation.
	BalanceNotifyThresholdType string `json:"balance_notify_threshold_type"`
	// BalanceNotifyThreshold retains the optional account threshold pointer. Nil
	// lets billing select its global threshold; the selected type determines
	// amount or percentage interpretation.
	BalanceNotifyThreshold *float64 `json:"balance_notify_threshold"`
	// Copied notification recipient entries.
	BalanceNotifyExtraEmails []NotifyEmailEntry `json:"balance_notify_extra_emails"`
	TotalRecharged           float64            `json:"total_recharged"` // USD

	// RPMLimit is the user RPM limit, with zero meaning unlimited. Gateway checks
	// use it when the selected group and user/group override supply no RPM limit.
	RPMLimit int `json:"rpm_limit"`
	// APIKeyLimit caps account-owned API Key creation. Zero permits any count;
	// changing runtime creation defaults does not rewrite this stored user limit.
	APIKeyLimit int `json:"api_key_limit"`

	// APIKeys uses the caller-supplied slice directly and is omitted from JSON
	// when empty. Callers retain responsibility for its element ownership and
	// contents.
	APIKeys []K `json:"api_keys,omitempty"`
	// Subscriptions contains converted loaded account subscriptions and is
	// omitted when empty. UserFromIdentity allocates the outer slice, while
	// nested pointer fields follow the subscription converter.
	Subscriptions []billinghttpapi.UserSubscription `json:"subscriptions,omitempty"`
}

// AdminUser adds administrator-visible notes, activity, and recharge overrides
// to the account response.
type AdminUser[K any] struct {
	User[K] // ordinary account response
	// BalanceRechargeMultiplier is the administrator-configured recharge rate.
	// Null means new balance orders inherit the current global payment setting.
	BalanceRechargeMultiplier *float64 `json:"balance_recharge_multiplier"`

	Notes string `json:"notes"` // administrator account note
	// LastUsedAt shares the account's latest request-use timestamp pointer. Nil
	// serializes as null, and response mutations need an independent time copy.
	LastUsedAt *time.Time `json:"last_used_at"`
	// GroupRates retains the account map from group IDs to rate multipliers.
	// Administrator DTO consumers must copy it before changing values or
	// retaining an independent mutable response.
	GroupRates map[int64]float64 `json:"group_rates,omitempty"`
}

// AuthResponse combines issued credentials with panel account data after
// authentication.
type AuthResponse[K any] struct {
	AccessToken  string   `json:"access_token"`            // signed access credential
	RefreshToken string   `json:"refresh_token,omitempty"` // optional rotating refresh credential
	ExpiresIn    int      `json:"expires_in,omitempty"`    // seconds
	TokenType    string   `json:"token_type"`              // Bearer authorization scheme
	User         *User[K] `json:"user"`                    // authenticated account response
}

// UserFromIdentityShallow maps account fields to panel JSON and returns nil
// for a nil user. Pointer fields and group slices remain shared; notification
// entries receive a new slice, while keys and subscriptions remain unset.
func UserFromIdentityShallow[K any](u *identity.User) *User[K] {
	if u == nil {
		return nil
	}
	return &User[K]{
		PreferredLocale:            u.PreferredLocale,
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		Role:                       u.Role,
		Balance:                    u.Balance,
		FrozenBalance:              u.FrozenBalance,
		Concurrency:                u.Concurrency,
		Status:                     u.Status,
		AllowedGroups:              u.AllowedGroups,
		DisabledPublicGroups:       u.DisabledPublicGroups,
		LastActiveAt:               u.LastActiveAt,
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
		BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
		BalanceNotifyExtraEmails:   NotifyEmailEntriesFromIdentity(u.BalanceNotifyExtraEmails),
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RPMLimit,
		APIKeyLimit:                u.APIKeyLimit,
		DeletedAt:                  u.DeletedAt,
	}
}

// UserFromIdentity maps account data, caller-supplied keys, and loaded
// subscriptions to panel JSON. Nil input returns nil; key data is retained
// directly, subscription elements are converted, and the shallow account
// fields retain their documented sharing.
func UserFromIdentity[K any](u *identity.User, keys []K) *User[K] {
	out := UserFromIdentityShallow[K](u)
	if out == nil {
		return nil
	}
	out.APIKeys = keys
	if len(u.Subscriptions) > 0 {
		out.Subscriptions = make([]billinghttpapi.UserSubscription, 0, len(u.Subscriptions))
		for i := range u.Subscriptions {
			out.Subscriptions = append(out.Subscriptions, *billinghttpapi.UserSubscriptionFromService(&u.Subscriptions[i]))
		}
	}
	return out
}

// AdminUserFromIdentity adds administrator notes, last use, and group rate
// data to the user response. Nil input returns nil; the group-rate map,
// recharge override and activity pointers remain shared with the supplied account.
func AdminUserFromIdentity[K any](u *identity.User, keys []K) *AdminUser[K] {
	base := UserFromIdentity(u, keys)
	if base == nil {
		return nil
	}
	return &AdminUser[K]{
		User:                      *base,
		Notes:                     u.Notes,
		LastUsedAt:                u.LastUsedAt,
		GroupRates:                u.GroupRates,
		BalanceRechargeMultiplier: u.BalanceRechargeMultiplier,
	}
}
