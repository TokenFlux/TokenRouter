package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIWSPayloadString_OnlyAcceptsStringValues(t *testing.T) {
	payload := map[string]any{
		"type":                 nil,
		"model":                123,
		"prompt_cache_key":     " cache-key ",
		"previous_response_id": []byte(" resp_1 "),
	}

	require.Equal(t, "", WSPayloadString(payload, "type"))
	require.Equal(t, "", WSPayloadString(payload, "model"))
	require.Equal(t, "cache-key", WSPayloadString(payload, "prompt_cache_key"))
	require.Equal(t, "resp_1", WSPayloadString(payload, "previous_response_id"))
}
