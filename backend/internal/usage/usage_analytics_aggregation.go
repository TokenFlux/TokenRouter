package usage

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
)

// UsageAnalyticsAggregationState 记录多维用量聚合的实时水位和历史覆盖进度。
type UsageAnalyticsAggregationState struct {
	observedManualStart, observedManualCursor *time.Time
	LiveWatermark                             time.Time
	CoverageStart                             *time.Time
	BackfillCursor                            *time.Time
	SourceOldestAt                            *time.Time
	ManualBackfillStart                       *time.Time
	ManualBackfillCursor                      *time.Time
	Phase                                     string
	LastRunAt                                 *time.Time
	LastSuccessAt                             *time.Time
	LastErrorAt                               *time.Time
	LastError                                 string
	LastDurationMS                            int64
}

// UsageAnalyticsAggregationRepository 定义多维用量预聚合所需的仓储能力。
type UsageAnalyticsAggregationRepository interface {
	AggregateUsageAnalyticsRange(ctx context.Context, start, end time.Time) error
	AggregateUsageAnalyticsHourlyRange(ctx context.Context, start, end time.Time) error
	RebuildUsageAnalyticsDailyRange(ctx context.Context, start, end time.Time) error
	RecomputeUsageAnalyticsRange(ctx context.Context, start, end time.Time) error
	GetUsageAnalyticsAggregationState(ctx context.Context) (*UsageAnalyticsAggregationState, error)
	ApplyUsageAnalyticsState(ctx context.Context, change AnalyticsStateChange) (*UsageAnalyticsAggregationState, error)
	GetOldestUsageLogTime(ctx context.Context) (*time.Time, error)
	CleanupUsageAnalytics(ctx context.Context, hourlyCutoff, dailyCutoff time.Time) error
}

type PreAggregationRuntimeStatus = preaggregation.PreAggregationRuntimeStatus
