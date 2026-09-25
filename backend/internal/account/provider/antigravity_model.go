package provider

import (
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// mapAntigravityModel 获取映射后的模型名
// 完全依赖映射配置：账户映射（通配符）→ 默认映射兜底（DefaultAntigravityModelMapping）
// 注意：返回空字符串表示模型不被支持，调度时会过滤掉该账号
// MapAntigravityModel 保留支持检查与显式透传的一跳映射。
func MapAntigravityModel(account *accountcore.Record, requestedModel string) string {
	if account == nil {
		return ""
	}
	requestedModel = strings.TrimPrefix(requestedModel, "models/")

	// 获取映射表（未配置时自动使用 DefaultAntigravityModelMapping）
	mapping := accountcore.ResolveModelMapping(account, ModelDefaults())
	if len(mapping) == 0 {
		return "" // 无映射配置（非 Antigravity 平台）
	}

	// 通过映射表查询（支持精确匹配 + 通配符）
	mapped, _ := accountcore.ResolveMappedModel(account.Platform, accountcore.ResolveModelMapping(account, ModelDefaults()), requestedModel)

	// 判断是否映射成功（mapped != requestedModel 说明找到了映射规则）
	if mapped != requestedModel {
		return mapped
	}

	// 如果 mapped == requestedModel，检查是否在映射表中配置（精确或通配符）
	// 这区分两种情况：
	// 1. 映射表中有 "model-a": "model-a"（显式透传）→ 返回 model-a
	// 2. 通配符匹配 "claude-*": "claude-sonnet-4-5" 恰好目标等于请求名 → 返回 model-a
	// 3. 映射表中没有 model-a 的配置 → 返回空（不支持）
	if account.IsModelSupported(requestedModel, ModelDefaults(), ModelRules(account)) {
		return requestedModel
	}

	// 未在映射表中配置的模型，返回空字符串（不支持）
	return ""
}
