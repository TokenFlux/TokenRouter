//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

// 纯倍率必须保留渠道区间及来源，同名倍率覆盖后仍以同一基础价格计算。
func TestGroupPricingModifiersInheritChannel(t *testing.T) {
	channel := routing.ChannelModelPricing{
		Platform: capability.PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: routing.BillingModeToken,
		FastMultiplier: testPtrFloat64(2), FlexMultiplier: testPtrFloat64(0.5), MaxReasoningEffortMultiplier: testPtrFloat64(3),
		Intervals:   []routing.PricingInterval{{MinTokens: 0, MaxTokens: testPtrInt(100), InputPrice: testPtrFloat64(0.01)}, {MinTokens: 100, InputPrice: testPtrFloat64(0.02)}},
		TimePricing: &routing.ChannelTimePricing{Timezone: "UTC", Periods: []routing.ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "12:00", Multiplier: 2}}},
	}
	rCalculator := billingtestkit.ResolverCalculator()
	r := billingtestkit.ResolverWithCards(t, rCalculator, []routing.ChannelModelPricing{channel})
	group := &routing.Group{ModelPricing: []routing.ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, FastMultiplier: testPtrFloat64(1.5), MaxReasoningEffortMultiplier: testPtrFloat64(4)}}}
	input := billing.PricingInput{Model: "claude-sonnet-4", GroupID: billingtestkit.GroupID(), Group: gatewaycapture.ProjectCompletionPriceGroup(group)}
	resolved := r.Resolve(context.Background(), input)
	require.Equal(t, pricing.PricingSourceChannel, resolved.Source)
	require.Len(t, resolved.Intervals, 2)
	require.Equal(t, channel.TimePricing, resolved.ChannelPricing.TimePricing)
	for _, tier := range []struct {
		name       string
		multiplier float64
	}{{"priority", 1.5}, {"flex", 0.5}, {"default", 1}} {
		for _, count := range []int{99, 100, 101} {
			base := 0.01
			if count > 100 {
				base = 0.02
			}
			cost, err := rCalculator.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: input.Model, Group: gatewaycapture.ProjectCompletionPriceGroup(group),
				GroupID: billingtestkit.GroupID(), Tokens: pricing.UsageTokens{InputTokens: count}, RateMultiplier: 5, ServiceTier: tier.name, ReasoningEffort: "max",
				PricingAt: time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC), Resolver: r})
			require.NoError(t, err)
			require.InDelta(t, float64(count)*base*tier.multiplier*4*2, cost.TotalCost, 1e-10)
			require.InDelta(t, cost.TotalCost*5, cost.ActualCost, 1e-10)
		}
	}
	group.ModelPricing[0].TimePricing = &routing.ChannelTimePricing{Timezone: "UTC", WeekdaysOnly: true, Periods: []routing.ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "12:00", Multiplier: 0.5}}}
	resolved = r.Resolve(context.Background(), input)
	require.Equal(t, 0.5, resolvedChannelTimeMultiplier(resolved, time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC)))
	require.Equal(t, 1.0, resolvedChannelTimeMultiplier(resolved, time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)))
	require.Equal(t, 1.0, resolvedChannelTimeMultiplier(resolved, time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)))
	// 第二个分组仍使用原渠道配置，覆盖不能污染共享缓存。
	original := r.Resolve(context.Background(), billing.PricingInput{Model: input.Model, GroupID: billingtestkit.GroupID()})
	require.Equal(t, 2.0, *r.GetIntervalPricing(original, 101).FastMultiplier)
	require.Equal(t, 2.0, resolvedChannelTimeMultiplier(original, time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC)))
}

