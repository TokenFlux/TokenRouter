package provider

import (
	"strings"
)

// ModelPlatformRules 只在平台专有资格分支按需调用，模型配置与一跳匹配由 provider 拥有。
type ModelPlatformRules struct {
	NormalizeQoder      func(string) string
	QoderCompatible     func(string) bool
	OpenAIOAuthServable func(string) bool
}

// IsModelSupported 在提供商映射后检查白名单和平台执行资格。
func (r *Record) IsModelSupported(requestedModel string, defaults ModelMappingDefaults, rules ModelPlatformRules) bool {
	if r == nil {
		return false
	}
	mapping := ResolveModelMapping(r, defaults)
	model, _ := ResolveMappedModel(mapping, requestedModel)
	scope := r.effectiveModelScope(defaults, mapping)
	if !ModelInFinalWhitelist(r.Platform, model, scope, rules.NormalizeQoder) {
		return false
	}
	return r.modelPlatformAllows(model, rules)
}

// modelPlatformAllows 检查型号的站点和账号资格。
func (r *Record) modelPlatformAllows(model string, rules ModelPlatformRules) bool {
	if r.Platform == PlatformQoder && rules.QoderCompatible != nil && !rules.QoderCompatible(model) {
		return false
	}
	if r.IsOpenAIOAuth() && rules.OpenAIOAuthServable != nil {
		return rules.OpenAIOAuthServable(model)
	}
	return true
}

// FinalModelWhitelisted 直接检查已映射的上游模型名是否在白名单中。
func (r *Record) FinalModelWhitelisted(model string, defaults ModelMappingDefaults, rules ModelPlatformRules) bool {
	if r == nil {
		return false
	}
	if r.Platform == PlatformQoder && rules.QoderCompatible != nil && !rules.QoderCompatible(model) {
		return false
	}
	return ModelInFinalWhitelist(r.Platform, model, r.effectiveModelScope(defaults, ResolveModelMapping(r, defaults)), rules.NormalizeQoder)
}

// effectiveModelScope 空白名单允许任意型号，Spark 影子使用独立的硬限制。
func (r *Record) effectiveModelScope(defaults ModelMappingDefaults, mapping map[string]string) map[string]struct{} {
	whitelist, explicit := ResolveFinalModelWhitelist(r.Platform, r.Credentials, mapping)
	// Spark 影子只有独立模型配额，明确的通配符也不能扩大其硬能力。
	if r.IsShadow() && r.Platform == PlatformOpenAI {
		scope := make(map[string]struct{})
		if defaults.Models != nil {
			for _, model := range defaults.Models(r) {
				if len(whitelist) == 0 || ModelInFinalWhitelist(r.Platform, model, whitelist, nil) {
					scope[model] = struct{}{}
				}
			}
		}
		return scope
	}
	if explicit && len(whitelist) > 0 {
		return whitelist
	}
	return map[string]struct{}{"*": {}}
}

// GetConfiguredRequestModels 枚举配置中的具体型号和执行路由别名。
func (r *Record) GetConfiguredRequestModels(defaults ModelMappingDefaults) []string {
	if r == nil {
		return nil
	}
	mapping := ResolveModelMapping(r, defaults)
	scope := r.effectiveModelScope(defaults, mapping)
	var platformModels []string
	if defaults.Models != nil {
		platformModels = defaults.Models(r)
	}
	return configuredRequestModels(mapping, scope, platformModels, func(model string) bool { return ModelInFinalWhitelist(r.Platform, model, scope, nil) })
}

// ResolveMappedModel 获取映射后的模型名，并返回是否命中了提供商级映射。
// matched=true 表示命中了精确映射或通配符映射，即使映射结果与原模型名相同。
func ResolveMappedModel(mapping map[string]string, requestedModel string) (mappedModel string, matched bool) {
	if len(mapping) == 0 {
		return requestedModel, false
	}
	if mappedModel, matched := ResolveRequestedModelInMapping(mapping, requestedModel); matched {
		return mappedModel, true
	}
	normalized := strings.TrimSpace(requestedModel)
	if normalized != requestedModel {
		if mappedModel, matched := ResolveRequestedModelInMapping(mapping, normalized); matched {
			return mappedModel, true
		}
	}
	return requestedModel, false
}

// ModelInFinalWhitelist 检查最终模型是否命中白名单，Qoder 按路由键比较别名。
func ModelInFinalWhitelist(platform, model string, whitelist map[string]struct{}, normalizeQoder func(string) string) bool {
	if len(whitelist) == 0 {
		return false
	}
	model = strings.TrimSpace(model)
	for pattern := range whitelist {
		pattern = strings.TrimSpace(pattern)
		if strings.EqualFold(model, pattern) || (strings.HasSuffix(pattern, "*") && strings.HasPrefix(strings.ToLower(model), strings.ToLower(strings.TrimSuffix(pattern, "*")))) {
			return true
		}
		if platform == PlatformQoder && normalizeQoder != nil && normalizeQoder(pattern) == normalizeQoder(model) {
			return true
		}
	}
	return false
}

// HasUnrestrictedModelScope 判断当前提供商是否允许任意型号。
func (r *Record) HasUnrestrictedModelScope(defaults ModelMappingDefaults) bool {
	if r == nil {
		return false
	}
	_, all := r.effectiveModelScope(defaults, ResolveModelMapping(r, defaults))["*"]
	return all
}
