package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"golang.org/x/sync/singleflight"
)

const (
	openAIAccountScheduleLayerPreviousResponse = "previous_response_id"
	openAIAccountScheduleLayerSessionSticky    = "session_hash"
	openAIAccountScheduleLayerLoadBalance      = "load_balance"
)

const (
	advancedSchedulerSettingCacheTTL  = 5 * time.Second
	advancedSchedulerSettingDBTimeout = 2 * time.Second
	// 单次账号选择限制凭据复核与并发槽尝试次数，避免候选集过大时放大数据库压力。
	openAIAccountSelectionProbeLimit = 64
)

// quota headroom 只影响调度偏好，不应像自动暂停那样强制屏蔽账号。
// 缺少或陈旧快照时使用中性分，避免没有 header 数据的账号被错误降权。
const (
	openAIQuotaHeadroomNeutralFactor      = 0.5
	openAIQuotaHeadroomSecondaryLowRemain = 0.10
	openAIQuotaHeadroomSnapshotStaleAfter = 8 * time.Hour
)

type cachedAdvancedSchedulerSetting struct {
	stickyWeightedEnabled       bool
	subscriptionPriorityEnabled bool
	lbTopKOverride              int
	weightOverrides             map[string]float64
	expiresAt                   int64
}

type advancedSchedulerRuntimeSettings struct {
	stickyWeightedEnabled       bool
	subscriptionPriorityEnabled bool
	lbTopKOverride              int
	weightOverrides             map[string]float64
}

var advancedSchedulerSettingCache atomic.Value // *cachedAdvancedSchedulerSetting
var advancedSchedulerSettingSF singleflight.Group

type OpenAIAccountScheduleRequest struct {
	GroupID                 *int64
	Platform                string
	SessionHash             string
	StickyAccountID         int64
	StickyPreviousAccountID int64
	StickyWeighted          bool
	SubscriptionPriority    bool
	PreserveStickyBinding   bool
	PreviousResponseID      string
	PreviousResponseCanMove bool
	RequestedModel          string // 客户端请求模型 R，用于限制、错误和会话语义。
	RoutingModel            string // 账号层模型：普通请求为 C，Messages 为分组映射后的 D。
	RequiredTransport       OpenAIUpstreamTransport
	RequiredCapability      OpenAIEndpointCapability
	RequiredImageCapability OpenAIImagesCapability
	RequireCompact          bool
	ExcludedIDs             map[int64]struct{}
}

// routingModel 返回账号调度层使用的模型，并兼容未设置 RoutingModel 的旧调用方。
func (r OpenAIAccountScheduleRequest) routingModel() string {
	if model := strings.TrimSpace(r.RoutingModel); model != "" {
		return model
	}
	return r.RequestedModel
}

type OpenAIAccountScheduleDecision struct {
	Layer               string
	StickyPreviousHit   bool
	StickySessionHit    bool
	CandidateCount      int
	TopK                int
	LatencyMs           int64
	LoadSkew            float64
	SelectedAccountID   int64
	SelectedAccountType string
}

type OpenAIAccountSchedulerMetricsSnapshot struct {
	SelectTotal              int64
	StickyPreviousHitTotal   int64
	StickySessionHitTotal    int64
	LoadBalanceSelectTotal   int64
	AccountSwitchTotal       int64
	SchedulerLatencyMsTotal  int64
	SchedulerLatencyMsAvg    float64
	StickyHitRatio           float64
	AccountSwitchRate        float64
	LoadSkewAvg              float64
	RuntimeStatsAccountCount int
}

type OpenAIAccountScheduler interface {
	Select(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error)
	ReportResult(accountID int64, success bool, firstTokenMs *int)
	ReportSwitch()
	SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot
}

type openAIAccountSchedulerMetrics struct {
	selectTotal            atomic.Int64
	stickyPreviousHitTotal atomic.Int64
	stickySessionHitTotal  atomic.Int64
	loadBalanceSelectTotal atomic.Int64
	accountSwitchTotal     atomic.Int64
	latencyMsTotal         atomic.Int64
	loadSkewMilliTotal     atomic.Int64
}

type openAIAccountLoadPlan struct {
	allCandidates             []openAIAccountCandidateScore
	candidates                []openAIAccountCandidateScore
	staleSnapshotCompactRetry []openAIAccountCandidateScore
	selectionOrder            []openAIAccountCandidateScore
	candidateCount            int
	topK                      int
	loadSkew                  float64
}

type openAIAccountLoadSelectionAttempt struct {
	result              *AccountSelectionResult
	selectionOrder      []openAIAccountCandidateScore
	candidateCount      int
	topK                int
	loadSkew            float64
	compactBlocked      bool
	noCompactCandidates bool
	err                 error
}

func (m *openAIAccountSchedulerMetrics) recordSelect(decision OpenAIAccountScheduleDecision) {
	if m == nil {
		return
	}
	m.selectTotal.Add(1)
	m.latencyMsTotal.Add(decision.LatencyMs)
	m.loadSkewMilliTotal.Add(int64(math.Round(decision.LoadSkew * 1000)))
	if decision.StickyPreviousHit {
		m.stickyPreviousHitTotal.Add(1)
	}
	if decision.StickySessionHit {
		m.stickySessionHitTotal.Add(1)
	}
	if decision.Layer == openAIAccountScheduleLayerLoadBalance {
		m.loadBalanceSelectTotal.Add(1)
	}
}

func (m *openAIAccountSchedulerMetrics) recordSwitch() {
	if m == nil {
		return
	}
	m.accountSwitchTotal.Add(1)
}

// 兼容内部调用点的别名：OpenAI 是通用高级调度器的能力适配者之一。
type openAIAccountRuntimeStats = advancedAccountRuntimeStats

func newOpenAIAccountRuntimeStats() *advancedAccountRuntimeStats {
	return newAdvancedAccountRuntimeStats()
}

type defaultOpenAIAccountScheduler struct {
	service                *OpenAIGatewayService
	metrics                openAIAccountSchedulerMetrics
	stats                  *openAIAccountRuntimeStats
	grokFreeQuotaGateCache sync.Map // key: int64(accountID), value: grokFreeQuotaGateCacheEntry
}

type openAISelectionProbeBudget struct {
	acquires  int
	rechecks  int
	attempted map[int64]struct{}
	limited   bool
}

func newOpenAISelectionProbeBudget() *openAISelectionProbeBudget {
	return &openAISelectionProbeBudget{attempted: make(map[int64]struct{})}
}

func (b *openAISelectionProbeBudget) enableLimit() {
	if b != nil {
		b.limited = true
	}
}

func (b *openAISelectionProbeBudget) recordAcquire(accountID int64) bool {
	if b == nil {
		return false
	}
	if !b.limited {
		return true
	}
	if b.acquires >= openAIAccountSelectionProbeLimit {
		return false
	}
	if b.attempted == nil {
		b.attempted = make(map[int64]struct{})
	}
	b.acquires++
	b.attempted[accountID] = struct{}{}
	return true
}

func (b *openAISelectionProbeBudget) recordRecheck() bool {
	if b == nil {
		return false
	}
	if !b.limited {
		return true
	}
	if b.rechecks >= openAIAccountSelectionProbeLimit {
		return false
	}
	b.rechecks++
	return true
}

func (b *openAISelectionProbeBudget) acquireExhausted() bool {
	return b != nil && b.limited && b.acquires >= openAIAccountSelectionProbeLimit
}

func (b *openAISelectionProbeBudget) wasAttempted(accountID int64) bool {
	if b == nil {
		return false
	}
	_, ok := b.attempted[accountID]
	return ok
}

type advancedStickyEscapeConfig struct {
	enabled   bool
	ttftMs    float64
	errorRate float64
}

func newDefaultOpenAIAccountScheduler(service *OpenAIGatewayService, stats *openAIAccountRuntimeStats) OpenAIAccountScheduler {
	if stats == nil {
		stats = newOpenAIAccountRuntimeStats()
	}
	return &defaultOpenAIAccountScheduler{
		service: service,
		stats:   stats,
	}
}

func (s *defaultOpenAIAccountScheduler) Select(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	decision := OpenAIAccountScheduleDecision{}
	start := time.Now()
	defer func() {
		decision.LatencyMs = time.Since(start).Milliseconds()
		s.metrics.recordSelect(decision)
	}()

	previousResponseID := strings.TrimSpace(req.PreviousResponseID)
	if previousResponseID != "" && normalizeOpenAICompatiblePlatform(req.Platform) == PlatformOpenAI &&
		(!req.StickyWeighted || !req.PreviousResponseCanMove) {
		selection, err := s.service.selectAccountByPreviousResponseIDForCapability(
			ctx,
			req.GroupID,
			previousResponseID,
			req.routingModel(),
			req.ExcludedIDs,
			req.RequiredCapability,
			req.RequireCompact,
		)
		if err != nil {
			return nil, decision, err
		}
		if selection != nil && selection.Account != nil {
			if !s.isAccountTransportCompatible(selection.Account, req.RequiredTransport) {
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				selection = nil
			}
		}
		if selection != nil && selection.Account != nil {
			decision.Layer = openAIAccountScheduleLayerPreviousResponse
			decision.StickyPreviousHit = true
			decision.SelectedAccountID = selection.Account.ID
			decision.SelectedAccountType = selection.Account.Type
			if req.SessionHash != "" {
				_ = s.service.BindStickySession(ctx, req.GroupID, req.SessionHash, selection.Account.ID)
			}
			return selection, decision, nil
		}
	}

	if !req.StickyWeighted {
		selection, escapedSticky, err := s.selectBySessionHash(ctx, req)
		if err != nil {
			return nil, decision, err
		}
		if selection != nil && selection.Account != nil {
			decision.Layer = openAIAccountScheduleLayerSessionSticky
			decision.StickySessionHit = true
			decision.SelectedAccountID = selection.Account.ID
			decision.SelectedAccountType = selection.Account.Type
			return selection, decision, nil
		}
		if escapedSticky {
			req.PreserveStickyBinding = true
		}
	}

	selection, candidateCount, topK, loadSkew, err := s.selectByLoadBalance(ctx, req)
	decision.Layer = openAIAccountScheduleLayerLoadBalance
	decision.CandidateCount = candidateCount
	decision.TopK = topK
	decision.LoadSkew = loadSkew
	if err != nil {
		return nil, decision, err
	}
	if selection != nil && selection.Account != nil {
		decision.SelectedAccountID = selection.Account.ID
		decision.SelectedAccountType = selection.Account.Type
		if req.StickyWeighted {
			if req.StickyPreviousAccountID > 0 && selection.Account.ID == req.StickyPreviousAccountID {
				decision.StickyPreviousHit = true
			}
			if req.StickyAccountID > 0 && selection.Account.ID == req.StickyAccountID {
				decision.StickySessionHit = true
			}
		}
	}
	return selection, decision, nil
}