func TestGroupPricingModifiersPreserveInheritedModeAndMissingPrice(t *testing.T) {
	for _, mode := range []routing.BillingMode{routing.BillingModePerRequest, routing.BillingModeImage, routing.BillingModeVideo} {
		t.Run(string(mode), func(t *testing.T) {
			rCalculator := billingtestkit.ResolverCalculator()
			r := billingtestkit.ResolverWithCards(t, rCalculator, []routing.ChannelModelPricing{{Platform: capability.PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: mode, PerRequestPrice: testPtrFloat64(0.3)}})
			resolved := r.Resolve(context.Background(), billing.PricingInput{Model: "claude-sonnet-4", GroupID: billingtestkit.GroupID(), Group: gatewaycapture.ProjectCompletionPriceGroup(&routing.Group{ModelPricing: []routing.ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, FastMultiplier: testPtrFloat64(3)}}})})
			require.Equal(t, mode, resolved.Mode)
			require.Equal(t, 0.3, resolved.DefaultPerRequestPrice)
		})
	}
	rCalculator := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(nil, rCalculator)
	for _, model := range []string{"claude-sonnet-4", "unknown-parity-model"} {
		group := &routing.Group{LongContextPricingEnabled: true, ModelPricing: []routing.ChannelModelPricing{{Models: []string{model}, FastMultiplier: testPtrFloat64(1.5)}}}
		base := r.Resolve(context.Background(), billing.PricingInput{Model: model})
		resolved := r.Resolve(context.Background(), billing.PricingInput{Model: model, Group: gatewaycapture.ProjectCompletionPriceGroup(group)})
		require.Equal(t, base.Source, resolved.Source)
		if base.BasePricing == nil {
			require.Nil(t, resolved.BasePricing)
			_, err := rCalculator.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: model, Group: gatewaycapture.ProjectCompletionPriceGroup(group), Tokens: pricing.UsageTokens{InputTokens: 1}, RateMultiplier: 1, Resolver: r})
			require.ErrorIs(t, err, pricing.ErrModelPricingUnavailable)
		} else {
			require.Equal(t, base.BasePricing.InputPricePerToken, resolved.BasePricing.InputPricePerToken)
			require.Equal(t, 1.5, *resolved.BasePricing.FastMultiplier)
			require.Nil(t, base.BasePricing.FastMultiplier)
		}
	}
}

// 同一显式价卡放在分组或渠道时，包含缓存上下文的区间结算必须完全相同。
func TestGroupAndChannelPricingParity(t *testing.T) {
	card := routing.ChannelModelPricing{Platform: capability.PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: routing.BillingModeToken,
		FastMultiplier: testPtrFloat64(1.5), FlexMultiplier: testPtrFloat64(0.4), MaxReasoningEffortMultiplier: testPtrFloat64(2), PriceMultiplier: testPtrFloat64(1.2),
		Intervals: []routing.PricingInterval{{MinTokens: 0, MaxTokens: testPtrInt(100), InputPrice: testPtrFloat64(0), CacheReadPrice: testPtrFloat64(0.01)},
			{MinTokens: 100, InputPrice: testPtrFloat64(0.02), CacheWrite1hPrice: testPtrFloat64(0.03), OutputMultiplier: testPtrFloat64(2)}},
		TimePricing: &routing.ChannelTimePricing{Timezone: "Asia/Tokyo", Periods: []routing.ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "00:00:01", Multiplier: 0.5}, {StartTime: "23:00", EndTime: "00:00", Multiplier: 2}}},
	}
	channelResolverCalculator := billingtestkit.ResolverCalculator()
	channelResolver := billingtestkit.ResolverWithCards(t, channelResolverCalculator, []routing.ChannelModelPricing{card})
	groupResolverCalculator := billingtestkit.ResolverCalculator()
	groupResolver := billingtestkit.PriceResolver(nil, groupResolverCalculator)
	for _, enabled := range []bool{true, false} {
		group := &routing.Group{LongContextPricingEnabled: enabled, ModelPricing: []routing.ChannelModelPricing{card}}
		for _, count := range []int{49, 50, 51, 200001} {
			for _, tier := range []string{"default", "priority", "flex"} {
				input := billing.CostInput{Ctx: context.Background(), Model: "claude-sonnet-4", Tokens: pricing.UsageTokens{InputTokens: count, CacheReadTokens: 50, OutputTokens: 10},
					RateMultiplier: 1.7, ServiceTier: tier, ReasoningEffort: "max", PricingAt: time.Date(2026, 9, 9, 14, 30, 0, 0, time.UTC), Resolver: groupResolver, Group: gatewaycapture.ProjectCompletionPriceGroup(group)}
				got, err := groupResolverCalculator.CalculateCostUnified(input)
				require.NoError(t, err)
				input.Resolver = channelResolver
				input.Group = gatewaycapture.ProjectCompletionPriceGroup(&routing.Group{LongContextPricingEnabled: enabled})
				input.GroupID = billingtestkit.GroupID()
				want, err := channelResolverCalculator.CalculateCostUnified(input)
				require.NoError(t, err)
				require.Equal(t, want, got)
				require.False(t, got.LongContextBillingApplied)
			}
		}
	}
}

