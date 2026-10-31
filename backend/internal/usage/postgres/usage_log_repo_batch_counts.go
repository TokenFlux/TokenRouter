package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_log_repo_batch_counts.go counts retained actor requests and tokens in one explicit
// time interval. It complements the existing batch charge query, whose native
// projection includes today's monetary values and positive-charge filtering.
// This query needs neither platform joins nor today's separate time window.

// GetBatchUserUsageCounts aggregates one bounded interval for distinct positive actor IDs.
// Missing usage remains a zero-valued requested entry. Empty normalized input
// returns immediately, and nonempty queries reject absent or reversed time bounds.
func (r *Store) GetBatchUserUsageCounts(ctx context.Context, ids []int64, start, end time.Time) (map[int64]usage.BatchUserUsageCounts, error) {
	selected := normalizePositiveInt64IDs(ids)
	result := make(map[int64]usage.BatchUserUsageCounts, len(selected))
	if len(selected) == 0 {
		return result, nil
	}
	if start.IsZero() || !end.After(start) {
		return nil, fmt.Errorf("batch user counters require an ordered reporting interval")
	}
	for _, id := range selected {
		result[id] = usage.BatchUserUsageCounts{UserID: id}
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT user_id,COUNT(*),
COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens),0)
FROM usage_logs WHERE user_id=ANY($1) AND created_at >= $2 AND created_at < $3 GROUP BY user_id`, pq.Array(selected), start, end)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var value usage.BatchUserUsageCounts
		if err := rows.Scan(&value.UserID, &value.Requests, &value.Tokens); err != nil {
			return nil, err
		}
		result[value.UserID] = value
	}
	return result, rows.Err()
}
