package bridge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int       { return &v }
func strPtr(s string) *string { return &s }

// TestStreamingParallelToolUseNoGhostDelta 复现 #4193：CC→Responses→Anthropic 桥接
// 收尾并行工具调用时，参数可能只打包在 function_call_arguments.done 中，
// 之前没有 delta。旧逻辑直接使用 state.ContentBlockIndex，而没有查询
// OutputIndexToBlockIdx；第一个工具 block 关闭且索引递增后，第二个工具的 .done
// 会向从未 content_block_start 的索引发送 content_block_delta，导致 Claude Code
// 报错 "Content block not found"。
//
// 本用例驱动完整收尾路径：两个并行 tool_call 的 CC chunk 依次经过
// ChatCompletionsChunkToResponsesEvents、FinalizeChatCompletionsResponsesStream 和
// ResponsesEventToAnthropicEvents，并断言每个 content_block_delta 都指向已启动的 block。
func TestStreamingParallelToolUseNoGhostDelta(t *testing.T) {
	ccState := NewChatCompletionsToResponsesStreamState(testRuntime(), "glm-5.2")
	anthropicState := NewResponsesEventToAnthropicState(testRuntime())
	anthropicState.Model = "glm-5.2"

	// 第一个 chunk 携带首个 tool_call 的 ID、名称和打包参数。
	chatChunk1 := &ChatCompletionsChunk{
		ID:    "chatcmpl-1",
		Model: "glm-5.2",
		Choices: []ChatChunkChoice{{
			Index: 0,
			Delta: ChatDelta{
				ToolCalls: []ChatToolCall{{
					Index: intPtr(0),
					ID:    "call_weather",
					Type:  "function",
					Function: ChatFunctionCall{
						Name:      "get_weather",
						Arguments: `{"city":"Tokyo"}`,
					},
				}},
			},
		}},
	}

	// 第二个 chunk 携带第二个 tool_call 的 ID、名称和打包参数。
	chatChunk2 := &ChatCompletionsChunk{
		ID:    "chatcmpl-1",
		Model: "glm-5.2",
		Choices: []ChatChunkChoice{{
			Index: 0,
			Delta: ChatDelta{
				ToolCalls: []ChatToolCall{{
					Index: intPtr(1),
					ID:    "call_time",
					Type:  "function",
					Function: ChatFunctionCall{
						Name:      "get_time",
						Arguments: `{}`,
					},
				}},
			},
		}},
	}

	// 第三个 chunk 结束工具调用。
	chatChunk3 := &ChatCompletionsChunk{
		ID:    "chatcmpl-1",
		Model: "glm-5.2",
		Choices: []ChatChunkChoice{{
			Index:        0,
			Delta:        ChatDelta{},
			FinishReason: strPtr("tool_calls"),
		}},
	}

	// 先将 chunk 送入 CC→Responses 桥接，再转换为 Anthropic 事件。
	var allAnthropicEvents []AnthropicStreamEvent
	for _, chunk := range []*ChatCompletionsChunk{chatChunk1, chatChunk2, chatChunk3} {
		responsesEvents := ChatCompletionsChunkToResponsesEvents(testRuntime(), chunk, ccState)
		for _, rEvent := range responsesEvents {
			allAnthropicEvents = append(allAnthropicEvents, ResponsesEventToAnthropicEvents(&rEvent, anthropicState)...)
		}
	}

	// 收尾时 closeChatToolItems 为每个工具发出 function_call_arguments.done。
	finalResponsesEvents := FinalizeChatCompletionsResponsesStream(testRuntime(), ccState)
	for _, rEvent := range finalResponsesEvents {
		allAnthropicEvents = append(allAnthropicEvents, ResponsesEventToAnthropicEvents(&rEvent, anthropicState)...)
	}

	// 收集已收到 content_block_start 的 block 索引。
	startedBlocks := make(map[int]string) // 索引到 block 类型的映射。
	for _, e := range allAnthropicEvents {
		if e.Type == "content_block_start" && e.ContentBlock != nil {
			startedBlocks[*e.Index] = e.ContentBlock.Type
		}
	}

	// 每个 content_block_delta 都必须指向已启动的 block，不得出现幽灵 delta。
	for _, e := range allAnthropicEvents {
		if e.Type != "content_block_delta" || e.Index == nil {
			continue
		}
		idx := *e.Index
		_, ok := startedBlocks[idx]
		require.Truef(t, ok,
			"content_block_delta on index %d which was never content_block_start'ed (ghost delta bug #4193)", idx)
	}

	// 每个 content_block_stop 也必须指向已启动的 block。
	for _, e := range allAnthropicEvents {
		if e.Type != "content_block_stop" || e.Index == nil {
			continue
		}
		idx := *e.Index
		_, ok := startedBlocks[idx]
		require.Truef(t, ok,
			"content_block_stop on index %d which was never content_block_start'ed", idx)
	}

	// 两个 tool_use block 都应已启动。
	var toolUseBlocks []int
	for idx, blockType := range startedBlocks {
		if blockType == "tool_use" {
			toolUseBlocks = append(toolUseBlocks, idx)
		}
	}
	assert.Len(t, toolUseBlocks, 2, "both parallel tool_use blocks should be opened")

	// 收尾原因应为 tool_use。
	var sawMessageDelta bool
	for _, e := range allAnthropicEvents {
		if e.Type == "message_delta" {
			sawMessageDelta = true
			assert.Equal(t, "tool_use", e.Delta.StopReason)
		}
	}
	assert.True(t, sawMessageDelta, "message_delta should be emitted")
}

