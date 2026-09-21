// 原生 Messages 执行端口只交换协议值、技术资源和明确的会话动作。
package openaiforward

import (
	"context"
	"net/http"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"go.uber.org/zap"
)

type TemplateData struct{ ExistingInstructions, OriginalModel, NormalizedModel, BillingModel, UpstreamModel string }
type MessagesProfile struct {
	Profile
	ID                                       int64
	GrokOAuth, Shadow, ContinuationSupported bool
}
type MessagesPorts interface {
	Prepare(context.Context) (MessagesProfile, Dispatch, error)
	Dispatch(context.Context, Dispatch, []byte, string, string) (*Result, error)
	ValidateEffort(body []byte, model string) error
	Error(status int, kind, message string)
	CloneDigest(r *protocolanthropic.AnthropicRequest) *protocolanthropic.AnthropicRequest
	NormalizeModel(r *protocolanthropic.AnthropicRequest)
	BillingModel(model, fallback string) string
	UpstreamModel(model string) string
	APIKeyID() int64
	ClaudeSession(body []byte) string
	MetadataSession(r *protocolanthropic.AnthropicRequest) string
	AutoCacheKey(model string) bool
	CacheControlKey(r *protocolanthropic.AnthropicRequest) string
	DigestChain(r *protocolanthropic.AnthropicRequest) string
	FindDigestKey(key int64, chain string) (string, string)
	DigestKey(chain string) string
	ContinuationEnabled(model string) bool
	ResponseID(ctx context.Context, key string) string
	ContinuationDisabled(ctx context.Context, key string) bool
	ReplayGuard(r *protocolanthropic.AnthropicRequest) bool
	Convert(r *protocolanthropic.AnthropicRequest) (*protocolopenai.ResponsesRequest, error)
	BetaFast() bool
	MessagesEffort(r *protocolanthropic.AnthropicRequest, model, effort string) string
	TrimLatestTurn(r *protocolopenai.ResponsesRequest)
	TodoGuard(r *protocolopenai.ResponsesRequest)
	HashForLog(s string) string
	Truncate(s string, limit int) string
	LogIDLimit() int
	Debug(msg string, fields ...zap.Field)
	Info(msg string, fields ...zap.Field)
	CodexTransform(body map[string]any, o openai.CodexOAuthTransformOptions) openai.CodexTransformResult
	ToolNameReverse(value map[string]string)
	ForcedTemplate() string
	ForcedInstructions(body map[string]any, text string, data TemplateData) (bool, error)
	EnsureInstructions(body map[string]any)
	TodoGuardBody(body map[string]any)
	AccountIdentity(body map[string]any, key int64)
	TurnState(ctx context.Context, key string) string
	ApplyEffort(ctx context.Context, body []byte) ([]byte, bool, error)
	ApplyFast(ctx context.Context, model string, body []byte) ([]byte, error)
	ServiceTier(body []byte) *string
	ResolvedServiceTier(tier *string) *string
	GrokCacheIdentity(body []byte, key, model string) string
	PatchGrokBody(body []byte, model string) ([]byte, error)
	ApplyGrokCache(body, intent []byte, key string, oauth bool) ([]byte, error)
	GrokFreeToolRoute(body, intent []byte, key string) ([]byte, error)
	Credential(ctx context.Context) (string, error)
	BindMessagesBridge(v bool)
	UpstreamContext(ctx context.Context) (context.Context, context.CancelFunc)
	Build(_ context.Context, ctx context.Context, body []byte, token string, stream bool, key, grokIdentity string) (*http.Request, error)
	IsolatedSessionID(key int64, cache string) string
	RestoreIdentity(h http.Header)
	Header(key string) string
	PrepareTransport()
	Send(r *http.Request) (*http.Response, error)
	TransportError(ctx context.Context, err error) error
	ReadErrorBody(r *http.Response) []byte
	GrokInvalidEncrypted(status int, body []byte) bool
	GrokHasEncrypted(body []byte) bool
	TrimGrokEncrypted(body []byte) ([]byte, bool, error)
	ReadUpstreamError(r *http.Response) ([]byte, string)
	AgentRecoveryTried(ctx context.Context) bool
	IsAgentIdentity(ctx context.Context) bool
	InvalidAgentTask(status int, body []byte) bool
	RecoverAgentTask(ctx context.Context) error
	MarkAgentRecovery(ctx context.Context) context.Context
	RedactErrorBody(ctx context.Context, body []byte) []byte
	ErrorMessage(body []byte) string
	PreviousMissing(status int, msg string, body []byte) bool
	PreviousUnsupported(status int, msg string, body []byte) bool
	DisableContinuation(ctx context.Context, key string)
	DeleteResponseID(ctx context.Context, key string)
	GrokStripRetried(ctx context.Context) bool
	StripThinkingSignatures(body []byte) ([]byte, bool)
	MarkGrokStrip(ctx context.Context) context.Context
	FailoverHTTP(ctx context.Context, r *http.Response, body []byte, msg, model string) error
	ErrorResponse(r *http.Response, model string) (*Result, error)
	UpdateGrokUsage(ctx context.Context, model string, h http.Header, status int)
	BindTurnState(ctx context.Context, key, state string)
	Sink() upstream.OutputSink
	ResponseOptions(r *http.Response, original, billing, model string) openai.MessagesResponseOptions
	CyberPolicy() bool
	CyberError() error
	BindResponseID(ctx context.Context, key, id string)
	BindDigestKey(id int64, chain, key, matched string)
	UpdateCodexUsage(ctx context.Context, h http.Header)
}
