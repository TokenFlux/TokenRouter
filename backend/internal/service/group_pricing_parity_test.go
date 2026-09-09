//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 纯倍率必须保留渠道区间及来源，同名倍率覆盖后仍以同一基础价格计算。
func TestGroupPricingModifiersInheritChannel(t *testing.T) {
	channel := ChannelModelPricing{
		Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
		FastMultiplier: testPtrFloat64(2), FlexMultiplier: testPtrFloat64(0.5), MaxReasoningEffortMultiplier: testPtrFloat64(3),
		Intervals:   []PricingInterval{{MinTokens: 0, MaxTokens: testPtrInt(100), InputPrice: testPtrFloat64(0.01)}, {MinTokens: 100, InputPrice: testPtrFloat64(0.02)}},
		TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "12:00", Multiplier: 2}}},
	}
	r := newResolverWithChannel(t, []ChannelModelPricing{channel})
	group := &Group{ModelPricing: []ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, FastMultiplier: testPtrFloat64(1.5), MaxReasoningEffortMultiplier: testPtrFloat64(4)}}}
	input := PricingInput{Model: "claude-sonnet-4", GroupID: groupIDPtr(), Group: group}
	resolved := r.Resolve(context.Background(), input)
	require.Equal(t, PricingSourceChannel, resolved.Source)
	require.Len(t, resolved.Intervals, 2)
	require.Equal(t, channel.TimePricing, resolved.channelPricing.TimePricing)
	for _, tier := range []struct {
		name       string
		multiplier float64
	}{{"priority", 1.5}, {"flex", 0.5}, {"default", 1}} {
		for _, count := range []int{99, 100, 101} {
			base := 0.01
			if count > 100 {
				base = 0.02
			}
			cost, err := r.billingService.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: input.Model, Group: group,
				GroupID: groupIDPtr(), Tokens: UsageTokens{InputTokens: count}, RateMultiplier: 5, ServiceTier: tier.name, ReasoningEffort: "max",
				PricingAt: time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC), Resolver: r})
			require.NoError(t, err)
			require.InDelta(t, float64(count)*base*tier.multiplier*4*2, cost.TotalCost, 1e-10)
			require.InDelta(t, cost.TotalCost*5, cost.ActualCost, 1e-10)
		}
	}
	group.ModelPricing[0].TimePricing = &ChannelTimePricing{Timezone: "UTC", WeekdaysOnly: true, Periods: []ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "12:00", Multiplier: 0.5}}}
	resolved = r.Resolve(context.Background(), input)
	require.Equal(t, 0.5, resolvedChannelTimeMultiplier(resolved, time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC)))
	require.Equal(t, 1.0, resolvedChannelTimeMultiplier(resolved, time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)))
	require.Equal(t, 1.0, resolvedChannelTimeMultiplier(resolved, time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)))
	// 第二个分组仍使用原渠道配置，覆盖不能污染共享缓存。
	original := r.Resolve(context.Background(), PricingInput{Model: input.Model, GroupID: groupIDPtr()})
	require.Equal(t, 2.0, *r.GetIntervalPricing(original, 101).FastMultiplier)
	require.Equal(t, 2.0, resolvedChannelTimeMultiplier(original, time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC)))
}

func TestGroupPricingModifiersPreserveInheritedModeAndMissingPrice(t *testing.T) {
	for _, mode := range []BillingMode{BillingModePerRequest, BillingModeImage, BillingModeVideo} {
		t.Run(string(mode), func(t *testing.T) {
			r := newResolverWithChannel(t, []ChannelModelPricing{{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: mode, PerRequestPrice: testPtrFloat64(0.3)}})
			resolved := r.Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4", GroupID: groupIDPtr(), Group: &Group{ModelPricing: []ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, FastMultiplier: testPtrFloat64(3)}}}})
			require.Equal(t, mode, resolved.Mode)
			require.Equal(t, 0.3, resolved.DefaultPerRequestPrice)
		})
	}
	r := NewModelPricingResolver(nil, newTestBillingServiceForResolver())
	for _, model := range []string{"claude-sonnet-4", "unknown-parity-model"} {
		group := &Group{LongContextPricingEnabled: true, ModelPricing: []ChannelModelPricing{{Models: []string{model}, FastMultiplier: testPtrFloat64(1.5)}}}
		base := r.Resolve(context.Background(), PricingInput{Model: model})
		resolved := r.Resolve(context.Background(), PricingInput{Model: model, Group: group})
		require.Equal(t, base.Source, resolved.Source)
		if base.BasePricing == nil {
			require.Nil(t, resolved.BasePricing)
			_, err := r.billingService.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: model, Group: group, Tokens: UsageTokens{InputTokens: 1}, RateMultiplier: 1, Resolver: r})
			require.ErrorIs(t, err, ErrModelPricingUnavailable)
		} else {
			require.Equal(t, base.BasePricing.InputPricePerToken, resolved.BasePricing.InputPricePerToken)
			require.Equal(t, 1.5, *resolved.BasePricing.FastMultiplier)
			require.Nil(t, base.BasePricing.FastMultiplier)
		}
	}
}

