package provider_test

import (
	"fmt"
	"strings"
	"testing"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
)

func TestApplyCodexOAuthTransform_ToolContinuationPreservesInput(t *testing.T) {
	// 续链场景：保留 item_reference 与 id，但不再强制 store=true。

	reqBody := map[string]any{
		"model": "gpt-5.2",
		"input": []any{
			map[string]any{"type": "item_reference", "id": "ref1", "text": "x"},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "ok", "id": "o1"},
		},
		"tool_choice": "auto",
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	// 未显式设置 store=true，默认为 false。
	store, ok := reqBody["store"].(bool)
	require.True(t, ok)
	require.False(t, store)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)

	// 校验 input[0] 为 map，避免断言失败导致测试中断。
	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "item_reference", first["type"])
	require.Equal(t, "ref1", first["id"])

	// 校验 input[1] 为 map，确保后续字段断言安全。
	second, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "o1", second["id"])
	require.Equal(t, "fc_1", second["call_id"])
}

func TestApplyCodexOAuthTransform_MessagesBridgePromptCacheKeyIsHeaderOnly(t *testing.T) {
	reqBody := map[string]any{
		"model":            "gpt-5.5",
		"prompt_cache_key": "anthropic-metadata-session-1",
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "developer",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": gatewayprovider.OpenAICompatClaudeCodeTodoGuardMarker,
					},
				},
			},
			map[string]any{
				"type":    "message",
				"role":    "user",
				"content": "hello",
			},
		},
	}

	result := gatewayprovider.ApplyCodexOAuthTransformWithOptions(reqBody, openai.CodexOAuthTransformOptions{
		SkipDefaultInstructions: true,
		PreserveToolCallIDs:     true,
	})

	require.Equal(t, "anthropic-metadata-session-1", result.PromptCacheKey)
	require.True(t, result.Modified)
	require.NotContains(t, reqBody, "prompt_cache_key")
}

func TestApplyCodexOAuthTransform_ToolContinuationPreservesNativeMessageAndReasoningIDs(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.2",
		"input": []any{
			map[string]any{"type": "message", "id": "msg_0", "role": "user", "content": "hi"},
			map[string]any{"type": "item_reference", "id": "rs_123"},
		},
		"tool_choice": "auto",
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)

	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "msg_0", first["id"])

	second, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "rs_123", second["id"])
}

func TestApplyCodexOAuthTransform_ToolContinuationNormalizesToolReferenceIDsOnly(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.2",
		"input": []any{
			map[string]any{"type": "item_reference", "id": "call_1"},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "ok"},
		},
		"tool_choice": "auto",
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)

	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fc_1", first["id"])

	second, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fc_1", second["call_id"])
}

func TestApplyCodexOAuthTransform_NormalizesIsolatedLegacyReferenceAcrossTurns(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.2",
		"input": []any{
			map[string]any{"type": "item_reference", "id": "call_previous_turn"},
			map[string]any{"type": "item_reference", "id": "fc_remote_item"},
			map[string]any{"type": "item_reference", "id": "vendor_remote_item"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 3)
	for i, expectedID := range []string{"fc_previous_turn", "fc_remote_item", "vendor_remote_item"} {
		item, itemOK := input[i].(map[string]any)
		require.True(t, itemOK)
		require.Equal(t, expectedID, item["id"])
	}
}

func TestApplyCodexOAuthTransform_BoundsLongCallIDsAndPreservesPairing(t *testing.T) {
	suffix := strings.Repeat("z", 62)
	for _, tc := range []struct {
		name         string
		callID       string
		outputCallID string
	}{
		{name: "non-native boundary id", callID: "call-" + strings.Repeat("x", 59), outputCallID: "call-" + strings.Repeat("x", 59)},
		{name: "overlong fc id", callID: "fc_" + strings.Repeat("y", 62), outputCallID: "fc_" + strings.Repeat("y", 62)},
		{name: "equivalent prefixes", callID: "call_" + suffix, outputCallID: "fc_" + suffix},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reqBody := map[string]any{
				"model": "gpt-5.2",
				"input": []any{
					map[string]any{"type": "function_call", "call_id": tc.callID, "name": "shell"},
					map[string]any{"type": "function_call_output", "call_id": tc.outputCallID, "output": "done"},
				},
			}

			gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

			input, ok := reqBody["input"].([]any)
			require.True(t, ok)
			call, ok := input[0].(map[string]any)
			require.True(t, ok)
			output, ok := input[1].(map[string]any)
			require.True(t, ok)

			fixedCallID, ok := call["call_id"].(string)
			require.True(t, ok)
			require.LessOrEqual(t, len(fixedCallID), openai.CodexCallIDMaxLength)
			require.True(t, strings.HasPrefix(fixedCallID, openai.CodexCallIDPrefix))
			require.Equal(t, fixedCallID, output["call_id"])
			require.Equal(t, fixedCallID, openai.NormalizeCodexCallID(tc.callID))
			require.Equal(t, fixedCallID, openai.NormalizeCodexCallID(tc.outputCallID))
		})
	}
}

