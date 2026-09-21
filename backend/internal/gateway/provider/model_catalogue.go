package provider

import (
	"context"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// DefaultRequestModels 延续各平台原目录来源与未知平台回退，不另建模型缓存。
func DefaultRequestModels(platform string) []string {
	switch platform {
	case capability.PlatformOpenAI:
		return openai.DefaultModelIDs()
	case capability.PlatformGemini:
		ids := make([]string, 0, len(codeassist.DefaultModels))
		for _, model := range codeassist.DefaultModels {
			ids = append(ids, model.ID)
		}
		return ids
	case capability.PlatformAntigravity:
		models := antigravity.DefaultModels()
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids
	case capability.PlatformQoder:
		return qoder.DefaultRequestModelIDs()
	case capability.PlatformGrok:
		return grok.DefaultModelIDs()
	default:
		return anthropic.DefaultModelIDs()
	}
}
func CatalogueDefaults() routing.CatalogueDefaults {
	return routing.CatalogueDefaults{Platform: DefaultRequestModels, Qoder: func(cn bool) []string {
		site := qoder.SiteGlobal
		if cn {
			site = qoder.SiteCN
		}
		return qoder.DefaultRequestModelIDsForSite(site)
	}}
}

type catalogueRules struct{ policy ModelPolicy }

func (v catalogueRules) ConfiguredModels() []string {
	return v.policy.Record.GetConfiguredRequestModels(accountprovider.ModelDefaults())
}
func (v catalogueRules) Mapping() map[string]string {
	return account.ResolveModelMapping(v.policy.Record, accountprovider.ModelDefaults())
}
func (v catalogueRules) Unrestricted() bool {
	return v.policy.Record.HasUnrestrictedModelScope(accountprovider.ModelDefaults())
}
func (v catalogueRules) QoderCN() bool {
	site, err := qoder.ParseSite(v.policy.Record.GetCredential("site"))
	return err == nil && site == qoder.SiteCN
}
func (v catalogueRules) Supports(ctx context.Context, model string) bool {
	return v.policy.Supports(ctx, model)
}
func (v catalogueRules) UpstreamModels(ctx context.Context, model string) []string {
	return v.policy.ListingModels(ctx, model)
}

// CatalogueAccount 只公开原目录投影，凭据仅留在内部模型规则端口。
func CatalogueAccount(value *account.Record, route requeststate.AttemptRoute) routing.CatalogueAccount {
	snapshot := value.RoutingSnapshot()
	target := account.ProtocolTarget{Record: value, Protocol: route.Protocol()}
	snapshot.EnabledProtocols = slices.Clone(value.UpstreamProtocolsForLegacy(target.GetAPIProtocol()))
	groups := make([]int64, len(value.AccountGroups))
	for i, g := range value.AccountGroups {
		groups[i] = g.GroupID
	}
	return routing.CatalogueAccount{AccountSnapshot: snapshot, GroupIDs: slices.Clone(value.GroupIDs), AccountGroupIDs: groups, MixedScheduling: value.IsMixedSchedulingEnabled(), Passthrough: value.IsOpenAIPassthroughEnabled(), Rules: catalogueRules{ModelPolicy{Record: value, Route: route}}}
}
func CatalogueAccounts(values []account.Record) []routing.CatalogueAccount {
	if values == nil {
		return nil
	}
	out := make([]routing.CatalogueAccount, len(values))
	for i := range values {
		out[i] = CatalogueAccount(&values[i], requeststate.AttemptRoute{})
	}
	return out
}
