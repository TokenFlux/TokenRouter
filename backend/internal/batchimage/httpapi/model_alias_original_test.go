package httpapi

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/stretchr/testify/require"
)

func TestAppendBatchImageAPIKeyModelAliasesPreservesProvider(t *testing.T) {
	models := AppendBatchImageAPIKeyModelAliases([]batchimage.BatchImagePublicModel{
		{ID: "gemini-image", Object: "image.batch.model", Provider: "gemini"},
		{ID: "gemini-image", Object: "image.batch.model", Provider: "antigravity"},
	}, map[string]string{
		"image-review": "gemini-image",
		"wild-*":       "gemini-image",
	})

	require.Equal(t, []batchimage.BatchImagePublicModel{
		{ID: "gemini-image", Object: "image.batch.model", Provider: "gemini"},
		{ID: "gemini-image", Object: "image.batch.model", Provider: "antigravity"},
		{ID: "image-review", Object: "image.batch.model", Provider: "gemini"},
		{ID: "image-review", Object: "image.batch.model", Provider: "antigravity"},
	}, models)
}
