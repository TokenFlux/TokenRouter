// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"sort"
)

// ModelPlatformRules 只在平台专有资格分支按需调用，模型配置与一跳匹配由 account 拥有。
type ModelPlatformRules struct {
	NormalizeQoder      func(string) string
	QoderCompatible     func(string) bool
	OpenAIOAuthServable func(string) bool
}

// IsModelSupported 检查账号是否支持该请求模型。
// 规则：
// 1. 未配置 model_mapping 时，直接按最终白名单（model_whitelist）判断；未配置白名单则允许所有模型；
// 2. 已配置时，若请求模型命中映射/透传规则，则先映射，再对映射后的最终模型做白名单校验；
// 3. 若请求模型未命中映射，则把它当作隐式透传模型，直接按最终模型做白名单校验；
// 4. 当不存在任何白名单时，mapping 仅作为可选改写规则，不限制请求模型。
// 5. 为兼容旧数据，非 Qoder 平台若未配置独立 model_whitelist，会继续把精确自映射条目视作最终白名单。
// 6. OpenAI OAuth 非透传账号还会排除明确属于其他厂商的模型，避免 Codex 上游返回不可重试的 400。
func (a *Record) IsModelSupported(requestedModel string, defaults ModelMappingDefaults, rules ModelPlatformRules) bool {
	// OpenAI 透传模式仅替换认证，模型能力由上游决定；必须在 model_mapping
	// 分支前短路，否则调度快照中的历史映射会把可用账号误判为不支持模型。
	if a.IsOpenAIPassthroughEnabled() {
		return true
	}
	mapping := ResolveModelMapping(a, defaults)
	// Antigravity 仍保持“请求模型命中映射即可支持”的既有语义。
	// 该平台的最终模型（含 thinking 后缀）校验由网关层专门处理，不能在这里提前套用通用白名单规则。
	if a.Platform == PlatformAntigravity {
		if len(mapping) == 0 {
			return true
		}
		_, matched := ResolveMappedModel(a.Platform, mapping, requestedModel)
		return matched
	}
	whitelist, _ := ResolveFinalModelWhitelist(a.Platform, a.Credentials, mapping)
	if a.Platform == PlatformQoder {
		mappedModel, matched := ResolveMappedModel(a.Platform, mapping, requestedModel)
		if matched {
			// 显式账号 mapping 优先于站点默认模型限制。
			return ModelInFinalWhitelist(a.Platform, mappedModel, whitelist, rules.NormalizeQoder)
		}
		if !rules.QoderCompatible(requestedModel) {
			return false
		}
		return ModelInFinalWhitelist(a.Platform, requestedModel, whitelist, rules.NormalizeQoder)
	}
	if len(mapping) == 0 {
		if !ModelInFinalWhitelist(a.Platform, requestedModel, whitelist, rules.NormalizeQoder) {
			return false
		}
		if a.IsOpenAIOAuth() && !a.IsOpenAIPassthroughEnabled() {
			return rules.OpenAIOAuthServable(requestedModel)
		}
		return true
	}
	mappedModel, matched := ResolveMappedModel(a.Platform, mapping, requestedModel)
	if matched {
		return ModelInFinalWhitelist(a.Platform, mappedModel, whitelist, rules.NormalizeQoder)
	}
	return ModelInFinalWhitelist(a.Platform, requestedModel, whitelist, rules.NormalizeQoder)
}

// FinalModelWhitelisted 直接检查已经完成账号映射和平台规范化的最终模型，避免再次执行账号映射。
func (a *Record) FinalModelWhitelisted(finalModel string, defaults ModelMappingDefaults, rules ModelPlatformRules) bool {
	if a == nil {
		return false
	}
	mapping := ResolveModelMapping(a, defaults)
	whitelist, _ := ResolveFinalModelWhitelist(a.Platform, a.Credentials, mapping)
	return ModelInFinalWhitelist(a.Platform, finalModel, whitelist, rules.NormalizeQoder)
}

// GetConfiguredRequestModels 返回账号显式配置的“可请求模型”列表。
// 只有存在最终白名单时，才返回有限的“可请求模型”集合：
// - 白名单中的模型可直接请求（隐式透传）；
// - mapping 的 key 也可请求（显式改写）。
// 若不存在白名单，则请求模型空间不受限制，返回 nil 表示调用方应回退到默认模型列表。
func (a *Record) GetConfiguredRequestModels(defaults ModelMappingDefaults) []string {
	mapping := ResolveModelMapping(a, defaults)
	whitelist, _ := ResolveFinalModelWhitelist(a.Platform, a.Credentials, mapping)
	if a.Platform == PlatformQoder {
		return ConfiguredQoderRequestModels(mapping, whitelist)
	}
	if len(whitelist) == 0 {
		return nil
	}
	modelSet := make(map[string]struct{})
	if len(mapping) > 0 {
		for model := range mapping {
			modelSet[model] = struct{}{}
		}
	}
	// 无论白名单来自显式字段还是 legacy 自映射，白名单模型本身都可直接请求。
	for model := range whitelist {
		modelSet[model] = struct{}{}
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

// ResolveMappedModel 获取映射后的模型名，并返回是否命中了账号级映射。
// matched=true 表示命中了精确映射或通配符映射，即使映射结果与原模型名相同。
func ResolveMappedModel(platform string, mapping map[string]string, requestedModel string) (mappedModel string, matched bool) {
	if len(mapping) == 0 {
		return requestedModel, false
	}
	if mappedModel, matched := ResolveRequestedModelInMapping(mapping, requestedModel); matched {
		return mappedModel, true
	}
	normalized := NormalizeRequestedModelForLookup(platform, requestedModel)
	if normalized != requestedModel {
		if mappedModel, matched := ResolveRequestedModelInMapping(mapping, normalized); matched {
			return mappedModel, true
		}
	}
	return requestedModel, false
}

// ModelInFinalWhitelist 检查最终上游模型是否命中白名单。
// Gemini / Antigravity 仍复用既有归一化逻辑，避免 customtools 这类别名导致误判。
func ModelInFinalWhitelist(platform, model string, whitelist map[string]struct{}, normalizeQoder func(string) string) bool {
	if len(whitelist) == 0 {
		return true
	}
	if _, ok := whitelist[model]; ok {
		return true
	}
	if platform == PlatformQoder {
		modelKey := normalizeQoder(model)
		for allowedModel := range whitelist {
			if normalizeQoder(allowedModel) == modelKey {
				return true
			}
		}
		return false
	}
	normalized := NormalizeRequestedModelForLookup(platform, model)
	if normalized == model {
		return false
	}
	_, ok := whitelist[normalized]
	return ok
}

// HasUnrestrictedModelScope 保留 Antigravity 映射限制与显式最终白名单的区别。
func (a *Record) HasUnrestrictedModelScope(defaults ModelMappingDefaults) bool {
	if a == nil {
		return false
	}
	mapping := ResolveModelMapping(a, defaults)
	whitelist, _ := ResolveFinalModelWhitelist(a.Platform, a.Credentials, mapping)
	if len(whitelist) > 0 {
		return false
	}
	return a.Platform != PlatformAntigravity || len(mapping) == 0
}
