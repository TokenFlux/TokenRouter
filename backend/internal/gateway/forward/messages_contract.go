package forward

import (
	"context"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// MessageInput 固化账号资格与静态选项，不携带凭据或完整账号。
type MessageInput struct {
	AccountPresent, HTTPPresent, OAuth, Passthrough, Bedrock bool
	AccountID                                                int64
	AccountName, AccountType, Platform                       string
	LogErrorBody, FailoverOn400                              bool
	LogErrorBodyMaxBytes                                     int
}
type PassthroughInput struct {
	Body                        []byte
	Parsed                      *requeststate.ParsedRequest
	RequestModel, OriginalModel string
	Stream                      bool
	StartedAt                   time.Time
}
type NormalizeOptions struct {
	StripSystemCacheControl, InjectMetadata bool
	MetadataUserID                          string
}
type Notice struct {
	UpstreamURL                                                     string
	Passthrough                                                     bool
	Platform, AccountName, UpstreamRequestID, Kind, Message, Detail string
	AccountID                                                       int64
	UpstreamStatusCode                                              int
}
type ExchangeResponse struct {
	StatusCode int
	Headers    map[string][]string
	RequestID  string
}
type StreamOutcome struct {
	Usage            *upstream.TokenUsage
	FirstTokenMs     *int
	ClientDisconnect bool
}
type MessageHooks struct {
	Before                 func(context.Context, *ExchangeResponse, []byte) (bool, error)
	Wire                   func([]byte)
	Accepted, BeforeStream func()
	Stream                 func(*StreamOutcome)
}
type MessageExecution struct {
	Model, OriginalModel string
	Stream, Mimic        bool
	StartedAt            time.Time
	Body                 []byte
	ReplaceBody          func([]byte) error
	Hooks                MessageHooks
}

// MessagePorts 是账号/设置/网络的单步能力，核心持有准备顺序和错误决定。
type MessagePorts interface {
	ShouldEmulate(context.Context, *int64, []byte) bool
	Emulate(context.Context, *requeststate.ParsedRequest) (*Result, error)
	MappedModel(string) string
	ReplaceModel([]byte, string) []byte
	Passthrough(context.Context, PassthroughInput) (*Result, error)
	Bedrock(context.Context, *requeststate.ParsedRequest, time.Time) (*Result, error)
	Begin() (func(), error)
	Beta(context.Context, string) error
	DebugOriginal([]byte, string, bool)
	AccountMappedModel(string) string
	IsClaudeCode(context.Context, []byte, string) bool
	SystemSettings(context.Context) (bool, string, string)
	RewriteSystem([]byte, *requeststate.ParsedRequest, string, string) []byte
	Metadata(context.Context, *requeststate.ParsedRequest) string
	NormalizeOAuth([]byte, string, NormalizeOptions) ([]byte, string)
	RewriteCache(context.Context, []byte) []byte
	RewriteTools([]byte) ([]byte, bool)
	BindTools()
	ToolsLast([]byte) []byte
	NormalizeDateline(context.Context, []byte) ([]byte, bool)
	CacheLimit([]byte) []byte
	PlatformModel(string) string
	InjectTTL(context.Context) bool
	CacheTTL([]byte) []byte
	Credential(context.Context) error
	Transport()
	FilterSearchHistory([]byte, string) []byte
	FilterThinking([]byte, string) []byte
	PassbackThinking(string) bool
	NormalizeThinking([]byte, string) ([]byte, bool)
	ReadErrorBody() ([]byte, error)
	ResetErrorBody([]byte)
	Health(context.Context, string, int, map[string][]string, []byte, string) ErrorDecision
	HandleError(context.Context, string, bool) (*Result, error)
	Observe(Notice)
	Failover400([]byte) bool
	FailoverError(int, []byte, bool) error
	IsFailover(error) bool
	Truncate(string, int) string
	TruncateBytes([]byte, int) string
	Sanitize(string) string
	Log(string)
	Execute(context.Context, MessageExecution) (upstream.AttemptResult, error)
	StreamError(error) (string, bool)
	Size() int
	Written() bool
	GenericError()
	ServiceTier() string
}

// ShouldRetry 保留 OAuth/Setup Token 仅重试 403 的既有资格。
func ShouldRetry(oauth bool, status int) bool { return oauth && status == 403 }

// ShouldFailover 保留旧可切换状态集合；是否实际切换仍由请求拥有者决定。
func ShouldFailover(status int) bool {
	switch status {
	case 401, 403, 429, 529:
		return true
	default:
		return status >= 500
	}
}

// RetryDelay 保留原指数退避，不新增等待或重试次数。
func RetryDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return 300 * time.Millisecond
	}
	delay := 300 * time.Millisecond * time.Duration(1<<(attempt-1))
	if delay > 3*time.Second {
		return 3 * time.Second
	}
	return delay
}

// PartialUsage 只包装已观测用量；可切换失败不带部分结果。
func PartialUsage(resp *ExchangeResponse, stream *StreamOutcome, model, upstreamModel string, start time.Time, speed string, failover bool) *Result {
	if stream == nil || !stream.Usage.HasObservedTokens() || failover {
		return nil
	}
	usage := *stream.Usage
	if strings.TrimSpace(usage.Speed) == "" && strings.EqualFold(strings.TrimSpace(speed), "fast") {
		usage.Speed = "fast"
	}
	requestID := ""
	if resp != nil {
		requestID = resp.RequestID
	}
	return &Result{
		RequestID:        requestID,
		UpstreamHeaders:  resp.Headers,
		Usage:            usage,
		Model:            model,
		UpstreamModel:    upstreamModel,
		Stream:           true,
		Duration:         time.Since(start),
		FirstTokenMs:     stream.FirstTokenMs,
		ClientDisconnect: stream.ClientDisconnect,
	}
}
