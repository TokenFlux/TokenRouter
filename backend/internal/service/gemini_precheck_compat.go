// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	slog "log/slog"
	time "time"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// LegacyGeminiUsageReader 只投影原 S08 统计字段，批量能力通过接口原样保留。
type legacyGeminiUsageReader struct{ source usage.UsageLogRepository }

func (r legacyGeminiUsageReader) GetModelUsage(ctx context.Context, id int64, start, end time.Time) ([]account.GeminiModelUsage, error) {
	values, err := r.source.GetModelStatsWithFilters(ctx, start, end, 0, 0, id, 0, nil, nil, nil)
	if values == nil {
		return nil, err
	}
	out := make([]account.GeminiModelUsage, len(values))
	for i, v := range values {
		out[i] = account.GeminiModelUsage{Model: v.Model, Requests: v.Requests, TotalTokens: v.TotalTokens, AccountCost: v.AccountCost}
	}
	return out, err
}

type legacyGeminiUsageBatchReader struct {
	legacyGeminiUsageReader
	account.GeminiUsageTotalsBatchReader
}

func LegacyGeminiUsageReader(source usage.UsageLogRepository) account.GeminiQuotaUsageReader {
	if source == nil {
		return nil
	}
	reader := legacyGeminiUsageReader{source}
	if batch, ok := source.(account.GeminiUsageTotalsBatchReader); ok {
		return legacyGeminiUsageBatchReader{reader, batch}
	}
	return reader
}
func (s *RateLimitService) BindGeminiPrecheck(core *account.GeminiPrecheck) {
	s.geminiPrecheckOnce.Do(func() { s.geminiPrecheck = core })
}

// GeminiPrecheckCore 保留旧独立构造，不创建第二个生产缓存。
func (s *RateLimitService) GeminiPrecheckCore() *account.GeminiPrecheck {
	if s == nil {
		return nil
	}
	s.geminiPrecheckOnce.Do(func() {
		s.geminiPrecheck = account.NewGeminiPrecheck(s.geminiQuotaService, LegacyGeminiUsageReader(s.usageRepo), account.GeminiPrecheckOptions{Now: time.Now, Location: geminiQuotaLocation(), Info: slog.Info})
	})
	return s.geminiPrecheck
}
