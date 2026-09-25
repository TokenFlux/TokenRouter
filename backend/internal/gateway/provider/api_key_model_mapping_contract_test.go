package provider_test

import (
	"context"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 以下合同直接验证所属模块，保留原输入与断言。
func TestChannelMappingChainIncludesAPIKeyRedirectAndDeduplicatesStages(t *testing.T) {
	ctx := modeltrace.WithContext(
		context.Background(),
		modeltrace.NewAPIKeyModelRedirectTrace("codex-auto-review", "codex-auto-review", "gpt-5.6-luna"),
	)
	mapping := modeltrace.WithChannelRedirect((routing.ChannelMappingResult{
		MappedModel:        "gpt-5.6-luna-channel",
		Mapped:             true,
		BillingModelSource: routing.BillingModelSourceChannelMapped,
	}), ctx, "gpt-5.6-luna")

	fields := mapping.ToUsageFields("gpt-5.6-luna", "gpt-5.6-luna-upstream")
	require.Equal(t, "gpt-5.6-luna", fields.OriginalModel)
	require.Equal(t, "gpt-5.6-luna-channel", fields.ChannelMappedModel)
	require.Equal(t, "codex-auto-review→gpt-5.6-luna→gpt-5.6-luna-channel→gpt-5.6-luna-upstream", fields.ModelMappingChain)

	// 同一模型在后续阶段再次出现时只保留第一次，避免映射链形成回环噪声。
	require.Equal(t, "codex-auto-review→gpt-5.6-luna→gpt-5.6-luna-channel", mapping.BuildModelMappingChain("gpt-5.6-luna", "codex-auto-review"))
	require.Equal(t, []string{"gpt-5.6-luna", "gpt-5.6-luna-channel"}, mustAPIKeyResponseModels(t, ctx))
}

func TestResolveAccountUpstreamModelRegistersFinalRedirectStage(t *testing.T) {
	ctx := modeltrace.WithContext(
		context.Background(),
		modeltrace.NewAPIKeyModelRedirectTrace("model-alias", "model-alias", "key-target"),
	)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
		Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"key-target": "upstream-target"},
		}},
	}

	require.Equal(t, "upstream-target", gatewayprovider.ExecutionModelPolicy(account).UpstreamModel(ctx, "key-target"))
	require.Equal(t, []string{"key-target", "upstream-target"}, mustAPIKeyResponseModels(t, ctx))
}

func mustAPIKeyResponseModels(t *testing.T, ctx context.Context) []string {
	t.Helper()
	trace, ok := modeltrace.FromContext(ctx)
	require.True(t, ok)
	return trace.ResponseModels()
}
