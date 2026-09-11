package service

import (
	"encoding/json"
	"testing"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/stretchr/testify/require"
)

// TestOpenAIChatReasoningAliasForkConsumers 验证首输出与静默拒绝链路共享别名语义。
func TestOpenAIChatReasoningAliasForkConsumers(t *testing.T) {
	payload := `{"id":"chatcmpl-alias","model":"reasoning-model","choices":[{"index":0,"delta":{"reasoning":"fork reasoning"},"finish_reason":"stop"}]}`
	var chunk protocolopenai.ChatCompletionsChunk
	require.NoError(t, json.Unmarshal([]byte(payload), &chunk))

	require.True(t, chatChunkStartsResponsesOutput(&chunk))

	detector := newOpenAIChatSilentRefusalDetector(openAISilentRefusalMinRequestBodyBytes)
	detector.ObserveChatChunk(chunk)
	require.False(t, detector.IsSilentRefusal())
	require.True(t, detector.ShouldReleaseClientOutput())
}
