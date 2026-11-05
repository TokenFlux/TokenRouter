package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// batch_image_public.go constructs batch image submission services with
// provider, model, subscription, and funding callbacks.
// Batch image submissions use the application's provider registry and funding
// services. Subscription lookup and rate selection receive the job owner's
// billing identity, while model tracing records the client name and group
// mapping used for submission.

// provideBatchPublic constructs batch submission dependencies and owner-aware
// subscription callbacks. A nil cfg leaves PublicOptions unset for the batch
// service to interpret; optional pricing and authentication invalidation are
// bound when supplied, while registry and subscription storage remain required
// by their callbacks.
func provideBatchPublic(attributes *routing.ModelAttributeService, repo batchimage.BatchImageRepository, providers *providerpostgres.ProviderStore, modelConfigs *routing.PricingConfigService, groups routing.GroupRepository, rates billing.UserGroupRateRepository, queue batchimage.BatchImageQueue, pricing *batchimage.Pricing, funds *billing.Funds, subscriptions *billingpostgres.SettlementStore, auth apikey.APIKeyAuthCacheInvalidator, cfg *config.Config, registry *batchimage.Registry[batchprovider.BatchImageProvider]) *batchimage.Public {
	core := &batchimage.Public{
		Attributes: attributes,
		Now:        time.Now,
		Repo:       repo,
		ProviderRepo: &batchprovider.Candidates{
			Source:       providers,
			Registry:     registry,
			ObserveModel: modeltrace.RegisterStage,
		},
		GroupRepo:         batchPricingGroups{groups, modelConfigs},
		UserGroupRateRepo: rates,
		Queue:             queue,
		Pricing:           pricing,
		Funding:           batchimage.Funding{Store: funds, Observe: creativeObserve},
		Observe:           creativeObserve,
	}
	if modelConfigs != nil {
		core.PricingConfigService = modelConfigs
	}
	if cfg != nil {
		c := cfg.BatchImage
		core.Options = batchimage.PublicOptions{
			StaleActiveAfterSeconds:       c.Queue.StaleActiveAfterSeconds,
			MaxItemsPerJobDefault:         c.MaxItemsPerJob,
			MaxOutputImagesPerJob:         c.MaxOutputImagesPerJob,
			MaxOutputImagesPerItem:        c.MaxOutputImagesPerItem,
			MaxPromptCharsPerItem:         c.MaxPromptBytesPerItem,
			MaxReferenceImagesPerJob:      c.MaxReferenceImagesPerJob,
			MaxReferenceInlineBytesPerJob: c.MaxReferenceInlineBytesPerJob,
			DefaultResponseMimeType:       c.DefaultResponseMimeType,
			DefaultImageSize:              c.DefaultImageSize,
		}
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
