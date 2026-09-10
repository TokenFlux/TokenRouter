// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package servertiming

import (
	context "context"
	time "time"

	foundation "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// HeaderName 兼容旧入口，值由唯一实现提供。
const HeaderName = foundation.HeaderName

// AdminUIHeader 兼容旧入口，值由唯一实现提供。
const AdminUIHeader = foundation.AdminUIHeader

// UserUIHeader 兼容旧入口，值由唯一实现提供。
const UserUIHeader = foundation.UserUIHeader

// MetricDatabase 兼容旧入口，值由唯一实现提供。
const MetricDatabase = foundation.MetricDatabase

// MetricRedis 兼容旧入口，值由唯一实现提供。
const MetricRedis = foundation.MetricRedis

// Collector 保留旧调用方的类型身份；实现归目标包。
type Collector = foundation.Collector

// New 兼容旧入口；仅转发到目标实现。
func New(startedAt time.Time) *Collector {
	return foundation.New(startedAt)
}

// WithCollector 兼容旧入口；仅转发到目标实现。
func WithCollector(ctx context.Context, collector *Collector) context.Context {
	return foundation.WithCollector(ctx, collector)
}

// FromContext 兼容旧入口；仅转发到目标实现。
func FromContext(ctx context.Context) (*Collector, bool) {
	return foundation.FromContext(ctx)
}

// Active 兼容旧入口；仅转发到目标实现。
func Active(ctx context.Context) bool {
	return foundation.Active(ctx)
}

// Record 兼容旧入口；仅转发到目标实现。
func Record(ctx context.Context, name string, startedAt, endedAt time.Time, count int) {
	foundation.Record(ctx, name, startedAt, endedAt, count)
}

// RecordInterval 兼容旧入口；仅转发到目标实现。
func RecordInterval(ctx context.Context, name string, startedAt, endedAt time.Time) {
	foundation.RecordInterval(ctx, name, startedAt, endedAt)
}

// Observe 兼容旧入口；仅转发到目标实现。
func Observe(ctx context.Context, name string) func() {
	return foundation.Observe(ctx, name)
}

// ObserveDependency 兼容旧入口；仅转发到目标实现。
func ObserveDependency(ctx context.Context, module string) func() {
	return foundation.ObserveDependency(ctx, module)
}

// RecordDependency 兼容旧入口；仅转发到目标实现。
func RecordDependency(ctx context.Context, module string, startedAt, endedAt time.Time) {
	foundation.RecordDependency(ctx, module, startedAt, endedAt)
}

// SetCacheStatus 兼容旧入口；仅转发到目标实现。
func SetCacheStatus(ctx context.Context, status string) {
	foundation.SetCacheStatus(ctx, status)
}

// HeaderValue 兼容旧入口；仅转发到目标实现。
func HeaderValue(ctx context.Context, endedAt time.Time, cacheStatus string) string {
	return foundation.HeaderValue(ctx, endedAt, cacheStatus)
}
