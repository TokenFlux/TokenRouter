package service

import (
	"context"
	"fmt"
	policy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AdvancedSchedulerScoreCalculationVersion 标识诊断公式的兼容版本。
const AdvancedSchedulerScoreCalculationVersion = "v1"

// AdvancedSchedulerScoreDiagnosticRequest 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticRequest = policy.AdvancedSchedulerScoreDiagnosticRequest

// AdvancedSchedulerScoreDiagnosticAccount 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticAccount = policy.AdvancedSchedulerScoreDiagnosticAccount

// AdvancedSchedulerScoreDiagnosticGroup 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticGroup = policy.AdvancedSchedulerScoreDiagnosticGroup

// AdvancedSchedulerScoreDiagnosticGroupSummary 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticGroupSummary = policy.AdvancedSchedulerScoreDiagnosticGroupSummary

// AdvancedSchedulerScoreDiagnosticResponse 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticResponse = policy.AdvancedSchedulerScoreDiagnosticResponse

// AdvancedSchedulerScoreDiagnosticContext 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticContext = policy.AdvancedSchedulerScoreDiagnosticContext

// AdvancedSchedulerScoreDiagnosticDetail 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticDetail = policy.AdvancedSchedulerScoreDiagnosticDetail

// AdvancedSchedulerScoreDiagnosticCandidatePool 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticCandidatePool = policy.AdvancedSchedulerScoreDiagnosticCandidatePool

// AdvancedSchedulerScoreDiagnosticRanges 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticRanges = policy.AdvancedSchedulerScoreDiagnosticRanges

// AdvancedSchedulerScoreDiagnosticCandidate 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticCandidate = policy.AdvancedSchedulerScoreDiagnosticCandidate

// AdvancedSchedulerScoreDiagnosticScore 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticScore = policy.AdvancedSchedulerScoreDiagnosticScore

// AdvancedSchedulerScoreDiagnosticMetric 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticMetric = policy.AdvancedSchedulerScoreDiagnosticMetric

// AdvancedSchedulerScoreDiagnosticSetting 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticSetting = policy.AdvancedSchedulerScoreDiagnosticSetting

// AdvancedSchedulerScoreDiagnosticPolicySignal 保留原诊断值入口。
type AdvancedSchedulerScoreDiagnosticPolicySignal = policy.AdvancedSchedulerScoreDiagnosticPolicySignal

// AdvancedSchedulerScoreDiagnosticSource 是诊断服务所需的最小账号读取能力。
// 通过窄接口保持服务可独立测试，也避免诊断路径触及凭据读取或写入接口。
type AdvancedSchedulerScoreDiagnosticSource interface {
	GetAccount(ctx context.Context, id int64) (*Account, error)
	GetGroup(ctx context.Context, id int64) (*Group, error)
	ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, error)
	ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, groupID *int64, platform string) ([]Account, error)
}

// AdvancedSchedulerScoreDiagnosticService 编排只读评分诊断。
// 它复用高级评分核心，但绝不获取并发槽、绑定会话或回写运行时状态。
type AdvancedSchedulerScoreDiagnosticService struct {
	source             AdvancedSchedulerScoreDiagnosticSource
	concurrencyService *ConcurrencyService
	rateLimitService   *RateLimitService
	gatewayService     *GatewayService
	openAIGateway      *OpenAIGatewayService
}

// SetSchedulingServices 注入生产调度服务，供诊断复用只读硬过滤逻辑。
func (s *AdvancedSchedulerScoreDiagnosticService) SetSchedulingServices(gateway *GatewayService, openAIGateway *OpenAIGatewayService) {
	if s == nil {
		return
	}
	s.gatewayService = gateway
	s.openAIGateway = openAIGateway
}

// NewAdvancedSchedulerScoreDiagnosticService 创建高级评分诊断服务。
func NewAdvancedSchedulerScoreDiagnosticService(
	source AdvancedSchedulerScoreDiagnosticSource,
	concurrencyService *ConcurrencyService,
	rateLimitService *RateLimitService,
) *AdvancedSchedulerScoreDiagnosticService {
	return &AdvancedSchedulerScoreDiagnosticService{
		source:             source,
		concurrencyService: concurrencyService,
		rateLimitService:   rateLimitService,
	}
}

// GetOverview 返回账号所属高级分组的轻量摘要。
func (s *AdvancedSchedulerScoreDiagnosticService) GetOverview(ctx context.Context, accountID int64) (*AdvancedSchedulerScoreDiagnosticResponse, error) {
	account, groups, err := s.loadAccountAndAdvancedGroups(ctx, accountID)
	if err != nil {
		return nil, err
	}
	response := s.newResponse(account)
	for _, group := range groups {
		summary, err := s.buildGroupSummary(ctx, account, group)
		if err != nil {
			return nil, err
		}
		response.Groups = append(response.Groups, summary)
	}
	return response, nil
}