func TestGroupPricingValidationAndCopy(t *testing.T) {
	config := routing.ChannelModelPricing{Models: []string{"test"}, BillingMode: routing.BillingModeToken, InputPrice: testPtrFloat64(0), FastMultiplier: testPtrFloat64(1),
		Intervals:   []routing.PricingInterval{{MaxTokens: testPtrInt(100), InputMultiplier: testPtrFloat64(2)}},
		TimePricing: &routing.ChannelTimePricing{Timezone: "UTC", Periods: []routing.ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "01:00", Multiplier: 1.5}}},
	}
	normalized, err := normalizeGroupModelPricing(capability.PlatformOpenAI, []routing.ChannelModelPricing{config})
	require.NoError(t, err)
	source := &routing.Group{LongContextPricingEnabled: true, FreeOpenAIFast: true, ModelPricing: normalized}
	cloned := cloneGroupForDuplicate(source, "test")
	require.Equal(t, source.ModelPricing, cloned.ModelPricing)
	require.True(t, cloned.LongContextPricingEnabled)
	require.True(t, cloned.FreeOpenAIFast)
	*cloned.ModelPricing[0].FastMultiplier = 9
	*cloned.ModelPricing[0].Intervals[0].MaxTokens = 999
	cloned.ModelPricing[0].TimePricing.Periods[0].Multiplier = 8
	require.Equal(t, 1.0, *source.ModelPricing[0].FastMultiplier)
	require.Equal(t, 100, *source.ModelPricing[0].Intervals[0].MaxTokens)
	require.Equal(t, 1.5, source.ModelPricing[0].TimePricing.Periods[0].Multiplier)
	for _, invalid := range []float64{0, -1, math.Inf(1), math.NaN()} {
		for _, field := range []string{"fast", "flex", "max"} {
			bad := config.Clone()
			switch field {
			case "fast":
				bad.FastMultiplier = &invalid
			case "flex":
				bad.FlexMultiplier = &invalid
			case "max":
				bad.MaxReasoningEffortMultiplier = &invalid
			}
			_, err := normalizeGroupModelPricing(capability.PlatformOpenAI, []routing.ChannelModelPricing{bad})
			require.Error(t, err)
		}
	}
}

// 认证快照经 JSON 往返后保留整张价卡，单次请求的修改不回写缓存。
func TestGroupPricingAuthSnapshotRoundTrip(t *testing.T) {
	source := &routing.Group{ID: 5, Platform: capability.PlatformOpenAI, LongContextPricingEnabled: true, FreeOpenAIFast: true,
		ModelPricing: []routing.ChannelModelPricing{{Models: []string{"gpt-test"}, InputPrice: testPtrFloat64(0), FastMultiplier: testPtrFloat64(1.5), FlexMultiplier: testPtrFloat64(0.4), MaxReasoningEffortMultiplier: testPtrFloat64(2),
			Intervals:   []routing.PricingInterval{{MinTokens: 100, InputMultiplier: testPtrFloat64(2)}},
			TimePricing: &routing.ChannelTimePricing{Timezone: "UTC", Periods: []routing.ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "10:00", Multiplier: 0.5}}}}},
	}
	svc := apikey.NewAPIKeyService(nil, nil, nil, nil, nil, nil, &apikey.Options{})
	apiKey := &apikey.APIKey{GroupID: &source.ID, Group: source, User: &identity.User{ID: 1}}
	snapshot := svc.KeySnapshotFromAPIKey(context.Background(), apiKey)
	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var restored apikey.APIKeyAuthSnapshot
	require.NoError(t, json.Unmarshal(data, &restored))
	got := svc.KeySnapshotToAPIKey("test", &restored)
	require.Equal(t, source.ModelPricing, got.Group.ModelPricing)
	require.True(t, got.Group.LongContextPricingEnabled)
	require.True(t, got.Group.FreeOpenAIFast)
	*got.Group.ModelPricing[0].FastMultiplier = 9
	require.Equal(t, 1.5, *restored.Group.ModelPricing[0].FastMultiplier)
	// 复合 Key 的独立分组快照也使用同样的完整价卡。
	require.Equal(t, source.ModelPricing, apikey.KeyGroupFromAuthSnapshot(apikey.KeyAuthGroupSnapshotFromGroup(source)).ModelPricing)
}

