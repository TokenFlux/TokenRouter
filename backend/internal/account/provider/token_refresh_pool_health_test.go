package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"maps"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type poolHealthAccountRepo struct {
	mu                   sync.Mutex
	pages                map[int64][]accountcore.Record
	requests             []accountcore.OAuthRefreshPageOptions
	updatedCredentialIDs []int64
	setErrorCalls        int
	setTempUnschedCalls  int
	getByIDErr           error
}

func (r *poolHealthAccountRepo) GetByID(_ context.Context, _ int64) (*accountcore.Record, error) {
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	return nil, accountcore.ErrAccountNotFound
}

func (r *poolHealthAccountRepo) ListOAuthRefreshCandidatePage(_ context.Context, options accountcore.OAuthRefreshPageOptions) (*accountcore.OAuthRefreshCandidatePage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, options)
	accounts := append([]accountcore.Record(nil), r.pages[options.AfterID]...)
	page := &accountcore.OAuthRefreshCandidatePage{Accounts: accounts, HasMore: len(accounts) == options.Limit}
	if len(accounts) > 0 {
		page.NextAfterID = accounts[len(accounts)-1].ID
	}
	return page, nil
}

func (r *poolHealthAccountRepo) UpdateCredentials(_ context.Context, id int64, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updatedCredentialIDs = append(r.updatedCredentialIDs, id)
	return nil
}

func (r *poolHealthAccountRepo) UpdateGrokOAuthCredentialsIfUnchanged(
	_ context.Context,
	id int64,
	_ map[string]any,
	_ *int64,
	_ map[string]any,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updatedCredentialIDs = append(r.updatedCredentialIDs, id)
	return true, nil
}

func (r *poolHealthAccountRepo) SetError(context.Context, int64, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setErrorCalls++
	return nil
}

func (r *poolHealthAccountRepo) SetGrokOAuthErrorIfCredentialsUnchanged(context.Context, int64, map[string]any, string) (bool, error) {
	return false, nil
}

func (r *poolHealthAccountRepo) SetGrokOAuthRefreshErrorIfCredentialsUnchanged(context.Context, int64, map[string]any, *int64, string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setErrorCalls++
	return true, nil
}

func (r *poolHealthAccountRepo) SetGrokOAuthRefreshTempUnschedulableIfCredentialsUnchanged(context.Context, int64, map[string]any, *int64, time.Time, string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setTempUnschedCalls++
	return true, nil
}

func (r *poolHealthAccountRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setTempUnschedCalls++
	return nil
}

func (r *poolHealthAccountRepo) snapshot() ([]accountcore.OAuthRefreshPageOptions, []int64, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]accountcore.OAuthRefreshPageOptions(nil), r.requests...), append([]int64(nil), r.updatedCredentialIDs...), r.setErrorCalls, r.setTempUnschedCalls
}

type poolHealthRefresher struct {
	err            error
	delay          time.Duration
	startDelays    []time.Duration
	ignoreContext  bool
	cancel         context.CancelFunc
	newCredentials map[string]any
	calls          atomic.Int64
	active         atomic.Int64
	maxActive      atomic.Int64
	startMu        sync.Mutex
	startTimes     []time.Time
}

type countingRefreshAttemptGate struct {
	calls atomic.Int64
}

type rejectedRefreshAttemptGate struct {
	err error
}

type poolHealthTokenCacheStub struct {
	accountcore.AccessTokenCache
}

type tripBeforeRateAdmissionGate struct {
	state *accountcore.RefreshProviderState
}

func (g *tripBeforeRateAdmissionGate) Acquire(ctx context.Context) (func(), error) {
	release, err := g.state.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	g.state.RecordResult(&accountcore.ProviderConfigurationRefreshError{Cause: errors.New("fixture provider unavailable")})
	return release, nil
}

func (g *tripBeforeRateAdmissionGate) AcquireRate(ctx context.Context) (func(), error) {
	return g.state.AcquireRate(ctx)
}

type breakerTripAccountRepo struct {
	*productionPathRateRepo
	setErrorCalls atomic.Int64
	setTempCalls  atomic.Int64
}

func (r *breakerTripAccountRepo) SetGrokOAuthRefreshErrorIfCredentialsUnchanged(context.Context, int64, map[string]any, *int64, string) (bool, error) {
	r.setErrorCalls.Add(1)
	return true, nil
}

func (r *breakerTripAccountRepo) SetGrokOAuthRefreshTempUnschedulableIfCredentialsUnchanged(context.Context, int64, map[string]any, *int64, time.Time, string) (bool, error) {
	r.setTempCalls.Add(1)
	return true, nil
}

