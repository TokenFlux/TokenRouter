package service

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
)

// RequestableModel 描述客户端可请求的模型，以及模型广场应使用的定价模型。
type RequestableModel struct {
	ID               string
	PricingModel     string
	PricingAmbiguous bool
}

// RequestableModelsResult 是分组模型解析结果。
// Restricted 用于区分渠道限制后的空结果与旧版“没有显式模型”语义。
type RequestableModelsResult struct {
	Models                   []RequestableModel
	Restricted               bool
	HadExplicitAccountModels bool // 用于保持 /v1/models 的历史响应字段结构。
}

// ResolveRequestableModels 统一解析模型列表中的 R -> C -> U 链路。
// 只有至少一个平台匹配账号能够处理的客户端模型才会出现在结果中。
func (s *GatewayService) ResolveRequestableModels(ctx context.Context, groupID *int64, platform string) RequestableModelsResult {
	if s == nil || s.accountRepo == nil {
		return RequestableModelsResult{}
	}

	baseModels := s.GetAvailableModels(ctx, groupID, platform)
	accounts, err := s.listRequestableModelAccounts(ctx, groupID)
	if err != nil {
		slog.Warn("failed to load accounts for requestable model resolution",
			"group_id", derefGroupID(groupID),
			"platform", platform,
			"error", err)
		fallback := requestableModelsFallback(baseModels, platform)
		fallback.HadExplicitAccountModels = len(baseModels) > 0
		return fallback
	}
	return s.resolveRequestableModelsWithAccounts(ctx, groupID, platform, baseModels, accounts)
}

// resolveRequestableModelsWithAccounts 使用已预取账号解析模型，供模型广场避免逐分组重复查询。
func (s *GatewayService) resolveRequestableModelsWithAccounts(
	ctx context.Context,
	groupID *int64,
	platform string,
	baseModels []string,
	accounts []Account,
) RequestableModelsResult {
	accounts = filterRequestableModelAccounts(accounts, platform)
	// 账号查询成功但没有平台匹配账号时必须保持空结果；渠道读取失败不能凭空补入默认模型。
	if len(accounts) == 0 {
		return RequestableModelsResult{}
	}
	currentAccountModels := configuredRequestModelsFromAccounts(accounts, platform)
	hadExplicitAccountModels := len(baseModels) > 0 || len(currentAccountModels) > 0
	// 缓存层可能暂时为空或滞后，当前查询成功时仍要纳入账号白名单模型。
	accountCandidateModels := make([]string, 0, len(baseModels)+len(currentAccountModels))
	accountCandidateModels = append(accountCandidateModels, baseModels...)
	accountCandidateModels = append(accountCandidateModels, currentAccountModels...)

	var channel *Channel
	channelPlatform := strings.TrimSpace(platform)
	var err error
	if groupID != nil && s.channelService != nil {
		channel, err = s.channelService.GetChannelForGroup(ctx, *groupID)
		if err != nil {
			slog.Warn("failed to load channel for requestable model resolution",
				"group_id", *groupID,
				"platform", platform,
				"error", err)
			fallback := requestableModelsFallback(mergeRequestableModelCandidates(accountCandidateModels, accounts, nil, channelPlatform), platform)
			fallback.HadExplicitAccountModels = hadExplicitAccountModels
			return fallback
		}
		if cachedPlatform := strings.TrimSpace(s.channelService.GetGroupPlatform(ctx, *groupID)); cachedPlatform != "" {
			channelPlatform = cachedPlatform
		}
	}

	candidates := mergeRequestableModelCandidates(accountCandidateModels, accounts, channel, channelPlatform)
	result := RequestableModelsResult{
		Restricted:               channel != nil && channel.RestrictModels,
		HadExplicitAccountModels: hadExplicitAccountModels,
	}
	if len(candidates) == 0 || len(accounts) == 0 {
		return result
	}

	result.Models = make([]RequestableModel, 0, len(candidates))
	for _, requestedModel := range candidates {
		if resolved, ok := s.resolveRequestableModel(ctx, groupID, channel, accounts, requestedModel); ok {
			result.Models = append(result.Models, resolved)
		}
	}
	return result
}

