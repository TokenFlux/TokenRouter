// 旧会话入口只投影协议字节，摘要算法归平台。
package service

import (
	"time"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

func AnthropicSessionTTL() time.Duration { return claude.AnthropicSessionTTL() }
func BuildAnthropicDigestChain(parsed *ParsedRequest) string {
	if parsed == nil {
		return ""
	}
	return claude.BuildAnthropicDigestChain(parsed.SystemRaw(), parsed.MessagesRaw())
}

func GenerateAnthropicDigestSessionKey(prefixHash, uuid string) string {
	return claude.GenerateAnthropicDigestSessionKey(prefixHash, uuid)
}
