package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAPIKeyModelMapping(t *testing.T) {
	normalized, err := apikey.NormalizeAPIKeyModelMapping(map[string]string{
		" codex-auto-review ": " gpt-5.6-luna ",
		"claude-*":            "claude-sonnet-4-6",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"codex-auto-review": "gpt-5.6-luna",
		"claude-*":          "claude-sonnet-4-6",
	}, normalized)
}

func TestNormalizeAPIKeyModelMappingRejectsInvalidRules(t *testing.T) {
	tests := map[string]map[string]string{
		"空来源":    {" ": "target"},
		"空目标":    {"source": " "},
		"自身映射":   {"source": "source"},
		"来源中间通配": {"source*-x": "target"},
		"来源多个通配": {"source**": "target"},
		"目标通配":   {"source": "target*"},
		"去空格后重复": {"source": "one", " source ": "two"},
		"来源过长":   {strings.Repeat("源", apikey.MaxAPIKeyModelNameRunes+1): "target"},
		"目标过长":   {"source": strings.Repeat("目", apikey.MaxAPIKeyModelNameRunes+1)},
	}
	for name, mapping := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := apikey.NormalizeAPIKeyModelMapping(mapping)
			require.Error(t, err)
			require.True(t, errors.Is(err, apikey.ErrInvalidAPIKeyModelMapping))
		})
	}

	tooMany := make(map[string]string, apikey.MaxAPIKeyModelMappingRules+1)
	for i := 0; i <= apikey.MaxAPIKeyModelMappingRules; i++ {
		tooMany[fmt.Sprintf("source-%d", i)] = fmt.Sprintf("target-%d", i)
	}
	_, err := apikey.NormalizeAPIKeyModelMapping(tooMany)
	require.Error(t, err)
	require.True(t, errors.Is(err, apikey.ErrInvalidAPIKeyModelMapping))
}

func TestResolveAPIKeyModelMappingPriorityAndSinglePass(t *testing.T) {
	mapping := map[string]string{
		"codex-*":           "wildcard-short",
		"codex-auto-*":      "wildcard-long",
		"codex-auto-review": "gpt-5.6-luna",
		"gpt-5.6-luna":      "must-not-chain",
		"Codex-*":           "case-sensitive",
	}

	model, matched := apikey.ResolveModelMapping(mapping, "codex-auto-review")
	require.True(t, matched)
	require.Equal(t, "gpt-5.6-luna", model)

	model, matched = apikey.ResolveModelMapping(mapping, "codex-auto-fix")
	require.True(t, matched)
	require.Equal(t, "wildcard-long", model)

	model, matched = apikey.ResolveModelMapping(mapping, "Codex-review")
	require.True(t, matched)
	require.Equal(t, "case-sensitive", model)

	model, matched = apikey.ResolveModelMapping(mapping, "CODEX-review")
	require.False(t, matched)
	require.Equal(t, "CODEX-review", model)
}

func TestAppendAPIKeyModelAliases(t *testing.T) {
	models := apikey.AppendAPIKeyModelAliases(
		[]string{"gpt-5.6-luna", "claude-sonnet-4-6", "gpt-5.6-luna"},
		map[string]string{
			"z-review": "gpt-5.6-luna",
			"a-review": "gpt-5.6-luna",
			"missing":  "not-requestable",
			"wild-*":   "gpt-5.6-luna",
		},
	)
	require.Equal(t, []string{
		"gpt-5.6-luna",
		"claude-sonnet-4-6",
		"a-review",
		"z-review",
	}, models)
}

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

	require.Equal(t, "upstream-target", resolveAccountUpstreamModel(ctx, account, "key-target"))
	require.Equal(t, []string{"key-target", "upstream-target"}, mustAPIKeyResponseModels(t, ctx))
}

func mustAPIKeyResponseModels(t *testing.T, ctx context.Context) []string {
	t.Helper()
	trace, ok := modeltrace.FromContext(ctx)
	require.True(t, ok)
	return trace.ResponseModels()
}
