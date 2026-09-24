package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// OpenAIWSHTTPBridgeRequestScopedError 识别只与当前请求有关的错误。
// 这类错误既不修改账号状态，也不能因为池模式配置而回放当前 turn。
func OpenAIWSHTTPBridgeRequestScopedError(account *ExecutionAccount, statusCode int, message string, body []byte) bool {
	if hit, _, _ := upstreamopenai.DetectOpenAICyberPolicy(body); hit {
		return true
	}
	if IsOpenAICyberWarningPayload(body, message) ||
		upstreamopenai.IsOpenAIClientInvalidRequestError(statusCode, message, body) ||
		upstreamopenai.IsOpenAIContextWindowError(message, body) {
		return true
	}
	return account != nil && account.Record.Platform == capability.PlatformGrok && grok.IsGrokContentPolicyRejection(statusCode, body)
}
