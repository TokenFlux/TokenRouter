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

// views.go converts loaded usage associations into nested account, key, group,
// and provider responses.
// Usage rows carry account, key and group data loaded for display. HTTP
// conversion passes that data to the owning modules' DTO mappers, which apply
// their own field selection and language handling before the nested response
// is encoded.

// groupFromView formats a loaded group through the routing DTO mapper and
// supplied display language. Nil views return nil; nested group values follow
// that mapper's ownership rules during response encoding.
func groupFromView(v *usage.GroupView, language ...string) *Group {
	if v == nil {
		return nil
	}
	return routingdto.GroupFromRouting((*routing.Group)(v), language...)
}

// providerFromView copies the loaded provider identity into a display
// summary. Nil input returns nil, and the response contains the ID and name
// supplied by the usage relation.
func providerFromView(v *usage.ProviderView) *ProviderSummary {
	if v == nil {
		return nil
	}
	return &ProviderSummary{ID: v.ID, Name: v.Name}
}

// userFromView formats a loaded account through the shallow identity DTO
// mapper. Nil input returns nil; account slices and optional pointers
// retained by that mapper remain shared during response encoding.
func userFromView(v *usage.UserView) *User {
	if v == nil {
		return nil
	}
	u := &identity.User{ID: v.ID, Email: v.Email, Username: v.Username, Role: v.Role, Balance: v.Balance, FrozenBalance: v.FrozenBalance, Concurrency: v.Concurrency, Status: v.Status, AllowedGroups: v.AllowedGroups, DisabledPublicGroups: v.DisabledPublicGroups, LastActiveAt: v.LastActiveAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, DeletedAt: v.DeletedAt, BalanceNotifyEnabled: v.BalanceNotifyEnabled, BalanceNotifyThresholdType: v.BalanceNotifyThresholdType, BalanceNotifyThreshold: v.BalanceNotifyThreshold, BalanceNotifyExtraEmails: v.BalanceNotifyExtraEmails, TotalRecharged: v.TotalRecharged, RPMLimit: v.RPMLimit, APIKeyLimit: v.APIKeyLimit}
	return identitydto.UserFromIdentityShallow[APIKey](u)
}

// keyFromView formats loaded key metadata and composite bindings through the
// API-key DTO mapper. Nil input returns nil; model mappings are copied there,
// while access-list slices and optional pointers remain shared.
func keyFromView(v *usage.KeyView, language ...string) *APIKey {
	if v == nil {
		return nil
	}
	k := &apikey.APIKey{ID: v.ID, UserID: v.UserID, TeamID: v.TeamID, TeamOwnerDisabled: v.TeamOwnerDisabled, Key: v.Key, Name: v.Name, GroupID: v.GroupID, IsComposite: v.IsComposite, Status: v.Status, FastModePolicy: v.FastModePolicy, ForceClaude1hCache: v.ForceClaude1hCache, BillingMode: v.BillingMode, PreferredSubscriptionID: v.PreferredSubscriptionID, ModelMapping: v.ModelMapping, IPWhitelist: v.IPWhitelist, IPBlacklist: v.IPBlacklist, LastUsedAt: v.LastUsedAt, LastUsedIP: v.LastUsedIP, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, FallbackWhenGroupUnavailable: v.FallbackWhenGroupUnavailable, CurrentConcurrency: v.CurrentConcurrency, ManagedBy: v.ManagedBy, Quota: v.Quota, QuotaUsed: v.QuotaUsed, ExpiresAt: v.ExpiresAt, RateLimit5h: v.RateLimit5h, RateLimit1d: v.RateLimit1d, RateLimit7d: v.RateLimit7d, Usage5h: v.Usage5h, Usage1d: v.Usage1d, Usage7d: v.Usage7d, Window5hStart: v.Window5hStart, Window1dStart: v.Window1dStart, Window7dStart: v.Window7dStart}
	k.Group = apikey.GroupFromRouting((*routing.Group)(v.Group))
	for _, g := range v.CompositeGroups {
		k.CompositeGroups = append(k.CompositeGroups, apikey.APIKeyCompositeGroup{ID: g.ID, APIKeyID: g.APIKeyID, GroupID: g.GroupID, Prefix: g.Prefix, NormalizedPrefix: g.NormalizedPrefix, SortOrder: g.SortOrder, UserGroupRPMOverride: g.UserGroupRPMOverride, Group: apikey.GroupFromRouting((*routing.Group)(g.Group))})
	}
	return keydto.APIKeyFromKey(k, func(g *routing.Group) *Group {
		return groupFromView((*usage.GroupView)(apikey.RoutingGroup(g)), language...)
	})
}