// 免费 Fast 必须在相同区间和 WS turn 时刻重算 Standard，账号基数仍为 Fast。
func TestGroupPricingFreeFastWithIntervalsAndTurnTime(t *testing.T) {
	for _, free := range []bool{false, true} {
		t.Run(fmt.Sprint(free), func(t *testing.T) {
			usageRepo := &gatewaytestkit.UsageLogStore{Inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, &gatewaytestkit.UserStore{}, &gatewaytestkit.SubscriptionStore{}, nil)
			svc.Dependencies.Prices = billingtestkit.PriceResolver(nil, svc.Dependencies.Calculator)
			groupID := int64(88)
			tier := "priority"
			group := &routing.Group{ID: groupID, Hydrated: true, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, RateMultiplier: 0.5, FreeOpenAIFast: free,
				LongContextPricingEnabled: true, ModelPricing: []routing.ChannelModelPricing{{Models: []string{"gpt-5.6-sol"}, BillingMode: routing.BillingModeToken,
					FastMultiplier: testPtrFloat64(3), MaxReasoningEffortMultiplier: testPtrFloat64(2),
					Intervals: []routing.PricingInterval{{MinTokens: 0, MaxTokens: testPtrInt(50), InputPrice: testPtrFloat64(0.001), OutputPrice: testPtrFloat64(0)},
						{MinTokens: 50, InputPrice: testPtrFloat64(0.002), OutputPrice: testPtrFloat64(0)}},
					TimePricing: &routing.ChannelTimePricing{Timezone: "UTC", Periods: []routing.ChannelTimePricingPeriod{{StartTime: "01:00", EndTime: "02:00", Multiplier: 0.5}}}}},
			}
			err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
				Result: &forwardcore.OpenAIResult{RequestID: "resp_group_pricing", Model: "gpt-5.6-sol", ServiceTier: &tier,
					Usage: openai.ForwardUsage{InputTokens: 100}, Duration: time.Second},
				APIKey: &apikey.APIKey{ID: 1020, GroupID: &groupID, Group: group}, User: &identity.User{ID: 2020},
				Account:   gatewaycapture.ExecutionCompletionRecord(&gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3020, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}),
				PricingAt: time.Date(2026, 9, 9, 1, 30, 0, 0, time.UTC),
			})
			require.NoError(t, err)
			standardTotal := 100 * 0.002 * 0.5
			wantBase := standardTotal * 3
			if free {
				wantBase = standardTotal
			}
			require.InDelta(t, standardTotal*3, usageRepo.LastLog.TotalCost, 1e-12)
			require.InDelta(t, wantBase*0.5, usageRepo.LastLog.ActualCost, 1e-12)
			cmd := requireOpenAIRecordUsageBillingRepoStub(t, svc).LastCmd
			require.InDelta(t, wantBase, cmd.BaseAmountUSD, 1e-12)
			require.InDelta(t, wantBase*0.5, cmd.BillableAmountUSD, 1e-12)
			// 展示复用解析器，但免费 Fast 不得污染随后计算的 Fast 成本。
			market := newGatewayMarketplaceFixture(nil, nil, withSchedulerParametersForTest(&GatewayService{resolver: svc.Dependencies.Prices}), svc.Dependencies.Calculator, nil, nil, nil)
			display := market.PublicModelPricing(context.Background(), group, "gpt-5.6-sol")
			require.Len(t, display.ContextIntervals, 2)
			ratio := 3.0
			if free {
				ratio = 1
			}
			require.InDelta(t, display.ContextIntervals[1].InputPricePerToken*ratio, display.ContextIntervals[1].FastInputPricePerToken, 1e-12)
			resolved := svc.Dependencies.Prices.Resolve(context.Background(), billing.PricingInput{Model: "gpt-5.6-sol", Group: gatewaycapture.ProjectCompletionPriceGroup(group)})
			require.Equal(t, 3.0, *resolved.BasePricing.FastMultiplier)
		})
	}
}

// 免费 Fast 不能让不支持该档位的模型在市场中多出 Fast 价格。
func TestGroupPricingFreeFastDisplayRespectsModelSupport(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	svc := newGatewayMarketplaceFixture(nil, nil, withSchedulerParametersForTest(&GatewayService{resolver: billingtestkit.PriceResolver(nil, bs)}), bs, nil, nil, nil)
	group := &routing.Group{ID: 1, Platform: capability.PlatformOpenAI, RateMultiplier: 1, FreeOpenAIFast: true,
		ModelPricing: []routing.ChannelModelPricing{{Models: []string{"embedding-parity"}, InputPrice: testPtrFloat64(0.01)}},
	}
	display := svc.PublicModelPricing(context.Background(), group, "embedding-parity")
	require.Equal(t, 0.01, display.InputPricePerToken)
	require.Zero(t, display.FastInputPricePerToken)
	// 旧零倍率仍是明确的 Fast 配置，启用免费 Fast 后展示实际收取的 Standard 价格。
	group.ModelPricing = []routing.ChannelModelPricing{{Models: []string{"embedding-parity"}, InputPrice: testPtrFloat64(0.02), FastModeMultiplier: testPtrFloat64(0)}}
	display = svc.PublicModelPricing(context.Background(), group, "embedding-parity")
	require.Equal(t, 0.02, display.FastInputPricePerToken)
}

