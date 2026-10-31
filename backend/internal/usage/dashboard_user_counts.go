package usage

import (
	"context"
	"errors"
	"time"
)

// dashboard_user_counts.go exposes bounded-period actor request and token counts
// alongside the existing batch charge report. The charge report retains its
// native shape and positive-charge rules; these counters include retained usage
// rows with zero charge, matching the counters used by ordinary user dashboards.

// BatchUserUsageCounts contains retained request and token totals for one actor account.
type BatchUserUsageCounts struct {
	UserID   int64 // queried actor account identifier
	Requests int64 // retained period request count
	Tokens   int64 // retained period token count
}

// BatchUserUsageCountReader retrieves actor counters over an explicit exclusive-end interval.
type BatchUserUsageCountReader interface {
	// GetBatchUserUsageCounts returns zero-valued entries for requested actors without usage.
	// Empty normalized IDs produce an empty map. Nonempty requests require explicit
	// ordered time bounds, and counters include zero-charge retained usage records.
	GetBatchUserUsageCounts(context.Context, []int64, time.Time, time.Time) (map[int64]BatchUserUsageCounts, error)
}

// GetBatchUserUsageCounts retrieves actor counters without changing the existing charge response.
// The configured repository must implement the dedicated count capability. Missing
// implementations fail explicitly instead of substituting charge-only values for token counts.
func (s *DashboardService) GetBatchUserUsageCounts(ctx context.Context, ids []int64, start, end time.Time) (map[int64]BatchUserUsageCounts, error) {
	reader, ok := s.usageRepo.(BatchUserUsageCountReader)
	if !ok {
		return nil, errors.New("batch user usage counters are unavailable")
	}
	return reader.GetBatchUserUsageCounts(ctx, ids, start, end)
}
