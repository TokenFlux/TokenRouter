package provider

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// GrokHealthStore 保留现有快照与状态写权限，扩展/恢复 CAS 仍由实际存储能力提供。
type GrokHealthStore interface {
	accountcore.GrokRateLimitWriter
	UpdateExtra(context.Context, int64, map[string]any) error
	SetTempUnschedulable(context.Context, int64, time.Time, string) error
}

// GrokHealth 复用应用持有的节流与运行状态，执行 Grok 账号健康规则。
// @project-doc docs/interfaces/grok_upstream.md#grok_account_contract
type GrokHealth struct {
	NormalizeModel func(*accountcore.Record, string) string
	Store          GrokHealthStore
	Throttle       *accountcore.WriteThrottle
	Runtime        *accountcore.RuntimeBlockState
	Health         *UpstreamHealth
	ModelTransient *accountcore.ModelTransientState
}

const grokQuotaSnapshotExtraKey = "grok_usage_snapshot"

func grokStateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, 5*time.Second)
}
func (s *GrokHealth) StoreSnapshot(ctx context.Context, value *accountcore.Record, snapshot *grok.QuotaSnapshot, installRateLimit bool, teamModel string) {
	if s == nil || value == nil || value.ID <= 0 || snapshot == nil {
		return
	}
	accountID := value.ID
	now := time.Now()
	resetAt, hasActiveLimit := accountcore.GrokRateLimitResetAtForAccount(value, snapshot, now)
	if hasActiveLimit {
		accountcore.NormalizeGrokExhaustedWindowResets(snapshot, resetAt, now)
	}
	recovery := accountcore.IsSuccessfulGrokRateLimitRecovery(value, snapshot)
	critical := snapshot.StatusCode == http.StatusTooManyRequests || hasActiveLimit || recovery
	if s.Throttle != nil {
		allowed := s.Throttle.Allow(accountID, now)
		if !critical && !allowed {
			return
		}
	}

	updates := map[string]any{
		grokQuotaSnapshotExtraKey: snapshot,
	}
	// 同时派生 grokThresholdCandidates 评估器读取的调度阈值扩展字段 grok_sched_*。
	// 缺少此写入逻辑时，管理员配置的 Grok 自动暂停阈值无法触发。
	for k, v := range accountcore.BuildGrokSchedulerExtraUpdates(snapshot) {
		updates[k] = v
	}
	stateCtx := ctx
	if hasActiveLimit {
		var cancel context.CancelFunc
		stateCtx, cancel = grokStateContext(ctx)
		defer cancel()
	}
	// 请求路径中的 Account 指针来自每次 Redis/DB 解码，不是进程内共享缓存；
	// 这里与 token 刷新和限流写入保持一致，调用方不得跨 goroutine 复用同一指针。
	if value.Extra == nil {
		value.Extra = map[string]any{}
	}
	value.Extra[grokQuotaSnapshotExtraKey] = snapshot
	if s.Store != nil {
		_ = s.Store.UpdateExtra(stateCtx, accountID, updates)
	}
	// 池模式上游本身负责在真实账号池中切换，额度头只作为观测数据保留，不能反向
	// 冷却本地这个聚合账号。非池模式仍将错误响应或成功后耗尽的窗口写成真实限流。
	if installRateLimit && hasActiveLimit && !value.IsPoolMode() {
		s.RateLimit(stateCtx, value, resetAt, teamModel)
	} else if recovery {
		accountcore.ClearGrokRateLimitAfterRecovery(stateCtx, s.Store, accountcore.CloneRecord(value), slog.Warn)
	}
}

func (s *GrokHealth) RateLimit(ctx context.Context, value *accountcore.Record, resetAt time.Time, teamModel string) {
	if s == nil || value == nil {
		return
	}
	now := time.Now()
	resetAt = accountcore.NormalizeGrokRateLimitResetAt(value, resetAt, now)

	runtimeUntil := resetAt
	if value.TempUnschedulableUntil != nil && value.TempUnschedulableUntil.After(runtimeUntil) {
		runtimeUntil = *value.TempUnschedulableUntil
	}
	s.Runtime.BlockAccountScheduling(value, runtimeUntil, "429")
	accountcore.PersistGrokRateLimit(ctx, s.Store, accountcore.CloneRecord(value), resetAt, slog.Warn)

	// 扩散短期团队与模型冷却，使同一 xAI 团队的关联 OAuth 账号跳过热点模型，
	// 无需等待每个账号分别收到 429；空模型不扩散团队冷却。
	if teamModel != "" {
		accountcore.MarkGrokTeamModelRateLimit(value, teamModel, accountcore.ResolveGrokTeamRateLimitUntil(resetAt, now))
	}
}

func (s *GrokHealth) TempUnschedule(ctx context.Context, value *accountcore.Record, cooldown time.Duration, reason string) {
	if s == nil || value == nil {
		return
	}
	until := time.Now().Add(cooldown)
	if value.TempUnschedulableUntil != nil && value.TempUnschedulableUntil.After(until) {
		until = *value.TempUnschedulableUntil
	}
	s.Runtime.BlockAccountScheduling(value, until, reason)
	if s.Store != nil {
		stateCtx, cancel := grokStateContext(ctx)
		defer cancel()
		_ = s.Store.SetTempUnschedulable(stateCtx, value.ID, until, reason)
	}
}

// ObserveResponse 保留先解析和标记、再写快照的顺序；没有额度头时只尝试精确恢复。
func (s *GrokHealth) ObserveResponse(ctx context.Context, value *accountcore.Record, headers http.Header, status int, model string) {
	snapshot := grok.ParseQuotaObservation(headers, status, time.Now())
	if snapshot != nil {
		accountcore.StampGrokQuotaPlan(accountcore.CloneRecord(value), snapshot, model, grok.ResolveGrokTextResponsesModelID, grok.ApplyGrok45ResponsesPlanSignal)
		s.StoreSnapshot(ctx, value, snapshot, true, model)
		return
	}
	if accountcore.IsSuccessfulGrokRateLimitRecovery(accountcore.CloneRecord(value), &grok.QuotaSnapshot{StatusCode: status}) {
		accountcore.ClearGrokRateLimitAfterRecovery(ctx, s.Store, accountcore.CloneRecord(value), slog.Warn)
	}
}
