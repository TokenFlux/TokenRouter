//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// 模拟两次读取之间管理员提交了新配置，两份配置各自的最终价格保持相同。
type changingMediaPricingGroupRepo struct {
	calls              int
	oldGroup, newGroup *Group
}

func (r *changingMediaPricingGroupRepo) GetByIDLite(context.Context, int64) (*Group, error) {
	r.calls++
	if r.calls == 1 {
		return r.oldGroup, nil
	}
	return r.newGroup, nil
}

func TestBatchImagePricingSnapshotUsesOneGroupVersion(t *testing.T) {
	oldGroup := &Group{ID: 7, Platform: PlatformGemini, AllowBatchImageGeneration: true, RateMultiplier: 10, BatchImageDiscountMultiplier: 0.5, BatchImageHoldMultiplier: 0.6,
		ModelPricing: []ChannelModelPricing{{Models: []string{"gemini-3.1-flash-image"}, BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(1)}}}
	newGroup := *oldGroup
	newGroup.RateMultiplier = 1
	newGroup.ModelPricing = []ChannelModelPricing{{Models: []string{"gemini-3.1-flash-image"}, BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(10)}}
	groups := &changingMediaPricingGroupRepo{oldGroup: oldGroup, newGroup: &newGroup}
	svc := &BatchImagePublicService{GroupRepo: groups, Pricing: &BatchImageModelPricingResolver{Resolver: NewModelPricingResolver(nil, NewBillingService(nil, nil)), GroupRepo: groups}}
	snapshot, err := svc.resolvePricingSnapshot(context.Background(), BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &oldGroup.ID, BillingMode: APIKeyBillingModeBalance}, BatchImageSubmitRequest{Model: "gemini-3.1-flash-image", ImageSize: "1K"}, "gemini_api", nil)
	require.NoError(t, err)
	require.Equal(t, 1, groups.calls)
	require.Equal(t, 1.0, snapshot.BaseUnitPrice)
	require.Equal(t, 10.0, snapshot.GroupRateMultiplier)
	require.InDelta(t, 5.0, snapshot.BillableUnitPrice, 1e-12)
	require.InDelta(t, 6.0, snapshot.HoldUnitPrice, 1e-12)
}

// 两类网关都保留订阅、余额各自的倍率；高峰只影响最终按 token 结算的请求。
func TestMediaAllocationRatesPreserveBalanceMultiplier(t *testing.T) {
	for _, gatewayKind := range []string{"openai", "generic"} {
		for _, mode := range []BillingMode{BillingModeToken, BillingModeImage, BillingModePerRequest, BillingModeVideo} {
			if gatewayKind == "generic" && mode == BillingModeVideo {
				continue
			}
			for _, peak := range []bool{false, true} {
				name := gatewayKind + "/" + string(mode) + "/" + map[bool]string{false: "normal", true: "peak"}[peak]
				t.Run(name, func(t *testing.T) {
					logs := &openAIRecordUsageLogRepoStub{inserted: true}
					billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
					model, platform := "gpt-image-2", PlatformOpenAI
					if gatewayKind == "generic" {
						model, platform = "gemini-3.1-flash-image", PlatformGemini
					}
					if mode == BillingModeVideo {
						model, platform = "grok-imagine-video", PlatformGrok
					}
					card := ChannelModelPricing{Models: []string{model}, BillingMode: mode, PerRequestPrice: testPtrFloat64(1)}
					if mode == BillingModeToken {
						card.PerRequestPrice = nil
						card.InputPrice = testPtrFloat64(0.1)
					}
					group := &Group{ID: 88, Platform: platform, RateMultiplier: 2, ModelPricing: []ChannelModelPricing{card}, PeakRateEnabled: peak, PeakStart: "11:00", PeakEnd: "13:00", PeakRateMultiplier: 3}
					key := &APIKey{ID: 100, GroupID: &group.ID, Group: group}
					user, account := &User{ID: 200}, &Account{ID: 300, Platform: platform}
					subscription := &UserSubscription{ID: 99, Plan: &SubscriptionPlan{ID: 199, GroupIDs: []int64{88}, GroupRateMultipliers: map[int64]float64{88: 0.5}}}
					now := time.Date(2026, 9, 9, 12, 0, 0, 0, timezone.Location())
					if gatewayKind == "openai" {
						svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
						svc.resolver = NewModelPricingResolver(nil, svc.billingService)
						svc.usageBillingNow = func() time.Time { return now }
						result := &OpenAIForwardResult{RequestID: name, Model: model, ImageCount: 1, ImageSize: "1K"}
						if mode == BillingModeToken {
							result.ImageCount = 0
							result.Usage = OpenAIUsage{InputTokens: 10}
						}
						if mode == BillingModeVideo {
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
						result := &ForwardResult{RequestID: name, Model: model, ImageCount: 1, ImageSize: "1K"}
						if mode == BillingModeToken {
							result.ImageCount = 0
							result.Usage.InputTokens = 10
						}
						require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{Result: result, APIKey: key, User: user, Account: account, Subscription: subscription}))
					}
					scale := 1.0
					if mode == BillingModeToken && peak {
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
