// 旧实体和运行配置只在兼容边界投影，不复制批量任务规则或状态。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

func (p batchBoundProvider) Name() string { return p.provider.Name() }
func batchGroupProjection(g *Group) *batchimage.GroupView {
	if g == nil {
		return nil
	}
	return &batchimage.GroupView{ID: g.ID, Platform: g.Platform, AllowBatchImageGeneration: g.AllowBatchImageGeneration, RateMultiplier: g.RateMultiplier, BatchImageDiscountMultiplier: g.BatchImageDiscountMultiplier, BatchImageHoldMultiplier: g.BatchImageHoldMultiplier, Price: *projectPriceGroup(g)}
}
func batchGroupFromProjection(g *batchimage.GroupView) *Group {
	if g == nil {
		return nil
	}
	return &Group{ID: g.ID, Platform: g.Platform, AllowBatchImageGeneration: g.AllowBatchImageGeneration, RateMultiplier: g.RateMultiplier, BatchImageDiscountMultiplier: g.BatchImageDiscountMultiplier, BatchImageHoldMultiplier: g.BatchImageHoldMultiplier, ModelPricing: g.Price.ModelPricing, LongContextPricingEnabled: g.Price.LongContextPricingEnabled}
}

type batchGroupReader struct {
	BatchImageGroupPricingRepository
}

func (r batchGroupReader) GetByIDLite(ctx context.Context, id int64) (*batchimage.GroupView, error) {
	v, err := r.BatchImageGroupPricingRepository.GetByIDLite(ctx, id)
	return batchGroupProjection(v), err
}

type batchPriceReader struct{ BatchImagePricingResolver }

func (r batchPriceReader) BatchImageUnitPrice(ctx context.Context, v batchimage.BatchImagePriceInput) (float64, error) {
	return r.BatchImagePricingResolver.BatchImageUnitPrice(ctx, BatchImagePriceInput{Model: v.Model, GroupID: v.GroupID, Group: batchGroupFromProjection(v.Group), ImageSize: v.ImageSize})
}

type batchChannelReader struct{ *ChannelService }

func (r batchChannelReader) ResolveChannelMapping(ctx context.Context, g int64, m string) routing.ChannelMappingResult {
	return routing.ChannelMappingResult(r.ChannelService.ResolveChannelMapping(ctx, g, m))
}
func batchCandidateRules(a *Account) *batchimage.Candidate {
	if a == nil {
		return nil
	}
	return &batchimage.Candidate{ID: a.ID, Priority: a.Priority, CandidateRules: a}
}
func (s *BatchImagePublicService) batchCandidate(a *Account) *batchimage.Candidate {
	out := batchCandidateRules(a)
	if out == nil {
		return nil
	}
	out.ProtocolEnabled = func() bool { _, ok := ResolveProtocolRoute(a, nil, domain.ProtocolImageBatches); return ok }
	out.SupportsProvider = func(name string) bool {
		p, ok := s.ProviderRegistry.Get(name)
		return ok && p != nil && p.SupportsAccount(a)
	}
	out.Bind = func(name string) batchimage.ExecutionProvider {
		p, _ := s.ProviderRegistry.Get(name)
		return batchBoundProvider{provider: p, account: a}
	}
	out.ResolveUpstream = func(ctx context.Context, m string) string { return resolveAccountUpstreamModel(ctx, a, m) }
	return out
}

type batchAccountReader struct{ service *BatchImagePublicService }

func (r batchAccountReader) GetByID(ctx context.Context, id int64) (*batchimage.Candidate, error) {
	a, err := r.service.AccountRepo.GetByID(ctx, id)
	return r.service.batchCandidate(a), err
}
func (r batchAccountReader) project(values []Account) []batchimage.Candidate {
	out := make([]batchimage.Candidate, len(values))
	for i := range values {
		out[i] = *r.service.batchCandidate(&values[i])
	}
	return out
}
func (r batchAccountReader) ListSchedulableByPlatform(ctx context.Context, p string) ([]batchimage.Candidate, error) {
	v, err := r.service.AccountRepo.ListSchedulableByPlatform(ctx, p)
	return r.project(v), err
}
func (r batchAccountReader) ListSchedulableByGroupIDAndPlatform(ctx context.Context, g int64, p string) ([]batchimage.Candidate, error) {
	v, err := r.service.AccountRepo.ListSchedulableByGroupIDAndPlatform(ctx, g, p)
	return r.project(v), err
}
func (s *BatchImagePublicService) nativePublic() *batchimage.Public {
	if s != nil && s.Core != nil {
		return s.Core
	}
	if s == nil {
		return nil
	}
	out := &batchimage.Public{Repo: s.Repo, UserGroupRateRepo: s.UserGroupRateRepo, Queue: s.Queue, Funding: batchFundingProjection(s.BillingRepo), Observe: creativeLegacyObserve}
	if s.AccountRepo != nil {
		out.AccountRepo = batchAccountReader{s}
	}
	if s.GroupRepo != nil {
		out.GroupRepo = batchGroupReader{s.GroupRepo}
	}
	if s.Pricing != nil {
		out.Pricing = batchPriceReader{s.Pricing}
	}
	if s.ChannelService != nil {
		out.ChannelService = batchChannelReader{s.ChannelService}
	}
	if s.Config != nil {
		c := s.Config.BatchImage
		out.Options = batchimage.PublicOptions{Enabled: c.Enabled, StaleActiveAfterSeconds: c.StaleActiveAfterSeconds, MaxItemsPerJobDefault: c.MaxItemsPerJobDefault, MaxOutputImagesPerJob: c.MaxOutputImagesPerJob, MaxOutputImagesPerItem: c.MaxOutputImagesPerItem, MaxPromptCharsPerItem: c.MaxPromptCharsPerItem, MaxReferenceImagesPerJob: c.MaxReferenceImagesPerJob, MaxReferenceInlineBytesPerJob: c.MaxReferenceInlineBytesPerJob, DefaultResponseMimeType: c.DefaultResponseMimeType, DefaultImageSize: c.DefaultImageSize}
	}
	out.ProviderExists = func(name string) bool { p, ok := s.ProviderRegistry.Get(name); return ok && p != nil }
	if s.AuthCache != nil {
		out.InvalidateAuth = s.AuthCache.InvalidateAuthCacheByUserID
	}
	out.ClientModel = func(ctx context.Context) string { v, _ := ctx.Value(ctxkey.ClientModel).(string); return v }
	out.WithModelTrace = func(ctx context.Context, m routing.ChannelMappingResult, requested string) routing.ChannelMappingResult {
		return routing.ChannelMappingResult(ChannelMappingResult(m).WithAPIKeyModelRedirect(ctx, requested))
	}
	out.RegisterModel = RegisterAPIKeyModelRedirectStage
	out.AutoSubscription = func(ctx context.Context, u int64, g *int64) *billing.UserSubscription {
		return resolveUsageSubscription(ctx, nil, nil, usageSubscriptionResolverFrom(s.BillingRepo), u, g)
	}
	out.PreferredSubscription = func(ctx context.Context, u, id int64, g *int64) *billing.UserSubscription {
		return resolvePreferredUsageSubscription(ctx, usagePreferredSubscriptionResolverFrom(s.BillingRepo), u, id, g)
	}
	out.SubscriptionMultiplier = func(ctx context.Context, owner BatchImageOwner, g *batchimage.GroupView, fallback float64, sub *billing.UserSubscription) float64 {
		return resolveUsageRateMultiplier(ctx, owner.EffectiveBillingUserID(), owner.GroupID, batchGroupFromProjection(g), fallback, sub, nil)
	}
	return out
}
