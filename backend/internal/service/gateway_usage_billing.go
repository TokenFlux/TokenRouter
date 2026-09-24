package service

import (
	"context"

	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
)

func writeUsageLogBestEffort(ctx context.Context, repo usage.UsageLogRepository, usageLog *usage.UsageLog, logKey string) {
	completion.NewRecorder(completion.Dependencies{Logs: completion.SnapshotLogWriter(repo), Observe: gatewaytelemetry.ObserveCompletion}, completion.RecorderOptions{}).WriteUsage(ctx, querycache.Clone(usageLog), logKey)
}
