// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"
	"time"

	service "github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
	"github.com/lib/pq"
)

func ReadAPIKeyUsageTotals(ctx context.Context, sqlq sqlExecutor, preAggregation *service.PreAggregationSettingsService, keyIDs []int64) (map[int64]float64, error) {
	result := make(map[int64]float64, len(keyIDs))
	if len(keyIDs) == 0 {
		return result, nil
	}
	for _, id := range keyIDs {
		result[id] = 0
	}

	now := time.Now()
	start := now.AddDate(0, 0, -30)
	if preAggregation != nil {
		usageRepo := &Store{sql: sqlq, preAggregation: preAggregation}
		if stats, ok, err := usageRepo.getBatchAPIKeyUsageStatsFromAnalytics(ctx, keyIDs, start, now); err == nil && ok {
			for keyID, stat := range stats {
				if stat != nil {
					result[keyID] = stat.TotalActualCost
				}
			}
			return result, nil
		} else if err != nil {
			usageRepo.logUsageAnalyticsFallback("api_key_list_usage", err)
		}
	}

	query := `
		SELECT api_key_id, COALESCE(SUM(actual_cost), 0)
		FROM usage_logs
		WHERE api_key_id = ANY($1)
		  AND created_at >= $2
		  AND created_at < $3
		GROUP BY api_key_id
	`
	rows, err := sqlq.QueryContext(ctx, query, pq.Array(keyIDs), now.AddDate(0, 0, -30), now)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var keyID int64
		var total float64
		if err := rows.Scan(&keyID, &total); err != nil {
			_ = rows.Close()
			return nil, err
		}
		result[keyID] = total
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}
