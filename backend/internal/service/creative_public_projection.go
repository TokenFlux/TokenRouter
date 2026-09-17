// 旧创建入口仅转换实体和参数，规则由 creative.Public 唯一执行。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
)

func creativeGroupProjection(g *Group) *creative.GroupView {
	if g == nil {
		return nil
	}
	return &creative.GroupView{ID: g.ID, Name: g.Name, Platform: g.Platform, IsExclusive: g.IsExclusive, AllowImageGeneration: g.AllowImageGeneration, Active: g.IsActive(), RateMultiplier: g.RateMultiplier, Operations: creativeOperationsForGroup(g), Price: *projectPriceGroup(g)}
}
func creativeGroupFromProjection(g *creative.GroupView) *Group {
	if g == nil {
		return nil
	}
	status := ""
	if g.Active {
		status = StatusActive
	}
	return &Group{ID: g.ID, Name: g.Name, Platform: g.Platform, IsExclusive: g.IsExclusive, AllowImageGeneration: g.AllowImageGeneration, Status: status, RateMultiplier: g.RateMultiplier, ModelPricing: g.Price.ModelPricing, LongContextPricingEnabled: g.Price.LongContextPricingEnabled}
}

type creativeUserReader struct{ CreativeUserRepository }

func (r creativeUserReader) GetByID(ctx context.Context, id int64) (creative.UserAccess, error) {
	value, err := r.CreativeUserRepository.GetByID(ctx, id)
	if value == nil {
		return nil, err
	}
	return value, err
}

type creativeGroupReader struct{ CreativeGroupRepository }

func (r creativeGroupReader) GetByIDLite(ctx context.Context, id int64) (*creative.GroupView, error) {
	v, err := r.CreativeGroupRepository.GetByIDLite(ctx, id)
	return creativeGroupProjection(v), err
}
func (r creativeGroupReader) ListActive(ctx context.Context) ([]creative.GroupView, error) {
	v, err := r.CreativeGroupRepository.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]creative.GroupView, len(v))
	for i := range v {
		out[i] = *creativeGroupProjection(&v[i])
	}
	return out, nil
}

type creativeAccountReader struct{ CreativeAccountRepository }

func (r creativeAccountReader) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, p string) ([]creative.CatalogAccount, error) {
	v, err := r.CreativeAccountRepository.ListSchedulableByGroupIDAndPlatform(ctx, id, p)
	if err != nil {
		return nil, err
	}
	out := make([]creative.CatalogAccount, len(v))
	for i := range v {
		out[i] = &v[i]
	}
	return out, nil
}

type creativeModerator struct{ service *ContentModerationService }

func (m creativeModerator) Check(ctx context.Context, v creative.ModerationInput) (*creative.ModerationDecision, error) {
	out, err := m.service.Check(ctx, ContentModerationCheckInput{RequestID: v.RequestID, UserID: v.UserID, BillingUserID: v.BillingUserID, GroupID: v.GroupID, GroupName: v.GroupName, Endpoint: v.Endpoint, Provider: v.Provider, Model: v.Model, Protocol: v.Protocol, Body: v.Body, NoMediaRetention: v.NoMediaRetention})
	if out == nil {
		return nil, err
	}
	return &creative.ModerationDecision{Allowed: out.Allowed}, err
}
func (s *CreativePublicService) nativePublic() *creative.Public {
	if s != nil && s.Core != nil {
		return s.Core
	}
	if s == nil {
		return nil
	}
	out := &creative.Public{Repo: s.Repo, UserGroupRateRepo: s.UserGroupRateRepo, Queue: s.Queue, TransientStore: s.TransientStore, Results: s.nativeResults(), Settings: s.Settings, UserNotFound: ErrUserNotFound, Observe: creativeLegacyObserve}
	if s.Config != nil {
		out.Options = creative.PublicOptions{Enabled: s.Config.Creative.Enabled, MaxPromptChars: s.Config.Creative.MaxPromptChars, MaxAssetBytes: s.Config.Creative.MaxAssetBytes, MaxTotalInputBytes: s.Config.Creative.MaxTotalInputBytes, DefaultImageSize: s.Config.Creative.DefaultImageSize}
	}
	if s.UserRepo != nil {
		out.UserRepo = creativeUserReader{s.UserRepo}
	}
	if s.GroupRepo != nil {
		out.GroupRepo = creativeGroupReader{s.GroupRepo}
	}
	if s.AccountRepo != nil {
		out.AccountRepo = creativeAccountReader{s.AccountRepo}
	}
	out.EnsureKey = func(ctx context.Context, u, g int64) (int64, error) {
		k, err := s.ensureCreativeManagedKey(ctx, u, g)
		if err != nil {
			return 0, err
		}
		return k.ID, nil
	}
	if s.PricingResolver != nil && s.PricingResolver.channelService != nil {
		out.ChannelMapping = func(ctx context.Context, id int64, model string) routing.ChannelMappingResult {
			return routing.ChannelMappingResult(s.PricingResolver.channelService.ResolveChannelMapping(ctx, id, model))
		}
	}
	out.ImageUnitPrice = func(ctx context.Context, g *creative.GroupView, m, size string) (float64, bool) {
		return s.creativeResolvedImageUnitPrice(ctx, creativeGroupFromProjection(g), m, size)
	}
	out.SubscriptionMultiplier = func(ctx context.Context, u int64, g *creative.GroupView, fallback float64) (float64, bool) {
		groupID := g.ID
		sub := resolveUsageSubscription(ctx, nil, nil, usageSubscriptionResolverFrom(s.BillingRepo), u, &groupID)
		if sub == nil {
			return 0, false
		}
		return resolveUsageRateMultiplier(ctx, u, &groupID, creativeGroupFromProjection(g), fallback, sub, nil), true
	}
	if s.Moderation != nil {
		out.Moderation = creativeModerator{s.Moderation}
	}
	out.RequestID = func(ctx context.Context) string { value, _ := ctx.Value(ctxkey.RequestID).(string); return value }
	return out
}
