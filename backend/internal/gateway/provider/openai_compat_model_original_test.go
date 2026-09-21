package provider

import (
	"testing"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAICompatRequestedModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "gpt reasoning alias strips xhigh", input: "gpt-5.4-xhigh", want: "gpt-5.4"},
		{name: "gpt reasoning alias strips max", input: "gpt-5.6-sol-max", want: "gpt-5.6-sol"},
		{name: "gpt reasoning alias keeps unsupported ultra", input: "gpt-5.6-terra-ultra", want: "gpt-5.6-terra-ultra"},
		{name: "gpt luna strips max", input: "gpt-5.6-luna-max", want: "gpt-5.6-luna"},
		{name: "gpt luna keeps unsupported ultra suffix", input: "gpt-5.6-luna-ultra", want: "gpt-5.6-luna-ultra"},
		{name: "old gpt keeps unsupported max suffix", input: "gpt-5.5-max", want: "gpt-5.5-max"},
		{name: "gpt reasoning alias strips none", input: "gpt-5.4-none", want: "gpt-5.4"},
		{name: "codex max model stays intact", input: "gpt-5.1-codex-max", want: "gpt-5.1-codex-max"},
		{name: "non openai model unchanged", input: "claude-opus-4-6", want: "claude-opus-4-6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, NormalizeOpenAICompatRequestedModel(tt.input))
		})
	}
}

func TestApplyOpenAICompatModelNormalization(t *testing.T) {
	t.Parallel()

	t.Run("derives xhigh from model suffix when output config missing", func(t *testing.T) {
		req := &protocolanthropic.AnthropicRequest{Model: "gpt-5.4-xhigh"}
		ApplyOpenAICompatModelNormalization(req)

		require.Equal(t, "gpt-5.4", req.Model)
		require.NotNil(t, req.OutputConfig)
		require.Equal(t, "max", req.OutputConfig.Effort)
	})

	t.Run("does not derive unsupported ultra suffix", func(t *testing.T) {
		req := &protocolanthropic.AnthropicRequest{Model: "gpt-5.6-terra-ultra"}
		ApplyOpenAICompatModelNormalization(req)

		require.Equal(t, "gpt-5.6-terra-ultra", req.Model)
		require.Nil(t, req.OutputConfig)
	})

	t.Run("explicit output config wins over model suffix", func(t *testing.T) {
		req := &protocolanthropic.AnthropicRequest{
			Model:        "gpt-5.4-xhigh",
			OutputConfig: &protocolanthropic.AnthropicOutputConfig{Effort: "low"},
		}
		ApplyOpenAICompatModelNormalization(req)

		require.Equal(t, "gpt-5.4", req.Model)
		require.NotNil(t, req.OutputConfig)
		require.Equal(t, "low", req.OutputConfig.Effort)
	})

	t.Run("non openai model is untouched", func(t *testing.T) {
		req := &protocolanthropic.AnthropicRequest{Model: "claude-opus-4-6"}
		ApplyOpenAICompatModelNormalization(req)

		require.Equal(t, "claude-opus-4-6", req.Model)
		require.Nil(t, req.OutputConfig)
	})
}
