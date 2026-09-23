package selection

import (
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"context"
	"errors"
	"fmt"

	"strings"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
)

const geminiStickySessionTTL = time.Hour

func (s *Gemini) SelectAccountForModel(ctx context.Context, groupID *int64, sessionHash string, requestedModel string) (*gatewayprovider.ExecutionAccount, error) {
	return s.SelectAccountForModelWithExclusions(ctx, groupID, sessionHash, requestedModel, nil)
}

func (s *Gemini) SelectAccountForModelWithExclusions(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*gatewayprovider.ExecutionAccount, error) {
	core, scope := s.geminiSelector()
	selected, err := core.SelectOnly(ctx, schedulercore.SelectionInput{GroupID: groupID, SessionHash: sessionHash, RequestedModel: requestedModel,
		ExcludedIDs: excludedIDs})
	return scope.oldAccount(selected), err
}

// resolvePlatformAndSchedulingMode 解析目标平台和调度模式。
// 返回：平台名称、是否使用混合调度、是否强制平台、已解析分组、错误。
func (s *Gemini) resolvePlatformAndSchedulingMode(ctx context.Context, groupID *int64) (platform string, useMixedScheduling bool, hasForcePlatform bool, group *routing.Group, err error) {

	forcePlatform, hasForcePlatform := apikey.ForcePlatformFromContext(ctx)
	if hasForcePlatform && forcePlatform != "" {
		if groupID == nil {
			return forcePlatform, false, true, nil, nil
		}
		if ctxGroup, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(ctxGroup) && ctxGroup.ID == *groupID {
			return forcePlatform, false, true, ctxGroup, nil
		}
		group, err = s.groupRepo.GetByIDLite(ctx, *groupID)
		if err != nil {
			return "", false, false, nil, fmt.Errorf("get group failed: %w", err)
		}
		return forcePlatform, false, true, group, nil
	}

	if groupID != nil {

		if ctxGroup, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(ctxGroup) && ctxGroup.ID == *groupID {
			group = ctxGroup
		} else {
			group, err = s.groupRepo.GetByIDLite(ctx, *groupID)
			if err != nil {
				return "", false, false, nil, fmt.Errorf("get group failed: %w", err)
			}
		}

		return group.Platform, group.Platform == capability.PlatformGemini, false, group, nil
	}

	return capability.PlatformGemini, true, false, nil, nil
}

// tryStickySessionHit 尝试从粘性会话获取账号。
// 如果命中且账号可用则返回账号；如果账号不可用则清理会话并返回 nil。
//
// tryStickySessionHit attempts to get account from sticky session.
// Returns account if hit and usable; clears session and returns nil if account unavailable.
func (s *Gemini) tryStickySessionHit(
	ctx context.Context,
	groupID *int64,
	sessionHash, cacheKey, requestedModel string,
	excludedIDs map[int64]struct{},
	platform string,
	useMixedScheduling bool,
) *gatewayprovider.ExecutionAccount {
	if sessionHash == "" {
		return nil
	}

	accountID, err := s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
	if err != nil || accountID <= 0 {
		return nil
	}

	if _, excluded := excludedIDs[accountID]; excluded {
		return nil
	}

	account, err := s.getSchedulableAccount(ctx, accountID)
	if err != nil {
		return nil
	}

	if shouldClearStickySession(account, requestedModel) {
		_ = s.cache.DeleteSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
		return nil
	}

	if !s.isAccountUsableForRequest(ctx, account, requestedModel, platform, useMixedScheduling) {
		return nil
	}

	_ = s.cache.RefreshSessionTTL(ctx, derefGroupID(groupID), cacheKey, geminiStickySessionTTL)
	return account
}

// isAccountUsableForRequest 检查账号是否可用于当前请求。
// 验证：模型调度、模型支持、平台匹配、速率限制预检。
//
// isAccountUsableForRequest checks if account is usable for current request.
// Validates: model scheduling, model support, platform matching, rate limit precheck.
func (s *Gemini) isAccountUsableForRequest(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	requestedModel, platform string,
	useMixedScheduling bool,
) bool {
	return s.isAccountUsableForRequestWithPrecheck(ctx, account, requestedModel, platform, useMixedScheduling, nil)
}

func (s *Gemini) isAccountUsableForRequestWithPrecheck(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	requestedModel, platform string,
	useMixedScheduling bool,
	precheckResult map[int64]bool,
) bool {

	if !gatewayprovider.ExecutionModelPolicy(account).Schedulable(ctx, requestedModel) {
		return false
	}

	if requestedModel != "" && !s.isModelSupportedByAccount(account, requestedModel) {
		return false
	}

	if !s.isAccountValidForPlatform(account, platform, useMixedScheduling) {
		return false
	}

	if !s.passesRateLimitPreCheckWithCache(ctx, account, requestedModel, precheckResult) {
		return false
	}

	return true
}

