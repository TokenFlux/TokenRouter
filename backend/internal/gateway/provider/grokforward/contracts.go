package grokforward

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	bridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// Input 仅带已选账号和本次请求投影，不携带凭据或旧业务实体。
type Input struct {
	AccountID                          int64
	AccountName, AccountType, Platform string
	OAuth, HTTPPresent, Compact        bool
	Body                               []byte
	OriginalModel                      string
	Stream                             bool
	StartedAt                          time.Time
}

// Options 复用唯一平台编解码器及应用的原生 attempt 生命周期屏障。
type Options struct {
	Codec       nativegrok.BodyCodec
	MaxLineSize int
	Enter       func() (func(), error)
}

const ComposerVisionModel = "grok-build-0.1"

// Result 保留旧同步结果的全部可观测字段，不另建完成任务或计费状态。
type Result struct {
	RequestID, ResponseID                                                             string
	UpstreamHeaders                                                                   http.Header
	Usage                                                                             wire.ForwardUsage
	Model, BillingModel, UpstreamModel, UpstreamResponseServiceTier, UpstreamEndpoint string
	ServiceTier, ReasoningEffort, RequestedReasoningEffort                            *string
	Stream, OpenAIWSMode                                                              bool
	UpstreamTerminalEvent                                                             string
	ResponseHeaders                                                                   http.Header
	Duration                                                                          time.Duration
	FirstTokenMs                                                                      *int
	ClientDisconnect                                                                  bool
	ImageCount                                                                        int
	ImageSize, ImageInputSize, ImageOutputSize, ImageSizeSource                       string
	ImageOutputSizes                                                                  []string
	ImageSizeBreakdown                                                                map[string]int
	UpstreamWarning                                                                   *Warning
	VideoCount                                                                        int
	VideoResolution                                                                   string
	VideoDurationSeconds, WebSearchCalls, SearchCount                                 int
	AudioUsage                                                                        *protocol.AudioUsage
}
type Warning struct {
	StatusCode   int
	ResponseBody []byte
	Message      string
}
type Decision struct{ Generic, Failover, RetrySameAccount bool }
type Retry struct {
	Retryable bool
	Delay     time.Duration
	Deadline  time.Time
	Max       int
}
type Failure struct {
	StatusCode                                     int
	ResponseBody                                   []byte
	ResponseHeaders                                http.Header
	RetryableOnSameAccount, RequestScopedTransient bool
	SameAccountRetryDelay                          time.Duration
	SameAccountRetryDeadline                       time.Time
	SameAccountRetryMax                            int
}
type Notice struct {
	AccountID                        int64
	Platform, AccountName            string
	UpstreamStatusCode               int
	UpstreamRequestID, Kind, Message string
}

// Ports 将账号健康和 HTTP 读取缩为单步能力；服务不通过单一回调保留整段编排。
type Ports interface {
	BillingModel(string) string
	UpstreamModel(string) string
	ImageModel(string) bool
	InvalidRequest(string, string)
	SetError(int, string, string)
	ClientTools(bridge.ResponsesClientToolMapping)
	CacheIdentity([]byte, string) string
	FreeCacheRoute([]byte, []byte, string) ([]byte, error)
	Credential(context.Context) error
	Detach(context.Context) (context.Context, func())
	ResolveProxy()
	Build(context.Context, []byte, string, bool) (*http.Request, error)
	Do(*http.Request) (*http.Response, error)
	ReadError(*http.Response) []byte
	Latency(int64)
	TransportError(context.Context, error) error
	ReplayNotice(bool)
	ErrorMessage([]byte) string
	Health(context.Context, int, http.Header, []byte, string, bool) Decision
	Observe(Notice)
	HandleError(context.Context, *http.Response, []byte, string) (*Result, error)
	ShouldMarkTeam(int, []byte) bool
	MarkTeam(string)
	RetryMetadata(int, []byte) Retry
	Failure(Failure) error
	ObserveSuccess(context.Context, http.Header, int, string)
	ReadStream(context.Context, *http.Response, time.Time, string, string) (upstream.ResponsesObservation, error)
	ReadNonStream(context.Context, *http.Response, string, string) (upstream.ResponsesObservation, error)
	Sink() upstream.OutputSink
	Effort([]byte, string) *string
	ReadBody(*http.Response) ([]byte, error)
	HasTokens(*wire.ForwardUsage) bool
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
