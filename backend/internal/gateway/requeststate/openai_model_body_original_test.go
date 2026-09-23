package requeststate

import (
	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIModelMappedBody(t *testing.T) {
	body := []byte(`{"model":"alias","input":"hello"}`)
	calls := 0

	forwardBody := ModelMappedBody(body, true, "gpt-5.4", func(body []byte, newModel string) []byte {
		calls++
		return openaiprotocol.ReplaceModelInBody(body, newModel)
	})

	require.Equal(t, 1, calls)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(forwardBody, "model").String())
	require.Equal(t, "alias", gjson.GetBytes(body, "model").String())
}

func TestOpenAIModelMappedBodyCache(t *testing.T) {
	body := []byte(`{"model":"alias","input":"hello"}`)
	calls := 0
	mappedBody := NewModelMappedBodyCache(body, func(body []byte, newModel string) []byte {
		calls++
		return openaiprotocol.ReplaceModelInBody(body, newModel)
	})

	first := mappedBody(true, "gpt-5.4")
	second := mappedBody(true, "gpt-5.4")
	third := mappedBody(true, "gpt-5.3-codex")
	unmapped := mappedBody(false, "ignored")

	require.Equal(t, 2, calls)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(first, "model").String())
	require.Equal(t, "gpt-5.4", gjson.GetBytes(second, "model").String())
	require.Equal(t, "gpt-5.3-codex", gjson.GetBytes(third, "model").String())
	require.Equal(t, body, unmapped)
	require.Same(t, &first[0], &second[0])
}