// 默认单价必须用于区间外请求、区间空字段和区间倍率，不能因添加区间而丢失。
func TestConfiguredIntervalsPreserveDefaultPrices(t *testing.T) {
	for _, source := range []string{"group", "channel"} {
		for _, model := range []string{"claude-sonnet-4", "custom-priced"} {
			t.Run(source+"/"+model, func(t *testing.T) {
				card := routing.ChannelModelPricing{Platform: capability.PlatformOpenAI, Models: []string{model}, BillingMode: routing.BillingModeToken,
					InputPrice: testPtrFloat64(0.001), OutputPrice: testPtrFloat64(0.002),
					CacheWritePrice: testPtrFloat64(0.003), CacheWrite1hPrice: testPtrFloat64(0.004), CacheReadPrice: testPtrFloat64(0.005),
					PriceMultiplier: testPtrFloat64(1.2), FastMultiplier: testPtrFloat64(1.5),
					Intervals: []routing.PricingInterval{
						{MinTokens: 100, MaxTokens: testPtrInt(200), InputMultiplier: testPtrFloat64(2), OutputPrice: testPtrFloat64(0), CacheWriteMultiplier: testPtrFloat64(2), CacheReadPrice: testPtrFloat64(0.001)},
						{MinTokens: 200, InputPrice: testPtrFloat64(0.008)},
					},
				}
				_, err := normalizeGroupModelPricing(capability.PlatformOpenAI, []routing.ChannelModelPricing{card})
				require.NoError(t, err)
				rCalculator := billingtestkit.ResolverCalculator()
				r := billingtestkit.ResolverWithCards(t, rCalculator, nil)
				group := &routing.Group{ID: 100, Platform: capability.PlatformOpenAI, ModelPricing: []routing.ChannelModelPricing{card}}
				if source == "channel" {
					rCalculator = billingtestkit.ResolverCalculator()
					r = billingtestkit.ResolverWithCards(t, rCalculator, []routing.ChannelModelPricing{card})
					group.ModelPricing = nil
				}
				for _, tc := range []struct {
					input    int
					standard float64
				}{{10, 0.19}, {110, 0.38}, {210, 1.86}} {
					for _, tier := range []string{"default", "priority"} {
						cost, err := rCalculator.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: model, Group: gatewaycapture.ProjectCompletionPriceGroup(group), GroupID: &group.ID,
							Tokens:         pricing.UsageTokens{InputTokens: tc.input, OutputTokens: 5, CacheCreationTokens: 20, CacheCreation5mTokens: 10, CacheCreation1hTokens: 10, CacheReadTokens: 20},
							RateMultiplier: 0.7, ServiceTier: tier, Resolver: r})
						require.NoError(t, err)
						expected := tc.standard * 1.2
						if tier == "priority" {
							expected *= 1.5
						}
						require.InDelta(t, expected, cost.TotalCost, 1e-10, "input=%d tier=%s", tc.input, tier)
						require.InDelta(t, expected*0.7, cost.ActualCost, 1e-10)
					}
				}
			})
		}
	}
}

// 自定义模型只有区间价格时，免费 Fast 在单档和多档展示中都必须与 Standard 一致。
func TestFreeFastIntervalOnlyDisplayMatchesStandard(t *testing.T) {
	for _, source := range []string{"group", "channel"} {
		for _, tierCount := range []int{1, 2} {
			for _, free := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/free=%v", source, tierCount, free), func(t *testing.T) {
					intervals := []routing.PricingInterval{{MinTokens: 0, InputPrice: testPtrFloat64(0.001), CacheReadPrice: testPtrFloat64(0.0001)}}
					if tierCount == 2 {
						intervals[0].MaxTokens = testPtrInt(100)
						intervals = append(intervals, routing.PricingInterval{MinTokens: 100, InputPrice: testPtrFloat64(0.002), CacheReadPrice: testPtrFloat64(0.0002)})
					}
					card := routing.ChannelModelPricing{Platform: capability.PlatformOpenAI, Models: []string{"custom-priced"}, BillingMode: routing.BillingModeToken,
						FastMultiplier: testPtrFloat64(3), Intervals: intervals}
					_, err := normalizeGroupModelPricing(capability.PlatformOpenAI, []routing.ChannelModelPricing{card})
					require.NoError(t, err)
					group := &routing.Group{ID: 100, Platform: capability.PlatformOpenAI, RateMultiplier: 0.5, FreeOpenAIFast: free, ModelPricing: []routing.ChannelModelPricing{card}}
					rCalculator := billingtestkit.ResolverCalculator()
					r := billingtestkit.ResolverWithCards(t, rCalculator, nil)
					if source == "channel" {
						rCalculator = billingtestkit.ResolverCalculator()
						r = billingtestkit.ResolverWithCards(t, rCalculator, []routing.ChannelModelPricing{card})
						group.ModelPricing = nil
					}
					svc := newGatewayMarketplaceFixture(nil, nil, withSchedulerParametersForTest(&GatewayService{resolver: r}), rCalculator, nil, nil, nil)
					display := svc.PublicModelPricing(context.Background(), group, "custom-priced")
					require.Equal(t, "priced", display.PriceStatus)
					ratio := 3.0
					if free {
						ratio = 1
					}
					if tierCount == 1 {
						require.Empty(t, display.ContextIntervals)
						require.InDelta(t, 0.001*group.RateMultiplier, display.InputPricePerToken, 1e-12)
						require.InDelta(t, display.InputPricePerToken*ratio, display.FastInputPricePerToken, 1e-12)
						require.InDelta(t, display.CacheReadPricePerToken*ratio, display.FastCacheReadPricePerToken, 1e-12)
					} else {
						require.Len(t, display.ContextIntervals, 2)
						for i, interval := range display.ContextIntervals {
							require.InDelta(t, float64(i+1)*0.001*group.RateMultiplier, interval.InputPricePerToken, 1e-12)
							require.InDelta(t, interval.InputPricePerToken*ratio, interval.FastInputPricePerToken, 1e-12)
							require.InDelta(t, interval.CacheReadPricePerToken*ratio, interval.FastCacheReadPricePerToken, 1e-12)
						}
					}
					// 展示副本不能污染后续结算的 Fast 成本。
					cost, err := rCalculator.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: "custom-priced", Group: gatewaycapture.ProjectCompletionPriceGroup(group), GroupID: &group.ID,
						Tokens: pricing.UsageTokens{InputTokens: 50}, RateMultiplier: group.RateMultiplier, ServiceTier: "priority", Resolver: r})
					require.NoError(t, err)
					require.InDelta(t, 50*0.001*3, cost.TotalCost, 1e-12)
				})
			}
		}
	}
}

