// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	time "time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// UpstreamUsageQueryConfig 是账号 Extra 中公开给管理员的非敏感查询配置。
type UpstreamUsageQueryConfig struct {
	Enabled bool   `json:"enabled"`
	Adapter string `json:"adapter"`
	BaseURL string `json:"base_url,omitempty"`
}

type UpstreamUsageAmount = usageview.UpstreamUsageAmount

type UpstreamUsageBalanceEntry = usageview.UpstreamUsageBalanceEntry

type UpstreamUsageLimit = usageview.UpstreamUsageLimit

type UpstreamUsageSubscription = usageview.UpstreamUsageSubscription

type UpstreamUsageInfo = usageview.UpstreamUsageInfo

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
