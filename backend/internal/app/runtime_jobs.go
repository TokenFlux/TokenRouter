package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/provider"
)

type jobsRuntimeReady struct{}

func provideJobsRuntime(
	batchImageCleanup *batchCleanupRuntime,
	batchImageWorker *batchimage.Runtime,
	creativeWorker *creative.CreativeWorkerRuntime,
	cnUsageMonitor *provider.CNUsageMonitor,
	manager *lifecycle.Manager,
) *jobsRuntimeReady {
	manager.Register(lifecycle.Hook{Name: "BatchImageCleanupService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if batchImageCleanup != nil {
			batchImageCleanup.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if batchImageCleanup != nil {
			return batchImageCleanup.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "BatchImageWorkerRuntime", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if batchImageWorker != nil {
			batchImageWorker.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if batchImageWorker != nil {
			return batchImageWorker.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "CreativeWorkerRuntime", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if creativeWorker != nil {
			creativeWorker.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if creativeWorker != nil {
			return creativeWorker.StopContext(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "CNProviderBalanceCheckService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if cnUsageMonitor != nil {
			return cnUsageMonitor.StartContext(ctx)
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if cnUsageMonitor != nil {
			return cnUsageMonitor.StopContext(ctx)
		}
		return nil
	}})
	return &jobsRuntimeReady{}
}
