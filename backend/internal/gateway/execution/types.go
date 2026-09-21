// execution 保存跨入口执行契约，既有 gateway 导出名保持类型别名兼容。
package execution

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// Executor 固定依赖在构造时绑定；调用只传业务状态和同步输出。
type Executor interface {
	Execute(context.Context, Request, upstream.OutputSink) (ExecutionResult, error)
}
type TextKind uint8

const (
	TextMessages TextKind = iota
	TextGeminiMessages
	TextGenericResponses
	TextGenericChat
	TextNativeGemini
	TextOpenAIResponses
	TextOpenAIChat
	TextOpenAIMessages
)

// TextState 只包含前置步骤已确定的请求值，不接收 HTTP 对象或业务回调。
type TextState struct {
	AlternateBudget                                                     bool
	SelectionContext                                                    context.Context
	SelectionSessionHash                                                string
	Action                                                              string
	UseDigestFallback                                                   bool
	DigestChain, PrefixHash, SessionUUID, MatchedDigestChain            string
	SignatureState                                                      requeststate.GeminiSignatureState
	SessionHashBody                                                     []byte
	ForwardModel, PreviousResponseID, AccountLayerModel, PromptCacheKey string
	NativeCompactionV2, LegacyCompact, RequireCompact                   bool
	RequiredCapability                                                  account.OpenAIEndpointCapability
	RoutingStart                                                        time.Time
	Mapping                                                             routing.ChannelMappingResult

	Kind            TextKind
	Parsed          *requeststate.ParsedRequest
	Platform        string
	BoundAccountID  int64
	HasBoundSession bool
	GeminiBody      []byte
	GeminiModel     string
}
type Request struct {
	Hints       requeststate.ExecutionHints
	Routing     requeststate.RoutingState
	Access      *apikey.AccessSnapshot
	Route       routing.RoutePlan
	UserID      int64
	Concurrency int
	Stream      bool
	Body        []byte
	Model       string
	Metadata    RequestMetadata
	Funding     FundingState
	SessionHash string
	AttemptBody []byte

	Text TextState
}
type RequestMetadata struct {
	Headers          map[string][]string
	UserAgent        string
	ClientIP         string
	InboundEndpoint  string
	UpstreamEndpoint string
	QuotaPlatform    string
	ClaudeCode       bool
	StartedAt        time.Time
}
type FundingState struct {
	Key          *apikey.APIKey
	Subscription *billing.UserSubscription
}
type ExecutionResult struct {
	// PlanProvided 只在执行适配捕获到实际候选投影时为真。
	PlanProvided bool

	Attempt  upstream.AttemptResult
	Account  account.AccountSnapshot
	Plan     routing.CandidatePlan
	Attempts int
}
