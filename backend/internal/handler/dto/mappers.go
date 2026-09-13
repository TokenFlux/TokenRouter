// Package dto provides data transfer objects for HTTP handlers.
package dto

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountdto "github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func UserFromServiceShallow(u *service.User) *User {
	return identitydto.UserFromIdentityShallow[APIKey](service.IdentityUser(u))
}

func UserFromService(u *service.User) *User {
	if u == nil {
		return nil
	}
	out := UserFromServiceShallow(u)
	if len(u.APIKeys) > 0 {
		out.APIKeys = make([]APIKey, 0, len(u.APIKeys))
		for i := range u.APIKeys {
			k := u.APIKeys[i]
			out.APIKeys = append(out.APIKeys, *APIKeyFromService(&k))
		}
	}
	if len(u.Subscriptions) > 0 {
		out.Subscriptions = make([]UserSubscription, 0, len(u.Subscriptions))
		for i := range u.Subscriptions {
			s := u.Subscriptions[i]
			out.Subscriptions = append(out.Subscriptions, *UserSubscriptionFromService(&s))
		}
	}
	return out
}

// UserFromServiceAdmin 将 service.User 转为管理端 DTO。
// 该 DTO 会包含管理员备注，普通用户接口不能使用。
func UserFromServiceAdmin(u *service.User) *AdminUser {
	if u == nil {
		return nil
	}
	base := UserFromService(u)
	if base == nil {
		return nil
	}
	return &AdminUser{
		User:       *base,
		Notes:      u.Notes,
		LastUsedAt: u.LastUsedAt,
		GroupRates: u.GroupRates,
	}
}

func APIKeyFromService(k *service.APIKey) *APIKey {
	return keydto.APIKeyFromKey(service.APIKeyView(k), func(g *apikey.Group) *Group { return GroupFromServiceShallow(service.GroupFromAPIKeyView(g)) })
}

func GroupFromServiceShallow(g *service.Group) *Group {
	if g == nil {
		return nil
	}
	out := groupFromServiceBase(g)
	return &out
}

func GroupFromService(g *service.Group) *Group {
	if g == nil {
		return nil
	}
	return GroupFromServiceShallow(g)
}

// GroupCapacityFromService 将分组容量快照转换为用户可见的聚合容量 DTO。
func GroupCapacityFromService(capacity *service.GroupCapacitySummary) *GroupCapacity {
	if capacity == nil {
		return nil
	}
	return &GroupCapacity{
		ConcurrencyUsed: capacity.ConcurrencyUsed,
		ConcurrencyMax:  capacity.ConcurrencyMax,
		SessionsUsed:    capacity.SessionsUsed,
		SessionsMax:     capacity.SessionsMax,
		RPMUsed:         capacity.RPMUsed,
		RPMMax:          capacity.RPMMax,
	}
}

func SubscriptionPlanFromServiceShallow(plan *service.SubscriptionPlan) *SubscriptionPlan {
	return billinghttpapi.SubscriptionPlanFromServiceShallow(plan)
}

// GroupFromServiceAdmin converts a service Group to DTO for admin users.
// It includes internal fields like model_routing and account_count.
func GroupFromServiceAdmin(g *service.Group) *AdminGroup {
	if g == nil {
		return nil
	}
	out := routingdto.AdminGroupFromRouting[AccountGroup](service.RoutingGroupView(g))
	if len(g.AccountGroups) > 0 {
		out.AccountGroups = make([]AccountGroup, 0, len(g.AccountGroups))
		for i := range g.AccountGroups {
			ag := g.AccountGroups[i]
			out.AccountGroups = append(out.AccountGroups, *AccountGroupFromService(&ag))
		}
	}
	return out
}

func groupFromServiceBase(g *service.Group) Group {
	return routingdto.GroupFromRoutingBase(service.RoutingGroupView(g))
}

func AccountFromServiceShallow(a *service.Account) *Account {
	return accountdto.AccountFromRecordShallow(service.AccountRecordView(a))
}

func AccountFromService(a *service.Account) *Account {
	return accountdto.AccountFromRecord(service.AccountRecordView(a))
}

