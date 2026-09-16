//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package service

import (
	"net/http"

	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	rawwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/gin-gonic/gin"
)

func ensureOpenAIChatStreamUsage(body []byte) ([]byte, error) {
	return rawwire.EnsureOpenAIChatStreamUsage(body)
}
func (s *OpenAIGatewayService) bufferRawChatCompletions(
	c *gin.Context,
	resp *http.Response,
	account *Account,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	result, err := nativeopenai.ReadRawChatBuffered(upstream.NewDeferredOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, s.nativeRawResponseOptions(c, resp, account, billingModel, upstreamModel, serviceTier, writeChatCompletionsError), originalModel, upstreamModel, reasoningEffort, startTime)
	return chatForwardResult(result, billingModel), err
}
