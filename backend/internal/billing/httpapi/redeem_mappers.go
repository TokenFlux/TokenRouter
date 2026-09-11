// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package httpapi

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

func RedeemCodeFromService(rc *billing.RedeemCode) *RedeemCode {
	if rc == nil {
		return nil
	}
	out := redeemCodeFromServiceBase(rc)
	return &out
}

// RedeemCodeFromServiceAdmin converts a service RedeemCode to DTO for admin users.
// It includes notes - user-facing endpoints must not use this.
func RedeemCodeFromServiceAdmin(rc *billing.RedeemCode) *AdminRedeemCode {
	if rc == nil {
		return nil
	}
	return &AdminRedeemCode{
		RedeemCode: redeemCodeFromServiceBase(rc),
		Notes:      rc.Notes,
	}
}

func redeemCodeFromServiceBase(rc *billing.RedeemCode) RedeemCode {
	out := RedeemCode{
		ID:        rc.ID,
		Code:      rc.Code,
		Type:      rc.Type,
		Value:     rc.Value,
		Status:    rc.Status,
		MaxUses:   rc.MaxUses,
		UsedCount: rc.UsedCount,
		ExpiresAt: rc.ExpiresAt,
		UsedBy:    rc.UsedBy,
		UsedAt:    rc.UsedAt,
		CreatedAt: rc.CreatedAt,
		PlanID:    rc.PlanID,
		User:      userSummaryResponse(rc.User),
	}

	// For admin_balance/admin_concurrency types, include notes so users can see
	// why they were charged or credited by admin
	if (rc.Type == "admin_balance" || rc.Type == "admin_concurrency") && rc.Notes != "" {
		out.Notes = &rc.Notes
	}

	return out
}
