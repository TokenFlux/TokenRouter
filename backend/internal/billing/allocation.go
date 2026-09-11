// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

type SubscriptionPlanSnapshot struct {
	Name            string   `json:"name"`
	Price           float64  `json:"price"`
	Currency        string   `json:"currency,omitempty"` // 下单时的套餐标价币种
	ValidityDays    int      `json:"validity_days"`
	DailyLimitUSD   *float64 `json:"daily_limit_usd,omitempty"`
	WeeklyLimitUSD  *float64 `json:"weekly_limit_usd,omitempty"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd,omitempty"`
}

type BillingAllocationType string

const (
	BillingAllocationTypeSubscription BillingAllocationType = "subscription"
	BillingAllocationTypeBalance      BillingAllocationType = "balance"
)

type BillingAllocation struct {
	Type      BillingAllocationType `json:"type"`
	AmountUSD float64               `json:"amount_usd"`
	// BaseAmountUSD 和 RateMultiplier 用于还原分来源计费，旧记录缺省时仍按 AmountUSD 兼容。
	BaseAmountUSD  float64 `json:"base_amount_usd,omitempty"`
	RateMultiplier float64 `json:"rate_multiplier,omitempty"`
	SubscriptionID *int64  `json:"subscription_id,omitempty"`
	PlanID         *int64  `json:"plan_id,omitempty"`
}
