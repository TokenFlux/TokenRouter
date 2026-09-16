// 旧入口只委托唯一 Qoder 请求转换，S11/S15 清理。
package service

import (
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

type qoderThinkingDirective = qoder.QoderThinkingDirective
type qoderPayloadRequest = qoder.QoderPayloadRequest

func BuildQoderPayloadFromChatCompletions(body []byte, userType string) (map[string]any, string, error) {
	return qoder.BuildQoderPayloadFromChatCompletions(body, userType)
}
func BuildQoderPayloadFromChatCompletionsForSite(body []byte, userType string, site qoder.Site) (map[string]any, string, error) {
	return qoder.BuildQoderPayloadFromChatCompletionsForSite(body, userType, site)
}
func parseQoderChatCompletionsPayload(body []byte) (qoderPayloadRequest, error) {
	return qoder.ParseQoderChatCompletionsPayload(body)
}
func BuildQoderPayloadFromAnthropicMessages(body []byte, userType string) (map[string]any, string, error) {
	return qoder.BuildQoderPayloadFromAnthropicMessages(body, userType)
}
func parseQoderResponsesPayload(body []byte) (qoderPayloadRequest, error) {
	return qoder.ParseQoderResponsesPayload(body)
}
func parseQoderAnthropicMessagesPayload(body []byte) (qoderPayloadRequest, error) {
	return qoder.ParseQoderAnthropicMessagesPayload(body)
}
func qoderThinkingDirectiveFromBody(body []byte, effortPaths ...string) qoderThinkingDirective {
	return qoder.QoderThinkingDirectiveFromBody(body, effortPaths...)
}

func buildQoderPayloadWithOptions(request qoderPayloadRequest, sessionID string, messages []qoderMessage, includeSystem bool, includeTools bool) (map[string]any, string) {
	return qoder.BuildQoderPayloadWithOptions(request, sessionID, messages, includeSystem, includeTools)
}

func qoderChatSystemText(messages []protocolopenai.ChatMessage) string {
	return qoder.QoderChatSystemText(messages)
}

func qoderDeclaredToolNameMapper(tools []any) qoderToolNameMapper {
	return qoder.QoderDeclaredToolNameMapper(tools)
}