func (s *defaultOpenAIAccountScheduler) selectBySessionHash(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) (*AccountSelectionResult, bool, error) {
	sessionHash := strings.TrimSpace(req.SessionHash)
	if sessionHash == "" || s == nil || s.service == nil || s.service.cache == nil {
		return nil, false, nil
	}

	accountID := req.StickyAccountID
	if accountID <= 0 {
		var err error
		accountID, err = s.service.getStickySessionAccountID(ctx, req.GroupID, sessionHash)
		if err != nil || accountID <= 0 {
			return nil, false, nil
		}
	}
	if accountID <= 0 {
		return nil, false, nil
	}
	if req.ExcludedIDs != nil {
		if _, excluded := req.ExcludedIDs[accountID]; excluded {
			return nil, false, nil
		}
	}

	account, err := s.service.getSchedulableAccount(ctx, accountID)
	if err != nil || account == nil {
		_ = s.service.deleteStickySessionAccountID(ctx, req.GroupID, sessionHash)
		return nil, false, nil
	}
	if shouldClearStickySession(account, req.routingModel()) || account.Platform != normalizeOpenAICompatiblePlatform(req.Platform) || !account.IsOpenAICompatible() || !account.IsSchedulable() {
		_ = s.service.deleteStickySessionAccountID(ctx, req.GroupID, sessionHash)
		return nil, false, nil
	}
	if !s.isAccountRequestCompatible(ctx, account, req) {
		return nil, false, nil
	}
	if !s.isAccountTransportCompatible(account, req.RequiredTransport) {
		_ = s.service.deleteStickySessionAccountID(ctx, req.GroupID, sessionHash)
		return nil, false, nil
	}
	account = s.service.recheckSelectedOpenAIAccountFromDB(ctx, account, req.GroupID, req.Platform, req.routingModel(), req.RequireCompact, req.RequiredCapability)
	if account == nil || !s.service.openAIAccountMatchesSchedulingGroup(account, req.GroupID) || !s.isAccountTransportCompatible(account, req.RequiredTransport) {
		_ = s.service.deleteStickySessionAccountID(ctx, req.GroupID, sessionHash)
		return nil, false, nil
	}
	// 免费层软性门禁：粘性会话不得固定到已超额的免费 OAuth 账号。
	// 管理端额度查询与导入探测不经过此路径。
	if account != nil && len(s.filterGrokFreeQuotaAccounts(ctx, []Account{*account})) == 0 {
		_ = s.service.deleteStickySessionAccountID(ctx, req.GroupID, sessionHash)
		return nil, false, nil
	}
	// 团队与模型冷却：粘性会话不得固定到同团队中仍处于 429 窗口的关联账号。
	now := time.Now()
	upstreamModel := canonicalOpenAIAccountSchedulingModel(account, req.RequestedModel)
	if account != nil && isGrokTeamModelRateLimited(account, upstreamModel, now) {
		_ = s.service.deleteStickySessionAccountID(ctx, req.GroupID, sessionHash)
		return nil, false, nil
	}
	if account != nil && isGrokModelQuotaBlocked(account.ID, upstreamModel, now) {
		_ = s.service.deleteStickySessionAccountID(ctx, req.GroupID, sessionHash)
		return nil, false, nil
	}
	escapeCfg := s.service.openAIStickyEscapeConfig()
	if reason, errorRate, ttft, shouldEscape := s.shouldEscapeStickyAccount(accountID, escapeCfg); shouldEscape {
		slog.Info("sticky_escape_triggered",
			"account_id", accountID,
			"reason", reason,
			"error_rate", errorRate,
			"ttft", ttft,
		)
		return nil, true, nil
	}
	result, acquireErr := s.service.tryAcquireAccountSlot(ctx, accountID, account.Concurrency)
	if acquireErr == nil && result != nil && result.Acquired {
		_ = s.service.refreshStickySessionTTL(ctx, req.GroupID, sessionHash, s.service.openAIWSSessionStickyTTL())
		return &AccountSelectionResult{
			Account:     account,
			Acquired:    true,
			ReleaseFunc: result.ReleaseFunc,
		}, false, nil
	}

	cfg := s.service.schedulingConfig()
	// WaitPlan.MaxConcurrency 使用 Concurrency（非 EffectiveLoadFactor），因为 WaitPlan 控制的是 Redis 实际并发槽位等待。
	if s.service.concurrencyService != nil {
		if escapeCfg.enabled && acquireErr == nil && result != nil && !result.Acquired {
			errorRate, ttft, _ := s.stats.snapshot(accountID)
			slog.Info("sticky_escape_triggered",
				"account_id", accountID,
				"reason", "concurrency_full",
				"error_rate", errorRate,
				"ttft", ttft,
			)
			return nil, true, nil
		}
		return &AccountSelectionResult{
			Account: account,
			WaitPlan: &AccountWaitPlan{
				AccountID:      accountID,
				MaxConcurrency: account.Concurrency,
				Timeout:        cfg.StickySessionWaitTimeout,
				MaxWaiting:     cfg.StickySessionMaxWaiting,
			},
		}, false, nil
	}
	return nil, false, nil
}

// openAIStickyAccountMatchesGroup 校验粘性会话账号是否仍属于当前请求分组。
func openAIStickyAccountMatchesGroup(account *Account, groupID *int64) bool {
	if account == nil {
		return false
	}
	if groupID == nil {
		return len(account.AccountGroups) == 0 && len(account.GroupIDs) == 0
	}
	for _, accountGroupID := range account.GroupIDs {
		if accountGroupID == *groupID {
			return true
		}
	}
	for _, accountGroup := range account.AccountGroups {
		if accountGroup.GroupID == *groupID {
			return true
		}
	}
	return false
}

func shouldEscapeAdvancedStickyAccount(stats *advancedAccountRuntimeStats, accountID int64, cfg advancedStickyEscapeConfig) (reason string, errorRate float64, ttft float64, shouldEscape bool) {
	if !cfg.enabled || stats == nil || accountID <= 0 {
		return "", 0, 0, false
	}
	errorRate, ttft, hasTTFT := stats.snapshot(accountID)
	if hasTTFT && ttft > cfg.ttftMs {
		return "ttft", errorRate, ttft, true
	}
	if errorRate > cfg.errorRate {
		return "error_rate", errorRate, ttft, true
	}
	return "", errorRate, ttft, false
}

func (s *defaultOpenAIAccountScheduler) shouldEscapeStickyAccount(accountID int64, cfg advancedStickyEscapeConfig) (reason string, errorRate float64, ttft float64, shouldEscape bool) {
	if s == nil {
		return "", 0, 0, false
	}
	return shouldEscapeAdvancedStickyAccount(s.stats, accountID, cfg)
}

// 以下别名保留 OpenAI 适配层的局部语义；底层实现由通用高级调度核心共享。
type openAIAccountCandidateScore = advancedSchedulerCandidateScore
type openAIAccountCandidateHeap = advancedSchedulerCandidateHeap

func isOpenAIAccountCandidateBetter(left, right openAIAccountCandidateScore) bool {
	return isAdvancedSchedulerCandidateBetter(left, right)
}

func selectTopKOpenAICandidates(candidates []openAIAccountCandidateScore, topK int) []openAIAccountCandidateScore {
	return selectTopKAdvancedSchedulerCandidates(candidates, topK)
}

func buildOpenAIWeightedSelectionOrder(candidates []openAIAccountCandidateScore, req OpenAIAccountScheduleRequest) []openAIAccountCandidateScore {
	return buildAdvancedWeightedSelectionOrder(candidates, advancedSchedulerSelectionInput{
		GroupID:                 req.GroupID,
		SessionHash:             req.SessionHash,
		PreviousResponseID:      req.PreviousResponseID,
		RequestedModel:          req.RequestedModel,
		StickyAccountID:         req.StickyAccountID,
		StickyPreviousAccountID: req.StickyPreviousAccountID,
		StickyWeighted:          req.StickyWeighted,
	})
}

// 兼容旧测试和基准的命名，生产代码使用通用高级调度器随机源。
type openAISelectionRNG = advancedSchedulerRNG

func newOpenAISelectionRNG(seed uint64) openAISelectionRNG {
	return newAdvancedSchedulerRNG(seed)
}

func deriveOpenAISelectionSeed(req OpenAIAccountScheduleRequest) uint64 {
	return deriveAdvancedSchedulerSelectionSeed(advancedSchedulerSelectionInput{
		GroupID:                 req.GroupID,
		SessionHash:             req.SessionHash,
		PreviousResponseID:      req.PreviousResponseID,
		RequestedModel:          req.RequestedModel,
		StickyAccountID:         req.StickyAccountID,
		StickyPreviousAccountID: req.StickyPreviousAccountID,
		StickyWeighted:          req.StickyWeighted,
	})
}

func (s *defaultOpenAIAccountScheduler) buildOpenAIAccountLoadPlan(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	filtered []*Account,
	loadMap map[int64]*AccountLoadInfo,
) openAIAccountLoadPlan {
	allCandidates := make([]openAIAccountCandidateScore, 0, len(filtered))
	for _, account := range filtered {
		loadInfo, loadKnown := loadMap[account.ID]
		if !loadKnown || loadInfo == nil {
			loadInfo = &AccountLoadInfo{AccountID: account.ID}
			loadKnown = false
		}
		errorRate, ttft, hasTTFT := 0.0, 0.0, false
		if s.stats != nil {
			errorRate, ttft, hasTTFT = s.stats.snapshot(account.ID)
		}
		allCandidates = append(allCandidates, openAIAccountCandidateScore{
			account:   account,
			loadInfo:  loadInfo,
			loadKnown: loadKnown,
			errorRate: errorRate,
			ttft:      ttft,
			hasTTFT:   hasTTFT,
		})
	}

	candidates := allCandidates
	staleSnapshotCompactRetry := make([]openAIAccountCandidateScore, 0, len(allCandidates))
	if req.RequireCompact {
		candidates = make([]openAIAccountCandidateScore, 0, len(allCandidates))
		for _, candidate := range allCandidates {
			if openAICompactSupportTier(candidate.account) == 0 {
				staleSnapshotCompactRetry = append(staleSnapshotCompactRetry, candidate)
				continue
			}
			candidates = append(candidates, candidate)
		}
	}

	plan := openAIAccountLoadPlan{
		allCandidates:             allCandidates,
		candidates:                candidates,
		staleSnapshotCompactRetry: staleSnapshotCompactRetry,
		candidateCount:            len(candidates),
	}
	if len(candidates) == 0 {
		plan.selectionOrder = s.buildOpenAISelectionOrder(req, plan)
		return plan
	}

	accounts := make([]*Account, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.account != nil {
			accounts = append(accounts, candidate.account)
		}
	}
	previousStickyAccountID := req.StickyPreviousAccountID
	if !req.PreviousResponseCanMove {
		previousStickyAccountID = 0
	}
	effectiveSettings := s.service.advancedSchedulerEffectiveSettingsForRequest(ctx, req.GroupID)
	plan.candidates, plan.loadSkew = scoreAdvancedSchedulerCandidates(
		accounts,
		loadMap,
		s.stats,
		effectiveSettings.weights,
		advancedSchedulerSelectionInput{
			GroupID:                 req.GroupID,
			SessionHash:             req.SessionHash,
			PreviousResponseID:      req.PreviousResponseID,
			RequestedModel:          req.RequestedModel,
			StickyAccountID:         req.StickyAccountID,
			StickyPreviousAccountID: previousStickyAccountID,
			StickyWeighted:          req.StickyWeighted,
			QuotaHeadroomFactor:     openAIQuotaHeadroomFactor,
		},
		time.Now(),
	)

	plan.topK = effectiveSettings.topK
	if plan.topK > len(plan.candidates) {
		plan.topK = len(plan.candidates)
	}
	if plan.topK <= 0 {
		plan.topK = 1
	}

	plan.selectionOrder = s.buildOpenAISelectionOrder(req, plan)
	return plan
}

