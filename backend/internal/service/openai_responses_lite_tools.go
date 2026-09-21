// 账号资格只在执行适配层选择，Lite 报文算法由 upstream/openai 唯一拥有。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// normalizeOpenAIResponsesLitePayloadForAccount 在调用方确认 Lite 标记后按账号契约归一化请求。
// OAuth 账号应用完整内部协议；API Key 账号只关闭并行工具调用，保留标准 Responses 请求语义。
func normalizeOpenAIResponsesLitePayloadForAccount(account *Account, body []byte) ([]byte, bool, error) {
	if account == nil || account.Platform != capability.PlatformOpenAI {
		return body, false, nil
	}
	if account.IsOpenAIOAuth() {
		return openai.NormalizeResponsesLiteToolsPayload(body)
	}
	return openai.NormalizeResponsesLiteParallelToolCallsPayload(body)
}
