package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// NormalizeResponsesLiteForAccount 在调用者确认 Lite 标记后按原账号资格选择唯一 wire 规范化。
func NormalizeResponsesLiteForAccount(value *account.Record, body []byte) ([]byte, bool, error) {
	if value == nil || value.Platform != capability.PlatformOpenAI {
		return body, false, nil
	}
	if value.IsOpenAIOAuth() {
		return openai.NormalizeResponsesLiteToolsPayload(body)
	}
	return openai.NormalizeResponsesLiteParallelToolCallsPayload(body)
}
