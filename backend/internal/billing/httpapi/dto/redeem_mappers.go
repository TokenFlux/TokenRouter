package dto

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// redeem_mappers.go maps redemption records into user and administrator
// responses with different notes visibility.
// User redemption history needs visible adjustment reasons while internal
// notes remain available to administrators. The common mapping selects
// user-visible notes by record type, and the administrator wrapper supplies
// its separate full notes field from the same service record.

// RedeemCodeFromService maps a redemption record for user consumption and
// returns nil for nil input. Notes are included for nonempty administrator
// balance/concurrency and plugin balance adjustments. Date and ID pointers and the visible Notes
// pointer remain shared with the service record for read-only response use.
func RedeemCodeFromService(rc *billing.RedeemCode) *RedeemCode {
	if rc == nil {
		return nil
	}
	out := RedeemCodeFromServiceBase(rc)
	return &out
}

// RedeemCodeFromServiceAdmin maps a redemption record with its full notes for
// administrator consumption. Nil input returns nil; the common mapping retains
// date, ID and any visible adjustment-note pointers for read-only use, while
// the outer administrator Notes string is copied by value.
func RedeemCodeFromServiceAdmin(rc *billing.RedeemCode) *AdminRedeemCode {
	if rc == nil {
		return nil
	}
	return &AdminRedeemCode{
		RedeemCode: RedeemCodeFromServiceBase(rc),
		Notes:      rc.Notes,
	}
}

// RedeemCodeFromServiceBase builds the common response from a non-nil service
// record. It maps hydrated user data and retains optional date and ID pointers.
// Nonempty administrator and plugin adjustment reasons use a pointer to rc.Notes;
// callers keep those shared fields unchanged while consuming the response.
func RedeemCodeFromServiceBase(rc *billing.RedeemCode) RedeemCode {
	out := RedeemCode{
		RequiresPayment: rc.RequiresPayment,
		ID:              rc.ID,
		Code:            rc.Code,
		Type:            rc.Type,
		Value:           rc.Value,
		Status:          rc.Status,
		MaxUses:         rc.MaxUses,
		UsedCount:       rc.UsedCount,
		ExpiresAt:       rc.ExpiresAt,
		UsedBy:          rc.UsedBy,
		UsedAt:          rc.UsedAt,
		CreatedAt:       rc.CreatedAt,
		PlanID:          rc.PlanID,
		ValidityDays:    rc.ValidityDays,
		User:            UserSummaryFromBilling(rc.User),
	}

	// Administrator and plugin adjustment reasons refer to the Notes field so the
	// user response displays that stored reason. Other types leave user notes
	// absent while the administrator wrapper adds its own string value.
	if (rc.Type == billing.AdjustmentTypeAdminBalance || rc.Type == billing.AdjustmentTypeAdminConcurrency || rc.Type == billing.AdjustmentTypePluginBalance) && rc.Notes != "" {
		out.Notes = &rc.Notes
	}

	return out
}
