package httpapi

import (
	"net/http"

	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
)

func (s *OpenAIResponseOutput) ChatBuffered(
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*forwardcore.OpenAIResult, error) {
	result, err := openai.ReadChatBuffered(resp, upstream.NewDeferredOutputContext(ResponseSink{Writer: c.Writer}), s.ChatOptions(c, account, resp, originalModel, billingModel, upstreamModel), originalModel, upstreamModel, startTime)
	return gatewayprovider.ChatForwardResult(result, billingModel), err
}
func (s *OpenAIResponseOutput) ChatStreaming(
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
	requestBodyLen int,
) (*forwardcore.OpenAIResult, error) {
	result, err := openai.ReadChatStreaming(resp, upstream.NewDeferredOutputContext(ResponseSink{Writer: c.Writer}), s.ChatOptions(c, account, resp, originalModel, billingModel, upstreamModel), originalModel, upstreamModel, startTime, requestBodyLen)
	return gatewayprovider.ChatForwardResult(result, billingModel), err
}
