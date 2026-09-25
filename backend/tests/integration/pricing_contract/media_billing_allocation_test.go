//go:build unit

package pricingcontract

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
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
					logs := &gatewaytestkit.UsageLogStore{Inserted: true}
					billingRepo := &gatewaytestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
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
					user, account := &identity.User{ID: 200}, &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 300, Platform: platform}}
					subscription := &billing.UserSubscription{ID: 99, Plan: &billing.SubscriptionPlan{ID: 199, GroupIDs: []int64{88}, GroupRateMultipliers: map[int64]float64{88: 0.5}}}
					now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.Local)
					if gatewayKind == "openai" {
						svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billingRepo, &gatewaytestkit.UserStore{}, &gatewaytestkit.SubscriptionStore{}, nil)
						svc.Dependencies.Prices = billingtestkit.PriceResolver(nil, svc.Dependencies.Calculator)
						svc.Options.Now = func() time.Time { return now }
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
						require.NoError(t, svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{Result: result, APIKey: key, User: user, Account: gatewaycapture.ExecutionCompletionRecord(account), Subscription: subscription}))
					} else {
						svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, billingRepo, &gatewaytestkit.UserStore{}, &gatewaytestkit.SubscriptionStore{})
						svc.Dependencies.Prices = billingtestkit.PriceResolver(nil, svc.Dependencies.Calculator)
						svc.Options.Now = func() time.Time { return now }
						result := &forwardcore.MessagesResult{RequestID: name, Model: model, ImageCount: 1, ImageSize: "1K"}
						if mode == routing.BillingModeToken {
							result.ImageCount = 0
							result.Usage.InputTokens = 10
						}
						require.NoError(t, svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{Result: result, APIKey: key, User: user, Account: gatewaycapture.ExecutionCompletionRecord(account), Subscription: subscription}))
					}
					scale := 1.0
					if mode == routing.BillingModeToken && peak {
						scale = 3
					}
					require.NotNil(t, billingRepo.LastCmd)
					require.InDelta(t, 2*scale, billingRepo.LastCmd.SubscriptionRateMultiplier, 1e-12)
					require.InDelta(t, 2*scale, billingRepo.LastCmd.BalanceRateMultiplier, 1e-12)
					require.InDelta(t, scale, billingRepo.LastCmd.SubscriptionRateMultiplierScale, 1e-12)
					// 有效订阅享有 0.5 倍率，但不能把该倍率透传为余额部分的按量倍率。
					require.InDelta(t, 0.5*scale, logs.LastLog.RateMultiplier, 1e-12)
				})
			}
		}
	}
}
