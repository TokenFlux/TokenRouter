package routing

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// GroupPolicyView 包含当前分组的路由策略和允许的协议。
type GroupPolicyView struct {
	GroupRoutingPolicy
	AllowedProtocols  []protocol.ProtocolID
	ProtocolFallbacks map[protocol.ProtocolID][]protocol.ProtocolID
	RequireOAuthOnly  bool
	// ModelsList 保存已启用的自定义展示型号，供目录生成候选。
	ModelsList []string
}

// PlatformBoolOverride 读取分组的布尔覆盖，兼容按平台保存的对象。
// 未配置或值类型不匹配时返回 nil，由调用方使用默认策略。
func PlatformBoolOverride(values map[string]any, key string, platform string) *bool {
	if values == nil {
		return nil
	}
	if v, ok := values[key].(bool); ok {
		return BoolOverridePtr(v)
	}
	if typed, ok := values[key].(map[string]bool); ok {
		if value, found := typed[platform]; found {
			return BoolOverridePtr(value)
		}
	}
	raw, ok := values[key].(map[string]any)
	if !ok {
		return nil
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return nil
	}
	if v, ok := raw[platform].(bool); ok {
		return BoolOverridePtr(v)
	}
	return nil
}

func BoolOverridePtr(v bool) *bool {
	return &v
}

// DecodeGroupRoutingPolicy 解析存储的策略，数据损坏时返回拒绝全部模型的策略。
func DecodeGroupRoutingPolicy(raw []byte) GroupRoutingPolicy {
	var policy GroupRoutingPolicy
	if len(raw) > 0 && json.Unmarshal(raw, &policy) != nil {
		return GroupRoutingPolicy{Enabled: true, RestrictModels: true}
	}
	return policy
}

// ValidateGroupRoutingPolicy 在写入前校验检查阶段及分组内的模型规则。
func ValidateGroupRoutingPolicy(p GroupRoutingPolicy) error {
	switch p.RestrictionModelSource {
	case "", BillingModelSourceRequested, BillingModelSourceGroupMapped, BillingModelSourceUpstream:
	default:
		return apperror.BadRequest("INVALID_ROUTING_POLICY", "invalid restriction_model_source")
	}
	if err := validateNoConflictingMappings(p.ModelMapping); err != nil {
		return err
	}
	for source, target := range p.ModelMapping {
		if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" || len(source) > 512 || len(target) > 512 || strings.Contains(strings.TrimSuffix(source, "*"), "*") {
			return apperror.BadRequest("INVALID_ROUTING_POLICY", "invalid model mapping")
		}
	}
	if len(p.AllowedModels) > 1000 {
		return apperror.BadRequest("INVALID_ROUTING_POLICY", "invalid model allowlist")
	}
	for _, model := range p.AllowedModels {
		if strings.TrimSpace(model) == "" || len(model) > 512 || strings.Contains(strings.TrimSuffix(model, "*"), "*") {
			return apperror.BadRequest("INVALID_ROUTING_POLICY", "invalid model allowlist pattern")
		}
	}
	return nil
}

// GetGroupPolicy 读取分组策略，生产装配优先使用当前请求的可信认证快照。
func (s *PricingConfigService) GetGroupPolicy(ctx context.Context, groupID int64) (*GroupPolicyView, error) {
	if s == nil || s.options.ReadGroup == nil {
		return nil, nil
	}
	group, err := s.options.ReadGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, nil
	}
	policy := group.RoutingPolicy.Clone()
	if !policy.Enabled {
		policy = GroupRoutingPolicy{}
	}
	var models []string
	if group.CustomModelsListEnabled() {
		models = append([]string(nil), group.ModelsListConfig.Models...)
	}
	return &GroupPolicyView{ModelsList: models, GroupRoutingPolicy: policy, AllowedProtocols: append([]protocol.ProtocolID{}, group.AllowedProtocols...), ProtocolFallbacks: protocol.CloneFallbacks(group.ProtocolFallbacks), RequireOAuthOnly: group.RequireOAuthOnly}, nil
}

func (p *GroupPolicyView) RestrictionSource() string {
	if p == nil || p.RestrictionModelSource == "" {
		return BillingModelSourceGroupMapped
	}
	return p.RestrictionModelSource
}

// ResolveModel 在分组内先精确匹配，再按有序前缀匹配，只改写一次。
func (p *GroupPolicyView) ResolveModel(model string) string {
	if p == nil {
		return model
	}
	rules := p.ModelMapping
	for _, source := range sortedModelMappingSources(rules) {
		if strings.EqualFold(source, model) {
			return rules[source]
		}
	}
	for _, source := range sortedModelMappingSources(rules) {
		if strings.HasSuffix(source, "*") && strings.HasPrefix(strings.ToLower(model), strings.ToLower(strings.TrimSuffix(source, "*"))) {
			return rules[source]
		}
	}
	return model
}

func (p *GroupPolicyView) IsModelRestricted(model string) bool {
	if p == nil || !p.RestrictModels {
		return false
	}
	model = pricing.NormalizePriceModelName(model)
	for _, pattern := range p.AllowedModels {
		wildcard := strings.HasSuffix(pattern, "*")
		prefix := pricing.NormalizePriceModelName(strings.TrimSuffix(pattern, "*"))
		if model == prefix || (wildcard && strings.HasPrefix(model, prefix)) {
			return false
		}
	}
	return true
}

// IsWebSearchEmulationEnabled 读取平台开关，配置值为非对象时返回 false。
func (p *GroupPolicyView) IsWebSearchEmulationEnabled(platform string) bool {
	if p == nil {
		return false
	}
	values, ok := p.FeaturesConfig[featureKeyWebSearchEmulation].(map[string]any)
	if !ok {
		return false
	}
	enabled, ok := values[platform].(bool)
	return ok && enabled
}

func (p *GroupPolicyView) IsBedrockCCCompatEnabled(platform string) bool {
	if p == nil {
		return false
	}
	value := PlatformBoolOverride(p.FeaturesConfig, featureKeyBedrockCCCompat, platform)
	return value != nil && *value
}

// ModelForRestriction 根据分组白名单检查阶段确定限制检查使用的模型。
// upstream 返回空（需逐提供商检查）。
func ModelForRestriction(source, requestedModel, groupMappedModel string) string {
	switch source {
	case BillingModelSourceRequested:
		return requestedModel
	case BillingModelSourceUpstream:
		return ""
	case BillingModelSourceGroupMapped:
		return groupMappedModel
	default:
		return groupMappedModel
	}
}
