// 图片输出适配保留旧 HTTP 上下文、错误策略和观察时点，算法由 upstream 唯一拥有。
package service

import (
	"io"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nativeImageResponseOptions(c *gin.Context) upstreamopenai.ImageResponseOptions {
	return upstreamopenai.ImageResponseOptions{

		PreserveContentType: s.cfg != nil && !s.cfg.Security.ResponseHeaders.Enabled,

		ReadLimit: func() int64 { return resolveUpstreamResponseReadLimit(s.cfg) },

		ReadBody: func(r io.Reader) ([]byte, error) { return ReadUpstreamResponseBody(r, s.cfg, c, openAITooLargeError) },

		ClassifyReadError: func(err error) error {
			if shouldClassifyOpenAIUpstreamStreamReadError(err, c.Request.Context()) {
				return upstreamopenai.NewUpstreamStreamReadError(err)
			}
			return err
		},

		ResponseHeaders: func(dst, src http.Header) { provider.WriteFilteredHeaders(dst, src, s.responseHeaderFilter) },

		ObserveError: func(status int, message, detail string) { gatewayhttp.SetOpsUpstreamError(c, status, message, detail) },

		WriteHTTPError: func(err *upstreamopenai.OpenAIImagesUpstreamError) bool {
			return writeOpenAIImagesUpstreamErrorResponse(c, err)
		},

		EmptyOutput: func(body []byte) error {
			return &forwardcore.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: body, RetryableOnSameAccount: true}
		},

		Summary: s.summarizeOpenAIImagesNoOutputBody,

		AdjustedWrittenSize: func() int { return OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) },

		WrittenSize: c.Writer.Size,

		StreamInterval: s.openAIImageStreamDataInterval,

		KeepaliveInterval: s.openAIImageStreamKeepaliveInterval,

		Logf: func(format string, args ...any) {
			logging.LegacyPrintf("service.openai_gateway", format, args...)
		},
	}
}

// openAIImagesForwardResult 仅恢复旧网关交付和计费投影，不重算用量。
func openAIImagesForwardResult(result upstream.AttemptResult, parsed *media.ImageRequest, imageCount int) *forwardcore.OpenAIResult {
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
