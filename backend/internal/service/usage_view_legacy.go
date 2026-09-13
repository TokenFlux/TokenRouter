// 旧日志形状与 usage 的字段投影；不复制统计或写入规则。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

func UsageUserView(v *User) *usage.UserView {
	if v == nil {
		return nil
	}
	out := &usage.UserView{ID: v.ID, Email: v.Email, Username: v.Username, Role: v.Role, Balance: v.Balance, FrozenBalance: v.FrozenBalance, Concurrency: v.Concurrency, Status: v.Status, AllowedGroups: v.AllowedGroups, DisabledPublicGroups: v.DisabledPublicGroups, LastActiveAt: v.LastActiveAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, DeletedAt: v.DeletedAt, BalanceNotifyEnabled: v.BalanceNotifyEnabled, BalanceNotifyThresholdType: v.BalanceNotifyThresholdType, BalanceNotifyThreshold: v.BalanceNotifyThreshold, BalanceNotifyExtraEmails: v.BalanceNotifyExtraEmails, TotalRecharged: v.TotalRecharged, RPMLimit: v.RPMLimit, APIKeyLimit: v.APIKeyLimit}
	return querycache.Clone(out)
}
func UsageUserFromView(v *usage.UserView) *User {
	if v == nil {
		return nil
	}
	out := &User{ID: v.ID, Email: v.Email, Username: v.Username, Role: v.Role, Balance: v.Balance, FrozenBalance: v.FrozenBalance, Concurrency: v.Concurrency, Status: v.Status, AllowedGroups: v.AllowedGroups, DisabledPublicGroups: v.DisabledPublicGroups, LastActiveAt: v.LastActiveAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, DeletedAt: v.DeletedAt, BalanceNotifyEnabled: v.BalanceNotifyEnabled, BalanceNotifyThresholdType: v.BalanceNotifyThresholdType, BalanceNotifyThreshold: v.BalanceNotifyThreshold, BalanceNotifyExtraEmails: v.BalanceNotifyExtraEmails, TotalRecharged: v.TotalRecharged, RPMLimit: v.RPMLimit, APIKeyLimit: v.APIKeyLimit}
	return querycache.Clone(out)
}
func UsageKeyView(v *APIKey) *usage.KeyView {
	if v == nil {
		return nil
	}
	out := &usage.KeyView{ID: v.ID, UserID: v.UserID, TeamID: v.TeamID, TeamOwnerDisabled: v.TeamOwnerDisabled, Key: v.Key, Name: v.Name, GroupID: v.GroupID, IsComposite: v.IsComposite, Status: v.Status, FastModePolicy: v.FastModePolicy, BillingMode: v.BillingMode, PreferredSubscriptionID: v.PreferredSubscriptionID, ModelMapping: v.ModelMapping, IPWhitelist: v.IPWhitelist, IPBlacklist: v.IPBlacklist, LastUsedAt: v.LastUsedAt, LastUsedIP: v.LastUsedIP, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, FallbackToDefaultGroupWhenUnavailable: v.FallbackToDefaultGroupWhenUnavailable, CurrentConcurrency: v.CurrentConcurrency, ManagedBy: v.ManagedBy, Quota: v.Quota, QuotaUsed: v.QuotaUsed, ExpiresAt: v.ExpiresAt, RateLimit5h: v.RateLimit5h, RateLimit1d: v.RateLimit1d, RateLimit7d: v.RateLimit7d, Usage5h: v.Usage5h, Usage1d: v.Usage1d, Usage7d: v.Usage7d, Window5hStart: v.Window5hStart, Window1dStart: v.Window1dStart, Window7dStart: v.Window7dStart}
	out.Group = (*accessview.GroupConfig)(RoutingGroupView(v.Group))
	for _, g := range v.CompositeGroups {
		out.CompositeGroups = append(out.CompositeGroups, usage.KeyCompositeGroupView{ID: g.ID, APIKeyID: g.APIKeyID, GroupID: g.GroupID, Prefix: g.Prefix, NormalizedPrefix: g.NormalizedPrefix, SortOrder: g.SortOrder, UserGroupRPMOverride: g.UserGroupRPMOverride, Group: (*accessview.GroupConfig)(RoutingGroupView(g.Group))})
	}
	return querycache.Clone(out)
}
func UsageKeyFromView(v *usage.KeyView) *APIKey {
	if v == nil {
		return nil
	}
	out := &APIKey{ID: v.ID, UserID: v.UserID, TeamID: v.TeamID, TeamOwnerDisabled: v.TeamOwnerDisabled, Key: v.Key, Name: v.Name, GroupID: v.GroupID, IsComposite: v.IsComposite, Status: v.Status, FastModePolicy: v.FastModePolicy, BillingMode: v.BillingMode, PreferredSubscriptionID: v.PreferredSubscriptionID, ModelMapping: v.ModelMapping, IPWhitelist: v.IPWhitelist, IPBlacklist: v.IPBlacklist, LastUsedAt: v.LastUsedAt, LastUsedIP: v.LastUsedIP, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, FallbackToDefaultGroupWhenUnavailable: v.FallbackToDefaultGroupWhenUnavailable, CurrentConcurrency: v.CurrentConcurrency, ManagedBy: v.ManagedBy, Quota: v.Quota, QuotaUsed: v.QuotaUsed, ExpiresAt: v.ExpiresAt, RateLimit5h: v.RateLimit5h, RateLimit1d: v.RateLimit1d, RateLimit7d: v.RateLimit7d, Usage5h: v.Usage5h, Usage1d: v.Usage1d, Usage7d: v.Usage7d, Window5hStart: v.Window5hStart, Window1dStart: v.Window1dStart, Window7dStart: v.Window7dStart}
	out.Group = GroupFromRouting((*routing.Group)(v.Group))
	for _, g := range v.CompositeGroups {
		out.CompositeGroups = append(out.CompositeGroups, APIKeyCompositeGroup{ID: g.ID, APIKeyID: g.APIKeyID, GroupID: g.GroupID, Prefix: g.Prefix, NormalizedPrefix: g.NormalizedPrefix, SortOrder: g.SortOrder, UserGroupRPMOverride: g.UserGroupRPMOverride, Group: GroupFromRouting((*routing.Group)(g.Group))})
	}
	return querycache.Clone(out)
}
func UsageLogView(v *UsageLog) *usage.UsageLog {
	if v == nil {
		return nil
	}
	out := &usage.UsageLog{ID: v.ID, UserID: v.UserID, BillingUserID: v.BillingUserID, TeamID: v.TeamID, APIKeyID: v.APIKeyID, AccountID: v.AccountID, RequestID: v.RequestID, Model: v.Model, RequestedModel: v.RequestedModel, UpstreamModel: v.UpstreamModel, ChannelID: v.ChannelID, ModelMappingChain: v.ModelMappingChain, BillingTier: v.BillingTier, BillingMode: v.BillingMode, ServiceTier: v.ServiceTier, ReasoningEffort: v.ReasoningEffort, RequestedReasoningEffort: v.RequestedReasoningEffort, InboundEndpoint: v.InboundEndpoint, UpstreamEndpoint: v.UpstreamEndpoint, GroupID: v.GroupID, SubscriptionID: v.SubscriptionID, InputTokens: v.InputTokens, OutputTokens: v.OutputTokens, CacheCreationTokens: v.CacheCreationTokens, CacheReadTokens: v.CacheReadTokens, CacheCreation5mTokens: v.CacheCreation5mTokens, CacheCreation1hTokens: v.CacheCreation1hTokens, ImageInputTokens: v.ImageInputTokens, ImageInputCost: v.ImageInputCost, ImageOutputTokens: v.ImageOutputTokens, ImageOutputCost: v.ImageOutputCost, InputCost: v.InputCost, OutputCost: v.OutputCost, CacheCreationCost: v.CacheCreationCost, CacheReadCost: v.CacheReadCost, TotalCost: v.TotalCost, ActualCost: v.ActualCost, SubscriptionAmountUSD: v.SubscriptionAmountUSD, BalanceAmountUSD: v.BalanceAmountUSD, BillingAllocations: v.BillingAllocations, RateMultiplier: v.RateMultiplier, LongContextBillingApplied: v.LongContextBillingApplied, AccountRateMultiplier: v.AccountRateMultiplier, AccountStatsCost: v.AccountStatsCost, BillingType: v.BillingType, RequestType: v.RequestType, Stream: v.Stream, OpenAIWSMode: v.OpenAIWSMode, NativeCompactionV2: v.NativeCompactionV2, DurationMs: v.DurationMs, FirstTokenMs: v.FirstTokenMs, UserAgent: v.UserAgent, IPAddress: v.IPAddress, SessionID: v.SessionID, UpstreamRequestID: v.UpstreamRequestID, CacheTTLOverridden: v.CacheTTLOverridden, ImageCount: v.ImageCount, ImageSize: v.ImageSize, ImageInputSize: v.ImageInputSize, ImageOutputSize: v.ImageOutputSize, ImageSizeSource: v.ImageSizeSource, ImageSizeBreakdown: v.ImageSizeBreakdown, MediaType: v.MediaType, VideoCount: v.VideoCount, VideoResolution: v.VideoResolution, VideoDurationSeconds: v.VideoDurationSeconds, CreatedAt: v.CreatedAt}
	out.User = UsageUserView(v.User)
	out.APIKey = UsageKeyView(v.APIKey)
	out.Group = (*accessview.GroupConfig)(RoutingGroupView(v.Group))
	if v.Account != nil {
		out.Account = &usage.AccountView{ID: v.Account.ID, Name: v.Account.Name}
	}
	out.Subscription = v.Subscription
	return querycache.Clone(out)
}
func UsageLogFromView(v *usage.UsageLog) *UsageLog {
	if v == nil {
		return nil
	}
	out := &UsageLog{ID: v.ID, UserID: v.UserID, BillingUserID: v.BillingUserID, TeamID: v.TeamID, APIKeyID: v.APIKeyID, AccountID: v.AccountID, RequestID: v.RequestID, Model: v.Model, RequestedModel: v.RequestedModel, UpstreamModel: v.UpstreamModel, ChannelID: v.ChannelID, ModelMappingChain: v.ModelMappingChain, BillingTier: v.BillingTier, BillingMode: v.BillingMode, ServiceTier: v.ServiceTier, ReasoningEffort: v.ReasoningEffort, RequestedReasoningEffort: v.RequestedReasoningEffort, InboundEndpoint: v.InboundEndpoint, UpstreamEndpoint: v.UpstreamEndpoint, GroupID: v.GroupID, SubscriptionID: v.SubscriptionID, InputTokens: v.InputTokens, OutputTokens: v.OutputTokens, CacheCreationTokens: v.CacheCreationTokens, CacheReadTokens: v.CacheReadTokens, CacheCreation5mTokens: v.CacheCreation5mTokens, CacheCreation1hTokens: v.CacheCreation1hTokens, ImageInputTokens: v.ImageInputTokens, ImageInputCost: v.ImageInputCost, ImageOutputTokens: v.ImageOutputTokens, ImageOutputCost: v.ImageOutputCost, InputCost: v.InputCost, OutputCost: v.OutputCost, CacheCreationCost: v.CacheCreationCost, CacheReadCost: v.CacheReadCost, TotalCost: v.TotalCost, ActualCost: v.ActualCost, SubscriptionAmountUSD: v.SubscriptionAmountUSD, BalanceAmountUSD: v.BalanceAmountUSD, BillingAllocations: v.BillingAllocations, RateMultiplier: v.RateMultiplier, LongContextBillingApplied: v.LongContextBillingApplied, AccountRateMultiplier: v.AccountRateMultiplier, AccountStatsCost: v.AccountStatsCost, BillingType: v.BillingType, RequestType: v.RequestType, Stream: v.Stream, OpenAIWSMode: v.OpenAIWSMode, NativeCompactionV2: v.NativeCompactionV2, DurationMs: v.DurationMs, FirstTokenMs: v.FirstTokenMs, UserAgent: v.UserAgent, IPAddress: v.IPAddress, SessionID: v.SessionID, UpstreamRequestID: v.UpstreamRequestID, CacheTTLOverridden: v.CacheTTLOverridden, ImageCount: v.ImageCount, ImageSize: v.ImageSize, ImageInputSize: v.ImageInputSize, ImageOutputSize: v.ImageOutputSize, ImageSizeSource: v.ImageSizeSource, ImageSizeBreakdown: v.ImageSizeBreakdown, MediaType: v.MediaType, VideoCount: v.VideoCount, VideoResolution: v.VideoResolution, VideoDurationSeconds: v.VideoDurationSeconds, CreatedAt: v.CreatedAt}
	out.User = UsageUserFromView(v.User)
	out.APIKey = UsageKeyFromView(v.APIKey)
	out.Group = GroupFromRouting((*routing.Group)(v.Group))
	if v.Account != nil {
		out.Account = &Account{ID: v.Account.ID, Name: v.Account.Name}
	}
	out.Subscription = v.Subscription
	return querycache.Clone(out)
}
