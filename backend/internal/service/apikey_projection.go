// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	ctxkey "github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
)

// APIKeyGroupView 是旧分组到认证/展示只读投影的唯一入口。
func APIKeyGroupView(g *Group) *apikey.Group {
	if g == nil {
		return nil
	}
	return &apikey.Group{ID: g.ID, Name: g.Name, Description: g.Description, Platform: g.Platform, SchedulerType: g.SchedulerType, AdvancedSchedulerOverrides: g.AdvancedSchedulerOverrides, DisplayBrand: g.DisplayBrand, RateMultiplier: g.RateMultiplier, PeakRateEnabled: g.PeakRateEnabled, PeakStart: g.PeakStart, PeakEnd: g.PeakEnd, PeakRateMultiplier: g.PeakRateMultiplier, IsExclusive: g.IsExclusive, IsDefault: g.IsDefault, Status: g.Status, Hydrated: g.Hydrated, DuplicateOperationID: g.DuplicateOperationID, SessionIsolationEnabled: g.SessionIsolationEnabled, AllowImageGeneration: g.AllowImageGeneration, AllowBatchImageGeneration: g.AllowBatchImageGeneration, BatchImageDiscountMultiplier: g.BatchImageDiscountMultiplier, BatchImageHoldMultiplier: g.BatchImageHoldMultiplier, WebSearchPricePerCall: g.WebSearchPricePerCall, SearchPricePer1k: g.SearchPricePer1k, AudioRealtimePricePerMin: g.AudioRealtimePricePerMin, AudioTTSPricePerMillionChars: g.AudioTTSPricePerMillionChars, AudioSTTPricePerHour: g.AudioSTTPricePerHour, LongContextPricingEnabled: g.LongContextPricingEnabled, ModelPricing: g.ModelPricing, ClaudeCodeOnly: g.ClaudeCodeOnly, FallbackGroupID: g.FallbackGroupID, FallbackGroupIDOnInvalidRequest: g.FallbackGroupIDOnInvalidRequest, UnavailableFallbackGroupID: g.UnavailableFallbackGroupID, ModelRouting: g.ModelRouting, ModelRoutingEnabled: g.ModelRoutingEnabled, MCPXMLInject: g.MCPXMLInject, SupportedModelScopes: g.SupportedModelScopes, SortOrder: g.SortOrder, AllowedProtocols: g.AllowedProtocols, ProtocolFallbacks: g.ProtocolFallbacks, ResponsesImagePolicy: g.ResponsesImagePolicy, AllowMessagesDispatch: g.AllowMessagesDispatch, AllowLive: g.AllowLive, ForceOpenAIFast: g.ForceOpenAIFast, OpenAIFastPolicy: g.OpenAIFastPolicy, FreeOpenAIFast: g.FreeOpenAIFast, RequireOAuthOnly: g.RequireOAuthOnly, RequirePrivacySet: g.RequirePrivacySet, DefaultMappedModel: g.DefaultMappedModel, MessagesDispatchModelConfig: g.MessagesDispatchModelConfig, ModelsListConfig: g.ModelsListConfig, AvailabilityProbeConfig: g.AvailabilityProbeConfig, RPMLimit: g.RPMLimit, MaxReasoningEffort: g.MaxReasoningEffort, MaxReasoningEffortOverLimit: g.MaxReasoningEffortOverLimit, ReasoningEffortMappings: g.ReasoningEffortMappings, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt, AccountCount: g.AccountCount, ActiveAccountCount: g.ActiveAccountCount, RateLimitedAccountCount: g.RateLimitedAccountCount}
}
func GroupFromAPIKeyView(g *apikey.Group) *Group {
	if g == nil {
		return nil
	}
	return &Group{ID: g.ID, Name: g.Name, Description: g.Description, Platform: g.Platform, SchedulerType: g.SchedulerType, AdvancedSchedulerOverrides: g.AdvancedSchedulerOverrides, DisplayBrand: g.DisplayBrand, RateMultiplier: g.RateMultiplier, PeakRateEnabled: g.PeakRateEnabled, PeakStart: g.PeakStart, PeakEnd: g.PeakEnd, PeakRateMultiplier: g.PeakRateMultiplier, IsExclusive: g.IsExclusive, IsDefault: g.IsDefault, Status: g.Status, Hydrated: g.Hydrated, DuplicateOperationID: g.DuplicateOperationID, SessionIsolationEnabled: g.SessionIsolationEnabled, AllowImageGeneration: g.AllowImageGeneration, AllowBatchImageGeneration: g.AllowBatchImageGeneration, BatchImageDiscountMultiplier: g.BatchImageDiscountMultiplier, BatchImageHoldMultiplier: g.BatchImageHoldMultiplier, WebSearchPricePerCall: g.WebSearchPricePerCall, SearchPricePer1k: g.SearchPricePer1k, AudioRealtimePricePerMin: g.AudioRealtimePricePerMin, AudioTTSPricePerMillionChars: g.AudioTTSPricePerMillionChars, AudioSTTPricePerHour: g.AudioSTTPricePerHour, LongContextPricingEnabled: g.LongContextPricingEnabled, ModelPricing: g.ModelPricing, ClaudeCodeOnly: g.ClaudeCodeOnly, FallbackGroupID: g.FallbackGroupID, FallbackGroupIDOnInvalidRequest: g.FallbackGroupIDOnInvalidRequest, UnavailableFallbackGroupID: g.UnavailableFallbackGroupID, ModelRouting: g.ModelRouting, ModelRoutingEnabled: g.ModelRoutingEnabled, MCPXMLInject: g.MCPXMLInject, SupportedModelScopes: g.SupportedModelScopes, SortOrder: g.SortOrder, AllowedProtocols: g.AllowedProtocols, ProtocolFallbacks: g.ProtocolFallbacks, ResponsesImagePolicy: g.ResponsesImagePolicy, AllowMessagesDispatch: g.AllowMessagesDispatch, AllowLive: g.AllowLive, ForceOpenAIFast: g.ForceOpenAIFast, OpenAIFastPolicy: g.OpenAIFastPolicy, FreeOpenAIFast: g.FreeOpenAIFast, RequireOAuthOnly: g.RequireOAuthOnly, RequirePrivacySet: g.RequirePrivacySet, DefaultMappedModel: g.DefaultMappedModel, MessagesDispatchModelConfig: g.MessagesDispatchModelConfig, ModelsListConfig: g.ModelsListConfig, AvailabilityProbeConfig: g.AvailabilityProbeConfig, RPMLimit: g.RPMLimit, MaxReasoningEffort: g.MaxReasoningEffort, MaxReasoningEffortOverLimit: g.MaxReasoningEffortOverLimit, ReasoningEffortMappings: g.ReasoningEffortMappings, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt, AccountCount: g.AccountCount, ActiveAccountCount: g.ActiveAccountCount, RateLimitedAccountCount: g.RateLimitedAccountCount}
}
func APIKeyCompositeView(g APIKeyCompositeGroup) apikey.APIKeyCompositeGroup {
	return apikey.APIKeyCompositeGroup{ID: g.ID, APIKeyID: g.APIKeyID, GroupID: g.GroupID, Prefix: g.Prefix, NormalizedPrefix: g.NormalizedPrefix, SortOrder: g.SortOrder, UserGroupRPMOverride: g.UserGroupRPMOverride, Group: APIKeyGroupView(g.Group)}
}
func APIKeyCompositeFromView(g apikey.APIKeyCompositeGroup) APIKeyCompositeGroup {
	return APIKeyCompositeGroup{ID: g.ID, APIKeyID: g.APIKeyID, GroupID: g.GroupID, Prefix: g.Prefix, NormalizedPrefix: g.NormalizedPrefix, SortOrder: g.SortOrder, UserGroupRPMOverride: g.UserGroupRPMOverride, Group: GroupFromAPIKeyView(g.Group)}
}
func APIKeyView(k *APIKey) *apikey.APIKey {
	if k == nil {
		return nil
	}
	out := &apikey.APIKey{ID: k.ID, UserID: k.UserID, TeamID: k.TeamID, TeamOwnerDisabled: k.TeamOwnerDisabled, Key: k.Key, Name: k.Name, GroupID: k.GroupID, IsComposite: k.IsComposite, Status: k.Status, FastModePolicy: k.FastModePolicy, BillingMode: k.BillingMode, PreferredSubscriptionID: k.PreferredSubscriptionID, ModelMapping: k.ModelMapping, IPWhitelist: k.IPWhitelist, IPBlacklist: k.IPBlacklist, CompiledIPWhitelist: k.CompiledIPWhitelist, CompiledIPBlacklist: k.CompiledIPBlacklist, LastUsedAt: k.LastUsedAt, LastUsedIP: k.LastUsedIP, CreatedAt: k.CreatedAt, UpdatedAt: k.UpdatedAt, FallbackToDefaultGroupWhenUnavailable: k.FallbackToDefaultGroupWhenUnavailable, CurrentConcurrency: k.CurrentConcurrency, ManagedBy: k.ManagedBy, Quota: k.Quota, QuotaUsed: k.QuotaUsed, ExpiresAt: k.ExpiresAt, RateLimit5h: k.RateLimit5h, RateLimit1d: k.RateLimit1d, RateLimit7d: k.RateLimit7d, Usage5h: k.Usage5h, Usage1d: k.Usage1d, Usage7d: k.Usage7d, Window5hStart: k.Window5hStart, Window1dStart: k.Window1dStart, Window7dStart: k.Window7dStart, User: IdentityUser(k.User), ActorUser: IdentityUser(k.ActorUser), Team: k.Team, TeamMembership: k.TeamMembership, Group: APIKeyGroupView(k.Group)}
	if k.CompositeGroups != nil {
		out.CompositeGroups = make([]apikey.APIKeyCompositeGroup, len(k.CompositeGroups))
		for i, v := range k.CompositeGroups {
			out.CompositeGroups[i] = APIKeyCompositeView(v)
		}
	}
	return out
}
func APIKeyFromView(k *apikey.APIKey) *APIKey {
	if k == nil {
		return nil
	}
	out := &APIKey{ID: k.ID, UserID: k.UserID, TeamID: k.TeamID, TeamOwnerDisabled: k.TeamOwnerDisabled, Key: k.Key, Name: k.Name, GroupID: k.GroupID, IsComposite: k.IsComposite, Status: k.Status, FastModePolicy: k.FastModePolicy, BillingMode: k.BillingMode, PreferredSubscriptionID: k.PreferredSubscriptionID, ModelMapping: k.ModelMapping, IPWhitelist: k.IPWhitelist, IPBlacklist: k.IPBlacklist, CompiledIPWhitelist: k.CompiledIPWhitelist, CompiledIPBlacklist: k.CompiledIPBlacklist, LastUsedAt: k.LastUsedAt, LastUsedIP: k.LastUsedIP, CreatedAt: k.CreatedAt, UpdatedAt: k.UpdatedAt, FallbackToDefaultGroupWhenUnavailable: k.FallbackToDefaultGroupWhenUnavailable, CurrentConcurrency: k.CurrentConcurrency, ManagedBy: k.ManagedBy, Quota: k.Quota, QuotaUsed: k.QuotaUsed, ExpiresAt: k.ExpiresAt, RateLimit5h: k.RateLimit5h, RateLimit1d: k.RateLimit1d, RateLimit7d: k.RateLimit7d, Usage5h: k.Usage5h, Usage1d: k.Usage1d, Usage7d: k.Usage7d, Window5hStart: k.Window5hStart, Window1dStart: k.Window1dStart, Window7dStart: k.Window7dStart, User: UserFromIdentity(k.User), ActorUser: UserFromIdentity(k.ActorUser), Team: k.Team, TeamMembership: k.TeamMembership, Group: GroupFromAPIKeyView(k.Group)}
	if k.CompositeGroups != nil {
		out.CompositeGroups = make([]APIKeyCompositeGroup, len(k.CompositeGroups))
		for i, v := range k.CompositeGroups {
			out.CompositeGroups[i] = APIKeyCompositeFromView(v)
		}
	}
	return out
}
func ApplyAPIKeyView(dst *APIKey, src *apikey.APIKey) {
	if dst != nil && src != nil {
		*dst = *APIKeyFromView(src)
	}
}
func keyGroupsFromView(values []apikey.Group) []Group {
	if values == nil {
		return nil
	}
	out := make([]Group, len(values))
	for i := range values {
		out[i] = *GroupFromAPIKeyView(&values[i])
	}
	return out
}
func keyBindingsFromView(values []apikey.APIKeyCompositeGroup) []APIKeyCompositeGroup {
	if values == nil {
		return nil
	}
	out := make([]APIKeyCompositeGroup, len(values))
	for i, v := range values {
		out[i] = APIKeyCompositeFromView(v)
	}
	return out
}
func keyRowsFromView(values []apikey.APIKey) []APIKey {
	if values == nil {
		return nil
	}
	out := make([]APIKey, len(values))
	for i := range values {
		out[i] = *APIKeyFromView(&values[i])
	}
	return out
}

