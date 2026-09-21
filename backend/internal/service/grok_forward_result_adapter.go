package service

import (
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	grokforward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/grokforward"
)

// nativeGrokForwardResult 显式投影同步观测结果，不复制平台算法或完成状态。
func nativeGrokForwardResult(v *forwardcore.OpenAIResult) *grokforward.Result {
	if v == nil {
		return nil
	}
	out := &grokforward.Result{
		RequestID:                   v.RequestID,
		ResponseID:                  v.ResponseID,
		UpstreamHeaders:             v.UpstreamHeaders,
		Usage:                       v.Usage,
		Model:                       v.Model,
		BillingModel:                v.BillingModel,
		UpstreamModel:               v.UpstreamModel,
		UpstreamResponseServiceTier: v.UpstreamResponseServiceTier,
		UpstreamEndpoint:            v.UpstreamEndpoint,
		ServiceTier:                 v.ServiceTier,
		ReasoningEffort:             v.ReasoningEffort,
		RequestedReasoningEffort:    v.RequestedReasoningEffort,
		Stream:                      v.Stream,
		OpenAIWSMode:                v.OpenAIWSMode,
		UpstreamTerminalEvent:       v.UpstreamTerminalEvent,
		ResponseHeaders:             v.ResponseHeaders,
		Duration:                    v.Duration,
		FirstTokenMs:                v.FirstTokenMs,
		ClientDisconnect:            v.ClientDisconnect,
		ImageCount:                  v.ImageCount,
		ImageSize:                   v.ImageSize,
		ImageInputSize:              v.ImageInputSize,
		ImageOutputSize:             v.ImageOutputSize,
		ImageOutputSizes:            v.ImageOutputSizes,
		ImageSizeSource:             v.ImageSizeSource,
		ImageSizeBreakdown:          v.ImageSizeBreakdown,
		VideoCount:                  v.VideoCount,
		VideoResolution:             v.VideoResolution,
		VideoDurationSeconds:        v.VideoDurationSeconds,
		WebSearchCalls:              v.WebSearchCalls,
		SearchCount:                 v.SearchCount,
		AudioUsage:                  v.AudioUsage,
	}
	if v.UpstreamWarning != nil {
		out.UpstreamWarning = &forwardcore.UpstreamWarning{StatusCode: v.UpstreamWarning.StatusCode, ResponseBody: v.UpstreamWarning.ResponseBody, Message: v.UpstreamWarning.Message}
	}
	return out
}

// legacyGrokForwardResult 显式投影同步观测结果，不复制平台算法或完成状态。
func legacyGrokForwardResult(v *grokforward.Result) *forwardcore.OpenAIResult {
	if v == nil {
		return nil
	}
	out := &forwardcore.OpenAIResult{
		RequestID:                   v.RequestID,
		ResponseID:                  v.ResponseID,
		UpstreamHeaders:             v.UpstreamHeaders,
		Usage:                       v.Usage,
		Model:                       v.Model,
		BillingModel:                v.BillingModel,
		UpstreamModel:               v.UpstreamModel,
		UpstreamResponseServiceTier: v.UpstreamResponseServiceTier,
		UpstreamEndpoint:            v.UpstreamEndpoint,
		ServiceTier:                 v.ServiceTier,
		ReasoningEffort:             v.ReasoningEffort,
		RequestedReasoningEffort:    v.RequestedReasoningEffort,
		Stream:                      v.Stream,
		OpenAIWSMode:                v.OpenAIWSMode,
		UpstreamTerminalEvent:       v.UpstreamTerminalEvent,
		ResponseHeaders:             v.ResponseHeaders,
		Duration:                    v.Duration,
		FirstTokenMs:                v.FirstTokenMs,
		ClientDisconnect:            v.ClientDisconnect,
		ImageCount:                  v.ImageCount,
		ImageSize:                   v.ImageSize,
		ImageInputSize:              v.ImageInputSize,
		ImageOutputSize:             v.ImageOutputSize,
		ImageOutputSizes:            v.ImageOutputSizes,
		ImageSizeSource:             v.ImageSizeSource,
		ImageSizeBreakdown:          v.ImageSizeBreakdown,
		VideoCount:                  v.VideoCount,
		VideoResolution:             v.VideoResolution,
		VideoDurationSeconds:        v.VideoDurationSeconds,
		WebSearchCalls:              v.WebSearchCalls,
		SearchCount:                 v.SearchCount,
		AudioUsage:                  v.AudioUsage,
	}
	if v.UpstreamWarning != nil {
		out.UpstreamWarning = &forwardcore.UpstreamWarning{StatusCode: v.UpstreamWarning.StatusCode, ResponseBody: v.UpstreamWarning.ResponseBody, Message: v.UpstreamWarning.Message}
	}
	return out
}
