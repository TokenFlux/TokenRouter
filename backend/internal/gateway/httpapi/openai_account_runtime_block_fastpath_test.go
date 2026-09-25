//go:build unit

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type oauth429RateLimitRepo struct {
	gatewaytestkit.HealthStoreBase
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
	var svc *OpenAIResponsesExecutor

	rateLimits := newHTTPHealthFixture(repo, &responsesFixtureOptions{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.Output.Health.Runtime.BlockAccountScheduling(v, until, reason)
	}}, nil)

	svc = newResponsesFixture(responsesFixtureInputs{health: rateLimits})
	rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, v, h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 42, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	apiKeyAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 43, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	setupTokenAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 44, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeSetupToken}}
	grokOAuthAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 45, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusTooManyRequests, http.Header{}, nil, false).StopScheduling
	apiKeyShouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, apiKeyAccount, http.StatusTooManyRequests, http.Header{}, nil, false).StopScheduling

	require.False(t, shouldDisable)
	require.False(t, apiKeyShouldDisable)
	require.False(t, httpFixtureRuntimeBlocked(svc, account))
	require.True(t, httpFixtureRuntimeBlocked(svc, apiKeyAccount), "API-key 429 keeps the existing scheduler cooldown behavior")
	require.Equal(t, 1, repo.setRateLimitedCalls, "only the API-key 429 should persist a scheduler block")
	require.True(t, accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, account.View(), nil, nil))
	require.True(t, accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, setupTokenAccount.View(), nil, nil))
	require.False(t, accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, apiKeyAccount.View(), nil, nil))
	require.False(t, accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, grokOAuthAccount.View(), nil, nil))
	require.WithinDuration(t, time.Now().Add(accountcore.RuntimeRetryWindow), svc.Output.Health.RetryDeadline(account.View()), time.Second)
	require.WithinDuration(t, time.Now().Add(accountcore.RuntimeRetryWindow), svc.Output.Health.RetryDeadline(setupTokenAccount.View()), time.Second)
}

func TestOpenAI429FastPath_BlocksOAuthImmediatelyWhenSevenDayQuotaIsExhausted(t *testing.T) {
	repo := &oauth429RateLimitRepo{}
	var svc *OpenAIResponsesExecutor

	rateLimits := newHTTPHealthFixture(repo, &responsesFixtureOptions{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.Output.Health.Runtime.BlockAccountScheduling(v, until, reason)
	}}, nil)

	svc = newResponsesFixture(responsesFixtureInputs{health: rateLimits})
	rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, v, h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 423, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "20")
	headers.Set("x-codex-secondary-reset-after-seconds", "3600")
	headers.Set("x-codex-secondary-window-minutes", "300")

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusTooManyRequests, headers, []byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`), false).StopScheduling

	require.False(t, shouldDisable)
	require.True(t, httpFixtureRuntimeBlocked(svc, account))
	require.Equal(t, 1, repo.setRateLimitedCalls)
	require.Greater(t, time.Until(repo.lastRateLimitedUntil), 6*24*time.Hour)
	require.False(t, accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, account.View(), headers, nil))
}

func TestOpenAI429FastPath_RetriesOAuthWhenNoQuotaSignalExists(t *testing.T) {
	svc := newResponsesFixture(responsesFixtureInputs{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 424, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	headers := http.Header{"Retry-After": []string{"1"}}

	require.True(t, accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, account.View(), headers, []byte(`{"error":{"type":"rate_limit_error","message":"try again"}}`)))
	require.False(t, httpFixtureRuntimeBlocked(svc, account))
}

func TestOpenAIStream429IgnoresSuccessfulQuotaSnapshotHeaders(t *testing.T) {
	repo := &oauth429RateLimitRepo{}
	var svc *OpenAIResponsesExecutor

	rateLimits := newHTTPHealthFixture(repo, &responsesFixtureOptions{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.Output.Health.Runtime.BlockAccountScheduling(v, until, reason)
	}}, nil)

	svc = newResponsesFixture(responsesFixtureInputs{health: rateLimits})
	rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, v, h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 421, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	clock := expireRuntimeRetryForTest(svc, account.Record.ID)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "37")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	payload := []byte(`{"type":"error","error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`)

	status, disabled := svc.Output.TerminalAccountEffects(nil, account, payload, "slow down", headers)

	require.Equal(t, http.StatusTooManyRequests, status)
	require.False(t, disabled)
	require.True(t, httpFixtureRuntimeBlocked(svc, account))
	clock.Set(time.Now().Add(time.Minute))
	require.False(t, httpFixtureRuntimeBlocked(svc, account), "流内 429 不得继承七天快照")
	if !repo.lastRateLimitedUntil.IsZero() {
		require.Less(t, time.Until(repo.lastRateLimitedUntil), time.Minute)
	}
}

