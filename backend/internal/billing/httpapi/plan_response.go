// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package httpapi

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	time "time"
)

// PlanRecordResponse 保留管理员套餐接口原 Ent JSON 形状，不暴露 Ent 实体。
type PlanRecordResponse struct {
	ID                   int64             `json:"id,omitempty"`
	Name                 string            `json:"name,omitempty"`
	Description          string            `json:"description,omitempty"`
	Price                float64           `json:"price,omitempty"`
	OriginalPrice        *float64          `json:"original_price,omitempty"`
	Currency             string            `json:"currency,omitempty"`
	ValidityDays         int               `json:"validity_days,omitempty"`
	DailyLimitUSD        *float64          `json:"daily_limit_usd,omitempty"`
	WeeklyLimitUSD       *float64          `json:"weekly_limit_usd,omitempty"`
	MonthlyLimitUSD      *float64          `json:"monthly_limit_usd,omitempty"`
	ValidityUnit         string            `json:"validity_unit,omitempty"`
	GroupIDs             []int64           `json:"group_ids,omitempty"`
	GroupRateMultipliers map[int64]float64 `json:"group_rate_multipliers,omitempty"`
	Features             string            `json:"features,omitempty"`
	ProductName          string            `json:"product_name,omitempty"`
	ForSale              bool              `json:"for_sale,omitempty"`
	SortOrder            int               `json:"sort_order,omitempty"`
	CreatedAt            time.Time         `json:"created_at,omitempty"`
	UpdatedAt            time.Time         `json:"updated_at,omitempty"`
	Edges                struct{}          `json:"edges"`
}

func planRecord(plan *billing.SubscriptionPlan) *PlanRecordResponse {
	if plan == nil {
		return nil
	}
	return &PlanRecordResponse{
		ID:                   plan.ID,
		Name:                 plan.Name,
		Description:          plan.Description,
		Price:                plan.Price,
		OriginalPrice:        plan.OriginalPrice,
		Currency:             plan.Currency,
		ValidityDays:         plan.ValidityDays,
		DailyLimitUSD:        plan.DailyLimitUSD,
		WeeklyLimitUSD:       plan.WeeklyLimitUSD,
		MonthlyLimitUSD:      plan.MonthlyLimitUSD,
		ValidityUnit:         plan.ValidityUnit,
		GroupIDs:             plan.GroupIDs,
		GroupRateMultipliers: plan.GroupRateMultipliers,
		Features:             plan.Features,
		ProductName:          plan.ProductName,
		ForSale:              plan.ForSale,
		SortOrder:            plan.SortOrder,
		CreatedAt:            plan.CreatedAt,
		UpdatedAt:            plan.UpdatedAt,
	}
}
