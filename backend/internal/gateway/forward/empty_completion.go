package forward

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

// OpenAISilentRefusalErrorBody 保留网关内部失败编码与旧安全消息。
func OpenAISilentRefusalErrorBody() []byte {
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "upstream_error",
			"code":    openAISilentRefusalErrorCode,
			"message": openAISilentRefusalUpstreamMessage,
		},
	})
	if err != nil {
		return []byte(`{"error":{"type":"upstream_error","code":"openai_silent_refusal","message":"OpenAI upstream returned an empty completion stream with finish_reason=stop and no usage"}}`)
	}
	return body
}

// IsOpenAISilentRefusalErrorBody 判断响应体是否由 OpenAI 静默拒绝检测器生成。
func IsOpenAISilentRefusalErrorBody(body []byte) bool {
	return strings.TrimSpace(gjson.GetBytes(body, "error.code").String()) == openAISilentRefusalErrorCode
}

// OpenAISilentRefusalClientMessage 返回静默拒绝且 failover 耗尽时给客户端看的错误文案。
func OpenAISilentRefusalClientMessage() string {
	return openAISilentRefusalClientMessage
}

const openAISilentRefusalErrorCode = "openai_silent_refusal"
const openAISilentRefusalUpstreamMessage = "OpenAI upstream returned an empty completion stream with finish_reason=stop and no usage"
const openAISilentRefusalClientMessage = "Upstream returned an empty completion without usage; no fallback account was available"
