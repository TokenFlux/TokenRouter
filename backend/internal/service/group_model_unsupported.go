package service

import (
	"sort"
	"strings"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// defaultRequestModelIDsForPlatform 返回指定平台没有显式白名单时展示的默认模型列表。
func defaultRequestModelIDsForPlatform(platform string) []string {
	return gatewayprovider.DefaultRequestModels(platform)
}

// availableRequestModelsFromAccounts 汇总当前分组账号对外可请求的模型列表。
func availableRequestModelsFromAccounts(accounts []Account, platform string) []string {
	modelSet := make(map[string]struct{})
	hasConfiguredModels := false
	for i := range accounts {
		acc := &accounts[i]
		if !acc.IsSchedulable() || !accountMatchesModelListPlatform(acc, platform) {
			continue
		}
		requestModels := acc.GetConfiguredRequestModels()
		if len(requestModels) == 0 {
			defaultModels := defaultRequestModelIDsForPlatform(platform)
			if platform == capability.PlatformQoder {
				site, err := qoderSiteForAccount(acc)
				if err != nil {
					continue
				}
				defaultModels = qoder.DefaultRequestModelIDsForSite(site)
			}
			for _, model := range defaultModels {
				if model = strings.TrimSpace(model); model != "" {
					modelSet[model] = struct{}{}
				}
			}
			continue
		}
		hasConfiguredModels = true
		for _, model := range requestModels {
			if model = strings.TrimSpace(model); model != "" && (platform != capability.PlatformQoder || acc.IsModelSupported(model)) {
				modelSet[model] = struct{}{}
			}
		}
	}
	if len(modelSet) == 0 && !hasConfiguredModels {
		return nil
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

// accountMatchesModelListPlatform 判断账号是否应计入目标平台的可用模型列表。
func accountMatchesModelListPlatform(account *Account, platform string) bool {
	if account == nil {
		return false
	}
	if platform == capability.PlatformAnthropic || platform == capability.PlatformGemini {
		return account.Platform == platform || (account.Platform == capability.PlatformAntigravity && account.IsMixedSchedulingEnabled())
	}
	return account.Platform == platform
}

// newGroupModelUnsupportedError 构造分组模型不支持错误。
func newGroupModelUnsupportedError(platform string, requestedModel string, accounts []Account) error {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(accounts) == 0 {
		return nil
	}
	return &routing.GroupModelUnsupportedError{
		Platform:        platform,
		RequestedModel:  requestedModel,
		AvailableModels: availableRequestModelsFromAccounts(accounts, platform),
	}
}
