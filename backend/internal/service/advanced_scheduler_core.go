package service

import (
	heap "container/heap"
	context "context"
	policy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	fnv "hash/fnv"
	math "math"
	strconv "strconv"
	strings "strings"
	sync "sync"
	atomic "sync/atomic"
	time "time"
)

// advancedSchedulerNoSlotSelectionContextKey 标记只做账号选择、不会实际转发的调用。
// 可用性探测和 count_tokens 需要复用高级评分，但不能占用真实并发槽或注册会话数量。
type advancedSchedulerNoSlotSelectionContextKey struct{}

// advancedSchedulerPreserveStickyBindingContextKey 标记当前请求只逃逸一次，不覆盖原粘性绑定。
type advancedSchedulerPreserveStickyBindingContextKey struct{}

// withAdvancedSchedulerNoSlotSelection 为辅助选择入口保留完整硬过滤和评分语义，
// 同时跳过并发槽与会话数量的副作用。
func withAdvancedSchedulerNoSlotSelection(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, advancedSchedulerNoSlotSelectionContextKey{}, true)
}

func isAdvancedSchedulerNoSlotSelection(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(advancedSchedulerNoSlotSelectionContextKey{}).(bool)
	return enabled
}

func withAdvancedSchedulerPreserveStickyBinding(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, advancedSchedulerPreserveStickyBindingContextKey{}, true)
}

func shouldPreserveAdvancedSchedulerStickyBinding(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	preserve, _ := ctx.Value(advancedSchedulerPreserveStickyBindingContextKey{}).(bool)
	return preserve
}

// advancedAccountRuntimeStats 保存所有高级调度分组共享的运行时反馈。
// 账号没有反馈样本时，错误率按 0% 处理，其它可选信号仍使用中性值。
type advancedAccountRuntimeStats struct {
	accounts     sync.Map
	accountCount atomic.Int64
	switchCount  atomic.Int64
}

// advancedSchedulerFeedbackConfig 保存一次请求回写运行时反馈时使用的 EWMA 系数。
// 统计仍按账号共享，但系数由请求最终命中的分组决定。
type advancedSchedulerFeedbackConfig struct {
	errorRateAlpha float64
	ttftAlpha      float64
}

const (
	defaultAdvancedSchedulerErrorRateAlpha = 0.2
	defaultAdvancedSchedulerTTFTAlpha      = 0.2
)

func normalizeAdvancedSchedulerFeedbackConfig(value advancedSchedulerFeedbackConfig) advancedSchedulerFeedbackConfig {
	v := policy.NormalizeFeedback(policy.FeedbackConfig{ErrorRateAlpha: value.errorRateAlpha, TtftAlpha: value.ttftAlpha})
	return advancedSchedulerFeedbackConfig{errorRateAlpha: v.ErrorRateAlpha, ttftAlpha: v.TtftAlpha}
}

func (s *advancedAccountRuntimeStats) reportSwitch() {
	if s != nil {
		s.switchCount.Add(1)
	}
}

type advancedAccountRuntimeStat struct {
	errorRateEWMABits atomic.Uint64
	ttftEWMABits      atomic.Uint64
	// 诊断页需要区分“零错误率”和“尚无样本”，因此保留样本数与最近观测时间。
	errorSamples          atomic.Int64
	ttftSamples           atomic.Int64
	lastObservedUnixNano  atomic.Int64
	lastTTFTObservedNanos atomic.Int64
}

func newAdvancedAccountRuntimeStats() *advancedAccountRuntimeStats {
	return &advancedAccountRuntimeStats{}
}

func (s *advancedAccountRuntimeStats) loadOrCreate(accountID int64) *advancedAccountRuntimeStat {
	if value, ok := s.accounts.Load(accountID); ok {
		stat, _ := value.(*advancedAccountRuntimeStat)
		if stat != nil {
			return stat
		}
	}

	stat := &advancedAccountRuntimeStat{}
	// 未观测错误率按 0% 处理，后续样本从零基线更新 EWMA。
	stat.errorRateEWMABits.Store(math.Float64bits(0))
	stat.ttftEWMABits.Store(math.Float64bits(math.NaN()))
	actual, loaded := s.accounts.LoadOrStore(accountID, stat)
	if !loaded {
		s.accountCount.Add(1)
		return stat
	}
	existing, _ := actual.(*advancedAccountRuntimeStat)
	if existing != nil {
		return existing
	}
	return stat
}

