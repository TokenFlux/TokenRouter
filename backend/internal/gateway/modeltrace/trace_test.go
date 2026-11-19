package modeltrace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRewriteAPIKeyAdditionalModelsRejectsAmbiguousJSON verifies that tool
// model redirection rewrites canonical tool models while rejecting duplicate
// or case-variant JSON keys without partially mutating the body.
func TestRewriteAPIKeyAdditionalModelsRejectsAmbiguousJSON(t *testing.T) {
	mapping := map[string]string{"alias-img": "gpt-image-2"}
	clean := []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation","model":"alias-img"}]}`)
	rewritten, err := RewriteAPIKeyAdditionalModels(clean, mapping)
	require.NoError(t, err)
	require.Contains(t, string(rewritten), `"gpt-image-2"`)

	for _, raw := range []string{
		`{"model":"gpt-5.4","tools":[{"type":"image_generation","model":"alias-img","model":"other"}]}`,
		`{"model":"gpt-5.4","tools":[{"type":"image_generation","model":"alias-img","Model":"other"}]}`,
	} {
		out, err := RewriteAPIKeyAdditionalModels([]byte(raw), mapping)
		require.Error(t, err, raw)
		require.Equal(t, raw, string(out))
	}
}
