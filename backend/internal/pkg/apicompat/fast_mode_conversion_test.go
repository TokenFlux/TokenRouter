package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicFastConvertsToOpenAIPriority(t *testing.T) {
	content, err := json.Marshal("hello")
	require.NoError(t, err)
	converted, err := AnthropicToResponses(&AnthropicRequest{
		Model:     "claude-opus-4.8",
		MaxTokens: 128,
		Speed:     "fast",
		Messages:  []AnthropicMessage{{Role: "user", Content: content}},
	})
	require.NoError(t, err)
	require.Equal(t, "priority", converted.ServiceTier)
}

func TestOpenAIPriorityConvertsToClaudeFast(t *testing.T) {
	input, err := json.Marshal("hello")
	require.NoError(t, err)
	converted, err := ResponsesToAnthropicRequest(&ResponsesRequest{
		Model:       "claude-opus-4.8",
		Input:       input,
		ServiceTier: "priority",
	})
	require.NoError(t, err)
	require.Equal(t, "fast", converted.Speed)
}
