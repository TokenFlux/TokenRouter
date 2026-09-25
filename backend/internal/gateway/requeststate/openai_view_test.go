package requeststate

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 验证视图保留首个重复字段、宽松读取及不展开未知字段的补丁行为。
func TestOpenAIViewCompatibility(t *testing.T) {
	body := []byte(`{"model":" first ","model":"second","stream":true,"input":[{"future":{"value":123456789012345678}}],"reasoning":{"effort":" high "},"service_tier":" priority "}`)
	view := NewOpenAIRequestView(body)
	require.Equal(t, "first", view.Model)
	require.Equal(t, "high", view.ReasoningEffort)
	require.True(t, view.Stream)
	require.True(t, view.HasServiceTier)
	require.Equal(t, "priority", view.ServiceTier)
	require.Same(t, &body[0], &view.Bytes()[0])
	view.MarkPatchSet("reasoning.effort", "low")
	view.MarkPatchDelete("service_tier")
	patched, err := view.ApplyPatches()
	require.NoError(t, err)
	require.Equal(t, "123456789012345678", gjson.GetBytes(patched, "input.0.future.value").Raw)
	require.Equal(t, "low", gjson.GetBytes(patched, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(patched, "service_tier").Exists())
	require.Equal(t, " high ", gjson.GetBytes(body, "reasoning.effort").String())
}
func TestOpenAIViewDisabledPatches(t *testing.T) {
	view := NewOpenAIRequestView([]byte(`{"model":"gpt-5"}`))
	view.MarkPatchSet(`unsupported\.field`, true)
	require.True(t, view.PatchesDisabled())
	require.False(t, view.HasPatches())
	_, err := view.ApplyPatches()
	require.Error(t, err)
}
