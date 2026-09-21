package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPopulateOpenAIUsageFromResponseJSONAcceptsChatUsageShape(t *testing.T) {
	usage := &ForwardUsage{}

	// 非流式 WS 结果可能直接带 Chat Completions usage 字段，必须参与计费。
	PopulateUsageFromResponseJSON(
		[]byte(`{"id":"resp_1","usage":{"prompt_tokens":23,"completion_tokens":6,"prompt_tokens_details":{"cached_tokens":5}}}`),
		usage,
	)
	require.Equal(t, 23, usage.InputTokens)
	require.Equal(t, 6, usage.OutputTokens)
	require.Equal(t, 5, usage.CacheReadInputTokens)
}

func TestReplaceOpenAIWSMessageModel_OptimizedStillCorrect(t *testing.T) {
	noModel := []byte(`{"type":"response.output_text.delta","delta":"hello"}`)
	require.Equal(t, string(noModel), string(ReplaceWSMessageModel(noModel, "gpt-5.1", "custom-model")))

	rootOnly := []byte(`{"type":"response.created","model":"gpt-5.1"}`)
	require.Equal(t, `{"type":"response.created","model":"custom-model"}`, string(ReplaceWSMessageModel(rootOnly, "gpt-5.1", "custom-model")))

	responseOnly := []byte(`{"type":"response.completed","response":{"model":"gpt-5.1"}}`)
	require.Equal(t, `{"type":"response.completed","response":{"model":"custom-model"}}`, string(ReplaceWSMessageModel(responseOnly, "gpt-5.1", "custom-model")))

	both := []byte(`{"model":"gpt-5.1","response":{"model":"gpt-5.1"}}`)
	require.Equal(t, `{"model":"custom-model","response":{"model":"custom-model"}}`, string(ReplaceWSMessageModel(both, "gpt-5.1", "custom-model")))
}
