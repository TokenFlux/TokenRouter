package service

import "time"

type SubscriptionPlan struct {
	ID              int64
	Name            string
	Description     string
	Price           float64
	OriginalPrice   *float64
	ValidityDays    int
	ValidityUnit    string
	DailyLimitUSD   *float64
	WeeklyLimitUSD  *float64
	MonthlyLimitUSD *float64
	Features        string
	ProductName     string
	ForSale         bool
	SortOrder       int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
