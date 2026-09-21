package ws

import (
	"context"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
)

// ClientSocket 把 HTTP WebSocket 限定为同步帧和关闭操作。
type ClientSocket interface {
	ClientConn
	Write(context.Context, int, []byte) error
}

// PolicyBlocked 保留客户端错误信息与原错误链，不引用旧策略实体。
type PolicyBlocked struct {
	Message string
	Cause   error
}

func (e *PolicyBlocked) Error() string { return e.Message }
func (e *PolicyBlocked) Unwrap() error { return e.Cause }

// DialResult 保存单次拨号观测，不向核心暴露底层客户端或认证参数。
type DialResult struct {
	PreparationError bool
	Conn             FrameConn
	Status           int
	Headers          map[string][]string
	Body             []byte
}

type PassthroughHooks struct {
	*IngressHooks
	InitialRequestModel  string
	InitialTurnStartedAt time.Time
	OnUpstreamError      func(int, string, int, []byte, string)
}

// PassthroughPort 的每个方法至多执行一次平台原语，重试和 turn 编排在核心。
type PassthroughPort interface {
	UsageDecoder
	IsLite([]byte) bool
	NormalizeLite([]byte) ([]byte, error)
	Reasoning([]byte, string) ([]byte, error)
	Models(int, string, []byte) (string, string, error)
	AliasTools([]byte) ([]byte, error)
	Compatibility([]byte, bool) ([]byte, bool, error)
	ScopeIdentity([]byte) ([]byte, bool, error)
	FastPolicy(context.Context, int, string, []byte, bool) ([]byte, *PolicyBlocked, error)
	PromptReplace(context.Context, []byte) []byte
	BlockedEvent(*PolicyBlocked) []byte
	PolicyDenied()
	SetUpstreamModel(string)
	PrepareDial(context.Context, []byte, string) error
	DialOnce(context.Context) (DialResult, error)
	CanRecover(context.Context, DialResult, error) bool
	Recover(context.Context) error
	DialFailure(context.Context, string, DialResult, error) error
	RunRelay(RelayInput) (RelayResult, *RelayExit)
	NormalizeCompleted([]byte) ([]byte, bool)
	RestoreTools([]byte) []byte
	EventType([]byte) string
	MayContainModel(string) bool
	ReplaceModel([]byte, string, string) []byte
	IsTerminal(string) bool
	NormalizeTerminal(string) string
	NormalizeTier(string) string
	Warning(string, []byte) *forwardcore.UpstreamWarning
	BeforeWrite(context.Context, string, []byte, bool, map[string][]string) error
	RelayClose(RelayExit, int) (int, string, bool)
	CloseError(int, string, error) error
	FirstOutputFailure(context.Context, Deadline, map[string][]string) error
	FirstOutputTimeout(string) time.Duration
	Log(string)
	Truncate(string, int) string
	TruncateReason(string, int) string
	ClosedError() error
}

// PassthroughOptions 只包含既有账号资格和本次会话的技术预算。
type PassthroughOptions struct {
	AccountID            int64
	OAuth                bool
	WriteTimeout         time.Duration
	IdleTimeout          time.Duration
	InterTurnIdleTimeout time.Duration
}

// PassthroughSession 拥有入站过滤、逐轮快照、relay 观察和错误收尾。
type PassthroughSession struct {
	Options PassthroughOptions
	Port    PassthroughPort
	Hooks   *PassthroughHooks
}
