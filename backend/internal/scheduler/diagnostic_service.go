package scheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
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

// DiagnosticAccount 仅含评分与展示字段。ProjectionID 是本次读取的临时关联号，不携带凭据或写能力。
type DiagnosticAccount struct {
	ProjectionID                                                                         uint64
	ID                                                                                   int64
	Name, Platform, Type, Status                                                         string
	Priority, LoadFactor                                                                 int
	Schedulable, AutoPauseOnExpired, PrivacySet, MixedScheduling, SubscriptionPriority   bool
	ExpiresAt, OverloadUntil, RateLimitResetAt, TempUnschedulableUntil, SessionWindowEnd *time.Time
	GroupIDs                                                                             []int64
	Groups                                                                               []*DiagnosticGroup
	AccountGroups                                                                        []DiagnosticAccountGroup
}
type DiagnosticAccountGroup struct{ Group *DiagnosticGroup }
type DiagnosticGroup struct {
	ProjectionID                uint64
	ID                          int64
	Name, Platform              string
	SortOrder                   int
	Advanced, RequirePrivacySet bool
	AdvancedSchedulerOverrides  policy.GroupAdvancedSchedulerOverrides
}

func (g *DiagnosticGroup) UsesAdvancedScheduler() bool { return g != nil && g.Advanced }

type DiagnosticSource interface {
	GetAccount(context.Context, int64) (*DiagnosticAccount, error)
	GetGroup(context.Context, int64) (*DiagnosticGroup, error)
	ListAccountsForSchedulerScoreFilter(context.Context, string, string, string, string, int64, string) ([]DiagnosticAccount, error)
	ListSchedulableAccountsForAdvancedSchedulerScore(context.Context, *int64, string) ([]DiagnosticAccount, error)
}

// DiagnosticPorts 只允许读取平台资格、预取观测和已共享的反馈，不暴露槽位、粘性或写入。
type DiagnosticPorts struct {
	Effective func(context.Context, *DiagnosticGroup) (policy.EffectiveSettings, policy.RuntimeSettings)
	Prepare   func(context.Context, *DiagnosticGroup, []DiagnosticAccount) context.Context
	Filter    func(context.Context, *DiagnosticAccount, *DiagnosticGroup, AdvancedSchedulerScoreDiagnosticRequest, time.Time) string
	Quota     func(uint64, time.Time) float64
	Stats     func() *RuntimeStats
	Now       func() time.Time
}
type DiagnosticService struct {
	source             DiagnosticSource
	concurrencyService *ConcurrencyService
	ports              DiagnosticPorts
	now                func() time.Time
}