// 同一显式价卡放在分组或渠道时，包含缓存上下文的区间结算必须完全相同。
func TestGroupAndChannelPricingParity(t *testing.T) {
	card := ChannelModelPricing{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
		FastMultiplier: testPtrFloat64(1.5), FlexMultiplier: testPtrFloat64(0.4), MaxReasoningEffortMultiplier: testPtrFloat64(2), PriceMultiplier: testPtrFloat64(1.2),
		Intervals: []PricingInterval{{MinTokens: 0, MaxTokens: testPtrInt(100), InputPrice: testPtrFloat64(0), CacheReadPrice: testPtrFloat64(0.01)},
			{MinTokens: 100, InputPrice: testPtrFloat64(0.02), CacheWrite1hPrice: testPtrFloat64(0.03), OutputMultiplier: testPtrFloat64(2)}},
		TimePricing: &ChannelTimePricing{Timezone: "Asia/Tokyo", Periods: []ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "00:00:01", Multiplier: 0.5}, {StartTime: "23:00", EndTime: "00:00", Multiplier: 2}}},
	}
	channelResolver := newResolverWithChannel(t, []ChannelModelPricing{card})
	groupResolver := NewModelPricingResolver(nil, newTestBillingServiceForResolver())
	for _, enabled := range []bool{true, false} {
		group := &Group{LongContextPricingEnabled: enabled, ModelPricing: []ChannelModelPricing{card}}
		for _, count := range []int{49, 50, 51, 200001} {
			for _, tier := range []string{"default", "priority", "flex"} {
				input := CostInput{Ctx: context.Background(), Model: "claude-sonnet-4", Tokens: UsageTokens{InputTokens: count, CacheReadTokens: 50, OutputTokens: 10},
					RateMultiplier: 1.7, ServiceTier: tier, ReasoningEffort: "max", PricingAt: time.Date(2026, 9, 9, 14, 30, 0, 0, time.UTC), Resolver: groupResolver, Group: group}
				got, err := groupResolver.billingService.CalculateCostUnified(input)
				require.NoError(t, err)
				input.Resolver = channelResolver
				input.Group = &Group{LongContextPricingEnabled: enabled}
				input.GroupID = groupIDPtr()
				want, err := channelResolver.billingService.CalculateCostUnified(input)
				require.NoError(t, err)
				require.Equal(t, want, got)
				require.False(t, got.LongContextBillingApplied)
			}
		}
	}
}

func TestGroupPricingValidationAndCopy(t *testing.T) {
	config := ChannelModelPricing{Models: []string{"test"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(0), FastMultiplier: testPtrFloat64(1),
		Intervals:   []PricingInterval{{MaxTokens: testPtrInt(100), InputMultiplier: testPtrFloat64(2)}},
		TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "01:00", Multiplier: 1.5}}},
	}
	normalized, err := normalizeGroupModelPricing(PlatformOpenAI, []ChannelModelPricing{config})
	require.NoError(t, err)
	source := &Group{LongContextPricingEnabled: true, FreeOpenAIFast: true, ModelPricing: normalized}
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
			_, err := normalizeGroupModelPricing(PlatformOpenAI, []ChannelModelPricing{bad})
			require.Error(t, err)
		}
	}
}