func (g *rejectedRefreshAttemptGate) Acquire(context.Context) (func(), error) {
	return nil, g.err
}

type productionPathRateRepo struct {
	mu       sync.Mutex
	accounts map[int64]*accountcore.Record
}

func (r *productionPathRateRepo) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[id]
	if account == nil {
		return nil, accountcore.ErrAccountNotFound
	}
	return accountcore.CloneRecord(account), nil
}

func (r *productionPathRateRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[id]
	if account == nil {
		return accountcore.ErrAccountNotFound
	}
	account.Credentials = maps.Clone(credentials)
	return nil
}

func (r *productionPathRateRepo) UpdateGrokOAuthCredentialsIfUnchanged(
	_ context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	credentials map[string]any,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[id]
	if account == nil || !reflect.DeepEqual(account.Credentials, expectedCredentials) ||
		!reflect.DeepEqual(account.ProxyID, expectedProxyID) {
		return false, nil
	}
	account.Credentials = maps.Clone(credentials)
	return true, nil
}

type productionPathRefreshStart struct {
	accountID int64
	at        time.Time
}

type productionPathRateExecutor struct {
	firstStarted chan struct{}
	releaseFirst chan struct{}
	calls        atomic.Int64
	startMu      sync.Mutex
	starts       []productionPathRefreshStart
}

func (e *productionPathRateExecutor) CacheKey(account *accountcore.Record) string {
	return fmt.Sprintf("production-path-rate:%d", account.ID)
}

func (e *productionPathRateExecutor) CanRefresh(account *accountcore.Record) bool {
	return account != nil && account.IsGrokOAuth()
}

func (e *productionPathRateExecutor) NeedsRefresh(account *accountcore.Record, _ time.Duration) bool {
	needsRefresh, _ := account.Credentials["needs_refresh"].(bool)
	return needsRefresh
}

func (e *productionPathRateExecutor) Refresh(ctx context.Context, account *accountcore.Record) (map[string]any, error) {
	call := e.calls.Add(1)
	e.startMu.Lock()
	e.starts = append(e.starts, productionPathRefreshStart{accountID: account.ID, at: time.Now()})
	e.startMu.Unlock()
	if call == 1 {
		close(e.firstStarted)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-e.releaseFirst:
		}
	}
	return map[string]any{
		"access_token":  fmt.Sprintf("fresh-access-%d", account.ID),
		"refresh_token": fmt.Sprintf("fresh-refresh-%d", account.ID),
		"needs_refresh": false,
	}, nil
}

func (e *productionPathRateExecutor) startsSnapshot() []productionPathRefreshStart {
	e.startMu.Lock()
	defer e.startMu.Unlock()
	return append([]productionPathRefreshStart(nil), e.starts...)
}

func (g *countingRefreshAttemptGate) Acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.calls.Add(1)
	return func() {}, nil
}

func (r *poolHealthRefresher) CacheKey(account *accountcore.Record) string {
	return fmt.Sprintf("pool-health:%d", account.ID)
}

func (r *poolHealthRefresher) CanRefresh(account *accountcore.Record) bool {
	return account != nil && account.Platform == capability.PlatformGrok && account.Type == capability.AccountTypeOAuth
}

func (r *poolHealthRefresher) NeedsRefresh(*accountcore.Record, time.Duration) bool { return true }