// 分时配置独立生效，渠道不能忽略没有填写单价的有效价卡。
func TestTimeOnlyPricingGroupChannelParity(t *testing.T) {
	card := routing.ChannelModelPricing{Platform: capability.PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: routing.BillingModeToken,
		TimePricing: &routing.ChannelTimePricing{Timezone: "UTC", Periods: []routing.ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "10:00", Multiplier: 2}}},
	}
	require.NoError(t, validatePricingEntries([]routing.ChannelModelPricing{card}))
	var costs []float64
	for _, source := range []string{"group", "channel"} {
		group := &routing.Group{ID: 100, Platform: capability.PlatformAnthropic, ModelPricing: []routing.ChannelModelPricing{card}}
		rCalculator := billingtestkit.ResolverCalculator()
		r := billingtestkit.ResolverWithCards(t, rCalculator, nil)
		if source == "channel" {
			rCalculator = billingtestkit.ResolverCalculator()
			r = billingtestkit.ResolverWithCards(t, rCalculator, []routing.ChannelModelPricing{card})
			group.ModelPricing = nil
		}
		cost, err := rCalculator.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: "claude-sonnet-4", Group: gatewaycapture.ProjectCompletionPriceGroup(group), GroupID: &group.ID,
			Tokens: pricing.UsageTokens{InputTokens: 100}, RateMultiplier: 1, PricingAt: time.Date(2026, 9, 9, 9, 30, 0, 0, time.UTC), Resolver: r})
		require.NoError(t, err)
		costs = append(costs, cost.ActualCost)
	}
	require.Equal(t, costs[0], costs[1])
	require.InDelta(t, 0.0006, costs[0], 1e-12)
}

// Fast 只改变倍率，不能清空内置的图片输入和输出价格桶。
func TestTierOnlyPricingPreservesImagePricesEqually(t *testing.T) {
	card := routing.ChannelModelPricing{Platform: capability.PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: routing.BillingModeToken, FastMultiplier: testPtrFloat64(2)}
	var costs []float64
	for _, source := range []string{"group", "channel"} {
		prices := billingtestkit.ResolverFallbackPrices()
		prices["claude-sonnet-4"].InputPricePerToken = 0.001
		prices["claude-sonnet-4"].ImageInputPricePerToken = 0.003
		prices["claude-sonnet-4"].ImageOutputPricePerToken = 0.004
		bs := newBillingServiceWithPrices(nil, nil, prices)
		group := &routing.Group{ID: 100, Platform: capability.PlatformAnthropic, ModelPricing: []routing.ChannelModelPricing{card}}
		r := billingtestkit.ResolverWithCards(t, bs, nil)
		if source == "channel" {
			r = billingtestkit.ResolverWithCards(t, bs, []routing.ChannelModelPricing{card})
			group.ModelPricing = nil
		}
		cost, err := bs.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: "claude-sonnet-4", Group: gatewaycapture.ProjectCompletionPriceGroup(group), GroupID: &group.ID,
			Tokens: pricing.UsageTokens{InputTokens: 200, ImageInputTokens: 100, OutputTokens: 50, ImageOutputTokens: 50}, RateMultiplier: 1, ServiceTier: "priority", Resolver: r})
		require.NoError(t, err)
		costs = append(costs, cost.ActualCost)
	}
	require.Equal(t, costs[0], costs[1])
	require.InDelta(t, 1.2, costs[0], 1e-12)
}

