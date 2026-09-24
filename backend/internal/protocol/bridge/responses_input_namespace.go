package bridge

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func HasOpenAIResponsesNamespaceToolDeclaration(body []byte) bool {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	found := false
	tools.ForEach(func(_, tool gjson.Result) bool {
		if strings.EqualFold(strings.TrimSpace(tool.Get("type").String()), "namespace") {
			found = true
			return false
		}
		return true
	})
	return found
}

// openAIResponsesToolCallItemTypes 是携带 namespace 的调用项类型集合。与
// removeOpenAIResponsesRejectedNamespaceAtIndex 的反应式白名单保持一致；codex-rs
// protocol/src/models.rs 中只有 FunctionCall 与 CustomToolCall 序列化 namespace，
// 其余类型带该字段一定是非 Codex 客户端或历史残留，清掉才安全。
var openAIResponsesToolCallItemTypes = map[string]bool{
	"function_call":    true,
	"tool_call":        true,
	"custom_tool_call": true,
	"mcp_tool_call":    true,
}

// StripOpenAIResponsesInputNamespaces 仅移除 input 数组直接子项的 namespace，
// 保留工具声明和嵌套内容中的同名字段。keepToolCallNamespaces 为 true 时，调用项
// 保留 namespace。一次性重建 input 数组可让长历史记录保持线性处理。
func StripOpenAIResponsesInputNamespaces(body []byte, keepToolCallNamespaces bool) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"namespace"`)) {
		return body, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, nil
	}

	var rebuilt bytes.Buffer
	rebuilt.Grow(len(input.Raw))
	_ = rebuilt.WriteByte('[')
	changed := false
	first := true
	var stripErr error
	input.ForEach(func(_, item gjson.Result) bool {
		if !first {
			_ = rebuilt.WriteByte(',')
		}
		first = false
		itemBody := []byte(item.Raw)
		if item.IsObject() && item.Get("namespace").Exists() &&
			(!keepToolCallNamespaces || !IsOpenAIResponsesToolCallItemType(item.Get("type").String())) {
			itemBody, stripErr = sjson.DeleteBytes(itemBody, "namespace")
			if stripErr != nil {
				return false
			}
			changed = true
		}
		_, _ = rebuilt.Write(itemBody)
		return true
	})
	_ = rebuilt.WriteByte(']')
	if stripErr != nil {
		return body, fmt.Errorf("delete OpenAI input namespace: %w", stripErr)
	}
	if !changed {
		return body, nil
	}
	stripped, err := sjson.SetRawBytes(body, "input", rebuilt.Bytes())
	if err != nil {
		return body, fmt.Errorf("replace OpenAI input after namespace deletion: %w", err)
	}
	return stripped, nil
}