func (r *poolHealthRefresher) Refresh(ctx context.Context, _ *accountcore.Record) (map[string]any, error) {
	r.calls.Add(1)
	active := r.active.Add(1)
	defer r.active.Add(-1)
	r.startMu.Lock()
	startIndex := len(r.startTimes)
	r.startTimes = append(r.startTimes, time.Now())
	delay := r.delay
	if startIndex < len(r.startDelays) {
		delay = r.startDelays[startIndex]
	}
	r.startMu.Unlock()
	for {
		maxActive := r.maxActive.Load()
		if active <= maxActive || r.maxActive.CompareAndSwap(maxActive, active) {
			break
		}
	}
	if r.cancel != nil {
		r.cancel()
	}
	if delay > 0 {
		if r.ignoreContext {
			time.Sleep(delay)
		} else {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.newCredentials != nil {
		credentials := make(map[string]any, len(r.newCredentials))
		for key, value := range r.newCredentials {
			credentials[key] = value
		}
		return credentials, nil
	}
	return map[string]any{"access_token": "new-token", "refresh_token": "new-refresh-token"}, nil
}

func (r *poolHealthRefresher) startsSnapshot() []time.Time {
	r.startMu.Lock()
	defer r.startMu.Unlock()
	return append([]time.Time(nil), r.startTimes...)
}

func grokPoolAccount(id int64) accountcore.Record {
	return accountcore.Record{
		ID:       id,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Status:   accountcore.StatusActive,
		Credentials: map[string]any{
			"access_token":  "old-token",
			"refresh_token": "refresh-token",
		},
	}
}

func newPoolHealthService(repo *poolHealthAccountRepo, refresher *poolHealthRefresher, cfg accountcore.RefreshTuning) *accountcore.BackgroundRefreshOptions {
	return &accountcore.BackgroundRefreshOptions{
		Tuning: &cfg, Pager: repo,
		Registrations:  []accountcore.RefreshRegistration{{Platform: capability.PlatformGrok, Refresher: refresher, Executor: refresher}},
		Attempts:       backgroundAttemptOptions(repo, &cfg),
		Reconciliation: accountcore.GrokReconciliationOptions{Reader: repo, ConditionalError: repo, Now: time.Now, Skew: accountcore.GrokTokenRefreshSkew},
	}
}

func TestTokenRefreshService_ProcessRefreshPagesByStableCursor(t *testing.T) {
	repo := &poolHealthAccountRepo{pages: map[int64][]accountcore.Record{
		0: {grokPoolAccount(1), grokPoolAccount(2)},
		2: {grokPoolAccount(3)},
	}}
	refresher := &poolHealthRefresher{}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		RefreshBeforeExpiryHours: 1,
		MaxRetries:               1,
		CandidatePageSize:        2,
		ProviderConcurrency:      4,
		ProviderQPS:              10000,
		AttemptTimeoutSeconds:    1,
		CycleTimeoutSeconds:      2,
	})

	runtime := accountcore.NewBackgroundRefreshService(*svc)
	runtime.ScanCycle(context.Background())

	requests, updatedIDs, _, _ := repo.snapshot()
	require.Len(t, requests, 2)
	require.Equal(t, int64(0), requests[0].AfterID)
	require.Equal(t, int64(2), requests[1].AfterID)
	require.Equal(t, []string{capability.PlatformGrok}, requests[0].Platforms)
	require.True(t, requests[0].ActiveOnly)
	require.True(t, requests[0].RequireRefreshToken)
	require.True(t, requests[0].ExcludeRetryCooldown)
	sort.Slice(updatedIDs, func(i, j int) bool { return updatedIDs[i] < updatedIDs[j] })
	require.Equal(t, []int64{1, 2, 3}, updatedIDs)
	require.Zero(t, runtime.CandidatePosition(), "a short final page must wrap the next cycle to the beginning")
}

func TestTokenRefreshService_BoundsPerProviderConcurrency(t *testing.T) {
	accounts := make([]accountcore.Record, 0, 8)
	for id := int64(1); id <= 8; id++ {
		accounts = append(accounts, grokPoolAccount(id))
	}
	repo := &poolHealthAccountRepo{pages: map[int64][]accountcore.Record{0: accounts}}
	refresher := &poolHealthRefresher{delay: 20 * time.Millisecond}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		MaxRetries:            1,
		CandidatePageSize:     20,
		ProviderConcurrency:   2,
		ProviderQPS:           10000,
		AttemptTimeoutSeconds: 1,
		CycleTimeoutSeconds:   2,
	})

	accountcore.NewBackgroundRefreshService(*svc).ScanCycle(context.Background())

	require.Equal(t, int64(8), refresher.calls.Load())
	require.Equal(t, int64(2), refresher.maxActive.Load())
}

func TestTokenRefreshRateGate_ReservesSpacedSlotsAndHonorsCancellation(t *testing.T) {
	const interval = 25 * time.Millisecond
	gate := accountcore.NewRefreshRateGateWithInterval(interval)
	base := time.Unix(1_700_000_000, 0)

	require.Equal(t, base, gate.ReserveSlot(base))
	require.Equal(t, base.Add(interval), gate.ReserveSlot(base))
	require.Equal(t, base.Add(2*interval), gate.ReserveSlot(base))
	jumped := base.Add(time.Second)
	require.Equal(t, jumped, gate.ReserveSlot(jumped), "an idle gate should not retain stale delay")

	cancelGate := accountcore.NewRefreshRateGateWithInterval(time.Hour)
	require.NoError(t, cancelGate.Wait(context.Background()), "the first slot is immediately available")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	require.ErrorIs(t, cancelGate.Wait(ctx), context.Canceled)
	require.Less(t, time.Since(started), 100*time.Millisecond, "cancellation must not wait for the reserved slot")
}