func TestOpenAI429FastPath_SparkQuotaOnlyBlocksSparkModel(t *testing.T) {
	repo := &oauth429RateLimitRepo{}
	var svc *OpenAIResponsesExecutor

	rateLimits := newHTTPHealthFixture(repo, &responsesFixtureOptions{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.Output.Health.Runtime.BlockAccountScheduling(v, until, reason)
	}}, nil)

	svc = newResponsesFixture(responsesFixtureInputs{health: rateLimits})
	rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, v, h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 425, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "20")
	headers.Set("x-codex-secondary-reset-after-seconds", "3600")
	headers.Set("x-codex-secondary-window-minutes", "300")

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusTooManyRequests, headers, []byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`), false, "gpt-5.3-codex-spark").StopScheduling

	require.False(t, shouldDisable)
	require.False(t, httpFixtureRuntimeBlocked(svc, account))
	require.Zero(t, repo.setRateLimitedCalls)
	require.Equal(t, 1, repo.setModelRateLimitCalls)
	require.Equal(t, "gpt-5.3-codex-spark", repo.lastModelRateLimitKey)
	require.Greater(t, time.Until(repo.lastModelRateLimitedUntil), 6*24*time.Hour)
}

func TestOpenAIStreamFailover_Spark429KeepsModelScope(t *testing.T) {
	repo := &oauth429RateLimitRepo{}
	var svc *OpenAIResponsesExecutor

	rateLimits := newHTTPHealthFixture(repo, &responsesFixtureOptions{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.Output.Health.Runtime.BlockAccountScheduling(v, until, reason)
	}}, nil)

	svc = newResponsesFixture(responsesFixtureInputs{health: rateLimits})
	rateLimits.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, v, h, body)
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

	failoverErr := svc.Output.NewStreamFailureWithModel(
		nil, account, false, "", payload, "quota exhausted", "gpt-5.3-codex-spark", headers,
	)

	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Zero(t, repo.setRateLimitedCalls)
	require.Equal(t, 1, repo.setModelRateLimitCalls)
	require.Equal(t, "gpt-5.3-codex-spark", repo.lastModelRateLimitKey)
	require.False(t, httpFixtureRuntimeBlocked(svc, account))
}

func TestOpenAI429FastPath_OpenCodeGoUsageLimitUsesMessageResetDuration(t *testing.T) {
	repo := &rateLimit429AccountRepoStub{}
	var svc *OpenAIResponsesExecutor

	healthObserver := newHTTPHealthFixture(repo, &responsesFixtureOptions{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.Output.Health.Runtime.BlockAccountScheduling(v, until, reason)
	}}, nil)

	svc = newResponsesFixture(responsesFixtureInputs{health: healthObserver})
	healthObserver.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, v, h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 44, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	body := []byte(`{"type":"error","error":{"type":"GoUsageLimitError","message":"5-hour usage limit reached. Resets in 4hr 59min. To continue using this model now, enable usage from your available balance: https://opencode.ai/workspace/wrk_test/go"},"metadata":{"workspace":"wrk_test","limitName":"5 hour"}}`)

	before := time.Now()
	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusTooManyRequests, http.Header{}, body, false).StopScheduling
	after := time.Now()

	require.False(t, shouldDisable)
	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, account.Record.ID, repo.lastRateLimitID)
	expectedResetAfter := 4*time.Hour + 59*time.Minute
	require.False(t, repo.lastRateLimitReset.Before(before.Add(expectedResetAfter-time.Second)))
	require.False(t, repo.lastRateLimitReset.After(after.Add(expectedResetAfter)))
	require.True(t, httpFixtureRuntimeBlocked(svc, account))
}

func TestOpenAIRuntimeBlock_AppliesToOpenAIAPIKeyWhenRateLimitServiceStopsScheduling(t *testing.T) {
	svc := newResponsesFixture(responsesFixtureInputs{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 44, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	svc.Output.Health.Runtime.BlockAccountScheduling(account.View(), time.Time{}, "custom_error_code")

	require.True(t, httpFixtureRuntimeBlocked(svc, account))
}

func TestOpenAIRuntimeBlock_DoesNotApplyToOtherPlatforms(t *testing.T) {
	svc := newResponsesFixture(responsesFixtureInputs{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 45, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth}}

	svc.Output.Health.Runtime.BlockAccountScheduling(account.View(), time.Time{}, "custom_error_code")

	require.False(t, httpFixtureRuntimeBlocked(svc, account))
}

func TestOpenAIRuntimeBlocker_IgnoresNonOpenAIFromRateLimitService(t *testing.T) {
	gateway := newResponsesFixture(responsesFixtureInputs{})
	repo := &gatewaytestkit.HealthStoreRecorder{}

	healthObserver := newHTTPHealthFixture(repo, &responsesFixtureOptions{}, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		gateway.Output.Health.Runtime.BlockAccountScheduling(v, until, reason)
	}}, nil)
	healthObserver.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return accountprovider.CanRetryOpenAI429(gateway.Output.Health.Runtime, v, h, body)
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 45, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), healthObserver, account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte("forbidden"), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.False(t, httpFixtureRuntimeBlocked(gateway, account))
}

func TestOpenAIPoolModeRetryable5xx_DoesNotCreateModelTransientBlock(t *testing.T) {
	repo := &gatewaytestkit.ErrorPolicyStore{}
	healthObserver := newHTTPHealthFixture(repo, &responsesFixtureOptions{}, nil, accountcore.HealthOptions{}, nil)

	gateway := newResponsesFixture(responsesFixtureInputs{health: healthObserver})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 47,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(524)},
		}},
	}

	for i := 0; i < 2; i++ {
		shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), gateway.Output.Health, account, 524, http.Header{}, []byte(`{"error":{"message":"upstream timeout"}}`), false, "gpt-5.4").StopScheduling
		require.False(t, shouldDisable)
	}

	require.False(t, (httpFixtureRuntimeBlocked(gateway, account) || gateway.Output.Health.ModelTransient.IsBlocked(account.Record.ID, accountcore.NormalizeTransientModel(gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel("gpt-5.4")), time.Now())))
}

func TestOpenAIPoolModeNonRetryable5xx_DoesNotCreateModelTransientBlock(t *testing.T) {
	repo := &gatewaytestkit.ErrorPolicyStore{}
	healthObserver := newHTTPHealthFixture(repo, &responsesFixtureOptions{}, nil, accountcore.HealthOptions{}, nil)

	gateway := newResponsesFixture(responsesFixtureInputs{health: healthObserver})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 48,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(http.StatusGatewayTimeout)},
		}},
	}

	for i := 0; i < 2; i++ {
		shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), gateway.Output.Health, account, http.StatusServiceUnavailable, http.Header{}, []byte(`{"error":{"message":"upstream unavailable"}}`), false, "gpt-5.4").StopScheduling
		require.False(t, shouldDisable)
	}

	require.False(t, (httpFixtureRuntimeBlocked(gateway, account) || gateway.Output.Health.ModelTransient.IsBlocked(account.Record.ID, accountcore.NormalizeTransientModel(gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel("gpt-5.4")), time.Now())))
}

func TestOpenAINonPoolAPIKey5xx_StillCreatesModelTransientBlock(t *testing.T) {
	repo := &gatewaytestkit.ErrorPolicyStore{}
	healthObserver := newHTTPHealthFixture(repo, &responsesFixtureOptions{}, nil, accountcore.HealthOptions{}, nil)

	gateway := newResponsesFixture(responsesFixtureInputs{health: healthObserver})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 49,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey},
	}

	for i := 0; i < 2; i++ {
		shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), gateway.Output.Health, account, http.StatusGatewayTimeout, http.Header{}, []byte(`{"error":{"message":"upstream timeout"}}`), false, "gpt-5.4").StopScheduling
		require.False(t, shouldDisable)
	}

	require.True(t, (httpFixtureRuntimeBlocked(gateway, account) || gateway.Output.Health.ModelTransient.IsBlocked(account.Record.ID, accountcore.NormalizeTransientModel(gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel("gpt-5.4")), time.Now())))
}

func TestOpenAIModelNotFound_DoesNotRuntimeBlockWholeAccount(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := newResponsesFixture(responsesFixtureInputs{health: newHTTPHealthFixture(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := gatewaytestkit.ModelNotFoundAccount()

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusNotFound, http.Header{}, []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`), false, "gpt-5.4").StopScheduling

	require.True(t, shouldDisable)
	require.False(t, httpFixtureRuntimeBlocked(svc, account))
	require.Zero(t, repo.TempCalls)
	require.Len(t, repo.ModelRateLimitCalls, 1)
}

