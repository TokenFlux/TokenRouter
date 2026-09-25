package openai_test

import (
	"testing"

	openaicore "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 以下合同直接验证所属模块，保留原输入与断言。
func TestSanitizeOpenAICrossModeFailoverReasoning_DropsWholeEncryptedItem(t *testing.T) {
	body := []byte(`{"model":"gpt-5.1","input":[` +
		`{"type":"message","role":"user","content":"hi"},` +
		`{"type":"reasoning","id":"rs_kiro_1","encrypted_content":"ENC","summary":[{"type":"summary_text","text":"t"}]},` +
		`{"type":"message","role":"assistant","content":"yo"}` +
		`]}`)

	sanitized, changed, err := openaicore.SanitizeOpenAICrossModeFailoverReasoning(body)
	require.NoError(t, err)
	require.True(t, changed)
	// 整个 reasoning 项都应移除，不能只保留脱敏后的空骨架。
	require.NotContains(t, string(sanitized), "reasoning")
	require.NotContains(t, string(sanitized), "rs_kiro_1")
	require.NotContains(t, string(sanitized), "summary_text")
	require.Equal(t, int64(2), gjson.GetBytes(sanitized, "input.#").Int())
}

func TestSanitizeOpenAICrossModeFailoverReasoning_NoEncryptedIsNoop(t *testing.T) {
	body := []byte(`{"model":"gpt-5.1","input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"t"}]}]}`)
	sanitized, changed, err := openaicore.SanitizeOpenAICrossModeFailoverReasoning(body)
	require.NoError(t, err)
	require.False(t, changed, "没有 encrypted_content 的 reasoning 必须保留")
	require.Equal(t, string(body), string(sanitized))
}

func TestSanitizeOpenAICrossModeFailoverReasoning_NoInputIsNoop(t *testing.T) {
	body := []byte(`{"model":"gpt-5.1"}`)
	sanitized, changed, err := openaicore.SanitizeOpenAICrossModeFailoverReasoning(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(sanitized))
}

func TestSanitizeOpenAICrossModeFailoverReasoning_PreservesLargeIntegers(t *testing.T) {
	body := []byte(`{"model":"gpt-5.1","input":[` +
		`{"type":"reasoning","id":"rs_kiro_1","encrypted_content":"ENC"},` +
		`{"type":"message","role":"user","content":"hi"}` +
		`],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"id":{"const":9007199254740993}}}}}]}`)

	sanitized, changed, err := openaicore.SanitizeOpenAICrossModeFailoverReasoning(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(sanitized), `"const":9007199254740993`,
		"清理请求体不能将 JSON 大整数转换为 float64")
}

func TestNormalizeOpenAIAPIKeyStoreFalseReasoningReplay(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","store":false,"input":[` +
		`{"type":"reasoning","id":"rs_encrypted","call_id":"remove","encrypted_content":"cipher","summary":null,"opaque":9007199254740993},` +
		`{"type":"reasoning","id":"rs_server_only","summary":[{"type":"summary_text","text":"drop"}]},` +
		`{"type":"item_reference","id":"rs_server_only"},` +
		`{"type":"item_reference","id":"msg_keep"},` +
		`{"type":"message","id":"msg_keep","role":"user","content":"continue"}` +
		`]}`)

	normalized, changed, err := openaicore.NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, int64(3), gjson.GetBytes(normalized, "input.#").Int())
	require.Equal(t, "reasoning", gjson.GetBytes(normalized, "input.0.type").String())
	require.False(t, gjson.GetBytes(normalized, "input.0.id").Exists())
	require.False(t, gjson.GetBytes(normalized, "input.0.call_id").Exists())
	require.Equal(t, "cipher", gjson.GetBytes(normalized, "input.0.encrypted_content").String())
	require.True(t, gjson.GetBytes(normalized, "input.0.summary").IsArray())
	require.Equal(t, "9007199254740993", gjson.GetBytes(normalized, "input.0.opaque").Raw)
	require.Equal(t, "msg_keep", gjson.GetBytes(normalized, "input.1.id").String())
	require.Equal(t, "message", gjson.GetBytes(normalized, "input.2.type").String())
}

func TestNormalizeOpenAIAPIKeyStoreFalseReasoningReplayRequiresExplicitStoreFalse(t *testing.T) {
	for _, body := range []string{
		`{"input":[{"type":"reasoning","id":"rs_keep"}]}`,
		`{"store":true,"input":[{"type":"reasoning","id":"rs_keep"}]}`,
	} {
		normalized, changed, err := openaicore.NormalizeOpenAIAPIKeyStoreFalseReasoningReplay([]byte(body), false)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, string(normalized))
	}
}

func TestNormalizeOpenAIAPIKeyStoreFalseReasoningReplayKnownCompactMode(t *testing.T) {
	body := []byte(`{"input":[{"type":"reasoning","id":"rs_drop","summary":[]},{"type":"message","content":"continue"}]}`)

	normalized, changed, err := openaicore.NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, true)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, int64(1), gjson.GetBytes(normalized, "input.#").Int())
	require.Equal(t, "message", gjson.GetBytes(normalized, "input.0.type").String())
}

func TestNormalizeOpenAIAPIKeyStoreFalseReasoningReplayRejectsEmptyEncryptedContent(t *testing.T) {
	for _, encrypted := range []string{"null", `""`, `"   "`, "123"} {
		body := []byte(`{"store":false,"input":[{"type":"reasoning","id":"rs_drop","encrypted_content":` + encrypted + `},{"type":"message","content":"continue"}]}`)
		normalized, changed, err := openaicore.NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, false)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, int64(1), gjson.GetBytes(normalized, "input.#").Int())
		require.Equal(t, "message", gjson.GetBytes(normalized, "input.0.type").String())
	}
}