func TestTokenRefreshService_RetriesAcquireRateSlotPerAttempt(t *testing.T) {
	repo := &poolHealthAccountRepo{}
	refresher := &poolHealthRefresher{err: errors.New("temporary provider failure")}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{MaxRetries: 3})
	gate := &countingRefreshAttemptGate{}
	account := grokPoolAccount(44)

	err := svc.Attempts.Run(context.Background(), &account, refresher, nil, time.Hour, gate)

	require.Error(t, err)
	require.Equal(t, int64(3), refresher.calls.Load())
	require.Equal(t, int64(3), gate.calls.Load(), "every upstream retry must consume a provider rate slot")
}

func TestTokenRefreshService_ProcessProviderAccountsLegacyNilReleaseGateIsSafe(t *testing.T) {
	repo := &poolHealthAccountRepo{}
	refresher := &poolHealthRefresher{}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		MaxRetries:          1,
		ProviderConcurrency: 1,
	})
	// 准入拒绝没有释放回调，直接刷新仍须传播跳过且不执行供应商交换。
	gate := accountcore.NewRefreshProviderState(&rejectedRefreshAttemptGate{err: accountcore.ErrRefreshSkipped}, nil, svc.Tuning.FailureThreshold(), IsNonRetryableRefreshError)
	state := &accountcore.RefreshProviderExecution{
		Platform: capability.PlatformGrok, State: gate,
		CanRefresh: refresher.CanRefresh, NeedsRefresh: refresher.NeedsRefresh,
		Execute: func(ctx context.Context, value *accountcore.Record, window time.Duration, state *accountcore.RefreshProviderState) error {
			return svc.Attempts.Run(ctx, value, refresher, nil, window, state)
		},
	}
	account := grokPoolAccount(45)

	refreshed, skipped, failed := (accountcore.RefreshPageProcessor{Concurrency: svc.Tuning.Concurrency(), Info: func(string, ...any) {}, Warn: func(string, ...any) {}}).ProcessProvider(
		context.Background(),
		state,
		[]*accountcore.Record{&account},
		time.Hour,
	)

	require.Zero(t, refreshed)
	require.Equal(t, 1, skipped)
	require.Zero(t, failed)
	require.Zero(t, refresher.calls.Load(), "rejected rate admission must not reach the legacy upstream refresher")
}

func TestTokenRefreshService_ProviderRateGateIsSharedAcrossRuns(t *testing.T) {
	runtime := accountcore.NewBackgroundRefreshService(accountcore.BackgroundRefreshOptions{Tuning: &accountcore.RefreshTuning{ProviderQPS: 40}})
	first := runtime.ProviderRateGate(capability.PlatformGrok)
	second := runtime.ProviderRateGate(capability.PlatformGrok)
	require.Same(t, first, second, "background cycles and reconciliation must share the process-local provider limiter")

	base := time.Unix(1_700_000_000, 0)
	require.Equal(t, base, first.ReserveSlot(base))
	require.Equal(t, base.Add(25*time.Millisecond), second.ReserveSlot(base))
}

func TestTokenRefreshService_ProviderConcurrencyGateIsSharedAcrossBackgroundAndConcurrentAdminReconciliation(t *testing.T) {
	accounts := []accountcore.Record{
		grokPoolAccount(1),
		grokPoolAccount(2),
		grokPoolAccount(3),
		grokPoolAccount(4),
	}
	repo := &poolHealthAccountRepo{pages: map[int64][]accountcore.Record{0: accounts}}
	refresher := &poolHealthRefresher{delay: 80 * time.Millisecond}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		RefreshBeforeExpiryHours: 1,
		MaxRetries:               1,
		CandidatePageSize:        20,
		ProviderConcurrency:      2,
		ProviderQPS:              100,
		ProviderFailureThreshold: 20,
		AttemptTimeoutSeconds:    1,
		CycleTimeoutSeconds:      3,
	})

	runtime := accountcore.NewBackgroundRefreshService(*svc)
	firstGate := runtime.ProviderConcurrencyGate(capability.PlatformGrok)
	require.Same(t, firstGate, runtime.ProviderConcurrencyGate(capability.PlatformGrok))

	start := make(chan struct{})
	adminErrors := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		runtime.ScanCycle(context.Background())
	}()
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := runtime.ReconcileGrokOAuth(context.Background(), accountcore.GrokOAuthReconcileInput{Apply: true, Limit: 20})
			adminErrors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(adminErrors)

	for err := range adminErrors {
		require.NoError(t, err)
	}
	require.Equal(t, int64(12), refresher.calls.Load(), "background and both admin calls must all execute")
	require.Equal(t, int64(2), refresher.maxActive.Load(),
		"all entry points must share the configured per-provider upstream concurrency cap")
}