func (s *defaultOpenAIAccountScheduler) buildOpenAISelectionOrder(
	req OpenAIAccountScheduleRequest,
	plan openAIAccountLoadPlan,
) []openAIAccountCandidateScore {
	buildSelectionOrder := func(pool []openAIAccountCandidateScore) []openAIAccountCandidateScore {
		if len(pool) == 0 || plan.topK <= 0 {
			return nil
		}
		groupTopK := plan.topK
		if groupTopK > len(pool) {
			groupTopK = len(pool)
		}
		ranked := selectTopKOpenAICandidates(pool, groupTopK)
		return buildOpenAIWeightedSelectionOrder(ranked, req)
	}

	if req.RequireCompact {
		supported := make([]openAIAccountCandidateScore, 0, len(plan.candidates))
		unknown := make([]openAIAccountCandidateScore, 0, len(plan.candidates))
		for _, candidate := range plan.candidates {
			switch openAICompactSupportTier(candidate.account) {
			case 2:
				supported = append(supported, candidate)
			case 1:
				unknown = append(unknown, candidate)
			}
		}
		selectionOrder := make([]openAIAccountCandidateScore, 0, len(plan.allCandidates))
		selectionOrder = append(selectionOrder, buildSelectionOrder(supported)...)
		selectionOrder = append(selectionOrder, buildSelectionOrder(unknown)...)
		if len(plan.staleSnapshotCompactRetry) > 0 && s.service.schedulerSnapshot != nil {
			selectionOrder = append(selectionOrder, sortOpenAICompactRetryCandidates(plan.staleSnapshotCompactRetry)...)
		}
		return selectionOrder
	}

	return buildSelectionOrder(plan.candidates)
}

func sortOpenAICompactRetryCandidates(pool []openAIAccountCandidateScore) []openAIAccountCandidateScore {
	if len(pool) == 0 {
		return nil
	}
	ordered := append([]openAIAccountCandidateScore(nil), pool...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.account.Priority != b.account.Priority {
			return a.account.Priority < b.account.Priority
		}
		if a.loadInfo.LoadRate != b.loadInfo.LoadRate {
			return a.loadInfo.LoadRate < b.loadInfo.LoadRate
		}
		if a.loadInfo.WaitingCount != b.loadInfo.WaitingCount {
			return a.loadInfo.WaitingCount < b.loadInfo.WaitingCount
		}
		switch {
		case a.account.LastUsedAt == nil && b.account.LastUsedAt != nil:
			return true
		case a.account.LastUsedAt != nil && b.account.LastUsedAt == nil:
			return false
		case a.account.LastUsedAt == nil && b.account.LastUsedAt == nil:
			return false
		default:
			return a.account.LastUsedAt.Before(*b.account.LastUsedAt)
		}
	})
	return ordered
}

func (s *defaultOpenAIAccountScheduler) tryAcquireOpenAISelectionOrder(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	selectionOrder []openAIAccountCandidateScore,
) (*AccountSelectionResult, bool, error) {
	budget := newOpenAISelectionProbeBudget()
	budget.enableLimit()
	return s.tryAcquireOpenAISelectionOrderWithBudget(ctx, req, selectionOrder, budget)
}

func (s *defaultOpenAIAccountScheduler) tryAcquireOpenAISelectionOrderWithBudget(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	selectionOrder []openAIAccountCandidateScore,
	budget *openAISelectionProbeBudget,
) (*AccountSelectionResult, bool, error) {
	compactBlocked := false
	release := func(result *AcquireResult) {
		if result != nil && result.ReleaseFunc != nil {
			result.ReleaseFunc()
		}
	}
	for i := 0; i < len(selectionOrder); i++ {
		candidate := selectionOrder[i]
		if candidate.account == nil {
			continue
		}
		if candidate.loadKnown && candidate.account.Concurrency > 0 &&
			candidate.loadInfo.CurrentConcurrency >= candidate.account.Concurrency {
			continue
		}

		result, attempted, acquireErr := s.tryAcquireOpenAIAccountSlot(ctx, candidate.account.ID, candidate.account.Concurrency, budget)
		if !attempted {
			break
		}
		if acquireErr != nil {
			return nil, compactBlocked, acquireErr
		}
		if result == nil || !result.Acquired {
			continue
		}

		fresh := s.service.resolveFreshSchedulableOpenAIAccount(ctx, candidate.account, req.Platform, req.routingModel(), false, req.RequiredCapability)
		if fresh == nil || !s.isAccountTransportCompatible(fresh, req.RequiredTransport) || !s.isAccountRequestCompatible(ctx, fresh, req) {
			release(result)
			continue
		}
		if !s.consumeOpenAISelectionDBRecheck(budget) {
			release(result)
			break
		}
		fresh = s.service.recheckSelectedOpenAIAccountFromDB(ctx, fresh, req.GroupID, req.Platform, req.routingModel(), false, req.RequiredCapability)
		if fresh == nil || !s.isAccountTransportCompatible(fresh, req.RequiredTransport) || !s.isAccountRequestCompatible(ctx, fresh, req) {
			release(result)
			continue
		}
		if req.RequireCompact && openAICompactSupportTier(fresh) == 0 {
			compactBlocked = true
			release(result)
			continue
		}

		if fresh.Concurrency != candidate.account.Concurrency {
			release(result)
			result, attempted, acquireErr = s.tryAcquireOpenAIAccountSlot(ctx, fresh.ID, fresh.Concurrency, budget)
			if !attempted {
				continue
			}
			if acquireErr != nil {
				return nil, compactBlocked, acquireErr
			}
			if result == nil || !result.Acquired {
				continue
			}
		}
		if req.SessionHash != "" && !req.PreserveStickyBinding {
			_ = s.service.BindStickySession(ctx, req.GroupID, req.SessionHash, fresh.ID)
		}
		return &AccountSelectionResult{
			Account:     fresh,
			Acquired:    true,
			ReleaseFunc: result.ReleaseFunc,
		}, compactBlocked, nil
	}
	return nil, compactBlocked, nil
}

func (s *defaultOpenAIAccountScheduler) tryAcquireOpenAIAccountSlot(
	ctx context.Context,
	accountID int64,
	maxConcurrency int,
	budget *openAISelectionProbeBudget,
) (*AcquireResult, bool, error) {
	if s.service.concurrencyService != nil && maxConcurrency > 0 && !budget.recordAcquire(accountID) {
		return nil, false, nil
	}
	result, err := s.service.tryAcquireAccountSlot(ctx, accountID, maxConcurrency)
	return result, true, err
}

func (s *defaultOpenAIAccountScheduler) consumeOpenAISelectionDBRecheck(budget *openAISelectionProbeBudget) bool {
	if s.service.schedulerSnapshot == nil || s.service.accountRepo == nil {
		return true
	}
	return budget.recordRecheck()
}

// openAISelectionFilterStats 统计负载均衡初筛阶段排除候选账号的原因。
// reasons 延迟分配，正常选中账号的热路径不会产生额外 map 分配。
type openAISelectionFilterStats struct {
	pool    int
	reasons map[string]int
}

func (s *openAISelectionFilterStats) exclude(reason string) {
	if s.reasons == nil {
		s.reasons = make(map[string]int, 4)
	}
	s.reasons[reason]++
}

