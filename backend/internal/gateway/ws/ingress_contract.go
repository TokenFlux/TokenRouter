package ws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	protocol "github.com/TokenFlux/TokenRouter/internal/protocol"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// ClientPayload 是一轮经过入站规则规范化后的独立报文，不持有旧账号或 HTTP context。
type ClientPayload struct {
	PayloadRaw               []byte
	AccountIdentitySourceRaw []byte
	RawForHash               []byte
	PromptCacheKey           string
	PreviousResponseID       string
	OriginalModel            string
	RoutingModel             string
	ImageBillingModel        string
	ImageSizeTier            string
	ImageInputSize           string
	PayloadBytes             int
	RequestedReasoningEffort *string
}

// ForwardResult 保存一次 WS turn 的可观测结果和恢复输入。
type ForwardResult struct {
	VideoCount           int
	VideoResolution      string
	VideoDurationSeconds int
	WebSearchCalls       int
	SearchCount          int
	AudioUsage           *protocol.AudioUsage
	UpstreamWarning      *UpstreamWarning

	RequestID                    string
	ResponseID                   string
	UpstreamHeaders              map[string][]string
	Usage                        wire.ForwardUsage
	Model                        string
	BillingModel                 string
	UpstreamModel                string
	UpstreamResponseServiceTier  string
	UpstreamEndpoint             string
	ServiceTier                  *string
	ReasoningEffort              *string
	RequestedReasoningEffort     *string
	Stream                       bool
	OpenAIWSMode                 bool
	UpstreamTerminalEvent        string
	ResponseHeaders              map[string][]string
	ResponseTurnState            string
	Duration                     time.Duration
	FirstTokenMs                 *int
	ClientDisconnect             bool
	ImageCount                   int
	ImageSize                    string
	ImageInputSize               string
	ImageOutputSize              string
	ImageOutputSizes             []string
	ImageSizeSource              string
	ImageSizeBreakdown           map[string]int
	WSReplayInput                []json.RawMessage
	WSReplayInputExists          bool
	WSAccountFailoverReplayInput []json.RawMessage
}

// TurnCapture 固化当前 turn，完成处理只接收该快照。
type TurnCapture struct {
	Turn               int
	StartedAt          time.Time
	RequestBody        []byte
	OriginalModel      string
	PreviousResponseID string
	Result             *ForwardResult
	Err                error
	PayloadSource      string
}

// IngressHooks 由请求级准入拥有者注入，网关核心决定调用时机。
type IngressHooks struct {
	TurnStarted   func(int, time.Time)
	BeforeTurn    func(int) error
	BeforeRequest func(int, []byte, string, string) ([]byte, error)
	AfterTurn     func(TurnCapture)
}

// IngressState 是一条入站会话的明确状态；技术 Adapter 仅同步握手字段。
type IngressState struct {
	OriginalModel   string
	TurnState       string
	SessionHash     string
	PreferredConnID string
	StoreDisabled   bool
}

// ConnLease 仅暴露本次池租约的技术资源操作，不暴露平台客户端或凭据。
type ConnLease interface {
	ConnID() string
	MarkBroken()
	Release()
	SupportsIdlePingWithoutReader() bool
	PingWithTimeout(time.Duration) error
}

// PreviousTurn 是纯协议严格续接比较器，具体编解码仍由供应商原语提供。
type PreviousTurn interface {
	Keep([]byte, string, bool) (bool, string, error)
}

// ReplayCodec 接入唯一供应商 wire 原语；恢复决策与调用次序由网关拥有。
type ReplayCodec interface {
	Extract([]byte) ([]json.RawMessage, bool, error)
	BuildFromItems([]json.RawMessage, bool, []json.RawMessage, bool, bool) ([]json.RawMessage, bool)
	Build([]json.RawMessage, bool, []byte, bool) ([]json.RawMessage, bool, error)
	SetInput([]byte, []json.RawMessage, bool) ([]byte, error)
	RetryPayload([]byte, []json.RawMessage, bool, string) ([]byte, bool, error)
	Combine([]json.RawMessage, []json.RawMessage) []json.RawMessage
	HasOutput([]byte) bool
	ItemsHaveOutput([]json.RawMessage) bool
	ItemsCoverOutput([]json.RawMessage) bool
	DropPrevious([]byte) ([]byte, bool, error)
	SetPrevious([]byte, string) ([]byte, error)
	BuildStrict([]byte) (PreviousTurn, error)
	KeepPrevious([]byte, []byte, string, bool) (bool, string, error)
	StripItems([]json.RawMessage, map[string]struct{}) ([]json.RawMessage, int)
	ShouldInfer(bool, int, wire.ToolContinuationSignals, string, string) bool
	ClassifyPrevious(string) string
}

// IngressPort 只提供一个操作或一次上游 turn，不持有账号切换/会话重试循环。
type IngressPort interface {
	Parse([]byte, bool, int) (ClientPayload, error)
	ShouldBridge(ClientPayload) bool
	ReadClient() ([]byte, error)
	GenerateHash([]byte) string
	StoreDisabled([]byte) bool
	InvalidDigests(int64, string) map[string]struct{}
	StripInvalid([]byte, map[string]struct{}, string, int64, int) ([]byte, int)
	BridgeIdentity([]byte, string) (string, error)
	Bridge(context.Context, ClientPayload, []byte, string, int) (*ForwardResult, error)
	SetRequestState(string, string)
	OpenPool(ClientPayload) error
	Acquire(int, string, bool, bool) (ConnLease, error)
	RecoverAcquire(context.Context) error
	Relay(int, ConnLease, ClientPayload) (*ForwardResult, error)
	PinConn(int64, string) bool
	UnpinConn(int64, string)
	Header(string) string
	UpdateHeaders(ClientPayload, string)
	BindOwner(context.Context, string)
	IsDisconnect(error) bool
	IsFailover(error) bool
	CloseError(int, string, error) error
	Log(string)
	Debug(string)
	NormalizeLog(string) string
	TruncateLog(string, int) string
	SummarizeClose(error) (string, string)
	BindWarning(int64, int64, string, error)
}

// IngressOptions 保留每条入站会话原有的控制预算与亲和配置。
type IngressOptions struct {
	AccountType        string
	BridgeThreshold    int64
	AccountID          int64
	Platform           string
	GroupID            int64
	UseBridge          bool
	Debug              bool
	PreviousRecovery   bool
	StoreDisabledMode  string
	PreflightPingIdle  time.Duration
	HealthCheckTimeout time.Duration
	ResponseStickyTTL  time.Duration
	SessionStickyTTL   time.Duration
}

// IngressSession 拥有 bridge/ctx_pool 的唯一逐轮循环和恢复状态。
type IngressSession struct {
	State   *IngressState
	Store   session.OpenAIWSStateStore
	Options IngressOptions
	Hooks   *IngressHooks
	Codec   ReplayCodec
	Port    IngressPort
}

// UpstreamWarning 是原始风控观察投影，不包含用户或凭据。
type UpstreamWarning struct {
	StatusCode   int
	ResponseBody []byte
	Message      string
}

// AcquireRecoveryError 仅标记允许进行一次账号身份恢复的拨号失败。
type AcquireRecoveryError struct{ Err error }

func (e *AcquireRecoveryError) Error() string { return e.Err.Error() }
func (e *AcquireRecoveryError) Unwrap() error { return e.Err }
