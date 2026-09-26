package creative

import (
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// groupModelPolicy 固定一次目录查询或执行尝试的分组策略，价格配置不参与模型准入。
type groupModelPolicy struct {
	view *routing.GroupPolicyView
}

func newGroupModelPolicy(platform string, policy routing.GroupRoutingPolicy) groupModelPolicy {
	if !policy.Enabled {
		return groupModelPolicy{}
	}
	return groupModelPolicy{view: &routing.GroupPolicyView{
		GroupRoutingPolicy: policy.Clone(),
		Platform:           platform,
	}}
}

// resolve 只执行一次分组映射；上游阶段留给具体账号解析完成后检查。
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

// candidates 合并可枚举的请求名称；通配符只用于匹配，不作为可选模型返回。
func (p groupModelPolicy) candidates(platform string, configured []string, accounts []CatalogAccount) []string {
	models := make(map[string]struct{})
	add := func(values ...string) {
		for _, model := range values {
			model = strings.TrimSpace(model)
			if model != "" && !strings.ContainsAny(model, "*?") {
				models[model] = struct{}{}
			}
		}
	}
	switch platform {
	case PlatformOpenAI:
		add(DefaultCreativeOpenAIModelCandidates()...)
	case PlatformGemini:
		add(DefaultCreativeGeminiModelCandidates()...)
	case PlatformGrok:
		add(DefaultCreativeGrokModelCandidates()...)
	}
	add(configured...)
	if p.view != nil {
		add(p.view.AllowedModels[platform]...)
		for model := range p.view.ModelMapping[platform] {
			add(model)
		}
	}
	for _, account := range accounts {
		if account == nil || !account.IsSchedulable() {
			continue
		}
		add(account.GetConfiguredRequestModels()...)
		for model := range account.GetModelMapping() {
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