func TestOpenAIModelTempUnschedulable_DoesNotRuntimeBlockWholeAccount(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := newResponsesFixture(responsesFixtureInputs{health: newHTTPHealthFixture(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := gatewaytestkit.ModelNotFoundAccount()

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), false, "gpt-5.4").StopScheduling

	require.True(t, shouldDisable)
	require.False(t, httpFixtureRuntimeBlocked(svc, account))
	require.Zero(t, repo.TempCalls)
	require.Len(t, repo.ModelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.ModelRateLimitCalls[0].Scope)
}

func TestOpenAIModelTempUnschedulable_WriteFailureDoesNotRuntimeBlockWholeAccount(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{ModelRateLimitErr: errors.New("write failed")}
	svc := newResponsesFixture(responsesFixtureInputs{health: newHTTPHealthFixture(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := gatewaytestkit.ModelNotFoundAccount()

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), false, "gpt-5.4").StopScheduling

	require.True(t, shouldDisable)
	require.False(t, httpFixtureRuntimeBlocked(svc, account))
	require.Zero(t, repo.TempCalls)
	require.Len(t, repo.ModelRateLimitCalls, 1)
}

func TestOpenAIOAuth429_MatchingModelTempRuleAvoidsAccountRuntimeBlock(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := newResponsesFixture(responsesFixtureInputs{health: newHTTPHealthFixture(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := gatewaytestkit.ModelNotFoundAccount()
	account.Record.Type = capability.AccountTypeOAuth
	account.Record.Credentials["temp_unschedulable_rules"] = []any{
		map[string]any{
			"error_code":       float64(http.StatusTooManyRequests),
			"keywords":         []any{"model quota"},
			"duration_minutes": float64(10),
		},
	}

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"model quota exhausted"}}`), false, "gpt-5.4").StopScheduling

	require.True(t, shouldDisable)
	require.False(t, httpFixtureRuntimeBlocked(svc, account))
	require.Len(t, repo.ModelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.ModelRateLimitCalls[0].Scope)
}

func TestOpenAIOAuth429_NonmatchingModelTempRuleKeepsAccountRuntimeBlock(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := newResponsesFixture(responsesFixtureInputs{health: newHTTPHealthFixture(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := gatewaytestkit.ModelNotFoundAccount()
	account.Record.Type = capability.AccountTypeOAuth
	account.Record.Credentials["temp_unschedulable_rules"] = []any{
		map[string]any{
			"error_code":       float64(http.StatusTooManyRequests),
			"keywords":         []any{"different marker"},
			"duration_minutes": float64(10),
		},
	}

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"global rate limit"}}`), false, "gpt-5.4").StopScheduling

	require.False(t, shouldDisable)
	require.False(t, httpFixtureRuntimeBlocked(svc, account))
	require.True(t, accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, account.View(), nil, nil))
	require.Empty(t, repo.ModelRateLimitCalls)
}

