package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	"github.com/TokenFlux/TokenRouter/internal/requestlog"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// batch_image_worker.go constructs batch image pricing, provider processing,
// settlement, and recovery runtimes.
// Batch image pricing snapshots read a group's base rate and its effective batch
// discount and hold multipliers. Provider processing, settlement, and stale
// funding recovery receive the same Funds service; optional authentication
// invalidation is bound to each of those consumers for billing-user updates.

// batchPricingGroups combines group policy and effective multipliers for batch quotes.
type batchPricingGroups struct {
	// source reads group protocols and the base multiplier, while settings resolves
	// batch discount and hold multipliers using the same requested group ID.
	source   routing.GroupRepository       // stored group reader
	settings *routing.PricingConfigService // effective multiplier reader
}

// GetByIDLite combines a lightweight group with effective batch billing multipliers.
// A nil group is returned with the repository error; a nonnil group produces
// a view even if that error is nonnil. PricingConfigService supplies defaults
// when effective billing settings cannot be loaded.
func (r batchPricingGroups) GetByIDLite(ctx context.Context, id int64) (*batchimage.GroupView, error) {
	value, err := r.source.GetByIDLite(ctx, id)
	if value == nil {
		return nil, err
	}
	settings := r.settings.GetEffectiveBillingSettings(ctx, id)
	return &batchimage.GroupView{
		ID:                           value.ID,
		AllowBatchImageGeneration:    value.AllowsClientProtocol(protocol.ProtocolImageBatches),
		RateMultiplier:               value.RateMultiplier,
		BatchImageDiscountMultiplier: settings.BatchImageDiscountMultiplier,
		BatchImageHoldMultiplier:     settings.BatchImageHoldMultiplier,
	}, err
}

// provideBatchPricing constructs batch price resolution with the supplied group reader.
func provideBatchPricing(resolver *billing.PriceResolver, groups routing.GroupRepository, configs *routing.PricingConfigService) *batchimage.Pricing {
	return &batchimage.Pricing{Resolver: resolver, GroupRepo: batchPricingGroups{groups, configs}}
}

// batchVertexOptions extracts Vertex provider configuration and the log environment.
// Nil cfg returns zero options; the Vertex provider constructor normalizes
// location, object prefixes, environment, and endpoint strings when using them.
func batchVertexOptions(cfg *config.Config) batchprovider.VertexBatchImageProviderOptions {
	if cfg == nil {
		return batchprovider.VertexBatchImageProviderOptions{}
	}
	return batchprovider.VertexBatchImageProviderOptions{
		ProjectID:              cfg.BatchImage.Vertex.ProjectID,
		Location:               cfg.BatchImage.Vertex.Location,
		ManagedGCSBucket:       cfg.BatchImage.Vertex.ManagedGCSBucket,
		ManagedGCSPrefix:       cfg.BatchImage.Vertex.ManagedGCSPrefix,
		Environment:            cfg.Environment,
		BatchPredictionBaseURL: cfg.BatchImage.Vertex.BatchPredictionBaseURL,
		GCSBaseURL:             cfg.BatchImage.Vertex.GCSBaseURL,
	}
}

