package provider

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// CodexUsageObserver 使用共享节流与后台任务端口保存已观测的全局额度头。
// 影子账号资格仍由调用方在原位置判断。
type CodexUsageObserver struct {
	Store interface {
		UpdateExtra(context.Context, int64, map[string]any) error
	}
	Throttle *account.WriteThrottle
	Go       func(string, func()) bool
}

func (s *CodexUsageObserver) Observe(ctx context.Context, id int64, snapshot *protocolopenai.OpenAICodexUsageSnapshot) {
	if snapshot == nil || s == nil || s.Store == nil {
		return
	}
	now := time.Now()
	updates := account.BuildCodexUsageExtraUpdates(snapshot, now)
	if len(updates) == 0 || !s.Throttle.Allow(id, now) {
		return
	}
	work := func() {
		updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Store.UpdateExtra(updateCtx, id, updates)
	}
	if s.Go != nil {
		s.Go("service/openai_gateway_usage.go:updateCodexUsageSnapshot", work)
	} else {
		go work()
	}
}
func (s *CodexUsageObserver) Headers(ctx context.Context, id int64, headers http.Header) {
	if id <= 0 || headers == nil {
		return
	}
	if snapshot := openai.ParseCodexRateLimitHeaders(headers); snapshot != nil {
		s.Observe(ctx, id, snapshot)
	}
}