// GetDetail 返回指定高级分组在给定安全场景下的完整解释。
func (s *AdvancedSchedulerScoreDiagnosticService) GetDetail(ctx context.Context, accountID int64, request AdvancedSchedulerScoreDiagnosticRequest) (*AdvancedSchedulerScoreDiagnosticResponse, error) {
	account, groups, err := s.loadAccountAndAdvancedGroups(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if request.GroupID <= 0 {
		return nil, fmt.Errorf("group_id is required")
	}
	request.RequestedModel = strings.TrimSpace(request.RequestedModel)
	if len(request.RequestedModel) > 512 {
		return nil, fmt.Errorf("requested_model is too long")
	}
	if request.StickyAccountID < 0 || request.PreviousResponseAccountID < 0 {
		return nil, fmt.Errorf("sticky account id must not be negative")
	}

	var selected *Group
	for _, group := range groups {
		if group.ID == request.GroupID {
			selected = group
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("account is not in an advanced scheduler group")
	}

	response := s.newResponse(account)
	for _, group := range groups {
		if group.ID == selected.ID {
			detail, buildErr := s.buildDetail(ctx, account, selected, request)
			if buildErr != nil {
				return nil, buildErr
			}
			response.Detail = detail
			response.Groups = append(response.Groups, summaryFromDiagnosticDetail(detail))
			continue
		}
		summary, buildErr := s.buildGroupSummary(ctx, account, group)
		if buildErr != nil {
			return nil, buildErr
		}
		response.Groups = append(response.Groups, summary)
	}
	return response, nil
}

func (s *AdvancedSchedulerScoreDiagnosticService) loadAccountAndAdvancedGroups(ctx context.Context, accountID int64) (*Account, []*Group, error) {
	if s == nil || s.source == nil {
		return nil, nil, fmt.Errorf("advanced scheduler diagnostics is unavailable")
	}
	account, err := s.source.GetAccount(ctx, accountID)
	if err != nil {
		return nil, nil, err
	}
	if account == nil {
		return nil, nil, fmt.Errorf("account not found")
	}
	groupsByID := make(map[int64]*Group)
	for _, accountGroup := range account.AccountGroups {
		if accountGroup.Group != nil && accountGroup.Group.UsesAdvancedScheduler() {
			groupsByID[accountGroup.Group.ID] = accountGroup.Group
		}
	}
	for _, group := range account.Groups {
		if group != nil && group.UsesAdvancedScheduler() {
			groupsByID[group.ID] = group
		}
	}
	for _, groupID := range account.GroupIDs {
		if groupID <= 0 {
			continue
		}
		if _, exists := groupsByID[groupID]; exists {
			continue
		}
		group, groupErr := s.source.GetGroup(ctx, groupID)
		if groupErr != nil {
			return nil, nil, groupErr
		}
		if group != nil && group.UsesAdvancedScheduler() {
			groupsByID[group.ID] = group
		}
	}

	groups := make([]*Group, 0, len(groupsByID))
	for _, group := range groupsByID {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].SortOrder != groups[j].SortOrder {
			return groups[i].SortOrder < groups[j].SortOrder
		}
		return groups[i].ID < groups[j].ID
	})
	return account, groups, nil
}

func (s *AdvancedSchedulerScoreDiagnosticService) newResponse(account *Account) *AdvancedSchedulerScoreDiagnosticResponse {
	return &AdvancedSchedulerScoreDiagnosticResponse{
		Account:            diagnosticAccountSummary(account),
		GeneratedAt:        time.Now().UTC(),
		CalculationVersion: AdvancedSchedulerScoreCalculationVersion,
		Groups:             make([]AdvancedSchedulerScoreDiagnosticGroupSummary, 0),
	}
}

func diagnosticAccountSummary(account *Account) AdvancedSchedulerScoreDiagnosticAccount {
	if account == nil {
		return AdvancedSchedulerScoreDiagnosticAccount{}
	}
	return AdvancedSchedulerScoreDiagnosticAccount{
		ID:       account.ID,
		Name:     account.Name,
		Platform: account.Platform,
		Type:     account.Type,
		Status:   account.Status,
	}
}

func diagnosticGroupSummary(group *Group) AdvancedSchedulerScoreDiagnosticGroup {
	if group == nil {
		return AdvancedSchedulerScoreDiagnosticGroup{}
	}
	return AdvancedSchedulerScoreDiagnosticGroup{ID: group.ID, Name: group.Name, Platform: group.Platform}
}

func summaryFromDiagnosticDetail(detail *AdvancedSchedulerScoreDiagnosticDetail) AdvancedSchedulerScoreDiagnosticGroupSummary {
	summary := AdvancedSchedulerScoreDiagnosticGroupSummary{
		AdvancedSchedulerScoreDiagnosticGroup: detail.Group,
		Eligible:                              detail.Eligible,
		Status:                                "eligible",
	}
	if !detail.Eligible {
		summary.Status = "filtered"
		return summary
	}
	if detail.Score != nil {
		score := detail.Score.FinalScore
		summary.FinalScore = &score
	}
	return summary
}

// buildGroupSummary 只计算分组 Tab 所需的资格和最终分数。
// 它刻意不读取完整分组账号清单、不组装指标或策略解释，避免首次打开诊断弹窗时放大读取负载。
func (s *AdvancedSchedulerScoreDiagnosticService) buildGroupSummary(
	ctx context.Context,
	target *Account,
	group *Group,
) (AdvancedSchedulerScoreDiagnosticGroupSummary, error) {
	summary := AdvancedSchedulerScoreDiagnosticGroupSummary{
		AdvancedSchedulerScoreDiagnosticGroup: diagnosticGroupSummary(group),
		Status:                                "eligible",
	}
	if target == nil || group == nil {
		summary.Status = "filtered"
		return summary, nil
	}
	now := time.Now()
	effective, _ := s.effectiveSettings(ctx, group)
	poolAccounts, err := s.source.ListSchedulableAccountsForAdvancedSchedulerScore(ctx, &group.ID, group.Platform)
	if err != nil {
		return summary, err
	}
	ctx = s.prepareEligibilityContext(ctx, group, poolAccounts)
	filtered := make([]*Account, 0, len(poolAccounts))
	for index := range poolAccounts {
		candidate := poolAccounts[index]
		if s.diagnosticHardFilterReason(ctx, &candidate, group, AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID}, now) == "" {
			filtered = append(filtered, &candidate)
		}
	}
	filtered, _, _ = diagnosticSubscriptionPriorityPool(filtered, group, effective)
	loadMap := s.loadMap(ctx, filtered)
	var stats *advancedAccountRuntimeStats
	if s.rateLimitService != nil {
		stats = s.rateLimitService.AdvancedSchedulerRuntimeStats()
	}
	candidates, _, _ := scoreAdvancedSchedulerCandidatesWithRanges(
		filtered,
		loadMap,
		stats,
		effective.weights,
		advancedSchedulerSelectionInput{
			GroupID:             &group.ID,
			StickyWeighted:      effective.stickyWeightedEnabled,
			TopK:                effective.topK,
			QuotaHeadroomFactor: openAIQuotaHeadroomFactor,
		},
		now,
	)
	targetCandidate, _ := findDiagnosticCandidate(candidates, target.ID)
	if targetCandidate == nil {
		summary.Status = "filtered"
		return summary, nil
	}
	summary.Eligible = true
	finalScore := targetCandidate.score
	summary.FinalScore = &finalScore
	return summary, nil
}

