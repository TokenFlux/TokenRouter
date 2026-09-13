package ops

import (
	"context"
	"time"
)

type CollectedSystemStats struct {
	CpuUsagePercent    *float64
	MemoryUsedMB       *int64
	MemoryTotalMB      *int64
	MemoryUsagePercent *float64
	DiskUsedMB         *int64
	DiskTotalMB        *int64
	DiskUsagePercent   *float64
}
type CollectedPercentiles struct {
	P50 *int
	P90 *int
	P95 *int
	P99 *int
	Avg *float64
	Max *int
}

type AccountLoadSource interface {
	ListSchedulable(context.Context) ([]AccountObservation, error)
}
type MetricsSource interface {
	AdvisoryLocker
	QueryUsageCounts(context.Context, time.Time, time.Time) (int64, int64, error)
	QueryUsageLatency(context.Context, time.Time, time.Time) (CollectedPercentiles, CollectedPercentiles, error)
	QueryErrorCounts(context.Context, time.Time, time.Time, []int) (int64, int64, int64, int64, int64, int64, error)
	QueryAccountSwitchCount(context.Context, time.Time, time.Time) (int64, error)
}
type HostSampler interface {
	CollectSystemStats(context.Context) (*CollectedSystemStats, error)
	CheckDB(context.Context) bool
	CheckRedis(context.Context) bool
	DbPoolStats() (int, int)
	RedisPoolStats() (int, int, bool)
	GoroutineCount() int
}
