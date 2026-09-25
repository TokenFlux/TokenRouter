//go:build unit

package pricing_test

import (
	"testing"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"

	"github.com/stretchr/testify/require"
)

func TestMatchAccountStatsRule_BothEmpty_NoMatch(t *testing.T) {
	rule := &purepricing.AccountStatsPricingRule{}
	require.False(t, purepricing.MatchAccountStatsRule(rule, 1, 10))
}
func TestMatchAccountStatsRule_AccountIDMatch(t *testing.T) {
	rule := &purepricing.AccountStatsPricingRule{AccountIDs: []int64{1, 2, 3}}
	require.True(t, purepricing.MatchAccountStatsRule(rule, 2, 999))
}
func TestMatchAccountStatsRule_GroupIDMatch(t *testing.T) {
	rule := &purepricing.AccountStatsPricingRule{GroupIDs: []int64{10, 20}}
	require.True(t, purepricing.MatchAccountStatsRule(rule, 999, 20))
}
func TestMatchAccountStatsRule_BothConfigured_AccountMatch(t *testing.T) {
	rule := &purepricing.AccountStatsPricingRule{
		AccountIDs: []int64{1, 2},
		GroupIDs:   []int64{10, 20},
	}
	require.True(t, purepricing.MatchAccountStatsRule(rule, 2, 999))
}
func TestMatchAccountStatsRule_BothConfigured_GroupMatch(t *testing.T) {
	rule := &purepricing.AccountStatsPricingRule{
		AccountIDs: []int64{1, 2},
		GroupIDs:   []int64{10, 20},
	}
	require.True(t, purepricing.MatchAccountStatsRule(rule, 999, 10))
}
func TestMatchAccountStatsRule_BothConfigured_NeitherMatch(t *testing.T) {
	rule := &purepricing.AccountStatsPricingRule{
		AccountIDs: []int64{1, 2},
		GroupIDs:   []int64{10, 20},
	}
	require.False(t, purepricing.MatchAccountStatsRule(rule, 999, 999))
}
func TestFindPricingForModel(t *testing.T) {
	exactPricing := purepricing.ChannelModelPricing{
		ID:     1,
		Models: []string{"claude-opus-4"},
	}
	wildcardPricing := purepricing.ChannelModelPricing{
		ID:     2,
		Models: []string{"claude-*"},
	}
	platformPricing := purepricing.ChannelModelPricing{
		ID:       3,
		Platform: "openai",
		Models:   []string{"gpt-4o"},
	}
	emptyPlatformPricing := purepricing.ChannelModelPricing{
		ID:     4,
		Models: []string{"gemini-2.5-pro"},
	}

	tests := []struct {
		name     string
		list     []purepricing.ChannelModelPricing
		platform string
		model    string
		wantID   int64
		wantNil  bool
	}{
		{
			name:     "exact match",
			list:     []purepricing.ChannelModelPricing{exactPricing},
			platform: "anthropic",
			model:    "claude-opus-4",
			wantID:   1,
		},
		{
			name:     "exact match case insensitive",
			list:     []purepricing.ChannelModelPricing{{ID: 5, Models: []string{"Claude-Opus-4"}}},
			platform: "",
			model:    "claude-opus-4",
			wantID:   5,
		},
		{
			name:     "wildcard match",
			list:     []purepricing.ChannelModelPricing{wildcardPricing},
			platform: "anthropic",
			model:    "claude-opus-4",
			wantID:   2,
		},
		{
			name:     "exact match takes priority over wildcard",
			list:     []purepricing.ChannelModelPricing{wildcardPricing, exactPricing},
			platform: "anthropic",
			model:    "claude-opus-4",
			wantID:   1,
		},
		{
			name:     "platform mismatch skipped",
			list:     []purepricing.ChannelModelPricing{platformPricing},
			platform: "anthropic",
			model:    "gpt-4o",
			wantNil:  true,
		},
		{
			name:     "empty platform in pricing matches any",
			list:     []purepricing.ChannelModelPricing{emptyPlatformPricing},
			platform: "gemini",
			model:    "gemini-2.5-pro",
			wantID:   4,
		},
		{
			name:     "empty platform in query matches any pricing platform",
			list:     []purepricing.ChannelModelPricing{platformPricing},
			platform: "",
			model:    "gpt-4o",
			wantID:   3,
		},
		{
			name:     "no match at all",
			list:     []purepricing.ChannelModelPricing{exactPricing, wildcardPricing},
			platform: "anthropic",
			model:    "gpt-4o",
			wantNil:  true,
		},
		{
			name:    "empty list returns nil",
			list:    nil,
			model:   "claude-opus-4",
			wantNil: true,
		},
		{
			name: "wildcard matches by config order (first match wins)",
			list: []purepricing.ChannelModelPricing{
				{ID: 10, Models: []string{"claude-*"}},
				{ID: 11, Models: []string{"claude-opus-*"}},
			},
			platform: "",
			model:    "claude-opus-4",
			wantID:   10, // config order: "claude-*" is first and matches, so it wins
		},
		{
			name: "shorter wildcard used when longer does not match",
			list: []purepricing.ChannelModelPricing{
				{ID: 10, Models: []string{"claude-*"}},
				{ID: 11, Models: []string{"claude-opus-*"}},
			},
			platform: "",
			model:    "claude-sonnet-4",
			wantID:   10, // only "claude-*" matches
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := purepricing.FindPricingForModel(tt.list, tt.platform, tt.model)
			if tt.wantNil {
				require.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			require.Equal(t, tt.wantID, result.ID)
		})
	}
}
func TestCalculateStatsCost_NilPricing(t *testing.T) {
	result := purepricing.CalculateStatsCost(nil, purepricing.UsageTokens{}, 1)
	require.Nil(t, result)
}
func TestCalculateStatsCost_TokenBilling(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode: purepricing.BillingModeToken,
		InputPrice:  contractFloat(0.001),
		OutputPrice: contractFloat(0.002),
	}
	tokens := purepricing.UsageTokens{
		InputTokens:  100,
		OutputTokens: 50,
	}
	result := purepricing.CalculateStatsCost(pricing, tokens, 1)
	require.NotNil(t, result)
	// 100*0.001 + 50*0.002 = 0.1 + 0.1 = 0.2
	require.InDelta(t, 0.2, *result, 1e-12)
}
func TestCalculateStatsCost_TokenBilling_WithCache(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode:     purepricing.BillingModeToken,
		InputPrice:      contractFloat(0.001),
		OutputPrice:     contractFloat(0.002),
		CacheWritePrice: contractFloat(0.003),
		CacheReadPrice:  contractFloat(0.0005),
	}
	tokens := purepricing.UsageTokens{
		InputTokens:         100,
		OutputTokens:        50,
		CacheCreationTokens: 200,
		CacheReadTokens:     300,
	}
	result := purepricing.CalculateStatsCost(pricing, tokens, 1)
	require.NotNil(t, result)
	// 100*0.001 + 50*0.002 + 200*0.003 + 300*0.0005
	// = 0.1 + 0.1 + 0.6 + 0.15 = 0.95
	require.InDelta(t, 0.95, *result, 1e-12)
}
func TestCalculateStatsCost_TokenBilling_WithCacheTTLPricing(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode:       purepricing.BillingModeToken,
		CacheWritePrice:   contractFloat(0.003),
		CacheWrite1hPrice: contractFloat(0.006),
	}
	tokens := purepricing.UsageTokens{
		CacheCreationTokens:   100,
		CacheCreation5mTokens: 40,
		CacheCreation1hTokens: 60,
	}
	result := purepricing.CalculateStatsCost(pricing, tokens, 1)
	require.NotNil(t, result)
	require.InDelta(t, 0.48, *result, 1e-12)
}
func TestCalculateStatsCost_TokenBilling_WithImageOutput(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode:      purepricing.BillingModeToken,
		InputPrice:       contractFloat(0.001),
		OutputPrice:      contractFloat(0.002),
		ImageOutputPrice: contractFloat(0.01),
	}
	tokens := purepricing.UsageTokens{
		InputTokens:       100,
		OutputTokens:      50,
		ImageOutputTokens: 10,
	}
	result := purepricing.CalculateStatsCost(pricing, tokens, 1)
	require.NotNil(t, result)
	// 统计侧的历史口径保留 output 与 image output 两个独立桶。
	// 100*0.001 + 50*0.002 + 10*0.01 = 0.1 + 0.1 + 0.1 = 0.3
	require.InDelta(t, 0.3, *result, 1e-12)
}
func TestCalculateStatsCost_TokenBilling_PartialPricesNil(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode: purepricing.BillingModeToken,
		InputPrice:  contractFloat(0.001),
		// OutputPrice, CacheWritePrice, etc. are all nil → treated as 0
	}
	tokens := purepricing.UsageTokens{
		InputTokens:         100,
		OutputTokens:        50,
		CacheCreationTokens: 200,
	}
	result := purepricing.CalculateStatsCost(pricing, tokens, 1)
	require.NotNil(t, result)
	// Only input contributes: 100*0.001 = 0.1
	require.InDelta(t, 0.1, *result, 1e-12)
}
func TestCalculateStatsCost_TokenBilling_AllTokensZero(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode: purepricing.BillingModeToken,
		InputPrice:  contractFloat(0.001),
		OutputPrice: contractFloat(0.002),
	}
	tokens := purepricing.UsageTokens{} // all zeros
	result := purepricing.CalculateStatsCost(pricing, tokens, 1)
	// totalCost == 0 → returns nil (does not override, falls back to default formula)
	require.Nil(t, result)
}
func TestCalculateStatsCost_TokenBilling_ExplicitZeroPriceOverrides(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode: purepricing.BillingModeToken,
		InputPrice:  contractFloat(0),
	}
	tokens := purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}
	result := purepricing.CalculateStatsCost(pricing, tokens, 1)
	require.NotNil(t, result)
	require.Zero(t, *result)
}
func TestCalculateStatsCost_TokenBilling_BlankPricingDoesNotOverride(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode: purepricing.BillingModeToken,
	}
	tokens := purepricing.UsageTokens{InputTokens: 100}
	result := purepricing.CalculateStatsCost(pricing, tokens, 1)
	require.Nil(t, result)
}
func TestCalculateStatsCost_PerRequestBilling(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode:     purepricing.BillingModePerRequest,
		PerRequestPrice: contractFloat(0.05),
	}
	tokens := purepricing.UsageTokens{InputTokens: 999, OutputTokens: 999}
	result := purepricing.CalculateStatsCost(pricing, tokens, 3)
	require.NotNil(t, result)
	// 0.05 * 3 = 0.15
	require.InDelta(t, 0.15, *result, 1e-12)
}
func TestCalculateStatsCost_PerRequestBilling_PriceNil(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode: purepricing.BillingModePerRequest,
		// PerRequestPrice is nil
	}
	result := purepricing.CalculateStatsCost(pricing, purepricing.UsageTokens{}, 1)
	require.Nil(t, result)
}
func TestCalculateStatsCost_PerRequestBilling_ExplicitZeroPrice(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode:     purepricing.BillingModePerRequest,
		PerRequestPrice: contractFloat(0),
	}
	result := purepricing.CalculateStatsCost(pricing, purepricing.UsageTokens{}, 1)
	require.NotNil(t, result)
	require.Zero(t, *result)
}
func TestCalculateStatsCost_ImageBilling(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode:     purepricing.BillingModeImage,
		PerRequestPrice: contractFloat(0.10),
	}
	result := purepricing.CalculateStatsCost(pricing, purepricing.UsageTokens{}, 2)
	require.NotNil(t, result)
	// 0.10 * 2 = 0.20
	require.InDelta(t, 0.20, *result, 1e-12)
}
func TestCalculateStatsCost_ImageBilling_PriceNil(t *testing.T) {
	pricing := &purepricing.ChannelModelPricing{
		BillingMode: purepricing.BillingModeImage,
		// PerRequestPrice is nil
	}
	result := purepricing.CalculateStatsCost(pricing, purepricing.UsageTokens{}, 1)
	require.Nil(t, result)
}
func TestCalculateStatsCost_AppliesPriceMultiplier(t *testing.T) {
	t.Run("token 定价", func(t *testing.T) {
		pricing := &purepricing.ChannelModelPricing{
			BillingMode:     purepricing.BillingModeToken,
			PriceMultiplier: contractFloat(1.5),
			InputPrice:      contractFloat(0.001),
		}
		result := purepricing.CalculateStatsCost(pricing, purepricing.UsageTokens{InputTokens: 100}, 1)
		require.NotNil(t, result)
		require.InDelta(t, 0.15, *result, 1e-12)
	})

	t.Run("按次定价", func(t *testing.T) {
		pricing := &purepricing.ChannelModelPricing{
			BillingMode:     purepricing.BillingModePerRequest,
			PriceMultiplier: contractFloat(0),
			PerRequestPrice: contractFloat(0.25),
		}
		result := purepricing.CalculateStatsCost(pricing, purepricing.UsageTokens{}, 2)
		require.NotNil(t, result)
		require.Zero(t, *result)
	})
}
func TestCalculateStatsCost_DefaultBillingMode_FallsToToken(t *testing.T) {
	// BillingMode is empty string (default) → falls into token billing
	pricing := &purepricing.ChannelModelPricing{
		InputPrice:  contractFloat(0.001),
		OutputPrice: contractFloat(0.002),
	}
	tokens := purepricing.UsageTokens{
		InputTokens:  100,
		OutputTokens: 50,
	}
	result := purepricing.CalculateStatsCost(pricing, tokens, 1)
	require.NotNil(t, result)
	require.InDelta(t, 0.2, *result, 1e-12)
}
func TestTryCustomRules_FirstMatchWins(t *testing.T) {
	channel := &struct {
		AccountStatsPricingRules []purepricing.AccountStatsPricingRule
	}{
		AccountStatsPricingRules: []purepricing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{1},
				Pricing: []purepricing.ChannelModelPricing{
					{ID: 100, Models: []string{"claude-opus-4"}, InputPrice: contractFloat(0.01), OutputPrice: contractFloat(0.02)},
				},
			},
			{
				GroupIDs: []int64{1},
				Pricing: []purepricing.ChannelModelPricing{
					{ID: 200, Models: []string{"claude-opus-4"}, InputPrice: contractFloat(0.99), OutputPrice: contractFloat(0.99)},
				},
			},
		},
	}
	tokens := purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}
	result := purepricing.TryCustomRules(channel.AccountStatsPricingRules, 999, 1, "", "claude-opus-4", tokens, 1)
	require.NotNil(t, result)
	// 应使用第一条规则的价格：100*0.01 + 50*0.02 = 2.0
	require.InDelta(t, 2.0, *result, 1e-12)
}
func TestTryCustomRules_SkipsNonMatchingRules(t *testing.T) {
	channel := &struct {
		AccountStatsPricingRules []purepricing.AccountStatsPricingRule
	}{
		AccountStatsPricingRules: []purepricing.AccountStatsPricingRule{
			{
				AccountIDs: []int64{888}, // 不匹配
				Pricing: []purepricing.ChannelModelPricing{
					{ID: 100, Models: []string{"claude-opus-4"}, InputPrice: contractFloat(0.99)},
				},
			},
			{
				GroupIDs: []int64{1}, // 匹配
				Pricing: []purepricing.ChannelModelPricing{
					{ID: 200, Models: []string{"claude-opus-4"}, InputPrice: contractFloat(0.05)},
				},
			},
		},
	}
	tokens := purepricing.UsageTokens{InputTokens: 100}
	result := purepricing.TryCustomRules(channel.AccountStatsPricingRules, 999, 1, "", "claude-opus-4", tokens, 1)
	require.NotNil(t, result)
	// 跳过规则1（账号不匹配），使用规则2：100*0.05 = 5.0
	require.InDelta(t, 5.0, *result, 1e-12)
}
func TestTryCustomRules_NoMatch_ReturnsNil(t *testing.T) {
	channel := &struct {
		AccountStatsPricingRules []purepricing.AccountStatsPricingRule
	}{
		AccountStatsPricingRules: []purepricing.AccountStatsPricingRule{
			{
				AccountIDs: []int64{888},
				Pricing: []purepricing.ChannelModelPricing{
					{ID: 100, Models: []string{"claude-opus-4"}, InputPrice: contractFloat(0.01)},
				},
			},
		},
	}
	tokens := purepricing.UsageTokens{InputTokens: 100}
	result := purepricing.TryCustomRules(channel.AccountStatsPricingRules, 999, 2, "", "claude-opus-4", tokens, 1)
	require.Nil(t, result) // 账号和分组都不匹配
}
func TestTryCustomRules_RuleMatchesButModelNot_ContinuesToNext(t *testing.T) {
	channel := &struct {
		AccountStatsPricingRules []purepricing.AccountStatsPricingRule
	}{
		AccountStatsPricingRules: []purepricing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{1},
				Pricing: []purepricing.ChannelModelPricing{
					{ID: 100, Models: []string{"gpt-4o"}, InputPrice: contractFloat(0.01)}, // 模型不匹配
				},
			},
			{
				GroupIDs: []int64{1},
				Pricing: []purepricing.ChannelModelPricing{
					{ID: 200, Models: []string{"claude-opus-4"}, InputPrice: contractFloat(0.05)}, // 模型匹配
				},
			},
		},
	}
	tokens := purepricing.UsageTokens{InputTokens: 100}
	result := purepricing.TryCustomRules(channel.AccountStatsPricingRules, 999, 1, "", "claude-opus-4", tokens, 1)
	require.NotNil(t, result)
	require.InDelta(t, 5.0, *result, 1e-12) // 使用规则2
}

func contractFloat(v float64) *float64 { return &v }