func TestApplyCodexOAuthTransform_PreservesCallIDsWithinLimitWhenRequested(t *testing.T) {
	// preserve 模式下 <=64 字符的 id 必须原样透传（含 64 字符等长边界），
	// 不做任何前缀改写或压缩。
	for _, tc := range []struct {
		name   string
		callID string
	}{
		{name: "anthropic toolu id", callID: "toolu_01ABCdefGHIjklMNOpqrsTUV"},
		{name: "boundary 64 chars", callID: "toolu_" + strings.Repeat("x", openai.CodexCallIDMaxLength-len("toolu_"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.LessOrEqual(t, len(tc.callID), openai.CodexCallIDMaxLength)
			reqBody := map[string]any{
				"model": "gpt-5.2",
				"input": []any{
					map[string]any{"type": "function_call", "call_id": tc.callID, "name": "shell"},
					map[string]any{"type": "function_call_output", "call_id": tc.callID, "output": "done"},
				},
			}

			gatewayprovider.ApplyCodexOAuthTransformWithOptions(reqBody, openai.CodexOAuthTransformOptions{
				PreserveToolCallIDs: true,
			})

			input, ok := reqBody["input"].([]any)
			require.True(t, ok)
			call, ok := input[0].(map[string]any)
			require.True(t, ok)
			output, ok := input[1].(map[string]any)
			require.True(t, ok)
			require.Equal(t, tc.callID, call["call_id"])
			require.Equal(t, tc.callID, output["call_id"])
		})
	}
}

func TestApplyCodexOAuthTransform_CompactsOverlongCallIDsWhenPreserveRequested(t *testing.T) {
	// preserve 模式下超过 64 字符的 id 若原样透传，上游必然返回 400；
	// function_call 与 function_call_output 两侧使用相同的确定性压缩结果。
	callID := "srvtoolu_" + strings.Repeat("x", 69)
	require.Len(t, callID, 78)
	reqBody := map[string]any{
		"model": "gpt-5.2",
		"input": []any{
			map[string]any{"type": "function_call", "call_id": callID, "name": "shell"},
			map[string]any{"type": "function_call_output", "call_id": callID, "output": "done"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransformWithOptions(reqBody, openai.CodexOAuthTransformOptions{
		PreserveToolCallIDs: true,
	})

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	call, ok := input[0].(map[string]any)
	require.True(t, ok)
	output, ok := input[1].(map[string]any)
	require.True(t, ok)

	compacted, ok := call["call_id"].(string)
	require.True(t, ok)
	require.Len(t, compacted, openai.CodexCallIDMaxLength)
	require.True(t, strings.HasPrefix(compacted, openai.CodexCallIDPrefix))
	require.Equal(t, compacted, output["call_id"], "两侧压缩结果必须一致以保持配对")
	require.Equal(t, openai.NormalizeCodexCallID(callID), compacted, "压缩必须是确定性的")
}

func TestApplyCodexOAuthTransform_ToolSearchOutputPreservesCallID(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.2",
		"input": []any{
			map[string]any{"type": "tool_search_output", "call_id": "call_1", "output": "ok"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)

	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_search_output", first["type"])
	require.Equal(t, "tsc_1", first["call_id"])
}

func TestApplyCodexOAuthTransform_CustomAndMCPToolOutputsPreserveCallID(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.2",
		"input": []any{
			map[string]any{"type": "custom_tool_call_output", "call_id": "call_custom", "output": "ok"},
			map[string]any{"type": "mcp_tool_call_output", "call_id": "call_mcp", "output": "ok"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)

	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ctc_custom", first["call_id"])

	second, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fc_mcp", second["call_id"])
}

func TestApplyCodexOAuthTransform_NormalizesNativeToolCallPairsByType(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.6-sol",
		"input": []any{
			map[string]any{"type": "custom_tool_call", "id": "fc_custom", "call_id": "call_custom", "name": "apply_patch"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "fc_custom", "output": "done"},
			map[string]any{"type": "tool_search_call", "id": "fc_search", "call_id": "call_search"},
			map[string]any{"type": "tool_search_output", "call_id": "fc_search", "output": "result"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 4)
	custom, ok := input[0].(map[string]any)
	require.True(t, ok)
	customOutput, ok := input[1].(map[string]any)
	require.True(t, ok)
	search, ok := input[2].(map[string]any)
	require.True(t, ok)
	searchOutput, ok := input[3].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, custom, "id", "the invalid replay item id must be removed, not fabricated")
	require.Equal(t, "ctc_custom", custom["call_id"])
	require.Equal(t, custom["call_id"], customOutput["call_id"])
	require.NotContains(t, search, "id", "the invalid replay item id must be removed, not fabricated")
	require.Equal(t, "tsc_search", search["call_id"])
	require.Equal(t, search["call_id"], searchOutput["call_id"])
}

func TestApplyCodexOAuthTransform_PreservesNativeCallIDsWhenRequested(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.6-sol",
		"input": []any{
			map[string]any{"type": "custom_tool_call", "id": "ctc_custom", "call_id": "call_custom", "name": "apply_patch"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "call_custom", "output": "done"},
			map[string]any{"type": "tool_search_call", "id": "tsc_search", "call_id": "call_search"},
			map[string]any{"type": "tool_search_output", "call_id": "call_search", "output": "result"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransformWithOptions(reqBody, openai.CodexOAuthTransformOptions{PreserveToolCallIDs: true})

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 4)
	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	second, ok := input[1].(map[string]any)
	require.True(t, ok)
	third, ok := input[2].(map[string]any)
	require.True(t, ok)
	fourth, ok := input[3].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ctc_custom", first["id"])
	require.Equal(t, "call_custom", first["call_id"])
	require.Equal(t, "call_custom", second["call_id"])
	require.Equal(t, "tsc_search", third["id"])
	require.Equal(t, "call_search", third["call_id"])
	require.Equal(t, "call_search", fourth["call_id"])
}

func TestApplyCodexOAuthTransform_BoundsEquivalentNativeToolCallIDsWithPairing(t *testing.T) {
	suffix := strings.Repeat("x", 70)
	reqBody := map[string]any{
		"model": "gpt-5.6-sol",
		"input": []any{
			map[string]any{"type": "custom_tool_call", "call_id": "call_" + suffix, "name": "apply_patch"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "fc_" + suffix, "output": "done"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransformWithOptions(reqBody, openai.CodexOAuthTransformOptions{PreserveToolCallIDs: true})

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)
	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	second, ok := input[1].(map[string]any)
	require.True(t, ok)
	callID, ok := first["call_id"].(string)
	require.True(t, ok)
	outputCallID, ok := second["call_id"].(string)
	require.True(t, ok)
	require.LessOrEqual(t, len(callID), openai.CodexCallIDMaxLength)
	require.True(t, strings.HasPrefix(callID, "ctc_"))
	require.Equal(t, callID, outputCallID)
}

func TestApplyCodexOAuthTransform_ImageAndWebSearchCallsDoNotGainCallID(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.2",
		"input": []any{
			map[string]any{"type": "image_generation_call", "id": "ig_123", "status": "completed"},
			map[string]any{"type": "web_search_call", "call_id": "call_bad", "status": "completed"},
		},
		"tool_choice": "auto",
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)

	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ig_123", first["id"])
	_, hasCallID := first["call_id"]
	require.False(t, hasCallID)

	second, ok := input[1].(map[string]any)
	require.True(t, ok)
	_, hasCallID = second["call_id"]
	require.False(t, hasCallID)
}

func TestApplyCodexOAuthTransform_ConvertsToolRoleMessageToFunctionCallOutput(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": []any{
			map[string]any{
				"type":         "message",
				"role":         "tool",
				"tool_call_id": "call_1",
				"content":      "ok",
			},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)

	item, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call_output", item["type"])
	require.Equal(t, "fc_1", item["call_id"])
	require.Equal(t, "ok", item["output"])
	_, hasRole := item["role"]
	require.False(t, hasRole)
}

func TestApplyCodexOAuthTransform_StringifiesNonStringMessageContentText(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": []any{"a", "b"}},
				},
			},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	item, ok := input[0].(map[string]any)
	require.True(t, ok)
	content, ok := item["content"].([]any)
	require.True(t, ok)
	part, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, `["a","b"]`, part["text"])
}

func TestApplyCodexOAuthTransform_DowngradesUnknownToolChoice(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"tools": []any{
			map[string]any{"type": "function", "name": "shell"},
		},
		"tool_choice": map[string]any{"type": "custom"},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	require.Equal(t, "auto", reqBody["tool_choice"])
}

func TestApplyCodexOAuthTransform_PreservesKnownToolChoice(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"tools": []any{
			map[string]any{"type": "custom", "name": "shell"},
		},
		"tool_choice": map[string]any{"type": "custom"},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	choice, ok := reqBody["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom", choice["type"])
}

func TestApplyCodexOAuthTransform_NormalizesLegacyFunctionToolChoice(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"tools": []any{
			map[string]any{"type": "function", "name": "shell"},
		},
		"tool_choice": map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "shell"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	choice, ok := reqBody["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", choice["type"])
	require.Equal(t, "shell", choice["name"])
	require.NotContains(t, choice, "function")
}

func TestApplyCodexOAuthTransform_DowngradesMissingFunctionToolChoice(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"tools": []any{
			map[string]any{"type": "function", "name": "shell"},
		},
		"tool_choice": map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "missing"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	require.Equal(t, "auto", reqBody["tool_choice"])
}

func TestApplyCodexOAuthTransform_AddsFallbackNameForFunctionCallInput(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "run tool"},
			map[string]any{"type": "function_call", "call_id": "call_1", "arguments": "{}"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)
	item, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", item["type"])
	require.Equal(t, "tool", item["name"])
	require.Equal(t, "fc_1", item["call_id"])
}

func TestApplyCodexOAuthTransform_PreservesFunctionCallInputName(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": []any{
			map[string]any{"type": "custom_tool_call", "call_id": "call_1", "name": "shell", "input": "pwd"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)
	item, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "shell", item["name"])
	require.Equal(t, "ctc_1", item["call_id"])
}

func TestApplyCodexOAuthTransform_PreservesMCPToolCallIDAndName(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": []any{
			map[string]any{
				"type":      "mcp_tool_call",
				"call_id":   "call_abc",
				"name":      "remote_tool",
				"arguments": "{}",
			},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)
	item, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_tool_call", item["type"])
	require.Equal(t, "remote_tool", item["name"])
	require.Equal(t, "fc_abc", item["call_id"])
}

func TestCodexInputItemRequiresNameTypesAllowCallID(t *testing.T) {
	for _, typ := range []string{"function_call", "custom_tool_call", "mcp_tool_call"} {
		require.True(t, openai.CodexInputItemRequiresName(typ), typ)
		require.True(t, openai.IsCodexToolCallItemType(typ), typ)
	}
}

func TestApplyCodexOAuthTransform_ExplicitStoreFalsePreserved(t *testing.T) {
	// 续链场景：显式 store=false 不再强制为 true，保持 false。

	reqBody := map[string]any{
		"model": "gpt-5.1",
		"store": false,
		"input": []any{
			map[string]any{"type": "function_call_output", "call_id": "call_1"},
		},
		"tool_choice": "auto",
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	store, ok := reqBody["store"].(bool)
	require.True(t, ok)
	require.False(t, store)
}

func TestApplyCodexOAuthTransform_ExplicitStoreTrueForcedFalse(t *testing.T) {
	// 显式 store=true 也会强制为 false。

	reqBody := map[string]any{
		"model": "gpt-5.1",
		"store": true,
		"input": []any{
			map[string]any{"type": "function_call_output", "call_id": "call_1"},
		},
		"tool_choice": "auto",
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	store, ok := reqBody["store"].(bool)
	require.True(t, ok)
	require.False(t, store)
}

func TestApplyCodexOAuthTransform_CompactForcesNonStreaming(t *testing.T) {
	reqBody := map[string]any{
		"model":  "gpt-5.1-codex",
		"store":  true,
		"stream": true,
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, true)

	_, hasStore := reqBody["store"]
	require.False(t, hasStore)
	_, hasStream := reqBody["stream"]
	require.False(t, hasStream)
	require.True(t, result.Modified)
}

func TestApplyCodexOAuthTransform_NonContinuationDefaultsStoreFalseAndStripsIDs(t *testing.T) {
	// 非续链场景：未设置 store 时默认 false，并移除 input 中的 id。

	reqBody := map[string]any{
		"model": "gpt-5.1",
		"input": []any{
			map[string]any{"type": "text", "id": "t1", "text": "hi"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	store, ok := reqBody["store"].(bool)
	require.True(t, ok)
	require.False(t, store)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)
	// 校验 input[0] 为 map，避免类型不匹配触发 errcheck。
	item, ok := input[0].(map[string]any)
	require.True(t, ok)
	_, hasID := item["id"]
	require.False(t, hasID)
}

func TestApplyCodexOAuthTransform_PreservesReasoningInput(t *testing.T) {
	// HTTP 非透传路径需要保留 reasoning item，避免多轮任务丢失模型交错思考上下文。
	reqBody := map[string]any{
		"model": "gpt-5.1",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "hi"},
			map[string]any{
				"type":    "reasoning",
				"id":      "rs_0672f12450da0b9c0169f07220a6c08198b68c2455ced99344",
				"summary": []any{},
			},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)
	reasoning, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "reasoning", reasoning["type"])
	require.Equal(t, []any{}, reasoning["summary"])
	require.NotContains(t, reasoning, "id")
}

func TestFilterCodexInput_RemovesItemReferenceWhenNotPreserved(t *testing.T) {
	input := []any{
		map[string]any{"type": "item_reference", "id": "ref1"},
		map[string]any{"type": "text", "id": "t1", "text": "hi"},
	}

	filtered := openai.FilterCodexInput(input, false)
	require.Len(t, filtered, 1)
	// 校验 filtered[0] 为 map，确保字段检查可靠。
	item, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", item["type"])
	_, hasID := item["id"]
	require.False(t, hasID)
}

func TestApplyCodexOAuthTransform_NormalizeCodexTools_PreservesResponsesFunctionTools(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.1",
		"tools": []any{
			map[string]any{
				"type":        "function",
				"name":        "bash",
				"description": "desc",
				"parameters":  map[string]any{"type": "object"},
			},
			map[string]any{
				"type":     "function",
				"function": nil,
			},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	tools, ok := reqBody["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)

	first, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", first["type"])
	require.Equal(t, "bash", first["name"])
}

func TestNormalizeOpenAIResponsesImageGenerationTools_RewritesLegacyFields(t *testing.T) {
	reqBody := map[string]any{
		"tools": []any{
			map[string]any{
				"type":        "image_generation",
				"format":      "png",
				"compression": 60,
			},
		},
	}

	modified := openai.NormalizeOpenAIResponsesImageGenerationTools(reqBody)
	require.True(t, modified)

	tools, ok := reqBody["tools"].([]any)
	require.True(t, ok)
	first, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "png", first["output_format"])
	require.Equal(t, 60, first["output_compression"])
	_, hasFormat := first["format"]
	require.False(t, hasFormat)
	_, hasCompression := first["compression"]
	require.False(t, hasCompression)
}

func TestEnsureOpenAIResponsesImageGenerationTool_NoTools(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": "draw a cat",
	}

	modified := gatewayprovider.EnsureOpenAIResponsesImageGenerationTool(reqBody)
	require.True(t, modified)

	tools, ok := reqBody["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image_generation", tool["type"])
	require.Equal(t, "png", tool["output_format"])
}

func TestEnsureOpenAIResponsesImageGenerationTool_SkipsSpark(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.3-codex-spark",
		"input": "draw a cat",
	}

	modified := gatewayprovider.EnsureOpenAIResponsesImageGenerationTool(reqBody)
	require.False(t, modified)
	require.NotContains(t, reqBody, "tools")
}

func TestEnsureOpenAIResponsesImageGenerationTool_AppendsToExistingTools(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"tools": []any{
			map[string]any{"type": "web_search"},
		},
	}

	modified := gatewayprovider.EnsureOpenAIResponsesImageGenerationTool(reqBody)
	require.True(t, modified)

	tools, ok := reqBody["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)
	first, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "web_search", first["type"])
	second, ok := tools[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image_generation", second["type"])
	require.Equal(t, "png", second["output_format"])
}

func TestEnsureOpenAIResponsesImageGenerationTool_PreservesExistingImageTool(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"tools": []any{
			map[string]any{"type": "image_generation", "output_format": "webp"},
			map[string]any{"type": "web_search"},
		},
	}

	modified := gatewayprovider.EnsureOpenAIResponsesImageGenerationTool(reqBody)
	require.False(t, modified)

	tools, ok := reqBody["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "webp", tool["output_format"])
}

func TestEnsureOpenAIResponsesImageGenerationTool_PreservesImageGenNamespace(t *testing.T) {
	tests := []struct {
		name    string
		reqBody map[string]any
	}{
		{
			name: "top-level tools",
			reqBody: map[string]any{
				"model": "gpt-5.5",
				"tools": []any{
					map[string]any{
						"type": "namespace",
						"name": "image_gen",
						"tools": []any{
							map[string]any{"type": "function", "name": "imagegen"},
						},
					},
				},
			},
		},
		{
			name: "responses lite additional_tools",
			reqBody: map[string]any{
				"model": "gpt-5.5",
				"input": []any{
					map[string]any{
						"type": "additional_tools",
						"tools": []any{
							map[string]any{
								"type": "namespace",
								"name": "image_gen",
								"tools": []any{
									map[string]any{"type": "function", "name": "imagegen"},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.True(t, openai.HasOpenAIImageGenerationTool(tt.reqBody))

			modified := gatewayprovider.EnsureOpenAIResponsesImageGenerationTool(tt.reqBody)

			require.False(t, modified)
			tools, _ := tt.reqBody["tools"].([]any)
			for _, rawTool := range tools {
				tool, ok := rawTool.(map[string]any)
				require.True(t, ok)
				require.NotEqual(t, "image_generation", openai.FirstNonEmptyString(tool["type"]))
			}
		})
	}
}

func TestCodexImageGenerationBridge_PreservesClientImageFunctionTools(t *testing.T) {
	tests := []struct {
		name       string
		reqBody    map[string]any
		wantClient bool
	}{
		{
			name: "flat image_gen function",
			reqBody: map[string]any{
				"model": "gpt-5.5",
				"input": "draw a cat",
				"tools": []any{
					map[string]any{"type": "function", "name": "image_gen.imagegen"},
				},
			},
			wantClient: true,
		},
		{
			name: "nested image_gen function",
			reqBody: map[string]any{
				"model": "gpt-5.5",
				"input": "draw a cat",
				"tools": []any{
					map[string]any{
						"type": "function",
						"function": map[string]any{
							"name": "image_gen.imagegen",
						},
					},
				},
			},
			wantClient: true,
		},
		{
			name: "similar function name still receives hosted bridge",
			reqBody: map[string]any{
				"model": "gpt-5.5",
				"input": "draw a cat",
				"tools": []any{
					map[string]any{"type": "function", "name": "image_gen.imagegenerator"},
				},
			},
			wantClient: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.reqBody["instructions"] = "existing instructions"
			require.Equal(t, tt.wantClient, openai.HasCodexImageGenerationFunctionTool(tt.reqBody))

			toolModified := gatewayprovider.EnsureOpenAIResponsesImageGenerationTool(tt.reqBody)
			choiceModified := gatewayprovider.EnsureOpenAIResponsesImageGenerationToolChoiceAuto(tt.reqBody)
			instructionsModified := gatewayprovider.ApplyCodexImageGenerationBridgeInstructions(tt.reqBody)

			require.Equal(t, !tt.wantClient, toolModified)
			require.Equal(t, !tt.wantClient, choiceModified)
			require.Equal(t, !tt.wantClient, instructionsModified)

			hasHostedTool := false
			tools, _ := tt.reqBody["tools"].([]any)
			for _, rawTool := range tools {
				tool, ok := rawTool.(map[string]any)
				if ok && openai.FirstNonEmptyString(tool["type"]) == "image_generation" {
					hasHostedTool = true
				}
			}
			require.Equal(t, !tt.wantClient, hasHostedTool)

			if tt.wantClient {
				require.NotContains(t, tt.reqBody, "tool_choice")
				require.Equal(t, "existing instructions", tt.reqBody["instructions"])
			} else {
				require.Equal(t, "auto", tt.reqBody["tool_choice"])
				require.Contains(t, tt.reqBody["instructions"], openai.CodexImageGenerationBridgeMarker)
			}
		})
	}
}

func TestApplyCodexImageGenerationBridgeInstructions_AppendsBridgeOnce(t *testing.T) {
	reqBody := map[string]any{
		"model":        "gpt-5.4",
		"instructions": "existing instructions",
		"tools": []any{
			map[string]any{"type": "image_generation", "output_format": "png"},
		},
	}

	modified := gatewayprovider.ApplyCodexImageGenerationBridgeInstructions(reqBody)
	require.True(t, modified)

	instructions, ok := reqBody["instructions"].(string)
	require.True(t, ok)
	require.Contains(t, instructions, "existing instructions")
	require.Contains(t, instructions, openai.CodexImageGenerationBridgeMarker)
	require.Contains(t, instructions, "Responses native `image_generation` tool")

	modified = gatewayprovider.ApplyCodexImageGenerationBridgeInstructions(reqBody)
	require.False(t, modified)
}

func TestApplyCodexImageGenerationBridgeInstructions_SkipsSpark(t *testing.T) {
	reqBody := map[string]any{
		"model":        "gpt-5.3-codex-spark",
		"instructions": "existing instructions",
		"tools": []any{
			map[string]any{"type": "image_generation", "output_format": "png"},
		},
	}

	modified := gatewayprovider.ApplyCodexImageGenerationBridgeInstructions(reqBody)
	require.False(t, modified)
	require.Equal(t, "existing instructions", reqBody["instructions"])
}

func TestApplyCodexImageGenerationBridgeInstructions_SkipsWithoutImageTool(t *testing.T) {
	reqBody := map[string]any{
		"instructions": "existing instructions",
		"tools": []any{
			map[string]any{"type": "web_search"},
		},
	}

	modified := gatewayprovider.ApplyCodexImageGenerationBridgeInstructions(reqBody)
	require.False(t, modified)
	require.Equal(t, "existing instructions", reqBody["instructions"])
}

func TestValidateCodexSparkInputRejectsInputImage(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.3-codex-spark",
		"input": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "describe"},
					map[string]any{"type": "input_image", "image_url": "data:image/png;base64,aGVsbG8="},
				},
			},
		},
	}

	err := gatewayprovider.ValidateCodexSparkInput(reqBody, "gpt-5.3-codex-spark")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not support image input")
}

func TestValidateCodexSparkInputRejectsChatImageURL(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.3-codex-spark",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "describe"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,aGVsbG8="}},
				},
			},
		},
	}

	err := gatewayprovider.ValidateCodexSparkInput(reqBody, "gpt-5.3-codex-spark")
	require.Error(t, err)
}

