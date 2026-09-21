// 回程输出参数在原写入时点读取，HTTP 回调不参与协议状态机。
package service

import (
	"context"
	"crypto/rand"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (s *OpenAIGatewayService) nativeAnthropicOutputOptions(c *gin.Context, writeError func(*gin.Context, int, string, string)) forward.AnthropicOutputOptions {
	return forward.AnthropicOutputOptions{
		Runtime: bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
		MaxLineSize: func() int {
			if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
				return s.cfg.Gateway.MaxLineSize
			}
			return defaultMaxLineSize
		},
		StreamInterval: s.anthropicNativeStreamInterval,
		CopyHeaders: func(dst, src http.Header) {
			if s.responseHeaderFilter != nil {
				provider.WriteFilteredHeaders(dst, src, s.responseHeaderFilter)
			}
		},
		ReverseTools: func(body []byte) []byte { return reverseToolNamesIfPresent(c, body) },
		Error:        func(status int, kind, message string) { writeError(c, status, kind, message) },
		Warn:         func(msg string, fields ...zap.Field) { logging.L().Warn(msg, fields...) },
	}
}

func (s *OpenAIGatewayService) nativeAnthropicDirectOptions(c *gin.Context, account *Account) forward.NativeAnthropicOptions {
	return forward.NativeAnthropicOptions{
		AccountID: account.ID,
		UpdateWindow: func(ctx context.Context, h http.Header) {
			if s.rateLimitService != nil {
				s.rateLimitService.UpdateSessionWindow(ctx, account, h)
			}
		},
		ReadBody: func(r io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(r, s.cfg, c, anthropicTooLargeError)
		},
		InvalidJSON: func(ctx context.Context, r *http.Response, body []byte, err error, model string) error {
			return invalidNonStreamingJSONFailoverError(ctx, s.rateLimitService, r, account, body, err, model)
		},
		ForceCache: IsForceCacheBilling, ClassifyCache: anthropic.ClassifyResponseInputAsCacheRead,
		CopyHeaders:  func(dst, src http.Header) { httpapi.WriteAnthropicPassthroughHeaders(dst, src, s.responseHeaderFilter) },
		ReverseTools: func(body []byte) []byte { return reverseToolNamesIfPresent(c, body) },
		MaxLineSize: func() int {
			if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
				return s.cfg.Gateway.MaxLineSize
			}
			return defaultMaxLineSize
		},
		StreamInterval: s.anthropicNativeStreamInterval,
		KeepaliveInterval: func() time.Duration {
			if s.cfg != nil && s.cfg.Gateway.StreamKeepaliveInterval > 0 {
				return time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
			}
			return 0
		},
		ExtractData: anthropic.ExtractSSEDataLine, IsTerminal: anthropic.StreamEventIsTerminal,
		Log: func(format string, args ...any) { logging.LegacyPrintf("service.gateway", format, args...) },
		HandleTimeout: func(ctx context.Context, model string) {
			if s.rateLimitService != nil {
				s.rateLimitService.HandleStreamTimeout(ctx, account, model)
			}
		},
	}
}
