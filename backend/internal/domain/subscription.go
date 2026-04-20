package domain

type SubscriptionPlanSnapshot struct {
	Name            string   `json:"name"`
	Price           float64  `json:"price"`
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
	Type           BillingAllocationType `json:"type"`
	AmountUSD      float64               `json:"amount_usd"`
	SubscriptionID *int64                `json:"subscription_id,omitempty"`
	PlanID         *int64                `json:"plan_id,omitempty"`
}
