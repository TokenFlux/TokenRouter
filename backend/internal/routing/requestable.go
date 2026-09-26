// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"context"
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// RequestableModel 描述客户端可请求的模型，以及模型广场应使用的定价模型。
type RequestableModel struct {
	ID               string
	PricingModel     string
	PricingAmbiguous bool
}

// RequestableModelsResult 是分组模型解析结果。
// Restricted 用于区分分组白名单后的空结果与旧版“没有显式模型”语义。
type RequestableModelsResult struct {
	Models                   []RequestableModel
	Restricted               bool
	HadExplicitAccountModels bool // 用于保持 /v1/models 的历史响应字段结构。
}

// ResolveWithAccounts 使用已预取账号解析模型，供模型广场避免逐分组重复查询。
func (s *RequestableResolver) ResolveWithAccounts(
	ctx context.Context,
	groupID *int64,
	platform string,
	baseModels []string,
	accounts []CatalogueAccount,
) RequestableModelsResult {
	accounts = filterRequestableModelAccounts(accounts, platform)
	// 账号查询成功但没有平台匹配账号时必须保持空结果；分组策略读取失败不能凭空补入默认模型。
	if len(accounts) == 0 {
		return RequestableModelsResult{}
	}
	currentAccountModels := ConfiguredRequestModelsFromAccounts(accounts, platform)
	hadExplicitAccountModels := len(baseModels) > 0 || len(currentAccountModels) > 0
	// 缓存层可能暂时为空或滞后，当前查询成功时仍要纳入账号白名单模型。
	accountCandidateModels := make([]string, 0, len(baseModels)+len(currentAccountModels))
	accountCandidateModels = append(accountCandidateModels, baseModels...)
	accountCandidateModels = append(accountCandidateModels, currentAccountModels...)

	var policy *GroupPolicyView
	policyPlatform := strings.TrimSpace(platform)
	var err error
	if groupID != nil && s.GroupPolicies != nil {
		policy, err = s.GroupPolicies.GetGroupPolicy(ctx, *groupID)
		if err != nil {
			s.Warn("failed to load group policy for requestable model resolution",
				"group_id", *groupID,
				"platform", platform,
				"error", err)
			return RequestableModelsResult{Restricted: true, HadExplicitAccountModels: hadExplicitAccountModels}
		}
		if cachedPlatform := strings.TrimSpace(s.GroupPolicies.GetGroupPlatform(ctx, *groupID)); cachedPlatform != "" {
			policyPlatform = cachedPlatform
		}
	}

	candidates := mergeRequestableModelCandidates(accountCandidateModels, accounts, policy, policyPlatform, s.Defaults)
	result := RequestableModelsResult{
		Restricted:               policy != nil && policy.RestrictModels,
		HadExplicitAccountModels: hadExplicitAccountModels,
	}
	if len(candidates) == 0 || len(accounts) == 0 {
		return result
	}

	result.Models = make([]RequestableModel, 0, len(candidates))
	for _, requestedModel := range candidates {
		if resolved, ok := s.resolveRequestableModel(ctx, groupID, policy, accounts, requestedModel); ok {
			result.Models = append(result.Models, resolved)
		}
	}
	return result
}