func (s *AdvancedSchedulerScoreDiagnosticService) buildDetail(
	ctx context.Context,
	target *Account,
	group *Group,
	request AdvancedSchedulerScoreDiagnosticRequest,
) (*AdvancedSchedulerScoreDiagnosticDetail, error) {
	now := time.Now()
	effective, runtime := s.effectiveSettings(ctx, group)
	allAccounts, err := s.source.ListAccountsForSchedulerScoreFilter(ctx, "", "", "", "", group.ID, "")
	if err != nil {
		return nil, err
	}
	poolAccounts, err := s.source.ListSchedulableAccountsForAdvancedSchedulerScore(ctx, &group.ID, group.Platform)
	if err != nil {
		return nil, err
	}
	ctx = s.prepareEligibilityContext(ctx, group, poolAccounts)
	stats := (*advancedAccountRuntimeStats)(nil)
	if s.rateLimitService != nil {
		stats = s.rateLimitService.AdvancedSchedulerRuntimeStats()
	}
	eligibilityRequest := request
	// 硬粘性逃逸后，生产调度会按普通候选执行费用与 RPM 门禁；诊断必须使用相同语义。
	if !effective.stickyWeightedEnabled && request.StickyAccountID > 0 {
		if _, _, _, escaped := shouldEscapeAdvancedStickyAccount(stats, request.StickyAccountID, effective.stickyEscape); escaped {
			eligibilityRequest.StickyAccountID = 0
		}
	}

	exclusions := make(map[string]int)
	filtered := make([]*Account, 0, len(poolAccounts))
	seenPoolIDs := make(map[int64]struct{}, len(poolAccounts))
	for i := range poolAccounts {
		candidate := poolAccounts[i]
		seenPoolIDs[candidate.ID] = struct{}{}
		if reason := s.diagnosticHardFilterReason(ctx, &candidate, group, eligibilityRequest, now); reason != "" {
			exclusions[reason]++
			continue
		}
		filtered = append(filtered, &candidate)
	}
	for i := range allAccounts {
		account := &allAccounts[i]
		if _, found := seenPoolIDs[account.ID]; found {
			continue
		}
		reason := s.diagnosticHardFilterReason(ctx, account, group, eligibilityRequest, now)
		if reason == "" {
			reason = "not_in_schedulable_pool"
		}
		exclusions[reason]++
	}

	policyOutcome := diagnosticHardStickyPolicyOutcome(filtered, group, request, effective, stats, effective.stickyEscape)
	deferredAccountIDs := map[int64]struct{}{}
	if policyOutcome.forcedAccountID == 0 {
		var subscriptionPoolActive bool
		filtered, deferredAccountIDs, subscriptionPoolActive = diagnosticSubscriptionPriorityPool(filtered, group, effective)
		policyOutcome.subscriptionPoolActive = subscriptionPoolActive
		if len(deferredAccountIDs) > 0 {
			exclusions["subscription_priority_deferred"] += len(deferredAccountIDs)
		}
	}

	loadMap := s.loadMap(ctx, filtered)
	previousResponseAccountID := int64(0)
	if group != nil && group.Platform == PlatformOpenAI {
		previousResponseAccountID = request.PreviousResponseAccountID
	}
	input := advancedSchedulerSelectionInput{
		GroupID:                 &group.ID,
		RequestedModel:          request.RequestedModel,
		StickyAccountID:         request.StickyAccountID,
		StickyPreviousAccountID: previousResponseAccountID,
		StickyWeighted:          effective.stickyWeightedEnabled,
		TopK:                    effective.topK,
		QuotaHeadroomFactor:     openAIQuotaHeadroomFactor,
	}
	candidates, _, ranges := scoreAdvancedSchedulerCandidatesWithRanges(filtered, loadMap, stats, effective.weights, input, now)
	sort.SliceStable(candidates, func(i, j int) bool {
		return isAdvancedSchedulerCandidateBetter(candidates[i], candidates[j])
	})
	topKCandidates := selectTopKAdvancedSchedulerCandidates(candidates, effective.topK)
	selection := diagnosticSelectionStats(topKCandidates, policyOutcome.forcedAccountID)

	detail := &AdvancedSchedulerScoreDiagnosticDetail{
		Group:             diagnosticGroupSummary(group),
		Context:           diagnosticContext(request),
		CandidatePool:     diagnosticCandidatePool(candidates, topKCandidates, ranges, exclusions, selection),
		EffectiveSettings: diagnosticEffectiveSettings(group, runtime, effective),
		PolicySignals:     diagnosticPolicySignals(group, request, effective, policyOutcome),
		Metrics:           make([]AdvancedSchedulerScoreDiagnosticMetric, 0),
	}
	targetReason := s.diagnosticHardFilterReason(ctx, target, group, eligibilityRequest, now)
	if target != nil {
		if _, deferred := deferredAccountIDs[target.ID]; deferred {
			targetReason = "subscription_priority_deferred"
		}
	}
	if targetReason != "" {
		detail.HardFilterReasons = append(detail.HardFilterReasons, targetReason)
	}
	targetCandidate, targetRank := findDiagnosticCandidate(candidates, target.ID)
	if targetCandidate == nil {
		if targetReason == "" {
			detail.HardFilterReasons = append(detail.HardFilterReasons, "not_in_schedulable_pool")
		}
		detail.Eligible = false
		return detail, nil
	}
	detail.Eligible = true
	detail.Score = diagnosticScore(targetCandidate, targetRank, topKCandidates, selection, effective, request)
	detail.Metrics = diagnosticMetrics(targetCandidate, ranges, effective.weights, now)
	return detail, nil
}

func (s *AdvancedSchedulerScoreDiagnosticService) effectiveSettings(ctx context.Context, group *Group) (advancedSchedulerEffectiveSettings, advancedSchedulerRuntimeSettings) {
	gateway := &OpenAIGatewayService{}
	if s != nil && s.rateLimitService != nil {
		gateway.rateLimitService = s.rateLimitService
		gateway.cfg = s.rateLimitService.cfg
	}
	runtime := gateway.advancedSchedulerRuntimeSettings(ctx)
	return gateway.advancedSchedulerEffectiveSettingsForGroup(ctx, group), runtime
}

