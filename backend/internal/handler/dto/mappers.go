// Package dto provides data transfer objects for HTTP handlers.
package dto

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountdto "github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/service"
	usagedto "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"
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

func UsageLogFromService(l *service.UsageLog) *UsageLog {
	return usagedto.FromUsage(service.UsageLogView(l))
}

func UsageLogFromServiceAdmin(l *service.UsageLog) *AdminUsageLog {
	return usagedto.FromUsageAdmin(service.UsageLogView(l))
}

func UsageLogTimingFromService(timing *service.OpsRequestTiming) *UsageLogTiming {
	return usagedto.TimingFromOps(timing)
}

func UsageCleanupTaskFromService(task *service.UsageCleanupTask) *UsageCleanupTask {
	return usagedto.CleanupFromUsage(task)
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
