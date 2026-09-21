// ChatPorts 只组合 Chat 实际使用的单步能力；不会继承 Messages 的会话恢复策略。
package openaiforward

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"go.uber.org/zap"
)

type ChatProfile struct {
	MessagesProfile
	Protocol                                             protocol.ProtocolID
	Adaptive, SupportsNativeCN, CNProvider, OpenAIAPIKey bool
}
type ChatPorts interface {
	PrepareChat(context.Context) (ChatProfile, error)
	DispatchChat(context.Context, Dispatch, []byte, string, string) (*Result, error)
	APIKeyID() int64
	AccountIdentity(body map[string]any, key int64)
	AgentRecoveryTried(ctx context.Context) bool
	AutoCacheKey(model string) bool
	BillingModel(model, fallback string) string
	CodexTransform(body map[string]any, o openai.CodexOAuthTransformOptions) openai.CodexTransformResult
	CyberError() error
	CyberPolicy() bool
	Debug(msg string, fields ...zap.Field)
	EnsureInstructions(body map[string]any)
	ErrorMessage(body []byte) string
	FailoverHTTP(ctx context.Context, r *http.Response, body []byte, msg, model string) error
	HashForLog(s string) string
	InvalidAgentTask(status int, body []byte) bool
	IsAgentIdentity(ctx context.Context) bool
	MarkAgentRecovery(ctx context.Context) context.Context
	ReadUpstreamError(r *http.Response) ([]byte, string)
	RecoverAgentTask(ctx context.Context) error
	RedactErrorBody(ctx context.Context, body []byte) []byte
	ResolvedServiceTier(tier *string) *string
	PrepareTransport()
	Send(r *http.Request) (*http.Response, error)
	ServiceTier(body []byte) *string
	Sink() upstream.OutputSink
	ToolNameReverse(value map[string]string)
	TransportError(ctx context.Context, err error) error
	UpdateCodexUsage(ctx context.Context, h http.Header)
	UpstreamContext(ctx context.Context) (context.Context, context.CancelFunc)
	UpstreamModel(model string) string
	ValidateEffort(body []byte, model string) error
	ClientAllowed(ctx context.Context, body []byte) bool
	PolicyDenied()
	Reject(status int, kind, message string)
	ResponsesToChat(r *protocolopenai.ResponsesRequest) (*protocolopenai.ChatCompletionsRequest, error)
	GrokBridgeEligible(body []byte) (bool, string)
	ChatError(status int, kind, message string)
	DefaultChat() bool
	DeriveCacheKey(r *protocolopenai.ChatCompletionsRequest, model string) string
	IsolateCacheKey(key string) string
	NormalizeBodyTier(body []byte) ([]byte, string, error)
	ChatToResponses(r *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error)
	NormalizeRequestTier(r *protocolopenai.ResponsesRequest)
	ApplyChatFast(ctx context.Context, model string, body []byte) ([]byte, error)
	EffectiveEffort(body, original []byte, models ...string) *string
	ThinkingFallback(effort *string, body []byte, model string) *string
	AccessToken(ctx context.Context) (string, error)
	BuildChat(ctx context.Context, body []byte, token, key string) (*http.Request, error)
	UpstreamSessionKey(id int64, key string) string
	SessionUUID(key string) string
	ChatErrorResponse(r *http.Response, model string) (*Result, error)
	ChatResponseOptions(r *http.Response, original, billing, model string) openai.ChatResponseOptions
}
