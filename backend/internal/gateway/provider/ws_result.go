package provider

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
)

// ProjectWSResult 显式投影本次 WS turn 的已观测结果与恢复输入。
func ProjectWSResult(r *forwardcore.OpenAIResult) *gatewayws.ForwardResult {
	if r == nil {
		return nil
	}
	replay, replayExists := r.WSReplayInput()
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
		WSReplayInput:               replay, WSReplayInputExists: replayExists, WSAccountFailoverReplayInput: r.WSAccountFailoverReplayInput(),
		ResponseTurnState: http.Header(r.ResponseHeaders).Get(openai.WSTurnStateHeader),
	}
	if r.UpstreamWarning != nil {
		out.UpstreamWarning = &forwardcore.UpstreamWarning{StatusCode: r.UpstreamWarning.StatusCode, ResponseBody: r.UpstreamWarning.ResponseBody, Message: r.UpstreamWarning.Message}
	}
	return out
}

// ForwardResultFromWS 为完成与健康端口保留原 HTTP 结果形状，不执行计算规则。
func ForwardResultFromWS(r *gatewayws.ForwardResult) *forwardcore.OpenAIResult {
	if r == nil {
		return nil
	}
	out := &forwardcore.OpenAIResult{
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
	}
	out.SetWSReplayInput(r.WSReplayInput, r.WSReplayInputExists)
	out.SetWSAccountFailoverReplayInput(r.WSAccountFailoverReplayInput)
	if r.UpstreamWarning != nil {
		out.UpstreamWarning = &forwardcore.UpstreamWarning{StatusCode: r.UpstreamWarning.StatusCode, ResponseBody: r.UpstreamWarning.ResponseBody, Message: r.UpstreamWarning.Message}
	}
	return out
}
