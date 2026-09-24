package openai

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// SupplementCompactionItemFromSSE 保证 compact 请求的终态 output 携带
// compaction item：终态 output 非空但缺失 compaction、而原始事件流的
// output_item.done（或 added）中存在时（上游不一致形态），以 raw JSON 补入。
// Codex remote compact v2 只从 output_item.done 收集 item 且要求恰好一个
// compaction item——纯流式透传（v0.1.146）下客户端直接读事件流天然拿得到，
// SSE→JSON 提取链路必须给出等价结果。非 compact 请求原样返回。
func SupplementCompactionItemFromSSE(compact bool, finalResponse []byte, bodyText string) []byte {
	if !compact {
		return finalResponse
	}
	if len(gjson.GetBytes(finalResponse, "output").Array()) == 0 {
		// 空 output 由 reconstructResponseOutputFromSSE 整体修补，不在此处理。
		return finalResponse
	}
	if ResponsesOutputHasCompactionItem(finalResponse) {
		return finalResponse
	}
	item, found := FindRawCompactionItemFromSSE(bodyText)
	if !found {
		return finalResponse
	}
	patched, err := sjson.SetRawBytes(finalResponse, "output.-1", item)
	if err != nil {
		return finalResponse
	}
	return patched
}

func OpenAIStreamEventIsTerminal(data string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return false
	}
	if trimmed == "[DONE]" {
		return true
	}
	return OpenAIStreamEventTypeIsTerminal(gjson.Get(trimmed, "type").String())
}

// OpenAIStreamEventIsTerminalWithType 复用已提取的 type，避免 SSE 热路径重复扫描 JSON。
func OpenAIStreamEventIsTerminalWithType(data, eventType string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return false
	}
	if trimmed == "[DONE]" {
		return true
	}
	return OpenAIStreamEventTypeIsTerminal(eventType)
}
