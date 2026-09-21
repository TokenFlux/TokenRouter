//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 两类网关都保留订阅、余额各自的倍率；高峰只影响最终按 token 结算的请求。
func TestMediaAllocationRatesPreserveBalanceMultiplier(t *testing.T) {
	for _, gatewayKind := range []string{"openai", "generic"} {
		for _, mode := range []routing.BillingMode{routing.BillingModeToken, routing.BillingModeImage, routing.BillingModePerRequest, routing.BillingModeVideo} {
			if gatewayKind == "generic" && mode == routing.BillingModeVideo {
				continue
			}
			for _, peak := range []bool{false, true} {
				name := gatewayKind + "/" + string(mode) + "/" + map[bool]string{false: "normal", true: "peak"}[peak]
				t.Run(name, func(t *testing.T) {
					logs := &openAIRecordUsageLogRepoStub{inserted: true}
					billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: true}}
					model, platform := "gpt-image-2", capability.PlatformOpenAI
					if gatewayKind == "generic" {
						model, platform = "gemini-3.1-flash-image", capability.PlatformGemini
					}
					if mode == routing.BillingModeVideo {
						model, platform = "grok-imagine-video", capability.PlatformGrok
					}
					card := routing.ChannelModelPricing{Models: []string{model}, BillingMode: mode, PerRequestPrice: testPtrFloat64(1)}
					if mode == routing.BillingModeToken {
						card.PerRequestPrice = nil
						card.InputPrice = testPtrFloat64(0.1)
					}
					group := &routing.Group{ID: 88, Platform: platform, RateMultiplier: 2, ModelPricing: []routing.ChannelModelPricing{card}, PeakRateEnabled: peak, PeakStart: "11:00", PeakEnd: "13:00", PeakRateMultiplier: 3}
					key := &apikey.APIKey{ID: 100, GroupID: &group.ID, Group: group}
					user, account := &identity.User{ID: 200}, &Account{ID: 300, Platform: platform}
					subscription := &billing.UserSubscription{ID: 99, Plan: &billing.SubscriptionPlan{ID: 199, GroupIDs: []int64{88}, GroupRateMultipliers: map[int64]float64{88: 0.5}}}
					now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.Local)
					if gatewayKind == "openai" {
						svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
						svc.resolver = NewModelPricingResolver(nil, svc.billingService)
						svc.usageBillingNow = func() time.Time { return now }
						result := &forwardcore.OpenAIResult{RequestID: name, Model: model, ImageCount: 1, ImageSize: "1K"}
						if mode == routing.BillingModeToken {
							result.ImageCount = 0
							result.Usage = openai.ForwardUsage{InputTokens: 10}
						}
						if mode == routing.BillingModeVideo {
							result.ImageCount = 0
							result.VideoCount = 1
							result.VideoDurationSeconds = 2
							result.VideoResolution = "480p"
						}
						require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: key, User: user, Account: account, Subscription: subscription}))
					} else {
						svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
						svc.resolver = NewModelPricingResolver(nil, svc.billingService)
						svc.usageBillingNow = func() time.Time { return now }
						result := &forwardcore.MessagesResult{RequestID: name, Model: model, ImageCount: 1, ImageSize: "1K"}
						if mode == routing.BillingModeToken {
							result.ImageCount = 0
							result.Usage.InputTokens = 10
						}
						require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{Result: result, APIKey: key, User: user, Account: account, Subscription: subscription}))
					}
					scale := 1.0
					if mode == routing.BillingModeToken && peak {
						scale = 3
					}
					require.NotNil(t, billingRepo.lastCmd)
					require.InDelta(t, 2*scale, billingRepo.lastCmd.SubscriptionRateMultiplier, 1e-12)
					require.InDelta(t, 2*scale, billingRepo.lastCmd.BalanceRateMultiplier, 1e-12)
					require.InDelta(t, scale, billingRepo.lastCmd.SubscriptionRateMultiplierScale, 1e-12)
					// 有效订阅享有 0.5 倍率，但不能把该倍率透传为余额部分的按量倍率。
					require.InDelta(t, 0.5*scale, logs.lastLog.RateMultiplier, 1e-12)
				})
			}
		}
	}
}
