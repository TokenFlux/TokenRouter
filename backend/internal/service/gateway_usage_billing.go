package service

import (
	"context"

	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
)

func detachStreamUpstreamContext(ctx context.Context, stream bool) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.Background(), func() {}
	}
	if !stream {
		return ctx, func() {}
	}
	return context.WithoutCancel(ctx), func() {}
}

func detachUpstreamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.Background(), func() {}
	}
	return context.WithoutCancel(ctx), func() {}
}

func writeUsageLogBestEffort(ctx context.Context, repo usage.UsageLogRepository, usageLog *usage.UsageLog, logKey string) {
	completion.NewRecorder(completion.Dependencies{Logs: completion.SnapshotLogWriter(repo), Observe: gatewaytelemetry.ObserveCompletion}, completion.RecorderOptions{}).WriteUsage(ctx, querycache.Clone(usageLog), logKey)
}
