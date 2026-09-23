package openai_test

import (
	"encoding/json"
	"testing"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/stretchr/testify/require"
)

// TestOpenAIChatReasoningAliasForkConsumers 验证首输出与静默拒绝链路共享别名语义。
func TestOpenAIChatReasoningAliasForkConsumers(t *testing.T) {
	payload := `{"id":"chatcmpl-alias","model":"reasoning-model","choices":[{"index":0,"delta":{"reasoning":"fork reasoning"},"finish_reason":"stop"}]}`
	var chunk protocolopenai.ChatCompletionsChunk
	require.NoError(t, json.Unmarshal([]byte(payload), &chunk))

	require.True(t, protocolopenai.ChatChunkStartsResponsesOutput(&chunk))

	detector := openai.NewChatSilentRefusalDetector(openai.SilentRefusalMinRequestBodyBytes)
	detector.ObserveChatChunk(chunk)
	require.False(t, detector.IsSilentRefusal())
	require.True(t, detector.ShouldReleaseClientOutput())
}
