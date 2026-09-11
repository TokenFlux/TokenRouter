package bridge

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// function-only 上游返回 fc_ ID；若还原为 custom_tool_call 仍保留该前缀，
// 客户端下一次重放历史时会被 Responses API 拒绝。
func TestRestoreResponsesClientToolPayloadRetypesItemIDs(t *testing.T) {
	mapping := ResponsesClientToolMapping{
		CustomTools: map[string]bool{"exec": true}, ToolSearch: true,
		NamespaceTools: map[string]ResponsesNamespaceName{"team__send": {Namespace: "team", Name: "send"}},
	}
	payload := []byte(`{"id":"resp","output":[` +
		`{"type":"function_call","id":"fc_abc123","call_id":"call_1","name":"exec","arguments":"{\"input\":\"dir\"}"},` +
		`{"type":"function_call","id":"fc_def456","call_id":"call_2","name":"tool_search","arguments":"{\"query\":\"git\"}"},` +
		`{"type":"function_call","id":"fc_ghi789","call_id":"call_3","name":"team__send","arguments":"{}"}]}`)

	restored, changed, err := RestoreResponsesClientToolPayload(payload, mapping)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, `{"id":"resp","output":[`+
		`{"type":"custom_tool_call","id":"ctc_abc123","call_id":"call_1","name":"exec","input":"dir"},`+
		`{"type":"tool_search_call","id":"tsc_def456","call_id":"call_2","execution":"client","arguments":{"query":"git"}},`+
		`{"type":"function_call","id":"fc_ghi789","call_id":"call_3","name":"send","namespace":"team","arguments":"{}"}]}`,
		string(restored))
}

func TestRestoreResponsesOutputClientToolsRetypesItemIDs(t *testing.T) {
	mapping := ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}, ToolSearch: true}
	outputs := []ResponsesOutput{
		{Type: "function_call", ID: "fc_abc123", CallID: "call_1", Name: "exec", Arguments: `{"input":"dir"}`},
		{Type: "function_call", ID: "fc_def456", CallID: "call_2", Name: toolSearchProxyName, Arguments: `{"query":"git"}`},
	}

	restoreResponsesOutputClientTools(outputs, &mapping)

	require.Equal(t, "custom_tool_call", outputs[0].Type)
	require.Equal(t, "ctc_abc123", outputs[0].ID)
	require.Equal(t, "call_1", outputs[0].CallID)
	require.Equal(t, "tool_search_call", outputs[1].Type)
	require.Equal(t, "tsc_def456", outputs[1].ID)
	require.Equal(t, "call_2", outputs[1].CallID)
}

func TestResponsesClientToolStreamRestorerRetypesCustomItemID(t *testing.T) {
	const upstreamID = "fc_09f77ac43cf7db36016a8920e7934487"
	const clientID = "ctc_09f77ac43cf7db36016a8920e7934487"

	restorer := NewResponsesClientToolStreamRestorer(ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}})

	added := restorer.Restore(ResponsesStreamEvent{
		Type: "response.output_item.added", SequenceNumber: 0, OutputIndex: 0,
		Item: &ResponsesOutput{Type: "function_call", ID: upstreamID, CallID: "call_1", Name: "exec", Status: "in_progress"},
	})
	require.Len(t, added, 1)
	require.Equal(t, "custom_tool_call", added[0].Item.Type)
	require.Equal(t, clientID, added[0].Item.ID)

	// 后续事件仍通过上游 ID 匹配，但发给客户端的事件使用重typed ID。
	require.Empty(t, restorer.Restore(ResponsesStreamEvent{
		Type: "response.function_call_arguments.delta", SequenceNumber: 1, ItemID: upstreamID, Delta: `{"input":"di`,
	}))
	done := restorer.Restore(ResponsesStreamEvent{
		Type: "response.function_call_arguments.done", SequenceNumber: 2, ItemID: upstreamID,
		CallID: "call_1", Name: "exec", Arguments: `{"input":"dir"}`,
	})
	require.Len(t, done, 2)
	require.Equal(t, "response.custom_tool_call_input.delta", done[0].Type)
	require.Equal(t, clientID, done[0].ItemID)
	require.Equal(t, "response.custom_tool_call_input.done", done[1].Type)
	require.Equal(t, clientID, done[1].ItemID)
	require.Equal(t, "call_1", done[1].CallID)

	closed := restorer.Restore(ResponsesStreamEvent{
		Type: "response.output_item.done", SequenceNumber: 3, OutputIndex: 0,
		Item: &ResponsesOutput{Type: "function_call", ID: upstreamID, CallID: "call_1", Name: "exec", Arguments: `{"input":"dir"}`, Status: "completed"},
	})
	require.Len(t, closed, 1)
	require.Equal(t, "custom_tool_call", closed[0].Item.Type)
	require.Equal(t, clientID, closed[0].Item.ID)
	require.Equal(t, "dir", closed[0].Item.Input)
}

func TestResponsesClientToolStreamRestorerRetypesToolSearchItemID(t *testing.T) {
	restorer := NewResponsesClientToolStreamRestorer(ResponsesClientToolMapping{ToolSearch: true})

	added := restorer.Restore(ResponsesStreamEvent{
		Type: "response.output_item.added", SequenceNumber: 0, OutputIndex: 0,
		Item: &ResponsesOutput{Type: "function_call", ID: "fc_search1", CallID: "call_1", Name: toolSearchProxyName, Status: "in_progress"},
	})
	require.Len(t, added, 1)
	require.Equal(t, "tool_search_call", added[0].Item.Type)
	require.Equal(t, "tsc_search1", added[0].Item.ID)
}

// WS bridge 会把客户端还原后的项目再次发回上游，因此 ctc_/tsc_ 必须恢复为 fc_。
func TestAdaptResponsesClientToolsRecoversRetypedItemID(t *testing.T) {
	req := map[string]any{
		"tools": []any{
			map[string]any{"type": "custom", "name": "exec"},
			map[string]any{"type": "tool_search"},
		},
		"input": []any{
			map[string]any{"type": "custom_tool_call", "id": "ctc_upstream1", "call_id": "call_1", "name": "exec", "input": "dir"},
			map[string]any{"type": "tool_search_call", "id": "tsc_upstream2", "call_id": "call_2", "arguments": map[string]any{"query": "git"}},
			map[string]any{"type": "custom_tool_call_output", "id": "ctco_client", "call_id": "call_1", "output": "ok"},
		},
	}

	_, changed, err := AdaptResponsesClientTools(req)
	require.NoError(t, err)
	require.True(t, changed)

	input := requireResponsesClientToolValue[[]any](t, req["input"])
	require.Len(t, input, 3)
	customCall := requireResponsesClientToolValue[map[string]any](t, input[0])
	require.Equal(t, "function_call", customCall["type"])
	require.Equal(t, "fc_upstream1", customCall["id"])
	searchCall := requireResponsesClientToolValue[map[string]any](t, input[1])
	require.Equal(t, "function_call", searchCall["type"])
	require.Equal(t, "fc_upstream2", searchCall["id"])
	customOutput := requireResponsesClientToolValue[map[string]any](t, input[2])
	require.Equal(t, "function_call_output", customOutput["type"])
	require.NotContains(t, customOutput, "id")
}
