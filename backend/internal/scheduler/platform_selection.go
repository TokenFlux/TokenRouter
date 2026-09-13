package scheduler

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

const (
	openAIAccountScheduleLayerPreviousResponse = "previous_response_id"
	openAIAccountScheduleLayerGuardianParent   = "guardian_parent"
	openAIAccountScheduleLayerSessionSticky    = "session_hash"
	openAIAccountScheduleLayerLoadBalance      = "load_balance"
	openAIAccountSelectionProbeLimit           = 64
)

type PlatformSelectionInput struct {
	GroupID                 *int64
	Platform                string
	SessionHash             string
	StickyAccountID         int64
	GuardianParentAccountID int64
	StickyPreviousAccountID int64
	StickyWeighted          bool
	SubscriptionPriority    bool
	PreserveStickyBinding   bool
	RequirePrivacySet       bool
	PreviousResponseID      string
	PreviousResponseCanMove bool
	RequestedModel          string // 客户端请求模型 R，用于限制、错误和会话语义。
	RoutingModel            string // 账号层模型：普通请求为 C，Messages 为分组映射后的 D。
	RequiredTransport       string
	RequiredCapability      account.OpenAIEndpointCapability
	RequiredImageCapability account.OpenAIImagesCapability
	RequireCompact          bool
	ExcludedIDs             map[int64]struct{}
	// AdvancedSchedulerFeedbackConfig 与 StickyEscapeConfig 固定本次请求使用的有效策略。
	AdvancedSchedulerFeedbackConfig policy.FeedbackConfig
	StickyEscapeConfig              policy.StickyEscapeConfig
}

func (r PlatformSelectionInput) routingModel() string {
	if model := strings.TrimSpace(r.RoutingModel); model != "" {
		return model
	}
	return r.RequestedModel
}

