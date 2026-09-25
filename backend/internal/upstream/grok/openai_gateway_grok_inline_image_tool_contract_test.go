//go:build unit

package grok_test

import (
	"testing"

	grok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStripRedundantGrokChatViewImageToolLeavesNonTargetRequestsByteExact(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{
			name: "current turn has no inline image",
			body: `{"messages":[{"role":"user","content":"Inspect a local image"}],"tools":[{"type":"function","function":{"name":"view_image"}}]}`,
		},
		{
			name: "inline image is only historical",
			body: `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]},{"role":"assistant","content":"Done"},{"role":"user","content":"Inspect another local image"}],"tools":[{"type":"function","function":{"name":"view_image"}}]}`,
		},
		{
			name: "view image is explicitly selected",
			body: `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}],"tools":[{"type":"function","function":{"name":"view_image"}}],"tool_choice":{"type":"function","function":{"name":"view_image"}}}`,
		},
		{
			name: "required with view image as the only tool",
			body: `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}],"tools":[{"type":"function","function":{"name":"view_image"}}],"tool_choice":"required"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(tt.body)
			patched, err := grok.StripRedundantGrokChatViewImageTool(body)
			require.NoError(t, err)
			require.Equal(t, body, patched)
		})
	}
}

func TestStripRedundantGrokChatViewImageToolDropsOnlyToolMetadata(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}],
		"tools":[{"type":"function","function":{"name":"view_image"}}],
		"tool_choice":"auto",
		"parallel_tool_calls":true
	}`)

	patched, err := grok.StripRedundantGrokChatViewImageTool(body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(patched, "parallel_tool_calls").Exists())
}
