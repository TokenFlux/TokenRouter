package service

import (
	"encoding/json"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestOpenAIChatReasoningAliasForkConsumers 验证 fork 的流聚合、首输出与静默拒绝链路共享别名语义。
func TestOpenAIChatReasoningAliasForkConsumers(t *testing.T) {
	payload := `{"id":"chatcmpl-alias","model":"reasoning-model","choices":[{"index":0,"delta":{"reasoning":"fork reasoning"},"finish_reason":"stop"}]}`
	var chunk apicompat.ChatCompletionsChunk
	require.NoError(t, json.Unmarshal([]byte(payload), &chunk))

	accumulator := newOpenAIChatCompletionsStreamAccumulator("reasoning-model")
	accumulator.ObservePayload(payload)
	require.Equal(t, "fork reasoning", gjson.GetBytes(accumulator.ResponseBody(nil), "choices.0.message.reasoning_content").String())
	require.True(t, chatChunkStartsResponsesOutput(&chunk))

	detector := newOpenAIChatSilentRefusalDetector(openAISilentRefusalMinRequestBodyBytes)
	detector.ObserveChatChunk(chunk)
	require.False(t, detector.IsSilentRefusal())
	require.True(t, detector.ShouldReleaseClientOutput())
}

// TestOpenAIChatReasoningAliasFormalFieldPrecedence 验证 fork 消费者不会拼接被正式字段覆盖的别名。
func TestOpenAIChatReasoningAliasFormalFieldPrecedence(t *testing.T) {
	payload := `{"id":"chatcmpl-alias","choices":[{"index":0,"delta":{"reasoning_content":"preferred","reasoning":"fallback"}}]}`
	accumulator := newOpenAIChatCompletionsStreamAccumulator("reasoning-model")
	accumulator.ObservePayload(payload)

	require.Equal(t, "preferred", gjson.GetBytes(accumulator.ResponseBody(nil), "choices.0.message.reasoning_content").String())
}