func (s *AdvancedSchedulerScoreDiagnosticService) prepareEligibilityContext(ctx context.Context, group *Group, accounts []Account) context.Context {
	if s == nil {
		return ctx
	}
	if s.gatewayService != nil {
		ctx = s.gatewayService.withGroupContext(ctx, group)
		ctx = s.gatewayService.withWindowCostPrefetch(ctx, accounts)
		ctx = s.gatewayService.withRPMPrefetch(ctx, accounts)
	}
	if s.openAIGateway != nil && group != nil && (group.Platform == PlatformOpenAI || group.Platform == PlatformGrok) {
		ctx = s.openAIGateway.withOpenAIQuotaAutoPauseContext(ctx)
	}
	return ctx
}

func (s *AdvancedSchedulerScoreDiagnosticService) loadMap(ctx context.Context, accounts []*Account) map[int64]*AccountLoadInfo {
	if s == nil || s.concurrencyService == nil || len(accounts) == 0 {
		return map[int64]*AccountLoadInfo{}
	}
	loads := make([]AccountWithConcurrency, 0, len(accounts))
	for _, account := range accounts {
		if account != nil {
			loads = append(loads, AccountWithConcurrency{ID: account.ID, MaxConcurrency: account.EffectiveLoadFactor()})
		}
	}
	loadMap, err := s.concurrencyService.GetAccountsLoadBatch(ctx, loads)
	if err != nil || loadMap == nil {
		return map[int64]*AccountLoadInfo{}
	}
	return loadMap
}

func diagnosticContext(request AdvancedSchedulerScoreDiagnosticRequest) AdvancedSchedulerScoreDiagnosticContext {
	return AdvancedSchedulerScoreDiagnosticContext{
		RequestedModel:            strings.TrimSpace(request.RequestedModel),
		StickyAccountID:           request.StickyAccountID,
		PreviousResponseAccountID: request.PreviousResponseAccountID,
		Baseline: strings.TrimSpace(request.RequestedModel) == "" &&
			request.StickyAccountID == 0 && request.PreviousResponseAccountID == 0,
	}
}

func (s *AdvancedSchedulerScoreDiagnosticService) diagnosticHardFilterReason(
	ctx context.Context,
	account *Account,
	group *Group,
	request AdvancedSchedulerScoreDiagnosticRequest,
	now time.Time,
) string {
	if reason := diagnosticBaseHardFilterReason(account, group, now); reason != "" {
		return reason
	}
	model := strings.TrimSpace(request.RequestedModel)
	if group != nil && (group.Platform == PlatformOpenAI || group.Platform == PlatformGrok) {
		if !account.IsSchedulableForModelWithContext(ctx, model) {
			return "model_runtime_blocked"
		}
		if account.IsOpenAI() {
			if paused, _ := shouldAutoPauseOpenAIAccountByQuota(ctx, account); paused {
				return "quota_auto_pause"
			}
		}
		if account.IsGrok() {
			if paused, _ := shouldAutoPauseGrokAccountByQuota(account); paused {
				return "quota_auto_pause"
			}
		}
		if !openAIAccountSupportsRoutingModel(ctx, account, model) {
			return "model_unsupported"
		}
		if s != nil && s.openAIGateway != nil {
			if s.openAIGateway.isOpenAIAccountRequestRuntimeBlocked(account, model) {
				return "runtime_blocked"
			}
			if s.openAIGateway.isOpenAIProxyStreamQuarantined(ctx, account) {
				return "proxy_stream_quarantined"
			}
			scheduler := &defaultOpenAIAccountScheduler{service: s.openAIGateway}
			if !parentHealthyForShadow(account, func(id int64) *Account {
				return scheduler.lookupShadowParentAccount(ctx, id)
			}) {
				return "shadow_parent_unhealthy"
			}
			groupID := group.ID
			if s.openAIGateway.needsUpstreamChannelRestrictionCheck(ctx, &groupID) &&
				s.openAIGateway.isUpstreamRoutingModelRestrictedByChannel(ctx, groupID, account, model, false) {
				return "channel_upstream_restricted"
			}
		}
		return ""
	}

	if s != nil && s.gatewayService != nil {
		if model != "" && !s.gatewayService.isModelSupportedByAccountWithContext(ctx, account, model) {
			return "model_unsupported"
		}
		if !s.gatewayService.isAccountSchedulableForModelSelection(ctx, account, model) {
			return "model_runtime_blocked"
		}
		if !s.gatewayService.isAccountSchedulableForQuota(account) {
			return "quota_exceeded"
		}
		isSticky := account.ID == request.StickyAccountID
		if !s.gatewayService.isAccountSchedulableForWindowCost(ctx, account, isSticky) {
			return "window_cost_exceeded"
		}
		if !s.gatewayService.isAccountSchedulableForRPM(ctx, account, isSticky) {
			return "rpm_exceeded"
		}
		groupID := group.ID
		if s.gatewayService.needsUpstreamChannelRestrictionCheck(ctx, &groupID) &&
			s.gatewayService.isUpstreamModelRestrictedByChannel(ctx, groupID, account, model) {
			return "channel_upstream_restricted"
		}
		return ""
	}

	if model != "" && !account.IsModelSupported(model) {
		return "model_unsupported"
	}
	if !account.IsSchedulableForModelWithContext(ctx, model) {
		return "model_runtime_blocked"
	}
	return ""
}

func diagnosticBaseHardFilterReason(account *Account, group *Group, now time.Time) string {
	if account == nil {
		return "account_missing"
	}
	if !diagnosticPlatformMatchesGroup(account, group) {
		return "platform_mismatch"
	}
	if account.Status != StatusActive {
		return "account_inactive"
	}
	if !account.Schedulable {
		return "account_disabled"
	}
	if account.AutoPauseOnExpired && account.ExpiresAt != nil && !now.Before(*account.ExpiresAt) {
		return "account_expired"
	}
	if account.OverloadUntil != nil && now.Before(*account.OverloadUntil) {
		return "account_overloaded"
	}
	if account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt) {
		return "account_rate_limited"
	}
	if account.TempUnschedulableUntil != nil && now.Before(*account.TempUnschedulableUntil) {
		return "account_temporarily_unschedulable"
	}
	if group != nil && group.RequirePrivacySet && !account.IsPrivacySet() {
		return "privacy_not_set"
	}
	return ""
}

func diagnosticPlatformMatchesGroup(account *Account, group *Group) bool {
	if account == nil || group == nil {
		return false
	}
	if account.Platform == group.Platform {
		return true
	}
	return (group.Platform == PlatformAnthropic || group.Platform == PlatformGemini) &&
		account.Platform == PlatformAntigravity && account.IsMixedSchedulingEnabled()
}

