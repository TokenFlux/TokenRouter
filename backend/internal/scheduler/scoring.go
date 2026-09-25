package scheduler

import (
	"container/heap"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// RuntimeStats 保存所有高级调度分组共享的运行时反馈。
// 账号没有反馈样本时，错误率按 0% 处理，其它可选信号仍使用中性值。
type RuntimeStats struct {
	now          func() time.Time
	accounts     sync.Map
	accountCount atomic.Int64
	switchCount  atomic.Int64
}

// FeedbackConfig 保存一次请求回写运行时反馈时使用的 EWMA 系数。
// 统计仍按账号共享，但系数由请求最终命中的分组决定。
type FeedbackConfig = policy.FeedbackConfig

const (
	DefaultErrorRateAlpha = 0.2
	DefaultTTFTAlpha      = 0.2
)

func NormalizeFeedbackConfig(value FeedbackConfig) FeedbackConfig {
	return policy.NormalizeFeedback(value)
}

func (s *RuntimeStats) ReportSwitch() {
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

func NewRuntimeStats(now func() time.Time) *RuntimeStats {
	return &RuntimeStats{now: now}
}

func (s *RuntimeStats) loadOrCreate(accountID int64) *advancedAccountRuntimeStat {
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

func (s *RuntimeStats) Report(accountID int64, success bool, firstTokenMs *int, feedback ...FeedbackConfig) {
	if s == nil || accountID <= 0 {
		return
	}
	feedbackConfig := FeedbackConfig{
		ErrorRateAlpha: DefaultErrorRateAlpha,
		TtftAlpha:      DefaultTTFTAlpha,
	}
	if len(feedback) > 0 {
		feedbackConfig = NormalizeFeedbackConfig(feedback[0])
	}
	stat := s.loadOrCreate(accountID)

	errorSample := 1.0
	if success {
		errorSample = 0.0
	}
	updateAdvancedSchedulerEWMA(&stat.errorRateEWMABits, errorSample, feedbackConfig.ErrorRateAlpha)
	stat.errorSamples.Add(1)
	stat.lastObservedUnixNano.Store(s.nowTime().UnixNano())

	if firstTokenMs != nil && *firstTokenMs > 0 {
		ttft := float64(*firstTokenMs)
		ttftBits := math.Float64bits(ttft)
		for {
			oldBits := stat.ttftEWMABits.Load()
			oldValue := math.Float64frombits(oldBits)
			if math.IsNaN(oldValue) {
				if stat.ttftEWMABits.CompareAndSwap(oldBits, ttftBits) {
					stat.ttftSamples.Add(1)
					stat.lastTTFTObservedNanos.Store(s.nowTime().UnixNano())
					break
				}
				continue
			}
			newValue := feedbackConfig.TtftAlpha*ttft + (1-feedbackConfig.TtftAlpha)*oldValue
			if stat.ttftEWMABits.CompareAndSwap(oldBits, math.Float64bits(newValue)) {
				stat.ttftSamples.Add(1)
				stat.lastTTFTObservedNanos.Store(s.nowTime().UnixNano())
				break
			}
		}
	}
}

// FeedbackSnapshot 是诊断与评分共享的只读运行时反馈快照。
// 不暴露任何请求内容，仅包含经 EWMA 聚合后的健康指标及其观测新鲜度。
type FeedbackSnapshot struct {
	HasFeedback    bool
	ErrorRate      float64
	ErrorSamples   int64
	TTFT           float64
	HasTTFT        bool
	TTFTSamples    int64
	LastObservedAt *time.Time
	LastTTFTAt     *time.Time
}

func (s *RuntimeStats) FeedbackSnapshot(accountID int64) FeedbackSnapshot {
	if s == nil || accountID <= 0 {
		return FeedbackSnapshot{}
	}
	value, ok := s.accounts.Load(accountID)
	if !ok {
		return FeedbackSnapshot{}
	}
	stat, _ := value.(*advancedAccountRuntimeStat)
	if stat == nil {
		return FeedbackSnapshot{}
	}

	Snapshot := FeedbackSnapshot{
		HasFeedback:  true,
		ErrorRate:    Clamp01(math.Float64frombits(stat.errorRateEWMABits.Load())),
		ErrorSamples: stat.errorSamples.Load(),
	}
	if observedAt := stat.lastObservedUnixNano.Load(); observedAt > 0 {
		value := time.Unix(0, observedAt).UTC()
		Snapshot.LastObservedAt = &value
	}
	if ttftValue := math.Float64frombits(stat.ttftEWMABits.Load()); !math.IsNaN(ttftValue) {
		Snapshot.TTFT = ttftValue
		Snapshot.HasTTFT = true
		Snapshot.TTFTSamples = stat.ttftSamples.Load()
		if observedAt := stat.lastTTFTObservedNanos.Load(); observedAt > 0 {
			value := time.Unix(0, observedAt).UTC()
			Snapshot.LastTTFTAt = &value
		}
	}
	return Snapshot
}

func (s *RuntimeStats) Snapshot(accountID int64) (errorRate float64, ttft float64, hasTTFT bool) {
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
	errorRate = Clamp01(math.Float64frombits(stat.errorRateEWMABits.Load()))
	ttftValue := math.Float64frombits(stat.ttftEWMABits.Load())
	if math.IsNaN(ttftValue) {
		return errorRate, 0, false
	}
	return errorRate, ttftValue, true
}

func (s *RuntimeStats) Size() int {
	if s == nil {
		return 0
	}
	return int(s.accountCount.Load())
}

// CandidateScore 是完成平台硬过滤后的通用高级调度候选。
type CandidateScore struct {
	Account            *ScoreAccount
	LoadInfo           *AccountLoadInfo
	LoadKnown          bool
	Score              float64
	BaseScore          float64
	StickyBonus        float64
	PreviousBonus      float64
	SessionStickyBonus float64
	Priority           int
	ErrorRate          float64
	TTFT               float64
	HasTTFT            bool
	HasFeedback        bool
	Feedback           FeedbackSnapshot
	Factors            CandidateFactors
}

// CandidateFactors 保留评分核心实际使用的归一化因子。
// 它只服务于诊断，不参与候选排序之外的业务决策。
type CandidateFactors struct {
	Priority      float64
	Load          float64
	Queue         float64
	ErrorRate     float64
	TTFT          float64
	Reset         float64
	QuotaHeadroom float64
}

// ScoreRanges 记录本次候选池的归一化范围，供诊断接口直接解释公式。
type ScoreRanges struct {
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

type candidateHeap []CandidateScore

func (h candidateHeap) Len() int {
	return len(h)
}

func (h candidateHeap) Less(i, j int) bool {
	// 最小堆根节点保存最差候选，便于在线维护 Top-K。
	return CandidateBetter(h[j], h[i])
}

func (h candidateHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *candidateHeap) Push(x any) {
	candidate, ok := x.(CandidateScore)
	if !ok {
		panic("candidateHeap: invalid element type")
	}
	*h = append(*h, candidate)
}

func (h *candidateHeap) Pop() any {
	old := *h
	n := len(old)
	last := old[n-1]
	*h = old[:n-1]
	return last
}

func CandidateBetter(left, right CandidateScore) bool {
	if left.Account == nil {
		return false
	}
	if right.Account == nil {
		return true
	}
	if left.Score != right.Score {
		return left.Score > right.Score
	}
	if left.Account.Priority != right.Account.Priority {
		return left.Account.Priority < right.Account.Priority
	}
	// 负载与等待已经进入评分；同分时只用实体 ID 决胜，保持严格且可传递的稳定全序。
	return left.Account.ID < right.Account.ID
}

func SelectTopK(candidates []CandidateScore, topK int) []CandidateScore {
	if len(candidates) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 1
	}
	if topK >= len(candidates) {
		ranked := append([]CandidateScore(nil), candidates...)
		SortCandidates(ranked)
		return ranked
	}

	best := make(candidateHeap, 0, topK)
	for _, candidate := range candidates {
		if len(best) < topK {
			heap.Push(&best, candidate)
			continue
		}
		if CandidateBetter(candidate, best[0]) {
			best[0] = candidate
			heap.Fix(&best, 0)
		}
	}

	ranked := make([]CandidateScore, len(best))
	copy(ranked, best)
	SortCandidates(ranked)
	return ranked
}

func SortCandidates(candidates []CandidateScore) {
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && CandidateBetter(candidates[j], candidates[j-1]); j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}
}

// ScoreInput 只携带平台无关的可选调度信号。
type ScoreInput struct {
	Now                     func() time.Time
	GroupID                 *int64
	SessionHash             string
	PreviousResponseID      string
	RequestedModel          string
	StickyAccountID         int64
	StickyPreviousAccountID int64
	StickyWeighted          bool
	TopK                    int
	QuotaHeadroomFactor     func(*ScoreAccount, time.Time) float64
}

// ScoreCandidates 对硬过滤后的候选执行通用评分，并返回负载偏斜。
// @project-doc docs/architecture/account_scheduling_and_cache.md#advanced_scheduler_selection
func ScoreCandidates(
	accounts []*ScoreAccount,
	loadMap map[int64]*AccountLoadInfo,
	stats *RuntimeStats,
	weights policy.ScoreWeights,
	input ScoreInput,
	now time.Time,
) ([]CandidateScore, float64) {
	candidates, skew, _ := ScoreCandidatesWithRanges(accounts, loadMap, stats, weights, input, now)
	return candidates, skew
}

// ScoreCandidatesWithRanges 与实际评分共用同一条计算路径，
// 额外返回归一化范围，供管理员诊断界面逐项解释结果。
func ScoreCandidatesWithRanges(
	accounts []*ScoreAccount,
	loadMap map[int64]*AccountLoadInfo,
	stats *RuntimeStats,
	weights policy.ScoreWeights,
	input ScoreInput,
	now time.Time,
) ([]CandidateScore, float64, ScoreRanges) {
	candidates := make([]CandidateScore, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		loadInfo, loadKnown := loadMap[account.ID]
		if !loadKnown || loadInfo == nil {
			loadInfo = &AccountLoadInfo{AccountID: account.ID}
			loadKnown = false
		}
		feedback := FeedbackSnapshot{}
		if stats != nil {
			feedback = stats.FeedbackSnapshot(account.ID)
		}
		candidates = append(candidates, CandidateScore{
			Account:     account,
			LoadInfo:    loadInfo,
			LoadKnown:   loadKnown,
			Priority:    account.Priority,
			ErrorRate:   feedback.ErrorRate,
			TTFT:        feedback.TTFT,
			HasTTFT:     feedback.HasTTFT,
			HasFeedback: feedback.HasFeedback,
			Feedback:    feedback,
		})
	}
	if len(candidates) == 0 {
		return nil, 0, ScoreRanges{}
	}

	minPriority, maxPriority := candidates[0].Priority, candidates[0].Priority
	maxWaiting := 1
	loadRateSum := 0.0
	loadRateSumSquares := 0.0
	knownLoadCount := 0
	minTTFT, maxTTFT := 0.0, 0.0
	hasTTFTSample := false
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.Priority < minPriority {
			minPriority = candidate.Priority
		}
		if candidate.Priority > maxPriority {
			maxPriority = candidate.Priority
		}
		if candidate.LoadKnown && candidate.LoadInfo.WaitingCount > maxWaiting {
			maxWaiting = candidate.LoadInfo.WaitingCount
		}
		if candidate.HasTTFT && candidate.TTFT > 0 {
			if !hasTTFTSample {
				minTTFT, maxTTFT, hasTTFTSample = candidate.TTFT, candidate.TTFT, true
			} else {
				if candidate.TTFT < minTTFT {
					minTTFT = candidate.TTFT
				}
				if candidate.TTFT > maxTTFT {
					maxTTFT = candidate.TTFT
				}
			}
		}
		if candidate.LoadKnown {
			loadRate := float64(candidate.LoadInfo.LoadRate)
			loadRateSum += loadRate
			loadRateSumSquares += loadRate * loadRate
			knownLoadCount++
		}
	}

	minResetRemaining, maxResetRemaining := 0.0, 0.0
	hasResetSample := false
	if weights.Reset > 0 {
		for _, candidate := range candidates {
			end := candidate.Account.SessionWindowEnd
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

	ranges := ScoreRanges{
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
		quotaFactor = func(*ScoreAccount, time.Time) float64 { return 0.5 }
	}
	for i := range candidates {
		item := &candidates[i]
		priorityFactor := 1.0
		if maxPriority > minPriority {
			priorityFactor = 1 - float64(item.Priority-minPriority)/float64(maxPriority-minPriority)
		}
		loadFactor := 0.5
		queueFactor := 0.5
		if item.LoadKnown {
			loadFactor = 1 - Clamp01(float64(item.LoadInfo.LoadRate)/100.0)
			queueFactor = 1 - Clamp01(float64(item.LoadInfo.WaitingCount)/float64(maxWaiting))
		}
		// 没有错误反馈等价于 0% 错误率，因此获得完整健康度分值。
		errorFactor := 1.0
		if item.HasFeedback {
			errorFactor = 1 - Clamp01(item.ErrorRate)
		}
		ttftFactor := 0.5
		if item.HasTTFT && hasTTFTSample && maxTTFT > minTTFT {
			ttftFactor = 1 - Clamp01((item.TTFT-minTTFT)/(maxTTFT-minTTFT))
		}
		resetFactor := 0.5
		if weights.Reset > 0 && hasResetSample {
			if end := item.Account.SessionWindowEnd; end != nil && now.Before(*end) {
				if maxResetRemaining > minResetRemaining {
					resetFactor = 1 - Clamp01((end.Sub(now).Seconds()-minResetRemaining)/(maxResetRemaining-minResetRemaining))
				} else {
					resetFactor = 1
				}
			}
		}
		quotaHeadroomFactor := 0.5
		if weights.QuotaHeadroom > 0 {
			quotaHeadroomFactor = Clamp01(quotaFactor(item.Account, now))
		}
		item.Factors = CandidateFactors{
			Priority:      priorityFactor,
			Load:          loadFactor,
			Queue:         queueFactor,
			ErrorRate:     errorFactor,
			TTFT:          ttftFactor,
			Reset:         resetFactor,
			QuotaHeadroom: quotaHeadroomFactor,
		}
		item.BaseScore = weights.Priority*priorityFactor +
			weights.Load*loadFactor +
			weights.Queue*queueFactor +
			weights.ErrorRate*errorFactor +
			weights.TTFT*ttftFactor +
			weights.Reset*resetFactor +
			weights.QuotaHeadroom*quotaHeadroomFactor
		item.Score = item.BaseScore
		if input.StickyWeighted {
			if input.StickyPreviousAccountID > 0 && item.Account.ID == input.StickyPreviousAccountID {
				item.PreviousBonus = weights.Previous
				item.StickyBonus += item.PreviousBonus
			}
			if input.StickyAccountID > 0 && item.Account.ID == input.StickyAccountID {
				item.SessionStickyBonus = weights.SessionSticky
				item.StickyBonus += item.SessionStickyBonus
			}
		}
		item.Score += item.StickyBonus
	}

	return candidates, LoadSkewByMoments(loadRateSum, loadRateSumSquares, knownLoadCount), ranges
}

type SelectionRNG struct {
	state uint64
}

func NewSelectionRNG(seed uint64) SelectionRNG {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return SelectionRNG{state: seed}
}

func (r *SelectionRNG) NextUint64() uint64 {
	// xorshift64*
	x := r.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.state = x
	return x * 2685821657736338717
}

func (r *SelectionRNG) NextFloat64() float64 {
	return float64(r.NextUint64()>>11) / (1 << 53)
}

func SelectionSeed(input ScoreInput) uint64 {
	now := input.Now
	if now == nil {
		now = time.Now
	}
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
		seed ^= uint64(now().UnixNano())
	}
	if seed == 0 {
		seed = uint64(now().UnixNano()) ^ 0x9e3779b97f4a7c15
	}
	return seed
}

func BuildWeightedSelectionOrder(candidates []CandidateScore, input ScoreInput) []CandidateScore {
	if len(candidates) <= 1 {
		return append([]CandidateScore(nil), candidates...)
	}

	pool := append([]CandidateScore(nil), candidates...)
	weights := make([]float64, len(pool))
	minScore := pool[0].Score
	for i := 1; i < len(pool); i++ {
		if pool[i].Score < minScore {
			minScore = pool[i].Score
		}
	}
	for i := range pool {
		// 将 Top-K 分值平移到正区间，避免单个账号长期垄断。
		weight := (pool[i].Score - minScore) + 1.0
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
			weight = 1.0
		}
		weights[i] = weight
	}

	order := make([]CandidateScore, 0, len(pool))
	rng := NewSelectionRNG(SelectionSeed(input))
	for len(pool) > 0 {
		total := 0.0
		for _, weight := range weights {
			total += weight
		}
		selectedIdx := 0
		if total > 0 {
			random := rng.NextFloat64() * total
			accumulated := 0.0
			for i, weight := range weights {
				accumulated += weight
				if random <= accumulated {
					selectedIdx = i
					break
				}
			}
		} else {
			selectedIdx = int(rng.NextUint64() % uint64(len(pool)))
		}
		order = append(order, pool[selectedIdx])
		pool = append(pool[:selectedIdx], pool[selectedIdx+1:]...)
		weights = append(weights[:selectedIdx], weights[selectedIdx+1:]...)
	}
	return order
}

func BuildSelectionOrder(candidates []CandidateScore, input ScoreInput) []CandidateScore {
	if len(candidates) == 0 {
		return nil
	}
	topK := input.TopK
	if topK <= 0 {
		topK = 1
	}
	ranked := SelectTopK(candidates, topK)
	return BuildWeightedSelectionOrder(ranked, input)
}

// ScoreSnapshot 是管理端和高级调度器共用的评分展示结构。
type ScoreSnapshot struct {
	BaseScore             float64
	StickyScore           float64
	StickyScoreInfinity   bool
	StickyWeightedEnabled bool
}

func BuildScoreSnapshot(
	accounts []*ScoreAccount,
	loadMap map[int64]*AccountLoadInfo,
	stats *RuntimeStats,
	group *ScoreGroup,
	weights policy.ScoreWeights,
	stickyWeightedEnabled bool,
	quotaHeadroomFactor func(*ScoreAccount, time.Time) float64,
	now time.Time,
) map[int64]ScoreSnapshot {
	candidates, _ := ScoreCandidates(accounts, loadMap, stats, weights, ScoreInput{
		QuotaHeadroomFactor: quotaHeadroomFactor,
	}, now)
	if len(candidates) == 0 {
		return nil
	}
	result := make(map[int64]ScoreSnapshot, len(candidates))
	for _, candidate := range candidates {
		score := ScoreSnapshot{
			BaseScore:             candidate.Score,
			StickyWeightedEnabled: stickyWeightedEnabled,
			StickyScoreInfinity:   !stickyWeightedEnabled,
		}
		if stickyWeightedEnabled {
			// 分组平台定义请求语义；无分组兼容入口按账号平台判断。
			platform := candidate.Account.Platform
			if group != nil && strings.TrimSpace(group.Platform) != "" {
				platform = group.Platform
			}
			score.StickyScore = candidate.Score + weights.SessionSticky
			if platform == capability.PlatformOpenAI {
				score.StickyScore += weights.Previous
			}
		}
		result[candidate.Account.ID] = score
	}
	return result
}

// ScoreAccount 只包含评分使用的值，不携带凭据、管理对象或动态设置。
type ScoreAccount struct {
	// Name 和 ProjectionID 仅用于只读诊断关联，不参与评分。
	Name             string
	ProjectionID     uint64
	ID               int64
	Platform         string
	Priority         int
	SessionWindowEnd *time.Time
}
type ScoreGroup struct{ Platform string }

func (s *RuntimeStats) nowTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
func Clamp01(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func LoadSkewByMoments(sum float64, sumSquares float64, count int) float64 {
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
