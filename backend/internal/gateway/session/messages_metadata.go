package session

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/tidwall/gjson"
)

func MessagesMetadataSession(claudeSessionID, sessionHash, promptCacheKey, reqModel string, body []byte) (string, string) {
	// Anthropic metadata.user_id 只作为账号粘性信号。上游 GPT/Codex 缓存键
	// 交给 ForwardAsAnthropic 从 cache_control 或完整消息 digest 派生，避免
	// 固定 metadata key 压住后续 turn 的缓存滚动。
	//
	// Claude Code 的 X-Claude-Code-Session-Id 是比 body content fallback 更稳定的
	// 会话边界，但它只用于本地账号粘性；不要把它提升为 prompt_cache_key 或上游
	// session_id，否则会改变现有 Messages→Codex 缓存滚动语义。
	if promptCacheKey == "" {
		if claudeSessionID != "" {
			return currentSessionHash(claudeSessionID), promptCacheKey
		}
	}
	if sessionHash != "" {
		return sessionHash, promptCacheKey
	}
	if userID := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String()); userID != "" {
		seed := reqModel + "-" + userID
		sessionHash = currentSessionHash(seed)
	}
	return sessionHash, promptCacheKey
}

// currentSessionHash 复用调度唯一哈希编码。
func currentSessionHash(seed string) string {
	current, _ := scheduler.DeriveSessionHashes(seed)
	return current
}