// TestStreamingParallelToolUseSecondToolPackedArgsDone 聚焦验证精确故障形态：
// 第一个工具通过 delta 流式输出参数后关闭 block，第二个工具只在 .done 中
// 携带打包参数。修复前，第二个 .done 会使用已经递增的 state.ContentBlockIndex。
func TestStreamingParallelToolUseSecondToolPackedArgsDone(t *testing.T) {
	state := NewResponsesEventToAnthropicState(testRuntime())

	// 先建立 response.created 状态。
	ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type:     "response.created",
		Response: &ResponsesResponse{ID: "resp_par", Model: "glm-5.2"},
	}, state)

	// 工具 1 在索引 0 发出 output_item.added。
	ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item:        &ResponsesOutput{Type: "function_call", CallID: "call_a", Name: "tool_a"},
	}, state)

	// 工具 1 通过 delta 流式输出参数，因此 CurrentToolHadDelta 为 true。
	ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type:        "response.function_call_arguments.delta",
		OutputIndex: 0,
		Delta:       `{"x":1}`,
	}, state)

	// 工具 1 的 arguments.done 关闭当前 block，ContentBlockIndex 从 0 递增到 1。
	eventsTool1Done := ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type:        "response.function_call_arguments.done",
		OutputIndex: 0,
		Arguments:   `{"x":1}`,
	}, state)
	// 参数 delta 已输出，此处只应产生 content_block_stop。
	for _, e := range eventsTool1Done {
		assert.NotEqual(t, "content_block_delta", e.Type,
			"tool 1 .done should not re-emit delta (args already streamed)")
	}

	// 工具 2 在索引 1 发出 output_item.added，并打开 ContentBlockIndex=1 的 block。
	ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 1,
		Item:        &ResponsesOutput{Type: "function_call", CallID: "call_b", Name: "tool_b"},
	}, state)

	// 工具 2 没有 delta，参数只打包在 .done 中，这正是触发幽灵 delta 的场景。
	eventsTool2Done := ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type:        "response.function_call_arguments.done",
		OutputIndex: 1,
		Arguments:   `{"y":2}`,
	}, state)

	// 修复后 delta 必须指向工具 2 的 block 索引 1，而不是盲目使用 state.ContentBlockIndex。
	// 在两个工具的简单情况下两者可能碰巧相等，但三个以上工具或 block 已关闭时会出错。
	//
	// 关键断言是 delta 必须位于索引 1，且同一索引上紧随 content_block_stop。
	var sawDelta, sawStop bool
	var deltaIndex, stopIndex int
	for _, e := range eventsTool2Done {
		if e.Type == "content_block_delta" && e.Index != nil {
			sawDelta = true
			deltaIndex = *e.Index
			assert.Equal(t, "input_json_delta", e.Delta.Type)
			assert.Equal(t, `{"y":2}`, e.Delta.PartialJSON)
		}
		if e.Type == "content_block_stop" && e.Index != nil {
			sawStop = true
			stopIndex = *e.Index
		}
	}
	assert.True(t, sawDelta, "tool 2 .done with packed args should emit content_block_delta")
	assert.True(t, sawStop, "tool 2 .done should close the block")
	assert.Equal(t, 1, deltaIndex, "delta must target the tool 2 block (index 1)")
	assert.Equal(t, deltaIndex, stopIndex, "delta and stop must be on the same block")
}

// TestStreamingThreeParallelToolsAllPackedDone 验证最极端情况：三个并行工具
// 都没有 delta，参数全部打包在 .done 中。修复前，工具 2 和 3 会在错误索引上发出幽灵 delta。
func TestStreamingThreeParallelToolsAllPackedDone(t *testing.T) {
	state := NewResponsesEventToAnthropicState(testRuntime())

	ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type:     "response.created",
		Response: &ResponsesResponse{ID: "resp_3par", Model: "glm-5.2"},
	}, state)

	// 在索引 0、1、2 上打开三个工具 block。
	for i, name := range []string{"tool_a", "tool_b", "tool_c"} {
		ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: i,
			Item:        &ResponsesOutput{Type: "function_call", CallID: "call_" + name, Name: name},
		}, state)
	}

	// 记录已启动的 block。
	started := map[int]bool{0: true, 1: true, 2: true}

	// 三个 .done 都携带打包参数，之前均没有 delta。
	for i, args := range []string{`{"a":1}`, `{"b":2}`, `{"c":3}`} {
		events := ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
			Type:        "response.function_call_arguments.done",
			OutputIndex: i,
			Arguments:   args,
		}, state)

		for _, e := range events {
			if e.Type == "content_block_delta" && e.Index != nil {
				idx := *e.Index
				require.Truef(t, started[idx],
					"ghost delta: tool %d .done emitted content_block_delta on index %d (never started)", i, idx)
				require.Equal(t, i, idx,
					"tool %d .done delta should target its own block index %d, got %d", i, i, idx)
			}
			if e.Type == "content_block_stop" && e.Index != nil {
				idx := *e.Index
				require.Truef(t, started[idx],
					"ghost stop: tool %d .done emitted content_block_stop on index %d (never started)", i, idx)
			}
		}
	}
}