// provideBatchRuntime constructs processing, settlement, and funding recovery with
// the supplied Funds service. Nil pricing, logs, or auth leaves the corresponding
// callbacks unset; nil cfg uses normalized worker defaults for runtime
// settings. provideJobsRuntime starts the returned loops, using the supplied
// repositories, queue, and provider registry throughout execution.
func provideBatchRuntime(requests *requestlog.Service, repo batchimage.BatchImageRepository, providers *providerpostgres.ProviderStore, queue batchimage.BatchImageQueue, funds *billing.Funds, logs usage.UsageLogRepository, pricing *batchimage.Pricing, auth apikey.APIKeyAuthCacheInvalidator, cfg *config.Config, registry *batchimage.Registry[batchprovider.BatchImageProvider]) *batchimage.Runtime {
	funding := batchimage.Funding{Store: funds, Observe: creativeObserve}
	processor := &batchimage.ProviderProcessor{Repo: repo, Funding: funding, Observe: creativeObserve, ResolveProvider: (batchprovider.ResultAccess{Registry: registry, Providers: providers}).Process}
	settlement := &batchimage.Settlement{Repo: repo, Funding: funding, Observe: creativeObserve}
	if cfg != nil {
		// Terminal output retention is configured in hours for expiry calculations.
		settlement.Retention = time.Duration(cfg.BatchImage.OutputRetentionAfterTerminalHours) * time.Hour
	}
	if pricing != nil {
		settlement.Quote = func(ctx context.Context, model string, group *int64, size string) (float64, error) {
			return pricing.BatchImageUnitPrice(ctx, batchimage.BatchImagePriceInput{Model: model, GroupID: group, ImageSize: size})
		}
	}
	if logs != nil {
		recorder := completion.NewRecorder(completion.Dependencies{RequestRecords: requests.Observe, Logs: completion.SnapshotLogWriter(logs), Observe: func(component, message string) { logging.LegacyPrintf(component, "%s", message) }}, completion.RecorderOptions{})
		settlement.RecordUsage = func(ctx context.Context, row *usage.UsageLog) {
			// Clone before WriteUsage applies client-model enrichment to the row;
			// SnapshotLogWriter then supplies another snapshot for each repository write.
			recorder.WriteUsage(ctx, querycache.Clone(row), "service.batch_image_settlement")
		}
	}
	options := batchWorkerOptions(cfg)
	options.Observe = creativeObserve
	recovery := &batchimage.BillingRecovery{Repo: repo, Funding: funding, Queue: queue, StaleAfter: options.StaleActiveAfter, Limit: options.RecoverLimit, Observe: creativeObserve}
	// Processing, settlement, and stale-submit recovery can change held or
	// settled funds; their callbacks invalidate authentication data for that billing user.
	if auth != nil {
		processor.InvalidateAuth = auth.InvalidateAuthCacheByUserID
		settlement.InvalidateAuth = auth.InvalidateAuthCacheByUserID
		recovery.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	worker := batchimage.NewBatchImageWorker(queue, &batchimage.PipelineProcessor{ProviderProcessor: processor, SettlementService: settlement}, options)
	return batchimage.NewWorkerRuntime(worker, recovery, true)
}

// batchWorkerOptions converts startup settings into normalized batch worker options.
// Nil cfg selects all defaults, and nonpositive configured values use the
// normalizer's defaults. ReserveBlockTimeout is always supplied by normalization
// because this adapter does not read a reserve timeout from cfg.
func batchWorkerOptions(cfg *config.Config) batchimage.BatchImageWorkerOptions {
	if cfg == nil {
		return batchimage.NormalizeBatchImageWorkerOptions(batchimage.BatchImageWorkerOptions{})
	}
	// Lock lifetimes, queue delays, polling intervals, and stale-active limits
	// use configuration seconds and worker durations; batch limits remain counts.
	return batchimage.NormalizeBatchImageWorkerOptions(batchimage.BatchImageWorkerOptions{
		JobLockTTL:          time.Duration(cfg.BatchImage.Queue.JobLockTTLSeconds) * time.Second,
		LockConflictDelay:   time.Duration(cfg.BatchImage.Queue.LockConflictDelaySeconds) * time.Second,
		DefaultRequeueDelay: time.Duration(cfg.BatchImage.Queue.DefaultRequeueDelaySeconds) * time.Second,
		ErrorRetryDelay:     time.Duration(cfg.BatchImage.Queue.ErrorRetryDelaySeconds) * time.Second,
		DelayedPollInterval: time.Duration(cfg.BatchImage.Queue.DelayedMoverIntervalSeconds) * time.Second,
		RecoveryInterval:    time.Duration(cfg.BatchImage.Queue.RecoveryIntervalSeconds) * time.Second,
		StaleActiveAfter:    time.Duration(cfg.BatchImage.Queue.StaleActiveAfterSeconds) * time.Second,
		DelayedMoveLimit:    cfg.BatchImage.Queue.DelayedMoveLimit,
		RecoverLimit:        cfg.BatchImage.Queue.RecoverLimit,
	})
}
