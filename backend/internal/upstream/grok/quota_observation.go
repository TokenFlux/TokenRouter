package grok

import (
	"net/http"
	"time"
)

// ParseQuotaObservation 只补齐旧 429 无 Header 的观测事实，不决定账号重试或冷却策略。
func ParseQuotaObservation(headers http.Header, status int, now time.Time) *QuotaSnapshot {
	snapshot := ParseQuotaHeaders(headers, status)
	if snapshot == nil && status == http.StatusTooManyRequests {
		return &QuotaSnapshot{StatusCode: status, UpdatedAt: now.UTC().Format(time.RFC3339)}
	}
	return snapshot
}
