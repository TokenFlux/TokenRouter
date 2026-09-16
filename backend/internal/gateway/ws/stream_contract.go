package ws

import (
	"context"
	"encoding/json"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// StreamLease 是已经取得的上游连接租约，核心只执行当前 turn 的同步帧操作。
type StreamLease interface {
	ConnLease
	WriteRequest(context.Context, []byte, time.Duration) error
	ReadEvent(context.Context, time.Duration) ([]byte, error)
	Headers() map[string][]string
}

// ErrorPolicy 保存账号错误规则给出的动作，不携带账号实体。
type ErrorPolicy struct {
	Generic   bool
	Failover  bool
	RetrySame bool
}
type TerminalPolicy struct {
	TerminalEvent string
	StatusCode    int
	Decision      ErrorPolicy
}
type StreamOptions struct {
	AccountID        int64
	WriteTimeout     time.Duration
	ReadTimeout      time.Duration
	PreviousRecovery bool
	Debug            bool
}
type StreamHooks struct {
	OnUpstreamError func(int, string, int, []byte, string)
}
type ImageCounter interface {
	AddSSEData([]byte)
	Count() int
	Sizes() []string
}
type ReplayCollector interface {
	AddEvent(string, []byte)
	Items() []json.RawMessage
}

// StreamPort 只提供单次 wire/账号规则投影和输出；帧循环与重试窗口由 RelayTurn 控制。
type StreamPort interface {
	BeginObservation()
	ObserveModel([]byte, string)
	ResponseTier() string
	ResolvedTier([]byte) *string
	Reasoning([]byte, string, string) *string
	ImageCounter() ImageCounter
	ReplayCollector() ReplayCollector
	Streaming([]byte) bool
	StoreDisabled([]byte) bool
	ClassifyPrevious(string) string
	HasToolOutput([]byte) bool
	MappedModel(string) string
	NormalizeCompleted([]byte) ([]byte, bool)
	Envelope([]byte) (string, string, bool)
	ShouldParseUsage(string) bool
	ParseUsage([]byte, *wire.ForwardUsage)
	MarkCyber([]byte, *wire.ForwardUsage)
	SchedulingModel(string) string
	ErrorDecision(context.Context, string, map[string][]string, []byte) ErrorPolicy
	TerminalDecision(context.Context, string, map[string][]string, []byte) TerminalPolicy
	ErrorFields([]byte) (string, string, string)
	ErrorStatus([]byte) int
	ClassifyError(string, string, string) (string, bool)
	EncryptedDigests([]byte) []string
	MarkEncrypted([]string)
	SummarizeError(string, string, string) (string, string, string)
	GenericError(int) error
	RawFailure(int, map[string][]string, []byte, bool) error
	Failure(int, map[string][]string, []byte, string, bool) error
	Warning(string, []byte) *UpstreamWarning
	IsToken(string) bool
	IsTerminal(string) bool
	NormalizeTerminal(string) string
	Message([]byte) string
	GenericEvent() []byte
	MayContainModel(string) bool
	ReplaceModel([]byte, string, string) []byte
	MayContainTools(string) bool
	LikelyTools([]byte) bool
	CorrectTools([]byte) ([]byte, bool)
	CapacityShed([]byte) ([]byte, bool)
	WriteClient([]byte) error
	IsDisconnect(error) bool
	SummarizeClose(error) (string, string)
	Truncate(string, int) string
	NormalizeLog(string) string
	Log(string)
	Debug(string)
}
