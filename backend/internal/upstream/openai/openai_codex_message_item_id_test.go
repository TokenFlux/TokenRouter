//go:build unit

package openai_test

import (
	"testing"

	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/stretchr/testify/require"
)

// TestFilterCodexInput_StripsMessageItemID_WhenPreservingReferences 验证续链模式下
// 仍会移除非 msg 前缀的 message id；OpenAI 上游会以 400 拒绝 item_* id。
func TestFilterCodexInput_StripsMessageItemID_WhenPreservingReferences(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "message",
			"id":   "item_3bc5a3fa8ccde25f1c0000d4",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "hello"},
			},
		},
	}

	filtered := upstreamopenai.FilterCodexInputWithOptions(input, upstreamopenai.CodexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 1)

	msg, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "message", msg["type"])
	_, hasID := msg["id"]
	require.False(t, hasID, "item_* id should be stripped from message")
	require.Equal(t, "user", msg["role"], "role must be preserved")
	require.NotNil(t, msg["content"], "content must be preserved")
}

// TestFilterCodexInput_KeepsMsgID_WhenPreservingReferences 验证续链模式会保留
// 合法的 msg* id，避免丢失上下文引用。
func TestFilterCodexInput_KeepsMsgID_WhenPreservingReferences(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "message",
			"id":   "msg_validID123",
			"role": "assistant",
		},
	}

	filtered := upstreamopenai.FilterCodexInputWithOptions(input, upstreamopenai.CodexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 1)
	msg, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "msg_validID123", msg["id"], "valid msg* id must be preserved")
}

// TestFilterCodexInput_StripsMessageIDWhenNotPreservingReferences 验证非续链路径
// 无论 id 前缀是否合法，都会移除 message id。
func TestFilterCodexInput_StripsMessageIDWhenNotPreservingReferences(t *testing.T) {
	for _, id := range []string{"item_abc", "msg_validID123"} {
		input := []any{
			map[string]any{
				"type": "message",
				"id":   id,
				"role": "user",
			},
		}

		filtered := upstreamopenai.FilterCodexInputWithOptions(input, upstreamopenai.CodexInputFilterOptions{
			PreserveReferences: false,
		})

		require.Len(t, filtered, 1)
		msg, ok := filtered[0].(map[string]any)
		require.True(t, ok)
		_, hasID := msg["id"]
		require.False(t, hasID, "id %q should be stripped when not preserving references", id)
	}
}

// TestFilterCodexInput_MessageIDStripDoesNotMutateInput 验证移除 id 时不会原地修改
// 调用方传入的 map。
func TestFilterCodexInput_MessageIDStripDoesNotMutateInput(t *testing.T) {
	original := map[string]any{
		"type": "message",
		"id":   "item_abc",
		"role": "user",
	}

	filtered := upstreamopenai.FilterCodexInputWithOptions([]any{original}, upstreamopenai.CodexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 1)
	require.Equal(t, "item_abc", original["id"], "original input must not be mutated")
}

// TestFilterCodexInput_MessageStripKeepsFunctionCallBehavior 验证 message 与
// function_call 的 id 规则相互独立，避免回归 #3785。
func TestFilterCodexInput_MessageStripKeepsFunctionCallBehavior(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "message",
			"id":   "item_msg_001",
			"role": "user",
		},
		map[string]any{
			"type":    "function_call",
			"id":      "fc_validID123",
			"call_id": "fc_validID123",
			"name":    "bash",
		},
		map[string]any{
			"type":    "function_call",
			"id":      "item_A9v0SNfS3VaLrfX0j3y4xhyK",
			"call_id": "fc_abc123",
			"name":    "bash",
		},
		map[string]any{
			"type":    "function_call_output",
			"id":      "o1",
			"call_id": "fc_abc123",
			"output":  "done",
		},
	}

	filtered := upstreamopenai.FilterCodexInputWithOptions(input, upstreamopenai.CodexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 4)

	msg, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	_, hasID := msg["id"]
	require.False(t, hasID, "message item_* id should be stripped")

	fcValid, ok := filtered[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fc_validID123", fcValid["id"], "valid fc* id must be preserved")

	fcBad, ok := filtered[2].(map[string]any)
	require.True(t, ok)
	_, hasID = fcBad["id"]
	require.False(t, hasID, "function_call item_* id should still be stripped")
	require.Equal(t, "fc_abc123", fcBad["call_id"], "call_id pairing must survive")

	out, ok := filtered[3].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "o1", out["id"], "output item id should be preserved")
	require.Equal(t, "fc_abc123", out["call_id"], "call_id pairing must survive")
}
