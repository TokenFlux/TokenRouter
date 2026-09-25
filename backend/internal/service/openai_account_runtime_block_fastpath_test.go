//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type oauth429RateLimitRepo struct {
	mockAccountRepoForGemini
	setRateLimitedCalls       int
	lastRateLimitedUntil      time.Time
	setModelRateLimitCalls    int
	lastModelRateLimitKey     string
	lastModelRateLimitedUntil time.Time
}

func (r *oauth429RateLimitRepo) SetRateLimited(_ context.Context, _ int64, until time.Time) error {
	r.setRateLimitedCalls++
	r.lastRateLimitedUntil = until
	return nil
}

func (r *oauth429RateLimitRepo) SetModelRateLimit(_ context.Context, _ int64, scope string, until time.Time, _ ...string) error {
	r.setModelRateLimitCalls++
	r.lastModelRateLimitKey = scope
	r.lastModelRateLimitedUntil = until
	return nil
}

func TestOpenAI429FastPath_KeepsOAuthAccountSchedulableDuringRetryWindow(t *testing.T) {
	repo := &oauth429RateLimitRepo{}
	var svc *OpenAIGatewayService

	rateLimits := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	svc = withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: rateLimits})
	rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return svc.ShouldRetryOpenAIOAuth429(gatewayprovider.NewExecutionAccount(v), h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 42, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	apiKeyAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 43, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	setupTokenAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 44, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeSetupToken}}
	grokOAuthAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 45, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusTooManyRequests, http.Header{}, nil, false).StopScheduling
	apiKeyShouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, apiKeyAccount, http.StatusTooManyRequests, http.Header{}, nil, false).StopScheduling

	require.False(t, shouldDisable)
	require.False(t, apiKeyShouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(apiKeyAccount), "API-key 429 keeps the existing scheduler cooldown behavior")
	require.Equal(t, 1, repo.setRateLimitedCalls, "only the API-key 429 should persist a scheduler block")
	require.True(t, svc.shouldRetryOpenAIOAuth429OnSameAccount(account, http.StatusTooManyRequests, false))
	require.True(t, svc.shouldRetryOpenAIOAuth429OnSameAccount(setupTokenAccount, http.StatusTooManyRequests, false))
	require.False(t, svc.shouldRetryOpenAIOAuth429OnSameAccount(apiKeyAccount, http.StatusTooManyRequests, false))
	require.False(t, svc.shouldRetryOpenAIOAuth429OnSameAccount(grokOAuthAccount, http.StatusTooManyRequests, false))
	require.WithinDuration(t, time.Now().Add(accountcore.RuntimeRetryWindow), svc.responseOutput.Health.RetryDeadline(account.View()), time.Second)
	require.WithinDuration(t, time.Now().Add(accountcore.RuntimeRetryWindow), svc.responseOutput.Health.RetryDeadline(setupTokenAccount.View()), time.Second)
}

func TestOpenAI429FastPath_BlocksOAuthImmediatelyWhenSevenDayQuotaIsExhausted(t *testing.T) {
	repo := &oauth429RateLimitRepo{}
	var svc *OpenAIGatewayService

	rateLimits := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	svc = withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: rateLimits})
	rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return svc.ShouldRetryOpenAIOAuth429(gatewayprovider.NewExecutionAccount(v), h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 423, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "20")
	headers.Set("x-codex-secondary-reset-after-seconds", "3600")
	headers.Set("x-codex-secondary-window-minutes", "300")

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusTooManyRequests, headers, []byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`), false).StopScheduling

	require.False(t, shouldDisable)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Equal(t, 1, repo.setRateLimitedCalls)
	require.Greater(t, time.Until(repo.lastRateLimitedUntil), 6*24*time.Hour)
	require.False(t, svc.ShouldRetryOpenAIOAuth429(account, headers, nil))
}

func TestOpenAI429FastPath_RetriesOAuthWhenNoQuotaSignalExists(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 424, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	headers := http.Header{"Retry-After": []string{"1"}}

	require.True(t, svc.ShouldRetryOpenAIOAuth429(account, headers, []byte(`{"error":{"type":"rate_limit_error","message":"try again"}}`)))
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIStream429IgnoresSuccessfulQuotaSnapshotHeaders(t *testing.T) {
	repo := &oauth429RateLimitRepo{}
	var svc *OpenAIGatewayService

	rateLimits := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	svc = withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: rateLimits})
	rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return svc.ShouldRetryOpenAIOAuth429(gatewayprovider.NewExecutionAccount(v), h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 421, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	clock := expireRuntimeRetryForTest(svc, account.Record.ID)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "37")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	payload := []byte(`{"type":"error","error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`)

	status, disabled := svc.responseOutput.TerminalAccountEffects(nil, account, payload, "slow down", headers)

	require.Equal(t, http.StatusTooManyRequests, status)
	require.False(t, disabled)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	clock.Set(time.Now().Add(time.Minute))
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account), "流内 429 不得继承七天快照")
	if !repo.lastRateLimitedUntil.IsZero() {
		require.Less(t, time.Until(repo.lastRateLimitedUntil), time.Minute)
	}
}

