// 旧入口委托唯一平台或协议实现，S11/S15 清理消费者。
package service

import (
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

var errAntigravityProjectIDRequired = antigravity.ErrProjectIDRequired

type PromptTooLongError = antigravity.PromptTooLongError

func applyThinkingModelSuffix(mappedModel string, thinkingEnabled bool) string {
	return antigravity.ApplyThinkingModelSuffix(mappedModel, thinkingEnabled)
}
func injectIdentityPatchToGeminiRequest(body []byte) ([]byte, error) {
	return antigravity.InjectIdentityPatchToGeminiRequest(body)
}
func (s *AntigravityGatewayService) wrapV1InternalRequest(projectID, model string, originalBody []byte) ([]byte, error) {
	return antigravity.WrapV1InternalRequest(projectID, model, originalBody)
}

func isPromptTooLongError(respBody []byte) bool { return antigravity.IsPromptTooLongError(respBody) }

func getPassthroughOrDefault(upstreamMsg, defaultMsg string) string {
	return antigravity.GetPassthroughOrDefault(upstreamMsg, defaultMsg)
}
func cleanGeminiRequest(body []byte) ([]byte, error) { return antigravity.CleanGeminiRequest(body) }
func preserveChatCompletionTokenLimit(request *protocolopenai.ChatCompletionsRequest, claudeRequest *protocolanthropic.AnthropicRequest) {
	antigravity.PreserveChatCompletionTokenLimit(request, claudeRequest)
}
func enableMixedGeminiToolInvocations(body []byte) ([]byte, error) {
	return antigravity.EnableMixedGeminiToolInvocations(body)
}
func stripThinkingFromClaudeRequest(req *protocolanthropic.ClaudeRequest) (bool, error) {
	return protocolanthropic.StripThinkingFromClaudeRequest(req)
}
func stripSignatureSensitiveBlocksFromClaudeRequest(req *protocolanthropic.ClaudeRequest) (bool, error) {
	return protocolanthropic.StripSignatureSensitiveBlocksFromClaudeRequest(req)
}
