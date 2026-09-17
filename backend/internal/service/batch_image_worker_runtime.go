// 兼容构造委托 batchimage 生命周期，S15/S16 清理。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

type BatchImageWorkerRuntime struct {
	*batchimage.Runtime
	worker          *BatchImageWorker
	billingRecovery *BatchImageBillingRecoveryService
}

func NewBatchImageWorkerRuntime(worker *BatchImageWorker, cfg *config.Config) *BatchImageWorkerRuntime {
	r := &BatchImageWorkerRuntime{worker: worker}
	enabled := worker != nil && cfg != nil && cfg.BatchImage.QueueEnabled
	var loops []func(context.Context)
	if worker != nil {
		loops = []func(context.Context){worker.Run, worker.RunDelayedMover, worker.RunStaleActiveRecovery, r.runBillingRecovery}
	}
	r.Runtime = batchimage.NewRuntime("batch image worker", enabled, loops...)
	return r
}
func ProvideBatchImageWorkerRuntime(
	repo BatchImageRepository,
	accountRepo AccountRepository,
	queue BatchImageQueue,
	billingRepo UsageBillingRepository,
	usageLogRepo UsageLogRepository,
	pricing *BatchImageModelPricingResolver,
	authCache APIKeyAuthCacheInvalidator,
	cfg *config.Config,
	registries ...*BatchImageProviderRegistry,
) *BatchImageWorkerRuntime {
	processor := &BatchImagePipelineProcessor{
		ProviderProcessor: &BatchImageProviderProcessor{
			Repo:             repo,
			ProviderRegistry: batchRegistryFromOptions(cfg, registries),
			AccountResolver:  &BatchImageAccountRepositoryResolver{Repo: accountRepo},
			BillingRepo:      billingRepo,
			AuthCache:        authCache,
		},
		SettlementService: &BatchImageSettlementService{
			Repo:         repo,
			BillingRepo:  billingRepo,
			UsageLogRepo: usageLogRepo,
			Pricing:      pricing,
			AuthCache:    authCache,
			Config:       cfg,
		},
	}
	runtime := NewBatchImageWorkerRuntime(NewBatchImageWorker(queue, processor, NewBatchImageWorkerOptionsFromConfig(cfg)), cfg)
	runtime.billingRecovery = &BatchImageBillingRecoveryService{
		Repo:       repo,
		Billing:    billingRepo,
		AuthCache:  authCache,
		Queue:      queue,
		StaleAfter: NewBatchImageWorkerOptionsFromConfig(cfg).StaleActiveAfter,
		Limit:      NewBatchImageWorkerOptionsFromConfig(cfg).RecoverLimit,
	}

	return runtime
}
func (r *BatchImageWorkerRuntime) runBillingRecovery(ctx context.Context) {
	if r == nil || r.worker == nil || r.billingRecovery == nil {
		return
	}
	interval := r.worker.Options().RecoveryInterval
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		_, _ = r.billingRecovery.ReleaseStaleUnsubmittedOnce(ctx)
		sleepOrDone(ctx, interval)
	}
}
