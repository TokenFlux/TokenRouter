// 原生 SSE 错误事件类型保持 errors.As 和终态行为。
package anthropic

import (
	"strings"

	"github.com/tidwall/gjson"
)

func StreamEventIsTerminal(eventName, data string) bool {
	if strings.EqualFold(strings.TrimSpace(eventName), "message_stop") {
		return true
	}
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return false
	}
	if trimmed == "[DONE]" {
		return true
	}
	return gjson.Get(trimmed, "type").String() == "message_stop"
}

// StreamErrorEventError 表示上游 SSE 流体内出现 event:error 帧。
// RawData 保留该事件 data: 行的原始内容，供上层写入 failover body 和 Ops 日志。
type StreamErrorEventError struct {
	RawData string
}

func (e *StreamErrorEventError) Error() string { return "have error in stream" }
