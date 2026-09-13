// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	slog "log/slog"
	strings "strings"
)

type RequestableModel = routing.RequestableModel

type RequestableModelsResult = routing.RequestableModelsResult

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

func (s *GatewayService) resolveRequestableModelsWithAccounts(
	ctx context.Context,
	groupID *int64,
	platform string,
	baseModels []string,
	accounts []Account,
) RequestableModelsResult {
	core := s.requestableModelResolver()
	return core.ResolveWithAccounts(ctx, groupID, platform, baseModels, legacyCatalogueAccounts(s, accounts))
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

func requestableModelsFallback(models []string, platform string) RequestableModelsResult {
	return routing.RequestableModelsFallback(models, platform, legacyCatalogueDefaults())
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

func RequestableModelIDs(models []RequestableModel) []string {
	return routing.RequestableModelIDs(models)
}