// 认证快照经 JSON 往返后保留整张价卡，单次请求的修改不回写缓存。
func TestGroupPricingAuthSnapshotRoundTrip(t *testing.T) {
	source := &Group{ID: 5, Platform: PlatformOpenAI, LongContextPricingEnabled: true, FreeOpenAIFast: true,
		ModelPricing: []ChannelModelPricing{{Models: []string{"gpt-test"}, InputPrice: testPtrFloat64(0), FastMultiplier: testPtrFloat64(1.5), FlexMultiplier: testPtrFloat64(0.4), MaxReasoningEffortMultiplier: testPtrFloat64(2),
			Intervals:   []PricingInterval{{MinTokens: 100, InputMultiplier: testPtrFloat64(2)}},
			TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "10:00", Multiplier: 0.5}}}}},
	}
	svc := &APIKeyService{}
	apiKey := &APIKey{GroupID: &source.ID, Group: source, User: &User{ID: 1}}
	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var restored APIKeyAuthSnapshot
	require.NoError(t, json.Unmarshal(data, &restored))
	got := svc.snapshotToAPIKey("test", &restored)
	require.Equal(t, source.ModelPricing, got.Group.ModelPricing)
	require.True(t, got.Group.LongContextPricingEnabled)
	require.True(t, got.Group.FreeOpenAIFast)
	*got.Group.ModelPricing[0].FastMultiplier = 9
	require.Equal(t, 1.5, *restored.Group.ModelPricing[0].FastMultiplier)
	// 复合 Key 的独立分组快照也使用同样的完整价卡。
	require.Equal(t, source.ModelPricing, groupFromAuthSnapshot(authGroupSnapshotFromGroup(source)).ModelPricing)
}

// 免费 Fast 必须在相同区间和 WS turn 时刻重算 Standard，账号基数仍为 Fast。
func TestGroupPricingFreeFastWithIntervalsAndTurnTime(t *testing.T) {
	for _, free := range []bool{false, true} {
		t.Run(fmt.Sprint(free), func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			groupID := int64(88)
			tier := "priority"
			group := &Group{ID: groupID, Hydrated: true, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.5, FreeOpenAIFast: free,
				LongContextPricingEnabled: true, ModelPricing: []ChannelModelPricing{{Models: []string{"gpt-5.6-sol"}, BillingMode: BillingModeToken,
					FastMultiplier: testPtrFloat64(3), MaxReasoningEffortMultiplier: testPtrFloat64(2),
					Intervals: []PricingInterval{{MinTokens: 0, MaxTokens: testPtrInt(50), InputPrice: testPtrFloat64(0.001), OutputPrice: testPtrFloat64(0)},
						{MinTokens: 50, InputPrice: testPtrFloat64(0.002), OutputPrice: testPtrFloat64(0)}},
					TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "01:00", EndTime: "02:00", Multiplier: 0.5}}}}},
			}
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "resp_group_pricing", Model: "gpt-5.6-sol", ServiceTier: &tier,
					Usage: OpenAIUsage{InputTokens: 100}, Duration: time.Second},
				APIKey: &APIKey{ID: 1020, GroupID: &groupID, Group: group}, User: &User{ID: 2020},
				Account:   &Account{ID: 3020, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
				PricingAt: time.Date(2026, 9, 9, 1, 30, 0, 0, time.UTC),
			})
			require.NoError(t, err)
			standardTotal := 100 * 0.002 * 0.5
			wantBase := standardTotal * 3
			if free {
				wantBase = standardTotal
			}
			require.InDelta(t, standardTotal*3, usageRepo.lastLog.TotalCost, 1e-12)
			require.InDelta(t, wantBase*0.5, usageRepo.lastLog.ActualCost, 1e-12)
			cmd := requireOpenAIRecordUsageBillingRepoStub(t, svc).lastCmd
			require.InDelta(t, wantBase, cmd.BaseAmountUSD, 1e-12)
			require.InDelta(t, wantBase*0.5, cmd.BillableAmountUSD, 1e-12)
			// 展示复用解析器，但免费 Fast 不得污染随后计算的 Fast 成本。
			market := NewModelMarketplaceService(nil, nil, &GatewayService{resolver: svc.resolver}, svc.billingService, nil, nil, nil)
			display := market.getPublicModelDisplayPricing(context.Background(), group, "gpt-5.6-sol")
			require.Len(t, display.ContextIntervals, 2)
			ratio := 3.0
			if free {
				ratio = 1
			}
			require.InDelta(t, display.ContextIntervals[1].InputPricePerToken*ratio, display.ContextIntervals[1].FastInputPricePerToken, 1e-12)
			resolved := svc.resolver.Resolve(context.Background(), PricingInput{Model: "gpt-5.6-sol", Group: group})
			require.Equal(t, 3.0, *resolved.BasePricing.FastMultiplier)
		})
	}
}

