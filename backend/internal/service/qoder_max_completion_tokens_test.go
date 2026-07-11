package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestQoderChatCompletionsRespectsMaxCompletionTokens 验证 max_completion_tokens 优先于 max_tokens
func TestQoderChatCompletionsRespectsMaxCompletionTokens(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"messages": [{"role": "user", "content": "hi"}],
		"max_completion_tokens": 1000,
		"max_tokens": 2000
	}`)

	req, err := parseQoderChatCompletionsPayload(body)

	require.NoError(t, err)
	require.Equal(t, 1000, req.maxTokens, "max_completion_tokens should take precedence")
}

// TestQoderChatCompletionsFallsBackToMaxTokens 验证无 max_completion_tokens 时使用 max_tokens
func TestQoderChatCompletionsFallsBackToMaxTokens(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 2000
	}`)

	req, err := parseQoderChatCompletionsPayload(body)

	require.NoError(t, err)
	require.Equal(t, 2000, req.maxTokens, "should use max_tokens when max_completion_tokens absent")
}

// TestQoderChatCompletionsUsesDefaultWhenBothAbsent 验证两者都缺失时使用默认值
func TestQoderChatCompletionsUsesDefaultWhenBothAbsent(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"messages": [{"role": "user", "content": "hi"}]
	}`)

	req, err := parseQoderChatCompletionsPayload(body)

	require.NoError(t, err)
	require.Equal(t, qoderDefaultMaxTokens, req.maxTokens, "should use default when both absent")
}

// TestQoderChatCompletionsIgnoresZeroMaxTokens 验证 max_tokens=0 时使用 max_completion_tokens
func TestQoderChatCompletionsIgnoresZeroMaxTokens(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"messages": [{"role": "user", "content": "hi"}],
		"max_completion_tokens": 1000,
		"max_tokens": 0
	}`)

	req, err := parseQoderChatCompletionsPayload(body)

	require.NoError(t, err)
	require.Equal(t, 1000, req.maxTokens, "should ignore max_tokens when it's 0")
}
