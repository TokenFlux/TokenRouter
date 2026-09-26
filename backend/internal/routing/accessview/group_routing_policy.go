package accessview

import (
	"maps"
	"slices"
)

// @project-doc docs/domains/gateway_policy_controls.md#group_routing_policy
// GroupRoutingPolicy 保存分组独立的路由与功能策略；价格配置不参与这些规则。
type GroupRoutingPolicy struct {
	Enabled                bool                         `json:"enabled"`
	ModelMapping           map[string]map[string]string `json:"model_mapping"`
	RestrictModels         bool                         `json:"restrict_models"`
	RestrictionModelSource string                       `json:"restriction_model_source"`
	AllowedModels          map[string][]string          `json:"allowed_models"`
	Features               string                       `json:"features"`
	FeaturesConfig         map[string]any               `json:"features_config"`
}

// Clone 为认证快照和管理请求隔离嵌套配置。
func (p GroupRoutingPolicy) Clone() GroupRoutingPolicy {
	out := p
	out.ModelMapping = maps.Clone(p.ModelMapping)
	for platform, rules := range out.ModelMapping {
		out.ModelMapping[platform] = maps.Clone(rules)
	}
	out.AllowedModels = maps.Clone(p.AllowedModels)
	for platform, models := range out.AllowedModels {
		out.AllowedModels[platform] = slices.Clone(models)
	}
	out.FeaturesConfig = DeepCopyFeaturesConfig(p.FeaturesConfig)
	return out
}

// DeepCopyFeaturesConfig 隔离配置中的嵌套 JSON 值，保持原数值类型。
func DeepCopyFeaturesConfig(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		if inner, ok := value.(map[string]any); ok {
			dst[key] = DeepCopyFeaturesConfig(inner)
		} else {
			dst[key] = cloneFeatureValue(value)
		}
	}
	return dst
}

func cloneFeatureValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		if v == nil {
			return map[string]any(nil)
		}
		return DeepCopyFeaturesConfig(v)
	case []any:
		if v == nil {
			return []any(nil)
		}
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneFeatureValue(v[i])
		}
		return out
	case map[string]bool:
		return maps.Clone(v)
	case map[string]string:
		return maps.Clone(v)
	case []string:
		return slices.Clone(v)
	default:
		return value
	}
}
