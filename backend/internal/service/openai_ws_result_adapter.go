package service

import gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"

// wsForwardResult 显式投影结果，不用不透明旧对象跨越核心边界。
func wsForwardResult(r *OpenAIForwardResult) *gatewayws.ForwardResult {
	if r == nil {
		return nil
	}
	out := &gatewayws.ForwardResult{
		RequestID:                   r.RequestID,
		ResponseID:                  r.ResponseID,
		UpstreamHeaders:             r.UpstreamHeaders,
		Usage:                       r.Usage,
		Model:                       r.Model,
		BillingModel:                r.BillingModel,
		UpstreamModel:               r.UpstreamModel,
		UpstreamResponseServiceTier: r.UpstreamResponseServiceTier,
		UpstreamEndpoint:            r.UpstreamEndpoint,
		ServiceTier:                 r.ServiceTier,
		ReasoningEffort:             r.ReasoningEffort,
		RequestedReasoningEffort:    r.RequestedReasoningEffort,
		Stream:                      r.Stream,
		OpenAIWSMode:                r.OpenAIWSMode,
		UpstreamTerminalEvent:       r.UpstreamTerminalEvent,
		ResponseHeaders:             r.ResponseHeaders,
		Duration:                    r.Duration,
		FirstTokenMs:                r.FirstTokenMs,
		ClientDisconnect:            r.ClientDisconnect,
		ImageCount:                  r.ImageCount,
		ImageSize:                   r.ImageSize,
		ImageInputSize:              r.ImageInputSize,
		ImageOutputSize:             r.ImageOutputSize,
		ImageOutputSizes:            r.ImageOutputSizes,
		ImageSizeSource:             r.ImageSizeSource,
		ImageSizeBreakdown:          r.ImageSizeBreakdown,
		VideoCount:                  r.VideoCount,
		VideoResolution:             r.VideoResolution,
		VideoDurationSeconds:        r.VideoDurationSeconds,
		WebSearchCalls:              r.WebSearchCalls,
		SearchCount:                 r.SearchCount,
		AudioUsage:                  r.AudioUsage,
		WSReplayInput:               r.wsReplayInput, WSReplayInputExists: r.wsReplayInputExists, WSAccountFailoverReplayInput: r.wsAccountFailoverReplayInput,
		ResponseTurnState: r.ResponseHeaders.Get(openAIWSTurnStateHeader),
	}
	if r.UpstreamWarning != nil {
		out.UpstreamWarning = &gatewayws.UpstreamWarning{StatusCode: r.UpstreamWarning.StatusCode, ResponseBody: r.UpstreamWarning.ResponseBody, Message: r.UpstreamWarning.Message}
	}
	return out
}

// legacyWSForwardResult 保留完成 hooks 所需旧形状，规则不在此执行。
func legacyWSForwardResult(r *gatewayws.ForwardResult) *OpenAIForwardResult {
	if r == nil {
		return nil
	}
	out := &OpenAIForwardResult{
		RequestID:                   r.RequestID,
		ResponseID:                  r.ResponseID,
		UpstreamHeaders:             r.UpstreamHeaders,
		Usage:                       r.Usage,
		Model:                       r.Model,
		BillingModel:                r.BillingModel,
		UpstreamModel:               r.UpstreamModel,
		UpstreamResponseServiceTier: r.UpstreamResponseServiceTier,
		UpstreamEndpoint:            r.UpstreamEndpoint,
		ServiceTier:                 r.ServiceTier,
		ReasoningEffort:             r.ReasoningEffort,
		RequestedReasoningEffort:    r.RequestedReasoningEffort,
		Stream:                      r.Stream,
		OpenAIWSMode:                r.OpenAIWSMode,
		UpstreamTerminalEvent:       r.UpstreamTerminalEvent,
		ResponseHeaders:             r.ResponseHeaders,
		Duration:                    r.Duration,
		FirstTokenMs:                r.FirstTokenMs,
		ClientDisconnect:            r.ClientDisconnect,
		ImageCount:                  r.ImageCount,
		ImageSize:                   r.ImageSize,
		ImageInputSize:              r.ImageInputSize,
		ImageOutputSize:             r.ImageOutputSize,
		ImageOutputSizes:            r.ImageOutputSizes,
		ImageSizeSource:             r.ImageSizeSource,
		ImageSizeBreakdown:          r.ImageSizeBreakdown,
		VideoCount:                  r.VideoCount,
		VideoResolution:             r.VideoResolution,
		VideoDurationSeconds:        r.VideoDurationSeconds,
		WebSearchCalls:              r.WebSearchCalls,
		SearchCount:                 r.SearchCount,
		AudioUsage:                  r.AudioUsage,
		wsReplayInput:               r.WSReplayInput, wsReplayInputExists: r.WSReplayInputExists, wsAccountFailoverReplayInput: r.WSAccountFailoverReplayInput,
	}
	if r.UpstreamWarning != nil {
		out.UpstreamWarning = &OpenAIUpstreamWarning{StatusCode: r.UpstreamWarning.StatusCode, ResponseBody: r.UpstreamWarning.ResponseBody, Message: r.UpstreamWarning.Message}
	}
	return out
}

// ProjectWSForwardResult 供原生 HTTP 入站适配逐字段接收旧执行入口的 turn 结果。
func ProjectWSForwardResult(result *OpenAIForwardResult) *gatewayws.ForwardResult {
	return wsForwardResult(result)
}

// LegacyWSForwardResult 只供尚未清理的完成/健康入口使用，不承载计算规则。
func LegacyWSForwardResult(result *gatewayws.ForwardResult) *OpenAIForwardResult {
	return legacyWSForwardResult(result)
}
