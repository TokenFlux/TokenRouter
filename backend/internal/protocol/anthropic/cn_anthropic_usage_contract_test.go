package anthropic_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"github.com/stretchr/testify/require"
)

func TestParseSSEUsagePassthroughNormalizesKimiPromptUsage(t *testing.T) {
	usage := &protocol.TokenUsage{}

	protocolanthropic.ParseSSEUsagePassthrough(`{"type":"message_start","message":{"usage":{"input_tokens":173306,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0,"prompt_tokens":173306,"cached_tokens":0}}}`, usage)
	require.Equal(t, 173306, usage.InputTokens)
	require.Zero(t, usage.CacheReadInputTokens)

	protocolanthropic.ParseSSEUsagePassthrough(`{"type":"message_delta","usage":{"input_tokens":250,"cache_creation_input_tokens":0,"cache_read_input_tokens":173056,"output_tokens":166,"prompt_tokens":173306,"cached_tokens":173056}}`, usage)
	require.Equal(t, 250, usage.InputTokens, "Kimi message_delta input_tokens is already the uncached bucket")
	require.Equal(t, 173056, usage.CacheReadInputTokens)
	require.Equal(t, 166, usage.OutputTokens)
}

func TestParseSSEUsagePassthroughKimiFullyCachedInputReplacesStartTotal(t *testing.T) {
	usage := &protocol.TokenUsage{}

	protocolanthropic.ParseSSEUsagePassthrough(`{"type":"message_start","message":{"usage":{"input_tokens":173306,"prompt_tokens":173306}}}`, usage)
	protocolanthropic.ParseSSEUsagePassthrough(`{"type":"message_delta","usage":{"input_tokens":0,"cache_read_input_tokens":173306,"output_tokens":8,"prompt_tokens":173306,"cached_tokens":173306}}`, usage)

	require.Zero(t, usage.InputTokens, "an explicit zero uncached bucket must not retain message_start's total")
	require.Equal(t, 173306, usage.CacheReadInputTokens)
}

func TestParseClaudeUsageFromResponseBodyNormalizesCNProviderAliases(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantInput     int
		wantCacheRead int
		wantOutput    int
	}{
		{
			name:          "Kimi top-level cached_tokens",
			body:          `{"usage":{"input_tokens":173306,"output_tokens":166,"cache_read_input_tokens":173056,"prompt_tokens":173306,"cached_tokens":173056}}`,
			wantInput:     250,
			wantCacheRead: 173056,
			wantOutput:    166,
		},
		{
			name:          "GLM nested prompt cache details",
			body:          `{"usage":{"input_tokens":1200,"output_tokens":300,"prompt_tokens":1200,"prompt_tokens_details":{"cached_tokens":800}}}`,
			wantInput:     400,
			wantCacheRead: 800,
			wantOutput:    300,
		},
		{
			name:          "DeepSeek prompt cache hit and miss buckets",
			body:          `{"usage":{"input_tokens":1200,"output_tokens":300,"prompt_cache_hit_tokens":800,"prompt_cache_miss_tokens":400}}`,
			wantInput:     400,
			wantCacheRead: 800,
			wantOutput:    300,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := protocolanthropic.ParseClaudeUsageFromResponseBody([]byte(tt.body))
			require.Equal(t, tt.wantInput, usage.InputTokens)
			require.Equal(t, tt.wantCacheRead, usage.CacheReadInputTokens)
			require.Equal(t, tt.wantOutput, usage.OutputTokens)
		})
	}
}

func TestParseSSEUsagePassthroughNormalizesGLMAndDeepSeekAliases(t *testing.T) {
	tests := []struct {
		name          string
		data          string
		wantInput     int
		wantCacheRead int
	}{
		{
			name:          "GLM",
			data:          `{"type":"message_delta","usage":{"input_tokens":1200,"output_tokens":30,"prompt_tokens":1200,"prompt_tokens_details":{"cached_tokens":800}}}`,
			wantInput:     400,
			wantCacheRead: 800,
		},
		{
			name:          "DeepSeek",
			data:          `{"type":"message_delta","usage":{"input_tokens":1200,"output_tokens":30,"prompt_cache_hit_tokens":800,"prompt_cache_miss_tokens":400}}`,
			wantInput:     400,
			wantCacheRead: 800,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := &protocol.TokenUsage{}
			protocolanthropic.ParseSSEUsagePassthrough(tt.data, usage)
			require.Equal(t, tt.wantInput, usage.InputTokens)
			require.Equal(t, tt.wantCacheRead, usage.CacheReadInputTokens)
			require.Equal(t, 30, usage.OutputTokens)
		})
	}
}