// summary 生成顺序稳定的排除统计，便于日志聚合和问题定位。
func (s openAISelectionFilterStats) summary(extra string) string {
	var b strings.Builder
	_, _ = b.WriteString("pool=")
	_, _ = b.WriteString(strconv.Itoa(s.pool))
	if len(s.reasons) > 0 {
		reasons := make([]string, 0, len(s.reasons))
		for reason := range s.reasons {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		_, _ = b.WriteString(", filtered:")
		for _, reason := range reasons {
			_, _ = b.WriteString(" ")
			_, _ = b.WriteString(reason)
			_, _ = b.WriteString("=")
			_, _ = b.WriteString(strconv.Itoa(s.reasons[reason]))
		}
	}
	if extra != "" {
		_, _ = b.WriteString(", ")
		_, _ = b.WriteString(extra)
	}
	return b.String()
}

func (s *defaultOpenAIAccountScheduler) selectByLoadBalance(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) (*AccountSelectionResult, int, int, float64, error) {
	budget := newOpenAISelectionProbeBudget()
	accounts, err := s.service.listSchedulableAccounts(ctx, req.GroupID, req.Platform)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	if len(accounts) == 0 {
		return nil, 0, 0, 0, noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, req.RequestedModel, req.routingModel(), false, openAISelectionFilterStats{}.summary(""), accounts)
	}
	// 本地免费层软性门禁仅应用于 Grok 调度路径，不影响管理端探测。
	accounts = s.filterGrokFreeQuotaAccounts(ctx, accounts)
	if len(accounts) == 0 {
		return nil, 0, 0, 0, noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, req.RequestedModel, req.routingModel(), false, openAISelectionFilterStats{}.summary("grok_free_quota_soft_gate"))
	}
	// 团队与模型限流冷却：发生 429 的团队关联账号跳过热点模型。
	if req.Platform == PlatformGrok {
		now := time.Now()
		filtered := filterGrokTeamModelRateLimitedAccounts(accounts, req.RequestedModel, now)
		if len(filtered) == 0 && len(accounts) > 0 {
			return nil, 0, 0, 0, noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, req.RequestedModel, req.routingModel(), false, openAISelectionFilterStats{}.summary("grok_team_model_rate_limit"))
		}
		if filtered != nil {
			accounts = filtered
		}
		// 按账号和模型执行免费额度软性阻断，其他模型仍可参与调度。
		modelFiltered := filterGrokModelQuotaBlockedAccounts(accounts, req.RequestedModel, now)
		if len(modelFiltered) == 0 && len(accounts) > 0 {
			return nil, 0, 0, 0, noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, req.RequestedModel, req.routingModel(), false, openAISelectionFilterStats{}.summary("grok_model_quota_block"))
		}
		accounts = modelFiltered
	}

	// require_privacy_set: 获取分组信息
	var schedGroup *Group
	if req.GroupID != nil && s.service.schedulerSnapshot != nil {
		schedGroup, _ = s.service.schedulerSnapshot.GetGroupByID(ctx, *req.GroupID)
	}

	filterStats := openAISelectionFilterStats{pool: len(accounts)}
	filtered := make([]*Account, 0, len(accounts))
	loadReq := make([]AccountWithConcurrency, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if req.ExcludedIDs != nil {
			if _, excluded := req.ExcludedIDs[account.ID]; excluded {
				filterStats.exclude("excluded")
				continue
			}
		}
		if !account.IsSchedulable() {
			filterStats.exclude("not_schedulable")
			continue
		}
		if account.Platform != normalizeOpenAICompatiblePlatform(req.Platform) || !account.IsOpenAICompatible() {
			filterStats.exclude("platform_mismatch")
			continue
		}
		if s.service.isOpenAIAccountRequestRuntimeBlocked(account, req.routingModel()) {
			filterStats.exclude("runtime_blocked")
			continue
		}
		// require_privacy_set: 跳过 privacy 未设置的账号并标记异常
		if schedGroup != nil && schedGroup.RequirePrivacySet && !account.IsPrivacySet() {
			s.service.BlockAccountScheduling(account, time.Time{}, "privacy_not_set")
			_ = s.service.accountRepo.SetError(ctx, account.ID,
				fmt.Sprintf("Privacy not set, required by group [%s]", schedGroup.Name))
			filterStats.exclude("privacy_not_set")
			continue
		}
		if compatible, reason := s.isAccountRequestCompatibleReason(ctx, account, req); !compatible {
			filterStats.exclude(reason)
			continue
		}
		if !s.isAccountTransportCompatible(account, req.RequiredTransport) {
			filterStats.exclude("transport_incompatible")
			continue
		}
		filtered = append(filtered, account)
		loadReq = append(loadReq, AccountWithConcurrency{
			ID:             account.ID,
			MaxConcurrency: account.EffectiveLoadFactor(),
		})
	}
	if len(filtered) == 0 {
		return nil, 0, 0, 0, noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, req.RequestedModel, req.routingModel(), false, filterStats.summary(""), accounts)
	}

	loadMap := map[int64]*AccountLoadInfo{}
	if s.service.concurrencyService != nil {
		if batchLoad, loadErr := s.service.concurrencyService.GetAccountsLoadBatch(ctx, loadReq); loadErr == nil {
			loadMap = batchLoad
		}
	}

	if req.SubscriptionPriority {
		subscriptionAccounts, regularAccounts := partitionOpenAIChatGPTSubscriptionAccounts(filtered)
		if len(subscriptionAccounts) > 0 {
			attempt := s.trySelectByLoadBalancePool(ctx, req, subscriptionAccounts, loadMap, budget)
			if attempt.err != nil && (!attempt.noCompactCandidates || len(regularAccounts) <= 0) {
				return nil, attempt.candidateCount, attempt.topK, attempt.loadSkew, attempt.err
			}
			if attempt.result != nil {
				return attempt.result, attempt.candidateCount, attempt.topK, attempt.loadSkew, nil
			}
			if len(regularAccounts) > 0 {
				regularAttempt := s.trySelectByLoadBalancePool(ctx, req, regularAccounts, loadMap, budget)
				if regularAttempt.err != nil && !regularAttempt.noCompactCandidates {
					return nil, regularAttempt.candidateCount, regularAttempt.topK, regularAttempt.loadSkew, regularAttempt.err
				}
				if regularAttempt.result != nil {
					return regularAttempt.result, regularAttempt.candidateCount, regularAttempt.topK, regularAttempt.loadSkew, nil
				}
				var result *AccountSelectionResult
				candidateCount, topK, loadSkew := regularAttempt.candidateCount, regularAttempt.topK, regularAttempt.loadSkew
				fallbackErr := regularAttempt.err
				if regularAttempt.err == nil {
					result, candidateCount, topK, loadSkew, fallbackErr = s.finishLoadBalanceSelectionFallback(ctx, req, regularAttempt, budget, filterStats)
					if fallbackErr == nil && result != nil {
						return result, candidateCount, topK, loadSkew, nil
					}
				}
				// 常规池既无法获取也无法排队（含仅剩不支持 compact 的候选）时，
				// 回退到订阅池的等待计划：busy-but-waitable 的订阅账号不应因常规池存在
				// 而被丢弃，否则开启订阅优先反而让本可排队成功的请求硬失败。
				subResult, subCandidateCount, subTopK, subLoadSkew, subErr := s.finishLoadBalanceSelectionFallback(ctx, req, attempt, budget, filterStats)
				if subErr == nil && subResult != nil {
					return subResult, subCandidateCount, subTopK, subLoadSkew, nil
				}
				return result, candidateCount, topK, loadSkew, fallbackErr
			}
			return s.finishLoadBalanceSelectionFallback(ctx, req, attempt, budget, filterStats)
		}
	}

	attempt := s.trySelectByLoadBalancePool(ctx, req, filtered, loadMap, budget)
	if attempt.err != nil {
		return nil, attempt.candidateCount, attempt.topK, attempt.loadSkew, attempt.err
	}
	if attempt.result != nil {
		return attempt.result, attempt.candidateCount, attempt.topK, attempt.loadSkew, nil
	}
	return s.finishLoadBalanceSelectionFallback(ctx, req, attempt, budget, filterStats)
}

func partitionOpenAIChatGPTSubscriptionAccounts(accounts []*Account) ([]*Account, []*Account) {
	subscriptionAccounts := make([]*Account, 0, len(accounts))
	regularAccounts := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if account != nil && account.IsOpenAIChatGPTSubscription() {
			subscriptionAccounts = append(subscriptionAccounts, account)
			continue
		}
		regularAccounts = append(regularAccounts, account)
	}
	return subscriptionAccounts, regularAccounts
}

func (s *defaultOpenAIAccountScheduler) trySelectByLoadBalancePool(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	filtered []*Account,
	loadMap map[int64]*AccountLoadInfo,
	budget *openAISelectionProbeBudget,
) openAIAccountLoadSelectionAttempt {
	plan := s.buildOpenAIAccountLoadPlan(ctx, req, filtered, loadMap)
	attempt := openAIAccountLoadSelectionAttempt{
		selectionOrder: plan.selectionOrder,
		candidateCount: plan.candidateCount,
		topK:           plan.topK,
		loadSkew:       plan.loadSkew,
	}
	if req.RequireCompact && len(plan.candidates) == 0 && len(plan.staleSnapshotCompactRetry) == 0 {
		attempt.noCompactCandidates = true
		attempt.err = ErrNoAvailableCompactAccounts
		return attempt
	}
	if req.RequireCompact && len(attempt.selectionOrder) == 0 && s.service.schedulerSnapshot == nil {
		attempt.noCompactCandidates = true
		attempt.err = ErrNoAvailableCompactAccounts
		return attempt
	}
	if len(attempt.selectionOrder) == 0 {
		attempt.compactBlocked = req.RequireCompact && len(plan.allCandidates) > 0
		return attempt
	}

	result, compactBlocked, acquireErr := s.tryAcquireOpenAISelectionOrderWithBudget(ctx, req, attempt.selectionOrder, budget)
	attempt.compactBlocked = compactBlocked
	if acquireErr != nil {
		attempt.err = acquireErr
		return attempt
	}
	if result != nil {
		attempt.result = result
		return attempt
	}

	if s.service.concurrencyService != nil && !budget.acquireExhausted() {
		loadReq := buildOpenAIAccountLoadRequest(filtered)
		if freshLoadMap, loadErr := s.service.concurrencyService.GetAccountsLoadBatchFresh(ctx, loadReq); loadErr == nil {
			freshPlan := s.buildOpenAIAccountLoadPlan(ctx, req, filtered, freshLoadMap)
			if len(freshPlan.selectionOrder) > 0 {
				freshResult, freshCompactBlocked, freshAcquireErr := s.tryAcquireOpenAISelectionOrderWithBudget(ctx, req, freshPlan.selectionOrder, budget)
				if freshAcquireErr != nil {
					attempt.err = freshAcquireErr
					return attempt
				}
				if freshResult != nil {
					attempt.result = freshResult
					attempt.selectionOrder = freshPlan.selectionOrder
					attempt.candidateCount = freshPlan.candidateCount
					attempt.topK = freshPlan.topK
					attempt.loadSkew = freshPlan.loadSkew
					return attempt
				}
				attempt.compactBlocked = attempt.compactBlocked || freshCompactBlocked
				attempt.selectionOrder = freshPlan.selectionOrder
				attempt.candidateCount = freshPlan.candidateCount
				attempt.topK = freshPlan.topK
				attempt.loadSkew = freshPlan.loadSkew
			}
		}
	}

	return attempt
}

func buildOpenAIAccountLoadRequest(accounts []*Account) []AccountWithConcurrency {
	loadReq := make([]AccountWithConcurrency, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		loadReq = append(loadReq, AccountWithConcurrency{
			ID:             account.ID,
			MaxConcurrency: account.EffectiveLoadFactor(),
		})
	}
	return loadReq
}