// 免费 Fast 不能让不支持该档位的模型在市场中多出 Fast 价格。
func TestGroupPricingFreeFastDisplayRespectsModelSupport(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{resolver: NewModelPricingResolver(nil, bs)}, bs, nil, nil, nil)
	group := &Group{ID: 1, Platform: PlatformOpenAI, RateMultiplier: 1, FreeOpenAIFast: true,
		ModelPricing: []ChannelModelPricing{{Models: []string{"embedding-parity"}, InputPrice: testPtrFloat64(0.01)}},
	}
	display := svc.getPublicModelDisplayPricing(context.Background(), group, "embedding-parity")
	require.Equal(t, 0.01, display.InputPricePerToken)
	require.Zero(t, display.FastInputPricePerToken)
	// 旧零倍率仍是明确的 Fast 配置，启用免费 Fast 后展示实际收取的 Standard 价格。
	group.ModelPricing = []ChannelModelPricing{{Models: []string{"embedding-parity"}, InputPrice: testPtrFloat64(0.02), FastModeMultiplier: testPtrFloat64(0)}}
	display = svc.getPublicModelDisplayPricing(context.Background(), group, "embedding-parity")
	require.Equal(t, 0.02, display.FastInputPricePerToken)
}

// 默认单价必须用于区间外请求、区间空字段和区间倍率，不能因添加区间而丢失。
func TestConfiguredIntervalsPreserveDefaultPrices(t *testing.T) {
	for _, source := range []string{"group", "channel"} {
		for _, model := range []string{"claude-sonnet-4", "custom-priced"} {
			t.Run(source+"/"+model, func(t *testing.T) {
				card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{model}, BillingMode: BillingModeToken,
					InputPrice: testPtrFloat64(0.001), OutputPrice: testPtrFloat64(0.002),
					CacheWritePrice: testPtrFloat64(0.003), CacheWrite1hPrice: testPtrFloat64(0.004), CacheReadPrice: testPtrFloat64(0.005),
					PriceMultiplier: testPtrFloat64(1.2), FastMultiplier: testPtrFloat64(1.5),
					Intervals: []PricingInterval{
						{MinTokens: 100, MaxTokens: testPtrInt(200), InputMultiplier: testPtrFloat64(2), OutputPrice: testPtrFloat64(0), CacheWriteMultiplier: testPtrFloat64(2), CacheReadPrice: testPtrFloat64(0.001)},
						{MinTokens: 200, InputPrice: testPtrFloat64(0.008)},
					},
				}
				_, err := normalizeGroupModelPricing(PlatformOpenAI, []ChannelModelPricing{card})
				require.NoError(t, err)
				r := newResolverWithChannel(t, nil)
				group := &Group{ID: 100, Platform: PlatformOpenAI, ModelPricing: []ChannelModelPricing{card}}
				if source == "channel" {
					r = newResolverWithChannel(t, []ChannelModelPricing{card})
					group.ModelPricing = nil
				}
				for _, tc := range []struct {
					input    int
					standard float64
				}{{10, 0.19}, {110, 0.38}, {210, 1.86}} {
					for _, tier := range []string{"default", "priority"} {
						cost, err := r.billingService.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: model, Group: group, GroupID: &group.ID,
							Tokens:         UsageTokens{InputTokens: tc.input, OutputTokens: 5, CacheCreationTokens: 20, CacheCreation5mTokens: 10, CacheCreation1hTokens: 10, CacheReadTokens: 20},
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
					intervals := []PricingInterval{{MinTokens: 0, InputPrice: testPtrFloat64(0.001), CacheReadPrice: testPtrFloat64(0.0001)}}
					if tierCount == 2 {
						intervals[0].MaxTokens = testPtrInt(100)
						intervals = append(intervals, PricingInterval{MinTokens: 100, InputPrice: testPtrFloat64(0.002), CacheReadPrice: testPtrFloat64(0.0002)})
					}
					card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"custom-priced"}, BillingMode: BillingModeToken,
						FastMultiplier: testPtrFloat64(3), Intervals: intervals}
					_, err := normalizeGroupModelPricing(PlatformOpenAI, []ChannelModelPricing{card})
					require.NoError(t, err)
					group := &Group{ID: 100, Platform: PlatformOpenAI, RateMultiplier: 0.5, FreeOpenAIFast: free, ModelPricing: []ChannelModelPricing{card}}
					r := newResolverWithChannel(t, nil)
					if source == "channel" {
						r = newResolverWithChannel(t, []ChannelModelPricing{card})
						group.ModelPricing = nil
					}
					svc := NewModelMarketplaceService(nil, nil, &GatewayService{resolver: r}, r.billingService, nil, nil, nil)
					display := svc.getPublicModelDisplayPricing(context.Background(), group, "custom-priced")
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
					cost, err := r.billingService.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "custom-priced", Group: group, GroupID: &group.ID,
						Tokens: UsageTokens{InputTokens: 50}, RateMultiplier: group.RateMultiplier, ServiceTier: "priority", Resolver: r})
					require.NoError(t, err)
					require.InDelta(t, 50*0.001*3, cost.TotalCost, 1e-12)
				})
			}
		}
	}
}