func NewDiagnosticService(source DiagnosticSource, concurrency *ConcurrencyService, ports DiagnosticPorts) *DiagnosticService {
	return &DiagnosticService{source: source, concurrencyService: concurrency, ports: ports, now: ports.Now}
}
func (s *DiagnosticService) effectiveSettings(ctx context.Context, g *DiagnosticGroup) (policy.EffectiveSettings, policy.RuntimeSettings) {
	return s.ports.Effective(ctx, g)
}
func (s *DiagnosticService) prepareEligibilityContext(ctx context.Context, g *DiagnosticGroup, a []DiagnosticAccount) context.Context {
	if s.ports.Prepare == nil {
		return ctx
	}
	return s.ports.Prepare(ctx, g, a)
}
func (s *DiagnosticService) HardFilterReason(ctx context.Context, a *DiagnosticAccount, g *DiagnosticGroup, request AdvancedSchedulerScoreDiagnosticRequest, now time.Time) string {
	if reason := diagnosticBaseHardFilterReason(a, g, now); reason != "" {
		return reason
	}
	if s.ports.Filter == nil {
		return ""
	}
	return s.ports.Filter(ctx, a, g, request, now)
}
func (s *DiagnosticService) diagnosticHardFilterReason(ctx context.Context, a *DiagnosticAccount, g *DiagnosticGroup, request AdvancedSchedulerScoreDiagnosticRequest, now time.Time) string {
	return s.HardFilterReason(ctx, a, g, request, now)
}
func partitionDiagnosticSubscriptionAccounts(accounts []*DiagnosticAccount) ([]*DiagnosticAccount, []*DiagnosticAccount) {
	var subscribed, regular []*DiagnosticAccount
	for _, a := range accounts {
		if a != nil && a.SubscriptionPriority {
			subscribed = append(subscribed, a)
		} else {
			regular = append(regular, a)
		}
	}
	return subscribed, regular
}
func (s *DiagnosticService) quotaHeadroom(a *ScoreAccount, now time.Time) float64 {
	if s.ports.Quota == nil {
		return 0.5
	}
	return s.ports.Quota(a.ProjectionID, now)
}
func (s *DiagnosticService) scoreCandidates(accounts []*DiagnosticAccount, loads map[int64]*AccountLoadInfo, stats *RuntimeStats, weights policy.ScoreWeights, input ScoreInput, now time.Time) ([]CandidateScore, float64, ScoreRanges) {
	projected := make([]*ScoreAccount, len(accounts))
	for i, a := range accounts {
		if a != nil {
			projected[i] = &ScoreAccount{ProjectionID: a.ProjectionID, ID: a.ID, Name: a.Name, Platform: a.Platform, Priority: a.Priority, SessionWindowEnd: a.SessionWindowEnd}
		}
	}
	return ScoreCandidatesWithRanges(projected, loads, stats, weights, input, now)
}

