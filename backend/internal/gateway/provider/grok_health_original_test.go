//go:build unit

package provider_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

type grokQuotaAccountRepo struct {
	gatewaytestkit.HealthStoreBase
	accountsByID          map[int64]*gatewayprovider.ExecutionAccount
	updates               map[int64]map[string]any
	updateCalls           int
	rateLimitedCalls      int
	lastRateLimitedID     int64
	lastRateLimitResetAt  time.Time
	tempUnschedCalls      int
	lastTempUnschedID     int64
	lastTempUnschedUntil  time.Time
	lastTempUnschedReason string
	recoveryClearCalls    int
	recoveryObservedAt    time.Time
	recoveryObservedReset time.Time
	recoveryClearResult   bool
}

func (r *grokQuotaAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.updateCalls++
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
	r.updates[id] = updates
	if r.accountsByID != nil {
		account := r.accountsByID[id]
		if account == nil {
			return nil
		}
		if account.Record.Extra == nil {
			account.Record.Extra = make(map[string]any)
		}
		for key, value := range updates {
			account.Record.Extra[key] = value
		}
	}
	return nil
}

func (r *grokQuotaAccountRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.lastRateLimitedID = id
	r.lastRateLimitResetAt = resetAt
	return nil
}

func (r *grokQuotaAccountRepo) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	return r.SetRateLimited(ctx, id, resetAt)
}

func (r *grokQuotaAccountRepo) ClearRateLimitIfObserved(_ context.Context, _ int64, observedLimitedAt, observedResetAt time.Time) (bool, error) {
	r.recoveryClearCalls++
	r.recoveryObservedAt = observedLimitedAt
	r.recoveryObservedReset = observedResetAt
	return r.recoveryClearResult, nil
}

func (r *grokQuotaAccountRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempUnschedCalls++
	r.lastTempUnschedID = id
	r.lastTempUnschedUntil = until
	r.lastTempUnschedReason = reason
	return nil
}

// grokPoolPolicyAccountRepo 记录 Grok 池模式错误策略产生的账号状态写入。
type grokPoolPolicyAccountRepo struct {
	*grokQuotaAccountRepo
	setErrorCalls            int
	overloadedCalls          int
	modelRateLimitCalls      int
	lastModelRateLimitScope  string
	lastModelRateLimitReason string
}

func (r *grokPoolPolicyAccountRepo) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrorCalls++
	return nil
}
func (r *grokPoolPolicyAccountRepo) SetOverloaded(_ context.Context, _ int64, _ time.Time) error {
	r.overloadedCalls++
	return nil
}
func (r *grokPoolPolicyAccountRepo) SetModelRateLimit(_ context.Context, _ int64, scope string, _ time.Time, reason ...string) error {
	r.modelRateLimitCalls++
	r.lastModelRateLimitScope = scope
	if len(reason) > 0 {
		r.lastModelRateLimitReason = reason[0]
	}
	return nil
}

// newGrokPoolAccount 返回开启池模式的 Grok API Key 账号。
func newGrokPoolAccount(id int64) *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"pool_mode": true}},
	}
}

// 测试直接组合生产健康组件；传输、凭据和完整网关不参与这些状态断言。
func newGrokHealthForTest(store accountprovider.GrokHealthStore, throttle *accountcore.WriteThrottle) *accountprovider.GrokHealth {
	return &accountprovider.GrokHealth{Store: store, Throttle: throttle, Runtime: accountcore.NewRuntimeBlockState(time.Now), ModelTransient: accountcore.NewModelTransientState(0), NormalizeModel: func(value *accountcore.Record, model string) string {
		return (gatewayprovider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
	}}
}
func newGrokPoolHealthForTest(value *gatewayprovider.ExecutionAccount) (*accountprovider.GrokHealth, *grokPoolPolicyAccountRepo) {
	repo := &grokPoolPolicyAccountRepo{grokQuotaAccountRepo: &grokQuotaAccountRepo{accountsByID: map[int64]*gatewayprovider.ExecutionAccount{value.Record.ID: value}}}
	health := newGrokHealthForTest(repo, nil)
	health.Health = gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: repo, Options: accountcore.HealthOptions{Block: health.Runtime.BlockAccountScheduling}})
	return health, repo
}
func (r *grokQuotaAccountRepo) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	return r.accountsByID[id], nil
}
func handleGrokHealthForTest(health *accountprovider.GrokHealth, ctx context.Context, value *gatewayprovider.ExecutionAccount, status int, headers http.Header, body []byte, models ...string) bool {
	return gatewayprovider.ApplyGrokExecutionHealth(ctx, health, value, status, headers, body, "", models...).StopScheduling
}
func handleGrokHealthWithTeamForTest(health *accountprovider.GrokHealth, team string, ctx context.Context, value *gatewayprovider.ExecutionAccount, status int, headers http.Header, body []byte, models ...string) bool {
	return gatewayprovider.ApplyGrokExecutionHealth(ctx, health, value, status, headers, body, team, models...).StopScheduling
}