func TestOpenAI429FastPath_SparkQuotaOnlyBlocksSparkModel(t *testing.T) {
	repo := &oauth429RateLimitRepo{}
	var svc *OpenAIGatewayService

	rateLimits := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	svc = withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: rateLimits})
	rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return svc.ShouldRetryOpenAIOAuth429(gatewayprovider.NewExecutionAccount(v), h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 425, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "20")
	headers.Set("x-codex-secondary-reset-after-seconds", "3600")
	headers.Set("x-codex-secondary-window-minutes", "300")

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusTooManyRequests, headers, []byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`), false, "gpt-5.3-codex-spark").StopScheduling

	require.False(t, shouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Zero(t, repo.setRateLimitedCalls)
	require.Equal(t, 1, repo.setModelRateLimitCalls)
	require.Equal(t, "gpt-5.3-codex-spark", repo.lastModelRateLimitKey)
	require.Greater(t, time.Until(repo.lastModelRateLimitedUntil), 6*24*time.Hour)
}

func TestOpenAIStreamFailover_Spark429KeepsModelScope(t *testing.T) {
	repo := &oauth429RateLimitRepo{}
	var svc *OpenAIGatewayService

	rateLimits := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	svc = withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: rateLimits})
	rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return svc.ShouldRetryOpenAIOAuth429(gatewayprovider.NewExecutionAccount(v), h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 432, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "20")
	headers.Set("x-codex-secondary-reset-after-seconds", "3600")
	headers.Set("x-codex-secondary-window-minutes", "300")
	payload := []byte(`{"type":"error","error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`)

	failoverErr := svc.responseOutput.NewStreamFailureWithModel(
		nil, account, false, "", payload, "quota exhausted", "gpt-5.3-codex-spark", headers,
	)

	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Zero(t, repo.setRateLimitedCalls)
	require.Equal(t, 1, repo.setModelRateLimitCalls)
	require.Equal(t, "gpt-5.3-codex-spark", repo.lastModelRateLimitKey)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAI429FastPath_OpenCodeGoUsageLimitUsesMessageResetDuration(t *testing.T) {
	repo := &rateLimit429AccountRepoStub{}
	var svc *OpenAIGatewayService

	healthObserver := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	svc = withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: healthObserver})
	healthObserver.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return svc.ShouldRetryOpenAIOAuth429(gatewayprovider.NewExecutionAccount(v), h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 44, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	body := []byte(`{"type":"error","error":{"type":"GoUsageLimitError","message":"5-hour usage limit reached. Resets in 4hr 59min. To continue using this model now, enable usage from your available balance: https://opencode.ai/workspace/wrk_test/go"},"metadata":{"workspace":"wrk_test","limitName":"5 hour"}}`)

	before := time.Now()
	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusTooManyRequests, http.Header{}, body, false).StopScheduling
	after := time.Now()

	require.False(t, shouldDisable)
	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, account.Record.ID, repo.lastRateLimitID)
	expectedResetAfter := 4*time.Hour + 59*time.Minute
	require.False(t, repo.lastRateLimitReset.Before(before.Add(expectedResetAfter-time.Second)))
	require.False(t, repo.lastRateLimitReset.After(after.Add(expectedResetAfter)))
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBlock_AppliesToOpenAIAPIKeyWhenRateLimitServiceStopsScheduling(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 44, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	svc.BlockAccountScheduling(account, time.Time{}, "custom_error_code")

	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBlock_DoesNotApplyToOtherPlatforms(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 45, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth}}

	svc.BlockAccountScheduling(account, time.Time{}, "custom_error_code")

	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBlocker_IgnoresNonOpenAIFromRateLimitService(t *testing.T) {
	gateway := withSchedulerParametersForTest(&OpenAIGatewayService{})
	repo := &gatewaytestkit.HealthStoreRecorder{}

	healthObserver := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		gateway.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)
	healthObserver.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return gateway.ShouldRetryOpenAIOAuth429(gatewayprovider.NewExecutionAccount(v), h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 45, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), healthObserver, account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte("forbidden"), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIPoolModeRetryable5xx_DoesNotCreateModelTransientBlock(t *testing.T) {
	repo := &gatewaytestkit.ErrorPolicyStore{}
	healthObserver := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{}, nil)

	gateway := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: healthObserver})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 47,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(524)},
		}},
	}

	for i := 0; i < 2; i++ {
		shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), gateway.responseOutput.Health, account, 524, http.Header{}, []byte(`{"error":{"message":"upstream timeout"}}`), false, "gpt-5.4").StopScheduling
		require.False(t, shouldDisable)
	}

	require.False(t, (gateway.isOpenAIAccountRuntimeBlocked(account) || gateway.getOpenAIAccountModelTransientState().IsBlocked(account.Record.ID, accountcore.NormalizeTransientModel(gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel("gpt-5.4")), time.Now())))
}

