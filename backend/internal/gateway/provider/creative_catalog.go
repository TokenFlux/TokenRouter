package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider"
)

type creativeCatalogProvider struct {
	creative.CatalogProvider
	policy ModelPolicy
}

// CreativeCatalogProvider 按执行时的透传、平台归一化和一跳映射规则解析目录模型。
func CreativeCatalogProvider(value *provider.Record) creative.CatalogProvider {
	if value == nil {
		return nil
	}
	policy := (ModelPolicy{Record: value}).Prepared()
	return creativeCatalogProvider{CatalogProvider: creativeprovider.CatalogProviderWithRules(value, policy.models), policy: policy}
}

func (a creativeCatalogProvider) ResolveMappedModel(model string) (string, bool) {
	mapped := a.policy.UpstreamModel(context.Background(), model)
	return mapped, mapped != model
}

func (a creativeCatalogProvider) IsModelSupported(model string) bool {
	return a.policy.Supports(context.Background(), model)
}
