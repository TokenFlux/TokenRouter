//go:build unit

package batchimage_test

import (
	"context"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// 测试源只保存旧交叉契约的输入，所有候选与资金规则使用原生模块。
type batchAccountsFixtureSource interface {
	GetByID(context.Context, int64) (*accountcore.Record, error)
	ListSchedulableByPlatform(context.Context, string) ([]accountcore.Record, error)
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]accountcore.Record, error)
}
type batchGroupFixtureSource interface {
	GetByIDLite(context.Context, int64) (*routing.Group, error)
}

type batchAccountFixture struct {
	source   batchAccountsFixtureSource
	registry *batchimage.Registry[batchprovider.BatchImageProvider]
}

func (r *batchAccountFixture) project(value *accountcore.Record) *batchimage.Candidate {
	return (&batchprovider.Candidates{Registry: r.registry, ObserveModel: modeltrace.RegisterStage}).Project(accountcore.CloneRecord(value))
}
func (r *batchAccountFixture) GetByID(ctx context.Context, id int64) (*batchimage.Candidate, error) {
	v, err := r.source.GetByID(ctx, id)
	return r.project(v), err
}
func (r *batchAccountFixture) values(rows []accountcore.Record) []batchimage.Candidate {
	out := make([]batchimage.Candidate, len(rows))
	for i := range rows {
		out[i] = *r.project(&rows[i])
	}
	return out
}
func (r *batchAccountFixture) ListSchedulableByPlatform(ctx context.Context, p string) ([]batchimage.Candidate, error) {
	v, err := r.source.ListSchedulableByPlatform(ctx, p)
	return r.values(v), err
}
func (r *batchAccountFixture) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, p string) ([]batchimage.Candidate, error) {
	v, err := r.source.ListSchedulableByGroupIDAndPlatform(ctx, id, p)
	return r.values(v), err
}
func rebindBatchFixtureAccounts(core *batchimage.Public, source batchAccountsFixtureSource) batchimage.AccountReader {
	return &batchAccountFixture{source: source, registry: testassert.MustType[*batchAccountFixture](core.AccountRepo).registry}
}

type batchGroupReader struct{ source batchGroupFixtureSource }

func (r batchGroupReader) GetByIDLite(ctx context.Context, id int64) (*batchimage.GroupView, error) {
	v, err := r.source.GetByIDLite(ctx, id)
	return batchGroupProjection(v), err
}
func batchGroupProjection(v *routing.Group) *batchimage.GroupView {
	if v == nil {
		return nil
	}
	return &batchimage.GroupView{ID: v.ID, Platform: v.Platform, AllowBatchImageGeneration: v.AllowBatchImageGeneration, RateMultiplier: v.RateMultiplier, BatchImageDiscountMultiplier: v.BatchImageDiscountMultiplier, BatchImageHoldMultiplier: v.BatchImageHoldMultiplier, Price: billing.PriceGroup{ModelPricing: v.ModelPricing, LongContextPricingEnabled: v.LongContextPricingEnabled}}
}

func taskFixtureBilling(core *batchimage.Public) batchimage.FundingStore { return core.Funding.Store }

// newBatchPublicFixture 只构造端口与选项；测试修改的资金替身仍在调用时读取。
func newBatchPublicFixture(repo batchimage.BatchImageRepository, accounts batchAccountsFixtureSource, channels *routing.ChannelService, groups batchGroupFixtureSource, rates batchimage.BatchImageUserGroupRateRepository, queue batchimage.BatchImageQueue, registry *batchimage.Registry[batchprovider.BatchImageProvider], prices batchimage.ImagePricer, funds batchimage.FundingStore, auth apikey.APIKeyAuthCacheInvalidator, cfg *config.Config) *batchimage.Public {
	core := &batchimage.Public{Repo: repo, UserGroupRateRepo: rates, Queue: queue, Pricing: prices, Funding: nativeTaskFundingFixture(funds), Observe: resultObserve}
	if accounts != nil {
		core.AccountRepo = &batchAccountFixture{source: accounts, registry: registry}
	}
	if groups != nil {
		core.GroupRepo = batchGroupReader{groups}
	}
	if channels != nil {
		core.ChannelService = channels
	}
	if cfg != nil {
		c := cfg.BatchImage
		core.Options = batchimage.PublicOptions{Enabled: c.Enabled, StaleActiveAfterSeconds: c.StaleActiveAfterSeconds, MaxItemsPerJobDefault: c.MaxItemsPerJobDefault, MaxOutputImagesPerJob: c.MaxOutputImagesPerJob, MaxOutputImagesPerItem: c.MaxOutputImagesPerItem, MaxPromptCharsPerItem: c.MaxPromptCharsPerItem, MaxReferenceImagesPerJob: c.MaxReferenceImagesPerJob, MaxReferenceInlineBytesPerJob: c.MaxReferenceInlineBytesPerJob, DefaultResponseMimeType: c.DefaultResponseMimeType, DefaultImageSize: c.DefaultImageSize}
	}
	core.ProviderExists = func(name string) bool {
		value, ok := registry.Get(name)
		return ok && value != nil
	}
	if auth != nil {
		core.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	core.ClientModel = func(ctx context.Context) string {
		v, _ := ctx.Value(telemetry.ClientModel).(string)
		return v
	}
	core.WithModelTrace = func(ctx context.Context, m routing.ChannelMappingResult, requested string) routing.ChannelMappingResult {
		return modeltrace.WithChannelRedirect(m, ctx, requested)
	}
	core.RegisterModel = modeltrace.RegisterStage
	core.AutoSubscription = func(ctx context.Context, userID int64, groupID *int64) *billing.UserSubscription {
		reader, _ := taskFixtureBilling(core).(completion.SubscriptionReader)
		return completion.ResolveSubscription(ctx, nil, reader, userID, groupID)
	}
	core.PreferredSubscription = func(ctx context.Context, userID, id int64, groupID *int64) *billing.UserSubscription {
		reader, _ := taskFixtureBilling(core).(billing.PreferredSubscriptionReader)
		return billing.ResolvePreferredSubscription(ctx, reader, userID, id, groupID)
	}
	core.SubscriptionMultiplier = func(ctx context.Context, owner batchimage.BatchImageOwner, group *batchimage.GroupView, fallback float64, sub *billing.UserSubscription) float64 {
		var projected *completion.GroupSnapshot
		if group != nil {
			projected = &completion.GroupSnapshot{ID: group.ID, RateMultiplier: group.RateMultiplier}
		}
		return completion.ResolveUsageRateMultiplier(ctx, owner.EffectiveBillingUserID(), owner.GroupID, projected, fallback, sub, nil)
	}
	return core
}
