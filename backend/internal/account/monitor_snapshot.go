// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"time"
)

// CNUsageMonitorError 记录最近一次探测失败，不包含凭据或原始响应正文。
type CNUsageMonitorError struct {
	Code       string    `json:"code"`
	ObservedAt time.Time `json:"observed_at"`
}

// CNUsageMonitorSnapshot 保存最近成功数据与最近一次尝试状态。失败只更新 LastError，
// 不覆盖上一次成功的余额或窗口。
type CNUsageMonitorSnapshot struct {
	Version       int                         `json:"version"`
	Adapter       string                      `json:"adapter"`
	IdentityHash  string                      `json:"identity_hash"`
	Provider      string                      `json:"provider,omitempty"`
	Mode          string                      `json:"mode,omitempty"`
	Unit          string                      `json:"unit,omitempty"`
	Balance       *UpstreamUsageAmount        `json:"balance,omitempty"`
	Balances      []UpstreamUsageBalanceEntry `json:"balances,omitempty"`
	Available     *bool                       `json:"available,omitempty"`
	Limits        []UpstreamUsageLimit        `json:"limits,omitempty"`
	Subscription  *UpstreamUsageSubscription  `json:"subscription,omitempty"`
	ExpiresAt     *time.Time                  `json:"expires_at,omitempty"`
	ObservedAt    *time.Time                  `json:"observed_at,omitempty"`
	LastAttemptAt time.Time                   `json:"last_attempt_at"`
	LastError     *CNUsageMonitorError        `json:"last_error,omitempty"`
}
