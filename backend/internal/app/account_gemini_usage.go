package app

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"time"
)

// projectGeminiModelUsage 明确选择账号成本，不能使用用户实际扣费字段。
func projectGeminiModelUsage(rows []usage.ModelStat) []account.GeminiModelUsage {
	if rows == nil {
		return nil
	}
	values := make([]account.GeminiModelUsage, len(rows))
	for i, row := range rows {
		values[i] = account.GeminiModelUsage{Model: row.Model, Requests: row.Requests, TotalTokens: row.TotalTokens, AccountCost: row.AccountCost}
	}
	return values
}

type accountGeminiUsageReader struct{ source usage.UsageLogRepository }

func (s accountGeminiUsageReader) GetModelUsage(ctx context.Context, id int64, start, end time.Time) ([]account.GeminiModelUsage, error) {
	rows, err := s.source.GetModelStatsWithFilters(ctx, start, end, 0, 0, id, 0, nil, nil, nil)
	return projectGeminiModelUsage(rows), err
}

type accountGeminiUsageBatchReader struct {
	accountGeminiUsageReader
	account.GeminiUsageTotalsBatchReader
}

// newAccountGeminiUsageReader 保留批量读取能力和原查询参数，不复制统计缓存。
func newAccountGeminiUsageReader(source usage.UsageLogRepository) account.GeminiQuotaUsageReader {
	if source == nil {
		return nil
	}
	reader := accountGeminiUsageReader{source: source}
	if batch, ok := source.(account.GeminiUsageTotalsBatchReader); ok {
		return accountGeminiUsageBatchReader{reader, batch}
	}
	return reader
}
