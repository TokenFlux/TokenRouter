//go:build unit

package pricingcontract

import (
	"context"
	"testing"
	time "time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestCalculateOpenAIRecordUsageCost_SearchIsAdditiveToTokens(t *testing.T) {
	t.Parallel()

	price := 10.0 // 每千次搜索 10 美元，100 次搜索费用为 1 美元。
	svc := completion.NewRecorder(completion.Dependencies{
		Calculator: newCalculator(nil, nil),
	}, completion.RecorderOptions{DefaultMultiplier: 1})

	apiKey := &apikey.APIKey{
		Group: &routing.Group{
			SearchPricePer1k: &price,
		},
	}

	// claude-sonnet-4 回退价格：输入 3 美元/百万令牌，输出 15 美元/百万令牌。
	// 输入 1000、输出 500 个令牌的费用为 0.0105 美元，再加 100 次搜索的 1 美元。
	cost, err := svc.CalculateOpenAIRecordUsageCostAt(
		context.Background(), gatewaycapture.ProjectOpenAICompletionResult(&forwardcore.OpenAIResult{SearchCount: 100}, nil), gatewaycapture.ProjectCompletionKey(apiKey), []string{"claude-sonnet-4"},
		1.0,
		1.0,
		1.0,
		1.0,
		pricing.UsageTokens{InputTokens: 1000, OutputTokens: 500},
		"", time.Time{},
	)
	require.NoError(t, err)
	require.NotNil(t, cost)
	require.InDelta(t, 1.0105, cost.ActualCost, 1e-9)
	require.InDelta(t, 1.0105, cost.TotalCost, 1e-9)
}

func TestCalculateOpenAIRecordUsageCost_SearchOnlyWhenNoTokenPricing(t *testing.T) {
	t.Parallel()

	price := 10.0
	svc := completion.NewRecorder(completion.Dependencies{
		Calculator: newCalculator(nil, nil),
	}, completion.RecorderOptions{DefaultMultiplier: 1})

	apiKey := &apikey.APIKey{
		Group: &routing.Group{SearchPricePer1k: &price},
	}
	// 模型列表为空时令牌路径失败，但仍应计算仅搜索附加费。
	cost, err := svc.CalculateOpenAIRecordUsageCostAt(
		context.Background(), gatewaycapture.ProjectOpenAICompletionResult(&forwardcore.OpenAIResult{SearchCount: 100}, nil), gatewaycapture.ProjectCompletionKey(apiKey), nil,
		1.0,
		1.0,
		1.0,
		1.0,
		pricing.UsageTokens{},
		"", time.Time{},
	)
	require.NoError(t, err)
	require.NotNil(t, cost)
	require.InDelta(t, 1.0, cost.ActualCost, 1e-9)
}

func TestCalculateOpenAIRecordUsageCost_TokenPricingErrorNotSwallowedBySearch(t *testing.T) {
	t.Parallel()

	price := 10.0
	svc := completion.NewRecorder(completion.Dependencies{
		Calculator: newCalculator(nil, nil),
	}, completion.RecorderOptions{DefaultMultiplier: 1})

	apiKey := &apikey.APIKey{
		Group: &routing.Group{SearchPricePer1k: &price},
	}
	// 未知模型会使令牌计价失败，搜索费用不得用零令牌费用或仅搜索账单掩盖该错误。
	cost, err := svc.CalculateOpenAIRecordUsageCostAt(
		context.Background(), gatewaycapture.ProjectOpenAICompletionResult(&forwardcore.OpenAIResult{SearchCount: 100}, nil), gatewaycapture.ProjectCompletionKey(apiKey), []string{"totally-unknown-model-xyz-no-pricing"},
		1.0,
		1.0,
		1.0,
		1.0,
		pricing.UsageTokens{InputTokens: 1000, OutputTokens: 500},
		"", time.Time{},
	)
	require.Error(t, err)
	require.Nil(t, cost)
}