func TestOpenAITempUnschedulable_UnknownModelKeepsAccountRuntimeBlock(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := newResponsesFixture(responsesFixtureInputs{health: newHTTPHealthFixture(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := gatewaytestkit.ModelNotFoundAccount()

	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.Output.Health, account, http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), false).StopScheduling

	require.True(t, shouldDisable)
	require.True(t, httpFixtureRuntimeBlocked(svc, account))
	require.Equal(t, 1, repo.TempCalls)
	require.Empty(t, repo.ModelRateLimitCalls)
}

// httpRuntimeClock 为过期窗口提供显式时钟，不改写运行状态内部字段。
type httpRuntimeClock struct{ nanos atomic.Int64 }

func (c *httpRuntimeClock) Now() time.Time {
	if value := c.nanos.Load(); value != 0 {
		return time.Unix(0, value)
	}
	return time.Now()
}
func (c *httpRuntimeClock) Set(value time.Time) { c.nanos.Store(value.UnixNano()) }
func expireRuntimeRetryForTest(s *OpenAIResponsesExecutor, id int64) *httpRuntimeClock {
	clock := &httpRuntimeClock{}
	state := accountcore.NewRuntimeBlockState(clock.Now)
	s.Output.Health.Runtime = state
	s.Output.GrokHealth.Runtime = state
	s.Requests.Failure.Health.Runtime = state
	s.Text.Credentials.Runtime = state
	s.Text.Credentials.Recovery.Runtime = state
	clock.Set(time.Now().Add(-accountcore.RuntimeRetryWindow - time.Second))
	state.RetryWindowActive(id)
	clock.nanos.Store(0)
	return clock
}

type rateLimit429AccountRepoStub struct {
	gatewaytestkit.HealthStoreBase
	rateLimitCalls     int
	lastRateLimitID    int64
	lastRateLimitReset time.Time
}

func (r *rateLimit429AccountRepoStub) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitCalls++
	r.lastRateLimitID = id
	r.lastRateLimitReset = resetAt
	return nil
}
