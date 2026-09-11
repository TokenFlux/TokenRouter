package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

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