// isAccountValidForPlatform 检查账号是否匹配目标平台。
// 原生平台直接匹配；混合调度模式下 antigravity 需要启用 mixed_scheduling。
//
// isAccountValidForPlatform checks if account matches target platform.
// Native platform matches directly; mixed scheduling mode requires antigravity to enable mixed_scheduling.
func (s *Gemini) isAccountValidForPlatform(account *gatewayprovider.ExecutionAccount, platform string, useMixedScheduling bool) bool {
	if account.Record.Platform == platform {
		return true
	}
	if useMixedScheduling && account.Record.Platform == capability.PlatformAntigravity && account.View().IsMixedSchedulingEnabled() {
		return true
	}
	return false
}

func (s *Gemini) passesRateLimitPreCheckWithCache(ctx context.Context, account *gatewayprovider.ExecutionAccount, requestedModel string, precheckResult map[int64]bool) bool {
	if s.quotaPrecheck == nil || requestedModel == "" {
		return true
	}

	if precheckResult != nil {
		if ok, exists := precheckResult[account.Record.ID]; exists {
			return ok
		}
	}

	ok, err := s.quotaPrecheck.PreCheckUsage(ctx, gatewayprovider.ExecutionRecord(account), requestedModel)
	if err != nil {
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini PreCheck] Account %d precheck error: %v", account.Record.ID, err)
	}
	return ok
}

// eligibleGeminiAccounts 在高级和基础调度器前复用 Gemini 现有的全部硬过滤规则。
func (s *Gemini) eligibleGeminiAccounts(
	ctx context.Context,
	accounts []gatewayprovider.ExecutionAccount,
	requestedModel string,
	excludedIDs map[int64]struct{},
	platform string,
	useMixedScheduling bool,
) []*gatewayprovider.ExecutionAccount {
	precheckResult := s.buildPreCheckUsageResultMap(ctx, accounts, requestedModel)
	eligible := make([]*gatewayprovider.ExecutionAccount, 0, len(accounts))

	for i := range accounts {
		acc := &accounts[i]

		if _, excluded := excludedIDs[acc.Record.ID]; excluded {
			continue
		}

		if !s.isAccountUsableForRequestWithPrecheck(ctx, acc, requestedModel, platform, useMixedScheduling, precheckResult) {
			continue
		}
		eligible = append(eligible, acc)
	}

	return eligible
}

// groupUsesAdvancedScheduler 只让最终分组显式选择高级模式；无分组路径保持基础调度。
func (s *Gemini) groupUsesAdvancedScheduler(ctx context.Context, groupID *int64, hasForcePlatform bool) bool {
	if s == nil || groupID == nil || *groupID <= 0 {
		return false
	}
	if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == *groupID {
		return group.UsesAdvancedScheduler()
	}
	if s.schedulerSnapshot != nil {
		if group, err := s.readSchedulingGroup(ctx, *groupID); err == nil && group != nil {
			return group.UsesAdvancedScheduler()
		}
	}
	if s.groupRepo == nil {
		return false
	}
	group, err := s.groupRepo.GetByIDLite(ctx, *groupID)
	return err == nil && group != nil && group.UsesAdvancedScheduler()
}

func (s *Gemini) advancedSchedulerStats() *schedulercore.RuntimeStats {
	if s == nil {
		return nil
	}
	if s.advancedAccountStats == nil {
		s.advancedAccountStats = schedulercore.NewRuntimeStats(time.Now)
	}
	return s.advancedAccountStats
}

// advancedSchedulerEffectiveSettingsForRequest 返回最终分组的高级调度有效配置。
func (s *Gemini) advancedSchedulerEffectiveSettingsForRequest(ctx context.Context, id *int64) policy.EffectiveSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	group := schedulerRequestGroup(ctx, id, s.schedulerSnapshot != nil, s.readSchedulingGroup)
	return s.schedulerParameters.Effective(ctx, schedulerGroupOverrides(group))
}

// selectAdvancedGeminiAccount 在 Gemini 已完成硬过滤后复用通用高级评分与 Top-K 选择。
func (s *Gemini) selectAdvancedGeminiAccount(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	cacheKey string,
	eligible []*gatewayprovider.ExecutionAccount,
	settings policy.EffectiveSettings,
) *gatewayprovider.ExecutionAccount {
	if len(eligible) == 0 {
		return nil
	}
	var stickyAccountID int64
	if sessionHash != "" && s.cache != nil {
		stickyAccountID, _ = s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
	}
	input := schedulercore.ScoreInput{
		GroupID:         groupID,
		SessionHash:     cacheKey,
		StickyAccountID: stickyAccountID,
		StickyWeighted:  settings.StickyWeightedEnabled,
		TopK:            settings.TopK,
	}
	values := make([]*schedulercore.ScoreAccount, len(eligible))
	source := make(map[*schedulercore.ScoreAccount]*gatewayprovider.ExecutionAccount, len(eligible))
	for i, value := range eligible {
		if value == nil {
			continue
		}
		values[i] = &schedulercore.ScoreAccount{ID: value.Record.ID, Platform: value.Record.Platform, Priority: value.Record.Priority, SessionWindowEnd: value.Record.SessionWindowEnd}
		source[values[i]] = value
	}
	candidates, _ := schedulercore.ScoreCandidates(values, nil, s.advancedSchedulerStats(), settings.Weights, input, time.Now())
	selectionOrder := schedulercore.BuildSelectionOrder(candidates, input)
	if len(selectionOrder) == 0 {
		return nil
	}
	return source[selectionOrder[0].Account]
}