// ConfiguredRequestModelsFromAccounts 复用 GetAvailableModels 的显式模型聚合规则。
func ConfiguredRequestModelsFromAccounts(accounts []CatalogueAccount, platform string) []string {
	modelSet := make(map[string]struct{})
	hasConfiguredModels := false
	for i := range accounts {
		account := &accounts[i]
		if platform != "" && account.Platform != platform {
			continue
		}
		requestModels := account.Rules.ConfiguredModels()
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

func filterRequestableModelAccounts(accounts []CatalogueAccount, platform string) []CatalogueAccount {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return accounts
	}
	filtered := make([]CatalogueAccount, 0, len(accounts))
	for i := range accounts {
		if matchesCataloguePlatform(&accounts[i], platform) {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered
}

// mergeRequestableModelCandidates 按既有候选、分组策略、账号配置和默认模型的顺序合并候选。
// 通配符只参与后续匹配，不会作为模型 ID 返回。
func mergeRequestableModelCandidates(baseModels []string, accounts []CatalogueAccount, policy *GroupPolicyView, platform string, defaults CatalogueDefaults) []string {
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
		appendModels(policy.AllowedModels[platform]...)
		if mapping := policy.ModelMapping[platform]; len(mapping) > 0 {
			appendModels(sortedModelMappingSources(mapping)...)
		}
	}

	hasUnrestrictedAccount := false
	hasUnrestrictedQoderGlobal := false
	hasUnrestrictedQoderCN := false
	for i := range accounts {
		account := &accounts[i]
		appendModels(sortedModelMappingSources(account.Rules.Mapping())...)
		if account.Rules.Unrestricted() {
			hasUnrestrictedAccount = true
			if platform == PlatformQoder && account.Platform == PlatformQoder {
				if account.Rules.QoderCN() {
					hasUnrestrictedQoderCN = true
				} else {
					hasUnrestrictedQoderGlobal = true
				}
			}
		}
	}
	if hasUnrestrictedAccount {
		if platform == PlatformQoder {
			if hasUnrestrictedQoderGlobal {
				appendModels(defaults.Qoder(false)...)
			}
			if hasUnrestrictedQoderCN {
				appendModels(defaults.Qoder(true)...)
			}
		} else {
			appendModels(defaults.Platform(platform)...)
		}
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

func RequestableModelsFallback(models []string, platform string, defaults CatalogueDefaults) RequestableModelsResult {
	if len(models) == 0 {
		models = defaults.Platform(platform)
	}
	result := RequestableModelsResult{Models: make([]RequestableModel, 0, len(models))}
	seen := make(map[string]struct{}, len(models))
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
		result.Models = append(result.Models, RequestableModel{ID: model, PricingModel: model})
	}
	return result
}

func (s *RequestableResolver) resolveRequestableModel(
	ctx context.Context,
	groupID *int64,
	policy *GroupPolicyView,
	accounts []CatalogueAccount,
	requestedModel string,
) (RequestableModel, bool) {
	groupMappedModel := requestedModel
	billingSource := BillingModelSourceRequested
	if groupID != nil && s.GroupPolicies != nil {
		mapping := s.GroupPolicies.ResolveGroupMapping(ctx, *groupID, requestedModel)
		if mapped := strings.TrimSpace(mapping.MappedModel); mapped != "" {
			groupMappedModel = mapped
		}
		billingSource = mapping.BillingModelSource
		if billingSource == "" {
			billingSource = BillingModelSourceGroupMapped
		}
	}

	if policy != nil && policy.RestrictModels && policy.RestrictionSource() != BillingModelSourceUpstream {
		pricingModel := ModelForRestriction(policy.RestrictionSource(), requestedModel, groupMappedModel)
		if s.requestableModelRestricted(ctx, groupID, pricingModel) {
			return RequestableModel{}, false
		}
	}

	upstreamModels := make([]string, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if !account.Rules.Supports(ctx, groupMappedModel) {
			continue
		}
		for _, upstreamModel := range account.Rules.UpstreamModels(ctx, groupMappedModel) {
			if policy != nil && policy.RestrictModels && policy.RestrictionSource() == BillingModelSourceUpstream &&
				s.requestableModelRestricted(ctx, groupID, upstreamModel) {
				continue
			}
			upstreamModels = append(upstreamModels, upstreamModel)
		}
	}
	if len(upstreamModels) == 0 {
		return RequestableModel{}, false
	}

	resolved := RequestableModel{ID: requestedModel}
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

func (s *RequestableResolver) requestableModelRestricted(ctx context.Context, groupID *int64, pricingModel string) bool {
	if groupID == nil || s == nil || s.GroupPolicies == nil {
		return false
	}
	pricingModel = strings.TrimSpace(pricingModel)
	if pricingModel == "" || !s.GroupPolicies.IsModelRestricted(ctx, *groupID, pricingModel) {
		return false
	}
	return true
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

// CatalogueRules 封装平台专有资格及执行层模型观测，不暴露账号凭据。
type CatalogueRules interface {
	ConfiguredModels() []string
	Mapping() map[string]string
	Unrestricted() bool
	QoderCN() bool
	Supports(context.Context, string) bool
	UpstreamModels(context.Context, string) []string
}

// CatalogueAccount 只向目录编排提供可分组和排序的只读快照。
type CatalogueAccount struct {
	account.AccountSnapshot
	GroupIDs        []int64
	AccountGroupIDs []int64
	MixedScheduling bool
	Passthrough     bool
	Rules           CatalogueRules
}
type CatalogueDefaults struct {
	Platform func(string) []string
	Qoder    func(bool) []string
}
type CataloguePolicies interface {
	GetGroupPolicy(context.Context, int64) (*GroupPolicyView, error)
	GetGroupPlatform(context.Context, int64) string
	ResolveGroupMapping(context.Context, int64, string) GroupMappingResult
	IsModelRestricted(context.Context, int64, string) bool
}

// RequestableResolver 只编排目录规则，缓存和数据取得均由现有唯一来源提供。
type RequestableResolver struct {
	GroupPolicies CataloguePolicies
	Defaults      CatalogueDefaults
	Warn          func(string, ...any)
}

func matchesCataloguePlatform(account *CatalogueAccount, platform string) bool {
	if platform == PlatformAnthropic || platform == PlatformGemini {
		return account.Platform == platform || (account.Platform == PlatformAntigravity && account.MixedScheduling)
	}
	return account.Platform == platform
}
