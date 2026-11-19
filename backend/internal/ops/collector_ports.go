package ops

import (
	"context"
	"time"
)

// collector_ports.go defines historical-query, metric-persistence, and
// host-sampling dependencies consumed by the metrics collector.
// A collector sample combines historical request counts with current host and
// connection-pool observations. Nullable measurements distinguish missing data
// from zero utilization, while narrow consumer interfaces let app supply
// PostgreSQL queries, metric writers, provider loads, and host probes to the
// collector without coupling it to dashboard or alert storage.

// MetricsCollectorRepository persists sampled system metrics and collector job
// heartbeats.
type MetricsCollectorRepository interface {
	JobHeartbeatWriter
	// InsertSystemMetrics stores one collected system, pool, and traffic-window
	// metric snapshot.
	InsertSystemMetrics(ctx context.Context, input *OpsInsertSystemMetricsInput) error
}

// CollectedSystemStats holds nullable host or container measurements for one
// collector sample.
type CollectedSystemStats struct {
	// CpuUsagePercent, MemoryUsedMB, MemoryTotalMB, MemoryUsagePercent,
	// DiskUsedMB, DiskTotalMB, and DiskUsagePercent use nil for unavailable
	// measurements. HostSampler selects each measurement source before the
	// collector persists its sample.
	CpuUsagePercent    *float64 // CPU utilization percentage
	MemoryUsedMB       *int64   // MiB
	MemoryTotalMB      *int64   // MiB
	MemoryUsagePercent *float64 // memory utilization percentage
	DiskUsedMB         *int64   // MiB
	DiskTotalMB        *int64   // MiB
	DiskUsagePercent   *float64 // filesystem utilization percentage
}

// CollectedPercentiles holds nullable request-duration or first-token latency
// statistics in milliseconds.
type CollectedPercentiles struct {
	// P50, P90, P95, P99, Avg, and Max distinguish missing latency samples with
	// nil pointers. Integer percentiles are rounded by MetricsQueries; Avg retains
	// one decimal place.
	P50 *int     // milliseconds
	P90 *int     // milliseconds
	P95 *int     // milliseconds
	P99 *int     // milliseconds
	Avg *float64 // milliseconds
	Max *int     // milliseconds
}

// ProviderLoadSource reads the schedulable provider observations needed for
// concurrency sampling.
type ProviderLoadSource interface {
	// ListSchedulable returns observations selected by the provider repository for
	// collector load queries. The collector extracts positive IDs and their
	// effective load factors when a dedicated load reader is unavailable.
	ListSchedulable(context.Context) ([]ProviderObservation, error)
}

// MetricsSource reads original request observations and supplies advisory
// locking for collector samples.
type MetricsSource interface {
	AdvisoryLocker // collector lock fallback capability
	// QueryUsageCounts returns usage-row count and the sum of input, output,
	// cache-creation, and cache-read tokens. The time range includes its start and
	// excludes its end; counts are observation rows.
	QueryUsageCounts(context.Context, time.Time, time.Time) (int64, int64, error)
	// QueryUsageLatency returns duration statistics first and first-token
	// statistics second for the half-open request window. Nil measurements
	// identify absent samples, and the collector aborts persistence when either
	// latency query fails.
	QueryUsageLatency(context.Context, time.Time, time.Time) (CollectedPercentiles, CollectedPercentiles, error)
	// QueryErrorCounts returns total errors, business-limited errors, SLA errors,
	// other upstream errors, upstream 429, and upstream 529 counts in that order.
	// Ignored client statuses use the supplied policy, with nil selecting
	// defaults.
	QueryErrorCounts(context.Context, time.Time, time.Time, []int) (int64, int64, int64, int64, int64, int64, error)
	// QueryProviderSwitchCount counts saved failover-kind attempt events within
	// the half-open request window. Count-token probes are excluded, and multiple
	// qualifying events in one error record each contribute a switch.
	QueryProviderSwitchCount(context.Context, time.Time, time.Time) (int64, error)
}

// HostSampler supplies process, machine, and connection-pool measurements for
// the metrics collector.
type HostSampler interface {
	// CollectSystemStats returns the sample object used by collectAndPersist, with
	// nil fields for unavailable measurements. Supply a nonnil object even when
	// returning an error, because the collector logs that error and continues
	// using the sample.
	CollectSystemStats(context.Context) (*CollectedSystemStats, error)
	// CheckDB reports whether the collector database health probe succeeds for
	// this sample. An unavailable dependency or failed probe is represented as
	// false and remains a measured health result.
	CheckDB(context.Context) bool
	// CheckRedis reports whether the collector Redis health probe succeeds for
	// this sample. An unavailable dependency or failed probe becomes false and is
	// persisted as a measured health result.
	CheckRedis(context.Context) bool
	// DbPoolStats returns database in-use and idle connection counts for the
	// collector sample. Missing pools return zero counts in HostObserver, so these
	// values alone do not identify measurement availability.
	DbPoolStats() (int, int)
	// RedisPoolStats returns total and idle Redis connections with an availability
	// boolean. The collector retains zero counts when the boolean is true and
	// writes nil measurements when it is false.
	RedisPoolStats() (int, int, bool)
	// GoroutineCount returns the process goroutine count recorded in the collector
	// sample.
	GoroutineCount() int
}
