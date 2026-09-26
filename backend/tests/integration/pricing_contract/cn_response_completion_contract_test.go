//go:build unit

package pricingcontract

import (
	"context"
	"testing"
	"time"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestFilterCNProviderBillingModelCandidates(t *testing.T) {
	svc := completion.NewRecorder(completion.Dependencies{}, completion.RecorderOptions{DefaultMultiplier: 1})

	apiKey := &apikey.APIKey{Group: &routing.Group{ID: 1, Platform: capability.PlatformKimi}}
	cnAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformKimi}}

	filtered := svc.FilterCNProviderBillingModelCandidates(
		context.Background(), gatewayprovider.ProjectCompletionAccount(gatewayprovider.ExecutionCompletionRecord(cnAccount)), gatewayprovider.ProjectCompletionKey(apiKey), []string{"kimi-k2-0905-preview", "claude-sonnet-4-5", "sonnet-custom", "moonshot-v1-8k"},
	)
	require.Equal(t, []string{"kimi-k2-0905-preview", "moonshot-v1-8k"}, filtered)

	require.Empty(t, svc.FilterCNProviderBillingModelCandidates(
		context.Background(), gatewayprovider.ProjectCompletionAccount(gatewayprovider.ExecutionCompletionRecord(cnAccount)), gatewayprovider.ProjectCompletionKey(apiKey), []string{"claude-sonnet-4-5"},
	))

	openAIAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI}}
	require.Equal(t, []string{"claude-sonnet-4-5"}, svc.FilterCNProviderBillingModelCandidates(
		context.Background(), gatewayprovider.ProjectCompletionAccount(gatewayprovider.ExecutionCompletionRecord(openAIAccount)), gatewayprovider.ProjectCompletionKey(apiKey), []string{"claude-sonnet-4-5"},
	))
}

func TestFilterCNProviderBillingModelCandidatesKeepsExplicitGroupPricing(t *testing.T) {
	inputPrice := 0.000001
	outputPrice := 0.000002
	billing := billingtestkit.Calculator(0, nil, nil)
	svc := completion.NewRecorder(completion.Dependencies{
		Calculator: billing,
		Prices:     billingtestkit.PriceResolver(nil, billing),
	}, completion.RecorderOptions{DefaultMultiplier: 1})

	group := &routing.Group{
		ID:       1,
		Platform: capability.PlatformKimi,
		ModelPricing: []routing.ModelPricingEntry{{
			Models:      []string{"claude-sonnet-4-5"},
			BillingMode: routing.BillingModeToken,
			InputPrice:  &inputPrice,
			OutputPrice: &outputPrice,
		}},
	}
	apiKey := &apikey.APIKey{Group: group}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformKimi}}

	require.Equal(t, []string{"claude-sonnet-4-5"}, svc.FilterCNProviderBillingModelCandidates(
		context.Background(), gatewayprovider.ProjectCompletionAccount(gatewayprovider.ExecutionCompletionRecord(account)), gatewayprovider.ProjectCompletionKey(apiKey), []string{"claude-sonnet-4-5"},
	))
}

func TestCalculateOpenAIRecordUsageCostEmptyCandidatesIsPricingUnavailable(t *testing.T) {
	svc := completion.NewRecorder(completion.Dependencies{}, completion.RecorderOptions{DefaultMultiplier: 1})

	apiKey := &apikey.APIKey{Group: &routing.Group{ID: 1, Platform: capability.PlatformKimi}}

	_, err := svc.CalculateOpenAIRecordUsageCostAt(
		context.Background(), gatewayprovider.ProjectOpenAICompletionResult(nil, nil), gatewayprovider.ProjectCompletionKey(apiKey), nil,
		1, 1, 1, 1, pricing.UsageTokens{InputTokens: 100}, "", time.Time{},
	)
	require.Error(t, err)
	require.True(t, completion.IsUsagePricingUnavailableError(err), err)
}
