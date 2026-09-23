//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestIsGrokModelSpecificFreeUsage(t *testing.T) {
	require.True(t, accountcore.IsGrokModelSpecificFreeUsage(
		"you've used all the included free usage for model grok-4.5", "grok-4.5"))
	require.True(t, accountcore.IsGrokModelSpecificFreeUsage("模型额度用完 grok-4.3", "grok-4.3"))
	require.False(t, accountcore.IsGrokModelSpecificFreeUsage("free usage exhausted", "grok-4.5"))
}

func TestGrokStickyAffinitySeed_ScopesByModel(t *testing.T) {
	a := gatewaysession.GrokStickyAffinitySeed("session-1", []byte(`{"model":"grok-4.5"}`))
	b := gatewaysession.GrokStickyAffinitySeed("session-1", []byte(`{"model":"grok-4.3"}`))
	c := gatewaysession.GrokStickyAffinitySeed("session-1", []byte(`{"model":"grok-4.5"}`))
	require.NotEqual(t, a, b)
	require.Equal(t, a, c)
	require.Contains(t, a, "grok-affinity:v1:")
}

func TestExtractGrokModelIDsFromModelsBody(t *testing.T) {
	body := []byte(`{"object":"list","data":[{"id":"grok-4.5"},{"id":"grok-4.3"},{"id":"grok-4.5"}]}`)
	ids := extractGrokModelIDsFromModelsBody(body)
	require.Equal(t, []string{"grok-4.5", "grok-4.3"}, ids)
}

func TestApplyGrokUpstreamFailure_ModelSpecificFreeUsage(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9109, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	body := []byte(`{"error":{"code":"subscription:free-usage-exhausted","message":"You've used all the included free usage for model grok-4.5. Usage resets over a rolling 24-hour window."}}`)

	svc.handleGrokAccountUpstreamError(context.Background(), account, 400, nil, body)

	require.Zero(t, repo.tempUnschedCalls, "model-scoped free usage must not cool sibling models")
	require.True(t, accountcore.IsGrokModelQuotaBlocked(account.Record.ID, "grok-4.5", time.Now()))
	require.False(t, accountcore.IsGrokModelQuotaBlocked(account.Record.ID, "grok-4.3", time.Now()))
}

func TestApplyGrokUpstreamFailure_SpendingLimitRemainsRecoverable(t *testing.T) {
	repo := &grokQuotaAccountRepo{}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9110, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	body := []byte(`{"code":"personal-team-blocked:spending-limit","error":"spending limit reached"}`)

	svc.handleGrokAccountUpstreamError(context.Background(), account, 403, nil, body)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Zero(t, repo.tempUnschedCalls)
	// 缺少账期快照时使用可恢复的短期探测冷却。
	require.WithinDuration(t, time.Now().Add(grokSpendingLimitProbeCooldown), repo.lastRateLimitResetAt, 2*time.Second)
}
