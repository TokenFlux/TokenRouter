package forward

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// GeminiInput 只带当前请求与账号显示/资格投影，不带凭据。
type GeminiInput struct {
	StartedAt                                  time.Time
	Model, Action                              string
	Stream, Sticky, TokenAvailable             bool
	Body                                       []byte
	GroupID, AccountID                         int64
	SessionHash, AccountName, Platform, Prefix string
}
type GeminiRecovery struct {
	ErrorBody   []byte
	ContentType string
}
type GeminiExecution struct {
	Model, OriginalModel                   string
	StartedAt                              time.Time
	Stream, Sticky                         bool
	Body, InjectedBody                     []byte
	ProjectID, UpstreamAction, SessionHash string
	GroupID                                int64
}
type GeminiHooks struct {
	Exchange    func() error
	Before      func(context.Context, *ExchangeResponse) (bool, error)
	OutputError func(error)
}

// GeminiPorts 的恢复端口复用唯一供应商原语；核心不再创建重试算法。
type GeminiPorts interface {
	GoogleError(int, string) error
	ImageInputSize([]byte) string
	ImageTier(string) string
	ZeroCount()
	MappedModel(string) string
	FeatureDenied()
	Credential(context.Context) error
	ProjectID() (string, error)
	Transport()
	InjectIdentity([]byte) ([]byte, error)
	CleanSchema([]byte) ([]byte, error)
	Wrap(string, string, []byte) ([]byte, error)
	ProjectRequired(error) bool
	Log(string)
	StdLog(string)
	Retry(context.Context, GeminiExecution) error
	SwitchError(error) (bool, bool)
	Failover(int, []byte, bool, bool) error
	ClientCanceled() bool
	Recover(context.Context, GeminiExecution) (GeminiRecovery, error)
	RequestID(string)
	Unwrap([]byte) ([]byte, error)
	Health(context.Context, int, map[string][]string, []byte, GeminiExecution)
	ErrorMessage([]byte) string
	Sanitize(string) string
	Detail([]byte) string
	SetError(int, string, string)
	GoogleConfigError(string) bool
	Observe(Notice)
	ShouldFailover(int) bool
	TruncateBytes([]byte, int) string
	ErrorBody(int, string, []byte)
	Execute(context.Context, GeminiExecution, GeminiHooks) (upstream.AttemptResult, error)
	IsImageModel(string) bool
}
