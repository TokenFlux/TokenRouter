package usage

import (
	"context"
	"time"
)

// AccountTokenWindowReader 保留旧统计入口及可选的批量读取能力。
type AccountTokenWindowReader interface {
	GetAccountWindowStats(context.Context, int64, time.Time) (*AccountStats, error)
}

// ReadAccountTokenWindow 优先批量读取；批量失败不额外执行逐账号查询。
func ReadAccountTokenWindow(ctx context.Context, reader AccountTokenWindowReader, ids []int64, start time.Time) (map[int64]int64, error) {
	if reader == nil {
		return nil, nil
	}
	var values map[int64]*AccountStats
	if batch, ok := reader.(interface {
		GetAccountWindowStatsBatch(context.Context, []int64, time.Time) (map[int64]*AccountStats, error)
	}); ok {
		var err error
		values, err = batch.GetAccountWindowStatsBatch(ctx, ids, start)
		if err != nil {
			return nil, err
		}
	} else {
		values = make(map[int64]*AccountStats, len(ids))
		for _, id := range ids {
			value, err := reader.GetAccountWindowStats(ctx, id, start)
			if err != nil {
				return nil, err
			}
			values[id] = value
		}
	}
	tokens := make(map[int64]int64, len(values))
	for id, value := range values {
		if value != nil {
			tokens[id] = value.Tokens
		}
	}
	return tokens, nil
}
