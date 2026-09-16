// 图片输出适配保留旧 HTTP 上下文、错误策略和观察时点，算法由 upstream 唯一拥有。
package service

import (
	"io"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nativeImageResponseOptions(c *gin.Context) native.ImageResponseOptions {
	return native.ImageResponseOptions{

		PreserveContentType: s.cfg != nil && !s.cfg.Security.ResponseHeaders.Enabled,

		ReadLimit: func() int64 { return resolveUpstreamResponseReadLimit(s.cfg) },

		ReadBody: func(r io.Reader) ([]byte, error) { return ReadUpstreamResponseBody(r, s.cfg, c, openAITooLargeError) },

		ClassifyReadError: func(err error) error {
			if shouldClassifyOpenAIUpstreamStreamReadError(err, c.Request.Context()) {
				return newOpenAIUpstreamStreamReadError(err)
			}
			return err
		},

		ResponseHeaders: func(dst, src http.Header) { responseheaders.WriteFilteredHeaders(dst, src, s.responseHeaderFilter) },

		ObserveError: func(status int, message, detail string) { setOpsUpstreamError(c, status, message, detail) },

		WriteHTTPError: func(err *native.OpenAIImagesUpstreamError) bool {
			return writeOpenAIImagesUpstreamErrorResponse(c, err)
		},

		EmptyOutput: func(body []byte) error {
			return &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: body, RetryableOnSameAccount: true}
		},

		Summary: s.summarizeOpenAIImagesNoOutputBody,

		AdjustedWrittenSize: func() int { return OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) },

		WrittenSize: c.Writer.Size,

		StreamInterval: s.openAIImageStreamDataInterval,

		KeepaliveInterval: s.openAIImageStreamKeepaliveInterval,

		Logf: func(format string, args ...any) { logger.LegacyPrintf("service.openai_gateway", format, args...) },
	}
}

// openAIImagesForwardResult 仅恢复旧网关交付和计费投影，不重算用量。
func openAIImagesForwardResult(result upstream.AttemptResult, parsed *OpenAIImagesRequest, imageCount int) *OpenAIForwardResult {
	return &OpenAIForwardResult{

		RequestID:       result.RequestID,
		UpstreamHeaders: result.UpstreamHeaders,

		Usage: OpenAIUsage{
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