type grokHealthTestClock struct{ nanos atomic.Int64 }

func (c *grokHealthTestClock) Now() time.Time {
	if n := c.nanos.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return time.Now()
}
func (c *grokHealthTestClock) Set(now time.Time) { c.nanos.Store(now.UnixNano()) }
func bindGrokHealthClockForTest(health *accountprovider.GrokHealth) *grokHealthTestClock {
	clock := &grokHealthTestClock{}
	health.Runtime = accountcore.NewRuntimeBlockState(clock.Now)
	return clock
}
func bindExpiredGrokHealthForTest(health *accountprovider.GrokHealth, id int64, expired time.Time) {
	clock := bindGrokHealthClockForTest(health)
	clock.Set(expired.Add(-time.Minute))
	health.Runtime.Block(id, expired, "原过期快照夹具")
	clock.nanos.Store(0)
}
func grokInt64PtrForTest(v int64) *int64 { return &v }

const (
	grokQuotaSnapshotExtraKey        = "grok_usage_snapshot"
	grokRateLimitFallbackCooldown    = 2 * time.Minute
	grokRateLimitRepeatCooldown      = 10 * time.Minute
	grokRateLimitSustainedCooldown   = 30 * time.Minute
	grokRateLimitMaxAdaptiveCooldown = time.Hour
	grokRateLimitBackoffQuietPeriod  = time.Hour
	grokSpendingLimitProbeCooldown   = 10 * time.Minute
)

func TestHandleGrokAccountUpstreamErrorPoolModeSkipsDefaultLocalState(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		headers    http.Header
		body       []byte
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized},
		{name: "payment required", statusCode: http.StatusPaymentRequired},
		{name: "forbidden", statusCode: http.StatusForbidden, body: []byte(`{"error":{"message":"access denied"}}`)},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, headers: http.Header{"Retry-After": []string{"60"}}},
		{name: "server error", statusCode: http.StatusInternalServerError},
		{name: "overloaded", statusCode: 529},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := newGrokPoolAccount(int64(620 + index))
			svc, repo := newGrokPoolHealthForTest(account)

			shouldDisable := handleGrokHealthForTest(svc,
				context.Background(),
				account,
				tt.statusCode,
				tt.headers,
				tt.body,
				"grok-4.5",
			)

			require.False(t, shouldDisable)
			require.False(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
			require.Zero(t, repo.rateLimitedCalls)
			require.Zero(t, repo.tempUnschedCalls)
			require.Zero(t, repo.setErrorCalls)
			require.Zero(t, repo.overloadedCalls)
			require.Zero(t, repo.modelRateLimitCalls)
			require.Nil(t, account.Record.RateLimitResetAt)
			require.Nil(t, account.Record.TempUnschedulableUntil)

			if tt.statusCode == http.StatusTooManyRequests {
				require.Equal(t, 1, repo.updateCalls)
				stored, ok := account.Record.Extra[grokQuotaSnapshotExtraKey].(*grok.QuotaSnapshot)
				require.True(t, ok)
				require.NotNil(t, stored.RetryAfterSeconds)
				require.Equal(t, 60, *stored.RetryAfterSeconds)
			}
		})
	}
}

