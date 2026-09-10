// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package servertiming

import (
	context "context"
	time "time"

	foundation "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// HTTPTraceSnapshot 保留旧调用方的类型身份；实现归目标包。
type HTTPTraceSnapshot = foundation.HTTPTraceSnapshot

// WithHTTPTrace 兼容旧入口；仅转发到目标实现。
func WithHTTPTrace(ctx context.Context) context.Context {
	return foundation.WithHTTPTrace(ctx)
}

// BeginHTTPTrace 兼容旧入口；仅转发到目标实现。
func BeginHTTPTrace(ctx context.Context) context.Context {
	return foundation.BeginHTTPTrace(ctx)
}

// HTTPTraceSnapshotFromContext 兼容旧入口；仅转发到目标实现。
func HTTPTraceSnapshotFromContext(ctx context.Context) (HTTPTraceSnapshot, bool) {
	return foundation.HTTPTraceSnapshotFromContext(ctx)
}

// HTTPTraceElapsedMs 兼容旧入口；仅转发到目标实现。
func HTTPTraceElapsedMs(snapshot HTTPTraceSnapshot, at time.Time) (int64, bool) {
	return foundation.HTTPTraceElapsedMs(snapshot, at)
}
