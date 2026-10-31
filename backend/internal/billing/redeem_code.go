package billing

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// redeem_code.go defines redemption credentials and derives their capacity and
// effective status.
// Redeem credentials can be used once, a fixed number of times or without a
// lifetime use cap. The latest recipient fields summarize one successful
// grant, while usage records retain each recipient/time for history and
// per-user checks. Capacity predicates inspect counts independently of
// administrative flags and natural expiry.
//
// PersistedStatus retains disabled or explicitly expired labels before
// deriving used/active/unused from capacity. EffectiveStatus adds wall-clock
// expiry while keeping disabled display precedence. Admission callers first
// establish a loaded code: the zero/nil status default is unused, and CanUse
// consequently returns true for a nil receiver even though the
// capacity/deletion predicates reject absence.

const (
	// Credential type labels select balance, concurrency or subscription grants,
	// while invitation labels route consumption through registration rather than
	// the direct redemption endpoint.
	RedeemTypeBalance      = "balance"      // balance entitlement credential
	RedeemTypeConcurrency  = "concurrency"  // concurrency entitlement credential
	RedeemTypeSubscription = "subscription" // subscription entitlement credential
	RedeemTypeInvitation   = "invitation"   // registration invitation credential

	// Adjustment type labels identify plugin balance and administrator balance or concurrency
	// changes in the history records, allowing user responses to expose their
	// recorded reasons.
	AdjustmentTypePluginBalance    = "plugin_balance"    // Plugin balance adjustment.
	AdjustmentTypeAdminBalance     = "admin_balance"     // Administrator balance adjustment.
	AdjustmentTypeAdminConcurrency = "admin_concurrency" // Administrator concurrency adjustment.

	// Redemption status helpers interpret these labels as administrative state or
	// derived lifetime capacity, with effective expiry checked separately against
	// wall-clock time.
	StatusUnused   = "unused"   // code has no recorded uses
	StatusUsed     = "used"     // code finite capacity is exhausted
	StatusExpired  = "expired"  // explicit credential expiry label
	StatusDisabled = "disabled" // credential administratively disabled
	StatusActive   = "active"   // active text; codes retain use capacity
)

// RedeemCode represents a grant credential or adjustment record with
// usage-capacity and expiry state.
type RedeemCode struct {
	ID   int64  // local redemption record identifier
	Code string // exact redeem credential text
	// Type and Value determine the grant amount representation: balance uses
	// money and concurrency uses an integer delta. Subscription grants select
	// PlanID and an optional fixed ValidityDays override instead of consuming Value.
	Type   string  // entitlement or adjustment type
	Value  float64 // entitlement change amount
	Status string  // supplied redemption status label
	// RequiresPayment asks the redemption service to check qualifying paid-order
	// history before any grant/use write. Invitation creation rejects this
	// requirement for registration credentials.
	RequiresPayment bool // paid-order eligibility requirement flag
	// MaxUses and UsedCount govern lifetime capacity together. Zero MaxUses is
	// unlimited, a negative value is interpreted as one, and UsedCount records
	// successful grants regardless of capacity.
	MaxUses   int        // zero permits unlimited lifetime uses
	UsedCount int        // recorded successful grant count
	ExpiresAt *time.Time // optional credential expiry timestamp
	// UsedBy and UsedAt summarize the latest successful grant. Individual history
	// uses RedeemCodeUsage recipient/time values, so consumers must select the
	// appropriate observation for that view.
	UsedBy    *int64     // latest successful recipient identifier
	UsedAt    *time.Time // latest successful grant timestamp
	Notes     string     // internal grant or adjustment notes
	CreatedAt time.Time  // redemption record creation timestamp

	PlanID *int64 // optional subscription plan identifier
	// ValidityDays fixes the subscription duration when present; nil uses the
	// current plan at redemption. Credential expiration remains governed by ExpiresAt.
	ValidityDays *int

	// User, Plan and UsageRecords carry optional hydrated edges from the owning
	// reader. Their absence does not replace the scalar recipient, plan or
	// usage-count identities in this record.
	User         *UserSummary      // hydrated latest recipient snapshot
	Plan         *SubscriptionPlan // hydrated subscription plan snapshot
	UsageRecords []RedeemCodeUsage // loaded individual redemption observations
}

// RedeemCodeUsage records one recipient and use time for a redemption
// credential.
type RedeemCodeUsage struct {
	ID           int64     // local usage record identifier
	RedeemCodeID int64     // consumed credential record identifier
	UserID       int64     // individual grant recipient identifier
	UsedAt       time.Time // individual grant consumption timestamp

	RedeemCode *RedeemCode  // hydrated credential record snapshot
	User       *UserSummary // hydrated individual recipient snapshot
}

