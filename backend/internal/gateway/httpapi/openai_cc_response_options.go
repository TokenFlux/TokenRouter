package httpapi

import (
	"io"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (p *OpenAIResponseOutput) CCOptions(c *gin.Context, writeError func(*gin.Context, int, string, string)) openai.CCResponseOptions {
	tierObserver := &forwardcore.ResponseObserver{}
	maxLineSize := openAIResponseDefaultMaxLineSize
	if p.Options.Configured && p.Options.MaxLineSize > 0 {
		maxLineSize = p.Options.MaxLineSize
	}
	return openai.CCResponseOptions{
		MaxLineSize: maxLineSize,
		ObserveChunk: func(body []byte) {
			tierObserver.ObserveOpenAI(body, wire.OpenAIChatCompletionServiceTierEventType(body))
			if observer := UpstreamResponseModelObserverFromContext(c); observer != nil {
				observer.ObserveOpenAI(body, "")
			}
		},
		ObserveJSON: func(body []byte) {
			ObserveOpenAIServiceTierInContext(c, body, "response.completed")
			if observer := UpstreamResponseModelObserverFromContext(c); observer != nil {
				observer.ObserveOpenAI(body, "")
			}
		},
		ServiceTier: tierObserver.ServiceTier,
		ReadBody: func(r io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(r, p.Options.ReadLimit, c, OpenAIResponseTooLarge)
		},
		BodyLimitError: httpclient.ErrResponseBodyTooLarge,
		WriteError:     func(status int, kind, message string) { writeError(c, status, kind, message) },
	}
}
