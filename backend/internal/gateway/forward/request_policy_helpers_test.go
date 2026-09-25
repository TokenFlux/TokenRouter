package forward

import (
	"encoding/json"
	"testing"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/stretchr/testify/require"
)

// 这些测试辅助函数只检查工具配对和解析断言，供旧入口的型号策略契约使用。
// assertAnthropicPairing 校验 Anthropic Messages 工具配对不变量，避免上游返回 400。
func assertAnthropicPairing(t *testing.T, messages []protocolanthropic.AnthropicMessage) {
	t.Helper()
	for i, m := range messages {
		blocks := parseContentBlocks(m.Content)

		// 不允许连续两条消息角色相同。
		if i > 0 {
			require.NotEqualf(t, messages[i-1].Role, m.Role, "consecutive %s messages at %d", m.Role, i)
		}

		for _, b := range blocks {
			switch b.Type {
			case "tool_result":
				// tool_result 必须在前一条消息里有对应 tool_use。
				require.Positivef(t, i, "tool_result %s has no previous message", b.ToolUseID)
				prev := parseContentBlocks(messages[i-1].Content)
				require.Truef(t, hasToolUse(prev, b.ToolUseID),
					"tool_result %s has no corresponding tool_use in previous message", b.ToolUseID)
			case "tool_use":
				// tool_use 必须在后一条消息里有对应 tool_result。
				require.Lessf(t, i+1, len(messages), "tool_use %s has no following message", b.ID)
				next := parseContentBlocks(messages[i+1].Content)
				require.Truef(t, hasToolResult(next, b.ID),
					"tool_use %s is not answered in the next message", b.ID)
			}
		}
	}
}

func hasToolUse(blocks []protocolanthropic.AnthropicContentBlock, id string) bool {
	for _, b := range blocks {
		if b.Type == "tool_use" && b.ID == id {
			return true
		}
	}
	return false
}

func hasToolResult(blocks []protocolanthropic.AnthropicContentBlock, toolUseID string) bool {
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID == toolUseID {
			return true
		}
	}
	return false
}

// parseContentBlocks 只解码工具配对断言需要的数组；纯文本没有工具块。
func parseContentBlocks(raw json.RawMessage) []protocolanthropic.AnthropicContentBlock {
	var blocks []protocolanthropic.AnthropicContentBlock
	_ = json.Unmarshal(raw, &blocks)
	return blocks
}
