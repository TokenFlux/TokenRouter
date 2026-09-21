package app

import (
	"context"
	"time"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// provideS13BatchRuntime 直接绑定唯一资金、处理和恢复实例，保留原四条运行循环。
func provideS13BatchRuntime(repo batchimage.BatchImageRepository, accounts *accountpostgres.AccountStore, queue batchimage.BatchImageQueue, funds *billing.Funds, logs usage.UsageLogRepository, pricing *batchimage.Pricing, auth apikey.APIKeyAuthCacheInvalidator, cfg *config.Config, registry *batchimage.Registry[batchprovider.BatchImageProvider]) *batchimage.Runtime {
	funding := batchimage.Funding{Store: funds, Observe: creativeObserve}
	processor := &batchimage.ProviderProcessor{Repo: repo, Funding: funding, Observe: creativeObserve, ResolveProvider: (batchprovider.ResultAccess{Registry: registry, Accounts: accounts}).Process}
	settlement := &batchimage.Settlement{Repo: repo, Funding: funding, Observe: creativeObserve}
	if cfg != nil {
		settlement.Retention = time.Duration(cfg.BatchImage.OutputRetentionAfterTerminalHours) * time.Hour
	}
	if pricing != nil {
		settlement.Quote = func(ctx context.Context, model string, group *int64, size string) (float64, error) {
			return pricing.BatchImageUnitPrice(ctx, batchimage.BatchImagePriceInput{Model: model, GroupID: group, ImageSize: size})
		}
	}
	if logs != nil {
		recorder := completion.NewRecorder(completion.Dependencies{Logs: completion.SnapshotLogWriter(logs), Observe: func(component, message string) { logging.LegacyPrintf(component, "%s", message) }}, completion.RecorderOptions{})
		settlement.RecordUsage = func(ctx context.Context, row *usage.UsageLog) {
			recorder.WriteUsage(ctx, querycache.Clone(row), "service.batch_image_settlement")
		}
	}
	options := batchWorkerOptions(cfg)
	options.Observe = creativeObserve
	recovery := &batchimage.BillingRecovery{Repo: repo, Funding: funding, Queue: queue, StaleAfter: options.StaleActiveAfter, Limit: options.RecoverLimit, Observe: creativeObserve}
	if auth != nil {
		processor.InvalidateAuth = auth.InvalidateAuthCacheByUserID
		settlement.InvalidateAuth = auth.InvalidateAuthCacheByUserID
		recovery.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	worker := batchimage.NewBatchImageWorker(queue, &batchimage.PipelineProcessor{ProviderProcessor: processor, SettlementService: settlement}, options)
	return batchimage.NewWorkerRuntime(worker, recovery, cfg != nil && cfg.BatchImage.QueueEnabled)
}

// batchWorkerOptions 只投影启动配置，默认值和校验由任务模块维护。
func batchWorkerOptions(cfg *config.Config) batchimage.BatchImageWorkerOptions {
	if cfg == nil {
		return batchimage.NormalizeBatchImageWorkerOptions(batchimage.BatchImageWorkerOptions{})
	}
	return batchimage.NormalizeBatchImageWorkerOptions(batchimage.BatchImageWorkerOptions{
		JobLockTTL:          time.Duration(cfg.BatchImage.JobLockTTLSeconds) * time.Second,
		LockConflictDelay:   time.Duration(cfg.BatchImage.LockConflictDelaySeconds) * time.Second,
		DefaultRequeueDelay: time.Duration(cfg.BatchImage.DefaultRequeueDelaySeconds) * time.Second,
		ErrorRetryDelay:     time.Duration(cfg.BatchImage.ErrorRetryDelaySeconds) * time.Second,
		DelayedPollInterval: time.Duration(cfg.BatchImage.DelayedMoverIntervalSeconds) * time.Second,
		RecoveryInterval:    time.Duration(cfg.BatchImage.RecoveryIntervalSeconds) * time.Second,
		StaleActiveAfter:    time.Duration(cfg.BatchImage.StaleActiveAfterSeconds) * time.Second,
		DelayedMoveLimit:    cfg.BatchImage.DelayedMoveLimit,
		RecoverLimit:        cfg.BatchImage.RecoverLimit,
	})
}