func TestValidateCodexSparkInputAllowsTextOnly(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.3-codex-spark",
		"input": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "hello"},
				},
			},
		},
	}

	require.NoError(t, gatewayprovider.ValidateCodexSparkInput(reqBody, "gpt-5.3-codex-spark"))
}

func TestApplyCodexOAuthTransform_AddsSparkImageUnsupportedInstructions(t *testing.T) {
	reqBody := map[string]any{
		"model":        "gpt-5.3-codex-spark",
		"instructions": "existing instructions",
		"input":        "hello",
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)
	require.True(t, result.Modified)

	instructions, ok := reqBody["instructions"].(string)
	require.True(t, ok)
	require.Contains(t, instructions, "existing instructions")
	require.Contains(t, instructions, openai.CodexSparkImageUnsupportedMarker)
	require.Contains(t, instructions, "does not support image generation")
	require.Contains(t, instructions, "switch to a non-Spark Codex model")
	require.NotContains(t, instructions, openai.CodexImageGenerationBridgeMarker)
}

func TestApplyCodexOAuthTransform_DoesNotAddSparkImageUnsupportedForNonSpark(t *testing.T) {
	reqBody := map[string]any{
		"model":        "gpt-5.4",
		"instructions": "existing instructions",
		"input":        "hello",
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)
	instructions, ok := reqBody["instructions"].(string)
	require.True(t, ok)
	require.NotContains(t, instructions, openai.CodexSparkImageUnsupportedMarker)
}