func updateAdvancedSchedulerEWMA(target *atomic.Uint64, sample float64, alpha float64) {
	for {
		oldBits := target.Load()
		oldValue := math.Float64frombits(oldBits)
		newValue := alpha*sample + (1-alpha)*oldValue
		if target.CompareAndSwap(oldBits, math.Float64bits(newValue)) {
			return
		}
	}
}

func (s *advancedAccountRuntimeStats) report(accountID int64, success bool, firstTokenMs *int, feedback ...advancedSchedulerFeedbackConfig) {
	if s == nil || accountID <= 0 {
		return
	}
	feedbackConfig := advancedSchedulerFeedbackConfig{
		errorRateAlpha: defaultAdvancedSchedulerErrorRateAlpha,
		ttftAlpha:      defaultAdvancedSchedulerTTFTAlpha,
	}
	if len(feedback) > 0 {
		feedbackConfig = normalizeAdvancedSchedulerFeedbackConfig(feedback[0])
	}
	stat := s.loadOrCreate(accountID)

	errorSample := 1.0
	if success {
		errorSample = 0.0
	}
	updateAdvancedSchedulerEWMA(&stat.errorRateEWMABits, errorSample, feedbackConfig.errorRateAlpha)
	stat.errorSamples.Add(1)
	stat.lastObservedUnixNano.Store(time.Now().UnixNano())

	if firstTokenMs != nil && *firstTokenMs > 0 {
		ttft := float64(*firstTokenMs)
		ttftBits := math.Float64bits(ttft)
		for {
			oldBits := stat.ttftEWMABits.Load()
			oldValue := math.Float64frombits(oldBits)
			if math.IsNaN(oldValue) {
				if stat.ttftEWMABits.CompareAndSwap(oldBits, ttftBits) {
					stat.ttftSamples.Add(1)
					stat.lastTTFTObservedNanos.Store(time.Now().UnixNano())
					break
				}
				continue
			}
			newValue := feedbackConfig.ttftAlpha*ttft + (1-feedbackConfig.ttftAlpha)*oldValue
			if stat.ttftEWMABits.CompareAndSwap(oldBits, math.Float64bits(newValue)) {
				stat.ttftSamples.Add(1)
				stat.lastTTFTObservedNanos.Store(time.Now().UnixNano())
				break
			}
		}
	}
}

// advancedAccountRuntimeFeedbackSnapshot 是诊断与评分共享的只读运行时反馈快照。
// 不暴露任何请求内容，仅包含经 EWMA 聚合后的健康指标及其观测新鲜度。
type advancedAccountRuntimeFeedbackSnapshot struct {
	HasFeedback    bool
	ErrorRate      float64
	ErrorSamples   int64
	TTFT           float64
	HasTTFT        bool
	TTFTSamples    int64
	LastObservedAt *time.Time
	LastTTFTAt     *time.Time
}

func (s *advancedAccountRuntimeStats) feedbackSnapshot(accountID int64) advancedAccountRuntimeFeedbackSnapshot {
	if s == nil || accountID <= 0 {
		return advancedAccountRuntimeFeedbackSnapshot{}
	}
	value, ok := s.accounts.Load(accountID)
	if !ok {
		return advancedAccountRuntimeFeedbackSnapshot{}
	}
	stat, _ := value.(*advancedAccountRuntimeStat)
	if stat == nil {
		return advancedAccountRuntimeFeedbackSnapshot{}
	}

	snapshot := advancedAccountRuntimeFeedbackSnapshot{
		HasFeedback:  true,
		ErrorRate:    clamp01(math.Float64frombits(stat.errorRateEWMABits.Load())),
		ErrorSamples: stat.errorSamples.Load(),
	}
	if observedAt := stat.lastObservedUnixNano.Load(); observedAt > 0 {
		value := time.Unix(0, observedAt).UTC()
		snapshot.LastObservedAt = &value
	}
	if ttftValue := math.Float64frombits(stat.ttftEWMABits.Load()); !math.IsNaN(ttftValue) {
		snapshot.TTFT = ttftValue
		snapshot.HasTTFT = true
		snapshot.TTFTSamples = stat.ttftSamples.Load()
		if observedAt := stat.lastTTFTObservedNanos.Load(); observedAt > 0 {
			value := time.Unix(0, observedAt).UTC()
			snapshot.LastTTFTAt = &value
		}
	}
	return snapshot
}

