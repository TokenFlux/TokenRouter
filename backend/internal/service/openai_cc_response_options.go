// CC 读取保留本次独立档位观察和请求级观察的原顺序，不安装新共享状态。
package service

import (
	"io"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nativeCCResponseOptions(c *gin.Context, writeError compatErrorWriter) native.CCResponseOptions {
	tierObserver := &upstreamResponseModelObserver{}
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	return native.CCResponseOptions{
		MaxLineSize: maxLineSize,
		ObserveChunk: func(body []byte) {
			tierObserver.ObserveOpenAI(body, wire.OpenAIChatCompletionServiceTierEventType(body))
			if observer := upstreamResponseModelObserverFromContext(c); observer != nil {
				observer.ObserveOpenAI(body, "")
			}
		},
		ObserveJSON: func(body []byte) {
			observeOpenAIServiceTierInContext(c, body, "response.completed")
			if observer := upstreamResponseModelObserverFromContext(c); observer != nil {
				observer.ObserveOpenAI(body, "")
			}
		},
		ServiceTier:    tierObserver.ServiceTier,
		ReadBody:       func(r io.Reader) ([]byte, error) { return ReadUpstreamResponseBody(r, s.cfg, c, openAITooLargeError) },
		BodyLimitError: ErrUpstreamResponseBodyTooLarge,
		WriteError:     func(status int, kind, message string) { writeError(c, status, kind, message) },
	}
}