// 分时配置独立生效，渠道不能忽略没有填写单价的有效价卡。
func TestTimeOnlyPricingGroupChannelParity(t *testing.T) {
	card := ChannelModelPricing{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
		TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "10:00", Multiplier: 2}}},
	}
	require.NoError(t, validatePricingEntries([]ChannelModelPricing{card}))
	var costs []float64
	for _, source := range []string{"group", "channel"} {
		group := &Group{ID: 100, Platform: PlatformAnthropic, ModelPricing: []ChannelModelPricing{card}}
		r := newResolverWithChannel(t, nil)
		if source == "channel" {
			r = newResolverWithChannel(t, []ChannelModelPricing{card})
			group.ModelPricing = nil
		}
		cost, err := r.billingService.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "claude-sonnet-4", Group: group, GroupID: &group.ID,
			Tokens: UsageTokens{InputTokens: 100}, RateMultiplier: 1, PricingAt: time.Date(2026, 9, 9, 9, 30, 0, 0, time.UTC), Resolver: r})
		require.NoError(t, err)
		costs = append(costs, cost.ActualCost)
	}
	require.Equal(t, costs[0], costs[1])
	require.InDelta(t, 0.0006, costs[0], 1e-12)
}

// Fast 只改变倍率，不能清空内置的图片输入和输出价格桶。
func TestTierOnlyPricingPreservesImagePricesEqually(t *testing.T) {
	card := ChannelModelPricing{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken, FastMultiplier: testPtrFloat64(2)}
	var costs []float64
	for _, source := range []string{"group", "channel"} {
		bs := newTestBillingServiceForResolver()
		bs.fallbackPrices["claude-sonnet-4"].InputPricePerToken = 0.001
		bs.fallbackPrices["claude-sonnet-4"].ImageInputPricePerToken = 0.003
		bs.fallbackPrices["claude-sonnet-4"].ImageOutputPricePerToken = 0.004
		group := &Group{ID: 100, Platform: PlatformAnthropic, ModelPricing: []ChannelModelPricing{card}}
		r := newResolverWithBillingService(t, bs, nil)
		if source == "channel" {
			r = newResolverWithBillingService(t, bs, []ChannelModelPricing{card})
			group.ModelPricing = nil
		}
		cost, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "claude-sonnet-4", Group: group, GroupID: &group.ID,
			Tokens: UsageTokens{InputTokens: 200, ImageInputTokens: 100, OutputTokens: 50, ImageOutputTokens: 50}, RateMultiplier: 1, ServiceTier: "priority", Resolver: r})
		require.NoError(t, err)
		costs = append(costs, cost.ActualCost)
	}
	require.Equal(t, costs[0], costs[1])
	require.InDelta(t, 1.2, costs[0], 1e-12)
}

// 分组与渠道按同一个身份候选匹配型号档位别名。
func TestGroupChannelAliasMatchingParity(t *testing.T) {
	card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"gpt-5.6-luna"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(0.001)}
	bs := NewBillingService(nil, nil)
	group := &Group{ID: 100, Platform: PlatformOpenAI, ModelPricing: []ChannelModelPricing{card}}
	groupResolver := NewModelPricingResolver(nil, bs)
	channelResolver := newResolverWithBillingService(t, bs, []ChannelModelPricing{card})
	groupPrice := groupResolver.Resolve(context.Background(), PricingInput{Model: "gpt-5.6-luna-high", Group: group, GroupID: &group.ID})
	channelPrice := channelResolver.Resolve(context.Background(), PricingInput{Model: "gpt-5.6-luna-high", GroupID: &group.ID})
	require.Equal(t, channelPrice.BasePricing.InputPricePerToken, groupPrice.BasePricing.InputPricePerToken)
	require.Equal(t, 0.001, groupPrice.BasePricing.InputPricePerToken)
}