func (s *advancedAccountRuntimeStats) snapshot(accountID int64) (errorRate float64, ttft float64, hasTTFT bool) {
	if s == nil || accountID <= 0 {
		return 0, 0, false
	}
	value, ok := s.accounts.Load(accountID)
	if !ok {
		return 0, 0, false
	}
	stat, _ := value.(*advancedAccountRuntimeStat)
	if stat == nil {
		return 0, 0, false
	}
	errorRate = clamp01(math.Float64frombits(stat.errorRateEWMABits.Load()))
	ttftValue := math.Float64frombits(stat.ttftEWMABits.Load())
	if math.IsNaN(ttftValue) {
		return errorRate, 0, false
	}
	return errorRate, ttftValue, true
}

func (s *advancedAccountRuntimeStats) size() int {
	if s == nil {
		return 0
	}
	return int(s.accountCount.Load())
}

// advancedSchedulerCandidateScore 是完成平台硬过滤后的通用高级调度候选。
type advancedSchedulerCandidateScore struct {
	account            *Account
	loadInfo           *AccountLoadInfo
	loadKnown          bool
	score              float64
	baseScore          float64
	stickyBonus        float64
	previousBonus      float64
	sessionStickyBonus float64
	priority           int
	errorRate          float64
	ttft               float64
	hasTTFT            bool
	hasFeedback        bool
	feedback           advancedAccountRuntimeFeedbackSnapshot
	factors            advancedSchedulerCandidateFactors
}

// advancedSchedulerCandidateFactors 保留评分核心实际使用的归一化因子。
// 它只服务于诊断，不参与候选排序之外的业务决策。
type advancedSchedulerCandidateFactors struct {
	Priority      float64
	Load          float64
	Queue         float64
	ErrorRate     float64
	TTFT          float64
	Reset         float64
	QuotaHeadroom float64
}

// advancedSchedulerScoreRanges 记录本次候选池的归一化范围，供诊断接口直接解释公式。
type advancedSchedulerScoreRanges struct {
	MinPriority       int
	MaxPriority       int
	MaxWaiting        int
	MinTTFT           float64
	MaxTTFT           float64
	HasTTFTSample     bool
	MinResetRemaining float64
	MaxResetRemaining float64
	HasResetSample    bool
}

type advancedSchedulerCandidateHeap []advancedSchedulerCandidateScore

func (h advancedSchedulerCandidateHeap) Len() int {
	return len(h)
}

func (h advancedSchedulerCandidateHeap) Less(i, j int) bool {
	// 最小堆根节点保存最差候选，便于在线维护 Top-K。
	return isAdvancedSchedulerCandidateBetter(h[j], h[i])
}

func (h advancedSchedulerCandidateHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *advancedSchedulerCandidateHeap) Push(x any) {
	candidate, ok := x.(advancedSchedulerCandidateScore)
	if !ok {
		panic("advancedSchedulerCandidateHeap: invalid element type")
	}
	*h = append(*h, candidate)
}

func (h *advancedSchedulerCandidateHeap) Pop() any {
	old := *h
	n := len(old)
	last := old[n-1]
	*h = old[:n-1]
	return last
}

func isAdvancedSchedulerCandidateBetter(left, right advancedSchedulerCandidateScore) bool {
	if left.account == nil {
		return false
	}
	if right.account == nil {
		return true
	}
	if left.score != right.score {
		return left.score > right.score
	}
	if left.account.Priority != right.account.Priority {
		return left.account.Priority < right.account.Priority
	}
	// 负载与等待已经进入评分；同分时只用实体 ID 决胜，保持严格且可传递的稳定全序。
	return left.account.ID < right.account.ID
}

func selectTopKAdvancedSchedulerCandidates(candidates []advancedSchedulerCandidateScore, topK int) []advancedSchedulerCandidateScore {
	if len(candidates) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 1
	}
	if topK >= len(candidates) {
		ranked := append([]advancedSchedulerCandidateScore(nil), candidates...)
		sortAdvancedSchedulerCandidates(ranked)
		return ranked
	}

	best := make(advancedSchedulerCandidateHeap, 0, topK)
	for _, candidate := range candidates {
		if len(best) < topK {
			heap.Push(&best, candidate)
			continue
		}
		if isAdvancedSchedulerCandidateBetter(candidate, best[0]) {
			best[0] = candidate
			heap.Fix(&best, 0)
		}
	}

	ranked := make([]advancedSchedulerCandidateScore, len(best))
	copy(ranked, best)
	sortAdvancedSchedulerCandidates(ranked)
	return ranked
}

func sortAdvancedSchedulerCandidates(candidates []advancedSchedulerCandidateScore) {
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && isAdvancedSchedulerCandidateBetter(candidates[j], candidates[j-1]); j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}
}