// groupModelUnsupportedErrorIfApplicable 在确认是分组模型限制时返回 typed error。
func (s *Gemini) groupModelUnsupportedErrorIfApplicable(ctx context.Context, accounts []gatewayprovider.ExecutionAccount, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixedScheduling bool) error {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(accounts) == 0 {
		return nil
	}
	precheckResult := s.buildPreCheckUsageResultMap(ctx, accounts, requestedModel)
	hasRelevantAccount := false
	for i := range accounts {
		acc := &accounts[i]
		if excludedIDs != nil {
			if _, excluded := excludedIDs[acc.Record.ID]; excluded {
				continue
			}
		}
		if !acc.View().IsSchedulable() || !s.isAccountValidForPlatform(acc, platform, useMixedScheduling) || !s.passesRateLimitPreCheckWithCache(ctx, acc, requestedModel, precheckResult) {
			continue
		}
		hasRelevantAccount = true
		if s.isModelSupportedByAccount(acc, requestedModel) {
			return nil
		}
	}
	if !hasRelevantAccount {
		return nil
	}
	return routing.NewGroupModelRejection(platform, requestedModel, modelRejectionSources(accounts))
}

func (s *Gemini) buildPreCheckUsageResultMap(ctx context.Context, accounts []gatewayprovider.ExecutionAccount, requestedModel string) map[int64]bool {
	if s.quotaPrecheck == nil || requestedModel == "" || len(accounts) == 0 {
		return nil
	}

	candidates := make([]*gatewayprovider.ExecutionAccount, 0, len(accounts))
	for i := range accounts {
		candidates = append(candidates, &accounts[i])
	}

	result, err := s.quotaPrecheck.PreCheckUsageBatch(ctx, gatewayprovider.ExecutionRecordPointers(candidates), requestedModel)
	if err != nil {
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini PreCheckBatch] failed: %v", err)
	}
	return result
}

// isModelSupportedByAccount 根据账户平台检查模型支持
func (s *Gemini) isModelSupportedByAccount(account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	if account.Record.Platform == capability.PlatformAntigravity {
		if strings.TrimSpace(requestedModel) == "" {
			return true
		}
		return mapAntigravityModel(account, requestedModel) != ""
	}
	return gatewayprovider.ExecutionProtocolRecord(account).IsModelSupported(requestedModel, accountprovider.ModelDefaults(), accountprovider.ModelRules(gatewayprovider.ExecutionProtocolRecord(account)))
}

func (s *Gemini) getSchedulableAccount(ctx context.Context, accountID int64) (*gatewayprovider.ExecutionAccount, error) {
	if s.schedulerSnapshot != nil {
		return readSnapshotAccount(ctx, s.schedulerSnapshot, accountID)
	}
	return s.accountRepo.GetByID(ctx, accountID)
}

func (s *Gemini) hydrateSelectedAccount(ctx context.Context, account *gatewayprovider.ExecutionAccount) (*gatewayprovider.ExecutionAccount, error) {
	if account == nil || s.schedulerSnapshot == nil {
		return account, nil
	}
	hydrated, err := readSnapshotAccount(ctx, s.schedulerSnapshot, account.Record.ID)
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, fmt.Errorf("selected gemini account %d not found during hydration", account.Record.ID)
	}
	return hydrated, nil
}

func (s *Gemini) listSchedulableAccountsOnce(ctx context.Context, groupID *int64, platform string, hasForcePlatform bool) ([]gatewayprovider.ExecutionAccount, error) {
	if s.schedulerSnapshot != nil {
		accounts, _, err := readSnapshotAccounts(ctx, s.schedulerSnapshot, groupID, platform, hasForcePlatform)
		return accounts, err
	}

	useMixedScheduling := platform == capability.PlatformGemini && !hasForcePlatform
	queryPlatforms := []string{platform}
	if useMixedScheduling {
		queryPlatforms = []string{platform, capability.PlatformAntigravity}
	}

	if groupID != nil {
		return s.accountRepo.ListSchedulableByGroupIDAndPlatforms(ctx, *groupID, queryPlatforms)
	}
	if s.options.Simple {
		return s.accountRepo.ListSchedulableByPlatforms(ctx, queryPlatforms)
	}
	return s.accountRepo.ListSchedulableUngroupedByPlatforms(ctx, queryPlatforms)
}

