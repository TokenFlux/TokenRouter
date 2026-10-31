package dto

import (
	"bytes"
	"encoding/json"
	"time"
)

// redeem_dto.go defines redemption responses and decodes presence-aware expiry
// updates.
// Redeem responses expose entitlement amounts and the most recent successful
// use, with ordinary users receiving reasons for administrator and plugin adjustments.
// Administrator responses add the full internal notes field. Batch expiry
// updates also need to distinguish an omitted value from clearing an expiry
// with JSON null.
//
// NullableTimeField retains field presence separately from the timestamp
// pointer. UnmarshalJSON marks presence before decoding and uses a nil pointer
// for explicit null; invalid timestamp input returns an error after that
// presence change. The handler decodes a fresh request and checks Set when
// building the domain update, so omitted fields leave saved expiry in place.

// RedeemCode is the user response for a redemption or account adjustment record.
type RedeemCode struct {
	// RequiresPayment records whether redemption needs qualifying paid-order
	// history. The service checks that condition inside the redemption
	// transaction before issuing benefits or recording use.
	RequiresPayment bool   `json:"requires_payment"` // paid-order eligibility requirement flag
	ID              int64  `json:"id"`               // local redemption record identifier
	Code            string `json:"code"`             // redeem credential text value
	// Type and Value identify the entitlement change together: balance uses a
	// monetary amount and concurrency uses an integer delta. Subscription grants
	// instead select their plan through PlanID.
	Type   string  `json:"type"`   // entitlement or adjustment type
	Value  float64 `json:"value"`  // stored entitlement adjustment amount
	Status string  `json:"status"` // provided redemption status value
	// MaxUses and UsedCount define lifetime redemption capacity for this code.
	// Zero MaxUses permits unlimited successes; UsedCount still records each
	// successful grant and determines the derived status.
	MaxUses   int        `json:"max_uses"`   // maximum successful redemption count
	UsedCount int        `json:"used_count"` // recorded successful redemption count
	ExpiresAt *time.Time `json:"expires_at"` // optional credential expiry timestamp
	// UsedBy and UsedAt identify the most recent successful grant recorded for
	// the code. Their optional pointers are retained from the service record by
	// response mapping.
	UsedBy    *int64     `json:"used_by"`    // latest successful recipient identifier
	UsedAt    *time.Time `json:"used_at"`    // latest successful redemption time
	CreatedAt time.Time  `json:"created_at"` // redemption record creation time

	// PlanID selects the plan for subscription entitlement codes. The response
	// mapper retains this optional pointer from the service record for read-only
	// consumption.
	PlanID *int64 `json:"plan_id"` // subscription grant plan identifier
	// ValidityDays exposes a frozen issued duration when present; omission keeps
	// the legacy contract in which redemption uses the current plan's duration.
	ValidityDays *int `json:"validity_days,omitempty"`

	// Notes references the service record's reason for nonempty administrator
	// balance/concurrency adjustments and plugin balance adjustments. Callers keep this shared string
	// unchanged; other types omit internal notes from the user response.
	Notes *string `json:"notes,omitempty"` // visible account adjustment reason

	User *UserSummaryResponse `json:"user,omitempty"` // hydrated latest recipient details
}

// AdminRedeemCode adds unrestricted administrator notes to the user redemption
// response fields.
type AdminRedeemCode struct {
	RedeemCode // common user redemption response

	Notes string `json:"notes"` // full administrator notes text
}

// NullableTimeField represents JSON expiry presence separately from its
// optional timestamp value.
type NullableTimeField struct {
	// Set and Value distinguish omission, explicit null and a timestamp in a
	// fresh or reset field. After successful decoding, Set true with nil Value
	// requests an expiry clear; omission retains the field's prior state.
	Set   bool       // expiry field was supplied
	Value *time.Time // decoded optional expiry timestamp
}

// BatchUpdateRedeemCodeFields contains supplied batch changes and core-field
// attempts for service validation.
type BatchUpdateRedeemCodeFields struct {
	Status *string `json:"status,omitempty"` // optional status replacement value
	// ExpiresAt carries field presence to the batch handler. Set false preserves
	// the stored expiry, while Set true and nil Value requests clearing it.
	ExpiresAt NullableTimeField `json:"expires_at,omitempty"` // supplied expiry field update
	Notes     *string           `json:"notes,omitempty"`      // optional notes replacement text

	// Type, Value, MaxUses and PlanID retain attempted entitlement changes for
	// validation. RedeemService rejects these batch core changes and requires the
	// single-code consistency checks.
	Type    *string  `json:"type,omitempty"`     // attempted entitlement type change
	Value   *float64 `json:"value,omitempty"`    // attempted entitlement amount change
	MaxUses *int     `json:"max_uses,omitempty"` // attempted maximum redemption change
	PlanID  *int64   `json:"plan_id,omitempty"`  // attempted subscription plan change
}

// BatchUpdateRedeemCodesRequest supplies target code IDs and their requested
// common field changes.
type BatchUpdateRedeemCodesRequest struct {
	IDs    []int64                     `json:"ids" binding:"required,min=1"` // requested target code identifiers
	Fields BatchUpdateRedeemCodeFields `json:"fields" binding:"required"`    // common requested field changes
}

// UnmarshalJSON records a supplied expiry field and decodes a timestamp or
// null. Whitespace around null is accepted, and null clears Value while Set
// remains true, allowing the handler to pass an intentional clear to the
// domain update.
//
// Omission never calls this method and retains prior field state. Callers
// decode into a fresh or reset field when omission must leave Set false. Set
// becomes true before timestamp parsing, and a previous Value can remain after
// a parsing error; callers discard the failed request before applying changes.
func (f *NullableTimeField) UnmarshalJSON(data []byte) error {
	// Record presence before examining the payload so explicit null survives
	// decoding as an intentional clear rather than an omitted expiry.
	f.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		f.Value = nil
		return nil
	}
	var value time.Time
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	f.Value = &value
	return nil
}
