//go:build unit

package batchimage_test

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// newBatchProcessorFixture 仅装配原生处理器及当前跨任务夹具的数据端口。
func newBatchProcessorFixture(repo batchimage.BatchImageRepository, registry *batchimage.Registry[batchprovider.BatchImageProvider], accounts batchprovider.ResultAccounts, indexer *batchimage.ResultIndexer, funds batchimage.FundingStore, auth apikey.APIKeyAuthCacheInvalidator, delay time.Duration) *batchimage.ProviderProcessor {
	core := &batchimage.ProviderProcessor{Repo: repo, Funding: nativeTaskFundingFixture(funds), DefaultRequeue: delay, Indexer: indexer}
	if registry != nil && accounts != nil {
		core.ResolveProvider = (batchprovider.ResultAccess{Registry: registry, Accounts: accounts}).Process
	}
	if auth != nil {
		core.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	return core
}

func newBatchSettlementFixture(repo batchimage.BatchImageRepository, funds batchimage.FundingStore, logs usage.UsageLogRepository, prices batchimage.ImagePricer, auth apikey.APIKeyAuthCacheInvalidator, cfg *config.Config) *batchimage.Settlement {
	core := &batchimage.Settlement{Repo: repo, Funding: nativeTaskFundingFixture(funds)}
	if cfg != nil {
		core.Retention = time.Duration(cfg.BatchImage.OutputRetentionAfterTerminalHours) * time.Hour
	}
	if prices != nil {
		core.Quote = func(ctx context.Context, model string, group *int64, size string) (float64, error) {
			return prices.BatchImageUnitPrice(ctx, batchimage.BatchImagePriceInput{Model: model, GroupID: group, ImageSize: size})
		}
	}
	if auth != nil {
		core.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	if logs != nil {
		core.RecordUsage = func(ctx context.Context, row *usage.UsageLog) {
			completion.NewRecorder(completion.Dependencies{Logs: completion.SnapshotLogWriter(logs), Observe: func(component, message string) { logging.LegacyPrintf(component, "%s", message) }}, completion.RecorderOptions{}).WriteUsage(ctx, querycache.Clone(row), "service.batch_image_settlement")
		}
	}
	return core
}

// nativeTaskFundingFixture 只绑定测试资金端口，不复制预占或结算规则。
func nativeTaskFundingFixture(store batchimage.FundingStore) batchimage.Funding {
	return batchimage.Funding{Store: store}
}