// KeyRequestContext 把旧入口已经确定的路由元数据投影给新核心。
func KeyRequestContext(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	m := apikey.RequestMetadataFromContext(ctx)
	if v, ok := ctx.Value(ctxkey.ForcePlatform).(string); ok {
		m.ForcePlatform = v
		m.ForcePlatformSet = true
	}
	if v, ok := ctx.Value(ctxkey.InboundEndpoint).(string); ok {
		m.InboundEndpoint = v
		m.InboundEndpointSet = true
	}
	return apikey.WithRequestMetadata(ctx, m)
}
func KeyOptionsFromConfig(cfg *config.Config) *apikey.Options {
	if cfg == nil {
		return nil
	}
	o := &apikey.Options{APIKeyAuth: apikey.APIKeyAuthCacheConfig{L1Size: cfg.APIKeyAuth.L1Size, L1TTLSeconds: cfg.APIKeyAuth.L1TTLSeconds, L2TTLSeconds: cfg.APIKeyAuth.L2TTLSeconds, NegativeTTLSeconds: cfg.APIKeyAuth.NegativeTTLSeconds, JitterPercent: cfg.APIKeyAuth.JitterPercent, Singleflight: cfg.APIKeyAuth.Singleflight, LookupConcurrency: cfg.APIKeyAuth.LookupConcurrency, InvalidAbuse: apikey.InvalidAuthAbuseConfig{Enabled: cfg.APIKeyAuth.InvalidAbuse.Enabled, Threshold: cfg.APIKeyAuth.InvalidAbuse.Threshold, WindowSeconds: cfg.APIKeyAuth.InvalidAbuse.WindowSeconds, BlockSeconds: cfg.APIKeyAuth.InvalidAbuse.BlockSeconds, Capacity: cfg.APIKeyAuth.InvalidAbuse.Capacity}}}
	o.Default.APIKeyPrefix = cfg.Default.APIKeyPrefix
	o.Team.Enabled = cfg.Team.Enabled
	o.GroupFastPolicy = func(raw string, force bool) string {
		return (&Group{OpenAIFastPolicy: raw, ForceOpenAIFast: force}).EffectiveOpenAIFastPolicy()
	}
	return o
}

