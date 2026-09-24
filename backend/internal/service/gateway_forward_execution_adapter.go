package service

import (
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
)

// legacyForwardExecutionResult 显式投影同步结果的全部字段，不复制转换状态。
func legacyForwardExecutionResult(v *forwardcore.Result) *forwardcore.MessagesResult {
	if v == nil {
		return nil
	}
	return &forwardcore.MessagesResult{
		RequestID:                   v.RequestID,
		UpstreamHeaders:             v.UpstreamHeaders,
		Usage:                       v.Usage,
		Model:                       v.Model,
		UpstreamModel:               v.UpstreamModel,
		Stream:                      v.Stream,
		Duration:                    v.Duration,
		FirstTokenMs:                v.FirstTokenMs,
		ClientDisconnect:            v.ClientDisconnect,
		ReasoningEffort:             v.ReasoningEffort,
		RequestedReasoningEffort:    v.RequestedReasoningEffort,
		UpstreamResponseServiceTier: v.UpstreamResponseServiceTier,
		ServiceTier:                 v.ServiceTier,
		ImageCount:                  v.ImageCount,
		ImageSize:                   v.ImageSize,
		ImageInputSize:              v.ImageInputSize,
		ImageOutputSize:             v.ImageOutputSize,
		ImageOutputSizes:            v.ImageOutputSizes,
		ImageSizeSource:             v.ImageSizeSource,
		ImageSizeBreakdown:          v.ImageSizeBreakdown,
		SearchCount:                 v.SearchCount,
		AudioUsage:                  v.AudioUsage,
	}
}
