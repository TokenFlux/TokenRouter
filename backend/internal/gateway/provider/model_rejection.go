package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// ModelRejectionDefaults 仅在没有显式模型时读取站点，保留无效站点不提供默认目录。
func ModelRejectionDefaults(platform string, site func() string) ([]string, error) {
	models := DefaultRequestModels(platform)
	if platform == capability.PlatformQoder {
		value, err := qoder.ParseSite(site())
		if err != nil {
			return nil, err
		}
		models = qoder.DefaultRequestModelIDsForSite(value)
	}
	return models, nil
}

type modelRejectionRules struct{ *account.Record }

func (r modelRejectionRules) GetConfiguredRequestModels() []string {
	return r.Record.GetConfiguredRequestModels(accountprovider.ModelDefaults())
}
func (r modelRejectionRules) IsModelSupported(model string) bool {
	return r.Record.IsModelSupported(model, accountprovider.ModelDefaults(), accountprovider.ModelRules(r.Record))
}

// ModelRejectionAccount 提供原生记录的窄规则投影，不把凭据传入 routing。
func ModelRejectionAccount(value *account.Record) routing.ModelRejectionSource {
	return routing.ModelRejectionSource{Platform: value.Platform, Rules: modelRejectionRules{value}, Defaults: func(platform string) ([]string, error) {
		return ModelRejectionDefaults(platform, func() string { return value.GetCredential("site") })
	}}
}