func diagnosticSubscriptionPriorityPool(
	accounts []*Account,
	group *Group,
	effective advancedSchedulerEffectiveSettings,
) ([]*Account, map[int64]struct{}, bool) {
	deferred := make(map[int64]struct{})
	if group == nil || !effective.subscriptionPriorityEnabled ||
		(group.Platform != PlatformOpenAI && group.Platform != PlatformGrok) {
		return accounts, deferred, false
	}
	subscriptionAccounts, regularAccounts := partitionOpenAIChatGPTSubscriptionAccounts(accounts)
	if len(subscriptionAccounts) == 0 {
		return accounts, deferred, false
	}
	for _, account := range regularAccounts {
		if account != nil {
			deferred[account.ID] = struct{}{}
		}
	}
	return subscriptionAccounts, deferred, true
}

type diagnosticPolicyOutcome struct {
	forcedAccountID        int64
	previousResponseState  string
	sessionStickyState     string
	stickyEscapeReason     string
	subscriptionPoolActive bool
}

func diagnosticHardStickyPolicyOutcome(
	accounts []*Account,
	group *Group,
	request AdvancedSchedulerScoreDiagnosticRequest,
	effective advancedSchedulerEffectiveSettings,
	stats *advancedAccountRuntimeStats,
	escapeConfig advancedStickyEscapeConfig,
) diagnosticPolicyOutcome {
	outcome := diagnosticPolicyOutcome{}
	eligibleIDs := make(map[int64]struct{}, len(accounts))
	for _, account := range accounts {
		if account != nil {
			eligibleIDs[account.ID] = struct{}{}
		}
	}
	if effective.stickyWeightedEnabled {
		if request.PreviousResponseAccountID > 0 {
			if group == nil || group.Platform != PlatformOpenAI {
				outcome.previousResponseState = "ignored"
			} else {
				outcome.previousResponseState = "weighted"
			}
		}
		if request.StickyAccountID > 0 {
			outcome.sessionStickyState = "weighted"
		}
		return outcome
	}

	if request.PreviousResponseAccountID > 0 {
		if group == nil || group.Platform != PlatformOpenAI {
			outcome.previousResponseState = "ignored"
		} else if _, eligible := eligibleIDs[request.PreviousResponseAccountID]; eligible {
			outcome.previousResponseState = "forced_first"
			outcome.forcedAccountID = request.PreviousResponseAccountID
		} else {
			outcome.previousResponseState = "unavailable"
		}
	}
	if request.StickyAccountID <= 0 {
		return outcome
	}
	if outcome.forcedAccountID > 0 {
		outcome.sessionStickyState = "not_reached"
		return outcome
	}
	if reason, _, _, escape := shouldEscapeAdvancedStickyAccount(stats, request.StickyAccountID, escapeConfig); escape {
		outcome.sessionStickyState = "escaped"
		outcome.stickyEscapeReason = reason
		return outcome
	}
	if _, eligible := eligibleIDs[request.StickyAccountID]; !eligible {
		outcome.sessionStickyState = "unavailable"
		return outcome
	}
	outcome.sessionStickyState = "forced_first"
	outcome.forcedAccountID = request.StickyAccountID
	return outcome
}

type diagnosticTopKSelection struct {
	minimumScore    float64
	weightSum       float64
	weights         map[int64]float64
	probabilities   map[int64]float64
	forcedAccountID int64
}

func diagnosticSelectionStats(topK []advancedSchedulerCandidateScore, forcedAccountID int64) diagnosticTopKSelection {
	selection := diagnosticTopKSelection{
		weights:       make(map[int64]float64, len(topK)),
		probabilities: make(map[int64]float64, len(topK)),
	}
	if len(topK) == 0 {
		return selection
	}
	selection.minimumScore = topK[0].score
	for _, candidate := range topK[1:] {
		if candidate.score < selection.minimumScore {
			selection.minimumScore = candidate.score
		}
	}
	for _, candidate := range topK {
		if candidate.account == nil {
			continue
		}
		weight := candidate.score - selection.minimumScore + 1
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
			weight = 1
		}
		selection.weights[candidate.account.ID] = weight
		selection.weightSum += weight
	}
	if selection.weightSum > 0 {
		for accountID, weight := range selection.weights {
			selection.probabilities[accountID] = weight / selection.weightSum
		}
	}
	if forcedAccountID > 0 {
		selection.forcedAccountID = forcedAccountID
		for accountID := range selection.probabilities {
			selection.probabilities[accountID] = 0
		}
		selection.probabilities[forcedAccountID] = 1
	}
	return selection
}

func diagnosticCandidatePool(
	candidates []advancedSchedulerCandidateScore,
	topK []advancedSchedulerCandidateScore,
	ranges advancedSchedulerScoreRanges,
	exclusions map[string]int,
	selection diagnosticTopKSelection,
) AdvancedSchedulerScoreDiagnosticCandidatePool {
	pool := AdvancedSchedulerScoreDiagnosticCandidatePool{
		EligibleCandidates:  len(candidates),
		ExcludedCandidates:  0,
		ExclusionReasons:    exclusions,
		TopK:                len(topK),
		NormalizationRanges: diagnosticRanges(ranges),
		Candidates:          make([]AdvancedSchedulerScoreDiagnosticCandidate, 0, len(candidates)),
	}
	for _, count := range exclusions {
		pool.ExcludedCandidates += count
	}
	pool.TotalCandidates = pool.EligibleCandidates + pool.ExcludedCandidates
	if len(topK) > 0 {
		minimum := selection.minimumScore
		weightSum := selection.weightSum
		pool.TopKMinimumScore = &minimum
		pool.TopKWeightSum = &weightSum
	}
	topKIDs := make(map[int64]struct{}, len(topK))
	for _, candidate := range topK {
		if candidate.account != nil {
			topKIDs[candidate.account.ID] = struct{}{}
		}
	}
	for index, candidate := range candidates {
		if candidate.account == nil {
			continue
		}
		_, inTopK := topKIDs[candidate.account.ID]
		item := AdvancedSchedulerScoreDiagnosticCandidate{
			ID:         candidate.account.ID,
			Name:       candidate.account.Name,
			Platform:   candidate.account.Platform,
			Priority:   candidate.account.Priority,
			FinalScore: candidate.score,
			Rank:       index + 1,
			InTopK:     inTopK,
		}
		if inTopK || selection.forcedAccountID == candidate.account.ID {
			if weight, found := selection.weights[candidate.account.ID]; found {
				item.SelectionWeight = &weight
			}
			if probability, found := selection.probabilities[candidate.account.ID]; found {
				item.SelectionProbability = &probability
			}
		}
		pool.Candidates = append(pool.Candidates, item)
	}
	return pool
}