func TestTokenRefreshService_SaturatedProviderPreservesConcurrencyAndActualQPSStartSpacing(t *testing.T) {
	const (
		providerConcurrency = 2
		providerQPS         = 20
		attemptCount        = 8
	)
	repo := &poolHealthAccountRepo{}
	refresher := &poolHealthRefresher{
		// 前两个按 QPS 间隔启动的尝试会同时完成；若排队调用先占速率槽再等待容量，过期预约会并发冲向上游。
		startDelays: []time.Duration{
			220 * time.Millisecond,
			170 * time.Millisecond,
			20 * time.Millisecond,
			20 * time.Millisecond,
			20 * time.Millisecond,
			20 * time.Millisecond,
			20 * time.Millisecond,
			20 * time.Millisecond,
		},
	}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		MaxRetries:            1,
		ProviderConcurrency:   providerConcurrency,
		ProviderQPS:           providerQPS,
		AttemptTimeoutSeconds: 1,
	})
	runtime := accountcore.NewBackgroundRefreshService(*svc)
	sharedRateGate := runtime.ProviderRateGate(capability.PlatformGrok)
	sharedPoolGate := runtime.ProviderConcurrencyGate(capability.PlatformGrok)

	start := make(chan struct{})
	errorsCh := make(chan error, attemptCount)
	var wg sync.WaitGroup
	for i := 0; i < attemptCount; i++ {
		account := grokPoolAccount(int64(i + 1))
		state := accountcore.NewRefreshProviderState(sharedRateGate, sharedPoolGate, svc.Tuning.FailureThreshold(), IsNonRetryableRefreshError)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errorsCh <- svc.Attempts.Run(context.Background(), &account, refresher, nil, time.Hour, state)
		}()
	}
	close(start)
	wg.Wait()
	close(errorsCh)

	for err := range errorsCh {
		require.NoError(t, err)
	}
	require.Equal(t, int64(providerConcurrency), refresher.maxActive.Load(),
		"the scripted attempts must actually saturate the provider semaphore")
	starts := refresher.startsSnapshot()
	require.Len(t, starts, attemptCount)
	configuredSpacing := time.Second / time.Duration(providerQPS)

	// 时间戳在速率门放行后才记录，繁忙机器上的调度延迟可能压缩相邻观测间隔。
	// 取配置间隔的十分之一仍可把正常的 50ms 节流与无节流时的微秒级启动区分开，
	// 同时避免把调度抖动误判成限速失效。不能改为总跨度断言，因为总跨度主要受
	// providerConcurrency 下单次刷新耗时影响，即使关闭速率门也可能通过。
	minimumObservedSpacing := configuredSpacing / 10
	actualMinimumSpacing := starts[1].Sub(starts[0])
	for i := 1; i < len(starts); i++ {
		spacing := starts[i].Sub(starts[i-1])
		if spacing < actualMinimumSpacing {
			actualMinimumSpacing = spacing
		}
		require.GreaterOrEqualf(t, spacing, minimumObservedSpacing,
			"upstream starts %d and %d violated configured QPS spacing", i-1, i)
	}
	t.Logf("max_active=%d configured_concurrency=%d minimum_start_spacing=%s configured_spacing=%s",
		refresher.maxActive.Load(), providerConcurrency, actualMinimumSpacing, configuredSpacing)
}

