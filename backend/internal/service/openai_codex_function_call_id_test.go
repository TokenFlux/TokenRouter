//go:build unit

package service

import (
	"testing"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
)

// TestFilterCodexInput_StripsFunctionCallItemID_WhenPreservingReferences 验证续链模式下
// 也会剥离 function_call 中非 fc 前缀（例如 item_*）的 id。OpenAI 上游要求
// function_call id 以 "fc" 开头，否则会返回 400：
// "Expected an ID that begins with 'fc'."（#3785）
func TestFilterCodexInput_StripsFunctionCallItemID_WhenPreservingReferences(t *testing.T) {
	input := []any{
		map[string]any{
			"type":    "function_call",
			"id":      "item_A9v0SNfS3VaLrfX0j3y4xhyK",
			"call_id": "fc_abc123",
			"name":    "bash",
		},
		map[string]any{
			"type":    "function_call_output",
			"call_id": "fc_abc123",
			"output":  "done",
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 2)

	fc, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", fc["type"])
	_, hasID := fc["id"]
	require.False(t, hasID, "item_* id should be stripped from function_call")
	require.Equal(t, "fc_abc123", fc["call_id"], "call_id must be preserved")
	require.Equal(t, "bash", fc["name"])
}

// TestFilterCodexInput_KeepsFcID_WhenPreservingReferences 验证续链模式下会保留
// function_call 中有效的 fc* id。
func TestFilterCodexInput_KeepsFcID_WhenPreservingReferences(t *testing.T) {
	input := []any{
		map[string]any{
			"type":    "function_call",
			"id":      "fc_validID123",
			"call_id": "fc_validID123",
			"name":    "bash",
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 1)
	fc, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fc_validID123", fc["id"], "valid fc* id must be preserved")
}

// TestFilterCodexInput_StripsItemIDFromAllToolCallInputTypes 验证所有调用输入类型
// （不含输出类型）中的 item_* id 都会被剥离。

func TestFilterCodexInput_PreservesNativeCustomAndToolSearchIDs(t *testing.T) {
	input := []any{
		map[string]any{"type": "custom_tool_call", "id": "ctc_valid", "call_id": "call_custom", "name": "apply_patch"},
		map[string]any{"type": "tool_search_call", "id": "tsc_valid", "call_id": "call_search"},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})

	require.Equal(t, "ctc_valid", testassert.MustType[map[string]any](filtered[0])["id"])
	require.Equal(t, "tsc_valid", testassert.MustType[map[string]any](filtered[1])["id"])
}

func TestFilterCodexInput_StripsWrongCustomAndToolSearchIDs(t *testing.T) {
	input := []any{
		map[string]any{"type": "custom_tool_call", "id": "fc_wrong", "call_id": "call_custom", "name": "apply_patch"},
		map[string]any{"type": "tool_search_call", "id": "fc_wrong", "call_id": "call_search"},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})

	require.NotContains(t, testassert.MustType[map[string]any](filtered[0]), "id")
	require.NotContains(t, testassert.MustType[map[string]any](filtered[1]), "id")
}

func TestFilterCodexInput_MapsItemReferencesToNativeToolCallPair(t *testing.T) {
	input := []any{
		map[string]any{"type": "custom_tool_call", "id": "fc_custom", "call_id": "call_custom", "name": "apply_patch"},
		map[string]any{"type": "custom_tool_call_output", "call_id": "fc_custom", "output": "done"},
		map[string]any{"type": "item_reference", "id": "call_custom"},
		map[string]any{"type": "tool_search_call", "id": "fc_search", "call_id": "call_search"},
		map[string]any{"type": "tool_search_output", "call_id": "fc_search", "output": "result"},
		map[string]any{"type": "item_reference", "id": "call_search"},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})

	require.Equal(t, "ctc_custom", testassert.MustType[map[string]any](filtered[0])["call_id"])
	require.Equal(t, "ctc_custom", testassert.MustType[map[string]any](filtered[1])["call_id"])
	require.Equal(t, "ctc_custom", testassert.MustType[map[string]any](filtered[2])["id"])
	require.Equal(t, "tsc_search", testassert.MustType[map[string]any](filtered[3])["call_id"])
	require.Equal(t, "tsc_search", testassert.MustType[map[string]any](filtered[4])["call_id"])
	require.Equal(t, "tsc_search", testassert.MustType[map[string]any](filtered[5])["id"])
}