func diagnosticRanges(ranges advancedSchedulerScoreRanges) AdvancedSchedulerScoreDiagnosticRanges {
	result := AdvancedSchedulerScoreDiagnosticRanges{
		PriorityMin:     ranges.MinPriority,
		PriorityMax:     ranges.MaxPriority,
		MaxWaitingCount: ranges.MaxWaiting,
	}
	if ranges.HasTTFTSample {
		minValue, maxValue := ranges.MinTTFT, ranges.MaxTTFT
		result.TTFTMinMs = &minValue
		result.TTFTMaxMs = &maxValue
	}
	if ranges.HasResetSample {
		minValue, maxValue := ranges.MinResetRemaining, ranges.MaxResetRemaining
		result.ResetMinSeconds = &minValue
		result.ResetMaxSeconds = &maxValue
	}
	return result
}

func findDiagnosticCandidate(candidates []advancedSchedulerCandidateScore, accountID int64) (*advancedSchedulerCandidateScore, int) {
	for index := range candidates {
		if candidates[index].account != nil && candidates[index].account.ID == accountID {
			return &candidates[index], index + 1
		}
	}
	return nil, 0
}

func diagnosticScore(
	candidate *advancedSchedulerCandidateScore,
	rank int,
	topK []advancedSchedulerCandidateScore,
	selection diagnosticTopKSelection,
	effective advancedSchedulerEffectiveSettings,
	request AdvancedSchedulerScoreDiagnosticRequest,
) *AdvancedSchedulerScoreDiagnosticScore {
	if candidate == nil {
		return nil
	}
	score := &AdvancedSchedulerScoreDiagnosticScore{
		BaseScore:     candidate.baseScore,
		StickyBonus:   candidate.stickyBonus,
		FinalScore:    candidate.score,
		Rank:          rank,
		Formula:       diagnosticFormula(candidate, effective.weights),
		SelectionMode: "top_k_weighted",
	}
	for _, item := range topK {
		if item.account == nil || item.account.ID != candidate.account.ID {
			continue
		}
		score.InTopK = true
		weight := selection.weights[candidate.account.ID]
		probability := selection.probabilities[candidate.account.ID]
		score.SelectionWeight = &weight
		score.SelectionProbability = &probability
		break
	}
	if selection.forcedAccountID > 0 {
		score.SelectionMode = "sticky_forced_first"
		if probability, found := selection.probabilities[candidate.account.ID]; found {
			score.SelectionProbability = &probability
		}
		if candidate.account != nil && candidate.account.ID == selection.forcedAccountID {
			if weight, found := selection.weights[candidate.account.ID]; found {
				score.SelectionWeight = &weight
			}
		}
	}
	return score
}

func diagnosticFormula(candidate *advancedSchedulerCandidateScore, weights GatewayAdvancedSchedulerScoreWeightsView) string {
	if candidate == nil {
		return ""
	}
	terms := []string{
		diagnosticFormulaTerm(weights.Priority, candidate.factors.Priority),
		diagnosticFormulaTerm(weights.Load, candidate.factors.Load),
		diagnosticFormulaTerm(weights.Queue, candidate.factors.Queue),
		diagnosticFormulaTerm(weights.ErrorRate, candidate.factors.ErrorRate),
		diagnosticFormulaTerm(weights.TTFT, candidate.factors.TTFT),
		diagnosticFormulaTerm(weights.Reset, candidate.factors.Reset),
		diagnosticFormulaTerm(weights.QuotaHeadroom, candidate.factors.QuotaHeadroom),
	}
	base := strings.Join(terms, " + ") + " = " + diagnosticFloat(candidate.baseScore)
	if candidate.stickyBonus == 0 {
		return base
	}
	bonus := make([]string, 0, 2)
	if candidate.previousBonus != 0 {
		bonus = append(bonus, diagnosticFloat(candidate.previousBonus))
	}
	if candidate.sessionStickyBonus != 0 {
		bonus = append(bonus, diagnosticFloat(candidate.sessionStickyBonus))
	}
	return base + "；粘性加成 " + strings.Join(bonus, " + ") + "；最终 = " + diagnosticFloat(candidate.score)
}

func diagnosticFormulaTerm(weight, factor float64) string {
	return diagnosticFloat(weight) + "×" + diagnosticFloat(factor)
}

func diagnosticFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 4, 64)
}

func diagnosticMetrics(
	candidate *advancedSchedulerCandidateScore,
	ranges advancedSchedulerScoreRanges,
	weights GatewayAdvancedSchedulerScoreWeightsView,
	now time.Time,
) []AdvancedSchedulerScoreDiagnosticMetric {
	if candidate == nil || candidate.account == nil {
		return []AdvancedSchedulerScoreDiagnosticMetric{}
	}
	metrics := []AdvancedSchedulerScoreDiagnosticMetric{
		{
			Key:                  "priority",
			RawValue:             strconv.Itoa(candidate.account.Priority),
			Normalization:        diagnosticPriorityNormalization(candidate.account.Priority, ranges),
			NormalizedValue:      candidate.factors.Priority,
			Weight:               weights.Priority,
			WeightedContribution: weights.Priority * candidate.factors.Priority,
			Available:            true,
			Source:               "account.priority",
		},
		diagnosticLoadMetric(candidate, weights),
		diagnosticQueueMetric(candidate, ranges, weights),
		diagnosticErrorRateMetric(candidate, weights),
		diagnosticTTFTMetric(candidate, ranges, weights),
		diagnosticResetMetric(candidate, ranges, weights, now),
		diagnosticQuotaMetric(candidate, weights, now),
	}
	return metrics
}

