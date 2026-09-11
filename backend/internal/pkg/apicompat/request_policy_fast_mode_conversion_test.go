package apicompat

import (
	json "encoding/json"
	require "github.com/stretchr/testify/require"
	testing "testing"
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
