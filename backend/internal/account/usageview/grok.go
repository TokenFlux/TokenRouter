// 本文件维护 usageview 的所属能力；兼容入口复用唯一实现。
package usageview

// BillingProductSummary 是供前端使用的规范化产品用量记录。
type BillingProductSummary struct {
	Product      string   `json:"product"`
	UsagePercent *float64 `json:"usage_percent,omitempty"`
}

// BillingSummary 是合并周度和月度数据后的 billing 视图。
type BillingSummary struct {
	PeriodType           string                  `json:"period_type,omitempty"` // weekly、monthly 或 unknown
	UsagePercent         *float64                `json:"usage_percent,omitempty"`
	PeriodStart          string                  `json:"period_start,omitempty"`
	PeriodEnd            string                  `json:"period_end,omitempty"`
	ProductUsage         []BillingProductSummary `json:"product_usage,omitempty"`
	MonthlyLimitCents    *float64                `json:"monthly_limit_cents,omitempty"`
	UsedCents            *float64                `json:"used_cents,omitempty"`
	IncludedUsedCents    *float64                `json:"included_used_cents,omitempty"`
	BillingPeriodStart   string                  `json:"billing_period_start,omitempty"`
	BillingPeriodEnd     string                  `json:"billing_period_end,omitempty"`
	UsedPercent          *float64                `json:"used_percent,omitempty"`
	Plan                 string                  `json:"plan,omitempty"` // SuperGrok、SuperGrok Heavy 或空字符串
	StatusCode           int                     `json:"status_code,omitempty"`
	WeeklyStatusCode     int                     `json:"weekly_status_code,omitempty"`
	MonthlyStatusCode    int                     `json:"monthly_status_code,omitempty"`
	Source               string                  `json:"source,omitempty"`
	FetchedAt            string                  `json:"fetched_at,omitempty"`
	UpdatedAt            string                  `json:"updated_at,omitempty"`
	WeeklyUpdatedAt      string                  `json:"weekly_updated_at,omitempty"`
	MonthlyUpdatedAt     string                  `json:"monthly_updated_at,omitempty"`
	Partial              bool                    `json:"partial,omitempty"`
	FailedWindows        []string                `json:"failed_windows,omitempty"`
	PrepaidBalance       *float64                `json:"prepaid_balance,omitempty"`
	OnDemandCap          *float64                `json:"on_demand_cap,omitempty"`
	OnDemandUsed         *float64                `json:"on_demand_used,omitempty"`
	MonthlyLimit         *float64                `json:"monthly_limit,omitempty"`
	MonthlyUsed          *float64                `json:"monthly_used,omitempty"`
	TopUpMethod          string                  `json:"top_up_method,omitempty"`
	IsUnifiedBillingUser bool                    `json:"is_unified_billing_user,omitempty"`
}

type QuotaWindow struct {
	Limit     *int64 `json:"limit,omitempty"`
	Remaining *int64 `json:"remaining,omitempty"`
	ResetUnix *int64 `json:"reset_unix,omitempty"`
	ResetAt   string `json:"reset_at,omitempty"`
}
