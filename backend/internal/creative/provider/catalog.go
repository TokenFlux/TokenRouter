package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

type catalogProvider struct {
	*provider.Record
	models    *provider.ModelRulesSnapshot
	protocols capability.ProviderProtocols
}

// CatalogProvider 向公开目录提供模型规则。
func CatalogProvider(value *provider.Record) creative.CatalogProvider {
	if value == nil {
		return nil
	}
	return CatalogProviderWithRules(value, provider.PrepareModelRules(value, provideradapter.ModelDefaults(), provideradapter.ModelRules(value)))
}

// CatalogProviderWithRules 在网关目录和创作台之间共享本次查询的规则。
func CatalogProviderWithRules(value *provider.Record, rules *provider.ModelRulesSnapshot) creative.CatalogProvider {
	return catalogProvider{Record: value, models: rules, protocols: value.RoutingSnapshot().Protocols()}
}

func (a catalogProvider) GetModelMapping() map[string]string {
	return a.models.Mapping()
}

func (a catalogProvider) GetConfiguredRequestModels() []string {
	return a.models.ConfiguredModels()
}

func (a catalogProvider) IsModelSupported(model string) bool {
	return a.models.Supports(model)
}

func (a catalogProvider) ResolveMappedModel(model string) (string, bool) {
	return a.models.ResolveMappedModel(model)
}

func (a catalogProvider) PlatformID() string { return a.Platform }
func (a catalogProvider) AllowsProtocol(source protocol.ProtocolID, fallbacks map[protocol.ProtocolID][]protocol.ProtocolID) bool {
	_, ok := capability.ResolveRoute(a.protocols, source, fallbacks)
	return ok
}
