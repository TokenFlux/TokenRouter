//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package service

import (
	"net/http"

	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	rawwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/gin-gonic/gin"
)

func ensureOpenAIChatStreamUsage(body []byte) ([]byte, error) {
	return rawwire.EnsureOpenAIChatStreamUsage(body)
}
func (s *OpenAIGatewayService) bufferRawChatCompletions(
	c *gin.Context,
	resp *http.Response,
	account *gatewayprovider.ExecutionAccount,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*forwardcore.OpenAIResult, error) {
	result, err := openai.ReadRawChatBuffered(upstream.NewDeferredOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, s.responseOutput.RawOptions(c, resp, account, billingModel, upstreamModel, serviceTier, gatewayhttp.WriteForwardChatError), originalModel, upstreamModel, reasoningEffort, startTime)
	return gatewayprovider.ChatForwardResult(result, billingModel), err
}
