package grokforward

import (
	"context"
	"net/http"
	"time"

	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// ObservationPorts 只发布本次额度事实或请求精确恢复；账号领域决定是否及如何写入。
type ObservationPorts interface {
	Stamp(*nativegrok.QuotaSnapshot, string)
	Store(context.Context, *nativegrok.QuotaSnapshot)
	Recovery(*nativegrok.QuotaSnapshot) bool
	ClearRecovered(context.Context)
}

// ObserveResponse 保留先解析/标记再发布的顺序；没有额度头时不以空快照覆盖原数据。
func ObserveResponse(ctx context.Context, p ObservationPorts, headers http.Header, status int, model string) {
	snapshot := ParseQuota(headers, status, time.Now())
	if snapshot != nil {
		p.Stamp(snapshot, model)
		p.Store(ctx, snapshot)
		return
	}
	if p.Recovery(&nativegrok.QuotaSnapshot{StatusCode: status}) {
		p.ClearRecovered(ctx)
	}
}

// ParseQuota 只补齐旧 429 无 Header 的观测事实，不决定账号重试或冷却策略。
func ParseQuota(headers http.Header, status int, now time.Time) *nativegrok.QuotaSnapshot {
	snapshot := nativegrok.ParseQuotaHeaders(headers, status)
	if snapshot == nil && status == http.StatusTooManyRequests {
		return &nativegrok.QuotaSnapshot{StatusCode: status, UpdatedAt: now.UTC().Format(time.RFC3339)}
	}
	return snapshot
}