func AccountGroupFromService(ag *service.AccountGroup) *AccountGroup {
	if ag == nil {
		return nil
	}
	return accountdto.AccountGroupFromRecord(&accountcore.GroupMembership{AccountID: ag.AccountID, GroupID: ag.GroupID, CreatedAt: ag.CreatedAt, Account: service.AccountRecordView(ag.Account), Group: (*accessview.GroupConfig)(service.RoutingGroupView(ag.Group))})
}

func ProxyFromService(p *service.Proxy) *Proxy {
	return egresshttp.ProxyFromService(p)
}

func ProxyWithAccountCountFromService(p *service.ProxyWithAccountCount) *ProxyWithAccountCount {
	return egresshttp.ProxyWithAccountCountFromService(p)
}

func ProxyFromServiceAdmin(p *service.Proxy) *AdminProxy {
	return egresshttp.ProxyFromServiceAdmin(p)
}

func ProxyWithAccountCountFromServiceAdmin(p *service.ProxyWithAccountCount) *AdminProxyWithAccountCount {
	return egresshttp.ProxyWithAccountCountFromServiceAdmin(p)
}

func ProxyAccountSummaryFromService(a *service.ProxyAccountSummary) *ProxyAccountSummary {
	return egresshttp.ProxyAccountSummaryFromService(a)
}

func RedeemCodeFromService(rc *service.RedeemCode) *RedeemCode {
	return billinghttpapi.RedeemCodeFromService(rc)
}

func RedeemCodeFromServiceAdmin(rc *service.RedeemCode) *AdminRedeemCode {
	return billinghttpapi.RedeemCodeFromServiceAdmin(rc)
}

// AccountSummaryFromService returns a minimal AccountSummary for usage log display.
// Only includes ID and Name - no sensitive fields like Credentials, Proxy, etc.
func AccountSummaryFromService(a *service.Account) *AccountSummary {
	if a == nil {
		return nil
	}
	return &AccountSummary{
		ID:   a.ID,
		Name: a.Name,
	}
}

func usageLogFromServiceUser(l *service.UsageLog) UsageLog {
	// 普通用户 DTO：严禁包含管理员字段（例如 account_rate_multiplier、account、upstream_model）。
	requestType := l.EffectiveRequestType()
	stream, openAIWSMode := service.ApplyLegacyRequestFields(requestType, l.Stream, l.OpenAIWSMode)
	requestedModel := l.RequestedModel
	if requestedModel == "" {
		requestedModel = l.Model
	}
	return UsageLog{
		ID:                        l.ID,
		UserID:                    l.UserID,
		TeamID:                    l.TeamID,
		APIKeyID:                  l.APIKeyID,
		AccountID:                 l.AccountID,
		RequestID:                 l.RequestID,
		Model:                     requestedModel,
		ServiceTier:               l.ServiceTier,
		ReasoningEffort:           l.ReasoningEffort,
		RequestedReasoningEffort:  l.RequestedReasoningEffort,
		InboundEndpoint:           l.InboundEndpoint,
		GroupID:                   l.GroupID,
		SubscriptionID:            l.SubscriptionID,
		InputTokens:               l.InputTokens,
		OutputTokens:              l.OutputTokens,
		CacheCreationTokens:       l.CacheCreationTokens,
		CacheReadTokens:           l.CacheReadTokens,
		CacheCreation5mTokens:     l.CacheCreation5mTokens,
		CacheCreation1hTokens:     l.CacheCreation1hTokens,
		InputCost:                 l.InputCost,
		OutputCost:                l.OutputCost,
		CacheCreationCost:         l.CacheCreationCost,
		CacheReadCost:             l.CacheReadCost,
		TotalCost:                 l.TotalCost,
		ActualCost:                l.ActualCost,
		SubscriptionAmountUSD:     l.SubscriptionAmountUSD,
		BalanceAmountUSD:          l.BalanceAmountUSD,
		BillingAllocations:        cloneBillingAllocationsDTO(l.BillingAllocations),
		RateMultiplier:            l.RateMultiplier,
		LongContextBillingApplied: l.LongContextBillingApplied,
		BillingType:               l.BillingType,
		RequestType:               requestType.String(),
		Stream:                    stream,
		OpenAIWSMode:              openAIWSMode,
		NativeCompactionV2:        l.NativeCompactionV2,
		DurationMs:                l.DurationMs,
		FirstTokenMs:              l.FirstTokenMs,
		ImageCount:                l.ImageCount,
		ImageSize:                 l.ImageSize,
		ImageInputSize:            l.ImageInputSize,
		ImageOutputSize:           l.ImageOutputSize,
		ImageInputTokens:          l.ImageInputTokens,
		ImageInputCost:            l.ImageInputCost,
		ImageOutputTokens:         l.ImageOutputTokens,
		ImageOutputCost:           l.ImageOutputCost,
		ImageSizeSource:           l.ImageSizeSource,
		ImageSizeBreakdown:        l.ImageSizeBreakdown,
		MediaType:                 l.MediaType,
		UserAgent:                 l.UserAgent,
		IPAddress:                 l.IPAddress,
		SessionID:                 l.SessionID,
		CacheTTLOverridden:        l.CacheTTLOverridden,
		BillingMode:               l.BillingMode,
		CreatedAt:                 l.CreatedAt,
		User:                      UserFromServiceShallow(l.User),
		APIKey:                    APIKeyFromService(l.APIKey),
		Group:                     GroupFromServiceShallow(l.Group),
		Subscription:              UserSubscriptionFromService(l.Subscription),
	}
}