func TestNormalizeOpenAIParallelToolCallsWithoutTools(t *testing.T) {
	withTools := []byte(`{"tools":[{"type":"function","name":"lookup"}],"parallel_tool_calls":false}`)
	normalized, changed, err := openaicore.NormalizeOpenAIParallelToolCallsWithoutTools(withTools, false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(withTools), string(normalized))

	withoutTools := []byte(`{"input":"hi","parallel_tool_calls":true}`)
	normalized, changed, err = openaicore.NormalizeOpenAIParallelToolCallsWithoutTools(withoutTools, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "parallel_tool_calls").Exists())
}

// Lite 工具迁移到 input[].additional_tools 后，仍应按有工具请求处理。
func TestNormalizeOpenAIParallelToolCallsWithoutTools_KeepsResponsesLiteAdditionalTools(t *testing.T) {
	liteBody := []byte(`{"input":[{"type":"message","role":"user","content":"hi"},{"type":"additional_tools","tools":[{"type":"function","name":"spawn_agent"}]}],"parallel_tool_calls":false}`)
	normalized, changed, err := openaicore.NormalizeOpenAIParallelToolCallsWithoutTools(liteBody, false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, gjson.False, gjson.GetBytes(normalized, "parallel_tool_calls").Type)

	// 非 Lite 请求的空 additional_tools 不构成有效工具声明，字段仍需删除。
	emptyLiteBody := []byte(`{"input":[{"type":"additional_tools","tools":[]}],"parallel_tool_calls":true}`)
	normalized, changed, err = openaicore.NormalizeOpenAIParallelToolCallsWithoutTools(emptyLiteBody, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "parallel_tool_calls").Exists())

	// Lite 请求即使没有工具，也必须保留已经固定的 false。
	toolLessLiteBody := []byte(`{"input":"hi","parallel_tool_calls":false}`)
	normalized, changed, err = openaicore.NormalizeOpenAIParallelToolCallsWithoutTools(toolLessLiteBody, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, gjson.False, gjson.GetBytes(normalized, "parallel_tool_calls").Type)
}

// Lite 工具迁移到 input[].additional_tools 后，仍应按有工具请求处理。
func TestNormalizeOpenAIParallelToolCallsWithoutTools_KeepsResponsesLiteAdditionalToolsUpstreamRegression(t *testing.T) {
	liteBody := []byte(`{"input":[{"type":"message","role":"user","content":"hi"},{"type":"additional_tools","tools":[{"type":"function","name":"spawn_agent"}]}],"parallel_tool_calls":false}`)
	normalized, changed, err := openaicore.NormalizeOpenAIParallelToolCallsWithoutTools(liteBody, false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, gjson.False, gjson.GetBytes(normalized, "parallel_tool_calls").Type)

	// 非 Lite 请求的空 additional_tools 不构成有效工具声明，字段仍需删除。
	emptyLiteBody := []byte(`{"input":[{"type":"additional_tools","tools":[]}],"parallel_tool_calls":true}`)
	normalized, changed, err = openaicore.NormalizeOpenAIParallelToolCallsWithoutTools(emptyLiteBody, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "parallel_tool_calls").Exists())

	// Lite 请求即使没有工具，也必须保留已经固定的 false。
	toolLessLiteBody := []byte(`{"input":"hi","parallel_tool_calls":false}`)
	normalized, changed, err = openaicore.NormalizeOpenAIParallelToolCallsWithoutTools(toolLessLiteBody, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, gjson.False, gjson.GetBytes(normalized, "parallel_tool_calls").Type)
}

func TestNormalizeOpenAIResponsesReasoningContentReplayStripsCrossProviderArray(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":[` +
		`{"type":"message","role":"user","content":"one"},` +
		`{"type":"message","role":"assistant","content":"two"},` +
		`{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_1","output":"ok"},` +
		`{"type":"message","role":"user","content":"five"},` +
		`{"type":"reasoning","id":"rs_provider","summary":[{"type":"summary_text","text":"portable"}],"content":[{"type":"reasoning_text","text":"visible reasoning"}],"opaque":9007199254740993},` +
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}` +
		`]}`)

	normalized, changed, err := openaicore.NormalizeOpenAIResponsesReasoningContentReplay(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "reasoning", gjson.GetBytes(normalized, "input.5.type").String())
	require.False(t, gjson.GetBytes(normalized, "input.5.content").Exists())
	require.Equal(t, "portable", gjson.GetBytes(normalized, "input.5.summary.0.text").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(normalized, "input.5.opaque").Raw)
	require.Equal(t, "answer", gjson.GetBytes(normalized, "input.6.content.0.text").String())
}

func TestNormalizeOpenAIResponsesReasoningContentReplayKeepsPortableShapes(t *testing.T) {
	for _, body := range []string{
		`{"input":[{"type":"reasoning","summary":[]}]}`,
		`{"input":[{"type":"reasoning","content":[],"summary":[]}]}`,
		`{"input":[{"type":"message","content":[{"type":"input_text","text":"keep"}]}]}`,
	} {
		normalized, changed, err := openaicore.NormalizeOpenAIResponsesReasoningContentReplay([]byte(body))
		require.NoError(t, err)
		require.False(t, changed)
		require.JSONEq(t, body, string(normalized))
	}
}
