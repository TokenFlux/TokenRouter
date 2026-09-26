package app

import (
	"context"
	"time"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// provideS13BatchPublic 直接绑定原生提交用例，共享任务、账号、资金和模型配置读取实例。
func provideS13BatchPublic(repo batchimage.BatchImageRepository, accounts *accountpostgres.AccountStore, modelConfigs *routing.PricingConfigService, groups routing.GroupRepository, rates billing.UserGroupRateRepository, queue batchimage.BatchImageQueue, pricing *batchimage.Pricing, funds *billing.Funds, subscriptions *billingpostgres.SettlementStore, auth apikey.APIKeyAuthCacheInvalidator, cfg *config.Config, registry *batchimage.Registry[batchprovider.BatchImageProvider]) *batchimage.Public {
	core := &batchimage.Public{Now: time.Now, Repo: repo, AccountRepo: &batchprovider.Candidates{Source: accounts, Registry: registry, ObserveModel: modeltrace.RegisterStage}, GroupRepo: batchPricingGroups{groups}, UserGroupRateRepo: rates, Queue: queue, Pricing: pricing, Funding: batchimage.Funding{Store: funds, Observe: creativeObserve}, Observe: creativeObserve}
	if modelConfigs != nil {
		core.PricingConfigService = modelConfigs
	}
	if cfg != nil {
		c := cfg.BatchImage
		core.Options = batchimage.PublicOptions{Enabled: c.Enabled, StaleActiveAfterSeconds: c.StaleActiveAfterSeconds, MaxItemsPerJobDefault: c.MaxItemsPerJobDefault, MaxOutputImagesPerJob: c.MaxOutputImagesPerJob, MaxOutputImagesPerItem: c.MaxOutputImagesPerItem, MaxPromptCharsPerItem: c.MaxPromptCharsPerItem, MaxReferenceImagesPerJob: c.MaxReferenceImagesPerJob, MaxReferenceInlineBytesPerJob: c.MaxReferenceInlineBytesPerJob, DefaultResponseMimeType: c.DefaultResponseMimeType, DefaultImageSize: c.DefaultImageSize}
	}
	core.ProviderExists = func(name string) bool {
		selected, ok := registry.Get(name)
		return ok && selected != nil
	}
	if auth != nil {
		core.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	core.ClientModel = func(ctx context.Context) string {
		value, _ := ctx.Value(telemetry.ClientModel).(string)
		return value
	}
	core.WithModelTrace = func(ctx context.Context, m routing.GroupMappingResult, requested string) routing.GroupMappingResult {
		return modeltrace.WithGroupRedirect(m, ctx, requested)
	}
	core.RegisterModel = modeltrace.RegisterStage
	core.AutoSubscription = func(ctx context.Context, userID int64, groupID *int64) *billing.UserSubscription {
		return completion.ResolveSubscription(ctx, nil, subscriptions, userID, groupID)
	}
	core.PreferredSubscription = func(ctx context.Context, userID, subscriptionID int64, groupID *int64) *billing.UserSubscription {
		return billing.ResolvePreferredSubscription(ctx, subscriptions, userID, subscriptionID, groupID)
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
