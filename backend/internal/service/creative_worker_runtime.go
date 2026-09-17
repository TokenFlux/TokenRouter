// 旧构造只投影运行参数，生命周期实现由 creative 唯一持有。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
)

const DefaultCreativeWorkerCount = creative.DefaultCreativeWorkerCount

type CreativeWorkerStatus = creative.CreativeWorkerStatus
type CreativeWorkerRuntime = creative.CreativeWorkerRuntime

func NewCreativeWorkerRuntime(worker *CreativeRunWorker, cfg *config.Config, settingServices ...*SettingService) *CreativeWorkerRuntime {
	opts := creative.RuntimeOptions{Enabled: cfg != nil && cfg.Creative.QueueEnabled}
	if worker != nil && worker.service != nil {
		opts.Outbox = worker.service.RunCreativeOutboxReconciler
		opts.Transient = worker.service.RunCreativeTransientReconciler
	}
	if len(settingServices) > 0 && settingServices[0] != nil {
		opts.WorkerCount = func(ctx context.Context) int {
			v, err := settingServices[0].GetAllSettings(ctx)
			if err == nil && v != nil {
				return v.CreativeWorkerCount
			}
			return 0
		}
	}
	return creative.NewCreativeWorkerRuntime(worker, opts)
}

// ProvideCreativeWorkerRuntime 只组装 worker；启动由 app 生命周期统一执行。
func ProvideCreativeWorkerRuntime(
	repo CreativeRunRepository,
	store CreativeTransientStore,
	queue CreativeRunQueue,
	executor CreativeRunExecutor,
	creativeService *CreativePublicService,
	concurrencyService *ConcurrencyService,
	settingService *SettingService,
	cfg *config.Config,
) *CreativeWorkerRuntime {
	worker := NewCreativeRunWorker(queue, repo, store, executor, creativeService, NewCreativeWorkerOptionsFromConfig(cfg), concurrencyService)
	runtime := NewCreativeWorkerRuntime(worker, cfg, settingService)
	return runtime
}