func diagnosticPriorityNormalization(priority int, ranges advancedSchedulerScoreRanges) string {
	if ranges.MaxPriority <= ranges.MinPriority {
		return "候选优先级相同，使用 1.0000"
	}
	return "1 - (" + strconv.Itoa(priority) + " - " + strconv.Itoa(ranges.MinPriority) + ") / (" + strconv.Itoa(ranges.MaxPriority) + " - " + strconv.Itoa(ranges.MinPriority) + ")"
}

func diagnosticLoadMetric(candidate *advancedSchedulerCandidateScore, weights GatewayAdvancedSchedulerScoreWeightsView) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "load",
		NormalizedValue:      candidate.factors.Load,
		Weight:               weights.Load,
		WeightedContribution: weights.Load * candidate.factors.Load,
		Source:               "concurrency.load_rate",
	}
	if !candidate.loadKnown {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = strconv.Itoa(candidate.loadInfo.LoadRate) + "%"
	metric.Normalization = "1 - clamp(" + strconv.Itoa(candidate.loadInfo.LoadRate) + " / 100)"
	return metric
}

func diagnosticQueueMetric(candidate *advancedSchedulerCandidateScore, ranges advancedSchedulerScoreRanges, weights GatewayAdvancedSchedulerScoreWeightsView) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "queue",
		NormalizedValue:      candidate.factors.Queue,
		Weight:               weights.Queue,
		WeightedContribution: weights.Queue * candidate.factors.Queue,
		Source:               "concurrency.waiting_count",
	}
	if !candidate.loadKnown {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = strconv.Itoa(candidate.loadInfo.WaitingCount)
	metric.Normalization = "1 - clamp(" + strconv.Itoa(candidate.loadInfo.WaitingCount) + " / " + strconv.Itoa(ranges.MaxWaiting) + ")"
	return metric
}

func diagnosticErrorRateMetric(candidate *advancedSchedulerCandidateScore, weights GatewayAdvancedSchedulerScoreWeightsView) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "error_rate",
		NormalizedValue:      candidate.factors.ErrorRate,
		Weight:               weights.ErrorRate,
		WeightedContribution: weights.ErrorRate * candidate.factors.ErrorRate,
		Source:               "advanced_scheduler.error_rate_ewma",
	}
	if !candidate.feedback.HasFeedback {
		metric.RawValue = "0%（未观测）"
		metric.Normalization = "未观测，错误率按 0% 计算：1 - 0 = 1.0000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = "EWMA " + diagnosticFloat(candidate.feedback.ErrorRate) + "（" + strconv.FormatInt(candidate.feedback.ErrorSamples, 10) + " 样本）"
	metric.Normalization = "1 - clamp(" + diagnosticFloat(candidate.feedback.ErrorRate) + ")"
	metric.ObservedAt = candidate.feedback.LastObservedAt
	return metric
}

func diagnosticTTFTMetric(candidate *advancedSchedulerCandidateScore, ranges advancedSchedulerScoreRanges, weights GatewayAdvancedSchedulerScoreWeightsView) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "ttft",
		NormalizedValue:      candidate.factors.TTFT,
		Weight:               weights.TTFT,
		WeightedContribution: weights.TTFT * candidate.factors.TTFT,
		Source:               "advanced_scheduler.ttft_ewma",
	}
	if !candidate.feedback.HasTTFT {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = diagnosticFloat(candidate.feedback.TTFT) + " ms（" + strconv.FormatInt(candidate.feedback.TTFTSamples, 10) + " 样本）"
	if !ranges.HasTTFTSample || ranges.MaxTTFT <= ranges.MinTTFT {
		metric.Normalization = "候选 TTFT 相同，使用中性值 0.5000"
		metric.Neutral = true
	} else {
		metric.Normalization = "1 - clamp((" + diagnosticFloat(candidate.feedback.TTFT) + " - " + diagnosticFloat(ranges.MinTTFT) + ") / (" + diagnosticFloat(ranges.MaxTTFT) + " - " + diagnosticFloat(ranges.MinTTFT) + "))"
	}
	metric.ObservedAt = candidate.feedback.LastTTFTAt
	return metric
}

func diagnosticResetMetric(candidate *advancedSchedulerCandidateScore, ranges advancedSchedulerScoreRanges, weights GatewayAdvancedSchedulerScoreWeightsView, now time.Time) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "reset",
		NormalizedValue:      candidate.factors.Reset,
		Weight:               weights.Reset,
		WeightedContribution: weights.Reset * candidate.factors.Reset,
		Source:               "account.session_window_end",
	}
	if weights.Reset <= 0 {
		metric.RawValue = "权重为 0，未参与评分"
		metric.Normalization = "不适用，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	if candidate.account.SessionWindowEnd == nil || !now.Before(*candidate.account.SessionWindowEnd) {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	remaining := candidate.account.SessionWindowEnd.Sub(now).Seconds()
	metric.Available = true
	metric.RawValue = diagnosticFloat(remaining) + " 秒"
	if !ranges.HasResetSample || ranges.MaxResetRemaining <= ranges.MinResetRemaining {
		metric.Normalization = "候选窗口重置时间相同，使用 1.0000"
	} else {
		metric.Normalization = "1 - clamp((" + diagnosticFloat(remaining) + " - " + diagnosticFloat(ranges.MinResetRemaining) + ") / (" + diagnosticFloat(ranges.MaxResetRemaining) + " - " + diagnosticFloat(ranges.MinResetRemaining) + "))"
	}
	return metric
}

func diagnosticQuotaMetric(candidate *advancedSchedulerCandidateScore, weights GatewayAdvancedSchedulerScoreWeightsView, now time.Time) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "quota_headroom",
		NormalizedValue:      candidate.factors.QuotaHeadroom,
		Weight:               weights.QuotaHeadroom,
		WeightedContribution: weights.QuotaHeadroom * candidate.factors.QuotaHeadroom,
		Source:               "platform.quota_headroom",
	}
	if weights.QuotaHeadroom <= 0 {
		metric.RawValue = "权重为 0，未参与评分"
		metric.Normalization = "不适用，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	if candidate.account.Platform != PlatformOpenAI && candidate.account.Platform != PlatformGrok {
		metric.RawValue = "当前平台未提供配额余量信号"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	factor := openAIQuotaHeadroomFactor(candidate.account, now)
	metric.Available = factor != openAIQuotaHeadroomNeutralFactor
	metric.Neutral = !metric.Available
	metric.RawValue = diagnosticFloat(factor)
	if metric.Neutral {
		metric.RawValue = "未观测或过期，0.5000"
		metric.Normalization = "未观测，使用中性值 0.5000"
	} else {
		metric.Normalization = "平台适配器提供的 0..1 quota headroom"
	}
	return metric
}

