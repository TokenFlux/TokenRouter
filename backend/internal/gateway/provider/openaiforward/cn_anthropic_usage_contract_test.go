package openaiforward_test

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"github.com/stretchr/testify/require"
)

func TestMergeAnthropicUsageNormalizesKimiStreamForOpenAIBilling(t *testing.T) {
	var start protocolanthropic.AnthropicStreamEvent
	require.NoError(t, json.Unmarshal([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":173306,"prompt_tokens":173306,"cached_tokens":0}}}`), &start))
	var delta protocolanthropic.AnthropicStreamEvent
	require.NoError(t, json.Unmarshal([]byte(`{"type":"message_delta","usage":{"input_tokens":250,"cache_read_input_tokens":173056,"output_tokens":166,"prompt_tokens":173306,"cached_tokens":173056}}`), &delta))

	usage := &protocol.TokenUsage{}
	protocolanthropic.MergeAnthropicUsage(usage, start.Message.Usage)
	protocolanthropic.MergeAnthropicUsage(usage, *delta.Usage)
	require.Equal(t, 250, usage.InputTokens)
	require.Equal(t, 173056, usage.CacheReadInputTokens)

	openAIUsage := openaiforward.AnthropicUsageToOpenAI(usage)
	require.Equal(t, 173306, openAIUsage.InputTokens, "OpenAI gateway expects an inclusive input total")
	require.Equal(t, 250, openAIUsage.InputTokens-openAIUsage.CacheReadInputTokens-openAIUsage.CacheCreationInputTokens)
	require.Equal(t, 166, openAIUsage.OutputTokens)
}

func TestMergeAnthropicUsageNormalizesGLMAndDeepSeekAliases(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "GLM",
			raw:  `{"input_tokens":1200,"output_tokens":30,"prompt_tokens":1200,"prompt_tokens_details":{"cached_tokens":800}}`,
		},
		{
			name: "DeepSeek",
			raw:  `{"input_tokens":1200,"output_tokens":30,"prompt_cache_hit_tokens":800,"prompt_cache_miss_tokens":400}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var src protocolanthropic.AnthropicUsage
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &src))

			usage := &protocol.TokenUsage{}
			protocolanthropic.MergeAnthropicUsage(usage, src)
			require.Equal(t, 400, usage.InputTokens)
			require.Equal(t, 800, usage.CacheReadInputTokens)

			openAIUsage := openaiforward.AnthropicUsageToOpenAI(usage)
			require.Equal(t, 1200, openAIUsage.InputTokens)
			require.Equal(t, 400, openAIUsage.InputTokens-openAIUsage.CacheReadInputTokens-openAIUsage.CacheCreationInputTokens)
		})
	}
}

func TestClaudeUsageToOpenAIUsagePreservesCNProviderNativeAnthropicBuckets(t *testing.T) {
	tests := []struct {
		name         string
		usage        protocol.TokenUsage
		wantTotal    int
		wantUncached int
	}{
		{
			name: "GLM",
			usage: protocol.TokenUsage{
				InputTokens:              2,
				OutputTokens:             302,
				CacheCreationInputTokens: 733,
				CacheReadInputTokens:     376156,
			},
			wantTotal:    376891,
			wantUncached: 2,
		},
		{
			name: "DeepSeek",
			usage: protocol.TokenUsage{
				InputTokens:          400,
				OutputTokens:         30,
				CacheReadInputTokens: 800,
			},
			wantTotal:    1200,
			wantUncached: 400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openAIUsage := openaiforward.AnthropicUsageToOpenAI(&tt.usage)
			require.Equal(t, tt.wantTotal, openAIUsage.InputTokens)
			require.Equal(t, tt.wantUncached, openAIUsage.InputTokens-openAIUsage.CacheReadInputTokens-openAIUsage.CacheCreationInputTokens)
			require.Equal(t, tt.usage.CacheReadInputTokens, openAIUsage.CacheReadInputTokens)
			require.Equal(t, tt.usage.CacheCreationInputTokens, openAIUsage.CacheCreationInputTokens)
		})
	}
}