func TestFilterCodexInput_PreservesAmbiguousItemReference(t *testing.T) {
	input := []any{
		map[string]any{"type": "custom_tool_call", "call_id": "call_shared", "name": "apply_patch"},
		map[string]any{"type": "tool_search_call", "call_id": "call_shared"},
		map[string]any{"type": "item_reference", "id": "call_shared"},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})

	require.Equal(t, "ctc_shared", testassert.MustType[map[string]any](filtered[0])["call_id"])
	require.Equal(t, "tsc_shared", testassert.MustType[map[string]any](filtered[1])["call_id"])
	require.Equal(t, "call_shared", testassert.MustType[map[string]any](filtered[2])["id"])
}

func TestFilterCodexInput_PreservesNativeItemIDReferenceIndependentlyFromCallID(t *testing.T) {
	input := []any{
		map[string]any{"type": "custom_tool_call", "id": "ctc_item", "call_id": "call_custom", "name": "apply_patch"},
		map[string]any{"type": "item_reference", "id": "ctc_item"},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})

	require.Equal(t, "ctc_item", testassert.MustType[map[string]any](filtered[0])["id"])
	require.Equal(t, "ctc_custom", testassert.MustType[map[string]any](filtered[0])["call_id"])
	require.Equal(t, "ctc_item", testassert.MustType[map[string]any](filtered[1])["id"])
}

func TestFilterCodexInput_ExistingItemIDWinsOverLegacyCallIDMapping(t *testing.T) {
	input := []any{
		map[string]any{"type": "custom_tool_call", "call_id": "call_shared", "name": "apply_patch"},
		map[string]any{"type": "function_call_output", "id": "call_shared", "call_id": "call_other", "output": "done"},
		map[string]any{"type": "item_reference", "id": "call_shared"},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})

	require.Equal(t, "ctc_shared", testassert.MustType[map[string]any](filtered[0])["call_id"])
	require.Equal(t, "call_shared", testassert.MustType[map[string]any](filtered[1])["id"])
	require.Equal(t, "call_shared", testassert.MustType[map[string]any](filtered[2])["id"])
}

func TestFilterCodexInput_NormalizesCrossTurnLegacyCallReference(t *testing.T) {
	input := []any{
		map[string]any{"type": "item_reference", "id": "call_previous_turn"},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})

	require.Equal(t, "fc_previous_turn", testassert.MustType[map[string]any](filtered[0])["id"])
}

func TestFilterCodexInput_PreservesNativeRemoteItemReferences(t *testing.T) {
	for _, id := range []string{"fc_remote", "ctc_remote", "tsc_remote", "msg_remote", "rs_remote", "vendor_remote"} {
		t.Run(id, func(t *testing.T) {
			input := []any{map[string]any{"type": "item_reference", "id": id}}

			filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})

			require.Equal(t, id, testassert.MustType[map[string]any](filtered[0])["id"])
		})
	}
}

// TestFilterCodexInput_StripsItemIDFromAllToolCallInputTypes verifies that
// item_* ids are stripped from all call-input types (not output types).
func TestFilterCodexInput_StripsItemIDFromAllToolCallInputTypes(t *testing.T) {
	types := []string{"function_call", "tool_call", "local_shell_call", "tool_search_call", "custom_tool_call", "mcp_tool_call"}

	for _, typ := range types {
		input := []any{
			map[string]any{
				"type":    typ,
				"id":      "item_xyz",
				"call_id": "fc_001",
				"name":    "tool",
			},
		}
		filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
			PreserveReferences: true,
		})
		require.Len(t, filtered, 1)
		item, ok := filtered[0].(map[string]any)
		require.True(t, ok)
		_, hasID := item["id"]
		require.False(t, hasID, "item_* id should be stripped from %s", typ)
	}
}

// TestFilterCodexInput_OutputTypeKeepsItemID 验证工具输出项（例如
// function_call_output）仍会保留 id，只有调用输入类型受 fc* 约束。
func TestFilterCodexInput_OutputTypeKeepsItemID(t *testing.T) {
	input := []any{
		map[string]any{
			"type":    "function_call_output",
			"id":      "o1",
			"call_id": "fc_abc",
			"output":  "done",
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 1)
	out, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "o1", out["id"], "output item id should be preserved")
}

// TestFilterCodexInput_NonToolCallItemKeepsID 验证续链模式下，不受 fc* 调用输入
// 和 msg* message 前缀约束的条目仍会保留 id；message 另有独立测试覆盖。
func TestFilterCodexInput_NonToolCallItemKeepsID(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "web_search_call",
			"id":   "ws_001",
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 1)
	item, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ws_001", item["id"], "unconstrained items keep their id in preserve mode")
}
