// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	slog "log/slog"
	slices "slices"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	qoder "github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

func legacyCatalogueDefaults() routing.CatalogueDefaults {
	return routing.CatalogueDefaults{Platform: defaultRequestModelIDsForPlatform, Qoder: func(cn bool) []string {
		site := qoder.SiteGlobal
		if cn {
			site = qoder.SiteCN
		}
		return qoder.DefaultRequestModelIDsForSite(site)
	}}
}
func (s *GatewayService) requestableModelResolver() routing.RequestableResolver {
	var channels routing.CatalogueChannels
	if s != nil && s.channelService != nil {
		channels = s.channelService.core()
	}
	return routing.RequestableResolver{Channels: channels, Defaults: legacyCatalogueDefaults(), Warn: slog.Warn}
}

type legacyCatalogueAccount struct {
	account *Account
	gateway *GatewayService
}

func (v legacyCatalogueAccount) ConfiguredModels() []string {
	return v.account.GetConfiguredRequestModels()
}
func (v legacyCatalogueAccount) Mapping() map[string]string { return v.account.GetModelMapping() }
func (v legacyCatalogueAccount) Unrestricted() bool {
	return protocolRecord(v.account).HasUnrestrictedModelScope(legacyAccountModelDefaults())
}
func (v legacyCatalogueAccount) QoderCN() bool {
	site, err := qoderSiteForAccount(v.account)
	return err == nil && site == qoder.SiteCN
}
func (v legacyCatalogueAccount) Supports(ctx context.Context, model string) bool {
	return v.gateway.isRoutingModelSupportedByAccountWithContext(ctx, v.account, model)
}
func (v legacyCatalogueAccount) UpstreamModels(ctx context.Context, model string) []string {
	return v.gateway.resolveAccountUpstreamModelsForListing(ctx, v.account, model)
}
func legacyCatalogueAccounts(gateway *GatewayService, values []Account) []routing.CatalogueAccount {
	if values == nil {
		return nil
	}
	out := make([]routing.CatalogueAccount, len(values))
	for i := range values {
		v := &values[i]
		groups := make([]int64, len(v.AccountGroups))
		for j, g := range v.AccountGroups {
			groups[j] = g.GroupID
		}
		out[i] = routing.CatalogueAccount{AccountSnapshot: AccountSnapshotView(v), GroupIDs: slices.Clone(v.GroupIDs), AccountGroupIDs: groups, MixedScheduling: v.IsMixedSchedulingEnabled(), Passthrough: v.IsOpenAIPassthroughEnabled(), Rules: legacyCatalogueAccount{v, gateway}}
	}
	return out
}
