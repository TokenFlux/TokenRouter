package billing

import (
	"time"
)

type SubscriptionPlan struct {
	ID                   int64
	Name                 string
	Description          string
	Price                float64
	OriginalPrice        *float64
	Currency             string
	ValidityDays         int
	ValidityUnit         string
	GroupIDs             []int64
	GroupRateMultipliers map[int64]float64
	GroupsRestricted     bool
	ApplicableGroups     []SubscriptionPlanGroup
	DailyLimitUSD        *float64
	WeeklyLimitUSD       *float64
	MonthlyLimitUSD      *float64
	Features             string
	ProductName          string
	ForSale              bool
	SortOrder            int
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// SubscriptionPlanGroup 是订阅套餐分组限制的名称摘要。
type SubscriptionPlanGroup struct {
	ID   int64
	Name string
}