func TestUpdateGrokUsageSnapshotPoolModeExhaustedSuccessIsObservationOnly(t *testing.T) {
	account := newGrokPoolAccount(626)
	svc, repo := newGrokPoolHealthForTest(account)
	resetAt := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	headers := http.Header{
		"X-Ratelimit-Limit-Requests":     []string{"10"},
		"X-Ratelimit-Remaining-Requests": []string{"0"},
		"X-Ratelimit-Reset-Requests":     []string{fmt.Sprintf("%d", resetAt.Unix())},
	}

	svc.ObserveResponse(context.Background(), account.View(), headers, http.StatusOK, "")

	require.Equal(t, 1, repo.updateCalls)
	require.Zero(t, repo.rateLimitedCalls)
	require.False(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
	stored, ok := account.Record.Extra[grokQuotaSnapshotExtraKey].(*grok.QuotaSnapshot)
	require.True(t, ok)
	require.NotNil(t, stored.Requests)
	require.NotNil(t, stored.Requests.Remaining)
	require.Zero(t, *stored.Requests.Remaining)
}

func TestHandleGrokAccountUpstreamErrorPoolModeKeepsExplicitPolicies(t *testing.T) {
	t.Run("custom error code still disables account", func(t *testing.T) {
		account := newGrokPoolAccount(627)
		account.Record.Credentials["custom_error_codes_enabled"] = true
		account.Record.Credentials["custom_error_codes"] = []any{float64(http.StatusUnauthorized)}
		svc, repo := newGrokPoolHealthForTest(account)

		shouldDisable := handleGrokHealthForTest(svc,
			context.Background(),
			account,
			http.StatusUnauthorized,
			nil,
			[]byte(`{"error":{"message":"invalid api key"}}`),
			"grok-4.5",
		)

		require.True(t, shouldDisable)
		require.Equal(t, 1, repo.setErrorCalls)
		require.True(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
	})

	t.Run("custom non-failover code still disables account", func(t *testing.T) {
		account := newGrokPoolAccount(633)
		account.Record.Credentials["custom_error_codes_enabled"] = true
		account.Record.Credentials["custom_error_codes"] = []any{float64(http.StatusUnprocessableEntity)}
		svc, repo := newGrokPoolHealthForTest(account)

		decision := gatewayprovider.ApplyGrokExecutionHealth(context.Background(), svc, account, http.StatusUnprocessableEntity, nil, []byte(`{"error":{"message":"configured"}}`), "", "grok-4.5")

		require.Equal(t, accountcore.ErrorPolicyCustomMatched, decision.Policy)
		require.True(t, decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), http.StatusUnprocessableEntity, false))
		require.False(t, decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), http.StatusUnprocessableEntity))
		require.Equal(t, 1, repo.setErrorCalls)
		require.True(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
	})

	t.Run("matching temporary rule only pauses requested model", func(t *testing.T) {
		account := newGrokPoolAccount(628)
		account.Record.Credentials["temp_unschedulable_enabled"] = true
		account.Record.Credentials["temp_unschedulable_rules"] = []any{
			map[string]any{
				"error_code":       float64(http.StatusServiceUnavailable),
				"keywords":         []any{"maintenance"},
				"duration_minutes": float64(30),
			},
		}
		svc, repo := newGrokPoolHealthForTest(account)

		shouldDisable := handleGrokHealthForTest(svc,
			context.Background(),
			account,
			http.StatusServiceUnavailable,
			nil,
			[]byte(`{"error":{"message":"maintenance in progress"}}`),
			"grok-4.5",
		)

		require.True(t, shouldDisable)
		require.Equal(t, 1, repo.modelRateLimitCalls)
		require.Equal(t, "grok-4.5", repo.lastModelRateLimitScope)
		require.Zero(t, repo.tempUnschedCalls)
		require.False(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
	})

	t.Run("unmatched temporary rule keeps default pool behavior", func(t *testing.T) {
		account := newGrokPoolAccount(629)
		account.Record.Credentials["temp_unschedulable_enabled"] = true
		account.Record.Credentials["temp_unschedulable_rules"] = []any{
			map[string]any{
				"error_code":       float64(http.StatusServiceUnavailable),
				"keywords":         []any{"maintenance"},
				"duration_minutes": float64(30),
			},
		}
		svc, repo := newGrokPoolHealthForTest(account)

		shouldDisable := handleGrokHealthForTest(svc,
			context.Background(),
			account,
			http.StatusServiceUnavailable,
			nil,
			[]byte(`{"error":{"message":"temporary outage"}}`),
			"grok-4.5",
		)

		require.False(t, shouldDisable)
		require.Zero(t, repo.modelRateLimitCalls)
		require.Zero(t, repo.tempUnschedCalls)
		require.False(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
	})
}

func TestHandleGrokAccountUpstreamErrorTempUnschedulesNonRateLimitStates(t *testing.T) {
	tests := []struct {
		name            string
		status          int
		headers         http.Header
		wantReason      string
		wantMinCooldown time.Duration
		wantMaxCooldown time.Duration
	}{
		{
			name:            "unauthorized reauth",
			status:          http.StatusUnauthorized,
			wantReason:      "grok credentials unauthorized",
			wantMinCooldown: 10*time.Minute - time.Second,
			wantMaxCooldown: 10*time.Minute + time.Second,
		},
		{
			name:            "forbidden entitlement",
			status:          http.StatusForbidden,
			wantReason:      "grok access or entitlement denied",
			wantMinCooldown: 30*time.Minute - time.Second,
			wantMaxCooldown: 30*time.Minute + time.Second,
		},
		{
			name:            "payment required",
			status:          http.StatusPaymentRequired,
			wantReason:      "grok payment required",
			wantMinCooldown: 30*time.Minute - time.Second,
			wantMaxCooldown: 30*time.Minute + time.Second,
		},
		{
			name:            "method not allowed",
			status:          http.StatusMethodNotAllowed,
			wantReason:      "grok endpoint not supported (405)",
			wantMinCooldown: 30*time.Minute - time.Second,
			wantMaxCooldown: 30*time.Minute + time.Second,
		},
		{
			name:            "upstream temporary error",
			status:          http.StatusInternalServerError,
			wantReason:      "grok upstream temporary error",
			wantMinCooldown: 2*time.Minute - time.Second,
			wantMaxCooldown: 2*time.Minute + time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 61, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
			repo := &grokQuotaAccountRepo{}
			svc := newGrokHealthForTest(repo, nil)
			before := time.Now()

			handleGrokHealthForTest(svc, context.Background(), account, tt.status, tt.headers, nil)

			require.True(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
			require.Equal(t, 1, repo.tempUnschedCalls)
			require.Zero(t, repo.rateLimitedCalls)
			require.Equal(t, account.Record.ID, repo.lastTempUnschedID)
			require.Equal(t, tt.wantReason, repo.lastTempUnschedReason)
			require.True(t, repo.lastTempUnschedUntil.After(before.Add(tt.wantMinCooldown)))
			require.True(t, repo.lastTempUnschedUntil.Before(before.Add(tt.wantMaxCooldown)))
		})
	}
}

func TestHandleGrokAccountUpstreamErrorSpendingLimit403RateLimits(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 614, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	before := time.Now()
	body := []byte(`{"code":"personal-team-blocked:spending-limit","error":"You have run out of credits"}`)

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusForbidden, nil, body)

	require.True(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, account.Record.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, before.Add(grokSpendingLimitProbeCooldown), repo.lastRateLimitResetAt, 2*time.Second)
	require.Zero(t, repo.tempUnschedCalls)
	require.True(t, grok.IsSpendingLimitError(body))
}