// advancedSchedulerSelectionInput 只携带平台无关的可选调度信号。
type advancedSchedulerSelectionInput struct {
	GroupID                 *int64
	SessionHash             string
	PreviousResponseID      string
	RequestedModel          string
	StickyAccountID         int64
	StickyPreviousAccountID int64
	StickyWeighted          bool
	TopK                    int
	QuotaHeadroomFactor     func(*Account, time.Time) float64
}

// scoreAdvancedSchedulerCandidates 对硬过滤后的候选执行通用评分，并返回负载偏斜。
// @project-doc docs/architecture/account_scheduling_and_cache.md#advanced_scheduler_selection
func scoreAdvancedSchedulerCandidates(
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
	stats *advancedAccountRuntimeStats,
	weights GatewayAdvancedSchedulerScoreWeightsView,
	input advancedSchedulerSelectionInput,
	now time.Time,
) ([]advancedSchedulerCandidateScore, float64) {
	candidates, skew, _ := scoreAdvancedSchedulerCandidatesWithRanges(accounts, loadMap, stats, weights, input, now)
	return candidates, skew
}

// scoreAdvancedSchedulerCandidatesWithRanges 与实际评分共用同一条计算路径，
// 额外返回归一化范围，供管理员诊断界面逐项解释结果。
func scoreAdvancedSchedulerCandidatesWithRanges(
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
	stats *advancedAccountRuntimeStats,
	weights GatewayAdvancedSchedulerScoreWeightsView,
	input advancedSchedulerSelectionInput,
	now time.Time,
) ([]advancedSchedulerCandidateScore, float64, advancedSchedulerScoreRanges) {
	candidates := make([]advancedSchedulerCandidateScore, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		loadInfo, loadKnown := loadMap[account.ID]
		if !loadKnown || loadInfo == nil {
			loadInfo = &AccountLoadInfo{AccountID: account.ID}
			loadKnown = false
		}
		feedback := advancedAccountRuntimeFeedbackSnapshot{}
		if stats != nil {
			feedback = stats.feedbackSnapshot(account.ID)
		}
		candidates = append(candidates, advancedSchedulerCandidateScore{
			account:     account,
			loadInfo:    loadInfo,
			loadKnown:   loadKnown,
			priority:    account.Priority,
			errorRate:   feedback.ErrorRate,
			ttft:        feedback.TTFT,
			hasTTFT:     feedback.HasTTFT,
			hasFeedback: feedback.HasFeedback,
			feedback:    feedback,
		})
	}
	if len(candidates) == 0 {
		return nil, 0, advancedSchedulerScoreRanges{}
	}

	minPriority, maxPriority := candidates[0].priority, candidates[0].priority
	maxWaiting := 1
	loadRateSum := 0.0
	loadRateSumSquares := 0.0
	knownLoadCount := 0
	minTTFT, maxTTFT := 0.0, 0.0
	hasTTFTSample := false
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.priority < minPriority {
			minPriority = candidate.priority
		}
		if candidate.priority > maxPriority {
			maxPriority = candidate.priority
		}
		if candidate.loadKnown && candidate.loadInfo.WaitingCount > maxWaiting {
			maxWaiting = candidate.loadInfo.WaitingCount
		}
		if candidate.hasTTFT && candidate.ttft > 0 {
			if !hasTTFTSample {
				minTTFT, maxTTFT, hasTTFTSample = candidate.ttft, candidate.ttft, true
			} else {
				if candidate.ttft < minTTFT {
					minTTFT = candidate.ttft
				}
				if candidate.ttft > maxTTFT {
					maxTTFT = candidate.ttft
				}
			}
		}
		if candidate.loadKnown {
			loadRate := float64(candidate.loadInfo.LoadRate)
			loadRateSum += loadRate
			loadRateSumSquares += loadRate * loadRate
			knownLoadCount++
		}
	}

	minResetRemaining, maxResetRemaining := 0.0, 0.0
	hasResetSample := false
	if weights.Reset > 0 {
		for _, candidate := range candidates {
			end := candidate.account.SessionWindowEnd
			if end == nil || !now.Before(*end) {
				continue
			}
			remaining := end.Sub(now).Seconds()
			if !hasResetSample {
				minResetRemaining, maxResetRemaining, hasResetSample = remaining, remaining, true
				continue
			}
			if remaining < minResetRemaining {
				minResetRemaining = remaining
			}
			if remaining > maxResetRemaining {
				maxResetRemaining = remaining
			}
		}
	}

	ranges := advancedSchedulerScoreRanges{
		MinPriority:       minPriority,
		MaxPriority:       maxPriority,
		MaxWaiting:        maxWaiting,
		MinTTFT:           minTTFT,
		MaxTTFT:           maxTTFT,
		HasTTFTSample:     hasTTFTSample,
		MinResetRemaining: minResetRemaining,
		MaxResetRemaining: maxResetRemaining,
		HasResetSample:    hasResetSample,
	}

	quotaFactor := input.QuotaHeadroomFactor
	if quotaFactor == nil {
		quotaFactor = func(*Account, time.Time) float64 { return 0.5 }
	}
	for i := range candidates {
		item := &candidates[i]
		priorityFactor := 1.0
		if maxPriority > minPriority {
			priorityFactor = 1 - float64(item.priority-minPriority)/float64(maxPriority-minPriority)
		}
		loadFactor := 0.5
		queueFactor := 0.5
		if item.loadKnown {
			loadFactor = 1 - clamp01(float64(item.loadInfo.LoadRate)/100.0)
			queueFactor = 1 - clamp01(float64(item.loadInfo.WaitingCount)/float64(maxWaiting))
		}
		// 没有错误反馈等价于 0% 错误率，因此获得完整健康度分值。
		errorFactor := 1.0
		if item.hasFeedback {
			errorFactor = 1 - clamp01(item.errorRate)
		}
		ttftFactor := 0.5
		if item.hasTTFT && hasTTFTSample && maxTTFT > minTTFT {
			ttftFactor = 1 - clamp01((item.ttft-minTTFT)/(maxTTFT-minTTFT))
		}
		resetFactor := 0.5
		if weights.Reset > 0 && hasResetSample {
			if end := item.account.SessionWindowEnd; end != nil && now.Before(*end) {
				if maxResetRemaining > minResetRemaining {
					resetFactor = 1 - clamp01((end.Sub(now).Seconds()-minResetRemaining)/(maxResetRemaining-minResetRemaining))
				} else {
					resetFactor = 1
				}
			}
		}
		quotaHeadroomFactor := 0.5
		if weights.QuotaHeadroom > 0 {
			quotaHeadroomFactor = clamp01(quotaFactor(item.account, now))
		}
		item.factors = advancedSchedulerCandidateFactors{
			Priority:      priorityFactor,
			Load:          loadFactor,
			Queue:         queueFactor,
			ErrorRate:     errorFactor,
			TTFT:          ttftFactor,
			Reset:         resetFactor,
			QuotaHeadroom: quotaHeadroomFactor,
		}
		item.baseScore = weights.Priority*priorityFactor +
			weights.Load*loadFactor +
			weights.Queue*queueFactor +
			weights.ErrorRate*errorFactor +
			weights.TTFT*ttftFactor +
			weights.Reset*resetFactor +
			weights.QuotaHeadroom*quotaHeadroomFactor
		item.score = item.baseScore
		if input.StickyWeighted {
			if input.StickyPreviousAccountID > 0 && item.account.ID == input.StickyPreviousAccountID {
				item.previousBonus = weights.Previous
				item.stickyBonus += item.previousBonus
			}
			if input.StickyAccountID > 0 && item.account.ID == input.StickyAccountID {
				item.sessionStickyBonus = weights.SessionSticky
				item.stickyBonus += item.sessionStickyBonus
			}
		}
		item.score += item.stickyBonus
	}

	return candidates, calcLoadSkewByMoments(loadRateSum, loadRateSumSquares, knownLoadCount), ranges
}

