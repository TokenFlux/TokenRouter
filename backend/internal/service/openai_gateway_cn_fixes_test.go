//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestResolveMessagesDispatchModelCNProvidersSkipOpenAIMapping(t *testing.T) {
	for _, platform := range []string{capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek} {
		group := &routing.Group{
			Platform: platform,
			MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
				SonnetMappedModel: "gpt-5.4",
			},
		}
		require.Empty(t, ResolveMessagesDispatchModel(group, "claude-sonnet-4-5"), platform)
	}

	openAIGroup := &routing.Group{
		Platform: capability.PlatformOpenAI,
		MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
			SonnetMappedModel: "gpt-5.4",
		},
	}
	require.Equal(t, "gpt-5.4", ResolveMessagesDispatchModel(openAIGroup, "claude-sonnet-4-5"))
}

func TestFilterCNProviderBillingModelCandidates(t *testing.T) {
	svc := &OpenAIGatewayService{}
	apiKey := &apikey.APIKey{Group: &routing.Group{ID: 1, Platform: capability.PlatformKimi}}
	cnAccount := &Account{ID: 1, Platform: capability.PlatformKimi}

	filtered := svc.filterCNProviderBillingModelCandidates(
		context.Background(),
		cnAccount,
		apiKey,
		[]string{"kimi-k2-0905-preview", "claude-sonnet-4-5", "sonnet-custom", "moonshot-v1-8k"},
	)
	require.Equal(t, []string{"kimi-k2-0905-preview", "moonshot-v1-8k"}, filtered)

	require.Empty(t, svc.filterCNProviderBillingModelCandidates(
		context.Background(), cnAccount, apiKey, []string{"claude-sonnet-4-5"},
	))

	openAIAccount := &Account{ID: 2, Platform: capability.PlatformOpenAI}
	require.Equal(t, []string{"claude-sonnet-4-5"}, svc.filterCNProviderBillingModelCandidates(
		context.Background(), openAIAccount, apiKey, []string{"claude-sonnet-4-5"},
	))
}

func TestFilterCNProviderBillingModelCandidatesKeepsExplicitGroupPricing(t *testing.T) {
	inputPrice := 0.000001
	outputPrice := 0.000002
	billing := NewBillingService(&config.Config{}, nil)
	svc := &OpenAIGatewayService{
		billingService: billing,
		resolver:       NewModelPricingResolver(nil, billing),
	}
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
	account := &Account{Platform: capability.PlatformKimi}

	require.Equal(t, []string{"claude-sonnet-4-5"}, svc.filterCNProviderBillingModelCandidates(
		context.Background(), account, apiKey, []string{"claude-sonnet-4-5"},
	))
}

func TestCalculateOpenAIRecordUsageCostEmptyCandidatesIsPricingUnavailable(t *testing.T) {
	svc := &OpenAIGatewayService{}
	apiKey := &apikey.APIKey{Group: &routing.Group{ID: 1, Platform: capability.PlatformKimi}}

	_, err := svc.calculateOpenAIRecordUsageCost(
		context.Background(), nil, apiKey, nil,
		1, 1, 1, 1, pricing.UsageTokens{InputTokens: 100}, "",
	)
	require.Error(t, err)
	require.True(t, isUsagePricingUnavailableError(err), err)
}

func TestHandle403_OtherCNProviderWithKimiConcurrencyMessageUsesNormalPolicy(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{accountcore.OpenAI403DisableThresholdDefault}}
	blocker := &runtimeBlockRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetAccountRuntimeBlocker(blocker)
	account := &Account{ID: 405, Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey}

	shouldDisable := service.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`),
	)

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
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetAccountRuntimeBlocker(blocker)
	account := &Account{ID: 403, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey}

	shouldDisable := service.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`),
	)

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
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetAccountRuntimeBlocker(blocker)
	account := &Account{ID: 406, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey}

	shouldDisable := service.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`),
	)

	require.True(t, shouldDisable, "the current request must fail over even when persistence fails")
	require.Equal(t, 1, repo.tempCalls, "the temporary cooldown should still be persisted when possible")
	require.Equal(t, 0, repo.setErrorCalls, "persistence failure must not fall back to permanent account error")
	require.Equal(t, []int64{accountcore.OpenAI403DisableThresholdDefault}, counter.counts, "persistence failure must not enter the permanent-error counter path")
	require.Len(t, blocker.accounts, 1, "the in-memory runtime block must survive repository failure")
	// 跨新旧类型边界比较完整投影，原同一输入断言在账号核心测试继续验证。
	require.Equal(t, account, blocker.accounts[0])
	require.Equal(t, accountcore.CNConcurrencyLimitReason, blocker.reasons[0])
	require.True(t, blocker.until[0].After(time.Now()))
}

func TestHandle403_CNProviderNearMatchRetainsNormalPermanentErrorPolicy(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{accountcore.OpenAI403DisableThresholdDefault}}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(counter)
	account := &Account{ID: 404, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey}

	shouldDisable := service.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(`{"error":{"message":"You've reached your concurrent request limit. Please contact support."}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls, "non-exact 403 must retain existing permission/auth protection")
	require.Equal(t, 0, repo.tempCalls)
}
