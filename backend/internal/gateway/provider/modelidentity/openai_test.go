package modelidentity_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/stretchr/testify/require"
)

// 裸 GPT-5.6 不再注册为内置型号，也不映射到 Sol 或旧 GPT。
func TestNormalizeKnownOpenAICodexModel_BareGPT56IsNotBuiltin(t *testing.T) {
	tests := map[string]string{
		"gpt-5.6":            "",
		"openai/gpt-5.6":     "",
		"gpt5.6":             "",
		"gpt-5.6-high":       "",
		"gpt-5.6-max":        "",
		"gpt-5.6-2026-07-09": "",
		"gpt-5.6-20260709":   "",
		"openai/gpt-5.6-max": "",
	}

	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, expected, modelidentity.NormalizeOpenAI(input))
		})
	}
}

func TestUsageBillingModelCandidates_BareGPT56ExcludesSol(t *testing.T) {
	require.Equal(t,
		[]string{"gpt-5.6"},
		modelidentity.UsageCandidates("gpt-5.6"),
	)
	require.Equal(t,
		[]string{"openai/gpt-5.6", "gpt-5.6"},
		modelidentity.UsageCandidates("openai/gpt-5.6"),
	)
}

func TestNormalizeKnownOpenAICodexModel_GPT6Astra(t *testing.T) {
	tests := map[string]string{
		"gpt-6-astra":                 "gpt-6-astra",
		"openai/gpt-6-astra":          "gpt-6-astra",
		"gpt-6-astra-preview":         "gpt-6-astra",
		"openai/gpt-6-astra-20260901": "gpt-6-astra",
		"gpt-6-astral":                "",
	}

	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, expected, modelidentity.NormalizeOpenAI(input))
		})
	}
}
