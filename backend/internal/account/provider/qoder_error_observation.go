package provider

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// QoderHealthStore 只提供请求错误原有的限流与短暂过载写入。
type QoderHealthStore interface {
	SetRateLimited(context.Context, int64, time.Time) error
	SetOverloaded(context.Context, int64, time.Time) error
}

// ObserveQoderUpstreamError 保留脱离请求取消的五秒写入预算及尽力失败语义。
func ObserveQoderUpstreamError(ctx context.Context, accountID int64, store QoderHealthStore, err error) {
	if store == nil || err == nil {
		return
	}
	var apiErr *qoder.APIError
	if !errors.As(err, &apiErr) {
		return
	}
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	stateCtx, cancel := context.WithTimeout(base, 5*time.Second)
	defer cancel()
	switch {
	case apiErr.IsAgentLimit():
		resetAt, ok := apiErr.AgentLimitResetAt()
		if !ok {
			resetAt = time.Now().Add(30 * time.Second)
		}
		_ = store.SetRateLimited(stateCtx, accountID, resetAt)
	case apiErr.StatusCode == http.StatusTooManyRequests:
		_ = store.SetRateLimited(stateCtx, accountID, time.Now().Add(30*time.Second))
	case apiErr.StatusCode >= 500:
		_ = store.SetOverloaded(stateCtx, accountID, time.Now().Add(30*time.Second))
	}
}