func TestHandleGrokAccountUpstreamError5xxRespectsPoolMode(t *testing.T) {
	t.Run("pool mode keeps scheduling state", func(t *testing.T) {
		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 611,
			Platform: capability.PlatformGrok,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"pool_mode": true,
			}},
		}
		repo := &grokQuotaAccountRepo{}
		svc := newGrokHealthForTest(repo, nil)

		handleGrokHealthForTest(svc, context.Background(), account, http.StatusBadGateway, nil, nil)

		require.False(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
		require.Zero(t, repo.tempUnschedCalls)
		require.Nil(t, account.Record.TempUnschedulableUntil)
		require.Empty(t, account.Record.TempUnschedulableReason)
	})

	t.Run("non-pool mode keeps two minute cooldown", func(t *testing.T) {
		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 612, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey}}
		repo := &grokQuotaAccountRepo{}
		svc := newGrokHealthForTest(repo, nil)
		before := time.Now()

		handleGrokHealthForTest(svc, context.Background(), account, http.StatusBadGateway, nil, nil)

		require.True(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
		require.Equal(t, 1, repo.tempUnschedCalls)
		require.Equal(t, account.Record.ID, repo.lastTempUnschedID)
		require.Equal(t, "grok upstream temporary error", repo.lastTempUnschedReason)
		require.WithinDuration(t, before.Add(2*time.Minute), repo.lastTempUnschedUntil, time.Second)
	})
}

func TestHandleGrokAccountUpstreamError405RespectsPoolMode(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 613,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode": true,
		}},
	}
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusMethodNotAllowed, nil, nil)

	require.False(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }), "公共池账号应跳过 405 默认冷却")
	require.Zero(t, repo.tempUnschedCalls)
	require.Nil(t, account.Record.TempUnschedulableUntil)
}

func TestHandleGrokAccountUpstreamError429SetsRateLimitedFromRetryAfter(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 61, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	before := time.Now()

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"45"}}, nil)

	require.True(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, account.Record.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, before.Add(45*time.Second), repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
}

