package httpapi

import "github.com/TokenFlux/TokenRouter/internal/gateway/media"

// 普通 Responses、passthrough 与 Raw Chat 保留各自请求头白名单。
const (
	chatgptCodexURL      = "https://chatgpt.com/backend-api/codex/responses"
	openaiPlatformAPIURL = "https://api.openai.com/v1/responses"
)

// OpenAI allowed headers whitelist (for non-passthrough).
var openaiAllowedHeaders = map[string]bool{
	"accept-language": true,
	"content-type":    true,
	"conversation_id": true,
	"user-agent":      true,
	"originator":      true,
	"session_id":      true,
	// Codex 设备/会话标识参与账号 namespace 隔离，必须在进入请求构造器时保留。
	"installation_id":            true,
	"x-codex-installation-id":    true,
	"session-id":                 true,
	"thread_id":                  true,
	"thread-id":                  true,
	"turn_id":                    true,
	"turn-id":                    true,
	"window_id":                  true,
	"window-id":                  true,
	"x-codex-window-id":          true,
	"x-client-request-id":        true,
	"x-codex-beta-features":      true,
	"x-codex-turn-state":         true,
	"x-codex-turn-metadata":      true,
	media.ResponsesLiteHeaderKey: true,
}

// OpenAI passthrough allowed headers whitelist.
// 透传模式下仅放行这些低风险请求头，避免将非标准/环境噪声头传给上游触发风控。
var openaiPassthroughAllowedHeaders = map[string]bool{
	"accept":                     true,
	"accept-language":            true,
	"content-type":               true,
	"conversation_id":            true,
	"openai-beta":                true,
	"user-agent":                 true,
	"originator":                 true,
	"session_id":                 true,
	"installation_id":            true,
	"x-codex-installation-id":    true,
	"session-id":                 true,
	"thread_id":                  true,
	"thread-id":                  true,
	"turn_id":                    true,
	"turn-id":                    true,
	"window_id":                  true,
	"window-id":                  true,
	"x-codex-window-id":          true,
	"x-client-request-id":        true,
	"x-codex-beta-features":      true,
	"x-codex-turn-state":         true,
	"x-codex-turn-metadata":      true,
	media.ResponsesLiteHeaderKey: true,
}

// openaiCCRawAllowedHeaders 是 CC 直转路径专用的客户端 header 透传白名单。
//
// **关键**：不能复用 openaiAllowedHeaders——后者含 Codex 客户端专属 header
// （originator / session_id / x-codex-turn-state / x-codex-turn-metadata / conversation_id），
// 这些在 ChatGPT OAuth 上游是必需的，但透传给 DeepSeek/Kimi/GLM 等第三方
// OpenAI 兼容上游会造成：
//   - 完全忽略（多数友好厂商）——隐性污染上游统计
//   - 400 "unknown parameter"（严格上游）——可见错误
//
// 这里仅放行通用 HTTP header；content-type / authorization / accept 由上下文
// 显式设置，不依赖透传。
//
// 参见决策记录：
// pensieve/short-term/maxims/dont-reuse-shared-headers-whitelist-across-different-upstream-trust-domains
var openaiCCRawAllowedHeaders = map[string]bool{
	"accept-language": true,
	"user-agent":      true,
}

// AllowOpenAIRawChatHeader 供媒体和原生 Chat 入口复用同一通用白名单。
func AllowOpenAIRawChatHeader(name string) bool { return openaiCCRawAllowedHeaders[name] }

// AllowOpenAIPassthroughHeader 保留图片入口不携带客户端超时头的范围。
func AllowOpenAIPassthroughHeader(name string) bool { return openaiPassthroughAllowedHeaders[name] }
