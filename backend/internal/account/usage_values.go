// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	time "time"
)

// UpstreamUsageQueryConfig 是账号 Extra 中公开给管理员的非敏感查询配置。
type UpstreamUsageQueryConfig struct {
	Enabled bool   `json:"enabled"`
	Adapter string `json:"adapter"`
	BaseURL string `json:"base_url,omitempty"`
}

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

// UpstreamUsageQueryResult 是管理员查询接口的成功响应。
type UpstreamUsageQueryResult struct {
	AccountID    int64                       `json:"account_id"`
	Adapter      string                      `json:"adapter"`
	ObservedAt   time.Time                   `json:"observed_at"`
	Provider     string                      `json:"provider,omitempty"`
	Mode         string                      `json:"mode,omitempty"`
	Unit         string                      `json:"unit,omitempty"`
	Balance      *UpstreamUsageAmount        `json:"balance,omitempty"`
	Balances     []UpstreamUsageBalanceEntry `json:"balances,omitempty"`
	Available    *bool                       `json:"available,omitempty"`
	Limits       []UpstreamUsageLimit        `json:"limits,omitempty"`
	Subscription *UpstreamUsageSubscription  `json:"subscription,omitempty"`
	ExpiresAt    *time.Time                  `json:"expires_at,omitempty"`
	// Usage 仅供服务内部复用归一化对象，不暴露到管理员响应，避免把协议内部模型
	// 再套一层 API Key 的“窗口”语义。
	Usage *UpstreamUsageInfo `json:"-"`
}

// UpstreamUsageMetrics 是进程内的查询计数快照；只保存适配器和错误分类，不保存凭据或响应内容。
type UpstreamUsageMetrics struct {
	Counts map[string]int64 `json:"counts"`
}

// UpstreamUsageAdapterOption 用于前端或诊断页面展示可用适配器。
type UpstreamUsageAdapterOption struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}