// HasAntigravityAccounts 检查是否有可用的 antigravity 账户
func (s *Gemini) HasAntigravityAccounts(ctx context.Context, groupID *int64) (bool, error) {
	accounts, err := s.listSchedulableAccountsOnce(ctx, groupID, capability.PlatformAntigravity, false)
	if err != nil {
		return false, err
	}
	return len(accounts) > 0, nil
}

// SelectAccountForAIStudioEndpoints selects an account that is likely to succeed against
// generativelanguage.googleapis.com (e.g. GET /v1beta/models).
//
// Preference order:
// 1) API key accounts (AI Studio)
// 2) OAuth accounts without project_id (AI Studio OAuth)
// 3) OAuth accounts explicitly marked as ai_studio
// 4) Any remaining Gemini accounts (fallback)
func (s *Gemini) SelectAccountForAIStudioEndpoints(ctx context.Context, groupID *int64) (*gatewayprovider.ExecutionAccount, error) {
	if group, ok := s.resolveAdvancedSchedulerGroup(ctx, groupID); ok {
		ctx = requeststate.WithGroup(ctx, group)
	}
	accounts, err := s.listSchedulableAccountsOnce(ctx, groupID, capability.PlatformGemini, true)
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}
	if len(accounts) == 0 {
		return nil, errors.New("no available Gemini accounts")
	}

	rank := func(a *gatewayprovider.ExecutionAccount) int {
		if a == nil {
			return 999
		}
		switch a.Record.Type {
		case capability.AccountTypeAPIKey:
			if strings.TrimSpace(a.View().GetCredential("api_key")) != "" {
				return 0
			}
			return 9
		case capability.AccountTypeOAuth:
			if strings.TrimSpace(a.View().GetCredential("project_id")) == "" {
				return 1
			}
			if strings.TrimSpace(a.View().GetCredential("oauth_type")) == "ai_studio" {
				return 2
			}

			return 3
		case capability.AccountTypeServiceAccount:

			return 999
		default:
			return 10
		}
	}

	var selected *gatewayprovider.ExecutionAccount
	for i := range accounts {
		acc := &accounts[i]
		if selected == nil {
			selected = acc
			continue
		}

		r1, r2 := rank(acc), rank(selected)
		if r1 < r2 {
			selected = acc
			continue
		}
		if r1 > r2 {
			continue
		}

		if acc.Record.Priority < selected.Record.Priority {
			selected = acc
		} else if acc.Record.Priority == selected.Record.Priority {
			switch {
			case acc.Record.LastUsedAt == nil && selected.Record.LastUsedAt != nil:
				selected = acc
			case acc.Record.LastUsedAt != nil && selected.Record.LastUsedAt == nil:

			case acc.Record.LastUsedAt == nil && selected.Record.LastUsedAt == nil:
				if acc.Record.Type == capability.AccountTypeOAuth && selected.Record.Type != capability.AccountTypeOAuth {
					selected = acc
				}
			default:
				if acc.Record.LastUsedAt.Before(*selected.Record.LastUsedAt) {
					selected = acc
				}
			}
		}
	}

	if selected == nil {
		return nil, errors.New("no available Gemini accounts")
	}

	if s.groupUsesAdvancedScheduler(ctx, groupID, false) {
		bestRank := rank(selected)
		if bestRank < 999 {
			eligible := make([]*gatewayprovider.ExecutionAccount, 0, len(accounts))
			for i := range accounts {
				account := &accounts[i]
				if rank(account) == bestRank {
					eligible = append(eligible, account)
				}
			}
			if advanced := s.selectAdvancedGeminiAccount(
				ctx,
				groupID,
				"",
				"",
				eligible,
				s.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID),
			); advanced != nil {
				selected = advanced
			}
		}
	}
	return s.hydrateSelectedAccount(ctx, selected)
}

// resolveAdvancedSchedulerGroup 为不经过普通模型选择的 Gemini 入口补齐最终分组。
func (s *Gemini) resolveAdvancedSchedulerGroup(ctx context.Context, groupID *int64) (*routing.Group, bool) {
	if s == nil || groupID == nil || *groupID <= 0 {
		return nil, false
	}
	if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == *groupID {
		return group, true
	}
	if s.schedulerSnapshot != nil {
		if group, err := s.readSchedulingGroup(ctx, *groupID); err == nil && group != nil {
			return group, true
		}
	}
	if s.groupRepo == nil {
		return nil, false
	}
	group, err := s.groupRepo.GetByIDLite(ctx, *groupID)
	return group, err == nil && group != nil
}
