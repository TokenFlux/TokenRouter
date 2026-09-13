// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	slog "log/slog"
	sync "sync"
	time "time"
)

// 原有行为断言保持输入形状，实际规则与共享状态由新核心运行。
const maxTokenRefreshProviderFailureThreshold = acctcore.MaxTokenRefreshProviderFailureThreshold
const maxTokenRefreshMaxRetries = acctcore.MaxTokenRefreshMaxRetries
const maxTokenRefreshRetryBackoff = acctcore.MaxTokenRefreshRetryBackoff
const maxTokenRefreshAttemptTimeout = acctcore.MaxTokenRefreshAttemptTimeout
const maxTokenRefreshCycleTimeout = acctcore.MaxTokenRefreshCycleTimeout

func (s *TokenRefreshService) eligiblePlatforms() []string {
	platforms := make([]string, 0, len(s.registrations))
	for _, registration := range s.registrations {
		if registration.platform != "" && registration.refresher != nil {
			platforms = append(platforms, registration.platform)
		}
	}
	return platforms
}
func (s *TokenRefreshService) candidateAfterID() int64 { return s.Core().CandidatePosition() }

type tokenRefreshProviderState struct {
	service      *TokenRefreshService
	registration tokenRefreshRegistration
	rateGate     refreshAttemptGate
	poolGate     *tokenRefreshConcurrencyGate

	stateOnce sync.Once
	stateCore *acctcore.RefreshProviderState
}
type tokenRefreshRateGate = acctcore.RefreshRateGate
type tokenRefreshConcurrencyGate = acctcore.RefreshConcurrencyGate

func newTokenRefreshRateGate(qps int) *tokenRefreshRateGate {
	return acctcore.NewRefreshRateGate(qps)
}
func newTokenRefreshRateGateWithInterval(interval time.Duration) *tokenRefreshRateGate {
	return acctcore.NewRefreshRateGateWithInterval(interval)
}
func newTokenRefreshConcurrencyGate(concurrency int) *tokenRefreshConcurrencyGate {
	return acctcore.NewRefreshConcurrencyGate(concurrency)
}
func (p *tokenRefreshProviderState) Acquire(ctx context.Context) (func(), error) {
	if p == nil {
		return nil, errRefreshSkipped
	}
	return p.core().Acquire(ctx)
}
func (p *tokenRefreshProviderState) AcquireRate(ctx context.Context) (func(), error) {
	if p == nil {
		return nil, errRefreshSkipped
	}
	return p.core().AcquireRate(ctx)
}
func (p *tokenRefreshProviderState) recordResult(err error) { p.core().RecordResult(err) }

// processRefresh 保留现有测试和内部调用入口，生产循环则提供可取消的父上下文。
func (s *TokenRefreshService) processRefresh() {
	s.processRefreshContext(context.Background())
}
func (s *TokenRefreshService) processProviderAccounts(ctx context.Context, state *tokenRefreshProviderState, values []*Account, window time.Duration) (int, int, int) {
	projections := make([]*acctcore.Record, len(values))
	for i, value := range values {
		projections[i] = AccountRecordView(value)
	}
	return (acctcore.RefreshPageProcessor{Concurrency: s.providerConcurrency(), Info: slog.Info, Warn: slog.Warn}).ProcessProvider(ctx, state.execution(s), projections, window)
}
func (s *TokenRefreshService) providerConcurrency() int { return s.refreshTuning().Concurrency() }
func (s *TokenRefreshService) providerRateGate(platform string) *tokenRefreshRateGate {
	return s.Core().ProviderRateGate(platform)
}
func (s *TokenRefreshService) providerConcurrencyGate(platform string) *tokenRefreshConcurrencyGate {
	return s.Core().ProviderConcurrencyGate(platform)
}
func (s *TokenRefreshService) providerFailureThreshold() int {
	return s.refreshTuning().FailureThreshold()
}
func (s *TokenRefreshService) cycleTimeout() time.Duration { return s.refreshTuning().CycleTimeout() }
func (s *TokenRefreshService) maxRetries() int             { return s.refreshTuning().Retries() }

// refreshWithRetry 带重试的刷新
func (s *TokenRefreshService) refreshWithRetry(ctx context.Context, account *Account, refresher TokenRefresher, executor OAuthRefreshExecutor, refreshWindow time.Duration) error {
	return s.refreshWithRetryWithRateGate(ctx, account, refresher, executor, refreshWindow, nil)
}

// refreshWithRetryWithRateGate 只投影旧调用形状，所有尝试算法由 account 运行。
func (s *TokenRefreshService) refreshWithRetryWithRateGate(ctx context.Context, value *Account, refresher TokenRefresher, executor OAuthRefreshExecutor, window time.Duration, gate refreshAttemptGate) error {
	record := AccountRecordView(value)
	var action acctcore.RefreshTokenOperation
	if refresher != nil {
		action = legacyBackgroundRefreshOperation{source: refresher, initial: value}
	}
	var exchange acctcore.OAuthRefreshExecutor
	if executor != nil {
		exchange = legacyRefreshExecutor{source: executor, initial: value}
	}
	return s.refreshAttempts(value, s.refreshAPI == nil || executor == nil).Run(ctx, record, action, exchange, window, gate)
}
func (s *TokenRefreshService) retryBackoff(accountID int64, attempt int) time.Duration {
	return s.refreshTuning().RetryBackoff(accountID, attempt)
}

// core 只投影旧执行器的准入接口，不复制平台失败计数或状态。
func (p *tokenRefreshProviderState) core() *acctcore.RefreshProviderState {
	p.stateOnce.Do(func() {
		var rate acctcore.RefreshAttemptGate
		if p.rateGate != nil {
			rate = p.rateGate
		}
		var pool *acctcore.RefreshConcurrencyGate
		if p.poolGate != nil {
			pool = p.poolGate
		}
		p.stateCore = acctcore.NewRefreshProviderState(rate, pool, p.service.providerFailureThreshold(), isNonRetryableRefreshError)
	})
	return p.stateCore
}

// execution 仅投影原供应商适配器，单轮状态与 worker 算法在 account 中复用。
func (p *tokenRefreshProviderState) execution(s *TokenRefreshService) *acctcore.RefreshProviderExecution {
	if p == nil {
		return nil
	}
	out := &acctcore.RefreshProviderExecution{Platform: p.registration.platform, State: p.core()}
	if p.registration.refresher != nil {
		out.CanRefresh = func(v *acctcore.Record) bool { return p.registration.refresher.CanRefresh(AccountFromRecord(v)) }
		out.NeedsRefresh = func(v *acctcore.Record, window time.Duration) bool {
			return p.registration.refresher.NeedsRefresh(AccountFromRecord(v), window)
		}
	}
	out.Execute = func(ctx context.Context, v *acctcore.Record, window time.Duration, state *acctcore.RefreshProviderState) error {
		return s.refreshWithRetryWithRateGate(ctx, AccountFromRecord(v), p.registration.refresher, p.registration.executor, window, state)
	}
	return out
}

type refreshAttemptGate interface {
	Acquire(ctx context.Context) (release func(), err error)
}