// configuredRequestModelsFromAccounts 复用 GetAvailableModels 的显式模型聚合规则。
func configuredRequestModelsFromAccounts(accounts []Account, platform string) []string {
	modelSet := make(map[string]struct{})
	hasConfiguredModels := false
	for i := range accounts {
		account := &accounts[i]
		if platform != "" && account.Platform != platform {
			continue
		}
		requestModels := account.GetConfiguredRequestModels()
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

// listRequestableModelAccounts 保持与 GetAvailableModels 相同的账号查询边界。
func (s *GatewayService) listRequestableModelAccounts(ctx context.Context, groupID *int64) ([]Account, error) {
	if s == nil || s.accountRepo == nil {
		return nil, nil
	}
	if groupID != nil {
		return s.accountRepo.ListSchedulableByGroupID(ctx, *groupID)
	}
	return s.accountRepo.ListSchedulable(ctx)
}

func filterRequestableModelAccounts(accounts []Account, platform string) []Account {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return accounts
	}
	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if accountMatchesModelListPlatform(&accounts[i], platform) {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered
}

// mergeRequestableModelCandidates 按既有候选、渠道配置、账号配置和默认模型的顺序合并候选。
// 通配符只参与后续匹配，不会作为模型 ID 返回。
func mergeRequestableModelCandidates(baseModels []string, accounts []Account, channel *Channel, platform string) []string {
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
	if channel != nil {
		for i := range channel.ModelPricing {
			pricing := &channel.ModelPricing[i]
			if pricing.Platform == platform {
				appendModels(pricing.Models...)
			}
		}
		if mapping := channel.ModelMapping[platform]; len(mapping) > 0 {
			appendModels(sortedModelMappingSources(mapping)...)
		}
	}

	hasUnrestrictedAccount := false
	hasUnrestrictedQoderGlobal := false
	hasUnrestrictedQoderCN := false
	for i := range accounts {
		account := &accounts[i]
		appendModels(sortedModelMappingSources(account.GetModelMapping())...)
		if accountHasUnrestrictedModelScope(account) {
			hasUnrestrictedAccount = true
			if platform == PlatformQoder && account.Platform == PlatformQoder {
				if site, err := qoderSiteForAccount(account); err == nil && site == qoder.SiteCN {
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
				appendModels(qoder.DefaultRequestModelIDsForSite(qoder.SiteGlobal)...)
			}
			if hasUnrestrictedQoderCN {
				appendModels(qoder.DefaultRequestModelIDsForSite(qoder.SiteCN)...)
			}
		} else {
			appendModels(defaultRequestModelIDsForPlatform(platform)...)
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

func accountHasUnrestrictedModelScope(account *Account) bool {
	if account == nil {
		return false
	}
	mapping := account.GetModelMapping()
	whitelist, _ := resolveFinalModelWhitelist(account.Platform, account.Credentials, mapping)
	if len(whitelist) > 0 {
		return false
	}
	// Antigravity 仍以映射命中表示平台能力，存在映射时不能视作支持全部默认模型。
	return account.Platform != PlatformAntigravity || len(mapping) == 0
}

func requestableModelsFallback(models []string, platform string) RequestableModelsResult {
	if len(models) == 0 {
		models = defaultRequestModelIDsForPlatform(platform)
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

func (s *GatewayService) resolveRequestableModel(
	ctx context.Context,
	groupID *int64,
	channel *Channel,
	accounts []Account,
	requestedModel string,
) (RequestableModel, bool) {
	channelMappedModel := requestedModel
	billingSource := BillingModelSourceRequested
	if groupID != nil && channel != nil && s.channelService != nil {
		mapping := s.channelService.ResolveChannelMapping(ctx, *groupID, requestedModel)
		if mapped := strings.TrimSpace(mapping.MappedModel); mapped != "" {
			channelMappedModel = mapped
		}
		billingSource = mapping.BillingModelSource
		if billingSource == "" {
			billingSource = BillingModelSourceChannelMapped
		}
	}

	if channel != nil && channel.RestrictModels && billingSource != BillingModelSourceUpstream {
		pricingModel := billingModelForRestriction(billingSource, requestedModel, channelMappedModel)
		if s.requestableModelRestricted(ctx, groupID, pricingModel) {
			return RequestableModel{}, false
		}
	}

	upstreamModels := make([]string, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if !s.isRoutingModelSupportedByAccountWithContext(ctx, account, channelMappedModel) {
			continue
		}
		for _, upstreamModel := range s.resolveAccountUpstreamModelsForListing(ctx, account, channelMappedModel) {
			if channel != nil && channel.RestrictModels && billingSource == BillingModelSourceUpstream &&
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
		resolved.PricingModel = channelMappedModel
	}
	return resolved, true
}

// resolveAccountUpstreamModelsForListing 返回静态模型列表可能产生的最终上游模型。
// Antigravity thinking 由请求体决定，因此需要同时纳入普通版和 thinking 版。
func (s *GatewayService) resolveAccountUpstreamModelsForListing(ctx context.Context, account *Account, requestedModel string) []string {
	models := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	appendModel := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		key := strings.ToLower(model)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		models = append(models, model)
	}

	if account != nil && account.Platform == PlatformAntigravity {
		plainCtx := WithThinkingEnabled(ctx, false, false)
		if account.modelRateLimitAllowsScheduling(plainCtx, requestedModel) &&
			s.isRoutingModelSupportedByAccountWithContext(plainCtx, account, requestedModel) {
			appendModel(resolveAccountUpstreamModel(plainCtx, account, requestedModel))
		}
		thinkingCtx := WithThinkingEnabled(ctx, true, false)
		if account.modelRateLimitAllowsScheduling(thinkingCtx, requestedModel) &&
			s.isRoutingModelSupportedByAccountWithContext(thinkingCtx, account, requestedModel) {
			appendModel(resolveAccountUpstreamModel(thinkingCtx, account, requestedModel))
		}
		return models
	}
	if account == nil || !account.modelRateLimitAllowsScheduling(ctx, requestedModel) {
		return models
	}
	appendModel(resolveAccountUpstreamModel(ctx, account, requestedModel))
	return models
}

func (s *GatewayService) requestableModelRestricted(ctx context.Context, groupID *int64, pricingModel string) bool {
	if groupID == nil || s == nil || s.channelService == nil {
		return false
	}
	pricingModel = strings.TrimSpace(pricingModel)
	if pricingModel == "" || !s.channelService.IsModelRestricted(ctx, *groupID, pricingModel) {
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
