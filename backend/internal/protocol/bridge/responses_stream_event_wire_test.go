package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// marshalEvent 通过自定义 MarshalJSON 序列化事件，并返回解码后的顶层对象。
func marshalEvent(t *testing.T, e ResponsesStreamEvent) map[string]any {
	t.Helper()
	b, err := json.Marshal(e)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

// TestWire_IndexFieldsPresentAtZero 防止 omitempty 丢掉值为 0 但协议必需的索引字段。
func TestWire_IndexFieldsPresentAtZero(t *testing.T) {
	m := marshalEvent(t, ResponsesStreamEvent{
		Type: "response.output_text.delta", OutputIndex: 0, ContentIndex: 0, ItemID: "msg_1", Delta: "hi",
	})
	require.Contains(t, m, "output_index")
	require.Contains(t, m, "content_index")
	require.EqualValues(t, 0, m["output_index"])

	r := marshalEvent(t, ResponsesStreamEvent{
		Type: "response.reasoning_summary_text.delta", OutputIndex: 0, SummaryIndex: 0, ItemID: "rs_1", Delta: "think",
	})
	require.Contains(t, r, "output_index")
	require.Contains(t, r, "summary_index")
}

// TestWire_FunctionCallItemAlwaysComplete 确保 function_call item 始终带
// call_id/name/arguments，包括 .added 时的 arguments:""。
func TestWire_FunctionCallItemAlwaysComplete(t *testing.T) {
	added := marshalEvent(t, ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 1,
		Item:        &ResponsesOutput{Type: "function_call", ID: "fc_1", CallID: "call_a", Name: "exec", Status: "in_progress"},
	})
	item, ok := added["item"].(map[string]any)
	require.True(t, ok, "item must be an object")
	for _, k := range []string{"call_id", "name", "arguments"} {
		require.Containsf(t, item, k, "function_call item missing %q", k)
	}
	require.Equal(t, "", item["arguments"])
}

// TestWire_MessageItemContentAlwaysArray 确保 message item 始终带 content:[]。
func TestWire_MessageItemContentAlwaysArray(t *testing.T) {
	m := marshalEvent(t, ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item:        &ResponsesOutput{Type: "message", ID: "msg_1", Role: "assistant", Status: "in_progress"},
	})
	item, ok := m["item"].(map[string]any)
	require.True(t, ok, "item must be an object")
	require.Contains(t, item, "content")
	_, ok = item["content"].([]any)
	require.True(t, ok, "content must be an array")
}

// TestWire_ReasoningItemSummaryAlwaysArray 确保 reasoning item 始终带 summary:[]。
func TestWire_ReasoningItemSummaryAlwaysArray(t *testing.T) {
	m := marshalEvent(t, ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item:        &ResponsesOutput{Type: "reasoning", ID: "rs_1", Status: "in_progress"},
	})
	item, ok := m["item"].(map[string]any)
	require.True(t, ok, "item must be an object")
	require.Contains(t, item, "summary")
	_, ok = item["summary"].([]any)
	require.True(t, ok, "summary must be an array")
}

// TestWire_ContentPartCarriesAnnotationsLogprobs 确保 output_text part 字段完整。
func TestWire_ContentPartCarriesAnnotationsLogprobs(t *testing.T) {
	m := marshalEvent(t, ResponsesStreamEvent{
		Type: "response.content_part.added", OutputIndex: 0, ContentIndex: 0, ItemID: "msg_1",
		Part: &ResponsesContentPart{Type: "output_text", Text: ""},
	})
	part, ok := m["part"].(map[string]any)
	require.True(t, ok, "part must be an object")
	require.Equal(t, "output_text", part["type"])
	require.Contains(t, part, "text")
	require.Contains(t, part, "annotations")
	require.Contains(t, part, "logprobs")
}

