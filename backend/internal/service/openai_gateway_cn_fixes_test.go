//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestFilterCNProviderBillingModelCandidates(t *testing.T) {
	svc := completion.NewRecorder(completion.Dependencies{}, completion.RecorderOptions{DefaultMultiplier: 1})

	apiKey := &apikey.APIKey{Group: &routing.Group{ID: 1, Platform: capability.PlatformKimi}}
	cnAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformKimi}}

	filtered := svc.FilterCNProviderBillingModelCandidates(
		context.Background(), gatewayprovider.ProjectCompletionAccount(gatewayprovider.ExecutionCompletionRecord(cnAccount)), gatewayprovider.ProjectCompletionKey(apiKey), []string{"kimi-k2-0905-preview", "claude-sonnet-4-5", "sonnet-custom", "moonshot-v1-8k"},
	)
	require.Equal(t, []string{"kimi-k2-0905-preview", "moonshot-v1-8k"}, filtered)

	require.Empty(t, svc.FilterCNProviderBillingModelCandidates(
		context.Background(), gatewayprovider.ProjectCompletionAccount(gatewayprovider.ExecutionCompletionRecord(cnAccount)), gatewayprovider.ProjectCompletionKey(apiKey), []string{"claude-sonnet-4-5"},
	))

	openAIAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI}}
	require.Equal(t, []string{"claude-sonnet-4-5"}, svc.FilterCNProviderBillingModelCandidates(
		context.Background(), gatewayprovider.ProjectCompletionAccount(gatewayprovider.ExecutionCompletionRecord(openAIAccount)), gatewayprovider.ProjectCompletionKey(apiKey), []string{"claude-sonnet-4-5"},
	))
}

func TestFilterCNProviderBillingModelCandidatesKeepsExplicitGroupPricing(t *testing.T) {
	inputPrice := 0.000001
	outputPrice := 0.000002
	billing := NewBillingService(&config.Config{}, nil)
	svc := completion.NewRecorder(completion.Dependencies{
		Calculator: billing,
		Prices:     billingtestkit.PriceResolver(nil, billing),
	}, completion.RecorderOptions{DefaultMultiplier: 1})

	group := &routing.Group{
		ID:       1,
		Platform: capability.PlatformKimi,
		ModelPricing: []routing.ChannelModelPricing{{
			Models:      []string{"claude-sonnet-4-5"},
			BillingMode: routing.BillingModeToken,
			InputPrice:  &inputPrice,
			OutputPrice: &outputPrice,
		}},
	}
	apiKey := &apikey.APIKey{Group: group}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformKimi}}

	require.Equal(t, []string{"claude-sonnet-4-5"}, svc.FilterCNProviderBillingModelCandidates(
		context.Background(), gatewayprovider.ProjectCompletionAccount(gatewayprovider.ExecutionCompletionRecord(account)), gatewayprovider.ProjectCompletionKey(apiKey), []string{"claude-sonnet-4-5"},
	))
}

func TestCalculateOpenAIRecordUsageCostEmptyCandidatesIsPricingUnavailable(t *testing.T) {
	svc := completion.NewRecorder(completion.Dependencies{}, completion.RecorderOptions{DefaultMultiplier: 1})

	apiKey := &apikey.APIKey{Group: &routing.Group{ID: 1, Platform: capability.PlatformKimi}}

	_, err := svc.CalculateOpenAIRecordUsageCostAt(
		context.Background(), gatewayprovider.ProjectOpenAICompletionResult(nil, nil), gatewayprovider.ProjectCompletionKey(apiKey), nil,
		1, 1, 1, 1, pricing.UsageTokens{InputTokens: 100}, "", time.Time{},
	)
	require.Error(t, err)
	require.True(t, isUsagePricingUnavailableError(err), err)
}

func TestHandle403_OtherCNProviderWithKimiConcurrencyMessageUsesNormalPolicy(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{accountcore.OpenAI403DisableThresholdDefault}}
	blocker := &runtimeBlockRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetAccountRuntimeBlocker(blocker)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 405, Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service.UpstreamHealth(), account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls, "non-Kimi CN provider must retain the normal permanent-error policy")
	require.Equal(t, 0, repo.tempCalls)
	require.Empty(t, counter.counts, "normal CN 403 policy must consume the counter result")
	require.Equal(t, []string{"auth_error"}, blocker.reasons, "the Kimi-specific runtime block must not apply")
}

func TestHandle403_CNProviderConcurrencyLimitAlwaysUsesTemporaryCooldown(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{accountcore.OpenAI403DisableThresholdDefault}}
	blocker := &runtimeBlockRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetAccountRuntimeBlocker(blocker)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 403, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service.UpstreamHealth(), account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`), nil)).StopScheduling

	require.True(t, shouldDisable, "the request must still fail over to another account")
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastTempReason, accountcore.CNConcurrencyLimitReason)
	require.Equal(t, []int64{accountcore.OpenAI403DisableThresholdDefault}, counter.counts, "transient concurrency 403 must bypass the permanent-error counter")
	require.Len(t, blocker.accounts, 1)
	require.Equal(t, accountcore.CNConcurrencyLimitReason, blocker.reasons[0])
	require.True(t, blocker.until[0].After(time.Now()))
}

func TestHandle403_KimiConcurrencyLimitRepositoryFailureKeepsRuntimeBlock(t *testing.T) {
	repo := &rateLimitAccountRepoStub{tempErr: errors.New("repository unavailable")}
	counter := &openAI403CounterCacheStub{counts: []int64{accountcore.OpenAI403DisableThresholdDefault}}
	blocker := &runtimeBlockRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetAccountRuntimeBlocker(blocker)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 406, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service.UpstreamHealth(), account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`), nil)).StopScheduling

	require.True(t, shouldDisable, "the current request must fail over even when persistence fails")
	require.Equal(t, 1, repo.tempCalls, "the temporary cooldown should still be persisted when possible")
	require.Equal(t, 0, repo.setErrorCalls, "persistence failure must not fall back to permanent account error")
	require.Equal(t, []int64{accountcore.OpenAI403DisableThresholdDefault}, counter.counts, "persistence failure must not enter the permanent-error counter path")
	require.Len(t, blocker.accounts, 1, "the in-memory runtime block must survive repository failure")
	// 原实体没有时钟依赖；保留全部业务字段和路线比较，函数本身不属于运行阻断数据。
	expectedAccount, observedAccount := *account, *blocker.accounts[0]
	expectedAccount.Record.Now, observedAccount.Record.Now = nil, nil
	expectedAccount.Record.LoadLocation, observedAccount.Record.LoadLocation = nil, nil
	require.Equal(t, expectedAccount, observedAccount)
	require.Equal(t, accountcore.CNConcurrencyLimitReason, blocker.reasons[0])
	require.True(t, blocker.until[0].After(time.Now()))
}

func TestHandle403_CNProviderNearMatchRetainsNormalPermanentErrorPolicy(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{accountcore.OpenAI403DisableThresholdDefault}}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil)
	service.SetOpenAI403CounterCache(counter)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 404, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service.UpstreamHealth(), account, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please contact support."}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls, "non-exact 403 must retain existing permission/auth protection")
	require.Equal(t, 0, repo.tempCalls)
}