// gpt-5.3-codex-spark 上游会拒绝 image_generation 工具，OAuth 转换需要剥离该工具并保留其它工具。
func TestApplyCodexOAuthTransform_StripsImageGenerationToolForSpark(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.3-codex-spark",
		"input": "hello",
		"tools": []any{
			map[string]any{"type": "function", "name": "shell"},
			map[string]any{"type": "image_generation", "output_format": "png"},
		},
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)
	require.True(t, result.Modified)
	require.False(t, openai.HasOpenAIImageGenerationTool(reqBody))

	tools, ok := reqBody["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	first, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", first["type"])
	require.Equal(t, "shell", first["name"])
}

// Spark 推理强度别名会归一化为 gpt-5.3-codex-spark，也需要剥离 image_generation。
func TestApplyCodexOAuthTransform_StripsImageGenerationToolForSparkAlias(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.3-codex-spark-high",
		"input": "hello",
		"tools": []any{
			map[string]any{"type": "image_generation", "output_format": "png"},
		},
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)
	require.True(t, result.Modified)
	require.False(t, openai.HasOpenAIImageGenerationTool(reqBody))
	// 剥离唯一工具后 tools 会被清空并删除字段。
	_, hasTools := reqBody["tools"]
	require.False(t, hasTools)
}

func TestStripOpenAIImageGenerationTools_StripsNamespaceFormats(t *testing.T) {
	imageNamespace := func() map[string]any {
		return map[string]any{
			"type": "namespace",
			"name": "image_gen",
			"tools": []any{
				map[string]any{"type": "function", "name": "imagegen"},
			},
		}
	}
	codeNamespace := func() map[string]any {
		return map[string]any{
			"type": "namespace",
			"name": "code_tools",
			"tools": []any{
				map[string]any{"type": "function", "name": "run"},
			},
		}
	}

	reqBody := map[string]any{
		"model": "gpt-5.5",
		"tools": []any{
			map[string]any{"type": "function", "name": "shell"},
			imageNamespace(),
			codeNamespace(),
		},
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "hello"},
			map[string]any{
				"type":  "additional_tools",
				"tools": []any{imageNamespace(), codeNamespace()},
			},
			map[string]any{
				"type":  "additional_tools",
				"tools": []any{imageNamespace()},
			},
		},
		"tool_choice": map[string]any{"type": "namespace", "name": "image_gen"},
	}

	require.True(t, openai.StripOpenAIImageGenerationTools(reqBody))
	require.False(t, openai.HasOpenAIImageGenerationTool(reqBody))
	require.NotContains(t, reqBody, "tool_choice")

	tools, ok := reqBody["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)
	firstTool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	secondTool, ok := tools[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "shell", firstTool["name"])
	require.Equal(t, "code_tools", secondTool["name"])

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)
	message, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "message", message["type"])
	additionalToolsItem, ok := input[1].(map[string]any)
	require.True(t, ok)
	additionalTools, ok := additionalToolsItem["tools"].([]any)
	require.True(t, ok)
	require.Len(t, additionalTools, 1)
	additionalTool, ok := additionalTools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "code_tools", additionalTool["name"])
	require.False(t, openai.StripOpenAIImageGenerationTools(reqBody), "stripping should be idempotent")
}