// 分组与渠道按同一个身份候选匹配型号档位别名。
func TestGroupChannelAliasMatchingParity(t *testing.T) {
	card := routing.ChannelModelPricing{Platform: capability.PlatformOpenAI, Models: []string{"gpt-5.6-luna"}, BillingMode: routing.BillingModeToken, InputPrice: testPtrFloat64(0.001)}
	bs := NewBillingService(nil, nil)
	group := &routing.Group{ID: 100, Platform: capability.PlatformOpenAI, ModelPricing: []routing.ChannelModelPricing{card}}
	groupResolverCalculator := bs
	groupResolver := billingtestkit.PriceResolver(nil, groupResolverCalculator)
	channelResolver := billingtestkit.ResolverWithCards(t, bs, []routing.ChannelModelPricing{card})
	groupPrice := groupResolver.Resolve(context.Background(), billing.PricingInput{Model: "gpt-5.6-luna-high", Group: gatewaycapture.ProjectCompletionPriceGroup(group), GroupID: &group.ID})
	channelPrice := channelResolver.Resolve(context.Background(), billing.PricingInput{Model: "gpt-5.6-luna-high", GroupID: &group.ID})
	require.Equal(t, channelPrice.BasePricing.InputPricePerToken, groupPrice.BasePricing.InputPricePerToken)
	require.Equal(t, 0.001, groupPrice.BasePricing.InputPricePerToken)
}

// Qoder 与其他平台一样继承未填的基础价格桶，不再强制手工价。
func TestQoderGroupChannelBlankPricesParity(t *testing.T) {
	card := routing.ChannelModelPricing{Platform: capability.PlatformQoder, Models: []string{"claude-opus-4-6"}, BillingMode: routing.BillingModeToken, InputPrice: testPtrFloat64(0.001)}
	var costs []float64
	for _, source := range []string{"group", "channel"} {
		bs := NewBillingService(nil, nil)
		group := &routing.Group{ID: 100, Platform: capability.PlatformQoder, ModelPricing: []routing.ChannelModelPricing{card}}
		r := billingtestkit.ResolverWithCards(t, bs, nil)
		if source == "channel" {
			r = billingtestkit.ResolverWithCards(t, bs, []routing.ChannelModelPricing{card})
			group.ModelPricing = nil
		}
		gateway := completion.NewRecorder(completion.Dependencies{Prices: r, Calculator: bs}, completion.RecorderOptions{DefaultMultiplier: 1})

		resolved, model := gateway.ResolveChannelPricing(context.Background(), "claude-opus-4-6", gatewaycapture.ProjectCompletionKey(&apikey.APIKey{Group: group, GroupID: &group.ID})), "claude-opus-4-6"
		require.NotNil(t, resolved)
		cost, err := bs.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: model, Group: gatewaycapture.ProjectCompletionPriceGroup(group), GroupID: &group.ID, Tokens: pricing.UsageTokens{OutputTokens: 100}, RateMultiplier: 1, Resolver: r, Resolved: resolved})
		require.NoError(t, err)
		costs = append(costs, cost.ActualCost)
	}
	require.Equal(t, costs[1], costs[0])
	require.InDelta(t, 0.0025, costs[0], 1e-12)
}

