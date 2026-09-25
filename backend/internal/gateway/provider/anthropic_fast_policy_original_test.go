package provider_test

import (
	billingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"context"
	"net/http"

	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeAPIKeyFastModeWireEncoding(t *testing.T) {
	resolver := fastModeTestResolver()
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey}}

	forceOnCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "claude-opus-4-8")
	body, headers, err := gatewayprovider.
		ApplyAnthropicFastMode(forceOnCtx, resolver, account, "claude-opus-4-8", []byte(`{"model":"claude-opus-4-8"}`), http.Header{})
	require.NoError(t, err)
	require.Equal(t, "fast", gjson.GetBytes(body, "speed").String())
	require.True(t, claude.ContainsBetaToken(claude.GetHeaderRaw(headers, "anthropic-beta"), claude.BetaFastMode))

	forceOffCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "claude-opus-4-8")
	claude.SetHeaderRaw(headers, "anthropic-beta", claude.BetaFastMode+",context-management-2025-06-27")
	body, headers, err = gatewayprovider.
		ApplyAnthropicFastMode(forceOffCtx, resolver, account, "claude-opus-4-8", body, headers)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "speed").Exists())
	require.False(t, claude.ContainsBetaToken(claude.GetHeaderRaw(headers, "anthropic-beta"), claude.BetaFastMode))
	require.True(t, claude.ContainsBetaToken(claude.GetHeaderRaw(headers, "anthropic-beta"), "context-management-2025-06-27"))
}

// Anthropic 直连的强制关闭应覆盖所有凭据类型，且不依赖定价解析器。
func TestClaudeAPIKeyFastModeForceOffIgnoresCapabilityAndCredentialType(t *testing.T) {
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "claude-opus-4-8")

	for _, accountType := range []string{capability.AccountTypeAPIKey, capability.AccountTypeOAuth, capability.AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			headers := http.Header{}
			claude.SetHeaderRaw(headers, "anthropic-beta", claude.BetaFastMode+",context-management-2025-06-27")
			body, updatedHeaders, err := gatewayprovider.
				ApplyAnthropicFastMode(
					ctx, nil,

					&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: accountType}},
					"claude-opus-4-8",
					[]byte(`{"model":"claude-opus-4-8","speed":"fast"}`),
					headers,
				)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(body, "speed").Exists())
			require.False(t, claude.ContainsBetaToken(claude.GetHeaderRaw(updatedHeaders, "anthropic-beta"), claude.BetaFastMode))
			require.True(t, claude.ContainsBetaToken(claude.GetHeaderRaw(updatedHeaders, "anthropic-beta"), "context-management-2025-06-27"))
		})
	}
}

func fastModeTestContext(policy, model string) context.Context {
	ctx := apikey.WithFastModePolicy(context.Background(), policy)
	ctx = requeststate.WithGroup(ctx, &routing.Group{ID: 11, Platform: capability.PlatformOpenAI})
	return context.WithValue(ctx, telemetry.Model, model)
}

func fastModeTestResolver() *billingcore.PriceResolver {
	pricing := billingprovider.NewPricingServiceFromSnapshot(billingprovider.Options{DefaultOpenAIModel: openai.DefaultTestModel, IsImageModel: media.IsImageGenerationModel, ModelLookupCandidates: modelidentity.CandidatesFactory}, nil, billingprovider.Snapshot{Data: map[string]*billingpricing.LiteLLMModelPricing{
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
