package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/service"
)

func (r *opsRepository) GetLatencyHistogram(ctx context.Context, filter *service.OpsDashboardFilter, bucketBoundariesMS []int64) (*service.OpsLatencyHistogramResponse, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if filter == nil {
		return nil, fmt.Errorf("nil filter")
	}
	if filter.StartTime.IsZero() || filter.EndTime.IsZero() {
		return nil, fmt.Errorf("start_time/end_time required")
	}
	boundaries, err := service.NormalizeOpsLatencyBucketBoundariesMS(bucketBoundariesMS)
	if err != nil {
		return nil, err
	}

	start := filter.StartTime.UTC()
	end := filter.EndTime.UTC()

	join, where, args, next := buildUsageWhere(filter, start, end, 1)
	bucketExpr := latencyHistogramBucketIndexCaseExpr("ul.duration_ms", next, len(boundaries))
	for _, boundary := range boundaries {
		args = append(args, boundary)
	}

	q := `
SELECT
  ` + bucketExpr + ` AS bucket_index,
  COALESCE(COUNT(*), 0) AS count
FROM usage_logs ul
` + join + `
` + where + `
AND ul.duration_ms IS NOT NULL
GROUP BY 1
ORDER BY 1 ASC`

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	labels := latencyHistogramBucketLabels(boundaries)
	counts := make(map[int]int64, len(labels))
	var total int64
	for rows.Next() {
		var bucketIndex int
		var count int64
		if err := rows.Scan(&bucketIndex, &count); err != nil {
			return nil, err
		}
		if bucketIndex < 0 || bucketIndex >= len(labels) {
			return nil, fmt.Errorf("invalid latency bucket index %d", bucketIndex)
		}
		counts[bucketIndex] = count
		total += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	buckets := make([]*service.OpsLatencyHistogramBucket, 0, len(labels))
	for index, label := range labels {
		buckets = append(buckets, &service.OpsLatencyHistogramBucket{
			Range: label,
			Count: counts[index],
		})
	}

	return &service.OpsLatencyHistogramResponse{
		StartTime:          start,
		EndTime:            end,
		Platform:           strings.TrimSpace(filter.Platform),
		GroupID:            filter.GroupID,
		BucketBoundariesMS: boundaries,
		TotalRequests:      total,
		Buckets:            buckets,
	}, nil
}
