// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package postgres

import (
	context "context"
	sql "database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	subscriptionplan "github.com/TokenFlux/TokenRouter/ent/subscriptionplan"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	apperror "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	strings "strings"
)

// PlanStore 拥有套餐与分组映射的原子存储，不读取支付状态或配置。
type PlanStore struct{ entClient *dbent.Client }

func NewPlanStore(client *dbent.Client) *PlanStore { return &PlanStore{entClient: client} }

func (s *PlanStore) ListPlans(ctx context.Context) ([]*billing.SubscriptionPlan, error) {
	models, err := s.entClient.SubscriptionPlan.Query().Order(subscriptionplan.BySortOrder()).All(ctx)

	if err != nil {
		return nil, err
	}
	out := make([]*billing.SubscriptionPlan, len(models))
	for i, m := range models {
		out[i] = PlanFromEntity(m)
	}
	return out, nil
}

func (s *PlanStore) ListPlansForSale(ctx context.Context) ([]*billing.SubscriptionPlan, error) {
	models, err := s.entClient.SubscriptionPlan.Query().Where(subscriptionplan.ForSaleEQ(true)).Order(subscriptionplan.BySortOrder()).All(ctx)

	if err != nil {
		return nil, err
	}
	out := make([]*billing.SubscriptionPlan, len(models))
	for i, m := range models {
		out[i] = PlanFromEntity(m)
	}
	return out, nil
}

func (s *PlanStore) CreatePlan(ctx context.Context, req billing.CreatePlanRequest) (*billing.SubscriptionPlan, error) {
	groupIDs, groupRates, currency := req.GroupIDs, req.GroupRateMultipliers, req.Currency

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()

	builder := client.SubscriptionPlan.Create().
		SetName(strings.TrimSpace(req.Name)).
		SetDescription(req.Description).
		SetPrice(req.Price).
		SetCurrency(currency).
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
	if err := syncPlanGroupMappings(ctx, client, int64(plan.ID), groupIDs, groupRates); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return PlanFromEntity(plan), nil
}

func (s *PlanStore) UpdatePlan(ctx context.Context, id int64, req billing.UpdatePlanRequest) (*billing.SubscriptionPlan, error) {
	var groupIDs []int64
	var groupRates map[int64]float64
	if req.GroupIDs != nil {
		groupID := int64(0)
		if req.GroupID != nil {
			groupID = *req.GroupID
		}
		groupIDs = billing.NormalizePlanGroupIDs(groupID, *req.GroupIDs)
	}
	useTx := req.GroupIDs != nil || req.GroupRateMultipliers != nil
	client := s.entClient
	var tx *dbent.Tx
	if useTx {
		var err error
		tx, err = s.entClient.Tx(ctx)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		client = tx.Client()
	}

	if req.GroupIDs != nil || req.GroupRateMultipliers != nil {
		existing, err := client.SubscriptionPlan.Get(ctx, id)
		if err != nil {
			return nil, apperror.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
		}
		if req.GroupIDs == nil {
			groupIDs = append([]int64(nil), existing.GroupIds...)
		}
		rates := existing.GroupRateMultipliers
		if req.GroupRateMultipliers != nil {
			rates = *req.GroupRateMultipliers
		}
		groupRates, err = billing.NormalizePlanGroupRateMultipliers(groupIDs, rates)
		if err != nil {
			return nil, err
		}
	}

	update := client.SubscriptionPlan.UpdateOneID(id)
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
	if req.OriginalPrice.Present {
		if req.OriginalPrice.Value == nil {
			update.ClearOriginalPrice()
		} else {
			update.SetOriginalPrice(*req.OriginalPrice.Value)
		}
	}
	if req.Currency != nil {
		currency, err := billing.NormalizePlanCurrency(*req.Currency)
		if err != nil {
			return nil, err
		}
		update.SetCurrency(currency)
	}
	if req.ValidityDays != nil {
		update.SetValidityDays(*req.ValidityDays)
	}
	if req.ValidityUnit != nil {
		update.SetValidityUnit(strings.TrimSpace(*req.ValidityUnit))
	}
	if req.DailyLimitUSD.Present {
		if req.DailyLimitUSD.Value == nil {
			update.ClearDailyLimitUsd()
		} else {
			update.SetDailyLimitUsd(*req.DailyLimitUSD.Value)
		}
	}
	if req.WeeklyLimitUSD.Present {
		if req.WeeklyLimitUSD.Value == nil {
			update.ClearWeeklyLimitUsd()
		} else {
			update.SetWeeklyLimitUsd(*req.WeeklyLimitUSD.Value)
		}
	}
	if req.MonthlyLimitUSD.Present {
		if req.MonthlyLimitUSD.Value == nil {
			update.ClearMonthlyLimitUsd()
		} else {
			update.SetMonthlyLimitUsd(*req.MonthlyLimitUSD.Value)
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
		if err := syncPlanGroupMappings(ctx, client, id, groupIDs, groupRates); err != nil {
			return nil, err
		}
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	return PlanFromEntity(plan), nil
}

type planGroupMappingExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func syncPlanGroupMappings(ctx context.Context, exec planGroupMappingExecutor, planID int64, groupIDs []int64, rates map[int64]float64) error {
	if exec == nil || planID <= 0 {
		return nil
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM subscription_plan_groups WHERE plan_id = $1`, planID); err != nil {
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
		if _, err := exec.ExecContext(ctx, `
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
func (s *PlanStore) DeletePlan(ctx context.Context, id int64) error {
	return s.entClient.SubscriptionPlan.DeleteOneID(id).Exec(ctx)
}

func (s *PlanStore) GetPlan(ctx context.Context, id int64) (*billing.SubscriptionPlan, error) {
	plan, err := s.entClient.SubscriptionPlan.Get(ctx, id)
	if err != nil {
		return nil, apperror.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
	}
	return PlanFromEntity(plan), nil
}