func TestStripOpenAIImageGenerationTools_KeepsNonImageNamespaces(t *testing.T) {
	reqBody := map[string]any{
		"tools": []any{
			map[string]any{"type": "namespace", "name": "code_tools"},
		},
		"input": []any{
			map[string]any{
				"type": "additional_tools",
				"tools": []any{
					map[string]any{"type": "namespace", "name": "browser_tools"},
				},
			},
		},
		"tool_choice": "auto",
	}

	require.False(t, openai.StripOpenAIImageGenerationTools(reqBody))
	require.Equal(t, "auto", reqBody["tool_choice"])
	require.False(t, openai.HasOpenAIImageGenerationTool(reqBody))
}

func TestStripOpenAIImageGenerationTools_KeepsCustomImagegenFunctionChoice(t *testing.T) {
	reqBody := map[string]any{
		"tool_choice": map[string]any{
			"function": map[string]any{"name": "imagegen"},
		},
	}

	require.False(t, openai.StripOpenAIImageGenerationTools(reqBody))
	require.Contains(t, reqBody, "tool_choice")
}

// 非 Spark Codex 模型支持 image_generation，工具应保持不变。
func TestApplyCodexOAuthTransform_KeepsImageGenerationToolForNonSpark(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.3-codex",
		"input": "hello",
		"tools": []any{
			map[string]any{"type": "image_generation", "output_format": "png"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)
	require.True(t, openai.HasOpenAIImageGenerationTool(reqBody))
}

func TestNormalizeOpenAIResponsesImageOnlyModel_BuildsImageToolRequest(t *testing.T) {
	reqBody := map[string]any{
		"model":         "gpt-image-2",
		"prompt":        "draw a cat",
		"size":          "1024x1024",
		"output_format": "png",
	}

	modified := gatewayprovider.NormalizeOpenAIResponsesImageOnlyModel(reqBody)
	require.True(t, modified)
	require.Equal(t, openai.ImagesResponsesMainModel, reqBody["model"])
	require.Equal(t, "draw a cat", reqBody["input"])
	_, hasPrompt := reqBody["prompt"]
	require.False(t, hasPrompt)
	_, hasTopLevelSize := reqBody["size"]
	require.False(t, hasTopLevelSize)

	tools, ok := reqBody["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image_generation", tool["type"])
	require.Equal(t, "gpt-image-2", tool["model"])
	require.Equal(t, "1024x1024", tool["size"])
	require.Equal(t, "png", tool["output_format"])

	choice, ok := reqBody["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image_generation", choice["type"])
}

func TestNormalizeOpenAIResponsesImageOnlyModel_PreservesExistingImageTool(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-image-2",
		"input": "draw a cat",
		"tools": []any{
			map[string]any{
				"type":  "image_generation",
				"model": "gpt-image-1.5",
			},
		},
		"tool_choice": "auto",
	}

	modified := gatewayprovider.NormalizeOpenAIResponsesImageOnlyModel(reqBody)
	require.True(t, modified)
	require.Equal(t, openai.ImagesResponsesMainModel, reqBody["model"])
	require.Equal(t, "auto", reqBody["tool_choice"])

	tools, ok := reqBody["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "gpt-image-1.5", tool["model"])
}

func TestValidateOpenAIResponsesImageModel_RejectsImageOnlyModel(t *testing.T) {
	err := gatewayprovider.ValidateOpenAIResponsesImageModel(map[string]any{
		"tools": []any{
			map[string]any{"type": "image_generation"},
		},
	}, "gpt-image-2")

	require.ErrorContains(t, err, `/v1/responses image_generation requests require a Responses-capable text model`)
}

func TestApplyCodexOAuthTransform_EmptyInput(t *testing.T) {
	// 空 input 应保持为空且不触发异常。

	reqBody := map[string]any{
		"model": "gpt-5.1",
		"input": []any{},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 0)
}

func TestNormalizeCodexModel_Gpt53(t *testing.T) {
	cases := map[string]string{
		"gpt-5.4":                   "gpt-5.4",
		"gpt5.5":                    "gpt-5.5",
		"openai/gpt5.5":             "gpt-5.5",
		"gpt-5.5-pro":               "gpt-5.5-pro",
		"gpt5.5-pro":                "gpt-5.5-pro",
		"openai/gpt5.5-pro":         "gpt-5.5-pro",
		"gpt-5.5-pro-high":          "gpt-5.5-pro",
		"gpt-5.5-max":               "gpt-5.5-max",
		"gpt5.6sol":                 "gpt-5.6-sol",
		"gpt-5.6-sol-max":           "gpt-5.6-sol",
		"gpt-5.6-terra-ultra":       "gpt-5.6-terra-ultra",
		"openai/gpt5.6luna-high":    "gpt-5.6-luna",
		"gpt-5.6-luna-max":          "gpt-5.6-luna",
		"gpt-5.6-luna-ultra":        "gpt-5.6-luna-ultra",
		"codex-auto-review":         "codex-auto-review",
		"gpt5.4":                    "gpt-5.4",
		"gpt-5.4-high":              "gpt-5.4",
		"gpt-5.4-chat-latest":       "gpt-5.4",
		"gpt 5.4":                   "gpt-5.4",
		"gpt-5.4-mini":              "gpt-5.4-mini",
		"gpt5.4-mini":               "gpt-5.4-mini",
		"gpt5.4mini":                "gpt-5.4-mini",
		"gpt 5.4 mini":              "gpt-5.4-mini",
		"gpt-5.3":                   "gpt-5.3-codex",
		"gpt5.3":                    "gpt-5.3-codex",
		"gpt-5.3-codex":             "gpt-5.3-codex",
		"gpt5.3-codex":              "gpt-5.3-codex",
		"gpt5.3codex":               "gpt-5.3-codex",
		"gpt-5.3-codex-xhigh":       "gpt-5.3-codex",
		"gpt-5.3-codex-spark":       "gpt-5.3-codex-spark",
		"gpt5.3-codex-spark":        "gpt-5.3-codex-spark",
		"gpt5.3codexspark":          "gpt-5.3-codex-spark",
		"gpt 5.3 codex spark":       "gpt-5.3-codex-spark",
		"gpt-5.3-codex-spark-high":  "gpt-5.3-codex-spark",
		"gpt-5.3-codex-spark-xhigh": "gpt-5.3-codex-spark",
		"gpt 5.3 codex":             "gpt-5.3-codex",
	}

	for input, expected := range cases {
		require.Equal(t, expected, gatewayprovider.NormalizeCodexModel(input))
	}
}

func TestNormalizeCodexModel_RemovedModelsFallbackToSupportedTargets(t *testing.T) {
	cases := map[string]string{
		"":                   "gpt-5.4",
		"gpt-5":              "gpt-5.4",
		"gpt-5-mini":         "gpt-5.4",
		"gpt-5-nano":         "gpt-5.4",
		"gpt-5.1":            "gpt-5.4",
		"gpt-5.1-codex":      "gpt-5.3-codex",
		"gpt-5.1-codex-max":  "gpt-5.3-codex",
		"gpt-5.1-codex-mini": "gpt-5.3-codex",
		"gpt-5.2-codex":      "gpt-5.2",
		"codex-mini-latest":  "gpt-5.3-codex",
		"gpt-5-codex":        "gpt-5.3-codex",
	}

	for input, expected := range cases {
		require.Equal(t, expected, gatewayprovider.NormalizeCodexModel(input))
	}
}

func TestApplyCodexOAuthTransform_PreservesBareSparkModel(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.3-codex-spark",
		"input": []any{},
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	require.Equal(t, "gpt-5.3-codex-spark", reqBody["model"])
	require.Equal(t, "gpt-5.3-codex-spark", result.NormalizedModel)
	store, ok := reqBody["store"].(bool)
	require.True(t, ok)
	require.False(t, store)
}

func TestApplyCodexOAuthTransform_TrimmedModelWithoutPolicyRewrite(t *testing.T) {
	reqBody := map[string]any{
		"model": "  gpt-5.3-codex-spark  ",
		"input": []any{},
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	require.Equal(t, "gpt-5.3-codex-spark", reqBody["model"])
	require.Equal(t, "gpt-5.3-codex-spark", result.NormalizedModel)
	require.True(t, result.Modified)
}

func TestApplyCodexOAuthTransform_CodexCLI_PreservesExistingInstructions(t *testing.T) {
	// Codex CLI 场景：已有 instructions 时不修改

	reqBody := map[string]any{
		"model":        "gpt-5.1",
		"instructions": "existing instructions",
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false) // isCodexCLI=true

	instructions, ok := reqBody["instructions"].(string)
	require.True(t, ok)
	require.Equal(t, "existing instructions", instructions)
	// Modified 仍可能为 true（因为其他字段被修改），但 instructions 应保持不变
	_ = result
}

func TestApplyCodexOAuthTransform_CodexCLI_SuppliesDefaultWhenEmpty(t *testing.T) {
	// Codex CLI 场景：无 instructions 时补充默认值

	reqBody := map[string]any{
		"model": "gpt-5.1",
		// 没有 instructions 字段
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false) // isCodexCLI=true

	instructions, ok := reqBody["instructions"].(string)
	require.True(t, ok)
	require.NotEmpty(t, instructions)
	require.True(t, result.Modified)
}

func TestApplyCodexOAuthTransform_GPT55SuppliesModelSpecificInstructions(t *testing.T) {
	reqBody := map[string]any{
		"model":        "gpt-5.5",
		"instructions": "   ",
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	instructions, ok := reqBody["instructions"].(string)
	require.True(t, ok)
	require.Contains(t, instructions, "You are Codex, a coding agent based on GPT-5")
	require.NotContains(t, instructions, "You are GPT-5.1 running in the Codex CLI")
	require.True(t, result.Modified)
}

func TestApplyCodexOAuthTransform_NonCodexCLI_PreservesExistingInstructions(t *testing.T) {
	// 非 Codex CLI 场景：已有 instructions 时保留客户端的值，不再覆盖

	reqBody := map[string]any{
		"model":        "gpt-5.1",
		"instructions": "old instructions",
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false) // isCodexCLI=false

	instructions, ok := reqBody["instructions"].(string)
	require.True(t, ok)
	require.Equal(t, "old instructions", instructions)
}

func TestApplyCodexOAuthTransform_StringInputConvertedToArray(t *testing.T) {
	reqBody := map[string]any{"model": "gpt-5.4", "input": "Hello, world!"}
	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)
	require.True(t, result.Modified)
	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)
	msg, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "message", msg["type"])
	require.Equal(t, "user", msg["role"])
	require.Equal(t, "Hello, world!", msg["content"])
}

