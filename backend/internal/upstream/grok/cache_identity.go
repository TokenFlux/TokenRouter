// 缓存身份限定于已鉴权的 Key 与模型；同一原种子只生成同一 UUID，不保存额外状态。
package grok

import (
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

type CacheIdentityInput struct {
	APIKeyID                                                         int64
	Compact                                                          bool
	Model, ClaudeSession, HeaderSession, ConversationID, ExplicitKey string
	StablePrefixSeed, AnchoredSeed, PreviousResponseSeed             func([]byte) string
}

// resolveGrokCacheIdentity 为 xAI 服务端提示缓存派生稳定且租户隔离的路由身份。
// 返回值不包含客户端原始会话标识，可安全发送到上游。
//
// 必须存在有效的下游 API Key。内部探测或请求上下文不完整时主动关闭缓存，避免生成
// 可能被无关租户共享的缓存身份。
func ResolveCacheIdentity(input CacheIdentityInput, body []byte) string {
	apiKeyID := input.APIKeyID
	if apiKeyID <= 0 {
		return ""
	}
	// /responses/compact 不接受 tool_choice，且不代表正常会话轮次；该路径不得添加
	// 缓存身份或免费层路由增强字段。
	if input.Compact {
		return ""
	}

	model := strings.ToLower(strings.TrimSpace(input.Model))
	if model == "" {
		return ""
	}

	seed := ExplicitCacheSeed(input, body)
	if seed == "" {
		seed = input.StablePrefixSeed(body)
		if seed == "" {
			// 仅使用模型会让缓存路由范围过大；没有可复用前缀时回退到首个用户输入派生身份，
			// 避免无关提示共享同一个租户级缓存键。
			seed = input.AnchoredSeed(body)
		}
	}
	if seed == "" {
		return ""
	}

	// upstream.GenerateSessionUUID 会先哈希完整种子再格式化为 UUID；加入带版本的命名空间，
	// 避免该身份与 TokenRouter 派生的其它上游会话标识冲突。
	isolatedSeed := fmt.Sprintf("grok-prompt-cache:v1:%d:%s:%s", apiKeyID, model, seed)
	return upstream.GenerateSessionUUID(isolatedSeed)
}
func ExplicitCacheSeed(input CacheIdentityInput, body []byte) string {
	// Claude Code 会话是 /v1/messages 到 Grok 桥接中最稳定的多轮身份，
	// 优先于通用会话头，以便提示缓存路由与 CPA 行为一致。
	seed := strings.TrimSpace(input.ClaudeSession)
	if seed == "" {
		seed = ExtractClaudeCodeSessionIDFromPayload(body)
	}
	if seed == "" {
		seed = input.HeaderSession
	}
	// Client-declared prompt_cache_key outranks X-Grok-Conv-Id. The
	// grok-build CLI sets this field on recap-style side-calls
	// (turn-summary/title-refresh) to the *parent* session id so the
	// side-call shares the main turn's server-side cache prefix. Its
	// X-Grok-Conv-Id header, in contrast, carries a fresh per-call label
	// ("turn-summary-<uuid>"); preferring the header there fragments the
	// cache identity per side-call and forces a full-price replay of the
	// entire conversation (~300K+ tokens each time). The body field is the
	// official xAI cache-routing signal — respect it when present.
	if seed == "" && len(body) > 0 {
		seed = strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
	}
	if seed == "" {
		seed = strings.TrimSpace(input.ConversationID)
	}
	if seed == "" {
		seed = strings.TrimSpace(input.ExplicitKey)
	}
	// previous_response_id 是最后的回退方案：没有显式会话的多轮 Responses 仍共享缓存身份，
	// 模型已包含在隔离种子中；消息 ID 会被种子辅助函数拒绝。
	if seed == "" && len(body) > 0 {
		seed = input.PreviousResponseSeed(body)
	}
	return seed
}
