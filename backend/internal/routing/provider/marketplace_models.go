package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// MarketplaceModelDefs 投影平台当前默认模型，保持动态目录读取时点与返回顺序。
func MarketplaceModelDefs(platform string) []routing.MarketplaceModelDef {
	switch platform {
	case capability.PlatformOpenAI:
		models := make([]routing.MarketplaceModelDef, 0, len(openai.DefaultModels))
		for _, model := range openai.DefaultModels {
			models = append(models, routing.MarketplaceModelDef{
				ID:          model.ID,
				DisplayName: model.DisplayName,
			})
		}
		return models
	case capability.PlatformAnthropic:
		models := make([]routing.MarketplaceModelDef, 0, len(claude.DefaultModels))
		for _, model := range claude.DefaultModels {
			models = append(models, routing.MarketplaceModelDef{
				ID:          model.ID,
				DisplayName: model.DisplayName,
			})
		}
		return models
	case capability.PlatformGemini:
		models := make([]routing.MarketplaceModelDef, 0, len(geminicli.DefaultModels))
		for _, model := range geminicli.DefaultModels {
			models = append(models, routing.MarketplaceModelDef{
				ID:          model.ID,
				DisplayName: model.DisplayName,
			})
		}
		return models
	case capability.PlatformGrok:
		defaultModels := xai.DefaultModels()
		models := make([]routing.MarketplaceModelDef, 0, len(defaultModels))
		for _, model := range defaultModels {
			models = append(models, routing.MarketplaceModelDef{
				ID:          model.ID,
				DisplayName: model.DisplayName,
			})
		}
		return models
	case capability.PlatformAntigravity:
		defaultModels := antigravity.DefaultModels()
		models := make([]routing.MarketplaceModelDef, 0, len(defaultModels))
		for _, model := range defaultModels {
			models = append(models, routing.MarketplaceModelDef{
				ID:          model.ID,
				DisplayName: model.DisplayName,
			})
		}
		return models
	case capability.PlatformQoder:
		models := make([]routing.MarketplaceModelDef, 0, len(qoder.DefaultQoderModelAliases))
		models = append(models, qoderDefaultPublicModels()...)
		return models
	default:
		return nil
	}
}

// MarketplaceDisplayNames 保留各平台显示名称及模型别名的注册规则。
func MarketplaceDisplayNames(platform string) map[string]string {
	switch platform {
	case capability.PlatformOpenAI:
		out := make(map[string]string, len(openai.DefaultModels))
		for _, model := range openai.DefaultModels {
			routing.RegisterMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	case capability.PlatformAnthropic:
		out := make(map[string]string, len(claude.DefaultModels))
		for _, model := range claude.DefaultModels {
			routing.RegisterMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	case capability.PlatformGemini:
		out := make(map[string]string, len(geminicli.DefaultModels))
		for _, model := range geminicli.DefaultModels {
			routing.RegisterMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	case capability.PlatformGrok:
		defaultModels := xai.DefaultModels()
		out := make(map[string]string, len(defaultModels))
		for _, model := range defaultModels {
			routing.RegisterMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	case capability.PlatformAntigravity:
		defaultModels := antigravity.DefaultModels()
		out := make(map[string]string, len(defaultModels))
		for _, model := range defaultModels {
			routing.RegisterMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	case capability.PlatformQoder:
		out := make(map[string]string, len(qoder.DefaultQoderModelAliases))
		for _, model := range qoderDefaultPublicModels() {
			routing.RegisterMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	default:
		return nil
	}
}

func qoderDefaultPublicModels() []routing.MarketplaceModelDef {
	models := make([]routing.MarketplaceModelDef, 0, len(qoder.DefaultQoderModelAliases))
	for alias, info := range qoder.DefaultQoderModelAliases {
		displayName := info.DisplayName
		if displayName == "" {
			displayName = alias
		}
		models = append(models, routing.MarketplaceModelDef{
			ID:          alias,
			DisplayName: displayName,
		})
	}
	routing.SortMarketplaceModelDefs(models)
	return models
}