func TestTokenRefreshService_ProductionPathRatesOnlyActualRefreshAfterSameAccountContention(t *testing.T) {
	const interval = 200 * time.Millisecond
	accountOne := grokPoolAccount(71)
	accountOne.Credentials["needs_refresh"] = true
	accountTwo := grokPoolAccount(72)
	accountTwo.Credentials["needs_refresh"] = true
	firstSelection := accountcore.CloneRecord(&accountOne)
	contendingSelection := accountcore.CloneRecord(&accountOne)
	differentSelection := accountcore.CloneRecord(&accountTwo)
	repo := &productionPathRateRepo{accounts: map[int64]*accountcore.Record{
		accountOne.ID: accountcore.CloneRecord(&accountOne),
		accountTwo.ID: accountcore.CloneRecord(&accountTwo),
	}}
	executor := &productionPathRateExecutor{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	tuning := &accountcore.RefreshTuning{MaxRetries: 1}
	svc := &accountcore.BackgroundRefreshOptions{Tuning: tuning, Attempts: backgroundAttemptOptions(repo, tuning)}
	svc.Attempts.API = refreshAPIForFixture(repo, nil)
	svc.Attempts.AttemptTimeout = 2 * time.Second
	poolGate := accountcore.NewRefreshConcurrencyGate(2)
	state := accountcore.NewRefreshProviderState(accountcore.NewRefreshRateGateWithInterval(interval), poolGate, svc.Tuning.FailureThreshold(), IsNonRetryableRefreshError)

	errorsCh := make(chan error, 3)
	go func() {
		errorsCh <- svc.Attempts.Run(context.Background(), firstSelection, executor, executor, time.Hour, state)
	}()
	select {
	case <-executor.firstStarted:
	case <-time.After(time.Second):
		require.FailNow(t, "first production-path refresh did not reach the upstream executor")
	}

	go func() {
		errorsCh <- svc.Attempts.Run(context.Background(), contendingSelection, executor, executor, time.Hour, state)
	}()
	require.Eventually(t, func() bool {
		return poolGate.InFlight() == 2
	}, time.Second, time.Millisecond, "same-account contender must hold the second provider slot while waiting on the local refresh lock")

	go func() {
		errorsCh <- svc.Attempts.Run(context.Background(), differentSelection, executor, executor, time.Hour, state)
	}()
	close(executor.releaseFirst)

	skipped := 0
	for i := 0; i < 3; i++ {
		err := <-errorsCh
		if errors.Is(err, accountcore.ErrRefreshSkipped) {
			skipped++
			continue
		}
		require.NoError(t, err)
	}
	require.Equal(t, 1, skipped, "the same-account contender must reread the refreshed row and skip without upstream admission")

	starts := executor.startsSnapshot()
	require.Len(t, starts, 2, "only the two accounts that actually refresh may consume QPS admission")
	require.Equal(t, int64(71), starts[0].accountID)
	require.Equal(t, int64(72), starts[1].accountID)
	spacing := starts[1].at.Sub(starts[0].at)
	require.GreaterOrEqual(t, spacing, interval-30*time.Millisecond)
	require.Less(t, spacing, 350*time.Millisecond,
		"a same-account lock waiter must not consume a rate slot and push the different-account refresh to the second interval")
	t.Logf("actual_refresh_calls=%d actual_start_spacing=%s configured_spacing=%s", executor.calls.Load(), spacing, interval)
}

func TestTokenRefreshService_ProviderTripBeforeRateAdmissionSkipsWithoutAccountMutation(t *testing.T) {
	account := grokPoolAccount(73)
	stored := accountcore.CloneRecord(&account)
	repo := &breakerTripAccountRepo{productionPathRateRepo: &productionPathRateRepo{
		accounts: map[int64]*accountcore.Record{account.ID: stored},
	}}
	refresher := &poolHealthRefresher{}
	tuning := &accountcore.RefreshTuning{MaxRetries: 1}
	svc := &accountcore.BackgroundRefreshOptions{Tuning: tuning, Attempts: backgroundAttemptOptions(repo, tuning)}
	svc.Attempts.API = refreshAPIForFixture(repo, nil)
	state := accountcore.NewRefreshProviderState(accountcore.NewRefreshRateGate(1), accountcore.NewRefreshConcurrencyGate(1), svc.Tuning.FailureThreshold(), IsNonRetryableRefreshError)
	gate := &tripBeforeRateAdmissionGate{state: state}

	err := svc.Attempts.Run(context.Background(), &account, refresher, refresher, time.Hour, gate)

	require.ErrorIs(t, err, accountcore.ErrRefreshSkipped)
	require.Zero(t, refresher.calls.Load(), "a tripped provider must not reach upstream rate admission")
	require.Zero(t, repo.setErrorCalls.Load())
	require.Zero(t, repo.setTempCalls.Load(), "provider skip must never fall through to per-account cooldown")
}

func TestTokenRefreshService_ConfigBounds(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tuning := &accountcore.RefreshTuning{
		MaxRetries:               maxInt,
		RetryBackoffSeconds:      maxInt,
		ProviderFailureThreshold: maxInt,
		AttemptTimeoutSeconds:    maxInt,
		CycleTimeoutSeconds:      maxInt,
	}

	require.Equal(t, accountcore.MaxTokenRefreshMaxRetries, tuning.Retries())
	require.Equal(t, accountcore.MaxTokenRefreshProviderFailureThreshold, tuning.FailureThreshold())
	require.Equal(t, accountcore.MaxTokenRefreshAttemptTimeout, tuning.AttemptTimeout(0, 0, false))
	require.Equal(t, accountcore.MaxTokenRefreshCycleTimeout, tuning.CycleTimeout())
	require.LessOrEqual(t, tuning.RetryBackoff(1, accountcore.MaxTokenRefreshMaxRetries), accountcore.MaxTokenRefreshRetryBackoff)
	require.Equal(t, 500, accountcore.MaxGrokOAuthReconcilePageSize)
}

func TestTokenRefreshService_AttemptTimeoutStaysInsideDistributedLockLease(t *testing.T) {
	cache := &poolHealthTokenCacheStub{}
	tuning := &accountcore.RefreshTuning{AttemptTimeoutSeconds: int(accountcore.MaxTokenRefreshAttemptTimeout / time.Second)}
	lease, configured := refreshAPIForFixture(&poolHealthAccountRepo{}, cache).LockLease()
	require.Equal(t, 55*time.Second, tuning.AttemptTimeout(0, lease, configured))
	require.Less(t, tuning.AttemptTimeout(0, lease, configured), time.Minute)
}

func TestTokenRefreshService_SharedProviderFailureContainsCycleWithoutAccountMutation(t *testing.T) {
	accounts := make([]accountcore.Record, 0, 5)
	for id := int64(1); id <= 5; id++ {
		accounts = append(accounts, grokPoolAccount(id))
	}
	repo := &poolHealthAccountRepo{pages: map[int64][]accountcore.Record{0: accounts}}
	refresher := &poolHealthRefresher{err: errors.New("invalid_client: provider configuration rejected")}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		MaxRetries:               1,
		CandidatePageSize:        10,
		ProviderConcurrency:      4,
		ProviderQPS:              10000,
		ProviderFailureThreshold: 3,
		AttemptTimeoutSeconds:    1,
		CycleTimeoutSeconds:      2,
	})

	accountcore.NewBackgroundRefreshService(*svc).ScanCycle(context.Background())

	_, _, setErrorCalls, setTempUnschedCalls := repo.snapshot()
	require.Equal(t, int64(1), refresher.calls.Load(), "shared provider configuration failures must open the in-cycle breaker immediately")
	require.Zero(t, setErrorCalls, "shared provider failures must not mass-disable accounts")
	require.Zero(t, setTempUnschedCalls, "shared provider failures must not mutate per-account scheduling state")
}

