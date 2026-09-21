package httpapi

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAppendAPIKeyAliasesToGeminiModelsJSON(t *testing.T) {
	body := (&ModelsHandler{}).AppendAPIKeyAliasesToGeminiModelsJSON([]byte(`{
		"models":[{"name":"models/gemini-3.1-pro-preview","displayName":"Gemini Pro","description":"keep"}],
		"nextPageToken":"next"
	}`), map[string]string{
		"gemini-review": "gemini-3.1-pro-preview",
		"missing":       "gemini-missing",
		"wild-*":        "gemini-3.1-pro-preview",
	})

	require.Equal(t, int64(2), gjson.GetBytes(body, "models.#").Int())
	require.Equal(t, "models/gemini-review", gjson.GetBytes(body, "models.1.name").String())
	require.Equal(t, "gemini-review", gjson.GetBytes(body, "models.1.displayName").String())
	require.Equal(t, "keep", gjson.GetBytes(body, "models.1.description").String())
	require.Equal(t, "next", gjson.GetBytes(body, "nextPageToken").String())
}
