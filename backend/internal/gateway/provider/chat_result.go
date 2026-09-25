package provider

import (
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// ChatForwardResult 将供应商用量和模型名称投影给网关完成处理。
func ChatForwardResult(result *openai.CompatResponseResult, billingModel string) *forwardcore.OpenAIResult {
	if result == nil {
		return nil
	}
	return &forwardcore.OpenAIResult{RequestID: result.RequestID, ReasoningEffort: result.ReasoningEffort, ServiceTier: result.ResolvedTier, ResponseID: result.ResponseID, ClientDisconnect: result.ClientDisconnect, UpstreamHeaders: result.UpstreamHeaders, Usage: result.Usage, Model: result.Model, BillingModel: billingModel, UpstreamModel: result.UpstreamModel, UpstreamResponseServiceTier: result.ServiceTier, Stream: result.Stream, Duration: result.Duration, FirstTokenMs: result.FirstTokenMs, SearchCount: result.SearchCount}
}