// legacyKeyGroups 只调用旧分组能力并投影，S06 改绑后删除。
type legacyKeyGroups struct{ Repository GroupRepository }

func (p legacyKeyGroups) GetByID(ctx context.Context, id int64) (*apikey.Group, error) {
	g, e := p.Repository.GetByID(ctx, id)
	return APIKeyGroupView(g), e
}
func (p legacyKeyGroups) GetByIDLite(ctx context.Context, id int64) (*apikey.Group, error) {
	g, e := p.Repository.GetByIDLite(ctx, id)
	return APIKeyGroupView(g), e
}
func (p legacyKeyGroups) ListActive(ctx context.Context) ([]apikey.Group, error) {
	g, e := p.Repository.ListActive(ctx)
	if g == nil {
		return nil, e
	}
	out := make([]apikey.Group, len(g))
	for i := range g {
		out[i] = *APIKeyGroupView(&g[i])
	}
	return out, e
}
func (p legacyKeyGroups) FindDefault(ctx context.Context, platform string) (*apikey.Group, error) {
	g, e := findPlatformDefaultGroup(ctx, p.Repository, platform)
	return APIKeyGroupView(g), e
}

func keyBindingsToView(v []APIKeyCompositeGroup) []apikey.APIKeyCompositeGroup {
	if v == nil {
		return nil
	}
	out := make([]apikey.APIKeyCompositeGroup, len(v))
	for i, x := range v {
		out[i] = APIKeyCompositeView(x)
	}
	return out
}

func apiKeyBindingView(v *APIKeyCompositeGroup) *apikey.APIKeyCompositeGroup {
	if v == nil {
		return nil
	}
	out := APIKeyCompositeView(*v)
	return &out
}