func TestApplyCodexOAuthTransform_EmptyStringInputBecomesEmptyArray(t *testing.T) {
	reqBody := map[string]any{"model": "gpt-5.4", "input": ""}
	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)
	require.True(t, result.Modified)
	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 0)
}

func TestApplyCodexOAuthTransform_WhitespaceStringInputBecomesEmptyArray(t *testing.T) {
	reqBody := map[string]any{"model": "gpt-5.4", "input": "   "}
	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)
	require.True(t, result.Modified)
	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 0)
}

func TestApplyCodexOAuthTransform_StringInputWithToolsField(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": "Run the tests",
		"tools": []any{map[string]any{"type": "function", "name": "bash"}},
	}
	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)
	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)
}

func TestExtractSystemMessagesFromInput(t *testing.T) {
	t.Run("no system messages", func(t *testing.T) {
		reqBody := map[string]any{
			"input": []any{
				map[string]any{"role": "user", "content": "hello"},
			},
		}
		result := openai.ExtractSystemMessagesFromInput(reqBody, false)
		require.False(t, result)
		input, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Len(t, input, 1)
		_, hasInstructions := reqBody["instructions"]
		require.False(t, hasInstructions)
	})

	t.Run("string content system message", func(t *testing.T) {
		reqBody := map[string]any{
			"input": []any{
				map[string]any{"role": "system", "content": "You are an assistant."},
				map[string]any{"role": "user", "content": "hello"},
			},
		}
		result := openai.ExtractSystemMessagesFromInput(reqBody, false)
		require.True(t, result)
		input, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Len(t, input, 2)
		msg, ok := input[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "developer", msg["role"])
		require.Equal(t, "You are an assistant.", msg["content"])
		user, ok := input[1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "user", user["role"])
		require.Equal(t, "hello", user["content"])
		require.Equal(t, "You are an assistant.", reqBody["instructions"])
	})

	t.Run("array content system message", func(t *testing.T) {
		reqBody := map[string]any{
			"input": []any{
				map[string]any{
					"role": "system",
					"content": []any{
						map[string]any{"type": "text", "text": "Be helpful."},
					},
				},
			},
		}
		result := openai.ExtractSystemMessagesFromInput(reqBody, false)
		require.True(t, result)
		require.Equal(t, "Be helpful.", reqBody["instructions"])
		input, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Len(t, input, 1)
		msg, ok := input[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "developer", msg["role"])
	})

	t.Run("multiple system messages concatenated", func(t *testing.T) {
		reqBody := map[string]any{
			"input": []any{
				map[string]any{"role": "system", "content": "First."},
				map[string]any{"role": "system", "content": "Second."},
				map[string]any{"role": "user", "content": "hi"},
			},
		}
		result := openai.ExtractSystemMessagesFromInput(reqBody, false)
		require.True(t, result)
		require.Equal(t, "First.\n\nSecond.", reqBody["instructions"])
		input, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Len(t, input, 3)
		first, ok := input[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "developer", first["role"])
		second, ok := input[1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "developer", second["role"])
		user, ok := input[2].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "user", user["role"])
	})

	t.Run("mixed system and non-system preserves non-system", func(t *testing.T) {
		reqBody := map[string]any{
			"input": []any{
				map[string]any{"role": "user", "content": "hello"},
				map[string]any{"role": "system", "content": "Sys prompt."},
				map[string]any{"role": "assistant", "content": "Hi there"},
			},
		}
		result := openai.ExtractSystemMessagesFromInput(reqBody, false)
		require.True(t, result)
		input, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Len(t, input, 3)
		first, ok := input[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "user", first["role"])
		second, ok := input[1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "developer", second["role"])
		third, ok := input[2].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "assistant", third["role"])
		require.Equal(t, "Sys prompt.", reqBody["instructions"])
	})

	t.Run("existing instructions prepended", func(t *testing.T) {
		reqBody := map[string]any{
			"input": []any{
				map[string]any{"role": "system", "content": "Extracted."},
				map[string]any{"role": "user", "content": "hi"},
			},
			"instructions": "Existing instructions.",
		}
		result := openai.ExtractSystemMessagesFromInput(reqBody, false)
		require.True(t, result)
		require.Equal(t, "Extracted.\n\nExisting instructions.", reqBody["instructions"])
		input, ok := reqBody["input"].([]any)
		require.True(t, ok)
		msg, ok := input[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "developer", msg["role"])
	})

	t.Run("omit losslessly promoted text-only messages", func(t *testing.T) {
		reqBody := map[string]any{
			"input": []any{
				map[string]any{"role": "system", "content": "First."},
				map[string]any{
					"role": "system",
					"content": []any{
						map[string]any{"type": "text", "text": "Second "},
						map[string]any{"type": "input_text", "text": "and "},
						map[string]any{"type": "output_text", "text": "third."},
					},
				},
				map[string]any{"role": "user", "content": "hi"},
			},
			"instructions": "Existing.",
		}

		result := openai.ExtractSystemMessagesFromInput(reqBody, true)

		require.True(t, result)
		require.Equal(t, "First.\n\nSecond and third.\n\nExisting.", reqBody["instructions"])
		input, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Len(t, input, 1)
		user, ok := input[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "user", user["role"])
	})

	t.Run("omit keeps mixed system content as developer", func(t *testing.T) {
		reqBody := map[string]any{
			"input": []any{
				map[string]any{
					"role": "system",
					"content": []any{
						map[string]any{"type": "input_text", "text": "Inspect this image."},
						map[string]any{"type": "input_image", "image_url": "https://example.com/image.png"},
					},
				},
				map[string]any{"role": "user", "content": "hi"},
			},
		}

		result := openai.ExtractSystemMessagesFromInput(reqBody, true)

		require.True(t, result)
		require.Equal(t, "Inspect this image.", reqBody["instructions"])
		input, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Len(t, input, 2)
		developer, ok := input[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "developer", developer["role"])
		require.Len(t, developer["content"], 2)
	})
}

