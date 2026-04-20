package service

import (
	"context"
	"fmt"
	"strings"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/subscriptionplan"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

var (
	ErrPlanQuotaRequired = infraerrors.BadRequest(
		"PLAN_QUOTA_REQUIRED",
		"subscription plan must define at least one positive quota limit",
	)
	ErrPlanQuotaInvalid = infraerrors.BadRequest(
		"PLAN_QUOTA_INVALID",
		"subscription plan quota limit must be greater than 0",
	)
)

func hasPositivePlanQuota(daily, weekly, monthly *float64) bool {
	return (daily != nil && *daily > 0) ||
		(weekly != nil && *weekly > 0) ||
		(monthly != nil && *monthly > 0)
}

func validatePlanQuotas(daily, weekly, monthly *float64) error {
	for _, limit := range []*float64{daily, weekly, monthly} {
		if limit != nil && *limit <= 0 {
			return ErrPlanQuotaInvalid
		}
	}
	if !hasPositivePlanQuota(daily, weekly, monthly) {
		return ErrPlanQuotaRequired
	}
	return nil
}

// validatePlanRequired checks that all required fields for a plan are provided.
func validatePlanRequired(name string, price float64, validityDays int, validityUnit string, originalPrice, daily, weekly, monthly *float64) error {
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
	return validatePlanQuotas(daily, weekly, monthly)
}

// validatePlanPatch validates only the non-nil fields in a patch update.
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
	return nil
}

// --- Plan CRUD ---

func (s *PaymentConfigService) ListPlans(ctx context.Context) ([]*dbent.SubscriptionPlan, error) {
	return s.entClient.SubscriptionPlan.Query().Order(subscriptionplan.BySortOrder()).All(ctx)
}

func (s *PaymentConfigService) ListPlansForSale(ctx context.Context) ([]*dbent.SubscriptionPlan, error) {
	return s.entClient.SubscriptionPlan.Query().Where(subscriptionplan.ForSaleEQ(true)).Order(subscriptionplan.BySortOrder()).All(ctx)
}

func (s *PaymentConfigService) CreatePlan(ctx context.Context, req CreatePlanRequest) (*dbent.SubscriptionPlan, error) {
	if err := validatePlanRequired(
		req.Name,
		req.Price,
		req.ValidityDays,
		req.ValidityUnit,
		req.OriginalPrice,
		req.DailyLimitUSD,
		req.WeeklyLimitUSD,
		req.MonthlyLimitUSD,
	); err != nil {
		return nil, err
	}
	b := s.entClient.SubscriptionPlan.Create().
		SetName(req.Name).SetDescription(req.Description).
		SetPrice(req.Price).SetValidityDays(req.ValidityDays).SetValidityUnit(req.ValidityUnit).
		SetNillableDailyLimitUsd(req.DailyLimitUSD).
		SetNillableWeeklyLimitUsd(req.WeeklyLimitUSD).
		SetNillableMonthlyLimitUsd(req.MonthlyLimitUSD).
		SetFeatures(req.Features).SetProductName(req.ProductName).
		SetForSale(req.ForSale).SetSortOrder(req.SortOrder)
	if req.OriginalPrice != nil {
		b.SetOriginalPrice(*req.OriginalPrice)
	}
	return b.Save(ctx)
}

// UpdatePlan updates a subscription plan by ID (patch semantics).
// NOTE: This function exceeds 30 lines due to per-field nil-check patch update boilerplate
// plus a validation guard for non-nil fields.
func (s *PaymentConfigService) UpdatePlan(ctx context.Context, id int64, req UpdatePlanRequest) (*dbent.SubscriptionPlan, error) {
	if err := validatePlanPatch(req); err != nil {
		return nil, err
	}
	existing, err := s.entClient.SubscriptionPlan.Get(ctx, id)
	if err != nil {
		return nil, infraerrors.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
	}

	finalDaily := existing.DailyLimitUsd
	if req.DailyLimitUSD != nil {
		finalDaily = req.DailyLimitUSD
	}
	finalWeekly := existing.WeeklyLimitUsd
	if req.WeeklyLimitUSD != nil {
		finalWeekly = req.WeeklyLimitUSD
	}
	finalMonthly := existing.MonthlyLimitUsd
	if req.MonthlyLimitUSD != nil {
		finalMonthly = req.MonthlyLimitUSD
	}
	if err := validatePlanQuotas(finalDaily, finalWeekly, finalMonthly); err != nil {
		return nil, err
	}

	u := s.entClient.SubscriptionPlan.UpdateOneID(id)
	if req.Name != nil {
		u.SetName(*req.Name)
	}
	if req.Description != nil {
		u.SetDescription(*req.Description)
	}
	if req.Price != nil {
		u.SetPrice(*req.Price)
	}
	if req.OriginalPrice != nil {
		u.SetOriginalPrice(*req.OriginalPrice)
	}
	if req.ValidityDays != nil {
		u.SetValidityDays(*req.ValidityDays)
	}
	if req.DailyLimitUSD != nil {
		u.SetDailyLimitUsd(*req.DailyLimitUSD)
	}
	if req.WeeklyLimitUSD != nil {
		u.SetWeeklyLimitUsd(*req.WeeklyLimitUSD)
	}
	if req.MonthlyLimitUSD != nil {
		u.SetMonthlyLimitUsd(*req.MonthlyLimitUSD)
	}
	if req.ValidityUnit != nil {
		u.SetValidityUnit(*req.ValidityUnit)
	}
	if req.Features != nil {
		u.SetFeatures(*req.Features)
	}
	if req.ProductName != nil {
		u.SetProductName(*req.ProductName)
	}
	if req.ForSale != nil {
		u.SetForSale(*req.ForSale)
	}
	if req.SortOrder != nil {
		u.SetSortOrder(*req.SortOrder)
	}
	return u.Save(ctx)
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

// GetPlan returns a subscription plan by ID.
func (s *PaymentConfigService) GetPlan(ctx context.Context, id int64) (*dbent.SubscriptionPlan, error) {
	plan, err := s.entClient.SubscriptionPlan.Get(ctx, id)
	if err != nil {
		return nil, infraerrors.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
	}
	return plan, nil
}