type advancedSchedulerRNG struct {
	state uint64
}

func newAdvancedSchedulerRNG(seed uint64) advancedSchedulerRNG {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return advancedSchedulerRNG{state: seed}
}

func (r *advancedSchedulerRNG) nextUint64() uint64 {
	// xorshift64*
	x := r.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.state = x
	return x * 2685821657736338717
}

func (r *advancedSchedulerRNG) nextFloat64() float64 {
	return float64(r.nextUint64()>>11) / (1 << 53)
}

func deriveAdvancedSchedulerSelectionSeed(input advancedSchedulerSelectionInput) uint64 {
	hasher := fnv.New64a()
	writeValue := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		_, _ = hasher.Write([]byte(trimmed))
		_, _ = hasher.Write([]byte{0})
	}
	writeValue(input.SessionHash)
	writeValue(input.PreviousResponseID)
	writeValue(input.RequestedModel)
	if input.GroupID != nil {
		_, _ = hasher.Write([]byte(strconv.FormatInt(*input.GroupID, 10)))
	}
	seed := hasher.Sum64()
	if strings.TrimSpace(input.SessionHash) == "" && strings.TrimSpace(input.PreviousResponseID) == "" {
		seed ^= uint64(time.Now().UnixNano())
	}
	if seed == 0 {
		seed = uint64(time.Now().UnixNano()) ^ 0x9e3779b97f4a7c15
	}
	return seed
}

