package provider

import (
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// ImagesForwardResult 仅恢复旧网关交付和计费投影，不重算用量。
func ImagesForwardResult(result upstream.AttemptResult, parsed *media.ImageRequest, imageCount int) *forwardcore.OpenAIResult {
	return &forwardcore.OpenAIResult{

		RequestID:       result.RequestID,
		UpstreamHeaders: result.UpstreamHeaders,

		Usage: openai.ForwardUsage{
			InputTokens:              result.Usage.InputTokens,
			OutputTokens:             result.Usage.OutputTokens,
			CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
			ImageInputTokens:         result.ImageInputTokens,
			ImageOutputTokens:        result.Usage.ImageOutputTokens,
		},

		Model:            result.Model,
		UpstreamModel:    result.UpstreamModel,
		Stream:           result.Stream,
		ResponseHeaders:  result.UpstreamHeaders.Clone(),
		Duration:         result.Duration,
		FirstTokenMs:     result.FirstTokenMs,
		ImageCount:       imageCount,
		ImageSize:        parsed.SizeTier,
		ImageInputSize:   parsed.Size,
		ImageOutputSizes: result.ImageOutputSizes,
	}
}