func TestHandleGrokAccountUpstreamError402RecoversAfterCooldownExpiry(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 610, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Schedulable: true},
	}
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusPaymentRequired, nil, nil)
	require.True(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
	require.Equal(t, 1, repo.tempUnschedCalls)

	expired := time.Now().Add(-time.Second)
	account.Record.TempUnschedulableUntil = &expired
	bindExpiredGrokHealthForTest(svc, account.Record.ID, expired)

	require.False(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
	require.True(t, account.View().IsSchedulable())
}

func TestHandleGrokAccountUpstreamError429UsesLatestExhaustedWindowReset(t *testing.T) {
	now := time.Now()
	requestReset := now.Add(10 * time.Minute).Truncate(time.Second)
	tokenReset := now.Add(20 * time.Minute).Truncate(time.Second)
	headers := http.Header{
		"X-Ratelimit-Limit-Requests":     []string{"10"},
		"X-Ratelimit-Remaining-Requests": []string{"0"},
		"X-Ratelimit-Reset-Requests":     []string{fmt.Sprintf("%d", requestReset.Unix())},
		"X-Ratelimit-Limit-Tokens":       []string{"1000"},
		"X-Ratelimit-Remaining-Tokens":   []string{"0"},
		"X-Ratelimit-Reset-Tokens":       []string{fmt.Sprintf("%d", tokenReset.Unix())},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 62, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusTooManyRequests, headers, nil)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, tokenReset, repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
}

func TestHandleGrokAccountUpstreamError429UsesFallbackReset(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 63, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	before := time.Now()

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusTooManyRequests, nil, nil)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, before.Add(grokRateLimitFallbackCooldown), repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
}

func TestGrokRateLimitResetAtForAccountEscalatesRepeated429s(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	retryAfter := 45
	snapshot := &grok.QuotaSnapshot{
		StatusCode:        http.StatusTooManyRequests,
		RetryAfterSeconds: &retryAfter,
		UpdatedAt:         now.Format(time.RFC3339),
	}
	tests := []struct {
		name             string
		previousCooldown time.Duration
		wantCooldown     time.Duration
	}{
		{name: "repeat after short boundary", previousCooldown: 45 * time.Second, wantCooldown: grokRateLimitRepeatCooldown},
		{name: "sustained repeat", previousCooldown: grokRateLimitRepeatCooldown, wantCooldown: grokRateLimitSustainedCooldown},
		{name: "capped repeat", previousCooldown: grokRateLimitSustainedCooldown, wantCooldown: grokRateLimitMaxAdaptiveCooldown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previousReset := now.Add(-time.Second)
			previousLimited := previousReset.Add(-tt.previousCooldown)
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 630,
				Platform:         capability.PlatformGrok,
				Type:             capability.AccountTypeOAuth,
				RateLimitedAt:    &previousLimited,
				RateLimitResetAt: &previousReset},
			}

			resetAt, limited := accountcore.GrokRateLimitResetAtForAccount(account.View(), snapshot, now)

			require.True(t, limited)
			require.WithinDuration(t, now.Add(tt.wantCooldown), resetAt, time.Second)
		})
	}
}

func TestGrokRateLimitResetAtForAccountPreservesAuthoritativeAndQuietRecovery(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	retryAfter := 45
	previousReset := now.Add(-grokRateLimitBackoffQuietPeriod - time.Second)
	previousLimited := previousReset.Add(-grokRateLimitSustainedCooldown)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 631,
		Platform:         capability.PlatformGrok,
		Type:             capability.AccountTypeOAuth,
		RateLimitedAt:    &previousLimited,
		RateLimitResetAt: &previousReset},
	}
	snapshot := &grok.QuotaSnapshot{
		StatusCode:        http.StatusTooManyRequests,
		RetryAfterSeconds: &retryAfter,
		UpdatedAt:         now.Format(time.RFC3339),
	}

	resetAt, limited := accountcore.GrokRateLimitResetAtForAccount(account.View(), snapshot, now)
	require.True(t, limited)
	require.WithinDuration(t, now.Add(45*time.Second), resetAt, time.Second)

	authoritativeReset := now.Add(2 * time.Hour)
	remaining := int64(0)
	snapshot.Requests = &grok.QuotaWindow{Remaining: &remaining, ResetUnix: grokInt64PtrForTest(authoritativeReset.Unix())}
	recentReset := now.Add(-time.Second)
	recentLimited := recentReset.Add(-grokRateLimitSustainedCooldown)
	account.Record.RateLimitResetAt = &recentReset
	account.Record.RateLimitedAt = &recentLimited

	resetAt, limited = accountcore.GrokRateLimitResetAtForAccount(account.View(), snapshot, now)
	require.True(t, limited)
	require.WithinDuration(t, authoritativeReset, resetAt, time.Second)
}

