//go:build unit

package forward_test

import (
	"encoding/json"

	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"

	"github.com/stretchr/testify/require"
)

func TestAdaptResponsesClientToolsForAnthropic_FlattensNamespace(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model":"claude-fable-5",
		"input":[{"type":"function_call","call_id":"call_1","namespace":"codex_app","name":"read_thread","arguments":"{}"}],
		"tools":[{"type":"namespace","name":"codex_app","tools":[{"type":"function","name":"read_thread","description":"Read a task","parameters":{"type":"object","properties":{}}}]}]
	}`)

	adapted, mapping, err := forward.AdaptResponsesClientToolsForAnthropic(body)
	require.NoError(t, err)
	require.Equal(t, bridge.ResponsesNamespaceName{Namespace: "codex_app", Name: "read_thread"}, mapping.NamespaceTools["codex_app__read_thread"])

	var request map[string]any
	require.NoError(t, json.Unmarshal(adapted, &request))
	tools := testassert.MustType[[]any](request["tools"])
	require.Len(t, tools, 1)
	tool := testassert.MustType[map[string]any](tools[0])
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "codex_app__read_thread", tool["name"])

	input := testassert.MustType[[]any](request["input"])
	call := testassert.MustType[map[string]any](input[0])
	require.Equal(t, "codex_app__read_thread", call["name"])
	require.NotContains(t, call, "namespace")
}

func TestAdaptResponsesClientToolsForAnthropic_LiftsAdditionalTools(t *testing.T) {
	body := []byte(`{
		"model":"claude-fable-5",
		"input":[
			{"type":"additional_tools","tools":[
				{"type":"custom","name":"exec","description":"Run a command"},
				{"type":"namespace","name":"codex_app","tools":[
					{"type":"function","name":"read_thread","parameters":{"type":"object"}}
				]}
			]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect"}]}
		]
	}`)

	adapted, mapping, err := forward.AdaptResponsesClientToolsForAnthropic(body)
	require.NoError(t, err)
	require.True(t, mapping.CustomTools["exec"])
	require.Equal(t, bridge.ResponsesNamespaceName{Namespace: "codex_app", Name: "read_thread"}, mapping.NamespaceTools["codex_app__read_thread"])

	var request map[string]any
	require.NoError(t, json.Unmarshal(adapted, &request))
	tools := testassert.MustType[[]any](request["tools"])
	require.Len(t, tools, 2)
	require.Equal(t, "function", testassert.MustType[map[string]any](tools[0])["type"])
	require.Equal(t, "codex_app__read_thread", testassert.MustType[map[string]any](tools[1])["name"])

	input := testassert.MustType[[]any](request["input"])
	require.Len(t, input, 1)
	require.Equal(t, "message", testassert.MustType[map[string]any](input[0])["type"])
}

func TestAppendRawJSON_EmptyObjectPlaceholder(t *testing.T) {
	t.Parallel()

	fragment := `{"query":"status"}`
	require.JSONEq(t, fragment, string(forward.AppendRawJSON(json.RawMessage("{ \n\t }"), fragment)))
	require.Equal(t, `{"existing":true}{"query":"status"}`, string(forward.AppendRawJSON(json.RawMessage(`{"existing":true}`), fragment)))
}

func TestExtractResponsesReasoningEffortFromBody(t *testing.T) {
	t.Parallel()

	got := responsesEffortFixture([]byte(`{"model":"claude-sonnet-4.5","reasoning":{"effort":"HIGH"}}`))
	require.NotNil(t, got)
	require.Equal(t, "high", *got)

	maxGot := responsesEffortFixture([]byte(`{"model":"deepseek-v4-pro","reasoning":{"effort":"max"}}`))
	require.NotNil(t, maxGot)
	require.Equal(t, "max", *maxGot)

	mappedMax := responsesEffortFixture(
		[]byte(`{"model":"public-alias","reasoning":{"effort":"max"}}`),
		"provider/glm-5.2",
		"public-alias",
	)
	require.NotNil(t, mappedMax)
	require.Equal(t, "max", *mappedMax)

	require.Nil(t, responsesEffortFixture([]byte(`{"model":"claude-sonnet-4.5"}`)))
}
