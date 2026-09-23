package session

import (
	"strings"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/tidwall/gjson"
)

// GrokPreviousResponseSeed 根据 Responses 的 previous_response_id 返回稳定的粘性种子。
// 仅接受 resp_* 响应 ID，消息 ID 与未知格式不得固定粘性路由或提示缓存身份。
func GrokPreviousResponseSeed(body []byte) string {
	id := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	if id == "" {
		return ""
	}
	if protocolopenai.ClassifyOpenAIPreviousResponseIDKind(id) != protocolopenai.OpenAIPreviousResponseIDKindResponseID {
		return ""
	}
	// 增加命名空间，避免内容派生种子与响应 ID 冲突。
	return "grok-prev-resp:" + id
}

// GrokStickyAffinitySeed 按模型隔离粘性路由，同时不改变
// applyGrokResponsesCacheIdentity 写入的上游 prompt_cache_key。
func GrokStickyAffinitySeed(sessionID string, body []byte) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	model := ""
	if len(body) > 0 {
		model = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "model").String()))
	}
	if model == "" {
		return "grok-affinity:v1:" + sessionID
	}
	return "grok-affinity:v1:" + model + ":" + sessionID
}