func TestOpenAIPoolModeNonRetryable5xx_DoesNotCreateModelTransientBlock(t *testing.T) {
	repo := &gatewaytestkit.ErrorPolicyStore{}
	healthObserver := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{}, nil)

	gateway := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: healthObserver})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 48,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(http.StatusGatewayTimeout)},
		}},
	}

	for i := 0; i < 2; i++ {
		shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), gateway.responseOutput.Health, account, http.StatusServiceUnavailable, http.Header{}, []byte(`{"error":{"message":"upstream unavailable"}}`), false, "gpt-5.4").StopScheduling
		require.False(t, shouldDisable)
	}

	require.False(t, (gateway.isOpenAIAccountRuntimeBlocked(account) || gateway.getOpenAIAccountModelTransientState().IsBlocked(account.Record.ID, accountcore.NormalizeTransientModel(gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel("gpt-5.4")), time.Now())))
}

func TestOpenAINonPoolAPIKey5xx_StillCreatesModelTransientBlock(t *testing.T) {
	repo := &gatewaytestkit.ErrorPolicyStore{}
	healthObserver := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{}, nil)

	gateway := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: healthObserver})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 49,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey},
	}

	for i := 0; i < 2; i++ {
		shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), gateway.responseOutput.Health, account, http.StatusGatewayTimeout, http.Header{}, []byte(`{"error":{"message":"upstream timeout"}}`), false, "gpt-5.4").StopScheduling
		require.False(t, shouldDisable)
	}

	require.True(t, (gateway.isOpenAIAccountRuntimeBlocked(account) || gateway.getOpenAIAccountModelTransientState().IsBlocked(account.Record.ID, accountcore.NormalizeTransientModel(gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel("gpt-5.4")), time.Now())))
}

