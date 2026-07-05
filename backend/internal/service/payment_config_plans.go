package service

import (
	"context"
	"fmt"
	"strings"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/subscriptionplan"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

func normalizePlanGroupIDs(groupID int64, groupIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(groupIDs)+1)
	out := make([]int64, 0, len(groupIDs)+1)
	if groupID > 0 {
		seen[groupID] = struct{}{}
		out = append(out, groupID)
	}
	for _, id := range groupIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func validatePlanGroupIDs(groupIDs []int64) error {
	return nil
}

func normalizePlanGroupRateMultipliers(groupIDs []int64, rates map[int64]float64) (map[int64]float64, error) {
	selected := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		selected[groupID] = struct{}{}
	}

	out := make(map[int64]float64, len(selected))
	for groupID, rate := range rates {
		if groupID <= 0 {
			continue
		}
		if _, ok := selected[groupID]; !ok {
			continue
		}
		if rate <= 0 {
			return nil, infraerrors.BadRequest("PLAN_GROUP_RATE_INVALID", "plan group rate multiplier must be > 0")
		}
		out[groupID] = rate
	}
	return out, nil
}

func cloneInt64Float64Map(in map[int64]float64) map[int64]float64 {
	if len(in) == 0 {
		return map[int64]float64{}
	}
	out := make(map[int64]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func validatePlanQuotas(daily, weekly, monthly *float64) error {
	for _, item := range []struct {
		value *float64
		code  string
		label string
	}{
		{value: daily, code: "PLAN_DAILY_LIMIT_INVALID", label: "daily limit"},
		{value: weekly, code: "PLAN_WEEKLY_LIMIT_INVALID", label: "weekly limit"},
		{value: monthly, code: "PLAN_MONTHLY_LIMIT_INVALID", label: "monthly limit"},
	} {
		if item.value != nil && *item.value < 0 {
			return infraerrors.BadRequest(item.code, item.label+" must be >= 0")
		}
	}
	return nil
}

func validatePlanRequired(name string, _ int64, price float64, validityDays int, validityUnit string, originalPrice *float64) error {
	if strings.TrimSpace(name) == "" {
		return infraerrors.BadRequest("PLAN_NAME_REQUIRED", "plan name is required")
	}
	if price <= 0 {
		return infraerrors.BadRequest("PLAN_PRICE_INVALID", "price must be > 0")
	}
	if validityDays <= 0 {
		return infraerrors.BadRequest("PLAN_VALIDITY_REQUIRED", "validity days must be > 0")
	}
	if strings.TrimSpace(validityUnit) == "" {
		return infraerrors.BadRequest("PLAN_VALIDITY_UNIT_REQUIRED", "validity unit is required")
	}
	if originalPrice != nil && *originalPrice < 0 {
		return infraerrors.BadRequest("PLAN_ORIGINAL_PRICE_INVALID", "original price must be >= 0")
	}
	return nil
}

func validatePlanPatch(req UpdatePlanRequest) error {
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return infraerrors.BadRequest("PLAN_NAME_REQUIRED", "plan name is required")
	}
	if req.Price != nil && *req.Price <= 0 {
		return infraerrors.BadRequest("PLAN_PRICE_INVALID", "price must be > 0")
	}
	if req.ValidityDays != nil && *req.ValidityDays <= 0 {
		return infraerrors.BadRequest("PLAN_VALIDITY_REQUIRED", "validity days must be > 0")
	}
	if req.ValidityUnit != nil && strings.TrimSpace(*req.ValidityUnit) == "" {
		return infraerrors.BadRequest("PLAN_VALIDITY_UNIT_REQUIRED", "validity unit is required")
	}
	if req.OriginalPrice.present && req.OriginalPrice.value != nil && *req.OriginalPrice.value < 0 {
		return infraerrors.BadRequest("PLAN_ORIGINAL_PRICE_INVALID", "original price must be >= 0")
	}
	return validatePlanQuotaPatch(req)
}

func validatePlanQuotaPatch(req UpdatePlanRequest) error {
	for _, item := range []struct {
		field nullableFloat64Patch
		code  string
		label string
	}{
		{field: req.DailyLimitUSD, code: "PLAN_DAILY_LIMIT_INVALID", label: "daily limit"},
		{field: req.WeeklyLimitUSD, code: "PLAN_WEEKLY_LIMIT_INVALID", label: "weekly limit"},
		{field: req.MonthlyLimitUSD, code: "PLAN_MONTHLY_LIMIT_INVALID", label: "monthly limit"},
	} {
		if item.field.present && item.field.value != nil && *item.field.value < 0 {
			return infraerrors.BadRequest(item.code, item.label+" must be >= 0")
		}
	}
	return nil
}

func (s *PaymentConfigService) ListPlans(ctx context.Context) ([]*dbent.SubscriptionPlan, error) {
	return s.entClient.SubscriptionPlan.Query().Order(subscriptionplan.BySortOrder()).All(ctx)
}

func (s *PaymentConfigService) ListPlansForSale(ctx context.Context) ([]*dbent.SubscriptionPlan, error) {
	return s.entClient.SubscriptionPlan.Query().Where(subscriptionplan.ForSaleEQ(true)).Order(subscriptionplan.BySortOrder()).All(ctx)
}

func (s *PaymentConfigService) CreatePlan(ctx context.Context, req CreatePlanRequest) (*dbent.SubscriptionPlan, error) {
	groupIDs := normalizePlanGroupIDs(req.GroupID, req.GroupIDs)
	if err := validatePlanGroupIDs(groupIDs); err != nil {
		return nil, err
	}
	groupRates, err := normalizePlanGroupRateMultipliers(groupIDs, req.GroupRateMultipliers)
	if err != nil {
		return nil, err
	}
	if err := validatePlanRequired(req.Name, req.GroupID, req.Price, req.ValidityDays, req.ValidityUnit, req.OriginalPrice); err != nil {
		return nil, err
	}
	if err := validatePlanQuotas(req.DailyLimitUSD, req.WeeklyLimitUSD, req.MonthlyLimitUSD); err != nil {
		return nil, err
	}

	builder := s.entClient.SubscriptionPlan.Create().
		SetName(strings.TrimSpace(req.Name)).
		SetDescription(req.Description).
		SetPrice(req.Price).
		SetValidityDays(req.ValidityDays).
		SetValidityUnit(strings.TrimSpace(req.ValidityUnit)).
		SetGroupIds(groupIDs).
		SetGroupRateMultipliers(groupRates).
		SetFeatures(req.Features).
		SetProductName(req.ProductName).
		SetForSale(req.ForSale).
		SetSortOrder(req.SortOrder)
	if req.OriginalPrice != nil {
		builder.SetOriginalPrice(*req.OriginalPrice)
	}
	if req.DailyLimitUSD != nil {
		builder.SetDailyLimitUsd(*req.DailyLimitUSD)
	}
	if req.WeeklyLimitUSD != nil {
		builder.SetWeeklyLimitUsd(*req.WeeklyLimitUSD)
	}
	if req.MonthlyLimitUSD != nil {
		builder.SetMonthlyLimitUsd(*req.MonthlyLimitUSD)
	}
	plan, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.syncPlanGroupMappings(ctx, int64(plan.ID), groupIDs, groupRates); err != nil {
		return nil, err
	}
	return plan, nil
}

func (s *PaymentConfigService) UpdatePlan(ctx context.Context, id int64, req UpdatePlanRequest) (*dbent.SubscriptionPlan, error) {
	if err := validatePlanPatch(req); err != nil {
		return nil, err
	}
	var groupIDs []int64
	var groupRates map[int64]float64
	if req.GroupIDs != nil {
		groupID := int64(0)
		if req.GroupID != nil {
			groupID = *req.GroupID
		}
		groupIDs = normalizePlanGroupIDs(groupID, *req.GroupIDs)
		if err := validatePlanGroupIDs(groupIDs); err != nil {
			return nil, err
		}
	}
	if req.GroupIDs != nil || req.GroupRateMultipliers != nil {
		existing, err := s.entClient.SubscriptionPlan.Get(ctx, id)
		if err != nil {
			return nil, infraerrors.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
		}
		if req.GroupIDs == nil {
			groupIDs = append([]int64(nil), existing.GroupIds...)
		}
		rates := existing.GroupRateMultipliers
		if req.GroupRateMultipliers != nil {
			rates = *req.GroupRateMultipliers
		}
		groupRates, err = normalizePlanGroupRateMultipliers(groupIDs, rates)
		if err != nil {
			return nil, err
		}
	}

	update := s.entClient.SubscriptionPlan.UpdateOneID(id)
	if req.GroupIDs != nil {
		update.SetGroupIds(groupIDs)
	}
	if req.GroupIDs != nil || req.GroupRateMultipliers != nil {
		update.SetGroupRateMultipliers(groupRates)
	}
	if req.Name != nil {
		update.SetName(strings.TrimSpace(*req.Name))
	}
	if req.Description != nil {
		update.SetDescription(*req.Description)
	}
	if req.Price != nil {
		update.SetPrice(*req.Price)
	}
	if req.OriginalPrice.present {
		if req.OriginalPrice.value == nil {
			update.ClearOriginalPrice()
		} else {
			update.SetOriginalPrice(*req.OriginalPrice.value)
		}
	}
	if req.ValidityDays != nil {
		update.SetValidityDays(*req.ValidityDays)
	}
	if req.ValidityUnit != nil {
		update.SetValidityUnit(strings.TrimSpace(*req.ValidityUnit))
	}
	if req.DailyLimitUSD.present {
		if req.DailyLimitUSD.value == nil {
			update.ClearDailyLimitUsd()
		} else {
			update.SetDailyLimitUsd(*req.DailyLimitUSD.value)
		}
	}
	if req.WeeklyLimitUSD.present {
		if req.WeeklyLimitUSD.value == nil {
			update.ClearWeeklyLimitUsd()
		} else {
			update.SetWeeklyLimitUsd(*req.WeeklyLimitUSD.value)
		}
	}
	if req.MonthlyLimitUSD.present {
		if req.MonthlyLimitUSD.value == nil {
			update.ClearMonthlyLimitUsd()
		} else {
			update.SetMonthlyLimitUsd(*req.MonthlyLimitUSD.value)
		}
	}
	if req.Features != nil {
		update.SetFeatures(*req.Features)
	}
	if req.ProductName != nil {
		update.SetProductName(*req.ProductName)
	}
	if req.ForSale != nil {
		update.SetForSale(*req.ForSale)
	}
	if req.SortOrder != nil {
		update.SetSortOrder(*req.SortOrder)
	}
	plan, err := update.Save(ctx)
	if err != nil {
		return nil, err
	}
	if req.GroupIDs != nil || req.GroupRateMultipliers != nil {
		if err := s.syncPlanGroupMappings(ctx, id, groupIDs, groupRates); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func (s *PaymentConfigService) syncPlanGroupMappings(ctx context.Context, planID int64, groupIDs []int64, rates map[int64]float64) error {
	if s == nil || s.entClient == nil || planID <= 0 {
		return nil
	}
	if _, err := s.entClient.ExecContext(ctx, `DELETE FROM subscription_plan_groups WHERE plan_id = $1`, planID); err != nil {
		return err
	}
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		var rate any
		if value, ok := rates[groupID]; ok && value > 0 {
			rate = value
		}
		if _, err := s.entClient.ExecContext(ctx, `
			INSERT INTO subscription_plan_groups (plan_id, group_id, rate_multiplier)
			VALUES ($1, $2, $3)
			ON CONFLICT (plan_id, group_id)
			DO UPDATE SET rate_multiplier = EXCLUDED.rate_multiplier
		`, planID, groupID, rate); err != nil {
			return err
		}
	}
	return nil
}

func (s *PaymentConfigService) DeletePlan(ctx context.Context, id int64) error {
	count, err := s.countPendingOrdersByPlan(ctx, id)
	if err != nil {
		return fmt.Errorf("check pending orders: %w", err)
	}
	if count > 0 {
		return infraerrors.Conflict("PENDING_ORDERS",
			fmt.Sprintf("this plan has %d in-progress orders and cannot be deleted — wait for orders to complete first", count))
	}
	return s.entClient.SubscriptionPlan.DeleteOneID(id).Exec(ctx)
}

func (s *PaymentConfigService) GetPlan(ctx context.Context, id int64) (*dbent.SubscriptionPlan, error) {
	plan, err := s.entClient.SubscriptionPlan.Get(ctx, id)
	if err != nil {
		return nil, infraerrors.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
	}
	return plan, nil
}