func (s *defaultOpenAIAccountScheduler) finishLoadBalanceSelectionFallback(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	attempt openAIAccountLoadSelectionAttempt,
	budget *openAISelectionProbeBudget,
	filterStats openAISelectionFilterStats,
) (*AccountSelectionResult, int, int, float64, error) {
	candidateCount := attempt.candidateCount
	topK := attempt.topK
	loadSkew := attempt.loadSkew

	if len(attempt.selectionOrder) == 0 {
		return nil, candidateCount, topK, loadSkew, noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, req.RequestedModel, req.routingModel(), attempt.compactBlocked, filterStats.summary("selection_order_empty"))
	}

	cfg := s.service.schedulingConfig()
	compactBlocked := attempt.compactBlocked
	// WaitPlan.MaxConcurrency 使用 Concurrency（非 EffectiveLoadFactor），因为 WaitPlan 控制的是 Redis 实际并发槽位等待。
	passes := 1
	if budget != nil && budget.limited {
		passes = 4
	}
	for pass := 0; pass < passes; pass++ {
		wantAttempted := pass == 1 || pass == 3
		wantKnownFull := pass >= 2
		for _, candidate := range attempt.selectionOrder {
			if candidate.account == nil {
				continue
			}
			if budget != nil && budget.limited {
				knownFull := candidate.loadKnown && candidate.account.Concurrency > 0 &&
					candidate.loadInfo.CurrentConcurrency >= candidate.account.Concurrency
				if budget.wasAttempted(candidate.account.ID) != wantAttempted || knownFull != wantKnownFull {
					continue
				}
			}
			fresh := s.service.resolveFreshSchedulableOpenAIAccount(ctx, candidate.account, req.Platform, req.routingModel(), false, req.RequiredCapability)
			if fresh == nil || !s.isAccountTransportCompatible(fresh, req.RequiredTransport) || !s.isAccountRequestCompatible(ctx, fresh, req) {
				continue
			}
			if !s.consumeOpenAISelectionDBRecheck(budget) {
				return nil, candidateCount, topK, loadSkew, noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, req.RequestedModel, req.routingModel(), compactBlocked, filterStats.summary("selection_order_exhausted"))
			}
			fresh = s.service.recheckSelectedOpenAIAccountFromDB(ctx, fresh, req.GroupID, req.Platform, req.routingModel(), false, req.RequiredCapability)
			if fresh == nil || !s.isAccountTransportCompatible(fresh, req.RequiredTransport) || !s.isAccountRequestCompatible(ctx, fresh, req) {
				continue
			}
			if req.RequireCompact && openAICompactSupportTier(fresh) == 0 {
				compactBlocked = true
				continue
			}
			return &AccountSelectionResult{
				Account: fresh,
				WaitPlan: &AccountWaitPlan{
					AccountID:      fresh.ID,
					MaxConcurrency: fresh.Concurrency,
					Timeout:        cfg.FallbackWaitTimeout,
					MaxWaiting:     cfg.FallbackMaxWaiting,
				},
			}, candidateCount, topK, loadSkew, nil
		}
	}

	return nil, candidateCount, topK, loadSkew, noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, req.RequestedModel, req.routingModel(), compactBlocked, filterStats.summary("selection_order_exhausted"))
}

func (s *defaultOpenAIAccountScheduler) isAccountTransportCompatible(account *Account, requiredTransport OpenAIUpstreamTransport) bool {
	if requiredTransport == OpenAIUpstreamTransportAny || requiredTransport == OpenAIUpstreamTransportHTTPSSE {
		return true
	}
	if s == nil || s.service == nil {
		return false
	}
	return s.service.isOpenAIAccountTransportCompatible(account, requiredTransport)
}

func (s *defaultOpenAIAccountScheduler) lookupShadowParentAccount(ctx context.Context, id int64) *Account {
	if s == nil || s.service == nil {
		return nil
	}
	if s.service.schedulerSnapshot != nil {
		if account, err := s.service.schedulerSnapshot.GetAccount(ctx, id); err == nil && account != nil {
			return account
		}
	}
	if s.service.accountRepo == nil {
		return nil
	}
	account, _ := s.service.accountRepo.GetByID(ctx, id)
	return account
}

func (s *defaultOpenAIAccountScheduler) isAccountRequestCompatible(ctx context.Context, account *Account, req OpenAIAccountScheduleRequest) bool {
	compatible, _ := s.isAccountRequestCompatibleReason(ctx, account, req)
	return compatible
}

// isAccountRequestCompatibleReason 返回账号是否兼容，并在拒绝时标明具体门禁原因。
func (s *defaultOpenAIAccountScheduler) isAccountRequestCompatibleReason(ctx context.Context, account *Account, req OpenAIAccountScheduleRequest) (bool, string) {
	if account == nil {
		return false, "account_nil"
	}
	if s != nil && s.service != nil && s.service.isOpenAIAccountRequestRuntimeBlocked(account, req.routingModel()) {
		return false, "runtime_blocked"
	}
	if s != nil && s.service != nil && s.service.isOpenAIProxyStreamQuarantined(ctx, account) {
		return false, "proxy_stream_quarantined"
	}
	// 配额自动暂停也必须在初始过滤阶段执行。否则 TopK 候选池可能被已暂停账号占满，
	// 后续 fresh/DB 复查无法触达落在 TopK 之外的健康账号，最终在存在健康账号时
	// 仍表现为“无可用账号”。
	if paused, decision := shouldAutoPauseOpenAIAccountByQuota(ctx, account); paused {
		reason := "quota_auto_pause"
		if decision.window != "" {
			reason += "_" + decision.window
		}
		return false, reason
	}
	// 母账号健康联动：影子账号的凭据来自母账号，母账号不可调度时影子也不应被选中。
	// Parent-health gate: shadow borrows the parent's credentials; an unschedulable
	// parent must block the shadow across all scheduler paths.
	if !parentHealthyForShadow(account, func(id int64) *Account {
		return s.lookupShadowParentAccount(ctx, id)
	}) {
		return false, "shadow_parent_unhealthy"
	}
	if !openAIAccountSupportsRoutingModel(ctx, account, req.routingModel()) {
		return false, "model_not_supported"
	}
	if req.GroupID != nil && s != nil && s.service != nil &&
		s.service.needsUpstreamChannelRestrictionCheck(ctx, req.GroupID) &&
		s.service.isUpstreamRoutingModelRestrictedByChannel(ctx, *req.GroupID, account, req.routingModel(), req.RequireCompact) {
		return false, "channel_upstream_restricted"
	}
	if !accountSupportsOpenAICapabilities(account, req.RequiredCapability, req.RequiredImageCapability) {
		return false, "capability_mismatch"
	}
	return true, ""
}

func (s *defaultOpenAIAccountScheduler) ReportResult(accountID int64, success bool, firstTokenMs *int) {
	if s == nil || s.stats == nil {
		return
	}
	s.stats.report(accountID, success, firstTokenMs)
}

func (s *defaultOpenAIAccountScheduler) ReportSwitch() {
	if s == nil {
		return
	}
	s.metrics.recordSwitch()
}

func (s *defaultOpenAIAccountScheduler) SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	if s == nil {
		return OpenAIAccountSchedulerMetricsSnapshot{}
	}

	selectTotal := s.metrics.selectTotal.Load()
	prevHit := s.metrics.stickyPreviousHitTotal.Load()
	sessionHit := s.metrics.stickySessionHitTotal.Load()
	switchTotal := s.metrics.accountSwitchTotal.Load()
	latencyTotal := s.metrics.latencyMsTotal.Load()
	loadSkewTotal := s.metrics.loadSkewMilliTotal.Load()

	snapshot := OpenAIAccountSchedulerMetricsSnapshot{
		SelectTotal:              selectTotal,
		StickyPreviousHitTotal:   prevHit,
		StickySessionHitTotal:    sessionHit,
		LoadBalanceSelectTotal:   s.metrics.loadBalanceSelectTotal.Load(),
		AccountSwitchTotal:       switchTotal,
		SchedulerLatencyMsTotal:  latencyTotal,
		RuntimeStatsAccountCount: s.stats.size(),
	}
	if selectTotal > 0 {
		snapshot.SchedulerLatencyMsAvg = float64(latencyTotal) / float64(selectTotal)
		snapshot.StickyHitRatio = float64(prevHit+sessionHit) / float64(selectTotal)
		snapshot.AccountSwitchRate = float64(switchTotal) / float64(selectTotal)
		snapshot.LoadSkewAvg = float64(loadSkewTotal) / 1000 / float64(selectTotal)
	}
	return snapshot
}

func (s *OpenAIGatewayService) advancedSchedulerSettingRepo() SettingRepository {
	if s == nil || s.rateLimitService == nil || s.rateLimitService.settingService == nil {
		return nil
	}
	return s.rateLimitService.settingService.settingRepo
}

func (s *OpenAIGatewayService) advancedSchedulerRuntimeSettings(ctx context.Context) advancedSchedulerRuntimeSettings {
	if cached, ok := advancedSchedulerSettingCache.Load().(*cachedAdvancedSchedulerSetting); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return advancedSchedulerRuntimeSettings{
				stickyWeightedEnabled:       cached.stickyWeightedEnabled,
				subscriptionPriorityEnabled: cached.subscriptionPriorityEnabled,
				lbTopKOverride:              cached.lbTopKOverride,
				weightOverrides:             cloneAdvancedSchedulerWeightOverrides(cached.weightOverrides),
			}
		}
	}

	result, _, _ := advancedSchedulerSettingSF.Do("advanced_scheduler_settings", func() (any, error) {
		if cached, ok := advancedSchedulerSettingCache.Load().(*cachedAdvancedSchedulerSetting); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return advancedSchedulerRuntimeSettings{
					stickyWeightedEnabled:       cached.stickyWeightedEnabled,
					subscriptionPriorityEnabled: cached.subscriptionPriorityEnabled,
					lbTopKOverride:              cached.lbTopKOverride,
					weightOverrides:             cloneAdvancedSchedulerWeightOverrides(cached.weightOverrides),
				}, nil
			}
		}

		stickyWeightedEnabled := false
		subscriptionPriorityEnabled := false
		lbTopKOverride := 0
		weightOverrides := map[string]float64{}
		if repo := s.advancedSchedulerSettingRepo(); repo != nil {
			dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), advancedSchedulerSettingDBTimeout)
			defer cancel()

			if values, err := repo.GetMultiple(dbCtx, advancedSchedulerRuntimeSettingKeys()); err == nil {
				stickyWeightedEnabled = strings.EqualFold(strings.TrimSpace(values[SettingKeyAdvancedSchedulerStickyWeightedEnabled]), "true")
				subscriptionPriorityEnabled = strings.EqualFold(strings.TrimSpace(values[SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled]), "true")
				lbTopKOverride = parsePositiveIntOverride(values[SettingKeyAdvancedSchedulerLBTopK])
				weightOverrides = parseAdvancedSchedulerWeightOverrides(values)
			} else {
				// 批量读取失败时逐键降级，覆盖全部键（含 TopK/权重），避免只加载布尔开关
				// 而静默丢弃管理员配置的覆盖值；降级状态会被缓存一个 TTL，必须留痕。
				slog.Warn("advanced_scheduler_settings_batch_load_failed", "error", err)
				fallbackValues := make(map[string]string)
				for _, key := range advancedSchedulerRuntimeSettingKeys() {
					if value, valueErr := repo.GetValue(dbCtx, key); valueErr == nil {
						fallbackValues[key] = value
					}
				}
				stickyWeightedEnabled = strings.EqualFold(strings.TrimSpace(fallbackValues[SettingKeyAdvancedSchedulerStickyWeightedEnabled]), "true")
				subscriptionPriorityEnabled = strings.EqualFold(strings.TrimSpace(fallbackValues[SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled]), "true")
				lbTopKOverride = parsePositiveIntOverride(fallbackValues[SettingKeyAdvancedSchedulerLBTopK])
				weightOverrides = parseAdvancedSchedulerWeightOverrides(fallbackValues)
			}
		}

		advancedSchedulerSettingCache.Store(&cachedAdvancedSchedulerSetting{
			stickyWeightedEnabled:       stickyWeightedEnabled,
			subscriptionPriorityEnabled: subscriptionPriorityEnabled,
			lbTopKOverride:              lbTopKOverride,
			weightOverrides:             cloneAdvancedSchedulerWeightOverrides(weightOverrides),
			expiresAt:                   time.Now().Add(advancedSchedulerSettingCacheTTL).UnixNano(),
		})
		return advancedSchedulerRuntimeSettings{
			stickyWeightedEnabled:       stickyWeightedEnabled,
			subscriptionPriorityEnabled: subscriptionPriorityEnabled,
			lbTopKOverride:              lbTopKOverride,
			weightOverrides:             weightOverrides,
		}, nil
	})

	settings, _ := result.(advancedSchedulerRuntimeSettings)
	return settings
}

