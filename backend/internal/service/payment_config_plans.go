package service

import (
	"context"
	"fmt"
	"strings"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/group"
	"github.com/TokenFlux/TokenRouter/ent/subscriptionplan"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

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

func validatePlanRequired(name string, groupIDs []int64, price float64, validityDays int, validityUnit string, originalPrice *float64) error {
	if strings.TrimSpace(name) == "" {
		return infraerrors.BadRequest("PLAN_NAME_REQUIRED", "plan name is required")
	}
	if len(normalizePlanGroupIDs(groupIDs)) == 0 {
		return infraerrors.BadRequest("PLAN_GROUP_REQUIRED", "at least one OpenAI group is required")
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
	if len(normalizePlanGroupIDs(req.GroupIDs)) == 0 {
		return infraerrors.BadRequest("PLAN_GROUP_REQUIRED", "at least one OpenAI group is required")
	}
	return validatePlanQuotaPatch(req)
}

func normalizePlanGroupIDs(groupIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(groupIDs))
	out := make([]int64, 0, len(groupIDs))
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

func (s *PaymentConfigService) validatePlanGroups(ctx context.Context, groupIDs []int64) ([]int64, error) {
	groupIDs = normalizePlanGroupIDs(groupIDs)
	if len(groupIDs) == 0 {
		return nil, infraerrors.BadRequest("PLAN_GROUP_REQUIRED", "at least one OpenAI group is required")
	}
	groups, err := s.entClient.Group.Query().
		Where(group.IDIn(groupIDs...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(groups) != len(groupIDs) {
		return nil, infraerrors.BadRequest("PLAN_GROUP_NOT_FOUND", "group not found")
	}
	for _, item := range groups {
		if !strings.EqualFold(item.Platform, PlatformOpenAI) {
			return nil, infraerrors.BadRequest("PLAN_GROUP_PLATFORM_INVALID", "subscription plan groups must be OpenAI groups")
		}
	}
	return groupIDs, nil
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
	return s.entClient.SubscriptionPlan.Query().WithGroup().WithGroups().Order(subscriptionplan.BySortOrder()).All(ctx)
}

func (s *PaymentConfigService) ListPlansForSale(ctx context.Context) ([]*dbent.SubscriptionPlan, error) {
	return s.entClient.SubscriptionPlan.Query().WithGroup().WithGroups().Where(subscriptionplan.ForSaleEQ(true)).Order(subscriptionplan.BySortOrder()).All(ctx)
}

func (s *PaymentConfigService) CreatePlan(ctx context.Context, req CreatePlanRequest) (*dbent.SubscriptionPlan, error) {
	if err := validatePlanRequired(req.Name, req.GroupIDs, req.Price, req.ValidityDays, req.ValidityUnit, req.OriginalPrice); err != nil {
		return nil, err
	}
	if err := validatePlanQuotas(req.DailyLimitUSD, req.WeeklyLimitUSD, req.MonthlyLimitUSD); err != nil {
		return nil, err
	}
	groupIDs, err := s.validatePlanGroups(ctx, req.GroupIDs)
	if err != nil {
		return nil, err
	}

	builder := s.entClient.SubscriptionPlan.Create().
		SetName(strings.TrimSpace(req.Name)).
		SetDescription(req.Description).
		SetPrice(req.Price).
		SetValidityDays(req.ValidityDays).
		SetValidityUnit(strings.TrimSpace(req.ValidityUnit)).
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
	plan, err := builder.AddGroupIDs(groupIDs...).Save(ctx)
	if err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, plan.ID)
}

func (s *PaymentConfigService) UpdatePlan(ctx context.Context, id int64, req UpdatePlanRequest) (*dbent.SubscriptionPlan, error) {
	if err := validatePlanPatch(req); err != nil {
		return nil, err
	}
	groupIDs, err := s.validatePlanGroups(ctx, req.GroupIDs)
	if err != nil {
		return nil, err
	}

	update := s.entClient.SubscriptionPlan.UpdateOneID(id)
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
	plan, err := update.ClearGroups().AddGroupIDs(groupIDs...).Save(ctx)
	if err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, plan.ID)
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
	plan, err := s.entClient.SubscriptionPlan.Query().WithGroup().WithGroups().Where(subscriptionplan.IDEQ(id)).Only(ctx)
	if err != nil {
		return nil, infraerrors.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
	}
	return plan, nil
}