func diagnosticEffectiveSettings(group *Group, runtime advancedSchedulerRuntimeSettings, effective advancedSchedulerEffectiveSettings) []AdvancedSchedulerScoreDiagnosticSetting {
	overrides := GroupAdvancedSchedulerOverrides{}
	if group != nil {
		overrides = group.AdvancedSchedulerOverrides
	}
	settingSource := func(groupOverride bool, runtimeOverride bool) string {
		if groupOverride {
			return "group_override"
		}
		if runtimeOverride {
			return "global_runtime"
		}
		return "process_default"
	}
	settings := []AdvancedSchedulerScoreDiagnosticSetting{
		{Key: "sticky_weighted_enabled", Value: strconv.FormatBool(effective.stickyWeightedEnabled), Source: settingSource(overrides.StickyWeightedEnabled != nil, true)},
		{Key: "subscription_priority_enabled", Value: strconv.FormatBool(effective.subscriptionPriorityEnabled), Source: settingSource(overrides.SubscriptionPriorityEnabled != nil, true)},
		{Key: "ewma_error_rate_alpha", Value: diagnosticFloat(effective.feedback.errorRateAlpha), Source: settingSource(overrides.EWMAErrorRateAlpha != nil, runtime.ewmaErrorRateAlphaSet)},
		{Key: "ewma_ttft_alpha", Value: diagnosticFloat(effective.feedback.ttftAlpha), Source: settingSource(overrides.EWMATTFTAlpha != nil, runtime.ewmaTTFTAlphaSet)},
		{Key: "sticky_escape_enabled", Value: strconv.FormatBool(effective.stickyEscape.enabled), Source: settingSource(overrides.StickyEscapeEnabled != nil, runtime.stickyEscapeEnabledSet)},
		{Key: "sticky_escape_ttft_ms", Value: diagnosticFloat(effective.stickyEscape.ttftMs), Source: settingSource(overrides.StickyEscapeTTFTMs != nil, runtime.stickyEscapeTTFTMsSet)},
		{Key: "sticky_escape_error_rate", Value: diagnosticFloat(effective.stickyEscape.errorRate), Source: settingSource(overrides.StickyEscapeErrorRate != nil, runtime.stickyEscapeErrorRateSet)},
		{Key: "lb_top_k", Value: strconv.Itoa(effective.topK), Source: settingSource(overrides.LBTopK != nil, runtime.lbTopKOverride > 0)},
	}
	for _, item := range []struct {
		key           string
		value         float64
		groupOverride *float64
		runtimeKey    string
	}{
		{"weight_priority", effective.weights.Priority, overrides.WeightPriority, "priority"},
		{"weight_load", effective.weights.Load, overrides.WeightLoad, "load"},
		{"weight_queue", effective.weights.Queue, overrides.WeightQueue, "queue"},
		{"weight_error_rate", effective.weights.ErrorRate, overrides.WeightErrorRate, "error_rate"},
		{"weight_ttft", effective.weights.TTFT, overrides.WeightTTFT, "ttft"},
		{"weight_reset", effective.weights.Reset, overrides.WeightReset, "reset"},
		{"weight_quota_headroom", effective.weights.QuotaHeadroom, overrides.WeightQuotaHeadroom, "quota_headroom"},
		{"weight_previous_response", effective.weights.Previous, overrides.WeightPreviousResponse, "previous_response"},
		{"weight_session_sticky", effective.weights.SessionSticky, overrides.WeightSessionSticky, "session_sticky"},
	} {
		_, hasRuntimeOverride := runtime.weightOverrides[item.runtimeKey]
		settings = append(settings, AdvancedSchedulerScoreDiagnosticSetting{
			Key: item.key, Value: diagnosticFloat(item.value), Source: settingSource(item.groupOverride != nil, hasRuntimeOverride),
		})
	}
	return settings
}

func diagnosticPolicySignals(
	group *Group,
	request AdvancedSchedulerScoreDiagnosticRequest,
	effective advancedSchedulerEffectiveSettings,
	outcome diagnosticPolicyOutcome,
) []AdvancedSchedulerScoreDiagnosticPolicySignal {
	signals := make([]AdvancedSchedulerScoreDiagnosticPolicySignal, 0, 4)
	if request.PreviousResponseAccountID > 0 {
		state := outcome.previousResponseState
		if state == "" {
			state = "ignored"
		}
		signals = append(signals, AdvancedSchedulerScoreDiagnosticPolicySignal{
			Key: "previous_response_binding", State: state,
			Detail: "仅使用管理员输入的账号 ID 模拟上一响应粘性；不会读取响应正文或响应标识。",
		})
	}
	if request.StickyAccountID > 0 {
		state := outcome.sessionStickyState
		if state == "" {
			state = "ignored"
		}
		detail := "仅使用管理员输入的账号 ID 模拟会话粘性；不会读取或写入 session hash。"
		if outcome.stickyEscapeReason != "" {
			detail += " 当前运行时反馈触发粘性逃逸：" + outcome.stickyEscapeReason + "。"
		}
		signals = append(signals, AdvancedSchedulerScoreDiagnosticPolicySignal{
			Key: "session_sticky", State: state,
			Detail: detail,
		})
	}
	if group != nil && (group.Platform == PlatformOpenAI || group.Platform == PlatformGrok) && effective.subscriptionPriorityEnabled {
		state := "enabled"
		detail := "当前没有可用订阅账号，使用完整候选池。"
		if outcome.subscriptionPoolActive {
			state = "active_pool"
			detail = "当前存在可用订阅账号，排名、Top-K 与概率仅基于订阅池计算。"
		}
		signals = append(signals,
			AdvancedSchedulerScoreDiagnosticPolicySignal{
				Key: "subscription_priority", State: state,
				Detail: detail,
			},
		)
	}
	signals = append(signals, AdvancedSchedulerScoreDiagnosticPolicySignal{
		Key:    "request_capabilities",
		State:  "not_evaluated",
		Detail: "诊断请求未提供端点、传输协议、compact 与会话注册上下文，这些请求级门禁不参与本次结果。",
	})
	return signals
}
