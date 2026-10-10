package routing

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// RequestableModel 描述客户端可请求的模型，以及模型广场应使用的定价模型。
type RequestableModel struct {
	// UpstreamModels 保存已确认可请求的最终模型，供展示使用。
	UpstreamModels []string
	Protocols      []capability.ProtocolID
	// NativeProtocols 是 Protocols 里每个承接该模型的提供商都能直接处理的协议，请求按这些协议进入时不经过协议转换。
	NativeProtocols  []capability.ProtocolID
	ID               string
	PricingModel     string
	PricingAmbiguous bool
}

// RequestableModelsResult 是分组模型解析结果。
// Restricted 用于区分分组白名单后的空结果与旧版“没有显式模型”语义。
type RequestableModelsResult struct {
	Models                    []RequestableModel
	Restricted                bool
	HadExplicitProviderModels bool // 用于保持 /v1/models 的历史响应字段结构。
}

// CatalogueRules 提供平台资格、模型映射和执行时观测到的模型信息。
type CatalogueRules interface {
	ConfiguredModels() []string
	Mapping() map[string]string
	Supports(context.Context, string) bool
	UpstreamModels(context.Context, string) []string
}

// CatalogueProvider 只向目录编排提供可分组和排序的只读快照。
type CatalogueProvider struct {
	provider.ProviderSnapshot
	GroupIDs         []int64
	ProviderGroupIDs []int64
	Passthrough      bool
	Rules            CatalogueRules
}
type CataloguePolicies interface {
	GetGroupPolicy(context.Context, int64) (*GroupPolicyView, error)
	GetPricingConfigForGroup(context.Context, int64) (*PricingConfig, error)
}

// RequestableResolver 按分组策略校验已配置的候选及指定型号。
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_resolution
type RequestableResolver struct {
	GroupPolicies CataloguePolicies
	Warn          func(string, ...any)
}

// requestableQuery 保存一次分组查询的规则和有限候选。
type requestableQuery struct {
	providers     []CatalogueProvider
	protocols     []capability.ProviderProtocols
	policy        *GroupPolicyView
	billingSource string
	candidates    []string
	result        RequestableModelsResult
}

// ResolveWithProviders 校验配置中的具体型号，提供商可由多分组查询预取。
func (s *RequestableResolver) ResolveWithProviders(ctx context.Context, groupID *int64, platform string, baseModels []string, providers []CatalogueProvider) RequestableModelsResult {
	query := s.prepare(ctx, groupID, platform, baseModels, providers)
	result := query.result
	for _, requestedModel := range query.candidates {
		if ctx.Err() != nil {
			return RequestableModelsResult{Restricted: true}
		}
		if resolved, ok := query.resolve(ctx, requestedModel); ok {
			result.Models = append(result.Models, resolved)
		}
	}
	return result
}

// ResolveSelectedWithProviders 校验指定的有限型号集合，供单型号查询及其 Key 别名使用。
func (s *RequestableResolver) ResolveSelectedWithProviders(ctx context.Context, groupID *int64, platform string, selected []string, providers []CatalogueProvider) RequestableModelsResult {
	query := s.prepare(ctx, groupID, platform, nil, providers)
	result := query.result
	seen := make(map[string]bool, len(selected))
	for _, model := range selected {
		if ctx.Err() != nil {
			return RequestableModelsResult{Restricted: true}
		}
		if seen[model] || !slices.Contains(query.candidates, model) {
			continue
		}
		seen[model] = true
		if resolved, ok := query.resolve(ctx, model); ok {
			result.Models = append(result.Models, resolved)
		}
	}
	return result
}

