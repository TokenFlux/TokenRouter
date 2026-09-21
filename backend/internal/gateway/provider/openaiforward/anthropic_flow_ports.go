// 原生 Anthropic 用例端口只接收模型、协议值和一次 HTTP 操作，不接收完整实体。
package openaiforward

import (
	"context"
	"net/http"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"go.uber.org/zap"
)

type NativeAnthropicPorts interface {
	BillingModel(model, fallback string) string
	Debug(msg string, fields ...zap.Field)
	ErrorResponse(r *http.Response, model string) (*Result, error)
	FailoverHTTP(ctx context.Context, r *http.Response, body []byte, msg, model string) error
	PrepareTransport()
	ReadUpstreamError(r *http.Response) ([]byte, string)
	Sink() upstream.OutputSink
	UpstreamModel(model string) string
	Profile() MessagesProfile
	Error(status int, kind, message string)
	NormalizeThinking(body []byte, model string) ([]byte, bool)
	ThinkingFallback(effort *string, body []byte, model string) *string
	StripEmpty(body []byte) []byte
	FilterSearch(body []byte, model string) []byte
	CacheLimit(body []byte) []byte
	Log(format string, args ...any)
	ProtocolAPIKey() string
	TargetURL() (string, error)
	StreamContext(ctx context.Context, stream bool) (context.Context, context.CancelFunc)
	BuildNative(ctx context.Context, body []byte, key, url string) (*http.Request, error)
	SendNative(r *http.Request) (*http.Response, error)
	TransportErrorNative(ctx context.Context, err error) error
	DirectOptions() NativeAnthropicOptions
	AdaptResponsesTools(body []byte) ([]byte, bridge.ResponsesClientToolMapping, error)
	ResponsesToAnthropic(r *protocolopenai.ResponsesRequest) (*protocolanthropic.AnthropicRequest, error)
	ChatToResponses(r *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error)
	ResponsesEffort(body []byte, models ...string) *string
	ChatEffort(body []byte, models ...string) *string
	MapStatus(status int) int
	OutputOptions() AnthropicOutputOptions
}
