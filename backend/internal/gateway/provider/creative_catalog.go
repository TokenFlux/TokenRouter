package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
)

// CreativeCatalogAccount 与任务执行共用账号规则，保留透传、平台归一化及一跳映射语义。
func CreativeCatalogAccount(value *account.Record) creative.CatalogAccount {
	if value == nil {
		return nil
	}
	return creativeCatalogAccount{CatalogAccount: creativeprovider.CatalogAccount(value), policy: ModelPolicy{Record: value}}
}

type creativeCatalogAccount struct {
	creative.CatalogAccount
	policy ModelPolicy
}

func (a creativeCatalogAccount) ResolveMappedModel(model string) (string, bool) {
	mapped := a.policy.UpstreamModel(context.Background(), model)
	return mapped, mapped != model
}

func (a creativeCatalogAccount) IsModelSupported(model string) bool {
	return a.policy.Supports(context.Background(), model)
}
