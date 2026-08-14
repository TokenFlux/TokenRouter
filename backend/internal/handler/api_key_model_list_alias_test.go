package handler

import (
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAppendAPIKeyAliasesToGeminiModelsJSON(t *testing.T) {
	body := appendAPIKeyAliasesToGeminiModelsJSON([]byte(`{
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

func TestAppendBatchImageAPIKeyModelAliasesPreservesProvider(t *testing.T) {
	models := appendBatchImageAPIKeyModelAliases([]service.BatchImagePublicModel{
		{ID: "gemini-image", Object: "image.batch.model", Provider: "gemini"},
		{ID: "gemini-image", Object: "image.batch.model", Provider: "antigravity"},
	}, map[string]string{
		"image-review": "gemini-image",
		"wild-*":       "gemini-image",
	})

	require.Equal(t, []service.BatchImagePublicModel{
		{ID: "gemini-image", Object: "image.batch.model", Provider: "gemini"},
		{ID: "gemini-image", Object: "image.batch.model", Provider: "antigravity"},
		{ID: "image-review", Object: "image.batch.model", Provider: "gemini"},
		{ID: "image-review", Object: "image.batch.model", Provider: "antigravity"},
	}, models)
}
