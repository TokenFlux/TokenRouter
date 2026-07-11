package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

// TestQoderChatSystemTextPreservesDeveloperMessages 验证 developer 消息被合并到 system prompt
func TestQoderChatSystemTextPreservesDeveloperMessages(t *testing.T) {
	messages := []apicompat.ChatMessage{
		{Role: "system", Content: []byte(`"You are helpful"`)},
		{Role: "developer", Content: []byte(`"Be concise"`)},
		{Role: "user", Content: []byte(`"Hello"`)},
	}

	systemText := qoderChatSystemText(messages)

	require.Contains(t, systemText, "You are helpful")
	require.Contains(t, systemText, "Be concise")
	require.Equal(t, "You are helpful\nBe concise", systemText)
}

// TestQoderChatSystemTextOnlySystemMessages 验证只有 system 消息时正常工作
func TestQoderChatSystemTextOnlySystemMessages(t *testing.T) {
	messages := []apicompat.ChatMessage{
		{Role: "system", Content: []byte(`"You are helpful"`)},
		{Role: "user", Content: []byte(`"Hello"`)},
	}

	systemText := qoderChatSystemText(messages)

	require.Equal(t, "You are helpful", systemText)
}

// TestQoderChatSystemTextOnlyDeveloperMessages 验证只有 developer 消息时正常工作
func TestQoderChatSystemTextOnlyDeveloperMessages(t *testing.T) {
	messages := []apicompat.ChatMessage{
		{Role: "developer", Content: []byte(`"Be concise"`)},
		{Role: "user", Content: []byte(`"Hello"`)},
	}

	systemText := qoderChatSystemText(messages)

	require.Equal(t, "Be concise", systemText)
}

// TestQoderChatSystemTextEmptyWhenNoSystemOrDeveloper 验证无 system/developer 时返回空
func TestQoderChatSystemTextEmptyWhenNoSystemOrDeveloper(t *testing.T) {
	messages := []apicompat.ChatMessage{
		{Role: "user", Content: []byte(`"Hello"`)},
		{Role: "assistant", Content: []byte(`"Hi"`)},
	}

	systemText := qoderChatSystemText(messages)

	require.Equal(t, "", systemText)
}

// TestQoderChatSystemTextMultipleDeveloperMessages 验证多个 developer 消息都被保留
func TestQoderChatSystemTextMultipleDeveloperMessages(t *testing.T) {
	messages := []apicompat.ChatMessage{
		{Role: "system", Content: []byte(`"You are helpful"`)},
		{Role: "developer", Content: []byte(`"Be concise"`)},
		{Role: "developer", Content: []byte(`"Use examples"`)},
		{Role: "user", Content: []byte(`"Hello"`)},
	}

	systemText := qoderChatSystemText(messages)

	require.Contains(t, systemText, "You are helpful")
	require.Contains(t, systemText, "Be concise")
	require.Contains(t, systemText, "Use examples")
}
