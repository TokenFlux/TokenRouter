package openaiforward

import native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

// FromCompatResult 仅附加已有计费模型，供应商事实仍由原生读取器提供。
func FromCompatResult(r *native.CompatResponseResult, billing string) *Result {
	if r == nil {
		return nil
	}
	return &Result{RequestID: r.RequestID, ResponseID: r.ResponseID, Headers: r.UpstreamHeaders, Usage: r.Usage, Model: r.Model, BillingModel: billing, UpstreamModel: r.UpstreamModel, UpstreamResponseServiceTier: r.ServiceTier, ServiceTier: r.ResolvedTier, ReasoningEffort: r.ReasoningEffort, Stream: r.Stream, Duration: r.Duration, FirstTokenMs: r.FirstTokenMs, ClientDisconnect: r.ClientDisconnect, SearchCount: r.SearchCount}
}
