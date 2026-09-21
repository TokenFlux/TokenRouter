// 只把用量关联投影交给现有 DTO 方法，不复制身份/Key/分组规则。
package dto

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

func groupFromView(v *usage.GroupView) *Group {
	if v == nil {
		return nil
	}
	out := routingdto.GroupFromRoutingBase((*routing.Group)(v))
	return &out
}
func accountFromView(v *usage.AccountView) *AccountSummary {
	if v == nil {
		return nil
	}
	return &AccountSummary{ID: v.ID, Name: v.Name}
}
func userFromView(v *usage.UserView) *User {
	if v == nil {
		return nil
	}
	u := &identity.User{ID: v.ID, Email: v.Email, Username: v.Username, Role: v.Role, Balance: v.Balance, FrozenBalance: v.FrozenBalance, Concurrency: v.Concurrency, Status: v.Status, AllowedGroups: v.AllowedGroups, DisabledPublicGroups: v.DisabledPublicGroups, LastActiveAt: v.LastActiveAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, DeletedAt: v.DeletedAt, BalanceNotifyEnabled: v.BalanceNotifyEnabled, BalanceNotifyThresholdType: v.BalanceNotifyThresholdType, BalanceNotifyThreshold: v.BalanceNotifyThreshold, BalanceNotifyExtraEmails: v.BalanceNotifyExtraEmails, TotalRecharged: v.TotalRecharged, RPMLimit: v.RPMLimit, APIKeyLimit: v.APIKeyLimit}
	return identitydto.UserFromIdentityShallow[APIKey](u)
}
func keyFromView(v *usage.KeyView) *APIKey {
	if v == nil {
		return nil
	}
	k := &apikey.APIKey{ID: v.ID, UserID: v.UserID, TeamID: v.TeamID, TeamOwnerDisabled: v.TeamOwnerDisabled, Key: v.Key, Name: v.Name, GroupID: v.GroupID, IsComposite: v.IsComposite, Status: v.Status, FastModePolicy: v.FastModePolicy, BillingMode: v.BillingMode, PreferredSubscriptionID: v.PreferredSubscriptionID, ModelMapping: v.ModelMapping, IPWhitelist: v.IPWhitelist, IPBlacklist: v.IPBlacklist, LastUsedAt: v.LastUsedAt, LastUsedIP: v.LastUsedIP, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, FallbackToDefaultGroupWhenUnavailable: v.FallbackToDefaultGroupWhenUnavailable, CurrentConcurrency: v.CurrentConcurrency, ManagedBy: v.ManagedBy, Quota: v.Quota, QuotaUsed: v.QuotaUsed, ExpiresAt: v.ExpiresAt, RateLimit5h: v.RateLimit5h, RateLimit1d: v.RateLimit1d, RateLimit7d: v.RateLimit7d, Usage5h: v.Usage5h, Usage1d: v.Usage1d, Usage7d: v.Usage7d, Window5hStart: v.Window5hStart, Window1dStart: v.Window1dStart, Window7dStart: v.Window7dStart}
	k.Group = apikey.GroupFromRouting((*routing.Group)(v.Group))
	for _, g := range v.CompositeGroups {
		k.CompositeGroups = append(k.CompositeGroups, apikey.APIKeyCompositeGroup{ID: g.ID, APIKeyID: g.APIKeyID, GroupID: g.GroupID, Prefix: g.Prefix, NormalizedPrefix: g.NormalizedPrefix, SortOrder: g.SortOrder, UserGroupRPMOverride: g.UserGroupRPMOverride, Group: apikey.GroupFromRouting((*routing.Group)(g.Group))})
	}
	return keydto.APIKeyFromKey(k, func(g *routing.Group) *Group { return groupFromView((*usage.GroupView)(apikey.RoutingGroup(g))) })
}
