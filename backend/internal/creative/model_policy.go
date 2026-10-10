package creative

import (
	"context"
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// groupModelPolicy 固定一次目录查询或执行尝试的分组策略，价格配置不参与模型准入。
type groupModelPolicy struct {
	view *routing.GroupPolicyView
}

// creativeModelQuery 复用一个分组的提供商及模型规则。
type creativeModelQuery struct {
	group     *GroupView
	policy    groupModelPolicy
	providers map[string][]CatalogProvider
}

func newGroupModelPolicy(policy routing.GroupRoutingPolicy) groupModelPolicy {
	if !policy.Enabled {
		return groupModelPolicy{}
	}
	return groupModelPolicy{view: &routing.GroupPolicyView{
		GroupRoutingPolicy: policy.Clone(),
	}}
}

// resolve 只执行一次分组映射；上游阶段留给具体提供商解析完成后检查。
func (p groupModelPolicy) resolve(requested string) (string, bool) {
	mapped := p.view.ResolveModel(requested)
	if p.view.RestrictionSource() != routing.BillingModelSourceUpstream {
		model := routing.ModelForRestriction(p.view.RestrictionSource(), requested, mapped)
		if p.view.IsModelRestricted(model) {
			return "", false
		}
	}
	return mapped, true
}

func (p groupModelPolicy) allowsUpstream(model string) bool {
	return p.view.RestrictionSource() != routing.BillingModelSourceUpstream || !p.view.IsModelRestricted(model)
}

// candidates 合并可枚举的请求名称，返回可选模型列表。通配符留给后续匹配使用。
func (p groupModelPolicy) candidates(configured []string, providers []CatalogProvider) []string {
	models := make(map[string]struct{})
	add := func(values ...string) {
		for _, model := range values {
			model = strings.TrimSpace(model)
			if model != "" && !strings.ContainsAny(model, "*?") {
				models[model] = struct{}{}
			}
		}
	}
	add(configured...)
	if p.view != nil {
		add(p.view.AllowedModels...)
		for source, target := range p.view.ModelMapping {
			add(source, target)
		}
	}
	for _, provider := range providers {
		if provider == nil || !provider.IsSchedulable() {
			continue
		}
		add(provider.GetConfiguredRequestModels()...)
		for model := range provider.GetModelMapping() {
			add(model)
		}
	}
	result := make([]string, 0, len(models))
	for model := range models {
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}

// prepareCreativeModels 按已开放的操作读取提供商，每个平台读取一次。
func (s *Public) prepareCreativeModels(ctx context.Context, group *GroupView) (*creativeModelQuery, error) {
	if s.ProviderRepo == nil || group == nil || group.ClaudeCodeOnly {
		return nil, nil
	}
	query := &creativeModelQuery{group: group, policy: newGroupModelPolicy(group.RoutingPolicy), providers: map[string][]CatalogProvider{}}
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, PlatformGrok} {
		if len(group.Operations[platform]) == 0 {
			continue
		}
		providers, err := s.ProviderRepo.ListSchedulableByGroupIDAndPlatform(ctx, group.ID, platform)
		if err != nil {
			return nil, err
		}
		query.providers[platform] = providers
	}
	return query, nil
}

func creativeSettingsModels(settings []CreativeModelSetting, groupID int64) []string {
	var models []string
	for _, setting := range settings {
		if setting.GroupID == groupID {
			models = append(models, setting.Model)
		}
	}
	return models
}

func (q *creativeModelQuery) candidates(configured []string) []string {
	if q == nil {
		return nil
	}
	var providers []CatalogProvider
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, PlatformGrok} {
		providers = append(providers, q.providers[platform]...)
	}
	models := append([]string(nil), configured...)
	models = append(models, q.group.ModelsList...)
	return q.policy.candidates(models, providers)
}

func (q *creativeModelQuery) resolveModels(ctx context.Context, models []string) map[string]creativeModelRoute {
	out := make(map[string]creativeModelRoute)
	for _, model := range models {
		if ctx.Err() != nil {
			return map[string]creativeModelRoute{}
		}
		if _, exists := out[model]; exists {
			continue
		}
		if route, ok := q.resolve(ctx, model); ok {
			out[model] = route
		}
	}
	return out
}

// resolve 校验具体型号，并按 OpenAI、Gemini、Grok 的顺序选择图片能力。
func (q *creativeModelQuery) resolve(ctx context.Context, model string) (creativeModelRoute, bool) {
	if q == nil || ctx.Err() != nil || strings.TrimSpace(model) == "" || strings.ContainsAny(model, "*?") {
		return creativeModelRoute{}, false
	}
	mapped, allowed := q.policy.resolve(model)
	if !allowed {
		return creativeModelRoute{}, false
	}
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, PlatformGrok} {
		for _, provider := range q.providers[platform] {
			if provider == nil || provider.PlatformID() != platform || !provider.IsSchedulable() || !provider.IsModelSupported(mapped) {
				continue
			}
			finalModel := mappedCatalogModel(provider, mapped)
			if !CreativePlatformImageModel(platform, finalModel) || !q.policy.allowsUpstream(finalModel) {
				continue
			}
			var operations []string
			for _, operation := range q.group.Operations[platform] {
				if provider.AllowsProtocol(OperationProtocol(platform, operation), q.group.ProtocolFallbacks) {
					operations = append(operations, operation)
				}
			}
			if len(operations) > 0 {
				return creativeModelRoute{Platform: platform, Model: finalModel, Operations: operations}, true
			}
		}
	}
	return creativeModelRoute{}, false
}
