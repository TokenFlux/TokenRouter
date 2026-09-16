// 旧工具续链入口只委托协议解析；账号恢复与重试时机仍由调用者决定。
package service

import protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

type ToolContinuationSignals = protocolopenai.ToolContinuationSignals
type FunctionCallOutputValidation = protocolopenai.FunctionCallOutputValidation

func isCodexToolCallContextItemType(typ string) bool {
	return protocolopenai.IsCodexToolCallContextItemType(typ)
}
func isCodexToolCallOutputItemType(typ string) bool {
	return protocolopenai.IsCodexToolCallOutputItemType(typ)
}
func NeedsToolContinuation(reqBody map[string]any) bool {
	return protocolopenai.NeedsToolContinuation(reqBody)
}
func AnalyzeToolContinuationSignals(reqBody map[string]any) ToolContinuationSignals {
	return protocolopenai.AnalyzeToolContinuationSignals(reqBody)
}
func ValidateFunctionCallOutputContextBytes(body []byte) FunctionCallOutputValidation {
	return protocolopenai.ValidateFunctionCallOutputContextBytes(body)
}

type ToolCallOutputContextCoverage = protocolopenai.ToolCallOutputContextCoverage

func AnalyzeToolCallOutputContextCoverageBytes(body []byte) ToolCallOutputContextCoverage {
	return protocolopenai.AnalyzeToolCallOutputContextCoverageBytes(body)
}
func ValidateFunctionCallOutputContext(reqBody map[string]any) FunctionCallOutputValidation {
	return protocolopenai.ValidateFunctionCallOutputContext(reqBody)
}
func HasFunctionCallOutput(reqBody map[string]any) bool {
	return protocolopenai.HasFunctionCallOutput(reqBody)
}
func HasToolCallContext(reqBody map[string]any) bool {
	return protocolopenai.HasToolCallContext(reqBody)
}
func FunctionCallOutputCallIDs(reqBody map[string]any) []string {
	return protocolopenai.FunctionCallOutputCallIDs(reqBody)
}
func HasFunctionCallOutputMissingCallID(reqBody map[string]any) bool {
	return protocolopenai.HasFunctionCallOutputMissingCallID(reqBody)
}
func HasItemReferenceForCallIDs(reqBody map[string]any, callIDs []string) bool {
	return protocolopenai.HasItemReferenceForCallIDs(reqBody, callIDs)
}