func TestGrokRateLimitResetAtForAccountLeavesAPIKey429PolicyUnchanged(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	retryAfter := 45
	previousReset := now.Add(-time.Second)
	previousLimited := previousReset.Add(-grokRateLimitSustainedCooldown)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 632,
		Platform:         capability.PlatformGrok,
		Type:             capability.AccountTypeAPIKey,
		RateLimitedAt:    &previousLimited,
		RateLimitResetAt: &previousReset},
	}
	snapshot := &grok.QuotaSnapshot{
		StatusCode:        http.StatusTooManyRequests,
		RetryAfterSeconds: &retryAfter,
		UpdatedAt:         now.Format(time.RFC3339),
	}

	resetAt, limited := accountcore.GrokRateLimitResetAtForAccount(account.View(), snapshot, now)
	require.True(t, limited)
	require.WithinDuration(t, now.Add(45*time.Second), resetAt, time.Second)
}

func TestGrokRateLimitResetAtUsesFutureWindowAfterRetryAfterExpires(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	observedAt := now.Add(-2 * time.Minute)
	windowReset := now.Add(15 * time.Minute)
	retryAfter := 30
	snapshot := &grok.QuotaSnapshot{
		StatusCode:        http.StatusTooManyRequests,
		UpdatedAt:         observedAt.Format(time.RFC3339),
		RetryAfterSeconds: &retryAfter,
		Requests: &grok.QuotaWindow{
			Limit:     grokInt64PtrForTest(10),
			Remaining: grokInt64PtrForTest(0),
			ResetUnix: grokInt64PtrForTest(windowReset.Unix()),
		},
	}

	resetAt, limited := accountcore.GrokRateLimitResetAt(snapshot, now)

	require.True(t, limited)
	require.WithinDuration(t, windowReset, resetAt, time.Second)
}

func TestHandleGrokAccountUpstreamError429DoesNotShortenExistingPause(t *testing.T) {
	existingUntil := time.Now().Add(15 * time.Minute)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 64,
		Platform:                capability.PlatformGrok,
		Type:                    capability.AccountTypeOAuth,
		TempUnschedulableUntil:  &existingUntil,
		TempUnschedulableReason: "existing pause"},
	}
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	clock := bindGrokHealthClockForTest(svc)

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"45"}}, nil)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, time.Now().Add(45*time.Second), repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
	clock.Set(existingUntil.Add(-time.Second))
	require.True(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
	clock.Set(existingUntil.Add(time.Second))
	require.False(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
}

func TestUpdateGrokUsageSnapshotExhaustedSuccessBypassesThrottleAndSetsRateLimited(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 65, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, accountcore.NewWriteThrottle(time.Hour))
	now := time.Now()

	// 先消耗普通快照的写入额度。
	svc.StoreSnapshot(context.Background(), account.View(), &grok.QuotaSnapshot{
		StatusCode: http.StatusOK,
		Requests: &grok.QuotaWindow{
			Limit:     grokInt64PtrForTest(10),
			Remaining: grokInt64PtrForTest(9),
		},
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}, true, "")
	resetAt := now.Add(30 * time.Minute).Truncate(time.Second)
	svc.StoreSnapshot(context.Background(), account.View(), &grok.QuotaSnapshot{
		StatusCode: http.StatusOK,
		Requests: &grok.QuotaWindow{
			Limit:     grokInt64PtrForTest(10),
			Remaining: grokInt64PtrForTest(0),
			ResetUnix: grokInt64PtrForTest(resetAt.Unix()),
			ResetAt:   resetAt.UTC().Format(time.RFC3339),
		},
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}, true, "")

	require.Equal(t, 2, repo.updateCalls)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, account.Record.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, resetAt, repo.lastRateLimitResetAt, time.Second)
	require.True(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
}

func TestUpdateGrokUsageSnapshotAvailableSuccessDoesNotSetRateLimited(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 66, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}

	svc.StoreSnapshot(context.Background(), account.View(), &grok.QuotaSnapshot{
		StatusCode: http.StatusOK,
		Requests: &grok.QuotaWindow{
			Limit:     grokInt64PtrForTest(10),
			Remaining: grokInt64PtrForTest(1),
		},
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}, true, "")

	require.Equal(t, 1, repo.updateCalls)
	require.Zero(t, repo.rateLimitedCalls)
}

func TestUpdateGrokUsageFromResponseHeaderlessSuccessClearsObservedCooldown(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	limitedAt := now.Add(-grokRateLimitRepeatCooldown)
	observedResetAt := now.Add(-time.Second)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 660,
		Platform:         capability.PlatformGrok,
		Type:             capability.AccountTypeOAuth,
		RateLimitedAt:    &limitedAt,
		RateLimitResetAt: &observedResetAt},
	}
	repo := &grokQuotaAccountRepo{recoveryClearResult: true}
	svc := newGrokHealthForTest(repo, accountcore.NewWriteThrottle(time.Hour))

	svc.ObserveResponse(context.Background(), account.View(), nil, http.StatusOK, "")

	require.Zero(t, repo.updateCalls, "headerless success must not overwrite an informative quota snapshot")
	require.Equal(t, 1, repo.recoveryClearCalls)
	require.Equal(t, limitedAt, repo.recoveryObservedAt)
	require.Equal(t, observedResetAt, repo.recoveryObservedReset)
	require.Same(t, &observedResetAt, account.Record.RateLimitResetAt, "shared account snapshots must not be mutated in place")
}

