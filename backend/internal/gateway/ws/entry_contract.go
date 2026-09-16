package ws

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// EntryKey 是入站 WS 所需的已验证访问投影，不携带 Key 密文或完整身份对象。
type EntryKey struct {
	ID                int64
	UserID            int64
	GroupID           *int64
	ModelMapping      map[string]string
	FastModePolicy    string
	RefreshFastPolicy bool
	Group             *EntryGroup
}
type EntryGroup struct {
	Platform                    string
	MaxReasoningEffort          string
	MaxReasoningEffortOverLimit string
	ReasoningEffortMappings     []routing.ReasoningEffortMapping
	ImagesAllowed               bool
}
type EntrySubject struct {
	UserID      int64
	Concurrency int
}
type EntryInput struct {
	Key                    *EntryKey
	Subject                EntrySubject
	ClientLifecycleContext context.Context
	FirstTurnStartedAt     time.Time
	ClientIP               string
	UserAgent              string
	MaxAccountSwitches     int
}

// EntryAccount 保留无凭据的账号资格和展示投影。
type EntryAccount struct {
	account.AccountSnapshot
	Name   string
	Shadow bool
}
type EntrySelection struct {
	Account     *EntryAccount
	Acquired    bool
	ReleaseFunc func()
	WaitPlan    *scheduler.AccountWaitPlan
	Target      EntryTarget
}
type EntryDecision struct {
	Layer             string
	CandidateCount    int
	StickyPreviousHit bool
}
type EntryFailure struct {
	Err                   error
	StatusCode            int
	ReportScheduleFailure bool
	RetryNext             bool
}
type EntryClose struct {
	Status  int
	Reason  string
	Present bool
}
type EntryCyberSnapshot struct {
	Excerpt string
	Input   moderation.ContentModerationInput
}

// EntryHooks 使用核心 turn 值，与传输策略端口分离。
type EntryHooks struct {
	ClientLifecycleContext      context.Context
	InitialRequestModel         string
	InitialTurnStartedAt        time.Time
	MaxReasoningEffort          string
	MaxReasoningEffortOverLimit string
	ReasoningEffortMappings     []routing.ReasoningEffortMapping
	ResolveFastModePolicy       func(int) string
	ResolveRoutingModel         func(int, string, []byte) (string, error)
	TurnStarted                 func(int, time.Time)
	BeforeTurn                  func(int) error
	BeforeRequest               func(int, []byte, string, string) ([]byte, error)
	OnUpstreamError             func(int, string, int, []byte, string)
	AfterTurn                   func(TurnCapture)
}

// EntryTarget 是已选账号的受控单次执行能力，不向核心暴露凭据。
type EntryTarget interface {
	MappedModel(string) string
	Report(string, bool, *int)
	Switched()
	Stop429(int, int, *failover.OAuth429State) bool
	Credential(context.Context) error
	EnforceClient(context.Context, []byte) error
	ResolveRouting(context.Context, string, bool) (string, error)
	Warning(context.Context, string, int, []byte, string, EntryCyberSnapshot)
	RecordMarked(context.Context, string, bool, []byte, routing.ChannelUsageFields, string) bool
	UpdateUsage(context.Context, map[string][]string)
	PrepareCompletion(context.Context, *ForwardResult, TurnCapture, string, routing.ChannelMappingResult, []byte, bool) *completion.Input
	BeginPreemption(context.Context, []byte) (context.Context, func(), bool)
	Run(context.Context, ClientSocket, []byte, *EntryHooks) error
	LogFailure(error)
}

type EntryCompletion interface {
	Record(context.Context, *completion.Input, bool) error
}

// EntryPorts 提供单步能力，账号循环、每轮资源及完成时序由 RunEntry 拥有。
type EntryPorts interface {
	Logger() EntryLogger
	SetLogger(EntryLogger)
	ClassifyPrevious(string) string
	Platform() string
	Prompt(context.Context, []byte) []byte
	Redirect(context.Context, string) (context.Context, string)
	BindContext(context.Context)
	ObserveFirst(string)
	CaptureCyber([]byte) EntryCyberSnapshot
	Moderate(context.Context, string, []byte) *moderation.Decision
	ModerationError(context.Context, *moderation.Decision)
	BlockedSession(context.Context, []byte) string
	BlockedError(context.Context)
	BlockedMessage() string
	BlockedOps(string, string)
	Plan(context.Context, string) (context.Context, routing.ChannelMappingResult)
	ImageIntent(string, []byte, routing.ChannelMappingResult) ([]byte, string, bool)
	ImageContext(context.Context) context.Context
	ExplicitImage(string, []byte) bool
	ImagesAllowed() bool
	ImageDeniedMessage() string
	PolicyDenied()
	FeatureDenied()
	AcquireUser(context.Context) (func(), bool, error)
	AcquireAccount(context.Context, int64, int) (func(), bool, error)
	WrapRelease(context.Context, func()) func()
	Eligibility(context.Context) error
	LoadSubscription()
	SessionHash([]byte, string) string
	ExplicitHash([]byte) string
	Isolate(context.Context, string, string) error
	IsolationError(context.Context, error)
	IsolationReason(error) string
	Guardian(context.Context, []byte, string) context.Context
	Select(context.Context, string, string, string, map[int64]struct{}, bool, bool, string) (*EntrySelection, EntryDecision, error)
	SelectionLogError(error, string) error
	BindSticky(context.Context, string, int64) error
	RefreshFast(context.Context) (string, bool)
	Failover(error) (*EntryFailure, bool)
	CloseFailover(*EntryFailure)
	Close(int, string)
	CloseError(int, string, error) error
	CloseInfo(error) EntryClose
	SessionPreempted(error) bool
	RemovePrevious([]byte) []byte
	LocalPolicyError(error) bool
	ReportFailure(error) bool
	EndedByClient(error) bool
	ClearCyber()
	CompletionRecorder() EntryCompletion
	CompletionObserver() func(int64, string, error)
	SubmitCompletion(*ForwardResult, func(context.Context))
}

// EntryField 只允许日志值，不以任意对象携带业务实体。
type EntryField struct {
	Key     string
	Text    string
	Integer int64
	Flag    bool
	Err     error
	Kind    uint8
}

func EntryString(key, value string) EntryField { return EntryField{Key: key, Text: value, Kind: 1} }
func EntryInt(key string, value int) EntryField {
	return EntryField{Key: key, Integer: int64(value), Kind: 2}
}
func EntryInt64(key string, value int64) EntryField {
	return EntryField{Key: key, Integer: value, Kind: 2}
}
func EntryBool(key string, value bool) EntryField { return EntryField{Key: key, Flag: value, Kind: 3} }
func EntryError(err error) EntryField             { return EntryField{Key: "error", Err: err, Kind: 4} }

type EntryLogger interface {
	With(...EntryField) EntryLogger
	Info(string, ...EntryField)
	Warn(string, ...EntryField)
	Debug(string, ...EntryField)
	Error(string, ...EntryField)
}
