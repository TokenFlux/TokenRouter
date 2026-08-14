package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/antigravity"
	"github.com/BrandonVee/TokenRouter/internal/pkg/claude"
	"github.com/BrandonVee/TokenRouter/internal/pkg/geminicli"
	"github.com/BrandonVee/TokenRouter/internal/pkg/openai"
	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
)

const groupModelUnsupportedAvailableModelsLimit = 20

// GroupModelUnsupportedError 表示当前分组没有支持本次请求模型的可调度账号。
type GroupModelUnsupportedError struct {
	Platform        string
	RequestedModel  string
	AvailableModels []string
}

func (e *GroupModelUnsupportedError) Error() string {
	if e == nil {
		return ""
	}
	return buildGroupModelUnsupportedMessage(e.RequestedModel, truncateModelListForError(e.AvailableModels))
}

// buildGroupModelUnsupportedMessage 生成对外返回的英文错误文案。
func buildGroupModelUnsupportedMessage(requestedModel string, availableModels []string) string {
	requestedModel = strings.TrimSpace(requestedModel)
	message := fmt.Sprintf("The current group does not support the requested model %q", requestedModel)
	if len(availableModels) == 0 {
		return message
	}
	return fmt.Sprintf("%s. Available models: %s", message, strings.Join(availableModels, ", "))
}

// truncateModelListForError 限制错误文案中的模型数量，避免响应体过长。
func truncateModelListForError(models []string) []string {
	if len(models) <= groupModelUnsupportedAvailableModelsLimit {
		return cloneStringSlice(models)
	}
	out := cloneStringSlice(models[:groupModelUnsupportedAvailableModelsLimit])
	out = append(out, fmt.Sprintf("and %d more", len(models)-groupModelUnsupportedAvailableModelsLimit))
	return out
}

// defaultRequestModelIDsForPlatform 返回指定平台没有显式白名单时展示的默认模型列表。
func defaultRequestModelIDsForPlatform(platform string) []string {
	switch platform {
	case PlatformOpenAI:
		return openai.DefaultModelIDs()
	case PlatformGemini:
		ids := make([]string, 0, len(geminicli.DefaultModels))
		for _, model := range geminicli.DefaultModels {
			ids = append(ids, model.ID)
		}
		return ids
	case PlatformAntigravity:
		models := antigravity.DefaultModels()
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids
	case PlatformQoder:
		return qoder.DefaultRequestModelIDs()
	case PlatformGrok:
		return xai.DefaultModelIDs()
	default:
		return claude.DefaultModelIDs()
	}
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
			if platform == PlatformQoder {
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
			if model = strings.TrimSpace(model); model != "" && (platform != PlatformQoder || acc.IsModelSupported(model)) {
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
	if platform == PlatformAnthropic || platform == PlatformGemini {
		return account.Platform == platform || (account.Platform == PlatformAntigravity && account.IsMixedSchedulingEnabled())
	}
	return account.Platform == platform
}

// newGroupModelUnsupportedError 构造分组模型不支持错误。
func newGroupModelUnsupportedError(platform string, requestedModel string, accounts []Account) error {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(accounts) == 0 {
		return nil
	}
	return &GroupModelUnsupportedError{
		Platform:        platform,
		RequestedModel:  requestedModel,
		AvailableModels: availableRequestModelsFromAccounts(accounts, platform),
	}
}