func cloneBillingAllocationsDTO(allocations []domain.BillingAllocation) []domain.BillingAllocation {
	if len(allocations) == 0 {
		return nil
	}
	out := make([]domain.BillingAllocation, 0, len(allocations))
	for i := range allocations {
		allocation := allocations[i]
		if allocation.SubscriptionID != nil {
			subscriptionID := *allocation.SubscriptionID
			allocation.SubscriptionID = &subscriptionID
		}
		if allocation.PlanID != nil {
			planID := *allocation.PlanID
			allocation.PlanID = &planID
		}
		out = append(out, allocation)
	}
	return out
}

// UsageLogFromService converts a service UsageLog to DTO for regular users.
// UsageLogFromService 转换普通用户可见的用量日志 DTO。
// 该 DTO 保留用户计费和请求元数据，但排除管理员专用的账号/上游内部字段。
func UsageLogFromService(l *service.UsageLog) *UsageLog {
	if l == nil {
		return nil
	}
	u := usageLogFromServiceUser(l)
	return &u
}

// UsageLogFromServiceAdmin converts a service UsageLog to DTO for admin users.
// It includes minimal Account info (ID, Name only) and IP address.
func UsageLogFromServiceAdmin(l *service.UsageLog) *AdminUsageLog {
	if l == nil {
		return nil
	}
	usageLog := usageLogFromServiceUser(l)
	usageLog.UpstreamEndpoint = l.UpstreamEndpoint
	return &AdminUsageLog{
		UsageLog:              usageLog,
		UpstreamModel:         l.UpstreamModel,
		UpstreamRequestID:     l.UpstreamRequestID,
		ChannelID:             l.ChannelID,
		ModelMappingChain:     l.ModelMappingChain,
		BillingTier:           l.BillingTier,
		AccountRateMultiplier: l.AccountRateMultiplier,
		AccountStatsCost:      l.AccountStatsCost,
		IPAddress:             l.IPAddress,
		Account:               AccountSummaryFromService(l.Account),
	}
}