func (s *RequestableResolver) prepare(ctx context.Context, groupID *int64, platform string, baseModels []string, providers []CatalogueProvider) requestableQuery {
	providers = filterRequestableModelProviders(providers, platform)
	query := requestableQuery{providers: providers, billingSource: BillingModelSourceRequested}
	if len(providers) == 0 {
		return query
	}
	currentModels := ConfiguredRequestModelsFromProviders(providers, platform)
	query.result.HadExplicitProviderModels = len(baseModels) > 0 || len(currentModels) > 0
	if groupID != nil && s.GroupPolicies != nil {
		policy, err := s.GroupPolicies.GetGroupPolicy(ctx, *groupID)
		if err != nil {
			if s.Warn != nil {
				s.Warn("failed to load group policy for requestable model resolution", "group_id", *groupID, "platform", platform, "error", err)
			}
			query.result.Restricted = true
			return query
		}
		query.policy = policy
		query.billingSource = BillingModelSourceGroupMapped
		if config, err := s.GroupPolicies.GetPricingConfigForGroup(ctx, *groupID); err == nil && config != nil && config.BillingModelSource != "" {
			query.billingSource = config.BillingModelSource
		}
	}
	query.result.Restricted = query.policy != nil && query.policy.RestrictModels
	configured := make([]string, 0, len(baseModels)+len(currentModels))
	configured = append(configured, baseModels...)
	configured = append(configured, currentModels...)
	query.candidates = mergeRequestableModelCandidates(configured, providers, query.policy)
	query.protocols = make([]capability.ProviderProtocols, len(providers))
	for i := range providers {
		query.protocols[i] = providers[i].Protocols()
	}
	return query
}

// ConfiguredRequestModelsFromProviders 聚合提供商配置中的具体请求型号。
func ConfiguredRequestModelsFromProviders(providers []CatalogueProvider, platform string) []string {
	modelSet := make(map[string]struct{})
	hasConfiguredModels := false
	for i := range providers {
		provider := &providers[i]
		if platform != "" && provider.Platform != platform {
			continue
		}
		requestModels := provider.Rules.ConfiguredModels()
		if len(requestModels) == 0 {
			continue
		}
		hasConfiguredModels = true
		for _, model := range requestModels {
			modelSet[model] = struct{}{}
		}
	}
	if !hasConfiguredModels {
		return nil
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

func filterRequestableModelProviders(providers []CatalogueProvider, platform string) []CatalogueProvider {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return providers
	}
	filtered := make([]CatalogueProvider, 0, len(providers))
	for i := range providers {
		if matchesCataloguePlatform(&providers[i], platform) {
			filtered = append(filtered, providers[i])
		}
	}
	return filtered
}

// mergeRequestableModelCandidates 合并提供商配置、分组策略和自定义列表中的具体名称。
// 通配符用于后续匹配，返回列表包含具体的模型 ID。
func mergeRequestableModelCandidates(baseModels []string, providers []CatalogueProvider, policy *GroupPolicyView) []string {
	candidates := make([]string, 0, len(baseModels)+16)
	seen := make(map[string]struct{}, len(baseModels)+16)
	appendModels := func(models ...string) {
		for _, model := range models {
			model = strings.TrimSpace(model)
			if model == "" || strings.Contains(model, "*") {
				continue
			}
			key := strings.ToLower(model)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, model)
		}
	}

	appendModels(baseModels...)
	if policy != nil {
		appendModels(policy.AllowedModels...)
		appendModels(policy.ModelsList...)
		if mapping := policy.ModelMapping; len(mapping) > 0 {
			appendModels(sortedModelMappingSources(mapping)...)
		}
	}

	for i := range providers {
		appendModels(sortedModelMappingSources(providers[i].Rules.Mapping())...)
	}

	return candidates
}

func sortedModelMappingSources(mapping map[string]string) []string {
	if len(mapping) == 0 {
		return nil
	}
	models := make([]string, 0, len(mapping))
	for model := range mapping {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool {
		left := strings.ToLower(models[i])
		right := strings.ToLower(models[j])
		if left != right {
			return left < right
		}
		return models[i] < models[j]
	})
	return models
}

