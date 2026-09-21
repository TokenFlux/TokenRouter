package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/creative"
)

// CatalogAccount 只交付目录规则，不向公开响应暴露账号凭据。
func CatalogAccount(value *account.Record) creative.CatalogAccount {
	if value == nil {
		return nil
	}
	return catalogAccount{value}
}

type catalogAccount struct{ *account.Record }

func (a catalogAccount) GetModelMapping() map[string]string {
	return account.ResolveModelMapping(a.Record, accountprovider.ModelDefaults())
}
func (a catalogAccount) GetConfiguredRequestModels() []string {
	return a.Record.GetConfiguredRequestModels(accountprovider.ModelDefaults())
}
func (a catalogAccount) IsModelSupported(model string) bool {
	return a.Record.IsModelSupported(model, accountprovider.ModelDefaults(), accountprovider.ModelRules(a.Record))
}
func (a catalogAccount) ResolveMappedModel(model string) (string, bool) {
	return account.ResolveMappedModel(a.Platform, a.GetModelMapping(), model)
}