// UsageLogTimingFromService 转换管理员使用记录需要展示的阶段耗时，避免暴露系统日志其它字段。
func UsageLogTimingFromService(timing *service.OpsRequestTiming) *UsageLogTiming {
	if timing == nil {
		return nil
	}
	return &UsageLogTiming{
		RequestContentLength:           timing.RequestContentLength,
		AccountSlotAcquiredMs:          timing.AccountSlotAcquiredMs,
		UpstreamGetConnMs:              timing.UpstreamGetConnMs,
		UpstreamGotConnMs:              timing.UpstreamGotConnMs,
		UpstreamWroteRequestMs:         timing.UpstreamWroteRequestMs,
		UpstreamFirstResponseByteMs:    timing.UpstreamFirstResponseByteMs,
		UpstreamFirstSSEDataMs:         timing.UpstreamFirstSSEDataMs,
		FirstVisibleOutputMs:           timing.FirstVisibleOutputMs,
		FirstDownstreamFlushMs:         timing.FirstDownstreamFlushMs,
		UpstreamGetConnCount:           timing.UpstreamGetConnCount,
		UpstreamGotConnCount:           timing.UpstreamGotConnCount,
		UpstreamAttemptCount:           timing.UpstreamAttemptCount,
		UpstreamFirstResponseByteCount: timing.UpstreamFirstResponseByteCount,
		UpstreamConnectionReused:       timing.UpstreamConnectionReused,
		UpstreamWroteRequestError:      timing.UpstreamWroteRequestError,
	}
}

func UsageCleanupTaskFromService(task *service.UsageCleanupTask) *UsageCleanupTask {
	if task == nil {
		return nil
	}
	return &UsageCleanupTask{
		ID:     task.ID,
		Status: task.Status,
		Filters: UsageCleanupFilters{
			StartTime:   task.Filters.StartTime,
			EndTime:     task.Filters.EndTime,
			UserID:      task.Filters.UserID,
			APIKeyID:    task.Filters.APIKeyID,
			AccountID:   task.Filters.AccountID,
			GroupID:     task.Filters.GroupID,
			Model:       task.Filters.Model,
			RequestType: requestTypeStringPtr(task.Filters.RequestType),
			Stream:      task.Filters.Stream,
			BillingType: task.Filters.BillingType,
		},
		CreatedBy:    task.CreatedBy,
		DeletedRows:  task.DeletedRows,
		ErrorMessage: task.ErrorMsg,
		CanceledBy:   task.CanceledBy,
		CanceledAt:   task.CanceledAt,
		StartedAt:    task.StartedAt,
		FinishedAt:   task.FinishedAt,
		CreatedAt:    task.CreatedAt,
		UpdatedAt:    task.UpdatedAt,
	}
}

func requestTypeStringPtr(requestType *int16) *string {
	if requestType == nil {
		return nil
	}
	value := service.RequestTypeFromInt16(*requestType).String()
	return &value
}

func SettingFromService(s *service.Setting) *Setting {
	if s == nil {
		return nil
	}
	return &Setting{
		ID:        s.ID,
		Key:       s.Key,
		Value:     s.Value,
		UpdatedAt: s.UpdatedAt,
	}
}

func UserSubscriptionFromService(sub *service.UserSubscription) *UserSubscription {
	return billinghttpapi.UserSubscriptionFromService(sub)
}

func UserSubscriptionFromServiceAdmin(sub *service.UserSubscription) *AdminUserSubscription {
	return billinghttpapi.UserSubscriptionFromServiceAdmin(sub)
}

func BulkAssignResultFromService(r *service.BulkAssignResult) *BulkAssignResult {
	return billinghttpapi.BulkAssignResultFromService(r)
}

func PromoCodeFromService(pc *service.PromoCode) *PromoCode {
	if pc == nil {
		return nil
	}
	return &PromoCode{
		ID:          pc.ID,
		Code:        pc.Code,
		BonusAmount: pc.BonusAmount,
		MaxUses:     pc.MaxUses,
		UsedCount:   pc.UsedCount,
		Status:      pc.Status,
		ExpiresAt:   pc.ExpiresAt,
		Notes:       pc.Notes,
		CreatedAt:   pc.CreatedAt,
		UpdatedAt:   pc.UpdatedAt,
	}
}

func PromoCodeUsageFromService(u *service.PromoCodeUsage) *PromoCodeUsage {
	if u == nil {
		return nil
	}
	return &PromoCodeUsage{
		ID:          u.ID,
		PromoCodeID: u.PromoCodeID,
		UserID:      u.UserID,
		BonusAmount: u.BonusAmount,
		UsedAt:      u.UsedAt,
		User:        UserFromServiceShallow(u.User),
	}
}
