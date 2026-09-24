package httpapi

import (
	"context"
	"crypto/rand"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"io"
	"net/http"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (p *OpenAIResponseOutput) AnthropicOptions(c *gin.Context, writeError func(*gin.Context, int, string, string)) forward.AnthropicOutputOptions {
	return forward.AnthropicOutputOptions{
		Runtime: bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
		MaxLineSize: func() int {
			if p.Options.Configured && p.Options.MaxLineSize > 0 {
				return p.Options.MaxLineSize
			}
			return openAIResponseDefaultMaxLineSize
		},
		StreamInterval: p.TextStreamInterval,
		CopyHeaders: func(dst, src http.Header) {
			if p.Headers != nil {
				provider.WriteFilteredHeaders(dst, src, p.Headers)
			}
		},
		ReverseTools: func(body []byte) []byte { return ReverseToolNamesIfPresent(c, body) },
		Error:        func(status int, kind, message string) { writeError(c, status, kind, message) },
		Warn:         func(msg string, fields ...zap.Field) { logging.L().Warn(msg, fields...) },
	}
}

func (p *OpenAIResponseOutput) AnthropicDirectOptions(c *gin.Context, account *gatewayprovider.ExecutionAccount) forward.NativeAnthropicOptions {
	return forward.NativeAnthropicOptions{
		AccountID: account.Record.ID,
		UpdateWindow: func(ctx context.Context, h http.Header) {
			if p.Observer != nil {
				gatewayprovider.ObserveExecutionSessionWindow(ctx, p.Observer, account, h)

			}
		},
		ReadBody: func(r io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(r, p.Options.ReadLimit, c, AnthropicResponseTooLarge)
		},
		InvalidJSON: func(ctx context.Context, r *http.Response, body []byte, err error, model string) error {
			return gatewayprovider.NonJSONUpstreamFailure(ctx, p.Observer, r, account, body, err, model)
		},
		ForceCache: requeststate.IsForceCacheBilling, ClassifyCache: anthropic.ClassifyResponseInputAsCacheRead,
		CopyHeaders:  func(dst, src http.Header) { WriteAnthropicPassthroughHeaders(dst, src, p.Headers) },
		ReverseTools: func(body []byte) []byte { return ReverseToolNamesIfPresent(c, body) },
		MaxLineSize: func() int {
			if p.Options.Configured && p.Options.MaxLineSize > 0 {
				return p.Options.MaxLineSize
			}
			return openAIResponseDefaultMaxLineSize
		},
		StreamInterval: p.TextStreamInterval,
		KeepaliveInterval: func() time.Duration {
			if p.Options.Configured && p.Options.StreamKeepaliveInterval > 0 {
				return time.Duration(p.Options.StreamKeepaliveInterval) * time.Second
			}
			return 0
		},
		ExtractData: anthropic.ExtractSSEDataLine, IsTerminal: anthropic.StreamEventIsTerminal,
		Log: func(format string, args ...any) { logging.LegacyPrintf("service.gateway", format, args...) },
		HandleTimeout: func(ctx context.Context, model string) {
			if p.Observer != nil {
				p.Observer.Core.HandleStreamTimeout(ctx, gatewayprovider.ExecutionRecord(account), model)

			}
		},
	}
}
