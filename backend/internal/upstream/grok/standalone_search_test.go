package grok

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 来源白名单和稳定排序由原生解析拥有，模型生成的陌生 URL 不得进入结果。
func TestStandaloneSourcesRequireObservedURL(t *testing.T) {
	body := []byte(`{"output":[{"type":"web_search_call","action":{"sources":[{"url":"https://Example.test/a#source","title":"source A","snippet":"source snippet"},{"url":"https://example.test/b","title":"123"}]}},{"type":"message","content":[{"type":"output_text","text":"{\"results\":[{\"url\":\"https://invented.test\",\"title\":\"invented\"},{\"url\":\"https://example.test/a\",\"title\":\"enriched\",\"snippet\":\"summary\"}]}"}]}]}`)
	results := ExtractGrokWebSearchSources(body, 5)
	require.Len(t, results, 2)
	require.Equal(t, "https://Example.test/a#source", results[0].URL)
	require.Equal(t, "enriched", results[0].Title)
	require.Equal(t, "summary", results[0].Snippet)
	require.Equal(t, "example.test", results[1].Title)
	require.Len(t, ExtractGrokWebSearchSources(body, 1), 1)
	require.Nil(t, ExtractGrokWebSearchSources([]byte("invalid"), 5))
}
func TestStandaloneNativeBodiesRetainToolOptions(t *testing.T) {
	disabled := false
	max := 3
	body, err := BuildGrokXSearchResponsesBody(StandaloneSearchRequest{Input: "query", MaxResults: &max, AllowedXHandles: []string{"xai"}, FromDate: " 2026-01-01 ", EnableImageUnderstanding: &disabled}, DefaultTextModel)
	require.NoError(t, err)
	require.Equal(t, "x_search", gjson.GetBytes(body, "tools.0.type").String())
	require.Equal(t, "xai", gjson.GetBytes(body, "tools.0.allowed_x_handles.0").String())
	require.Equal(t, "2026-01-01", gjson.GetBytes(body, "tools.0.from_date").String())
	require.True(t, gjson.GetBytes(body, "tools.0.enable_image_understanding").Exists())
	require.False(t, gjson.GetBytes(body, "tools.0.enable_image_understanding").Bool())
	require.Equal(t, "required", gjson.GetBytes(body, "tool_choice").String())
	require.Contains(t, gjson.GetBytes(body, "input").String(), "at most 3")
	web := BuildGrokWebSearchResponsesBody("query", 0, DefaultTextModel)
	require.Equal(t, "web_search", gjson.GetBytes(web, "tools.0.type").String())
	require.False(t, gjson.GetBytes(web, "stream").Bool())
	require.Equal(t, "web_search_call.action.sources", gjson.GetBytes(web, "include.0").String())
}