// MaxUsesOrDefault returns stored capacity, retaining zero as unlimited. A nil
// receiver or negative stored capacity selects one; this default does not
// validate status, expiry or per-user redemption history.
func (r *RedeemCode) MaxUsesOrDefault() int {
	if r == nil || r.MaxUses < 0 {
		return 1
	}
	return r.MaxUses
}

// HasUnlimitedUses reports whether a present code has zero lifetime use
// capacity.
func (r *RedeemCode) HasUnlimitedUses() bool {
	return r != nil && r.MaxUsesOrDefault() == 0
}

// HasBeenUsed reports whether a present code records positive completed-use
// count.
func (r *RedeemCode) HasBeenUsed() bool {
	return r != nil && r.UsedCount > 0
}

// HasRemainingUses reports capacity remaining for a present code, including
// unlimited zero capacity. Nil returns false; this calculation uses counts
// alone, so disabled or expired credentials can still have remaining capacity.
func (r *RedeemCode) HasRemainingUses() bool {
	if r == nil {
		return false
	}
	if r.HasUnlimitedUses() {
		return true
	}
	return r.UsedCount < r.MaxUsesOrDefault()
}

// IsExhausted reports consumed finite capacity, with nil and unlimited codes
// returning false. It derives the effective capacity through MaxUsesOrDefault
// and does not decide whether the code's status or expiry permits redemption.
func (r *RedeemCode) IsExhausted() bool {
	return r != nil && !r.HasUnlimitedUses() && !r.HasRemainingUses()
}

// IsNaturallyExpired reports whether wall-clock time has reached the stored
// expiry. Nil code or expiry returns false; equality counts as expired, and
// this observation is independent of the stored status label.
func (r *RedeemCode) IsNaturallyExpired() bool {
	if r == nil || r.ExpiresAt == nil {
		return false
	}
	return !time.Now().Before(*r.ExpiresAt)
}

// IsExpired reports an explicit expired status or a reached wall-clock expiry.
// Nil returns false; a disabled credential can still be expired by this
// predicate even though EffectiveStatus prioritizes its disabled label.
func (r *RedeemCode) IsExpired() bool {
	if r == nil {
		return false
	}
	return r.Status == StatusExpired || r.IsNaturallyExpired()
}

// PersistedStatus derives the stored redemption label from administrative
// flags and usage capacity. Nil selects unused; disabled and explicit expired
// states retain precedence over count-derived results. It does not consult the
// current expiry timestamp.
//
// An exhausted finite capacity selects used, otherwise a positive count
// selects active and no count selects unused. Thus a multi-use code can remain
// active after a grant, while a natural time expiry is evaluated separately by
// EffectiveStatus before this count-derived label is used.
func (r *RedeemCode) PersistedStatus() string {
	switch {
	case r == nil:
		return StatusUnused
	case r.Status == StatusDisabled:
		return StatusDisabled
	case r.Status == StatusExpired:
		return StatusExpired
	case r.IsExhausted():
		return StatusUsed
	case r.HasBeenUsed():
		return StatusActive
	default:
		return StatusUnused
	}
}

// EffectiveStatus returns the currently displayed redemption label, selecting
// unused for nil. Disabled wins first; explicit or wall-clock expiry then
// selects expired, and remaining cases use PersistedStatus's capacity-derived
// label.
//
// The separate IsExpired predicate can still report true for an expired
// disabled code, because it answers the time/status-expiry question rather
// than display precedence. This method uses the current wall clock through
// IsNaturallyExpired, so callers invoking status helpers separately can
// observe different instants at an expiry edge.
func (r *RedeemCode) EffectiveStatus() string {
	if r == nil {
		return StatusUnused
	}
	if r.Status == StatusDisabled {
		return StatusDisabled
	}
	if r.IsExpired() {
		return StatusExpired
	}
	return r.PersistedStatus()
}

// IsUsed reports whether current display status denotes exhausted finite use
// capacity.
func (r *RedeemCode) IsUsed() bool {
	return r.EffectiveStatus() == StatusUsed
}

// CanUse reports whether the effective label is unused or active. Nil inherits
// EffectiveStatus's unused label and therefore returns true; callers establish
// a non-nil loaded record before using this predicate for admission.
func (r *RedeemCode) CanUse() bool {
	switch r.EffectiveStatus() {
	case StatusUnused, StatusActive:
		return true
	default:
		return false
	}
}

// CanDelete reports whether a present code has no recorded successful uses.
func (r *RedeemCode) CanDelete() bool {
	return r != nil && r.UsedCount == 0
}

// GenerateRedeemCode returns sixteen random bytes encoded as thirty-two
// lowercase hexadecimal characters with nil error. crypto/rand.Read terminates
// the process when io.ReadFull reports an error. Callers persist a returned
// credential and handle uniqueness conflicts at creation.
func GenerateRedeemCode() (string, error) {
	// Sixteen entropy bytes become thirty-two hexadecimal credential characters.
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
