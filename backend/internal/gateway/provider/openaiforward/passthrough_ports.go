// 透传端口保留独立预算、头部策略和错误边界；只有当前请求的技术资源进入 Adapter。
package openaiforward

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type PassthroughPorts interface {
	ErrorMessage(body []byte) string
	InvalidAgentTask(status int, body []byte) bool
	IsAgentIdentity(ctx context.Context) bool
	ReadErrorBody(r *http.Response) []byte
	RecoverAgentTask(ctx context.Context) error
	ResolvedServiceTier(tier *string) *string
	ServiceTier(body []byte) *string
	Sink() upstream.OutputSink
	UpdateCodexUsage(ctx context.Context, h http.Header)
	UpstreamContext(ctx context.Context) (context.Context, context.CancelFunc)
	Profile() MessagesProfile
	CompactPath() bool
	CompactModel(model string) string
	InstructionsRejection(model string, body []byte) string
	PolicyDenied()
	LogInstructionsRejected(ctx context.Context, model, reason string, body []byte)
	Reject(status int, kind, message, param string)
	CodexModel(model string) bool
	OAuthBody(body []byte, compact bool) ([]byte, bool, error)
	AccountIdentityRaw(body []byte) ([]byte, bool, error)
	StageFingerprint(ids *openai.FingerprintIDs)
	Fingerprint() *openai.FingerprintIDs
	FingerprintBody(body []byte, ids *openai.FingerprintIDs) ([]byte, bool, error)
	HasContext() bool
	LiteHeader() bool
	LitePayloadFlag(body []byte) bool
	CompatibilityBody(body []byte, lite bool) ([]byte, bool, error)
	ReservedToolNames(body []byte) ([]byte, map[string]string, bool, error)
	MergeToolNames(value map[string]string)
	NeedsClientTools(body []byte) bool
	AdaptClientTools(body []byte) ([]byte, error)
	NormalizeLite(body []byte) ([]byte, bool, error)
	ApplyFastPass(ctx context.Context, model string, body []byte) ([]byte, error)
	ImageIntent(model string, canonical []byte, policy string, body []byte, invalidated bool) bool
	ExplicitImageIntent(model string, body []byte) bool
	ImageAllowed() bool
	FeatureDenied()
	ImagePermissionMessage() string
	ImageBilling(body []byte, model string) (ImageBilling, error)
	UpstreamError(status int, message string)
	Log(format string, args ...any)
	WarnTimeoutHeaders(ctx context.Context)
	AccessToken(ctx context.Context) (string, error)
	PrepareTransportPass()
	MarkPassthrough()
	RetryState(body []byte) *openai.ResponsesRejectedFieldRetryState
	UpstreamModelObserved(model string)
	BuildPass(ctx context.Context, body []byte, token string) (*http.Request, error)
	SendPass(r *http.Request) (*http.Response, error)
	Latency(d time.Duration)
	TransportErrorPass(ctx context.Context, err error) error
	CompactRetry(model string, body []byte, status int, message string, payload []byte, tried bool) ([]byte, string, bool)
	CompactObserved(r *http.Response, body []byte, message string)
	ErrorCode(body []byte) string
	ShouldFailover(status int, body []byte) bool
	FailoverError(ctx context.Context, r *http.Response, body, payload []byte) error
	ErrorResponsePass(ctx context.Context, r *http.Response, body, payload []byte) error
	WrapResponseBody(r *http.Response)
	ObserveProvenance(h http.Header)
	ResponseOptions(ctx context.Context) openai.PassthroughOptions
	CompactFromSignal(model string, body []byte, err error, tried bool, r *http.Response) ([]byte, string, bool)
	CompactSignal(err error) (CompactFailure, bool)
	CompactErrorResponse(r *http.Response, v CompactFailure) (*http.Response, []byte)
	BindOwner(ctx context.Context, id string)
	ObservedServiceTier() string
}
