package qoder_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestQoderGatewayOpenAIUsageDerivesTotalFromPromptWhenUpstreamTotalMissing(t *testing.T) {
	event := qoderCachedUsageEventForTest
	event.TotalTokens = 0
	body, err := qoder.BuildQoderOpenAICompletion("auto", []qoder.SSEEvent{
		{Type: "text_delta", Text: "OK"},
		event,
		{IsDone: true},
	})
	require.NoError(t, err)

	require.Equal(t, int64(66637), gjson.GetBytes(body, "usage.prompt_tokens").Int())
	require.Equal(t, int64(6), gjson.GetBytes(body, "usage.completion_tokens").Int())
	require.Equal(t, int64(66643), gjson.GetBytes(body, "usage.total_tokens").Int())
	require.Equal(t, int64(66612), gjson.GetBytes(body, "usage.prompt_tokens_details.cached_tokens").Int())
}
