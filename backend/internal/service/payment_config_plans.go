package service

import (
	"context"
	"fmt"
	"strings"

	dbent "github.com/TokenFlux/TokenRouter/ent"
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
	if req.OriginalPrice != nil && *req.OriginalPrice < 0 {
		return infraerrors.BadRequest("PLAN_ORIGINAL_PRICE_INVALID", "original price must be >= 0")
	}
	return validatePlanQuotaPatch(req)
}

func validatePlanQuotaPatch(req UpdatePlanRequest) error {
	return validatePlanQuotas(req.DailyLimitUSD, req.WeeklyLimitUSD, req.MonthlyLimitUSD)
}

func (s *PaymentConfigService) ListPlans(ctx context.Context) ([]*dbent.SubscriptionPlan, error) {
	return s.entClient.SubscriptionPlan.Query().Order(subscriptionplan.BySortOrder()).All(ctx)
}

func (s *PaymentConfigService) ListPlansForSale(ctx context.Context) ([]*dbent.SubscriptionPlan, error) {
	return s.entClient.SubscriptionPlan.Query().Where(subscriptionplan.ForSaleEQ(true)).Order(subscriptionplan.BySortOrder()).All(ctx)
}

func (s *PaymentConfigService) CreatePlan(ctx context.Context, req CreatePlanRequest) (*dbent.SubscriptionPlan, error) {
	if err := validatePlanRequired(req.Name, req.GroupID, req.Price, req.ValidityDays, req.ValidityUnit, req.OriginalPrice); err != nil {
		return nil, err
	}
	if err := validatePlanQuotaPatch(UpdatePlanRequest{
		DailyLimitUSD:   req.DailyLimitUSD,
		WeeklyLimitUSD:  req.WeeklyLimitUSD,
		MonthlyLimitUSD: req.MonthlyLimitUSD,
	}); err != nil {
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
	return builder.Save(ctx)
}

func (s *PaymentConfigService) UpdatePlan(ctx context.Context, id int64, req UpdatePlanRequest) (*dbent.SubscriptionPlan, error) {
	if err := validatePlanPatch(req); err != nil {
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
	if req.OriginalPrice != nil {
		update.SetOriginalPrice(*req.OriginalPrice)
	}
	if req.ValidityDays != nil {
		update.SetValidityDays(*req.ValidityDays)
	}
	if req.ValidityUnit != nil {
		update.SetValidityUnit(strings.TrimSpace(*req.ValidityUnit))
	}
	if req.DailyLimitUSD != nil {
		update.SetDailyLimitUsd(*req.DailyLimitUSD)
	}
	if req.WeeklyLimitUSD != nil {
		update.SetWeeklyLimitUsd(*req.WeeklyLimitUSD)
	}
	if req.MonthlyLimitUSD != nil {
		update.SetMonthlyLimitUsd(*req.MonthlyLimitUSD)
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
	return update.Save(ctx)
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
