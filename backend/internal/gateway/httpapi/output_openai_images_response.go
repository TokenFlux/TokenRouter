package httpapi

import (
	"io"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (p *OpenAIResponseOutput) ImageOptions(c *gin.Context) upstreamopenai.ImageResponseOptions {
	return upstreamopenai.ImageResponseOptions{

		PreserveContentType: p.Options.Configured && !p.Options.ResponseHeadersEnabled,

		ReadLimit: func() int64 { return p.Options.ReadLimit },

		ReadBody: func(r io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(r, p.Options.ReadLimit, c, OpenAIResponseTooLarge)
		},

		ClassifyReadError: func(err error) error {
			if upstreamopenai.ShouldClassifyUpstreamStreamReadError(err, httpclient.ErrResponseBodyTooLarge, c.Request.Context()) {
				return upstreamopenai.NewUpstreamStreamReadError(err)
			}
			return err
		},

		ResponseHeaders: func(dst, src http.Header) { provider.WriteFilteredHeaders(dst, src, p.Headers) },

		ObserveError: func(status int, message, detail string) { SetOpsUpstreamError(c, status, message, detail) },

		WriteHTTPError: func(err *upstreamopenai.OpenAIImagesUpstreamError) bool {
			return WriteOpenAIImagesUpstreamErrorResponse(c, err)
		},

		EmptyOutput: func(body []byte) error {
			return &forwardcore.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: body, RetryableOnSameAccount: true}
		},

		Summary: p.ImageNoOutputSummary,

		AdjustedWrittenSize: func() int { return OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) },

		WrittenSize: c.Writer.Size,

		StreamInterval: p.ImageStreamInterval,

		KeepaliveInterval: p.ImageKeepaliveInterval,

		Logf: func(format string, args ...any) {
			logging.LegacyPrintf("service.openai_gateway", format, args...)
		},
	}
}