func TestTokenRefreshService_SharedDBRereadFailureContainsCycleWithoutAccountMutation(t *testing.T) {
	accounts := []accountcore.Record{grokPoolAccount(1), grokPoolAccount(2), grokPoolAccount(3)}
	repo := &poolHealthAccountRepo{
		pages:      map[int64][]accountcore.Record{0: accounts},
		getByIDErr: errors.New("database unavailable"),
	}
	refresher := &poolHealthRefresher{}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		MaxRetries:            3,
		CandidatePageSize:     10,
		ProviderConcurrency:   4,
		ProviderQPS:           10000,
		AttemptTimeoutSeconds: 1,
		CycleTimeoutSeconds:   2,
	})
	svc.Attempts.API = refreshAPIForFixture(repo, nil)

	accountcore.NewBackgroundRefreshService(*svc).ScanCycle(context.Background())

	_, _, setErrorCalls, setTempUnschedCalls := repo.snapshot()
	require.Zero(t, refresher.calls.Load(), "refresh must fail closed before using stale account credentials")
	require.Zero(t, setErrorCalls)
	require.Zero(t, setTempUnschedCalls, "a shared DB outage must not mutate the selected account")
}

func TestTokenRefreshService_GenericGrokForbiddenContainsCycleWithoutAccountMutation(t *testing.T) {
	accounts := []accountcore.Record{grokPoolAccount(1), grokPoolAccount(2), grokPoolAccount(3)}
	repo := &poolHealthAccountRepo{pages: map[int64][]accountcore.Record{0: accounts}}
	refresher := &poolHealthRefresher{err: errors.New(`GROK_OAUTH_ENTITLEMENT_DENIED: token refresh failed: status 403, body: <html>request blocked</html>`)}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		MaxRetries:               1,
		CandidatePageSize:        10,
		ProviderConcurrency:      4,
		ProviderQPS:              10000,
		ProviderFailureThreshold: 3,
		AttemptTimeoutSeconds:    1,
		CycleTimeoutSeconds:      2,
	})

	accountcore.NewBackgroundRefreshService(*svc).ScanCycle(context.Background())

	_, _, setErrorCalls, setTempUnschedCalls := repo.snapshot()
	require.Equal(t, int64(1), refresher.calls.Load(), "an ambiguous Grok 403 must contain the provider immediately")
	require.Zero(t, setErrorCalls, "a generic 403 is not evidence that an account credential is permanently invalid")
	require.Zero(t, setTempUnschedCalls, "provider containment must not mutate account scheduling state")
}

