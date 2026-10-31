package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// plugin_redeem.go binds plugin issuance to the existing plan and redeem-code
// services. Plan durations use the same unit conversion and normalization as
// ordinary subscription assignment. Each plugin subscription credential stores
// that accepted duration, while future redemption still uses the plan's current
// quota template and the ordinary billing transaction and cache behavior.
//
// Code creation requires the ledger's Ent transaction. This adapter fixes one
// allowed use, leaves payment eligibility disabled, and supplies the deterministic
// credential and source notes already authorized by the core capability. Issuance
// itself credits no account; balance recharge accounting and applicable affiliate
// effects remain deferred to the normal redemption workflow selected by billing.

// pluginRedeemEffects adapts native plan lookup and transaction-bound credential creation.
type pluginRedeemEffects struct {
	plans  *billing.Plans         // existing live subscription plan reader
	redeem *billing.RedeemService // existing deterministic credential creation service
}

// providePluginRedeemCodes binds deterministic issuance to shared billing and ledger owners.
func providePluginRedeemCodes(writes *plugin.Writes, grants *plugin.Grants, plans *billing.Plans, redeem *billing.RedeemService) *plugin.RedeemCodes {
	return plugin.NewRedeemCodes(writes, grants, &pluginRedeemEffects{plans: plans, redeem: redeem}, time.Local)
}

// SubscriptionDays resolves plan defaults or a positive override using native billing rules.
// The ledger context keeps the read on its transaction connection. Missing plans
// use the plugin's stable allowlist error instead of exposing a persistence diagnostic.
func (a *pluginRedeemEffects) SubscriptionDays(ctx context.Context, planID int64, requested int32) (int32, error) {
	plan, err := a.plans.GetPlan(ctx, planID)
	if err != nil {
		if apperror.FromError(err).Reason == "PLAN_NOT_FOUND" {
			return 0, plugin.ErrPlanNotAllowed
		}
		return 0, err
	}
	days := int(requested)
	if requested == 0 {
		days = billing.ComputeValidityDays(plan.ValidityDays, plan.ValidityUnit)
	}
	return int32(billing.NormalizeAssignValidityDays(days)), nil
}

// CreateCode stores one credential and its frozen subscription duration on the ledger transaction.
// Absence of that transaction fails before creation. Ordinary billing redemption
// remains responsible for granting benefits, tracking use, and performing any rebate.
func (a *pluginRedeemEffects) CreateCode(ctx context.Context, input plugin.RedeemCredential) error {
	if ent.TxFromContext(ctx) == nil {
		return errors.New("plugin redeem issuance requires its ledger transaction")
	}
	value, err := strconv.ParseFloat(input.Value, 64)
	if err != nil {
		return err
	}
	code := &billing.RedeemCode{
		Code:            input.Code,
		Type:            input.Type,
		Value:           value,
		Status:          billing.StatusUnused,
		MaxUses:         1,
		RequiresPayment: false,
		ExpiresAt:       input.ExpiresAt,
		Notes:           fmt.Sprintf("[plugin:%s] %s", input.PluginID, input.Reason),
	}
	if input.Type == billing.RedeemTypeSubscription {
		days := int(input.Days)
		code.PlanID = &input.PlanID
		code.ValidityDays = &days
	}
	return a.redeem.CreateCode(ctx, code)
}
