package provider

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// resolveAntigravityModel 只执行一次映射，白名单在 thinking 规范化之后检查。
func resolveAntigravityModel(value *provider.Record, requested string) string {
	if value == nil {
		return ""
	}
	requested = strings.TrimSpace(strings.TrimPrefix(requested, "models/"))
	if requested == "" {
		return ""
	}
	mapping := provider.ResolveModelMapping(value, ModelDefaults())
	mapped, _ := provider.ResolveMappedModel(mapping, requested)
	return mapped
}

// MapAntigravityModel 返回不改变 thinking 档位时的最终可用模型。
func MapAntigravityModel(value *provider.Record, requested string) string {
	return FinalAntigravityModel(value, requested, nil)
}

// FinalAntigravityModel 在单跳映射和 thinking 规范化后检查最终白名单。
func FinalAntigravityModel(value *provider.Record, requested string, thinking *bool) string {
	mapped := resolveAntigravityModel(value, requested)
	if mapped == "" {
		return ""
	}
	return finalAntigravityModel(mapped, thinking, func(model string) bool {
		return value.FinalModelWhitelisted(model, ModelDefaults(), ModelRules(value))
	})
}

// FinalAntigravityModelWithRules 使用已准备的映射和白名单处理 thinking 型号。
func FinalAntigravityModelWithRules(rules *provider.ModelRulesSnapshot, requested string, thinking *bool) string {
	requested = strings.TrimSpace(strings.TrimPrefix(requested, "models/"))
	if requested == "" {
		return ""
	}
	mapped, _ := rules.ResolveMappedModel(requested)
	return finalAntigravityModel(mapped, thinking, rules.FinalModelWhitelisted)
}

func finalAntigravityModel(mapped string, thinking *bool, allows func(string) bool) string {
	if mapped == "" {
		return ""
	}
	if thinking != nil {
		mapped = antigravity.ApplyThinkingModelSuffix(mapped, *thinking)
	}
	if !allows(mapped) {
		return ""
	}
	return mapped
}
