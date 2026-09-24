//go:build unit

package anthropic_test

import (
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	"github.com/stretchr/testify/require"
)

// --- parseSSEUsage 测试 ---

func TestParseSSEUsage_MessageStart(t *testing.T) {
	usage := &protocol.TokenUsage{}

	data := `{"type":"message_start","message":{"usage":{"input_tokens":100,"cache_creation_input_tokens":50,"cache_read_input_tokens":200}}}`
	protocolanthropic.ParseSSEUsage(data, usage)

	require.Equal(t, 100, usage.InputTokens)
	require.Equal(t, 50, usage.CacheCreationInputTokens)
	require.Equal(t, 200, usage.CacheReadInputTokens)
	require.Equal(t, 0, usage.OutputTokens, "message_start 不应设置 output_tokens")
}

func TestParseSSEUsage_MessageDelta(t *testing.T) {
	usage := &protocol.TokenUsage{}

	data := `{"type":"message_delta","usage":{"output_tokens":42}}`
	protocolanthropic.ParseSSEUsage(data, usage)

	require.Equal(t, 42, usage.OutputTokens)
	require.Equal(t, 0, usage.InputTokens, "message_delta 的 output_tokens 不应影响已有的 input_tokens")
}

func TestParseSSEUsage_DeltaDoesNotOverwriteStartValues(t *testing.T) {
	usage := &protocol.TokenUsage{}

	// 先处理 message_start
	protocolanthropic.ParseSSEUsage(`{"type":"message_start","message":{"usage":{"input_tokens":100}}}`, usage)
	require.Equal(t, 100, usage.InputTokens)

	// 再处理 message_delta（output_tokens > 0, input_tokens = 0）
	protocolanthropic.ParseSSEUsage(`{"type":"message_delta","usage":{"output_tokens":50}}`, usage)
	require.Equal(t, 100, usage.InputTokens, "delta 中 input_tokens=0 不应覆盖 start 中的值")
	require.Equal(t, 50, usage.OutputTokens)
}

func TestParseSSEUsage_DeltaOverwritesWithNonZero(t *testing.T) {
	usage := &protocol.TokenUsage{}

	// GLM 等 API 会在 delta 中包含所有 usage 信息
	protocolanthropic.ParseSSEUsage(`{"type":"message_delta","usage":{"input_tokens":200,"output_tokens":100,"cache_creation_input_tokens":30,"cache_read_input_tokens":60}}`, usage)
	require.Equal(t, 200, usage.InputTokens)
	require.Equal(t, 100, usage.OutputTokens)
	require.Equal(t, 30, usage.CacheCreationInputTokens)
	require.Equal(t, 60, usage.CacheReadInputTokens)
}

func TestParseSSEUsage_DeltaAuthoritativelyUpdatesCacheCreationBreakdown(t *testing.T) {
	usage := &protocol.TokenUsage{}

	protocolanthropic.ParseSSEUsage(`{"type":"message_start","message":{"usage":{"cache_creation_input_tokens":463184,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":463184}}}}`, usage)
	require.Equal(t, 463184, usage.CacheCreationInputTokens)
	require.Equal(t, 0, usage.CacheCreation5mTokens)
	require.Equal(t, 463184, usage.CacheCreation1hTokens)

	protocolanthropic.ParseSSEUsage(`{"type":"message_delta","usage":{"cache_creation_input_tokens":463184,"cache_creation":{"ephemeral_5m_input_tokens":463184,"ephemeral_1h_input_tokens":0}}}`, usage)
	require.Equal(t, 463184, usage.CacheCreationInputTokens)
	require.Equal(t, 463184, usage.CacheCreation5mTokens)
	require.Equal(t, 0, usage.CacheCreation1hTokens)
}

func TestParseSSEUsage_InvalidJSON(t *testing.T) {
	usage := &protocol.TokenUsage{}

	// 无效 JSON 不应 panic
	protocolanthropic.ParseSSEUsage("not json", usage)
	require.Equal(t, 0, usage.InputTokens)
	require.Equal(t, 0, usage.OutputTokens)
}

func TestParseSSEUsage_UnknownType(t *testing.T) {
	usage := &protocol.TokenUsage{}

	// 不是 message_start 或 message_delta 的类型
	protocolanthropic.ParseSSEUsage(`{"type":"content_block_delta","delta":{"text":"hello"}}`, usage)
	require.Equal(t, 0, usage.InputTokens)
	require.Equal(t, 0, usage.OutputTokens)
}

func TestParseSSEUsage_EmptyString(t *testing.T) {
	usage := &protocol.TokenUsage{}

	protocolanthropic.ParseSSEUsage("", usage)
	require.Equal(t, 0, usage.InputTokens)
}

func TestParseSSEUsage_DoneEvent(t *testing.T) {
	usage := &protocol.TokenUsage{}

	// [DONE] 事件不应影响 usage
	protocolanthropic.ParseSSEUsage("[DONE]", usage)
	require.Equal(t, 0, usage.InputTokens)
}

// --- 流式响应端到端测试 ---