// resolve 用本次查询的规则校验单个请求型号。
func (q *requestableQuery) resolve(ctx context.Context, requestedModel string) (RequestableModel, bool) {
	policy, providers, billingSource := q.policy, q.providers, q.billingSource
	groupMappedModel := requestedModel
	if mapped := strings.TrimSpace(policy.ResolveModel(requestedModel)); mapped != "" {
		groupMappedModel = mapped
	}

	if policy != nil && policy.RestrictModels && policy.RestrictionSource() != BillingModelSourceUpstream {
		pricingModel := ModelForRestriction(policy.RestrictionSource(), requestedModel, groupMappedModel)
		if policy.IsModelRestricted(pricingModel) {
			return RequestableModel{}, false
		}
	}

	upstreamModels := make([]string, 0, len(providers))
	var protocols []capability.ProtocolID
	// nativeByProtocol 记录协议是否在每个承接该模型的提供商上都走原生路线。
	nativeByProtocol := map[capability.ProtocolID]bool{}
	for i := range providers {
		provider := &providers[i]
		var candidateProtocols []capability.ProtocolID
		var nativeCandidates []capability.ProtocolID
		if policy != nil && policy.AllowedProtocols != nil {
			if policy.RequireOAuthOnly && provider.Type == capability.ProviderTypeAPIKey {
				continue
			}
			for _, source := range policy.AllowedProtocols {
				target, ok := capability.ResolveRoute(q.protocols[i], source, policy.ProtocolFallbacks)
				if !ok {
					continue
				}
				if aware, ok := provider.Rules.(interface {
					SupportsClientProtocol(string, capability.ProtocolID) bool
				}); ok && !aware.SupportsClientProtocol(groupMappedModel, source) {
					continue
				}
				candidateProtocols = append(candidateProtocols, source)
				if target == source {
					nativeCandidates = append(nativeCandidates, source)
				}
			}
			if len(candidateProtocols) == 0 {
				continue
			}
		}
		if !provider.Rules.Supports(ctx, groupMappedModel) {
			continue
		}
		contributed := false
		for _, upstreamModel := range provider.Rules.UpstreamModels(ctx, groupMappedModel) {
			if policy != nil && policy.RestrictModels && policy.RestrictionSource() == BillingModelSourceUpstream &&
				policy.IsModelRestricted(upstreamModel) {
				continue
			}
			upstreamModels = append(upstreamModels, upstreamModel)
			contributed = true
		}
		if !contributed {
			continue
		}
		for _, source := range candidateProtocols {
			native := slices.Contains(nativeCandidates, source)
			if previous, seen := nativeByProtocol[source]; seen {
				nativeByProtocol[source] = previous && native
				continue
			}
			nativeByProtocol[source] = native
			protocols = append(protocols, source)
		}
	}
	if len(upstreamModels) == 0 {
		return RequestableModel{}, false
	}

	var nativeProtocols []capability.ProtocolID
	for _, source := range protocols {
		if nativeByProtocol[source] {
			nativeProtocols = append(nativeProtocols, source)
		}
	}
	slices.Sort(upstreamModels)
	resolved := RequestableModel{ID: requestedModel, Protocols: protocols, NativeProtocols: nativeProtocols, UpstreamModels: slices.Compact(upstreamModels)}
	switch billingSource {
	case BillingModelSourceRequested:
		resolved.PricingModel = requestedModel
	case BillingModelSourceUpstream:
		resolved.PricingModel, resolved.PricingAmbiguous = uniquePricingModel(upstreamModels)
	default:
		resolved.PricingModel = groupMappedModel
	}
	return resolved, true
}

func uniquePricingModel(models []string) (string, bool) {
	var selected string
	selectedKey := ""
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if selectedKey == "" {
			selected = model
			selectedKey = key
			continue
		}
		if key != selectedKey {
			return "", true
		}
	}
	return selected, false
}

// RequestableModelIDs 返回保持解析顺序的客户端模型 ID。
func RequestableModelIDs(models []RequestableModel) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// matchesCataloguePlatform 只把平台参数用于专用入口的强制过滤。
func matchesCataloguePlatform(provider *CatalogueProvider, platform string) bool {
	return platform == "" || provider.Platform == platform
}
