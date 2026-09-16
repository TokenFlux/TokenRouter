// 旧服务入口仅转接平台唯一实现；剩余网关消费者在 S11/S15 退出。
package service

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/gin-gonic/gin"

	qoder "github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

const qoderDefaultMaxTokens = qoder.QoderDefaultMaxTokens
const qoderStreamTimeout = qoder.QoderStreamTimeout

const qoderConversationTTL = qoder.QoderConversationTTL
const qoderRefreshLockPoll = qoder.QoderRefreshLockPoll
const qoderRefreshLockWait = qoder.QoderRefreshLockWait
const qoderAccountStateUpdateTimeout = qoder.QoderAccountStateUpdateTimeout

var defaultQoderModelAliases = qoder.DefaultQoderModelAliases

type qoderModelInfo = qoder.QoderModelInfo
type qoderMessage = qoder.QoderMessage

func ReadQoderSSEEvents(resp *http.Response) ([]qoder.SSEEvent, error) {
	return qoder.ReadQoderSSEEvents(resp)
}
func ReadQoderSSEEventsContext(ctx context.Context, resp *http.Response, keepalive func() error) ([]qoder.SSEEvent, error) {
	return qoder.ReadQoderSSEEventsContext(ctx, resp, keepalive)
}
func qoderNonStreamingKeepalive(c *gin.Context) func() error {
	return qoder.QoderNonStreamingKeepalive(qoderOutputContext(c))
}
func writeQoderStreamKeepalive(c *gin.Context, started bool) error {
	return qoder.WriteQoderStreamKeepalive(qoderOutputContext(c), started)
}
func WriteQoderOpenAIStream(c *gin.Context, model string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) error {
	return qoder.WriteQoderOpenAIStream(qoderOutputContext(c), model, events, toolNameMappers...)
}

type qoderStreamResult = qoder.QoderStreamResult

type qoderToolNameMapper = qoder.QoderToolNameMapper
type qoderOpenAIStreamResponseOption = qoder.QoderOpenAIStreamResponseOption
type qoderAnthropicStreamResponseOption = qoder.QoderAnthropicStreamResponseOption
type qoderResponsesStreamResponseOption = qoder.QoderResponsesStreamResponseOption

func qoderOpenAIStreamToolNameMapper(mapper qoderToolNameMapper) qoderOpenAIStreamResponseOption {
	return qoder.QoderOpenAIStreamToolNameMapper(mapper)
}
func qoderOpenAIStreamIncludeUsage(include bool) qoderOpenAIStreamResponseOption {
	return qoder.QoderOpenAIStreamIncludeUsage(include)
}

func qoderResponsesStreamToolNameMapper(mapper qoderToolNameMapper) qoderResponsesStreamResponseOption {
	return qoder.QoderResponsesStreamToolNameMapper(mapper)
}

func WriteQoderOpenAIStreamResponse(ctx context.Context, c *gin.Context, model string, resp *http.Response, options ...qoderOpenAIStreamResponseOption) (*qoderStreamResult, error) {
	return qoder.WriteQoderOpenAIStreamResponse(ctx, qoderOutputContext(c), model, resp, options...)
}
func WriteQoderAnthropicStream(c *gin.Context, model string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) error {
	return qoder.WriteQoderAnthropicStream(qoderOutputContext(c), model, events, toolNameMappers...)
}
func WriteQoderAnthropicStreamResponse(ctx context.Context, c *gin.Context, model string, resp *http.Response, options ...qoderAnthropicStreamResponseOption) (*qoderStreamResult, error) {
	return qoder.WriteQoderAnthropicStreamResponse(ctx, qoderOutputContext(c), model, resp, options...)
}
func BuildQoderResponsesResponse(model string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) ([]byte, error) {
	return qoder.BuildQoderResponsesResponse(model, events, toolNameMappers...)
}
func BuildQoderResponsesResponseWithID(model, responseID string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) ([]byte, error) {
	return qoder.BuildQoderResponsesResponseWithID(model, responseID, events, toolNameMappers...)
}
func WriteQoderResponsesStreamResponse(ctx context.Context, c *gin.Context, model string, resp *http.Response, options ...qoderResponsesStreamResponseOption) (*qoderStreamResult, error) {
	return qoder.WriteQoderResponsesStreamResponse(ctx, qoderOutputContext(c), model, resp, options...)
}

type qoderEventResult = qoder.QoderEventResult

func scanQoderEvents(ctx context.Context, resp *http.Response, results chan<- qoderEventResult) {
	qoder.ScanQoderEvents(ctx, resp, results)
}

func BuildQoderOpenAICompletion(model string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) ([]byte, error) {
	return qoder.BuildQoderOpenAICompletion(model, events, toolNameMappers...)
}
func BuildQoderAnthropicMessage(model string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) ([]byte, error) {
	return qoder.BuildQoderAnthropicMessage(model, events, toolNameMappers...)
}

func qoderUsageFromEvents(events []qoder.SSEEvent) ClaudeUsage {
	return qoder.QoderUsageFromEvents(events)
}

func resolveQoderModel(model string) qoderModelInfo { return qoder.ResolveQoderModel(model) }
func resolveQoderModelForSite(site qoder.Site, model string) qoderModelInfo {
	return qoder.ResolveQoderModelForSite(site, model)
}

func qoderAnySlice(raw any) []any { return qoder.QoderAnySlice(raw) }

func gjsonString(body []byte, path string) string { return qoder.GjsonString(body, path) }
func gjsonBool(body []byte, path string) bool     { return qoder.GjsonBool(body, path) }

// qoderOutputContext 仅适配旧 HTTP 入口，不创建第二份流状态。
func qoderOutputContext(c *gin.Context) *upstream.OutputContext {
	if c == nil {
		return nil
	}
	return &upstream.OutputContext{Writer: c.Writer}
}
