// 通用上游用量的只读值契约，不接收账号、HTTP 或资金服务。
package usageview

import "time"

// UpstreamUsageAmount 表示余额或累计限额的三个可选维度。
type UpstreamUsageAmount struct {
	Used      *float64 `json:"used,omitempty"`
	Total     *float64 `json:"total,omitempty"`
	Remaining *float64 `json:"remaining,omitempty"`
}

// UpstreamUsageBalanceEntry 表示多币种余额中的一项。
type UpstreamUsageBalanceEntry struct {
	Currency  string  `json:"currency"`
	Remaining float64 `json:"remaining"`
}

// UpstreamUsageLimit 表示上游返回的某个周期限额，不使用 OAuth 的窗口命名。
type UpstreamUsageLimit struct {
	Name      string     `json:"name"`
	Used      *float64   `json:"used,omitempty"`
	Limit     *float64   `json:"limit,omitempty"`
	Remaining *float64   `json:"remaining,omitempty"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
}

// UpstreamUsageSubscription 表示订阅余额和订阅周期限额。
type UpstreamUsageSubscription struct {
	PlanName  string               `json:"plan_name"`
	Unlimited bool                 `json:"unlimited,omitempty"`
	Remaining *float64             `json:"remaining,omitempty"`
	ExpiresAt *time.Time           `json:"expires_at,omitempty"`
	Limits    []UpstreamUsageLimit `json:"limits,omitempty"`
}

// UpstreamUsageInfo 是适配器归一化后的上游用量模型。
type UpstreamUsageInfo struct {
	Provider string `json:"provider"`
	Mode     string `json:"mode"`
	Unit     string `json:"unit,omitempty"`
	// New API/Zivv 的 balance 是用户钱包；Key quota 使用 Limits/Subscription。
	Balance      *UpstreamUsageAmount        `json:"balance,omitempty"`
	Balances     []UpstreamUsageBalanceEntry `json:"balances,omitempty"`
	Available    *bool                       `json:"available,omitempty"`
	Limits       []UpstreamUsageLimit        `json:"limits,omitempty"`
	Subscription *UpstreamUsageSubscription  `json:"subscription,omitempty"`
	ExpiresAt    *time.Time                  `json:"expires_at,omitempty"`
}