// Qoder 与其他平台一样继承未填的基础价格桶，不再强制手工价。
func TestQoderGroupChannelBlankPricesParity(t *testing.T) {
	card := ChannelModelPricing{Platform: PlatformQoder, Models: []string{"claude-opus-4-6"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(0.001)}
	var costs []float64
	for _, source := range []string{"group", "channel"} {
		bs := NewBillingService(nil, nil)
		group := &Group{ID: 100, Platform: PlatformQoder, ModelPricing: []ChannelModelPricing{card}}
		r := newResolverWithBillingService(t, bs, nil)
		if source == "channel" {
			r = newResolverWithBillingService(t, bs, []ChannelModelPricing{card})
			group.ModelPricing = nil
		}
		gateway := &GatewayService{resolver: r, billingService: bs}
		resolved, model := gateway.resolveChannelPricingForUsage(context.Background(), "claude-opus-4-6", &APIKey{Group: group, GroupID: &group.ID})
		require.NotNil(t, resolved)
		cost, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: model, Group: group, GroupID: &group.ID, Tokens: UsageTokens{OutputTokens: 100}, RateMultiplier: 1, Resolver: r, Resolved: resolved})
		require.NoError(t, err)
		costs = append(costs, cost.ActualCost)
	}
	require.Equal(t, costs[1], costs[0])
	require.InDelta(t, 0.0025, costs[0], 1e-12)
}

// 保留内置来源，确保纯倍率不会意外禁用内置峰值定价；渠道和分组只覆盖同名倍率。
func TestModifierCardsPreserveBuiltinPricingPolicy(t *testing.T) {
	model := "deepseek-v4-flash"
	card := ChannelModelPricing{Platform: PlatformDeepseek, Models: []string{model}, FastMultiplier: testPtrFloat64(3),
		TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "01:00", EndTime: "04:00", Multiplier: 2}}}}
	for _, scope := range []string{"group", "channel", "both"} {
		t.Run(scope, func(t *testing.T) {
			bs := NewBillingService(nil, nil)
			group := &Group{ID: 100, Platform: PlatformDeepseek, LongContextPricingEnabled: true}
			var channelCards []ChannelModelPricing
			if scope != "group" {
				channelCards = []ChannelModelPricing{card}
			} else {
				group.ModelPricing = []ChannelModelPricing{card}
			}
			if scope == "both" {
				group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, FastMultiplier: testPtrFloat64(1.5)}}
			}
			r := newResolverWithBillingService(t, bs, channelCards)
			for _, hour := range []int{0, 1, 3, 4} {
				at := time.Date(2026, 9, 9, hour, 0, 0, 0, time.UTC)
				resolved := r.Resolve(context.Background(), PricingInput{Model: model, GroupID: &group.ID, Group: group})
				require.Equal(t, PricingSourceLiteLLM, resolved.Source)
				cost, err := bs.CalculateCostUnified(CostInput{Model: model, GroupID: &group.ID, Group: group, Resolver: r,
					Tokens: UsageTokens{InputTokens: 100}, RateMultiplier: 1, PricingAt: at, ServiceTier: "priority"})
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
				var prices []ModelDisplayPricing
				var costs []*CostBreakdown
				for _, platform := range []string{PlatformQoder, PlatformOpenAI} {
					group := &Group{ID: 100, Platform: platform, RateMultiplier: 1}
					card := ChannelModelPricing{Models: []string{model}, Platform: platform}
					switch kind {
					case "modifiers":
						card.FastMultiplier = testPtrFloat64(2)
						card.TimePricing = &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "12:00", Multiplier: 2}}}
					case "free":
						card.InputPrice, card.OutputPrice = testPtrFloat64(0), testPtrFloat64(0)
					}
					bs := NewBillingService(nil, nil)
					r := newResolverWithBillingService(t, bs, []ChannelModelPricing{card})
					gateway := &GatewayService{resolver: r, billingService: bs}
					market := NewModelMarketplaceService(nil, nil, gateway, bs, nil, nil, nil)
					prices = append(prices, market.getRequestableModelDisplayPricing(context.Background(), group, marketplaceModelDef{ID: model, PricingModel: model}))
					result := &ForwardResult{Usage: ClaudeUsage{InputTokens: 100, OutputTokens: 10}, ServiceTier: testPtrString("priority")}
					if looksLikeImageModel(model) {
						result.ImageCount = 1
					}
					costs = append(costs, gateway.calculateRecordUsageCost(context.Background(), result, &APIKey{Group: group}, &Account{Platform: platform},
						model, model, BillingModelSourceRequested, model, 1, 1, &recordUsageOpts{PricingAt: time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)}))
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
