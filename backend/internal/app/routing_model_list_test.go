package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestRequestableNativeProtocolsRequireEveryProvider 检查原生协议的判定。
// 单个提供商承接的模型按它的原生协议标记。多个提供商共同承接时，协议要在每个提供商上都不经过转换才算原生。
func TestRequestableNativeProtocolsRequireEveryProvider(t *testing.T) {
	groupID := int64(7)
	textProtocols := []capability.ProtocolID{capability.ProtocolAnthropicMessages, capability.ProtocolOpenAIResponses, capability.ProtocolOpenAIChatCompletions}
	policies := routing.NewPricingConfigService(modelCatalogueEmptyPrices{}, nil, routing.PricingConfigOptions{ReadGroup: func(_ context.Context, id int64) (*routing.Group, error) {
		return &routing.Group{ID: id, AllowedProtocols: textProtocols}, nil
	}})
	records := []provider.Record{
		{
			ID:       1,
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{"model_whitelist": []string{"claude-sonnet-4-6"}, "model_mapping": map[string]any{
				"claude-only": "claude-sonnet-4-6",
				"shared":      "claude-sonnet-4-6",
			}},
		},
		{
			ID:       2,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{"model_whitelist": []string{"gpt-5.5"}, "model_mapping": map[string]any{
				"shared": "gpt-5.5",
			}},
		},
	}
	resolver := routing.RequestableResolver{GroupPolicies: policies}

	result := resolver.ResolveWithProviders(context.Background(), &groupID, "", []string{"claude-only", "shared"}, gatewayprovider.CatalogueProviders(records))

	models := map[string]routing.RequestableModel{}
	for _, model := range result.Models {
		models[model.ID] = model
	}
	require.Contains(t, models, "claude-only")
	require.Contains(t, models, "shared")
	require.ElementsMatch(t, textProtocols, models["claude-only"].Protocols)
	require.Equal(t, []capability.ProtocolID{capability.ProtocolAnthropicMessages}, models["claude-only"].NativeProtocols)
	require.ElementsMatch(t, textProtocols, models["shared"].Protocols)
	require.Empty(t, models["shared"].NativeProtocols)
}