// 保留内置来源，确保纯倍率不会意外禁用内置峰值定价；渠道和分组只覆盖同名倍率。
func TestModifierCardsPreserveBuiltinPricingPolicy(t *testing.T) {
	model := "deepseek-v4-flash"
	card := routing.ChannelModelPricing{Platform: capability.PlatformDeepseek, Models: []string{model}, FastMultiplier: testPtrFloat64(3),
		TimePricing: &routing.ChannelTimePricing{Timezone: "UTC", Periods: []routing.ChannelTimePricingPeriod{{StartTime: "01:00", EndTime: "04:00", Multiplier: 2}}}}
	for _, scope := range []string{"group", "channel", "both"} {
		t.Run(scope, func(t *testing.T) {
			bs := NewBillingService(nil, nil)
			group := &routing.Group{ID: 100, Platform: capability.PlatformDeepseek, LongContextPricingEnabled: true}
			var channelCards []routing.ChannelModelPricing
			if scope != "group" {
				channelCards = []routing.ChannelModelPricing{card}
			} else {
				group.ModelPricing = []routing.ChannelModelPricing{card}
			}
			if scope == "both" {
				group.ModelPricing = []routing.ChannelModelPricing{{Models: []string{model}, FastMultiplier: testPtrFloat64(1.5)}}
			}
			r := billingtestkit.ResolverWithCards(t, bs, channelCards)
			for _, hour := range []int{0, 1, 3, 4} {
				at := time.Date(2026, 9, 9, hour, 0, 0, 0, time.UTC)
				resolved := r.Resolve(context.Background(), billing.PricingInput{Model: model, GroupID: &group.ID, Group: gatewaycapture.ProjectCompletionPriceGroup(group)})
				require.Equal(t, pricing.PricingSourceLiteLLM, resolved.Source)
				cost, err := bs.CalculateCostUnified(billing.CostInput{Model: model, GroupID: &group.ID, Group: gatewaycapture.ProjectCompletionPriceGroup(group), Resolver: r,
					Tokens: pricing.UsageTokens{InputTokens: 100}, RateMultiplier: 1, PricingAt: at, ServiceTier: "priority"})
				require.NoError(t, err)
				expected := 100 * deepseekFlashOffPeakInputPrice * 3
				if scope == "both" {
					expected /= 2
				}
				if hour >= 1 && hour < 4 {
					expected *= 2 * 2 // 内置峰值与价卡分时各生效一次。
				}
				require.InDelta(t, expected, cost.TotalCost, 1e-12)
			}
		})
	}
}

// Qoder 的服务层级、分时、零价及图片默认价与其他平台共用结算和展示入口。
func TestQoderPricingMatchesOtherPlatforms(t *testing.T) {
	for _, model := range []string{"claude-opus-4-6", "gpt-image-1", "custom-image", "qmodel"} {
		for _, kind := range []string{"default", "modifiers", "free"} {
			t.Run(model+"/"+kind, func(t *testing.T) {
				var prices []pricing.ModelDisplayPricing
				var costs []*pricing.CostBreakdown
				for _, platform := range []string{capability.PlatformQoder, capability.PlatformOpenAI} {
					group := &routing.Group{ID: 100, Platform: platform, RateMultiplier: 1}
					card := routing.ChannelModelPricing{Models: []string{model}, Platform: platform}
					switch kind {
					case "modifiers":
						card.FastMultiplier = testPtrFloat64(2)
						card.TimePricing = &routing.ChannelTimePricing{Timezone: "UTC", Periods: []routing.ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "12:00", Multiplier: 2}}}
					case "free":
						card.InputPrice, card.OutputPrice = testPtrFloat64(0), testPtrFloat64(0)
					}
					bs := NewBillingService(nil, nil)
					r := billingtestkit.ResolverWithCards(t, bs, []routing.ChannelModelPricing{card})
					gateway := completion.NewRecorder(completion.Dependencies{Prices: r, Calculator: bs}, completion.RecorderOptions{DefaultMultiplier: 1})

					market := newGatewayMarketplaceFixture(nil, nil, withSchedulerParametersForTest(&GatewayService{resolver: r}), bs, nil, nil, nil)
					prices = append(prices, market.RequestableModelPricing(context.Background(), group, routing.MarketplaceModelDef{ID: model, PricingModel: model}))
					result := &forwardcore.MessagesResult{Usage: upstream.TokenUsage{InputTokens: 100, OutputTokens: 10}, ServiceTier: testPtrString("priority")}
					if looksLikeImageModel(model) {
						result.ImageCount = 1
					}
					costs = append(costs, gateway.CalculateRecordUsageCost(context.Background(), gatewaycapture.ProjectMessagesCompletionResult(result, gatewaycapture.ExecutionCompletionRecord(&gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: platform}})), gatewaycapture.ProjectCompletionKey(&apikey.APIKey{Group: group}), gatewaycapture.ProjectCompletionAccount(gatewaycapture.ExecutionCompletionRecord(&gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: platform}})), model, model, routing.BillingModelSourceRequested, model, 1, 1, &completion.PricingOptions{PricingAt: time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)}))
				}
				require.Equal(t, prices[0], prices[1])
				require.Equal(t, costs[0], costs[1])
				if model == "claude-opus-4-6" && kind != "free" {
					require.Positive(t, costs[0].TotalCost)
					require.Equal(t, "priced", prices[0].PriceStatus)
				}
				if kind == "free" {
					require.Zero(t, costs[0].TotalCost)
					require.Equal(t, "priced", prices[0].PriceStatus)
				}
			})
		}
	}
}