// GetOverview 返回账号所属高级分组的轻量摘要。
func (s *DiagnosticService) GetOverview(ctx context.Context, accountID int64) (*AdvancedSchedulerScoreDiagnosticResponse, error) {
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
func (s *DiagnosticService) GetDetail(ctx context.Context, accountID int64, request AdvancedSchedulerScoreDiagnosticRequest) (*AdvancedSchedulerScoreDiagnosticResponse, error) {
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

	var selected *DiagnosticGroup
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

func (s *DiagnosticService) loadAccountAndAdvancedGroups(ctx context.Context, accountID int64) (*DiagnosticAccount, []*DiagnosticGroup, error) {
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
	groupsByID := make(map[int64]*DiagnosticGroup)
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

	groups := make([]*DiagnosticGroup, 0, len(groupsByID))
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

func (s *DiagnosticService) newResponse(account *DiagnosticAccount) *AdvancedSchedulerScoreDiagnosticResponse {
	return &AdvancedSchedulerScoreDiagnosticResponse{
		Account:            diagnosticAccountSummary(account),
		GeneratedAt:        s.now().UTC(),
		CalculationVersion: AdvancedSchedulerScoreCalculationVersion,
		Groups:             make([]AdvancedSchedulerScoreDiagnosticGroupSummary, 0),
	}
}

func diagnosticAccountSummary(account *DiagnosticAccount) AdvancedSchedulerScoreDiagnosticAccount {
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

func diagnosticGroupSummary(group *DiagnosticGroup) AdvancedSchedulerScoreDiagnosticGroup {
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
func (s *DiagnosticService) buildGroupSummary(
	ctx context.Context,
	target *DiagnosticAccount,
	group *DiagnosticGroup,
) (AdvancedSchedulerScoreDiagnosticGroupSummary, error) {
	summary := AdvancedSchedulerScoreDiagnosticGroupSummary{
		AdvancedSchedulerScoreDiagnosticGroup: diagnosticGroupSummary(group),
		Status:                                "eligible",
	}
	if target == nil || group == nil {
		summary.Status = "filtered"
		return summary, nil
	}
	now := s.now()
	effective, _ := s.effectiveSettings(ctx, group)
	poolAccounts, err := s.source.ListSchedulableAccountsForAdvancedSchedulerScore(ctx, &group.ID, group.Platform)
	if err != nil {
		return summary, err
	}
	ctx = s.prepareEligibilityContext(ctx, group, poolAccounts)
	filtered := make([]*DiagnosticAccount, 0, len(poolAccounts))
	for index := range poolAccounts {
		candidate := poolAccounts[index]
		if s.diagnosticHardFilterReason(ctx, &candidate, group, AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID}, now) == "" {
			filtered = append(filtered, &candidate)
		}
	}
	filtered, _, _ = diagnosticSubscriptionPriorityPool(filtered, group, effective)
	loadMap := s.LoadMap(ctx, filtered)
	var stats *RuntimeStats
	if s.ports.Stats != nil {
		stats = s.ports.Stats()
	}
	candidates, _, _ := s.scoreCandidates(
		filtered,
		loadMap,
		stats,
		effective.Weights,
		ScoreInput{
			GroupID:             &group.ID,
			StickyWeighted:      effective.StickyWeightedEnabled,
			TopK:                effective.TopK,
			QuotaHeadroomFactor: s.quotaHeadroom,
		},
		now,
	)
	targetCandidate, _ := findDiagnosticCandidate(candidates, target.ID)
	if targetCandidate == nil {
		summary.Status = "filtered"
		return summary, nil
	}
	summary.Eligible = true
	finalScore := targetCandidate.Score
	summary.FinalScore = &finalScore
	return summary, nil
}

func (s *DiagnosticService) buildDetail(
	ctx context.Context,
	target *DiagnosticAccount,
	group *DiagnosticGroup,
	request AdvancedSchedulerScoreDiagnosticRequest,
) (*AdvancedSchedulerScoreDiagnosticDetail, error) {
	now := s.now()
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
	stats := (*RuntimeStats)(nil)
	if s.ports.Stats != nil {
		stats = s.ports.Stats()
	}
	eligibilityRequest := request
	// 硬粘性逃逸后，生产调度会按普通候选执行费用与 RPM 门禁；诊断必须使用相同语义。
	if !effective.StickyWeightedEnabled && request.StickyAccountID > 0 {
		if _, _, _, escaped := ShouldEscapeSticky(stats, request.StickyAccountID, effective.StickyEscape); escaped {
			eligibilityRequest.StickyAccountID = 0
		}
	}

	exclusions := make(map[string]int)
	filtered := make([]*DiagnosticAccount, 0, len(poolAccounts))
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

	policyOutcome := diagnosticHardStickyPolicyOutcome(filtered, group, request, effective, stats, effective.StickyEscape)
	deferredAccountIDs := map[int64]struct{}{}
	if policyOutcome.forcedAccountID == 0 {
		var subscriptionPoolActive bool
		filtered, deferredAccountIDs, subscriptionPoolActive = diagnosticSubscriptionPriorityPool(filtered, group, effective)
		policyOutcome.subscriptionPoolActive = subscriptionPoolActive
		if len(deferredAccountIDs) > 0 {
			exclusions["subscription_priority_deferred"] += len(deferredAccountIDs)
		}
	}

	loadMap := s.LoadMap(ctx, filtered)
	previousResponseAccountID := int64(0)
	if group != nil && group.Platform == capability.PlatformOpenAI {
		previousResponseAccountID = request.PreviousResponseAccountID
	}
	input := ScoreInput{
		GroupID:                 &group.ID,
		RequestedModel:          request.RequestedModel,
		StickyAccountID:         request.StickyAccountID,
		StickyPreviousAccountID: previousResponseAccountID,
		StickyWeighted:          effective.StickyWeightedEnabled,
		TopK:                    effective.TopK,
		QuotaHeadroomFactor:     s.quotaHeadroom,
	}
	candidates, _, ranges := s.scoreCandidates(filtered, loadMap, stats, effective.Weights, input, now)
	sort.SliceStable(candidates, func(i, j int) bool {
		return CandidateBetter(candidates[i], candidates[j])
	})
	topKCandidates := SelectTopK(candidates, effective.TopK)
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
	detail.Metrics = s.diagnosticMetrics(targetCandidate, ranges, effective.Weights, now)
	return detail, nil
}

func (s *DiagnosticService) LoadMap(ctx context.Context, accounts []*DiagnosticAccount) map[int64]*AccountLoadInfo {
	if s == nil || s.concurrencyService == nil || len(accounts) == 0 {
		return map[int64]*AccountLoadInfo{}
	}
	loads := make([]AccountWithConcurrency, 0, len(accounts))
	for _, account := range accounts {
		if account != nil {
			loads = append(loads, AccountWithConcurrency{ID: account.ID, MaxConcurrency: account.LoadFactor})
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

func diagnosticBaseHardFilterReason(account *DiagnosticAccount, group *DiagnosticGroup, now time.Time) string {
	if account == nil {
		return "account_missing"
	}
	if !diagnosticPlatformMatchesGroup(account, group) {
		return "platform_mismatch"
	}
	if account.Status != "active" {
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
	if group != nil && group.RequirePrivacySet && !account.PrivacySet {
		return "privacy_not_set"
	}
	return ""
}

func diagnosticPlatformMatchesGroup(account *DiagnosticAccount, group *DiagnosticGroup) bool {
	if account == nil || group == nil {
		return false
	}
	if account.Platform == group.Platform {
		return true
	}
	return (group.Platform == capability.PlatformAnthropic || group.Platform == capability.PlatformGemini) &&
		account.Platform == capability.PlatformAntigravity && account.MixedScheduling
}

func diagnosticSubscriptionPriorityPool(
	accounts []*DiagnosticAccount,
	group *DiagnosticGroup,
	effective policy.EffectiveSettings,
) ([]*DiagnosticAccount, map[int64]struct{}, bool) {
	deferred := make(map[int64]struct{})
	if group == nil || !effective.SubscriptionPriorityEnabled ||
		(group.Platform != capability.PlatformOpenAI && group.Platform != capability.PlatformGrok) {
		return accounts, deferred, false
	}
	subscriptionAccounts, regularAccounts := partitionDiagnosticSubscriptionAccounts(accounts)
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
	accounts []*DiagnosticAccount,
	group *DiagnosticGroup,
	request AdvancedSchedulerScoreDiagnosticRequest,
	effective policy.EffectiveSettings,
	stats *RuntimeStats,
	escapeConfig policy.StickyEscapeConfig,
) diagnosticPolicyOutcome {
	outcome := diagnosticPolicyOutcome{}
	eligibleIDs := make(map[int64]struct{}, len(accounts))
	for _, account := range accounts {
		if account != nil {
			eligibleIDs[account.ID] = struct{}{}
		}
	}
	if effective.StickyWeightedEnabled {
		if request.PreviousResponseAccountID > 0 {
			if group == nil || group.Platform != capability.PlatformOpenAI {
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
		if group == nil || group.Platform != capability.PlatformOpenAI {
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
	if reason, _, _, escape := ShouldEscapeSticky(stats, request.StickyAccountID, escapeConfig); escape {
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

func diagnosticSelectionStats(topK []CandidateScore, forcedAccountID int64) diagnosticTopKSelection {
	selection := diagnosticTopKSelection{
		weights:       make(map[int64]float64, len(topK)),
		probabilities: make(map[int64]float64, len(topK)),
	}
	if len(topK) == 0 {
		return selection
	}
	selection.minimumScore = topK[0].Score
	for _, candidate := range topK[1:] {
		if candidate.Score < selection.minimumScore {
			selection.minimumScore = candidate.Score
		}
	}
	for _, candidate := range topK {
		if candidate.Account == nil {
			continue
		}
		weight := candidate.Score - selection.minimumScore + 1
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
			weight = 1
		}
		selection.weights[candidate.Account.ID] = weight
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
	candidates []CandidateScore,
	topK []CandidateScore,
	ranges ScoreRanges,
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
		if candidate.Account != nil {
			topKIDs[candidate.Account.ID] = struct{}{}
		}
	}
	for index, candidate := range candidates {
		if candidate.Account == nil {
			continue
		}
		_, inTopK := topKIDs[candidate.Account.ID]
		item := AdvancedSchedulerScoreDiagnosticCandidate{
			ID:         candidate.Account.ID,
			Name:       candidate.Account.Name,
			Platform:   candidate.Account.Platform,
			Priority:   candidate.Account.Priority,
			FinalScore: candidate.Score,
			Rank:       index + 1,
			InTopK:     inTopK,
		}
		if inTopK || selection.forcedAccountID == candidate.Account.ID {
			if weight, found := selection.weights[candidate.Account.ID]; found {
				item.SelectionWeight = &weight
			}
			if probability, found := selection.probabilities[candidate.Account.ID]; found {
				item.SelectionProbability = &probability
			}
		}
		pool.Candidates = append(pool.Candidates, item)
	}
	return pool
}

func diagnosticRanges(ranges ScoreRanges) AdvancedSchedulerScoreDiagnosticRanges {
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

func findDiagnosticCandidate(candidates []CandidateScore, accountID int64) (*CandidateScore, int) {
	for index := range candidates {
		if candidates[index].Account != nil && candidates[index].Account.ID == accountID {
			return &candidates[index], index + 1
		}
	}
	return nil, 0
}

func diagnosticScore(
	candidate *CandidateScore,
	rank int,
	topK []CandidateScore,
	selection diagnosticTopKSelection,
	effective policy.EffectiveSettings,
	request AdvancedSchedulerScoreDiagnosticRequest,
) *AdvancedSchedulerScoreDiagnosticScore {
	if candidate == nil {
		return nil
	}
	score := &AdvancedSchedulerScoreDiagnosticScore{
		BaseScore:     candidate.BaseScore,
		StickyBonus:   candidate.StickyBonus,
		FinalScore:    candidate.Score,
		Rank:          rank,
		Formula:       diagnosticFormula(candidate, effective.Weights),
		SelectionMode: "top_k_weighted",
	}
	for _, item := range topK {
		if item.Account == nil || item.Account.ID != candidate.Account.ID {
			continue
		}
		score.InTopK = true
		weight := selection.weights[candidate.Account.ID]
		probability := selection.probabilities[candidate.Account.ID]
		score.SelectionWeight = &weight
		score.SelectionProbability = &probability
		break
	}
	if selection.forcedAccountID > 0 {
		score.SelectionMode = "sticky_forced_first"
		if probability, found := selection.probabilities[candidate.Account.ID]; found {
			score.SelectionProbability = &probability
		}
		if candidate.Account != nil && candidate.Account.ID == selection.forcedAccountID {
			if weight, found := selection.weights[candidate.Account.ID]; found {
				score.SelectionWeight = &weight
			}
		}
	}
	return score
}

func diagnosticFormula(candidate *CandidateScore, weights policy.ScoreWeights) string {
	if candidate == nil {
		return ""
	}
	terms := []string{
		diagnosticFormulaTerm(weights.Priority, candidate.Factors.Priority),
		diagnosticFormulaTerm(weights.Load, candidate.Factors.Load),
		diagnosticFormulaTerm(weights.Queue, candidate.Factors.Queue),
		diagnosticFormulaTerm(weights.ErrorRate, candidate.Factors.ErrorRate),
		diagnosticFormulaTerm(weights.TTFT, candidate.Factors.TTFT),
		diagnosticFormulaTerm(weights.Reset, candidate.Factors.Reset),
		diagnosticFormulaTerm(weights.QuotaHeadroom, candidate.Factors.QuotaHeadroom),
	}
	base := strings.Join(terms, " + ") + " = " + diagnosticFloat(candidate.BaseScore)
	if candidate.StickyBonus == 0 {
		return base
	}
	bonus := make([]string, 0, 2)
	if candidate.PreviousBonus != 0 {
		bonus = append(bonus, diagnosticFloat(candidate.PreviousBonus))
	}
	if candidate.SessionStickyBonus != 0 {
		bonus = append(bonus, diagnosticFloat(candidate.SessionStickyBonus))
	}
	return base + "；粘性加成 " + strings.Join(bonus, " + ") + "；最终 = " + diagnosticFloat(candidate.Score)
}

func diagnosticFormulaTerm(weight, factor float64) string {
	return diagnosticFloat(weight) + "×" + diagnosticFloat(factor)
}

func diagnosticFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 4, 64)
}

func (s *DiagnosticService) diagnosticMetrics(
	candidate *CandidateScore,
	ranges ScoreRanges,
	weights policy.ScoreWeights,
	now time.Time,
) []AdvancedSchedulerScoreDiagnosticMetric {
	if candidate == nil || candidate.Account == nil {
		return []AdvancedSchedulerScoreDiagnosticMetric{}
	}
	metrics := []AdvancedSchedulerScoreDiagnosticMetric{
		{
			Key:                  "priority",
			RawValue:             strconv.Itoa(candidate.Account.Priority),
			Normalization:        diagnosticPriorityNormalization(candidate.Account.Priority, ranges),
			NormalizedValue:      candidate.Factors.Priority,
			Weight:               weights.Priority,
			WeightedContribution: weights.Priority * candidate.Factors.Priority,
			Available:            true,
			Source:               "account.Priority",
		},
		diagnosticLoadMetric(candidate, weights),
		diagnosticQueueMetric(candidate, ranges, weights),
		diagnosticErrorRateMetric(candidate, weights),
		diagnosticTTFTMetric(candidate, ranges, weights),
		diagnosticResetMetric(candidate, ranges, weights, now),
		s.diagnosticQuotaMetric(candidate, weights, now),
	}
	return metrics
}

func diagnosticPriorityNormalization(priority int, ranges ScoreRanges) string {
	if ranges.MaxPriority <= ranges.MinPriority {
		return "候选优先级相同，使用 1.0000"
	}
	return "1 - (" + strconv.Itoa(priority) + " - " + strconv.Itoa(ranges.MinPriority) + ") / (" + strconv.Itoa(ranges.MaxPriority) + " - " + strconv.Itoa(ranges.MinPriority) + ")"
}

func diagnosticLoadMetric(candidate *CandidateScore, weights policy.ScoreWeights) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "load",
		NormalizedValue:      candidate.Factors.Load,
		Weight:               weights.Load,
		WeightedContribution: weights.Load * candidate.Factors.Load,
		Source:               "concurrency.load_rate",
	}
	if !candidate.LoadKnown {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = strconv.Itoa(candidate.LoadInfo.LoadRate) + "%"
	metric.Normalization = "1 - clamp(" + strconv.Itoa(candidate.LoadInfo.LoadRate) + " / 100)"
	return metric
}

func diagnosticQueueMetric(candidate *CandidateScore, ranges ScoreRanges, weights policy.ScoreWeights) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "queue",
		NormalizedValue:      candidate.Factors.Queue,
		Weight:               weights.Queue,
		WeightedContribution: weights.Queue * candidate.Factors.Queue,
		Source:               "concurrency.waiting_count",
	}
	if !candidate.LoadKnown {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = strconv.Itoa(candidate.LoadInfo.WaitingCount)
	metric.Normalization = "1 - clamp(" + strconv.Itoa(candidate.LoadInfo.WaitingCount) + " / " + strconv.Itoa(ranges.MaxWaiting) + ")"
	return metric
}

func diagnosticErrorRateMetric(candidate *CandidateScore, weights policy.ScoreWeights) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "error_rate",
		NormalizedValue:      candidate.Factors.ErrorRate,
		Weight:               weights.ErrorRate,
		WeightedContribution: weights.ErrorRate * candidate.Factors.ErrorRate,
		Source:               "advanced_scheduler.error_rate_ewma",
	}
	if !candidate.Feedback.HasFeedback {
		metric.RawValue = "0%（未观测）"
		metric.Normalization = "未观测，错误率按 0% 计算：1 - 0 = 1.0000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = "EWMA " + diagnosticFloat(candidate.Feedback.ErrorRate) + "（" + strconv.FormatInt(candidate.Feedback.ErrorSamples, 10) + " 样本）"
	metric.Normalization = "1 - clamp(" + diagnosticFloat(candidate.Feedback.ErrorRate) + ")"
	metric.ObservedAt = candidate.Feedback.LastObservedAt
	return metric
}

func diagnosticTTFTMetric(candidate *CandidateScore, ranges ScoreRanges, weights policy.ScoreWeights) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "ttft",
		NormalizedValue:      candidate.Factors.TTFT,
		Weight:               weights.TTFT,
		WeightedContribution: weights.TTFT * candidate.Factors.TTFT,
		Source:               "advanced_scheduler.ttft_ewma",
	}
	if !candidate.Feedback.HasTTFT {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = diagnosticFloat(candidate.Feedback.TTFT) + " ms（" + strconv.FormatInt(candidate.Feedback.TTFTSamples, 10) + " 样本）"
	if !ranges.HasTTFTSample || ranges.MaxTTFT <= ranges.MinTTFT {
		metric.Normalization = "候选 TTFT 相同，使用中性值 0.5000"
		metric.Neutral = true
	} else {
		metric.Normalization = "1 - clamp((" + diagnosticFloat(candidate.Feedback.TTFT) + " - " + diagnosticFloat(ranges.MinTTFT) + ") / (" + diagnosticFloat(ranges.MaxTTFT) + " - " + diagnosticFloat(ranges.MinTTFT) + "))"
	}
	metric.ObservedAt = candidate.Feedback.LastTTFTAt
	return metric
}

func diagnosticResetMetric(candidate *CandidateScore, ranges ScoreRanges, weights policy.ScoreWeights, now time.Time) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "reset",
		NormalizedValue:      candidate.Factors.Reset,
		Weight:               weights.Reset,
		WeightedContribution: weights.Reset * candidate.Factors.Reset,
		Source:               "account.session_window_end",
	}
	if weights.Reset <= 0 {
		metric.RawValue = "权重为 0，未参与评分"
		metric.Normalization = "不适用，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	if candidate.Account.SessionWindowEnd == nil || !now.Before(*candidate.Account.SessionWindowEnd) {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	remaining := candidate.Account.SessionWindowEnd.Sub(now).Seconds()
	metric.Available = true
	metric.RawValue = diagnosticFloat(remaining) + " 秒"
	if !ranges.HasResetSample || ranges.MaxResetRemaining <= ranges.MinResetRemaining {
		metric.Normalization = "候选窗口重置时间相同，使用 1.0000"
	} else {
		metric.Normalization = "1 - clamp((" + diagnosticFloat(remaining) + " - " + diagnosticFloat(ranges.MinResetRemaining) + ") / (" + diagnosticFloat(ranges.MaxResetRemaining) + " - " + diagnosticFloat(ranges.MinResetRemaining) + "))"
	}
	return metric
}

func (s *DiagnosticService) diagnosticQuotaMetric(candidate *CandidateScore, weights policy.ScoreWeights, now time.Time) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "quota_headroom",
		NormalizedValue:      candidate.Factors.QuotaHeadroom,
		Weight:               weights.QuotaHeadroom,
		WeightedContribution: weights.QuotaHeadroom * candidate.Factors.QuotaHeadroom,
		Source:               "platform.quota_headroom",
	}
	if weights.QuotaHeadroom <= 0 {
		metric.RawValue = "权重为 0，未参与评分"
		metric.Normalization = "不适用，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	if candidate.Account.Platform != capability.PlatformOpenAI && candidate.Account.Platform != capability.PlatformGrok {
		metric.RawValue = "当前平台未提供配额余量信号"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	factor := s.quotaHeadroom(candidate.Account, now)
	metric.Available = factor != 0.5
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

func diagnosticEffectiveSettings(group *DiagnosticGroup, runtime policy.RuntimeSettings, effective policy.EffectiveSettings) []AdvancedSchedulerScoreDiagnosticSetting {
	overrides := policy.GroupAdvancedSchedulerOverrides{}
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
		{Key: "sticky_weighted_enabled", Value: strconv.FormatBool(effective.StickyWeightedEnabled), Source: settingSource(overrides.StickyWeightedEnabled != nil, true)},
		{Key: "subscription_priority_enabled", Value: strconv.FormatBool(effective.SubscriptionPriorityEnabled), Source: settingSource(overrides.SubscriptionPriorityEnabled != nil, true)},
		{Key: "ewma_error_rate_alpha", Value: diagnosticFloat(effective.Feedback.ErrorRateAlpha), Source: settingSource(overrides.EWMAErrorRateAlpha != nil, runtime.EwmaErrorRateAlphaSet)},
		{Key: "ewma_ttft_alpha", Value: diagnosticFloat(effective.Feedback.TtftAlpha), Source: settingSource(overrides.EWMATTFTAlpha != nil, runtime.EwmaTTFTAlphaSet)},
		{Key: "sticky_escape_enabled", Value: strconv.FormatBool(effective.StickyEscape.Enabled), Source: settingSource(overrides.StickyEscapeEnabled != nil, runtime.StickyEscapeEnabledSet)},
		{Key: "sticky_escape_ttft_ms", Value: diagnosticFloat(effective.StickyEscape.TtftMs), Source: settingSource(overrides.StickyEscapeTTFTMs != nil, runtime.StickyEscapeTTFTMsSet)},
		{Key: "sticky_escape_error_rate", Value: diagnosticFloat(effective.StickyEscape.ErrorRate), Source: settingSource(overrides.StickyEscapeErrorRate != nil, runtime.StickyEscapeErrorRateSet)},
		{Key: "lb_top_k", Value: strconv.Itoa(effective.TopK), Source: settingSource(overrides.LBTopK != nil, runtime.LbTopKOverride > 0)},
	}
	for _, item := range []struct {
		key           string
		value         float64
		groupOverride *float64
		runtimeKey    string
	}{
		{"weight_priority", effective.Weights.Priority, overrides.WeightPriority, "priority"},
		{"weight_load", effective.Weights.Load, overrides.WeightLoad, "load"},
		{"weight_queue", effective.Weights.Queue, overrides.WeightQueue, "queue"},
		{"weight_error_rate", effective.Weights.ErrorRate, overrides.WeightErrorRate, "error_rate"},
		{"weight_ttft", effective.Weights.TTFT, overrides.WeightTTFT, "ttft"},
		{"weight_reset", effective.Weights.Reset, overrides.WeightReset, "reset"},
		{"weight_quota_headroom", effective.Weights.QuotaHeadroom, overrides.WeightQuotaHeadroom, "quota_headroom"},
		{"weight_previous_response", effective.Weights.Previous, overrides.WeightPreviousResponse, "previous_response"},
		{"weight_session_sticky", effective.Weights.SessionSticky, overrides.WeightSessionSticky, "session_sticky"},
	} {
		_, hasRuntimeOverride := runtime.WeightOverrides[item.runtimeKey]
		settings = append(settings, AdvancedSchedulerScoreDiagnosticSetting{
			Key: item.key, Value: diagnosticFloat(item.value), Source: settingSource(item.groupOverride != nil, hasRuntimeOverride),
		})
	}
	return settings
}

func diagnosticPolicySignals(
	group *DiagnosticGroup,
	request AdvancedSchedulerScoreDiagnosticRequest,
	effective policy.EffectiveSettings,
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
	if group != nil && (group.Platform == capability.PlatformOpenAI || group.Platform == capability.PlatformGrok) && effective.SubscriptionPriorityEnabled {
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