type PlatformDecision struct {
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
type PlatformMetricsSnapshot struct {
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
type PlatformMetrics struct {
	selectTotal            atomic.Int64
	stickyPreviousHitTotal atomic.Int64
	stickySessionHitTotal  atomic.Int64
	loadBalanceSelectTotal atomic.Int64
	accountSwitchTotal     atomic.Int64
	latencyMsTotal         atomic.Int64
	loadSkewMilliTotal     atomic.Int64
}

func (m *PlatformMetrics) RecordSelect(decision PlatformDecision) {
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
func (m *PlatformMetrics) RecordSwitch() {
	if m == nil {
		return
	}
	m.accountSwitchTotal.Add(1)
}
func (m *PlatformMetrics) Snapshot(accountCount int) PlatformMetricsSnapshot {
	if m == nil {
		return PlatformMetricsSnapshot{}
	}

	selectTotal := m.selectTotal.Load()
	prevHit := m.stickyPreviousHitTotal.Load()
	sessionHit := m.stickySessionHitTotal.Load()
	switchTotal := m.accountSwitchTotal.Load()
	latencyTotal := m.latencyMsTotal.Load()
	loadSkewTotal := m.loadSkewMilliTotal.Load()

	snapshot := PlatformMetricsSnapshot{
		SelectTotal:              selectTotal,
		StickyPreviousHitTotal:   prevHit,
		StickySessionHitTotal:    sessionHit,
		LoadBalanceSelectTotal:   m.loadBalanceSelectTotal.Load(),
		AccountSwitchTotal:       switchTotal,
		SchedulerLatencyMsTotal:  latencyTotal,
		RuntimeStatsAccountCount: accountCount,
	}
	if selectTotal > 0 {
		snapshot.SchedulerLatencyMsAvg = float64(latencyTotal) / float64(selectTotal)
		snapshot.StickyHitRatio = float64(prevHit+sessionHit) / float64(selectTotal)
		snapshot.AccountSwitchRate = float64(switchTotal) / float64(selectTotal)
		snapshot.LoadSkewAvg = float64(loadSkewTotal) / 1000 / float64(selectTotal)
	}
	return snapshot
}

type ProbeBudget struct {
	acquires  int
	rechecks  int
	attempted map[int64]struct{}
	limited   bool
}

func NewProbeBudget() *ProbeBudget {
	return &ProbeBudget{attempted: make(map[int64]struct{})}
}
func (b *ProbeBudget) enableLimit() {
	if b != nil {
		b.limited = true
	}
}
func (b *ProbeBudget) recordAcquire(accountID int64) bool {
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
func (b *ProbeBudget) recordRecheck() bool {
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
func (b *ProbeBudget) acquireExhausted() bool {
	return b != nil && b.limited && b.acquires >= openAIAccountSelectionProbeLimit
}
func (b *ProbeBudget) wasAttempted(accountID int64) bool {
	if b == nil {
		return false
	}
	_, ok := b.attempted[accountID]
	return ok
}

// PlatformCandidateScore 使用同一评分结果，仅保留本次候选的无凭据关联。
type PlatformCandidateScore struct {
	Account                                                          *FlowAccount
	LoadInfo                                                         *AccountLoadInfo
	LoadKnown                                                        bool
	Score, BaseScore, StickyBonus, PreviousBonus, SessionStickyBonus float64
	Priority                                                         int
	ErrorRate, TTFT                                                  float64
	HasTTFT, HasFeedback                                             bool
	Feedback                                                         FeedbackSnapshot
	Factors                                                          CandidateFactors
}

// PlatformSelectionPorts 负责平台特有资格，评分、绑定优先级、抢槽与等待由核心决定。
type PlatformSelectionPorts struct {
	BasicStickyTTL     time.Duration
	CheckPricing       func(context.Context, *int64, string) bool
	Hydrate            func(context.Context, *FlowAccount) (*FlowAccount, error)
	SetSticky          func(context.Context, *int64, string, int64, time.Duration) error
	PrivacyAllowed     func(context.Context, *int64, *FlowAccount) bool
	ShadowAllowed      func(context.Context, *FlowAccount) bool
	ParentHealthy      func(*FlowAccount, func(int64) *FlowAccount) bool
	ParentLookup       func(context.Context) func(int64) *FlowAccount
	ReadAccountDB      func(context.Context, int64) (*FlowAccount, error)
	NeedsChannelCheck  func(context.Context, *int64) bool
	ChannelRestricted  func(context.Context, int64, *FlowAccount, string, bool) bool
	BasicEligible      func(context.Context, *FlowAccount, string, string, bool, account.OpenAIEndpointCapability) bool
	BasicFailureReason func(context.Context, *FlowAccount, string, string, bool, account.OpenAIEndpointCapability) string
	CompleteAcquired   func(context.Context, *FlowAccount, func()) (*FlowSelection, error)
	Complete           func(context.Context, *FlowAccount, bool, func(), *AccountWaitPlan) (*FlowSelection, error)

	Available, CacheAvailable, SnapshotAvailable, RecheckAvailable bool
	Effective                                                      func(context.Context, *int64) policy.EffectiveSettings
	GroupRequiresPrivacy                                           func(context.Context, *int64) bool
	PreviousResponse                                               func(context.Context, *int64, string, string, map[int64]struct{}, account.OpenAIEndpointCapability, bool) (*FlowSelection, error)
	RequestCompatible                                              func(context.Context, *FlowAccount, PlatformSelectionInput) (bool, string)
	TransportCompatible                                            func(*FlowAccount, string) bool
	HasGroupMetadata                                               func(*FlowAccount) bool
	MatchesGroup                                                   func(*FlowAccount, *int64) bool
	BindSticky                                                     func(context.Context, *int64, string, int64) error
	DeleteSticky                                                   func(context.Context, *int64, string) error
	GetSticky                                                      func(context.Context, *int64, string) (int64, error)
	RefreshSticky                                                  func(context.Context, *int64, string, time.Duration) error
	StickyTTL                                                      func() time.Duration
	Options                                                        func() FlowOptions
	GetSchedulable                                                 func(context.Context, int64) (*FlowAccount, error)
	ClearSticky                                                    func(*FlowAccount, string) bool
	IsCompatible                                                   func(*FlowAccount) bool
	IsSchedulable                                                  func(*FlowAccount) bool
	Recheck                                                        func(context.Context, *FlowAccount, *int64, string, string, bool, account.OpenAIEndpointCapability) *FlowAccount
	Fresh                                                          func(context.Context, *FlowAccount, string, string, bool, account.OpenAIEndpointCapability) *FlowAccount
	FreeQuota                                                      func(context.Context, []FlowAccount) []FlowAccount
	CanonicalModel                                                 func(*FlowAccount, string) string
	TeamLimited                                                    func(*FlowAccount, string, time.Time) bool
	ModelQuotaBlocked                                              func(int64, string, time.Time) bool
	Acquire                                                        func(context.Context, int64, int) (*AcquireResult, error)
	ListCandidates                                                 func(context.Context, *int64, string) ([]FlowAccount, error)
	RuntimeBlocked                                                 func(*FlowAccount, string) bool
	FilterTeamLimited                                              func([]FlowAccount, string, time.Time) []FlowAccount
	FilterModelQuota                                               func([]FlowAccount, string, time.Time) []FlowAccount
	CompactAllowed                                                 func(*FlowAccount) bool
	IsSubscription                                                 func(*FlowAccount) bool
	QuotaHeadroom                                                  func(*ScoreAccount, time.Time) float64
	Unavailable                                                    func(context.Context, string, string, bool, string, ...[]FlowAccount) error
}

// PlatformSelector 复用同一个反馈与计数实例，按次创建的端口只持有请求内投影。
type PlatformSelector struct {
	ports       PlatformSelectionPorts
	concurrency *ConcurrencyService
	stats       *RuntimeStats
	metrics     *PlatformMetrics
	diagnostics Diagnostics
	now         func() time.Time
}

func NewPlatformSelector(ports PlatformSelectionPorts, concurrency *ConcurrencyService, stats *RuntimeStats, metrics *PlatformMetrics, diagnostics Diagnostics, now func() time.Time) *PlatformSelector {
	return &PlatformSelector{ports: ports, concurrency: concurrency, stats: stats, metrics: metrics, diagnostics: diagnostics, now: now}
}

var ErrNoAvailableCompactAccounts = errors.New("no available accounts support /responses/compact")

func (s *PlatformSelector) requestCompatible(ctx context.Context, a *FlowAccount, input PlatformSelectionInput) bool {
	ok, _ := s.ports.RequestCompatible(ctx, a, input)
	return ok
}
func (s *PlatformSelector) canRecheck(b *ProbeBudget) bool {
	if !s.ports.RecheckAvailable {
		return true
	}
	return b.recordRecheck()
}

type PlatformLoadPlan struct {
	allCandidates             []PlatformCandidateScore
	candidates                []PlatformCandidateScore
	staleSnapshotCompactRetry []PlatformCandidateScore
	selectionOrder            []PlatformCandidateScore
	candidateCount            int
	topK                      int
	loadSkew                  float64
}
type platformPoolAttempt struct {
	result              *FlowSelection
	selectionOrder      []PlatformCandidateScore
	candidateCount      int
	topK                int
	loadSkew            float64
	compactBlocked      bool
	noCompactCandidates bool
	err                 error
}
type PlatformFilterStats struct {
	Pool    int
	Reasons map[string]int
}

func (s *PlatformFilterStats) Exclude(reason string) {
	if s.Reasons == nil {
		s.Reasons = make(map[string]int, 4)
	}
	s.Reasons[reason]++
}
func (s PlatformFilterStats) Summary(extra string) string {
	var b strings.Builder
	_, _ = b.WriteString("pool=")
	_, _ = b.WriteString(strconv.Itoa(s.Pool))
	if len(s.Reasons) > 0 {
		reasons := make([]string, 0, len(s.Reasons))
		for reason := range s.Reasons {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		_, _ = b.WriteString(", filtered:")
		for _, reason := range reasons {
			_, _ = b.WriteString(" ")
			_, _ = b.WriteString(reason)
			_, _ = b.WriteString("=")
			_, _ = b.WriteString(strconv.Itoa(s.Reasons[reason]))
		}
	}
	if extra != "" {
		_, _ = b.WriteString(", ")
		_, _ = b.WriteString(extra)
	}
	return b.String()
}
func (s *PlatformSelector) Select(
	ctx context.Context,
	req PlatformSelectionInput,
) (selectionResult *FlowSelection, decision PlatformDecision, selectErr error) {
	if s != nil && s.ports.Available {
		effective := s.ports.Effective(ctx, req.GroupID)
		req.AdvancedSchedulerFeedbackConfig = NormalizeFeedbackConfig(effective.Feedback)
		req.StickyEscapeConfig = policy.NormalizeStickyEscape(effective.StickyEscape)
	}
	defer func() {
		// 统一给所有成功选择路径附带请求开始时捕获的反馈配置，避免回写时重新读取运行时设置。
		if selectionResult != nil && selectionResult.AdvancedSchedulerFeedback == nil && s != nil && s.ports.Available {
			feedback := NormalizeFeedbackConfig(req.AdvancedSchedulerFeedbackConfig)
			selectionResult.AdvancedSchedulerFeedback = &feedback
		}
	}()
	if s != nil && s.ports.Available && s.ports.GroupRequiresPrivacy(ctx, req.GroupID) {
		req.RequirePrivacySet = true
	}
	decision = PlatformDecision{}
	start := s.now()
	defer func() {
		decision.LatencyMs = s.now().Sub(start).Milliseconds()
		s.metrics.RecordSelect(decision)
	}()

	previousResponseID := strings.TrimSpace(req.PreviousResponseID)
	if previousResponseID != "" && routing.NormalizeOpenAICompatiblePlatform(req.Platform) == capability.PlatformOpenAI &&
		(!req.StickyWeighted || !req.PreviousResponseCanMove) {
		selection, err := s.ports.PreviousResponse(
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
			compatible, _ := s.ports.RequestCompatible(ctx, selection.Account, req)
			groupCompatible := !s.ports.HasGroupMetadata(selection.Account) || s.ports.MatchesGroup(selection.Account, req.GroupID)
			if !groupCompatible || !compatible || !s.ports.TransportCompatible(selection.Account, req.RequiredTransport) {
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
				_ = s.ports.BindSticky(ctx, req.GroupID, req.SessionHash, selection.Account.ID)
			}
			return selection, decision, nil
		}
	}

	if req.GuardianParentAccountID > 0 {
		parentReq := req
		parentReq.StickyAccountID = req.GuardianParentAccountID
		parentReq.PreserveStickyBinding = true
		selection, _, err := s.SelectBySessionHash(ctx, parentReq)
		if err != nil {
			return nil, decision, err
		}
		if selection != nil && selection.Account != nil {
			decision.Layer = openAIAccountScheduleLayerGuardianParent
			decision.StickySessionHit = true
			decision.SelectedAccountID = selection.Account.ID
			decision.SelectedAccountType = selection.Account.Type
			return selection, decision, nil
		}
	}

	if !req.StickyWeighted {
		selection, escapedSticky, err := s.SelectBySessionHash(ctx, req)
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

	selection, candidateCount, topK, loadSkew, err := s.SelectByLoadBalance(ctx, req)
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
func (s *PlatformSelector) SelectBySessionHash(
	ctx context.Context,
	req PlatformSelectionInput,
) (*FlowSelection, bool, error) {
	sessionHash := strings.TrimSpace(req.SessionHash)
	if sessionHash == "" || s == nil || !s.ports.Available || !s.ports.CacheAvailable {
		return nil, false, nil
	}

	clearBinding := func() {
		if !req.PreserveStickyBinding {
			_ = s.ports.DeleteSticky(ctx, req.GroupID, sessionHash)
		}
	}
	accountID := req.StickyAccountID
	if accountID <= 0 {
		var err error
		accountID, err = s.ports.GetSticky(ctx, req.GroupID, sessionHash)
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

	account, err := s.ports.GetSchedulable(ctx, accountID)
	if err != nil || account == nil {
		clearBinding()
		return nil, false, nil
	}
	if s.ports.ClearSticky(account, req.routingModel()) || account.Platform != routing.NormalizeOpenAICompatiblePlatform(req.Platform) || !s.ports.IsCompatible(account) || !s.ports.IsSchedulable(account) {
		clearBinding()
		return nil, false, nil
	}
	if !s.requestCompatible(ctx, account, req) {
		return nil, false, nil
	}
	if !s.ports.TransportCompatible(account, req.RequiredTransport) {
		clearBinding()
		return nil, false, nil
	}
	account = s.ports.Recheck(ctx, account, req.GroupID, req.Platform, req.routingModel(), req.RequireCompact, req.RequiredCapability)
	if account == nil || (s.ports.HasGroupMetadata(account) && !s.ports.MatchesGroup(account, req.GroupID)) || !s.ports.TransportCompatible(account, req.RequiredTransport) {
		clearBinding()
		return nil, false, nil
	}
	// 免费层软性门禁：粘性会话不得固定到已超额的免费 OAuth 账号。
	// 管理端额度查询与导入探测不经过此路径。
	if account != nil && len(s.ports.FreeQuota(ctx, []FlowAccount{*account})) == 0 {
		clearBinding()
		return nil, false, nil
	}
	// 团队与模型冷却：粘性会话不得固定到同团队中仍处于 429 窗口的关联账号。
	now := s.now()
	upstreamModel := s.ports.CanonicalModel(account, req.RequestedModel)
	if account != nil && s.ports.TeamLimited(account, upstreamModel, now) {
		clearBinding()
		return nil, false, nil
	}
	if account != nil && s.ports.ModelQuotaBlocked(account.ID, upstreamModel, now) {
		clearBinding()
		return nil, false, nil
	}
	escapeCfg := policy.NormalizeStickyEscape(req.StickyEscapeConfig)
	if reason, errorRate, ttft, shouldEscape := ShouldEscapeSticky(s.stats, accountID, escapeCfg); shouldEscape {
		s.diagnostics.event("info", "sticky_escape_triggered",
			"account_id", accountID,
			"reason", reason,
			"error_rate", errorRate,
			"ttft", ttft,
		)
		return nil, true, nil
	}
	result, acquireErr := s.ports.Acquire(ctx, accountID, account.Concurrency)
	if acquireErr == nil && result != nil && result.Acquired {
		if !req.PreserveStickyBinding {
			_ = s.ports.RefreshSticky(ctx, req.GroupID, sessionHash, s.ports.StickyTTL())
		}
		return &FlowSelection{
			Account:     account,
			Acquired:    true,
			ReleaseFunc: result.ReleaseFunc,
			AdvancedSchedulerFeedback: func() *policy.FeedbackConfig {
				feedback := NormalizeFeedbackConfig(req.AdvancedSchedulerFeedbackConfig)
				return &feedback
			}(),
		}, false, nil
	}

	cfg := s.ports.Options()
	// WaitPlan.MaxConcurrency 使用 Concurrency（非 EffectiveLoadFactor），因为 WaitPlan 控制的是 Redis 实际并发槽位等待。
	if s.concurrency != nil {
		if escapeCfg.Enabled && acquireErr == nil && result != nil && !result.Acquired {
			errorRate, ttft, _ := s.stats.Snapshot(accountID)
			s.diagnostics.event("info", "sticky_escape_triggered",
				"account_id", accountID,
				"reason", "concurrency_full",
				"error_rate", errorRate,
				"ttft", ttft,
			)
			return nil, true, nil
		}
		return &FlowSelection{
			Account: account,
			WaitPlan: &AccountWaitPlan{
				AccountID:      accountID,
				MaxConcurrency: account.Concurrency,
				Timeout:        cfg.StickySessionWaitTimeout,
				MaxWaiting:     cfg.StickySessionMaxWaiting,
			},
			AdvancedSchedulerFeedback: func() *policy.FeedbackConfig {
				feedback := NormalizeFeedbackConfig(req.AdvancedSchedulerFeedbackConfig)
				return &feedback
			}(),
		}, false, nil
	}
	return nil, false, nil
}
func (s *PlatformSelector) BuildPlan(
	ctx context.Context,
	req PlatformSelectionInput,
	filtered []*FlowAccount,
	loadMap map[int64]*AccountLoadInfo,
) PlatformLoadPlan {
	allCandidates := make([]PlatformCandidateScore, 0, len(filtered))
	for _, account := range filtered {
		loadInfo, loadKnown := loadMap[account.ID]
		if !loadKnown || loadInfo == nil {
			loadInfo = &AccountLoadInfo{AccountID: account.ID}
			loadKnown = false
		}
		errorRate, ttft, hasTTFT := 0.0, 0.0, false
		if s.stats != nil {
			errorRate, ttft, hasTTFT = s.stats.Snapshot(account.ID)
		}
		allCandidates = append(allCandidates, PlatformCandidateScore{
			Account:   account,
			LoadInfo:  loadInfo,
			LoadKnown: loadKnown,
			ErrorRate: errorRate,
			TTFT:      ttft,
			HasTTFT:   hasTTFT,
		})
	}

	candidates := allCandidates
	staleSnapshotCompactRetry := make([]PlatformCandidateScore, 0, len(allCandidates))
	if req.RequireCompact {
		candidates = make([]PlatformCandidateScore, 0, len(allCandidates))
		for _, candidate := range allCandidates {
			if !s.ports.CompactAllowed(candidate.Account) {
				staleSnapshotCompactRetry = append(staleSnapshotCompactRetry, candidate)
				continue
			}
			candidates = append(candidates, candidate)
		}
	}

	plan := PlatformLoadPlan{
		allCandidates:             allCandidates,
		candidates:                candidates,
		staleSnapshotCompactRetry: staleSnapshotCompactRetry,
		candidateCount:            len(candidates),
	}
	if len(candidates) == 0 {
		plan.selectionOrder = s.buildOpenAISelectionOrder(req, plan)
		return plan
	}

	accounts := make([]*FlowAccount, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Account != nil {
			accounts = append(accounts, candidate.Account)
		}
	}
	previousStickyAccountID := req.StickyPreviousAccountID
	if !req.PreviousResponseCanMove {
		previousStickyAccountID = 0
	}
	effectiveSettings := s.ports.Effective(ctx, req.GroupID)
	plan.candidates, plan.loadSkew = s.scoreCandidates(
		accounts,
		loadMap,
		s.stats,
		effectiveSettings.Weights,
		ScoreInput{
			GroupID:                 req.GroupID,
			SessionHash:             req.SessionHash,
			PreviousResponseID:      req.PreviousResponseID,
			RequestedModel:          req.RequestedModel,
			StickyAccountID:         req.StickyAccountID,
			StickyPreviousAccountID: previousStickyAccountID,
			StickyWeighted:          req.StickyWeighted,
			QuotaHeadroomFactor:     s.ports.QuotaHeadroom,
		},
		s.now(),
	)

	plan.topK = effectiveSettings.TopK
	if plan.topK > len(plan.candidates) {
		plan.topK = len(plan.candidates)
	}
	if plan.topK <= 0 {
		plan.topK = 1
	}

	plan.selectionOrder = s.buildOpenAISelectionOrder(req, plan)
	return plan
}
func (s *PlatformSelector) buildOpenAISelectionOrder(
	req PlatformSelectionInput,
	plan PlatformLoadPlan,
) []PlatformCandidateScore {
	buildSelectionOrder := func(pool []PlatformCandidateScore) []PlatformCandidateScore {
		if len(pool) == 0 || plan.topK <= 0 {
			return nil
		}
		groupTopK := plan.topK
		if groupTopK > len(pool) {
			groupTopK = len(pool)
		}
		ranked := platformTopK(pool, groupTopK)
		return s.weightedOrder(ranked, req)
	}

	if req.RequireCompact {
		selectionOrder := make([]PlatformCandidateScore, 0, len(plan.allCandidates))
		selectionOrder = append(selectionOrder, buildSelectionOrder(plan.candidates)...)
		if len(plan.staleSnapshotCompactRetry) > 0 && s.ports.SnapshotAvailable {
			selectionOrder = append(selectionOrder, sortOpenAICompactRetryCandidates(plan.staleSnapshotCompactRetry)...)
		}
		return selectionOrder
	}

	return buildSelectionOrder(plan.candidates)
}
func (s *PlatformSelector) SelectByLoadBalance(
	ctx context.Context,
	req PlatformSelectionInput,
) (*FlowSelection, int, int, float64, error) {
	budget := NewProbeBudget()
	accounts, err := s.ports.ListCandidates(ctx, req.GroupID, req.Platform)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	if len(accounts) == 0 {
		return nil, 0, 0, 0, s.ports.Unavailable(ctx, req.RequestedModel, req.routingModel(), false, PlatformFilterStats{}.Summary(""), accounts)
	}
	// 本地免费层软性门禁仅应用于 Grok 调度路径，不影响管理端探测。
	accounts = s.ports.FreeQuota(ctx, accounts)
	if len(accounts) == 0 {
		return nil, 0, 0, 0, s.ports.Unavailable(ctx, req.RequestedModel, req.routingModel(), false, PlatformFilterStats{}.Summary("grok_free_quota_soft_gate"))
	}
	// 团队与模型限流冷却：发生 429 的团队关联账号跳过热点模型。
	if req.Platform == capability.PlatformGrok {
		now := s.now()
		filtered := s.ports.FilterTeamLimited(accounts, req.RequestedModel, now)
		if len(filtered) == 0 && len(accounts) > 0 {
			return nil, 0, 0, 0, s.ports.Unavailable(ctx, req.RequestedModel, req.routingModel(), false, PlatformFilterStats{}.Summary("grok_team_model_rate_limit"))
		}
		if filtered != nil {
			accounts = filtered
		}
		// 按账号和模型执行免费额度软性阻断，其他模型仍可参与调度。
		modelFiltered := s.ports.FilterModelQuota(accounts, req.RequestedModel, now)
		if len(modelFiltered) == 0 && len(accounts) > 0 {
			return nil, 0, 0, 0, s.ports.Unavailable(ctx, req.RequestedModel, req.routingModel(), false, PlatformFilterStats{}.Summary("grok_model_quota_block"))
		}
		accounts = modelFiltered
	}

	filterStats := PlatformFilterStats{Pool: len(accounts)}
	filtered := make([]*FlowAccount, 0, len(accounts))
	loadReq := make([]AccountWithConcurrency, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if req.ExcludedIDs != nil {
			if _, excluded := req.ExcludedIDs[account.ID]; excluded {
				filterStats.Exclude("excluded")
				continue
			}
		}
		if !s.ports.IsSchedulable(account) {
			filterStats.Exclude("not_schedulable")
			continue
		}
		if account.Platform != routing.NormalizeOpenAICompatiblePlatform(req.Platform) || !s.ports.IsCompatible(account) {
			filterStats.Exclude("platform_mismatch")
			continue
		}
		if s.ports.RuntimeBlocked(account, req.routingModel()) {
			filterStats.Exclude("runtime_blocked")
			continue
		}
		// 隐私要求是当前分组的资格门，不修改共享账号状态，避免影响其它分组。
		if req.RequirePrivacySet && !account.IsPrivacySet() {
			filterStats.Exclude("privacy_not_set")
			continue
		}
		if compatible, reason := s.ports.RequestCompatible(ctx, account, req); !compatible {
			filterStats.Exclude(reason)
			continue
		}
		if !s.ports.TransportCompatible(account, req.RequiredTransport) {
			filterStats.Exclude("transport_incompatible")
			continue
		}
		filtered = append(filtered, account)
		loadReq = append(loadReq, AccountWithConcurrency{
			ID:             account.ID,
			MaxConcurrency: account.EffectiveLoadFactor(),
		})
	}
	if len(filtered) == 0 {
		return nil, 0, 0, 0, s.ports.Unavailable(ctx, req.RequestedModel, req.routingModel(), false, filterStats.Summary(""), accounts)
	}

	loadMap := map[int64]*AccountLoadInfo{}
	if s.concurrency != nil {
		if batchLoad, loadErr := s.concurrency.GetAccountsLoadBatch(ctx, loadReq); loadErr == nil {
			loadMap = batchLoad
		}
	}

	if req.SubscriptionPriority {
		subscriptionAccounts, regularAccounts := s.partitionSubscription(filtered)
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
				var result *FlowSelection
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
func (s *PlatformSelector) trySelectByLoadBalancePool(
	ctx context.Context,
	req PlatformSelectionInput,
	filtered []*FlowAccount,
	loadMap map[int64]*AccountLoadInfo,
	budget *ProbeBudget,
) platformPoolAttempt {
	plan := s.BuildPlan(ctx, req, filtered, loadMap)
	attempt := platformPoolAttempt{
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
	if req.RequireCompact && len(attempt.selectionOrder) == 0 && !s.ports.SnapshotAvailable {
		attempt.noCompactCandidates = true
		attempt.err = ErrNoAvailableCompactAccounts
		return attempt
	}
	if len(attempt.selectionOrder) == 0 {
		attempt.compactBlocked = req.RequireCompact && len(plan.allCandidates) > 0
		return attempt
	}

	result, compactBlocked, acquireErr := s.TryOrder(ctx, req, attempt.selectionOrder, budget)
	attempt.compactBlocked = compactBlocked
	if acquireErr != nil {
		attempt.err = acquireErr
		return attempt
	}
	if result != nil {
		attempt.result = result
		return attempt
	}

	if s.concurrency != nil && !budget.acquireExhausted() {
		loadReq := buildOpenAIAccountLoadRequest(filtered)
		if freshLoadMap, loadErr := s.concurrency.GetAccountsLoadBatchFresh(ctx, loadReq); loadErr == nil {
			freshPlan := s.BuildPlan(ctx, req, filtered, freshLoadMap)
			if len(freshPlan.selectionOrder) > 0 {
				freshResult, freshCompactBlocked, freshAcquireErr := s.TryOrder(ctx, req, freshPlan.selectionOrder, budget)
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
func (s *PlatformSelector) finishLoadBalanceSelectionFallback(
	ctx context.Context,
	req PlatformSelectionInput,
	attempt platformPoolAttempt,
	budget *ProbeBudget,
	filterStats PlatformFilterStats,
) (*FlowSelection, int, int, float64, error) {
	candidateCount := attempt.candidateCount
	topK := attempt.topK
	loadSkew := attempt.loadSkew

	if len(attempt.selectionOrder) == 0 {
		return nil, candidateCount, topK, loadSkew, s.ports.Unavailable(ctx, req.RequestedModel, req.routingModel(), attempt.compactBlocked, filterStats.Summary("selection_order_empty"))
	}

	cfg := s.ports.Options()
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
			if candidate.Account == nil {
				continue
			}
			if budget != nil && budget.limited {
				knownFull := candidate.LoadKnown && candidate.Account.Concurrency > 0 &&
					candidate.LoadInfo.CurrentConcurrency >= candidate.Account.Concurrency
				if budget.wasAttempted(candidate.Account.ID) != wantAttempted || knownFull != wantKnownFull {
					continue
				}
			}
			fresh := s.ports.Fresh(ctx, candidate.Account, req.Platform, req.routingModel(), false, req.RequiredCapability)
			if fresh == nil || !s.ports.TransportCompatible(fresh, req.RequiredTransport) || !s.requestCompatible(ctx, fresh, req) {
				continue
			}
			if !s.canRecheck(budget) {
				return nil, candidateCount, topK, loadSkew, s.ports.Unavailable(ctx, req.RequestedModel, req.routingModel(), compactBlocked, filterStats.Summary("selection_order_exhausted"))
			}
			fresh = s.ports.Recheck(ctx, fresh, req.GroupID, req.Platform, req.routingModel(), false, req.RequiredCapability)
			if fresh == nil || !s.ports.TransportCompatible(fresh, req.RequiredTransport) || !s.requestCompatible(ctx, fresh, req) {
				continue
			}
			if req.RequireCompact && !s.ports.CompactAllowed(fresh) {
				compactBlocked = true
				continue
			}
			return &FlowSelection{
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

	return nil, candidateCount, topK, loadSkew, s.ports.Unavailable(ctx, req.RequestedModel, req.routingModel(), compactBlocked, filterStats.Summary("selection_order_exhausted"))
}
func sortOpenAICompactRetryCandidates(pool []PlatformCandidateScore) []PlatformCandidateScore {
	if len(pool) == 0 {
		return nil
	}
	ordered := append([]PlatformCandidateScore(nil), pool...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Account.Priority != b.Account.Priority {
			return a.Account.Priority < b.Account.Priority
		}
		if a.LoadInfo.LoadRate != b.LoadInfo.LoadRate {
			return a.LoadInfo.LoadRate < b.LoadInfo.LoadRate
		}
		if a.LoadInfo.WaitingCount != b.LoadInfo.WaitingCount {
			return a.LoadInfo.WaitingCount < b.LoadInfo.WaitingCount
		}
		switch {
		case a.Account.LastUsedAt == nil && b.Account.LastUsedAt != nil:
			return true
		case a.Account.LastUsedAt != nil && b.Account.LastUsedAt == nil:
			return false
		case a.Account.LastUsedAt == nil && b.Account.LastUsedAt == nil:
			return false
		default:
			return a.Account.LastUsedAt.Before(*b.Account.LastUsedAt)
		}
	})
	return ordered
}
func buildOpenAIAccountLoadRequest(accounts []*FlowAccount) []AccountWithConcurrency {
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
