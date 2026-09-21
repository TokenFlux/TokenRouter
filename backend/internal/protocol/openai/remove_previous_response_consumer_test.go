//go:build unit

package openai_test

import (
	testing "testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	require "github.com/stretchr/testify/require"
	gjson "github.com/tidwall/gjson"
)

func TestRemovePreviousResponseIDFromBody(t *testing.T) {
	t.Run("empty body returned as-is", func(t *testing.T) {
		require.Equal(t, []byte{}, openai.RemovePreviousResponseIDFromBody([]byte{}))
		require.Nil(t, openai.RemovePreviousResponseIDFromBody(nil))
	})

	t.Run("no previous_response_id field is a no-op", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5","input":"hi"}`)
		result := openai.RemovePreviousResponseIDFromBody(body)
		require.Equal(t, body, result)
	})

	t.Run("strips previous_response_id and preserves other fields", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5","previous_response_id":"resp_abc","input":"hi"}`)
		result := openai.RemovePreviousResponseIDFromBody(body)
		require.False(t, gjson.GetBytes(result, "previous_response_id").Exists())
		require.Equal(t, "gpt-5", gjson.GetBytes(result, "model").String())
		require.Equal(t, "hi", gjson.GetBytes(result, "input").String())
	})

	t.Run("empty-string previous_response_id is also stripped", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5","previous_response_id":""}`)
		result := openai.RemovePreviousResponseIDFromBody(body)
		require.False(t, gjson.GetBytes(result, "previous_response_id").Exists())
	})
}