func (s *OpenAIGatewayService) isAdvancedSchedulerStickyWeightedEnabled(ctx context.Context) bool {
	return s.advancedSchedulerRuntimeSettings(ctx).stickyWeightedEnabled
}

func advancedSchedulerRuntimeSettingKeys() []string {
	keys := []string{
		SettingKeyAdvancedSchedulerStickyWeightedEnabled,
		SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled,
		SettingKeyAdvancedSchedulerLBTopK,
	}
	for _, spec := range advancedSchedulerWeightOverrideSpecs() {
		keys = append(keys, spec.key)
	}
	return keys
}

type advancedSchedulerWeightOverrideSpec struct {
	key  string
	name string
}

func advancedSchedulerWeightOverrideSpecs() []advancedSchedulerWeightOverrideSpec {
	return []advancedSchedulerWeightOverrideSpec{
		{key: SettingKeyAdvancedSchedulerWeightPriority, name: "priority"},
		{key: SettingKeyAdvancedSchedulerWeightLoad, name: "load"},
		{key: SettingKeyAdvancedSchedulerWeightQueue, name: "queue"},
		{key: SettingKeyAdvancedSchedulerWeightErrorRate, name: "error_rate"},
		{key: SettingKeyAdvancedSchedulerWeightTTFT, name: "ttft"},
		{key: SettingKeyAdvancedSchedulerWeightReset, name: "reset"},
		{key: SettingKeyAdvancedSchedulerWeightQuotaHeadroom, name: "quota_headroom"},
		{key: SettingKeyAdvancedSchedulerWeightPreviousResponse, name: "previous_response"},
		{key: SettingKeyAdvancedSchedulerWeightSessionSticky, name: "session_sticky"},
	}
}

