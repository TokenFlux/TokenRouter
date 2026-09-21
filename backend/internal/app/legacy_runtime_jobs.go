// 本文件登记旧持久任务资源；业务迁移后删除对应绑定，S16 清零。
// 启动按停止依赖的逆序排列，纯预热在消费者启动前完成。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/creative"
)

type jobsRuntimeReady struct{}

func provideJobsRuntime(
	batchImageCleanup *batchCleanupRuntime,
	batchImageWorker *batchimage.Runtime,
	creativeWorker *creative.CreativeWorkerRuntime,
	cnUsageMonitor *account.CNUsageMonitor,
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
