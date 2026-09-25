package httpapi

import (
	"context"

	billingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func fastModeTestContext(policy, model string) context.Context {
	ctx := apikey.WithFastModePolicy(context.Background(), policy)
	ctx = requeststate.WithGroup(ctx, &routing.Group{ID: 11, Platform: capability.PlatformOpenAI})
	return context.WithValue(ctx, telemetry.Model, model)
}
func fastModeTestResolver() *billingcore.PriceResolver {
	pricing := billingprovider.NewPricingServiceFromSnapshot(billingprovider.Options{DefaultOpenAIModel: upstreamopenai.DefaultTestModel, IsImageModel: media.IsImageGenerationModel, ModelLookupCandidates: modelidentity.CandidatesFactory}, nil, billingprovider.Snapshot{Data: map[string]*billingpricing.LiteLLMModelPricing{
		"gpt-5.5": {
			InputCostPerToken:     5e-6,
			OutputCostPerToken:    30e-6,
			SupportsServiceTier:   true,
			SupportsPromptCaching: true,
		},
		"claude-opus-4-8": {
			InputCostPerToken:     5e-6,
			OutputCostPerToken:    25e-6,
			SupportsServiceTier:   true,
			SupportsPromptCaching: true,
		},
	}})
	billing := billingtestkit.Calculator(0, pricing, nil)
	return billingtestkit.PriceResolver(nil, billing)
}

// WebSocket 每个 turn 都应读取最新策略，不能永久复用握手时的策略快照。
func TestOpenAIWSFastModePolicyContextRefreshesEachTurn(t *testing.T) {
	svc := newWSFastPolicy(t, tierpolicy.Default())
	svc.Prices = fastModeTestResolver()
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	baseCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "gpt-5.5")
	policies := map[int]string{
		1: apikey.APIKeyFastModePolicyForceOn,
		2: apikey.APIKeyFastModePolicyForceOff,
	}
	hooks := &gatewayws.OpenAIIngressHooks{
		ResolveFastModePolicy: func(turn int) string {
			return policies[turn]
		},
	}

	turnOneCtx := openAIWSFastModePolicyContext(baseCtx, hooks, 1)
	updated, blocked, err := gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"gpt-5.5"}`), "gpt-5.5", svc.Input(turnOneCtx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())

	turnTwoCtx := openAIWSFastModePolicyContext(baseCtx, hooks, 2)
	updated, blocked, err = gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"gpt-5.5","service_tier":"priority"}`), "gpt-5.5", svc.Input(turnTwoCtx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}