func parsePositiveIntOverride(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func parseAdvancedSchedulerWeightOverrides(values map[string]string) map[string]float64 {
	overrides := map[string]float64{}
	for _, spec := range advancedSchedulerWeightOverrideSpecs() {
		raw := strings.TrimSpace(values[spec.key])
		if raw == "" {
			continue
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		overrides[spec.name] = value
	}
	return overrides
}

func cloneAdvancedSchedulerWeightOverrides(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// groupUsesAdvancedScheduler 只依据最终解析后的分组决定调度模式。
func (s *OpenAIGatewayService) groupUsesAdvancedScheduler(ctx context.Context, groupID *int64) bool {
	if s == nil || groupID == nil || *groupID <= 0 {
		return false
	}
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) && group.ID == *groupID {
		return group.UsesAdvancedScheduler()
	}
	if s.schedulerSnapshot == nil {
		return false
	}
	group, err := s.schedulerSnapshot.GetGroupByID(ctx, *groupID)
	return err == nil && group != nil && group.UsesAdvancedScheduler()
}

func (s *OpenAIGatewayService) getOpenAIAccountScheduler(ctx context.Context, groupID *int64) OpenAIAccountScheduler {
	if s == nil {
		return nil
	}
	if !s.groupUsesAdvancedScheduler(ctx, groupID) {
		return nil
	}
	s.openaiSchedulerOnce.Do(func() {
		if s.rateLimitService != nil {
			s.openaiAccountStats = s.rateLimitService.AdvancedSchedulerRuntimeStats()
		}
		if s.openaiAccountStats == nil {
			s.openaiAccountStats = newOpenAIAccountRuntimeStats()
		}
		if s.openaiScheduler == nil {
			s.openaiScheduler = newDefaultOpenAIAccountScheduler(s, s.openaiAccountStats)
		}
	})
	return s.openaiScheduler
}

// ensureOpenAIAccountScheduler 仅供结果统计和诊断初始化调度器实例。
// 实际账号选择仍必须经 getOpenAIAccountScheduler 按分组模式门控。
func (s *OpenAIGatewayService) ensureOpenAIAccountScheduler() OpenAIAccountScheduler {
	if s == nil {
		return nil
	}
	s.openaiSchedulerOnce.Do(func() {
		if s.rateLimitService != nil {
			s.openaiAccountStats = s.rateLimitService.AdvancedSchedulerRuntimeStats()
		}
		if s.openaiAccountStats == nil {
			s.openaiAccountStats = newOpenAIAccountRuntimeStats()
		}
		if s.openaiScheduler == nil {
			s.openaiScheduler = newDefaultOpenAIAccountScheduler(s, s.openaiAccountStats)
		}
	})
	return s.openaiScheduler
}

func resetAdvancedSchedulerSettingCacheForTest() {
	advancedSchedulerSettingCache = atomic.Value{}
	advancedSchedulerSettingSF = singleflight.Group{}
}

func (s *OpenAIGatewayService) SelectAccountWithScheduler(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requireCompact bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	return s.selectAccountWithScheduler(ctx, groupID, previousResponseID, sessionHash, requestedModel, excludedIDs, requiredTransport, "", "", requireCompact, PlatformOpenAI, false)
}

// SelectAccountWithSchedulerForCapability 按能力要求调度账号。
// previousResponseCanMove 表示首包 input 可自行重建工具续链，previous_response_id 允许跨账号迁移
// （粘性加权模式下改为加权偏好而非硬粘连）。
func (s *OpenAIGatewayService) SelectAccountWithSchedulerForCapability(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requireCompact bool,
	previousResponseCanMove bool,
	platformOverride ...string,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	platform := PlatformOpenAI
	if len(platformOverride) > 0 {
		platform = platformOverride[0]
	}
	return s.selectAccountWithScheduler(ctx, groupID, previousResponseID, sessionHash, requestedModel, excludedIDs, requiredTransport, requiredCapability, "", requireCompact, platform, previousResponseCanMove)
}

// SelectAccountWithSchedulerForCapabilityAndRoutingModel 同时保留客户端模型 R 与已解析的账号层模型。
// 该入口供 /v1/messages 使用，使渠道限制按 R/C 检查，账号能力与账号映射按 D 检查。
func (s *OpenAIGatewayService) SelectAccountWithSchedulerForCapabilityAndRoutingModel(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requireCompact bool,
	previousResponseCanMove bool,
	platformOverride ...string,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	platform := PlatformOpenAI
	if len(platformOverride) > 0 {
		platform = platformOverride[0]
	}
	routingModel = strings.TrimSpace(routingModel)
	if routingModel == "" {
		routingModel = s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	}
	return s.selectAccountWithSchedulerForRouting(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, "", requireCompact, platform, previousResponseCanMove)
}

func (s *OpenAIGatewayService) SelectAccountWithSchedulerForImages(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredCapability OpenAIImagesCapability,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	selection, decision, err := s.selectAccountWithScheduler(ctx, groupID, "", sessionHash, requestedModel, excludedIDs, OpenAIUpstreamTransportHTTPSSE, "", requiredCapability, false, PlatformOpenAI, false)
	if err == nil && selection != nil && selection.Account != nil {
		return selection, decision, nil
	}
	// 如果要求 native 能力（如指定了模型）但没有可用的 APIKey 账号，回退到 basic（OAuth 账号）
	if requiredCapability == OpenAIImagesCapabilityNative {
		return s.selectAccountWithScheduler(ctx, groupID, "", sessionHash, requestedModel, excludedIDs, OpenAIUpstreamTransportHTTPSSE, "", OpenAIImagesCapabilityBasic, false, PlatformOpenAI, false)
	}
	return selection, decision, err
}

func (s *OpenAIGatewayService) selectAccountWithScheduler(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requiredImageCapability OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	routingModel := s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	return s.selectAccountWithSchedulerForRouting(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
}

// selectAccountWithSchedulerForRouting 首次调度仍把隔离代理当作不可用；只有因容量耗尽
// 失败且熔断器确实在隔离代理时，才用同一账号层模型重跑一次并忽略隔离。这样健康代理
// 始终优先，同时避免共享代理场景被熔断器清空全部容量。
func (s *OpenAIGatewayService) selectAccountWithSchedulerForRouting(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requiredImageCapability OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	originalGroupID := derefGroupID(groupID)
	resolvedCtx, resolvedGroupID, err := s.resolveOpenAISchedulerGroup(ctx, groupID)
	if err != nil {
		return nil, OpenAIAccountScheduleDecision{}, err
	}
	ctx = resolvedCtx
	groupID = resolvedGroupID
	if derefGroupID(groupID) != originalGroupID {
		// 回退后的分组可能有不同渠道映射，必须重新解析账号层模型。
		routingModel = s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	}
	selection, decision, err := s.selectAccountWithSchedulerForRoutingOnce(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
	if err == nil || openAIProxyStreamQuarantineBypassed(ctx) {
		return selection, decision, err
	}
	if !errors.Is(err, ErrNoAvailableAccounts) && !errors.Is(err, ErrNoAvailableCompactAccounts) {
		return selection, decision, err
	}
	// 断流熔断器只隔离 OpenAI 平台账号，其他兼容平台不应触发第二次调度。
	if normalizeOpenAICompatiblePlatform(platform) != PlatformOpenAI {
		return selection, decision, err
	}
	blocked := s.getOpenAIProxyStreamCircuit().activeBlockCount(time.Now())
	if blocked == 0 {
		return selection, decision, err
	}
	s.logOpenAIProxyStreamQuarantineFailOpen(requestedModel, blocked)
	return s.selectAccountWithSchedulerForRoutingOnce(withOpenAIProxyStreamQuarantineBypass(ctx), groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
}

// resolveOpenAISchedulerGroup 解析 OpenAI 路径最终实际使用的分组。
// 与通用网关保持一致：Claude Code 专属分组会在非 Claude Code 请求时沿回退链解析，
// 因此高级调度器模式、渠道映射和粘性缓存均绑定到最终目标分组。
func (s *OpenAIGatewayService) resolveOpenAISchedulerGroup(ctx context.Context, groupID *int64) (context.Context, *int64, error) {
	if groupID == nil || *groupID <= 0 {
		return ctx, groupID, nil
	}
	if forcePlatform, ok := ctx.Value(ctxkey.ForcePlatform).(string); ok && strings.TrimSpace(forcePlatform) != "" {
		return ctx, groupID, nil
	}

	currentID := *groupID
	visited := map[int64]struct{}{}
	for {
		if _, seen := visited[currentID]; seen {
			return ctx, nil, fmt.Errorf("fallback group cycle detected")
		}
		visited[currentID] = struct{}{}

		var group *Group
		if contextual, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(contextual) && contextual.ID == currentID {
			group = contextual
		} else if s != nil && s.schedulerSnapshot != nil {
			resolved, err := s.schedulerSnapshot.GetGroupByID(ctx, currentID)
			if err != nil {
				return ctx, nil, err
			}
			group = resolved
		}
		if group == nil {
			// 没有可用分组快照时保持已有选择语义；高级模式也会安全回退到基础路径。
			return ctx, &currentID, nil
		}
		if !group.ClaudeCodeOnly || IsClaudeCodeClient(ctx) {
			return context.WithValue(ctx, ctxkey.Group, group), &currentID, nil
		}
		if group.FallbackGroupID == nil || *group.FallbackGroupID <= 0 {
			return ctx, nil, ErrClaudeCodeOnly
		}
		currentID = *group.FallbackGroupID
	}
}

func (s *OpenAIGatewayService) selectAccountWithSchedulerForRoutingOnce(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requiredImageCapability OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	platform = normalizeOpenAICompatiblePlatform(platform)
	decision := OpenAIAccountScheduleDecision{}
	scheduler := s.getOpenAIAccountScheduler(ctx, groupID)
	if scheduler == nil {
		decision.Layer = openAIAccountScheduleLayerLoadBalance
		if requiredTransport == OpenAIUpstreamTransportAny || requiredTransport == OpenAIUpstreamTransportHTTPSSE {
			effectiveExcludedIDs := cloneExcludedAccountIDs(excludedIDs)
			for {
				selection, err := s.selectAccountWithLoadAwarenessForRouting(ctx, groupID, platform, sessionHash, requestedModel, routingModel, effectiveExcludedIDs, requireCompact, requiredCapability)
				if err != nil {
					return nil, decision, err
				}
				if selection == nil || selection.Account == nil {
					return selection, decision, nil
				}
				if accountSupportsOpenAICapabilities(selection.Account, requiredCapability, requiredImageCapability) {
					return selection, decision, nil
				}
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				if effectiveExcludedIDs == nil {
					effectiveExcludedIDs = make(map[int64]struct{})
				}
				if _, exists := effectiveExcludedIDs[selection.Account.ID]; exists {
					return nil, decision, ErrNoAvailableAccounts
				}
				effectiveExcludedIDs[selection.Account.ID] = struct{}{}
			}
		}

		effectiveExcludedIDs := cloneExcludedAccountIDs(excludedIDs)
		for {
			selection, err := s.selectAccountWithLoadAwarenessForRouting(ctx, groupID, platform, sessionHash, requestedModel, routingModel, effectiveExcludedIDs, requireCompact, requiredCapability)
			if err != nil {
				return nil, decision, err
			}
			if selection == nil || selection.Account == nil {
				return selection, decision, nil
			}
			if s.isOpenAIAccountTransportCompatible(selection.Account, requiredTransport) &&
				accountSupportsOpenAICapabilities(selection.Account, requiredCapability, requiredImageCapability) {
				return selection, decision, nil
			}
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			if effectiveExcludedIDs == nil {
				effectiveExcludedIDs = make(map[int64]struct{})
			}
			if _, exists := effectiveExcludedIDs[selection.Account.ID]; exists {
				return nil, decision, ErrNoAvailableAccounts
			}
			effectiveExcludedIDs[selection.Account.ID] = struct{}{}
		}
	}

	if s.checkChannelPricingRestriction(ctx, groupID, requestedModel) {
		slog.Warn("channel pricing restriction blocked request",
			"group_id", derefGroupID(groupID),
			"model", requestedModel)
		return nil, decision, fmt.Errorf("%w supporting model: %s (channel pricing restriction)", ErrNoAvailableAccounts, requestedModel)
	}

	var stickyAccountID int64
	if sessionHash != "" && s.cache != nil {
		if accountID, err := s.getStickySessionAccountID(ctx, groupID, sessionHash); err == nil && accountID > 0 {
			stickyAccountID = accountID
		}
	}
	effectiveSettings := s.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID)
	stickyWeighted := effectiveSettings.stickyWeightedEnabled
	subscriptionPriority := effectiveSettings.subscriptionPriorityEnabled
	stickyPreviousAccountID := int64(0)
	if stickyWeighted && previousResponseCanMove && strings.TrimSpace(previousResponseID) != "" && platform == PlatformOpenAI {
		stickyPreviousAccountID = s.ResolveAccountIDByPreviousResponseIDForScheduler(ctx, groupID, previousResponseID, routingModel, excludedIDs, requiredCapability, requireCompact)
	}

	selection, decision, selectErr := scheduler.Select(ctx, OpenAIAccountScheduleRequest{
		GroupID:                 groupID,
		Platform:                platform,
		SessionHash:             sessionHash,
		StickyAccountID:         stickyAccountID,
		StickyPreviousAccountID: stickyPreviousAccountID,
		StickyWeighted:          stickyWeighted,
		SubscriptionPriority:    subscriptionPriority,
		PreviousResponseID:      previousResponseID,
		PreviousResponseCanMove: previousResponseCanMove,
		RequestedModel:          requestedModel,
		RoutingModel:            routingModel,
		RequiredTransport:       requiredTransport,
		RequiredCapability:      requiredCapability,
		RequiredImageCapability: requiredImageCapability,
		RequireCompact:          requireCompact,
		ExcludedIDs:             excludedIDs,
	})
	if selection != nil {
		// 只有进入 OpenAI 高级适配器的选择才允许写入高级运行时反馈。
		selection.AdvancedScheduler = true
	}
	return selection, decision, selectErr
}

func accountSupportsOpenAICapabilities(account *Account, requiredCapability OpenAIEndpointCapability, requiredImageCapability OpenAIImagesCapability) bool {
	if account == nil {
		return false
	}
	return account.SupportsOpenAIEndpointCapability(requiredCapability) &&
		account.SupportsOpenAIImageCapability(requiredImageCapability)
}

func cloneExcludedAccountIDs(excludedIDs map[int64]struct{}) map[int64]struct{} {
	if len(excludedIDs) == 0 {
		return nil
	}
	cloned := make(map[int64]struct{}, len(excludedIDs))
	for id := range excludedIDs {
		cloned[id] = struct{}{}
	}
	return cloned
}

func (s *OpenAIGatewayService) isOpenAIAccountTransportCompatible(account *Account, requiredTransport OpenAIUpstreamTransport) bool {
	if requiredTransport == OpenAIUpstreamTransportAny || requiredTransport == OpenAIUpstreamTransportHTTPSSE {
		return true
	}
	if s == nil || account == nil {
		return false
	}
	if requiredTransport == OpenAIUpstreamTransportResponsesWebsocketV2Ingress {
		// Grok WS ingress 在转发层固定走 HTTP bridge，不依赖仅适用于 OpenAI 账号的 WSv2 mode。
		if account.IsGrok() {
			return true
		}
		if s.cfg == nil || !s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled {
			return s.getOpenAIWSProtocolResolver().Resolve(account).Transport == OpenAIUpstreamTransportResponsesWebsocketV2
		}
		switch account.ResolveOpenAIResponsesWebSocketV2Mode(s.cfg.Gateway.OpenAIWS.IngressModeDefault) {
		case OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough, OpenAIWSIngressModeHTTPBridge, OpenAIWSIngressModeShared, OpenAIWSIngressModeDedicated:
			return true
		default:
			return false
		}
	}
	return s.getOpenAIWSProtocolResolver().Resolve(account).Transport == requiredTransport
}

func (s *OpenAIGatewayService) ReportOpenAIAccountScheduleResult(accountID int64, model string, success bool, firstTokenMs *int) {
	// 旧调用点不携带本次选择模式，不能据此写入高级统计；成功结果仍需清理模型临时状态。
	s.reportOpenAIAccountScheduleResult(false, accountID, model, success, firstTokenMs)
}

// ReportOpenAIAccountScheduleResultForSelection 按本次实际选择模式写入反馈。
// 基础分组仍清理请求临时状态，但不会污染高级调度统计。
func (s *OpenAIGatewayService) ReportOpenAIAccountScheduleResultForSelection(selection *AccountSelectionResult, accountID int64, model string, success bool, firstTokenMs *int) {
	s.reportOpenAIAccountScheduleResult(selection != nil && selection.AdvancedScheduler, accountID, model, success, firstTokenMs)
}

func (s *OpenAIGatewayService) reportOpenAIAccountScheduleResult(advanced bool, accountID int64, model string, success bool, firstTokenMs *int) {
	if success {
		s.clearOpenAIAccountModelTransientState(accountID, normalizeOpenAIAccountModelTransientModel(model))
	}
	if !advanced {
		return
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportResult(accountID, success, firstTokenMs)
}

func (s *OpenAIGatewayService) RecordOpenAIAccountSwitch() {
	// 没有选择结果时无法判断所属分组，避免把基础调度的切换误记为高级调度事件。
	s.recordOpenAIAccountSwitch(false)
}

// RecordOpenAIAccountSwitchForSelection 只记录高级调度请求的账号切换。
func (s *OpenAIGatewayService) RecordOpenAIAccountSwitchForSelection(selection *AccountSelectionResult) {
	s.recordOpenAIAccountSwitch(selection != nil && selection.AdvancedScheduler)
}

func (s *OpenAIGatewayService) recordOpenAIAccountSwitch(advanced bool) {
	if !advanced {
		return
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportSwitch()
}

func (s *OpenAIGatewayService) SnapshotOpenAIAccountSchedulerMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return OpenAIAccountSchedulerMetricsSnapshot{}
	}
	return scheduler.SnapshotMetrics()
}

func (s *OpenAIGatewayService) openAIWSSessionStickyTTL() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds > 0 {
		return time.Duration(s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds) * time.Second
	}
	return openaiStickySessionTTL
}

func (s *OpenAIGatewayService) openAIWSLBTopK() int {
	if s != nil && s.cfg != nil && s.cfg.Gateway.AdvancedScheduler.LBTopK > 0 {
		return s.cfg.Gateway.AdvancedScheduler.LBTopK
	}
	return 7
}

func (s *OpenAIGatewayService) openAIWSLBTopKForRequest(ctx context.Context) int {
	base := s.openAIWSLBTopK()
	settings := s.advancedSchedulerRuntimeSettings(ctx)
	if settings.lbTopKOverride > 0 {
		return settings.lbTopKOverride
	}
	return base
}

func resolveAdvancedStickyEscapeConfig(appConfig *config.Config) advancedStickyEscapeConfig {
	if appConfig != nil {
		cfg := appConfig.Gateway.AdvancedScheduler
		enabled := cfg.StickyEscapeEnabled
		if !enabled && cfg.StickyEscapeTTFTMs == 0 && cfg.StickyEscapeErrorRate == 0 {
			enabled = true
		}
		ttftMs := float64(cfg.StickyEscapeTTFTMs)
		if ttftMs <= 0 {
			ttftMs = 15000
		}
		errorRate := cfg.StickyEscapeErrorRate
		if errorRate < 0 || errorRate > 1 {
			errorRate = 0.5
		}
		if errorRate == 0 && cfg.StickyEscapeTTFTMs == 0 && cfg.StickyEscapeErrorRate == 0 {
			errorRate = 0.5
		}
		return advancedStickyEscapeConfig{
			enabled:   enabled,
			ttftMs:    ttftMs,
			errorRate: errorRate,
		}
	}
	return advancedStickyEscapeConfig{
		enabled:   true,
		ttftMs:    15000,
		errorRate: 0.5,
	}
}

func (s *OpenAIGatewayService) openAIStickyEscapeConfig() advancedStickyEscapeConfig {
	if s == nil {
		return resolveAdvancedStickyEscapeConfig(nil)
	}
	return resolveAdvancedStickyEscapeConfig(s.cfg)
}

func (s *OpenAIGatewayService) openAIWSSchedulerWeights() GatewayAdvancedSchedulerScoreWeightsView {
	if s != nil && s.cfg != nil {
		return GatewayAdvancedSchedulerScoreWeightsView{
			Priority:      s.cfg.Gateway.AdvancedScheduler.ScoreWeights.Priority,
			Load:          s.cfg.Gateway.AdvancedScheduler.ScoreWeights.Load,
			Queue:         s.cfg.Gateway.AdvancedScheduler.ScoreWeights.Queue,
			ErrorRate:     s.cfg.Gateway.AdvancedScheduler.ScoreWeights.ErrorRate,
			TTFT:          s.cfg.Gateway.AdvancedScheduler.ScoreWeights.TTFT,
			Reset:         s.cfg.Gateway.AdvancedScheduler.ScoreWeights.Reset,
			QuotaHeadroom: s.cfg.Gateway.AdvancedScheduler.ScoreWeights.QuotaHeadroom,
			Previous:      s.cfg.Gateway.AdvancedScheduler.ScoreWeights.PreviousResponse,
			SessionSticky: s.cfg.Gateway.AdvancedScheduler.ScoreWeights.SessionSticky,
		}
	}
	return GatewayAdvancedSchedulerScoreWeightsView{
		Priority:      1.0,
		Load:          1.0,
		Queue:         0.7,
		ErrorRate:     0.8,
		TTFT:          0.5,
		Reset:         0.0,
		QuotaHeadroom: 0.0,
		Previous:      5.0,
		SessionSticky: 3.0,
	}
}

func (s *OpenAIGatewayService) openAIWSSchedulerWeightsForRequest(ctx context.Context) GatewayAdvancedSchedulerScoreWeightsView {
	weights := s.openAIWSSchedulerWeights()
	settings := s.advancedSchedulerRuntimeSettings(ctx)
	overridden := applyAdvancedSchedulerWeightOverrides(weights, settings.weightOverrides)
	if !overridden.configWeights().IsValid() {
		return weights
	}
	return overridden
}

func applyAdvancedSchedulerWeightOverrides(
	weights GatewayAdvancedSchedulerScoreWeightsView,
	overrides map[string]float64,
) GatewayAdvancedSchedulerScoreWeightsView {
	for key, value := range overrides {
		switch key {
		case "priority":
			weights.Priority = value
		case "load":
			weights.Load = value
		case "queue":
			weights.Queue = value
		case "error_rate":
			weights.ErrorRate = value
		case "ttft":
			weights.TTFT = value
		case "reset":
			weights.Reset = value
		case "quota_headroom":
			weights.QuotaHeadroom = value
		case "previous_response":
			weights.Previous = value
		case "session_sticky":
			weights.SessionSticky = value
		}
	}
	return weights
}

type GatewayAdvancedSchedulerScoreWeightsView struct {
	Priority  float64
	Load      float64
	Queue     float64
	ErrorRate float64
	TTFT      float64
	// Reset 倾向「会话窗口最早重置」的账号；0 表示关闭（默认）。
	Reset float64
	// QuotaHeadroom 倾向 Codex 7d 剩余额度更健康的账号；0 表示关闭（默认）。
	QuotaHeadroom float64
	Previous      float64
	SessionSticky float64
}

func (w GatewayAdvancedSchedulerScoreWeightsView) configWeights() config.GatewayAdvancedSchedulerScoreWeights {
	return config.GatewayAdvancedSchedulerScoreWeights{
		Priority:         w.Priority,
		Load:             w.Load,
		Queue:            w.Queue,
		ErrorRate:        w.ErrorRate,
		TTFT:             w.TTFT,
		Reset:            w.Reset,
		QuotaHeadroom:    w.QuotaHeadroom,
		PreviousResponse: w.Previous,
		SessionSticky:    w.SessionSticky,
	}
}

// BuildAdvancedAccountSchedulerScoreSnapshot 按运行时设置生成通用高级调度评分快照。
func (s *RateLimitService) BuildAdvancedAccountSchedulerScoreSnapshot(
	ctx context.Context,
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
) map[int64]AdvancedAccountSchedulerScoreSnapshot {
	return s.BuildAdvancedAccountSchedulerScoreSnapshotForGroup(ctx, nil, accounts, loadMap)
}

// BuildAdvancedAccountSchedulerScoreSnapshotForGroup 按指定高级分组的有效配置生成评分快照。
// 该入口供管理端展示使用，必须与实际调度的分组覆盖优先级保持一致。
func (s *RateLimitService) BuildAdvancedAccountSchedulerScoreSnapshotForGroup(
	ctx context.Context,
	group *Group,
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
) map[int64]AdvancedAccountSchedulerScoreSnapshot {
	gateway := &OpenAIGatewayService{cfg: nil, rateLimitService: s}
	var stats *advancedAccountRuntimeStats
	if s != nil {
		gateway.cfg = s.cfg
		stats = s.AdvancedSchedulerRuntimeStats()
	}
	effectiveSettings := gateway.advancedSchedulerEffectiveSettingsForGroup(ctx, group)
	return buildAdvancedAccountSchedulerScoreSnapshot(
		accounts,
		loadMap,
		stats,
		group,
		effectiveSettings.weights,
		effectiveSettings.stickyWeightedEnabled,
		openAIQuotaHeadroomFactor,
	)
}

// BuildAdvancedAccountSchedulerScoreSnapshot 按默认配置生成通用高级调度评分快照。
func BuildAdvancedAccountSchedulerScoreSnapshot(
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
) map[int64]AdvancedAccountSchedulerScoreSnapshot {
	return BuildAdvancedAccountSchedulerScoreSnapshotForGroup(nil, accounts, loadMap)
}

// BuildAdvancedAccountSchedulerScoreSnapshotForGroup 按静态默认值和分组覆盖生成评分。
// 未注入设置服务的管理端测试路径使用该函数。
func BuildAdvancedAccountSchedulerScoreSnapshotForGroup(
	group *Group,
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
) map[int64]AdvancedAccountSchedulerScoreSnapshot {
	gateway := &OpenAIGatewayService{}
	effectiveSettings := gateway.advancedSchedulerEffectiveSettingsForGroup(context.Background(), group)
	return buildAdvancedAccountSchedulerScoreSnapshot(
		accounts,
		loadMap,
		nil,
		group,
		effectiveSettings.weights,
		effectiveSettings.stickyWeightedEnabled,
		openAIQuotaHeadroomFactor,
	)
}

// openAIQuotaHeadroomFactor 把 Codex quota 快照转换成 0..1 的调度因子。
// 7d/primary 剩余额度越高分越高；5h/secondary 接近耗尽时会折扣该分值。
func openAIQuotaHeadroomFactor(account *Account, now time.Time) float64 {
	if account == nil || len(account.Extra) == 0 || openAIQuotaHeadroomSnapshotStale(account.Extra, now) {
		return openAIQuotaHeadroomNeutralFactor
	}
	primaryUsedPercent, ok := resolveAccountExtraNumber(account.Extra, "codex_primary_used_percent", "codex_7d_used_percent")
	if !ok || openAIQuotaWindowResetAny(account.Extra, now, "primary", "7d") {
		return openAIQuotaHeadroomNeutralFactor
	}

	factor := 1 - clamp01(primaryUsedPercent/100)
	if secondaryUsedPercent, ok := resolveAccountExtraNumber(account.Extra, "codex_secondary_used_percent", "codex_5h_used_percent"); ok &&
		!openAIQuotaWindowResetAny(account.Extra, now, "secondary", "5h") {
		secondaryRemaining := 1 - clamp01(secondaryUsedPercent/100)
		if secondaryRemaining < openAIQuotaHeadroomSecondaryLowRemain {
			factor *= openAIQuotaHeadroomNeutralFactor
		}
	}
	return factor
}

// openAIQuotaHeadroomSnapshotStale 判断 quota 快照是否过旧到只能按中性分参与调度。
func openAIQuotaHeadroomSnapshotStale(extra map[string]any, now time.Time) bool {
	updatedRaw, ok := extra["codex_usage_updated_at"]
	if !ok {
		return true
	}
	updatedAt, err := parseTime(fmt.Sprint(updatedRaw))
	if err != nil {
		return true
	}
	return now.Sub(updatedAt) >= openAIQuotaHeadroomSnapshotStaleAfter
}

// openAIQuotaWindowResetAny 支持同时检查 primary/7d 或 secondary/5h 兼容字段。
func openAIQuotaWindowResetAny(extra map[string]any, now time.Time, windows ...string) bool {
	for _, window := range windows {
		if openAIQuotaWindowReset(extra, window, now) {
			return true
		}
	}
	return false
}

func clamp01(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func calcLoadSkewByMoments(sum float64, sumSquares float64, count int) float64 {
	if count <= 1 {
		return 0
	}
	mean := sum / float64(count)
	variance := sumSquares/float64(count) - mean*mean
	if variance < 0 {
		variance = 0
	}
	return math.Sqrt(variance)
}
