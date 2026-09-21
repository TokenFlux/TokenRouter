package forward

import (
	json "encoding/json"
	testing "testing"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	require "github.com/stretchr/testify/require"
	gjson "github.com/tidwall/gjson"
)

// TestChatReasoningAliasRequestConversion 验证历史 assistant reasoning 别名不会在 Chat 转 Responses 时丢失。
func TestChatReasoningAliasRequestConversion(t *testing.T) {
	request := &protocolopenai.ChatCompletionsRequest{
		Model: "reasoning-model",
		Messages: []protocolopenai.ChatMessage{
			{Role: "assistant", Reasoning: "prior plan", Content: json.RawMessage(`"answer"`)},
		},
	}

	converted, err := ChatCompletionsToResponses(request)
	require.NoError(t, err)
	require.Equal(t, "<thinking>prior plan</thinking>\nanswer", gjson.GetBytes(converted.Input, "0.content.0.text").String())
}
