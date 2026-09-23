// OpenAI 文本 HTTP 入口固定绑定用例端口，逐请求只保存显式投影与派生报文。
package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OpenAITextOptions 只投影 HTTP 限制和原账号切换上限，不携带配置对象。
type OpenAITextOptions struct {
	MaxBodyBytes             int64
	MaxSwitches              int
	CompactKeepaliveInterval time.Duration
	ForceCodexCLI            bool
}

// OpenAISessionInput 区分粘性、显式隔离和上游缓存键的读取目的。
type OpenAISessionInput uint8

const (
	OpenAISelectionSession OpenAISessionInput = iota
	OpenAIExplicitSession
	OpenAIPromptCacheSession
)

// OpenAITextCall 是 HTTP 准入成功后的请求独立输入；完成快照仍由执行端口同步冻结。
type OpenAITextCall struct {
	Route                                                          routing.RoutePlan
	Protocol                                                       protocol.ProtocolID
	Key                                                            *apikey.APIKey
	Subject                                                        authctx.AuthSubject
	Subscription                                                   *billing.UserSubscription
	Body, ForwardBody, SessionHashBody                             []byte
	Model, ForwardModel, SessionHash, PreviousResponseID, Platform string
	Stream, NativeCompactionV2, LegacyCompact, RequireCompact      bool
	StreamStarted                                                  *bool
	SelectionContext                                               context.Context
	Mapping                                                        routing.ChannelMappingResult
	RoutingStart                                                   time.Time
	RequiredCapability                                             account.OpenAIEndpointCapability
	AccountLayerModel, PromptCacheKey                              string
	Log                                                            *zap.Logger
}

// OpenAITextBackend 提供单步用例、状态投影和同步观测，不能接回旧完整 handler。
// 三条入口的读取、校验、审核、等待和循环调用顺序均由本 HTTP Adapter 决定。
type OpenAITextBackend interface {
	Access(*gin.Context) (*apikey.APIKey, bool)
	Dependencies(*gin.Context, *zap.Logger) bool
	ReadFailure(*zap.Logger, *http.Request, error)
	TransportHTTP(*gin.Context)
	StartCompact(*gin.Context, time.Duration) func()
	StopCompact(*gin.Context) bool
	ObserveRequest(*gin.Context, string, bool)
	ObserveEndpoint(*gin.Context, bool)
	Snapshot(*gin.Context, protocol.ProtocolID, []byte)
	Reasoning(*gin.Context, *apikey.APIKey, []byte) ([]byte, bool, error)
	MessageReasoning(*gin.Context, *apikey.APIKey, []byte)
	PolicyDenied(*gin.Context)
	NormalizeBootstrap([]byte, bool) ([]byte, bool)
	ValidateTier([]byte) error
	PreviousKind(string) string
	ValidateOwner(context.Context, int64, string, int64, int64) (bool, error)
	SetOwner(*gin.Context, int64, int64)
	Moderate(*gin.Context, *zap.Logger, *apikey.APIKey, authctx.AuthSubject, protocol.ProtocolID, string, []byte) *moderation.Decision
	Plan(context.Context, *apikey.APIKey, string) routing.RoutePlan
	BindPlan(*gin.Context, routing.RoutePlan)
	ImageIntent(string, []byte, routing.ChannelMappingResult, string) ([]byte, string, bool)
	ExplicitImageIntent(string, string, []byte) bool
	PassthroughContext(context.Context) context.Context
	ImageContext(context.Context) context.Context
	AllowsImages(*apikey.APIKey) bool
	FeatureDenied(*gin.Context)
	ImagePermissionMessage() string
	ImageSlot(*gin.Context, bool) (func(), bool)
	SeedImageIntent(*gin.Context, bool, bool)
	ValidateTools(*gin.Context, []byte, *zap.Logger) bool
	BindErrors(*gin.Context)
	Platform(*apikey.APIKey) string
	AuthLatency(*gin.Context, int64)
	UserSlot(*gin.Context, int64, int, bool, *bool, *zap.Logger) (func(), bool)
	Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error
	SessionHash(*gin.Context, OpenAISessionInput, []byte) string
	RejectCyber(*gin.Context, *apikey.APIKey, []byte, string, protocol.ProtocolID) bool
	Isolate(context.Context, *apikey.APIKey, int64, string, string) error
	GuardianContext(context.Context, *gin.Context, []byte, string) context.Context
	AllowsMessages(*apikey.APIKey) bool
	MessageAccountModel(context.Context, *apikey.APIKey, string) string
	MetadataSession(*gin.Context, string, string, string, []byte) (string, string)
	ChatImageModel(string, routing.ChannelMappingResult) bool
	ErrorMetadata(*gin.Context) (string, string)
	MarkStream(*gin.Context, string, string, int)
	MarkStreamFailure(*gin.Context, string, string, string, int)
	EnsureFallback(*gin.Context, bool) bool
}

// OpenAITextHandler 不创建工作任务、反馈或缓存，app 可将同一对象绑定三种协议路由。
type OpenAITextHandler struct {
	executor execution.Executor
	requestLifetime

	options OpenAITextOptions
	backend OpenAITextBackend
	prompt  MessagesPrompt
}

func NewOpenAITextHandler(options OpenAITextOptions, backend OpenAITextBackend, prompt MessagesPrompt, executor execution.Executor) *OpenAITextHandler {
	return &OpenAITextHandler{executor: executor, options: options, backend: backend, prompt: prompt}
}

// executeText 的入参全部来自已经完成的前置步骤，不追加读取或提前解释报文。
func (h *OpenAITextHandler) executeText(c *gin.Context, call OpenAITextCall, kind execution.TextKind) {
	request := execution.Request{
		Hints:       requeststate.ExecutionHintsFromContext(c.Request.Context()),
		Routing:     requeststate.RoutingStateFromContext(c.Request.Context()),
		Route:       call.Route,
		UserID:      call.Subject.UserID,
		Concurrency: call.Subject.Concurrency,
		Stream:      call.Stream,
		Body:        call.Body,
		Model:       call.Model,
		Funding: execution.FundingState{
			Key:          call.Key,
			Subscription: call.Subscription,
		},
		SessionHash: call.SessionHash,
		AttemptBody: call.ForwardBody,

		Text: execution.TextState{Kind: kind, Platform: call.Platform, SelectionContext: call.SelectionContext, Mapping: call.Mapping, SessionHashBody: call.SessionHashBody, ForwardModel: call.ForwardModel, PreviousResponseID: call.PreviousResponseID, AccountLayerModel: call.AccountLayerModel, PromptCacheKey: call.PromptCacheKey, NativeCompactionV2: call.NativeCompactionV2, LegacyCompact: call.LegacyCompact, RequireCompact: call.RequireCompact, RequiredCapability: call.RequiredCapability, RoutingStart: call.RoutingStart}}
	output := &MessagesOutput{ResponseSink: ResponseSink{Writer: c.Writer}, HTTP: c, Log: call.Log, StreamStarted: call.StreamStarted}
	_, _ = h.executor.Execute(c.Request.Context(), request, output)
}
