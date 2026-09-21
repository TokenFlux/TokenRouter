package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type openAI403CounterResetStub struct {
	resetCalls []int64
}

func (s *openAI403CounterResetStub) IncrementOpenAI403Count(context.Context, int64, int) (int64, error) {
	return 0, nil
}

func (s *openAI403CounterResetStub) ResetOpenAI403Count(_ context.Context, accountID int64) error {
	s.resetCalls = append(s.resetCalls, accountID)
	return nil
}

func TestOpenAIGatewayServiceRecordUsageResets403CounterForZeroUsage(t *testing.T) {
	for _, platform := range []string{capability.PlatformOpenAI, capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek} {
		t.Run(platform, func(t *testing.T) {
			counter := &openAI403CounterResetStub{}
			rateLimitSvc := NewRateLimitService(nil, nil, nil, nil, nil)
			rateLimitSvc.SetOpenAI403CounterCache(counter)

			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: true}}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &openAIRecordUsageSubRepoStub{}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)
			svc.rateLimitService = rateLimitSvc

			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &forwardcore.OpenAIResult{
					RequestID: "resp_zero_usage_reset_403_" + platform,
					Model:     "gpt-5.1",
				},
				APIKey:  &apikey.APIKey{ID: 1001, Group: &routing.Group{RateMultiplier: 1}},
				User:    &identity.User{ID: 2001},
				Account: &Account{ID: 777, Platform: platform},
			})

			require.NoError(t, err)
			require.Equal(t, []int64{777}, counter.resetCalls)
			require.Equal(t, 1, usageRepo.calls)
		})
	}
}