func TestOpenAIModelNotFound_DoesNotRuntimeBlockWholeAccount(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),
	})
	account := gatewaytestkit.ModelNotFoundAccount()

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusNotFound, http.Header{}, []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`), false, "gpt-5.4").StopScheduling

	require.True(t, shouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Zero(t, repo.TempCalls)
	require.Len(t, repo.ModelRateLimitCalls, 1)
}

func TestOpenAIModelTempUnschedulable_DoesNotRuntimeBlockWholeAccount(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),
	})
	account := gatewaytestkit.ModelNotFoundAccount()

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), false, "gpt-5.4").StopScheduling

	require.True(t, shouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Zero(t, repo.TempCalls)
	require.Len(t, repo.ModelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.ModelRateLimitCalls[0].Scope)
}

func TestOpenAIModelTempUnschedulable_WriteFailureDoesNotRuntimeBlockWholeAccount(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{ModelRateLimitErr: errors.New("write failed")}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),
	})
	account := gatewaytestkit.ModelNotFoundAccount()

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), false, "gpt-5.4").StopScheduling

	require.True(t, shouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Zero(t, repo.TempCalls)
	require.Len(t, repo.ModelRateLimitCalls, 1)
}

func TestOpenAIOAuth429_MatchingModelTempRuleAvoidsAccountRuntimeBlock(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),
	})
	account := gatewaytestkit.ModelNotFoundAccount()
	account.Record.Type = capability.AccountTypeOAuth
	account.Record.Credentials["temp_unschedulable_rules"] = []any{
		map[string]any{
			"error_code":       float64(http.StatusTooManyRequests),
			"keywords":         []any{"model quota"},
			"duration_minutes": float64(10),
		},
	}

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"model quota exhausted"}}`), false, "gpt-5.4").StopScheduling

	require.True(t, shouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Len(t, repo.ModelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.ModelRateLimitCalls[0].Scope)
}

func TestOpenAIOAuth429_NonmatchingModelTempRuleKeepsAccountRuntimeBlock(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),
	})
	account := gatewaytestkit.ModelNotFoundAccount()
	account.Record.Type = capability.AccountTypeOAuth
	account.Record.Credentials["temp_unschedulable_rules"] = []any{
		map[string]any{
			"error_code":       float64(http.StatusTooManyRequests),
			"keywords":         []any{"different marker"},
			"duration_minutes": float64(10),
		},
	}

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"global rate limit"}}`), false, "gpt-5.4").StopScheduling

	require.False(t, shouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.True(t, svc.shouldRetryOpenAIOAuth429OnSameAccount(account, http.StatusTooManyRequests, false))
	require.Empty(t, repo.ModelRateLimitCalls)
}

func TestOpenAITempUnschedulable_UnknownModelKeepsAccountRuntimeBlock(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),
	})
	account := gatewaytestkit.ModelNotFoundAccount()

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, account, http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), false).StopScheduling

	require.True(t, shouldDisable)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Equal(t, 1, repo.TempCalls)
	require.Empty(t, repo.ModelRateLimitCalls)
}

func TestShouldStopOpenAIOAuth429Failover_AfterBoundedFullWindows(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 42, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	apiKeyAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 43, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	var state failover.OAuth429State

	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 1, &state))

	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 1, &state))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 2, &state))
	require.True(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 3, &state))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(apiKeyAccount, http.StatusTooManyRequests, 1, &state))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusInternalServerError, 1, &state))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 0, &state))
}

func TestShouldStopOpenAIOAuth429Failover_TracksOneGrokFollowupAttempt(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 44, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	apiKeyAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 45, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey}}

	t.Run("429 then 500 stops after one followup", func(t *testing.T) {
		var state failover.OAuth429State
		require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 1, &state))
		require.True(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusInternalServerError, 2, &state))
	})

	t.Run("500 then 429 still allows one followup", func(t *testing.T) {
		var state failover.OAuth429State
		require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusInternalServerError, 1, &state))
		require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 2, &state))
		require.True(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusBadGateway, 3, &state))
	})

	t.Run("OAuth 429 then API-key failure consumes the same followup", func(t *testing.T) {
		var state failover.OAuth429State
		require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 1, &state))
		require.True(t, svc.ShouldStopOpenAIOAuth429Failover(apiKeyAccount, http.StatusInternalServerError, 2, &state))
	})

	var state failover.OAuth429State
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 0, &state))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(apiKeyAccount, http.StatusTooManyRequests, 2, &state))
}