func TestUpdateGrokUsageFromResponseRecoveryRespectsCancellationAndAPIKeyBoundary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	observedResetAt := now.Add(-time.Second)
	observedLimitedAt := observedResetAt.Add(-grokRateLimitRepeatCooldown)

	t.Run("parent cancellation does not mutate account state", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 661,
			Platform:         capability.PlatformGrok,
			Type:             capability.AccountTypeOAuth,
			RateLimitedAt:    &observedLimitedAt,
			RateLimitResetAt: &observedResetAt},
		}
		repo := &grokQuotaAccountRepo{recoveryClearResult: true}
		svc := newGrokHealthForTest(repo, nil)

		svc.ObserveResponse(ctx, account.View(), nil, http.StatusOK, "")

		require.Zero(t, repo.recoveryClearCalls)
	})

	t.Run("API key success does not alter OAuth cooldown state", func(t *testing.T) {
		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 662,
			Platform:         capability.PlatformGrok,
			Type:             capability.AccountTypeAPIKey,
			RateLimitedAt:    &observedLimitedAt,
			RateLimitResetAt: &observedResetAt},
		}
		repo := &grokQuotaAccountRepo{recoveryClearResult: true}
		svc := newGrokHealthForTest(repo, nil)

		svc.ObserveResponse(context.Background(), account.View(), nil, http.StatusOK, "")

		require.Zero(t, repo.recoveryClearCalls)
	})
}

func TestUpdateGrokUsageSnapshotExhaustedSuccessWithoutResetUsesFallback(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 67, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	before := time.Now()

	svc.StoreSnapshot(context.Background(), account.View(), &grok.QuotaSnapshot{
		StatusCode: http.StatusOK,
		Tokens: &grok.QuotaWindow{
			Limit:     grokInt64PtrForTest(2_000_000),
			Remaining: grokInt64PtrForTest(0),
		},
		UpdatedAt: before.UTC().Format(time.RFC3339),
	}, true, "")

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, before.Add(grokRateLimitFallbackCooldown), repo.lastRateLimitResetAt, time.Second)
	stored, ok := repo.updates[account.Record.ID][grokQuotaSnapshotExtraKey].(*grok.QuotaSnapshot)
	require.True(t, ok)
	require.NotNil(t, stored.Tokens.ResetUnix)
	paused, _ := accountcore.GrokQuotaWindowAutoPause("tokens", stored.Tokens, before.Add(time.Second))
	require.True(t, paused)
	paused, _ = accountcore.GrokQuotaWindowAutoPause("tokens", stored.Tokens, repo.lastRateLimitResetAt.Add(time.Second))
	require.False(t, paused)
}

func TestHandleGrokAccountUpstreamErrorEntitlement403KeepsDefaultCooldown(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4716, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	before := time.Now()

	handleGrokHealthForTest(svc,
		context.Background(), account, http.StatusForbidden, nil,
		[]byte(`{"error":{"message":"subscription required"}}`),
	)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Equal(t, "grok access or entitlement denied", repo.lastTempUnschedReason)
	require.Greater(t, repo.lastTempUnschedUntil, before.Add(29*time.Minute))
	require.Less(t, repo.lastTempUnschedUntil, before.Add(31*time.Minute))
}

func TestHandleGrokAccountUpstreamError403UsesConfiguredRule(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4717,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{
				map[string]any{
					"error_code":       float64(http.StatusForbidden),
					"keywords":         []any{"subscription"},
					"duration_minutes": float64(7),
				},
			},
		}},
	}
	before := time.Now()

	handleGrokHealthForTest(svc,
		context.Background(), account, http.StatusForbidden, nil,
		[]byte(`{"error":{"message":"subscription required"}}`),
	)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Greater(t, repo.lastTempUnschedUntil, before.Add(6*time.Minute))
	require.Less(t, repo.lastTempUnschedUntil, before.Add(8*time.Minute))
}

func TestHandleGrokAccountUpstreamError403ConfiguredUnmatchedKeepsDefaultCooldown(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4718,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{
				map[string]any{
					"error_code":       float64(http.StatusForbidden),
					"keywords":         []any{"different failure"},
					"duration_minutes": float64(7),
				},
			},
		}},
	}

	handleGrokHealthForTest(svc,
		context.Background(), account, http.StatusForbidden, nil,
		[]byte(`{"error":{"message":"subscription required"}}`),
	)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Equal(t, "grok access or entitlement denied", repo.lastTempUnschedReason)
	require.True(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
}