// TestApplyCodexOAuthTransform_StripsPromptCacheRetention is a regression
// test: some clients (e.g. Cursor cloud via the Responses-shape compat path)
// send prompt_cache_retention, but the ChatGPT internal Codex endpoint
// rejects it with "Unsupported parameter: prompt_cache_retention".
func TestApplyCodexOAuthTransform_StripsPromptCacheRetention(t *testing.T) {
	reqBody := map[string]any{
		"model":                  "gpt-5.1",
		"prompt_cache_retention": "24h",
		"input": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	_, stillThere := reqBody["prompt_cache_retention"]
	require.False(t, stillThere,
		"prompt_cache_retention must be stripped before forwarding to Codex upstream")
}

func TestApplyCodexOAuthTransform_StripsChatGPTInternalUnsupportedFields(t *testing.T) {
	reqBody := map[string]any{
		"model":                  "gpt-5.4",
		"chat_template_kwargs":   map[string]any{"enable_thinking": true},
		"user":                   "user_123",
		"metadata":               map[string]any{"trace_id": "abc"},
		"prompt_cache_retention": "24h",
		"safety_identifier":      "sid",
		"stream_options":         map[string]any{"include_usage": true},
		"truncation":             "auto",
		"stop_sequences":         []any{"END"},
		"input": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	require.True(t, result.Modified)
	for _, field := range openai.OpenAIChatGPTInternalUnsupportedFields {
		require.NotContains(t, reqBody, field)
	}
}

func TestApplyCodexOAuthTransform_NormalizesPromptAndCommands(t *testing.T) {
	reqBody := map[string]any{
		"model":    "gpt-5.5",
		"prompt":   "hello",
		"commands": []any{"unsupported"},
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)
	require.True(t, result.Modified)
	require.Equal(t, []any{
		map[string]any{"type": "message", "role": "user", "content": "hello"},
	}, reqBody["input"])
	require.NotContains(t, reqBody, "prompt")
	require.NotContains(t, reqBody, "commands")
}

func TestNormalizeOpenAIResponsesImageGenerationTools_StripsGPTImage2InputFidelity(t *testing.T) {
	reqBody := map[string]any{"tools": []any{
		map[string]any{"type": "image_generation", "model": "gpt-image-2-codex", "input_fidelity": "high"},
		map[string]any{"type": "image_generation", "model": "gpt-image-1.5", "input_fidelity": "high"},
	}}

	require.True(t, openai.NormalizeOpenAIResponsesImageGenerationTools(reqBody))
	tools, ok := reqBody["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)
	first, ok := tools[0].(map[string]any)
	require.True(t, ok)
	second, ok := tools[1].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, first, "input_fidelity")
	require.Equal(t, "high", second["input_fidelity"])
}

func TestOpenAIRequestBodyImageGenerationToolNeedsNormalization_GPTImage2InputFidelity(t *testing.T) {
	body := []byte(`{"tools":[{"type":"image_generation","model":"gpt-image-2-codex","input_fidelity":"high"}]}`)

	require.True(t, gatewayprovider.ImageIntent().OpenAIRequestBodyImageGenerationToolNeedsNormalization(body))
}

func TestApplyCodexOAuthTransform_ExtractsSystemMessages(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.1",
		"input": []any{
			map[string]any{"role": "system", "content": "You are a coding assistant."},
			map[string]any{"role": "user", "content": "Write a function."},
		},
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	require.True(t, result.Modified)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)
	system, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "developer", system["role"])
	require.Equal(t, "You are a coding assistant.", system["content"])
	user, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "user", user["role"])

	instructions, ok := reqBody["instructions"].(string)
	require.True(t, ok)
	require.Equal(t, "You are a coding assistant.", instructions)
}

