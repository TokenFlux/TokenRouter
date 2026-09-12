// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

// redeemUpdateBuilder 是兑换快照字段的唯一赋值实现，注册补偿和普通管理复用。
func redeemUpdateBuilder(client *dbent.Client, code *billing.RedeemCode) *dbent.RedeemCodeUpdateOne {
	update := client.RedeemCode.UpdateOneID(code.ID).
		SetCode(code.Code).
		SetType(code.Type).
		SetValue(code.Value).
		SetStatus(code.Status).
		SetMaxUses(code.MaxUses).
		SetUsedCount(code.UsedCount).
		SetNotes(code.Notes)

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
	if code.ExpiresAt != nil {
		update.SetExpiresAt(*code.ExpiresAt)
	} else {
		update.ClearExpiresAt()
	}

	return update
}
