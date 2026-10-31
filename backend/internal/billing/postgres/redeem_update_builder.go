package postgres

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// redeem_update_builder.go builds complete redemption record updates from
// supplied snapshots.
// Management edits and registration compensation restore the redemption
// record's supplied fields as one update builder. Optional recipient, usage
// time, plan, issued duration and expiry pointers use set-or-clear operations.
// A nil snapshot value removes saved data instead of leaving a later value in place.

// redeemUpdateBuilder stages redemption snapshot replacement on the supplied
// client for a non-nil code. Nil optional pointers clear recipient, usage
// time, plan, issued duration or expiry; other fields are assigned from the
// snapshot. The caller owns authorization, consistency checks and Save/transaction
// completion.
func redeemUpdateBuilder(client *dbent.Client, code *billing.RedeemCode) *dbent.RedeemCodeUpdateOne {
	update := client.RedeemCode.UpdateOneID(code.ID).
		SetCode(code.Code).
		SetType(code.Type).
		SetValue(code.Value).
		SetStatus(code.Status).
		SetRequiresPayment(code.RequiresPayment).
		SetMaxUses(code.MaxUses).
		SetUsedCount(code.UsedCount).
		SetNotes(code.Notes)

	// Restore every optional snapshot field as set or clear, so compensation does
	// not retain recipient/time/plan/duration/expiry values absent from the supplied
	// record.
	if code.UsedBy != nil {
		update.SetUsedBy(*code.UsedBy)
	} else {
		update.ClearUsedBy()
	}
	if code.UsedAt != nil {
		update.SetUsedAt(*code.UsedAt)
	} else {
		update.ClearUsedAt()
	}
	if code.PlanID != nil {
		update.SetPlanID(*code.PlanID)
	} else {
		update.ClearPlanID()
	}
	if code.ValidityDays != nil {
		update.SetValidityDays(*code.ValidityDays)
	} else {
		update.ClearValidityDays()
	}
	if code.ExpiresAt != nil {
		update.SetExpiresAt(*code.ExpiresAt)
	} else {
		update.ClearExpiresAt()
	}

	return update
}