// TestWire_ArgumentsDonePresentEvenEmpty 确保 done 事件即使空参数也带 arguments。
func TestWire_ArgumentsDonePresentEvenEmpty(t *testing.T) {
	m := marshalEvent(t, ResponsesStreamEvent{
		Type: "response.function_call_arguments.done", OutputIndex: 1, ItemID: "fc_1", CallID: "call_a", Name: "exec", Arguments: "",
	})
	require.Contains(t, m, "arguments")
	require.Equal(t, "", m["arguments"])
}

// TestWire_CustomToolCallInputIndexPresentAtZero 确保 custom 工具位于首个输出项时，
// delta/done 事件仍会序列化值为 0 的 output_index。
func TestWire_CustomToolCallInputIndexPresentAtZero(t *testing.T) {
	d := marshalEvent(t, ResponsesStreamEvent{
		Type: "response.custom_tool_call_input.delta", OutputIndex: 0, ItemID: "ct_1", Delta: "dir",
	})
	require.Contains(t, d, "output_index")
	require.EqualValues(t, 0, d["output_index"])
	require.Equal(t, "dir", d["delta"])

	done := marshalEvent(t, ResponsesStreamEvent{
		Type: "response.custom_tool_call_input.done", OutputIndex: 0, ItemID: "ct_1", CallID: "call_1", Name: "exec", Input: "dir",
	})
	require.Contains(t, done, "output_index")
	require.EqualValues(t, 0, done["output_index"])
	require.Equal(t, "dir", done["input"])
	require.NotContains(t, done, "delta")
}

// TestWire_UnknownEventFallsBackToDefault 确保非流式 item 事件继续保留默认序列化。
func TestWire_UnknownEventFallsBackToDefault(t *testing.T) {
	m := marshalEvent(t, ResponsesStreamEvent{
		Type:     "response.completed",
		Response: &ResponsesResponse{ID: "resp_1", Object: "response", Status: "completed"},
	})
	require.Contains(t, m, "response")
}

// TestResponsesOutputUnmarshal_ToolSearchObjectArguments 验证单个工具搜索条目可以解析
// 对象形态的 arguments，并在重新序列化后保持对象形态。
func TestResponsesOutputUnmarshal_ToolSearchObjectArguments(t *testing.T) {
	var item ResponsesOutput
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"tool_search_call",
		"id":"item_1",
		"call_id":"call_1",
		"execution":"client",
		"arguments":{"query":"gmail","limit":2}
	}`), &item))
	require.Equal(t, "tool_search_call", item.Type)
	require.Equal(t, `{"query":"gmail","limit":2}`, item.Arguments)

	wire, err := json.Marshal(item)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(wire, &decoded))
	args, ok := decoded["arguments"].(map[string]any)
	require.True(t, ok, "tool_search_call arguments must remain an object")
	require.Equal(t, "gmail", args["query"])
}

// TestResponsesResponseUnmarshal_ToolSearchObjectArguments 验证完整响应中的工具搜索参数。
func TestResponsesResponseUnmarshal_ToolSearchObjectArguments(t *testing.T) {
	var response ResponsesResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"response_1",
		"object":"response",
		"status":"completed",
		"output":[{
			"type":"tool_search_call",
			"id":"item_1",
			"call_id":"call_1",
			"arguments":{"query":"gmail"}
		}]
	}`), &response))
	require.Len(t, response.Output, 1)
	require.Equal(t, `{"query":"gmail"}`, response.Output[0].Arguments)
}

// TestResponsesStreamEventUnmarshal_ToolSearchObjectArguments 验证流式完成事件中的
// 工具搜索参数对象可以进入统一的内部字符串表示。
func TestResponsesStreamEventUnmarshal_ToolSearchObjectArguments(t *testing.T) {
	var event ResponsesStreamEvent
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"response.output_item.done",
		"item":{
			"type":"tool_search_call",
			"id":"item_1",
			"call_id":"call_1",
			"arguments":{"query":"gmail"}
		}
	}`), &event))
	require.NotNil(t, event.Item)
	require.Equal(t, `{"query":"gmail"}`, event.Item.Arguments)
}