func TestHandleGrokAccountUpstreamError_FreeUsageBodyCoolsAccount(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9101, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	before := time.Now()
	body := []byte(`{"error":{"code":"subscription:free-usage-exhausted","message":"You've used all the included free usage. Usage resets over a rolling 24-hour window."}}`)

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusBadRequest, nil, body)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Equal(t, "grok free usage exhausted", repo.lastTempUnschedReason)
	// 滚动窗口耗尽且缺少上游绝对重置时间时必须使用短期探测冷却，
	// 不得在此启动 24 小时锁定。
	require.Greater(t, repo.lastTempUnschedUntil, before.Add(grok.GrokFreeUsageProbeCooldown-time.Second))
	require.Less(t, repo.lastTempUnschedUntil, before.Add(grok.GrokFreeUsageProbeCooldown+time.Second))
}

func TestHandleGrokAccountUpstreamError_FreeUsageUsesUpstreamReset(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9102, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	body := []byte(`{"error":{"code":"subscription:free-usage-exhausted","message":"free usage exhausted; rolling 24-hour window"}}`)

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusTooManyRequests,
		http.Header{"Retry-After": []string{"3600"}}, body)

	require.Zero(t, repo.tempUnschedCalls)
	require.WithinDuration(t, time.Now().Add(time.Hour), repo.lastRateLimitResetAt, 2*time.Second)
}

func TestHandleGrokAccountUpstreamError_EmptyOutputCoolsAccount(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9102, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	before := time.Now()

	handleGrokHealthForTest(svc,
		context.Background(), account, http.StatusBadGateway, nil,
		[]byte(`empty model output: no content/tool_calls`),
	)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Equal(t, "grok empty model output", repo.lastTempUnschedReason)
	require.WithinDuration(t, before.Add(4*time.Minute), repo.lastTempUnschedUntil, time.Second)
}

func TestHandleGrokAccountUpstreamError_MultiAgentCapacityBlocksOnlyThatModel(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9120, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	ctx := context.Background()

	handleGrokHealthWithTeamForTest(svc, "grok-4.20-multi-agent-0309",
		ctx, account, http.StatusBadGateway, nil,
		[]byte(`{"error":{"message":"engine_overloaded"}}`),
	)

	require.Zero(t, repo.tempUnschedCalls)
	require.True(t, accountcore.IsGrokModelQuotaBlocked(account.Record.ID, "grok-4.20-multi-agent-0309", time.Now()))
	require.False(t, accountcore.IsGrokModelQuotaBlocked(account.Record.ID, "grok-4.5", time.Now()))
}

func TestHandleGrokAccountUpstreamError_CapacityNeverCoolsAccount(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9121, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	ctx := context.Background()

	handleGrokHealthWithTeamForTest(svc, "grok-4.6", ctx, account, http.StatusTooManyRequests, nil,
		[]byte(`{"error":{"message":"The model is currently at capacity due to high demand"}}`))

	require.Zero(t, repo.tempUnschedCalls)
	require.False(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
}

func TestHandleGrokAccountUpstreamError_FreeUsageDoesNotCoolPoolMode(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9103,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode": true,
		}},
	}
	body := []byte(`{"error":{"code":"subscription:free-usage-exhausted","message":"free usage exhausted"}}`)

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusBadRequest, nil, body)

	require.Zero(t, repo.tempUnschedCalls)
	require.False(t, svc.Runtime.Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(account.View()) }))
}

func TestHandleGrokAccountUpstreamError_ContentPolicyStillNoMutation(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9104, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	body := []byte(`{"error":{"code":"new_sensitive","message":"text is sensitive"}}`)

	handleGrokHealthForTest(svc, context.Background(), account, http.StatusForbidden, nil, body)

	require.Zero(t, repo.tempUnschedCalls)
}

func TestHandleGrokAccountUpstreamError_Entitlement403Unchanged(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := newGrokHealthForTest(repo, nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9105, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	before := time.Now()

	handleGrokHealthForTest(svc,
		context.Background(), account, http.StatusForbidden, nil,
		[]byte(`{"error":{"message":"subscription required"}}`),
	)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Equal(t, "grok access or entitlement denied", repo.lastTempUnschedReason)
	require.Greater(t, repo.lastTempUnschedUntil, before.Add(29*time.Minute))
	require.Less(t, repo.lastTempUnschedUntil, before.Add(31*time.Minute))
}