func TestTokenRefreshService_ExplicitGrokEntitlementDenialIsPermanent(t *testing.T) {
	repo := &poolHealthAccountRepo{pages: map[int64][]accountcore.Record{0: {grokPoolAccount(1)}}}
	refresher := &poolHealthRefresher{err: errors.New(`GROK_OAUTH_ENTITLEMENT_DENIED: token refresh failed: status 403, body: {"error":"subscription required"}`)}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		MaxRetries:            1,
		CandidatePageSize:     10,
		ProviderConcurrency:   1,
		ProviderQPS:           10000,
		AttemptTimeoutSeconds: 1,
		CycleTimeoutSeconds:   2,
	})

	accountcore.NewBackgroundRefreshService(*svc).ScanCycle(context.Background())

	_, _, setErrorCalls, setTempUnschedCalls := repo.snapshot()
	require.Equal(t, int64(1), refresher.calls.Load())
	require.Equal(t, 1, setErrorCalls, "explicit entitlement evidence is an account-permanent failure")
	require.Zero(t, setTempUnschedCalls)
}

func TestTokenRefreshService_AttemptTimeoutTripsRetryableProviderThreshold(t *testing.T) {
	accounts := []accountcore.Record{grokPoolAccount(1), grokPoolAccount(2), grokPoolAccount(3)}
	repo := &poolHealthAccountRepo{pages: map[int64][]accountcore.Record{0: accounts}}
	refresher := &poolHealthRefresher{delay: time.Second}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		MaxRetries:               1,
		CandidatePageSize:        10,
		ProviderConcurrency:      1,
		ProviderQPS:              10000,
		ProviderFailureThreshold: 2,
		CycleTimeoutSeconds:      2,
	})
	svc.Attempts.AttemptTimeout = 20 * time.Millisecond

	accountcore.NewBackgroundRefreshService(*svc).ScanCycle(context.Background())

	_, _, setErrorCalls, setTempUnschedCalls := repo.snapshot()
	require.Equal(t, int64(2), refresher.calls.Load(), "two attempt timeouts should trip the retryable provider threshold")
	require.Zero(t, setErrorCalls)
	require.Equal(t, 2, setTempUnschedCalls, "attempt timeouts remain account-transient failures before containment opens")
}

func TestTokenRefreshService_ParentCancellationStopsRetryWithoutAccountMutation(t *testing.T) {
	repo := &poolHealthAccountRepo{}
	ctx, cancel := context.WithCancel(context.Background())
	refresher := &poolHealthRefresher{
		err:    errors.New("temporary provider failure"),
		cancel: cancel,
	}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{
		MaxRetries:            3,
		RetryBackoffSeconds:   1,
		AttemptTimeoutSeconds: 1,
	})
	account := grokPoolAccount(42)

	err := svc.Attempts.Run(ctx, &account, refresher, nil, time.Hour, nil)

	require.ErrorIs(t, err, context.Canceled)
	_, _, setErrorCalls, setTempUnschedCalls := repo.snapshot()
	require.Zero(t, setErrorCalls)
	require.Zero(t, setTempUnschedCalls)
}

func TestTokenRefreshService_LateSuccessPastAttemptDeadlineIsRejected(t *testing.T) {
	repo := &poolHealthAccountRepo{}
	refresher := &poolHealthRefresher{
		delay:         30 * time.Millisecond,
		ignoreContext: true,
	}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{MaxRetries: 1})
	svc.Attempts.AttemptTimeout = 10 * time.Millisecond
	account := grokPoolAccount(43)

	err := svc.Attempts.Run(context.Background(), &account, refresher, nil, time.Hour, nil)

	var timeoutErr *accountcore.RefreshAttemptTimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	_, updatedIDs, setErrorCalls, setTempUnschedCalls := repo.snapshot()
	require.Empty(t, updatedIDs, "credentials returned after the deadline must not be persisted")
	require.Zero(t, setErrorCalls)
	require.Equal(t, 1, setTempUnschedCalls)
}

func TestTokenRefreshService_NonRetryableGrokFailureInvalidatesTokenCache(t *testing.T) {
	repo := &poolHealthAccountRepo{}
	invalidator := &reconcileInvalidator{}
	refresher := &poolHealthRefresher{err: errors.New("invalid_grant: revoked")}
	svc := newPoolHealthService(repo, refresher, accountcore.RefreshTuning{MaxRetries: 1})
	svc.Attempts.Invalidate = invalidator.InvalidateToken
	account := grokPoolAccount(77)

	err := svc.Attempts.Run(context.Background(), &account, refresher, nil, time.Hour, nil)

	require.Error(t, err)
	_, _, setErrorCalls, setTempUnschedCalls := repo.snapshot()
	require.Equal(t, 1, setErrorCalls)
	require.Zero(t, setTempUnschedCalls)
	require.Equal(t, 1, invalidator.count())
}