func TestApplyCodexOAuthTransform_JsonObjectKeepsJsonInstructionInInput(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": []any{
			map[string]any{
				"role":    "system",
				"content": "You are an assistant. Output JSON only.",
			},
			map[string]any{
				"role":    "user",
				"content": "symbol data without the keyword",
			},
		},
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_object",
			},
		},
	}

	result := gatewayprovider.ApplyCodexOAuthTransform(reqBody, false, false)

	require.True(t, result.Modified)
	instructions, ok := reqBody["instructions"].(string)
	require.True(t, ok)
	require.Contains(t, instructions, "JSON")
	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)
	developer, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "developer", developer["role"])
	require.Contains(t, developer["content"], "JSON")
	user, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "user", user["role"])
}

func TestIsInstructionsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		reqBody  map[string]any
		expected bool
	}{
		{"missing field", map[string]any{}, true},
		{"nil value", map[string]any{"instructions": nil}, true},
		{"empty string", map[string]any{"instructions": ""}, true},
		{"whitespace only", map[string]any{"instructions": "   "}, true},
		{"non-string", map[string]any{"instructions": 123}, true},
		{"valid string", map[string]any{"instructions": "hello"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := openai.IsInstructionsEmpty(tt.reqBody)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestFilterCodexInput_PreservesReasoningStripsID covers the core OAuth-path
// reasoning contract (replaces the earlier "drops reasoning" test, whose
// premise was wrong). A reasoning item carrying encrypted_content is the
// official channel for replaying reasoning context across turns under
// store=false, so it must survive the filter with encrypted_content intact;
// only its rs_* id is stripped (always, independent of PreserveReferences)
// because a bare rs_* id replayed under store=false 404s upstream. Contracts
// 1/2/3, verified end-to-end against chatgpt.com codex (gpt-5.5). See issue
// #1957.
func TestFilterCodexInput_PreservesReasoningStripsID(t *testing.T) {
	build := func() []any {
		return []any{
			map[string]any{
				"type":              "reasoning",
				"id":                "rs_0672f12450da0b9c0169f07220a6c08198b68c2455ced99344",
				"call_id":           "remove_non_pair_call_id",
				"encrypted_content": "gAAAAAB-enc-payload",
				"summary":           []any{},
			},
		}
	}

	for _, preserve := range []bool{true, false} {
		preserve := preserve
		t.Run(fmt.Sprintf("preserveReferences=%v", preserve), func(t *testing.T) {
			filtered := openai.FilterCodexInput(build(), preserve)
			require.Len(t, filtered, 1)

			item, ok := filtered[0].(map[string]any)
			require.True(t, ok)
			// Contract 2: the reasoning item survives the filter.
			require.Equal(t, "reasoning", item["type"])
			// Contract 2: encrypted_content (cross-turn channel) preserved verbatim.
			require.Equal(t, "gAAAAAB-enc-payload", item["encrypted_content"])
			// Contract 1/3: rs_* id stripped unconditionally, even when
			// PreserveReferences=true (id lookup, not the item, triggers the 404).
			_, hasID := item["id"]
			require.False(t, hasID)
			_, hasCallID := item["call_id"]
			require.False(t, hasCallID)
			// summary passed through untouched.
			summary, ok := item["summary"].([]any)
			require.True(t, ok)
			require.Len(t, summary, 0)
		})
	}
}

// TestFilterCodexInput_BareReasoningStripsIDBackfillsSummary covers contract 1
// plus 5: a reasoning item carrying only an rs_* id (no encrypted_content) is
// kept as an empty shell with the id stripped, and a missing summary is
// backfilled to [] so upstream does not reject it with 400 "Missing required
// parameter 'input[N].summary'". Verified against chatgpt.com codex (gpt-5.5).
func TestFilterCodexInput_BareReasoningStripsIDBackfillsSummary(t *testing.T) {
	input := []any{
		map[string]any{"type": "message", "id": "msg_0", "role": "user", "content": "hi"},
		map[string]any{
			"type":    "reasoning",
			"id":      "rs_0672f12450da0b9c0169f07220a6c08198b68c2455ced99344",
			"summary": []any{},
		},
		map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "tool"},
		map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "{}"},
	}

	filtered := openai.FilterCodexInput(input, false)

	require.Len(t, filtered, 4)
	reasoning, ok := filtered[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "reasoning", reasoning["type"])
	require.Equal(t, []any{}, reasoning["summary"])
	require.NotContains(t, reasoning, "id")
}

func TestFilterCodexInput_ReasoningBackfillsMissingSummary(t *testing.T) {
	// 上游要求 reasoning item 带 summary；缺失时补空数组，同时保留 encrypted_content。
	input := []any{
		map[string]any{
			"type":              "reasoning",
			"id":                "rs_0672f12450da0b9c0169f07220a6c08198b68c2455ced99344",
			"encrypted_content": "gAAAAAB-enc-payload",
		},
	}

	filtered := openai.FilterCodexInput(input, false)

	require.Len(t, filtered, 1)
	reasoning, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "reasoning", reasoning["type"])
	require.Equal(t, []any{}, reasoning["summary"])
	require.Equal(t, "gAAAAAB-enc-payload", reasoning["encrypted_content"])
	require.NotContains(t, reasoning, "id")
}

func TestFilterCodexInput_PreservesReasoningSummaryContentAndEncryptedContent(t *testing.T) {
	// 只移除 store=false 下不可续链的 rs_* id，其余 reasoning 字段必须原样保留。
	summary := []any{map[string]any{"type": "summary_text", "text": "checked files"}}
	content := []any{map[string]any{"type": "reasoning_text", "text": "private state"}}
	input := []any{
		map[string]any{
			"type":              "reasoning",
			"id":                "rs_0672f12450da0b9c0169f07220a6c08198b68c2455ced99344",
			"summary":           summary,
			"content":           content,
			"encrypted_content": "gAAAAAB-enc-payload",
		},
	}

	filtered := openai.FilterCodexInput(input, true)

	require.Len(t, filtered, 1)
	reasoning, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, summary, reasoning["summary"])
	require.Equal(t, content, reasoning["content"])
	require.Equal(t, "gAAAAAB-enc-payload", reasoning["encrypted_content"])
	require.NotContains(t, reasoning, "id")
}

func TestFilterCodexInput_StripsReasoningIDsWhenReferencesArePreserved(t *testing.T) {
	// 续链场景仍移除 reasoning item 的 rs_* id，避免 store=false 时触发不可达引用。
	input := []any{
		map[string]any{
			"type":    "reasoning",
			"id":      "rs_0672f12450da0b9c0169f07220a6c08198b68c2455ced99344",
			"summary": []any{},
		},
	}

	filtered := openai.FilterCodexInput(input, true)

	require.Len(t, filtered, 1)
	reasoning, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "reasoning", reasoning["type"])
	require.NotContains(t, reasoning, "id")
}