func buildAdvancedWeightedSelectionOrder(candidates []advancedSchedulerCandidateScore, input advancedSchedulerSelectionInput) []advancedSchedulerCandidateScore {
	if len(candidates) <= 1 {
		return append([]advancedSchedulerCandidateScore(nil), candidates...)
	}

	pool := append([]advancedSchedulerCandidateScore(nil), candidates...)
	weights := make([]float64, len(pool))
	minScore := pool[0].score
	for i := 1; i < len(pool); i++ {
		if pool[i].score < minScore {
			minScore = pool[i].score
		}
	}
	for i := range pool {
		// 将 Top-K 分值平移到正区间，避免单个账号长期垄断。
		weight := (pool[i].score - minScore) + 1.0
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
			weight = 1.0
		}
		weights[i] = weight
	}

	order := make([]advancedSchedulerCandidateScore, 0, len(pool))
	rng := newAdvancedSchedulerRNG(deriveAdvancedSchedulerSelectionSeed(input))
	for len(pool) > 0 {
		total := 0.0
		for _, weight := range weights {
			total += weight
		}
		selectedIdx := 0
		if total > 0 {
			random := rng.nextFloat64() * total
			accumulated := 0.0
			for i, weight := range weights {
				accumulated += weight
				if random <= accumulated {
					selectedIdx = i
					break
				}
			}
		} else {
			selectedIdx = int(rng.nextUint64() % uint64(len(pool)))
		}
		order = append(order, pool[selectedIdx])
		pool = append(pool[:selectedIdx], pool[selectedIdx+1:]...)
		weights = append(weights[:selectedIdx], weights[selectedIdx+1:]...)
	}
	return order
}

func buildAdvancedSchedulerSelectionOrder(candidates []advancedSchedulerCandidateScore, input advancedSchedulerSelectionInput) []advancedSchedulerCandidateScore {
	if len(candidates) == 0 {
		return nil
	}
	topK := input.TopK
	if topK <= 0 {
		topK = 1
	}
	ranked := selectTopKAdvancedSchedulerCandidates(candidates, topK)
	return buildAdvancedWeightedSelectionOrder(ranked, input)
}

// AdvancedAccountSchedulerScoreSnapshot 是管理端和高级调度器共用的评分展示结构。
type AdvancedAccountSchedulerScoreSnapshot struct {
	BaseScore             float64
	StickyScore           float64
	StickyScoreInfinity   bool
	StickyWeightedEnabled bool
}

func buildAdvancedAccountSchedulerScoreSnapshot(
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
	stats *advancedAccountRuntimeStats,
	group *Group,
	weights GatewayAdvancedSchedulerScoreWeightsView,
	stickyWeightedEnabled bool,
	quotaHeadroomFactor func(*Account, time.Time) float64,
) map[int64]AdvancedAccountSchedulerScoreSnapshot {
	candidates, _ := scoreAdvancedSchedulerCandidates(accounts, loadMap, stats, weights, advancedSchedulerSelectionInput{
		QuotaHeadroomFactor: quotaHeadroomFactor,
	}, time.Now())
	if len(candidates) == 0 {
		return nil
	}
	result := make(map[int64]AdvancedAccountSchedulerScoreSnapshot, len(candidates))
	for _, candidate := range candidates {
		score := AdvancedAccountSchedulerScoreSnapshot{
			BaseScore:             candidate.score,
			StickyWeightedEnabled: stickyWeightedEnabled,
			StickyScoreInfinity:   !stickyWeightedEnabled,
		}
		if stickyWeightedEnabled {
			// 分组平台定义请求语义；无分组兼容入口按账号平台判断。
			platform := candidate.account.Platform
			if group != nil && strings.TrimSpace(group.Platform) != "" {
				platform = group.Platform
			}
			score.StickyScore = candidate.score + weights.SessionSticky
			if platform == PlatformOpenAI {
				score.StickyScore += weights.Previous
			}
		}
		result[candidate.account.ID] = score
	}
	return result
}
