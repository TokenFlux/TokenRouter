// 文本 HTTP 单次尝试适配接收固定原生端口，不持有旧聚合 Handler。
package textattempt

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// ForwardPorts 是平台单次执行边界；账号切换循环仍唯一位于 gateway/text。
type ForwardPorts struct {
	AntigravityAvailable   bool
	BedrockCompat          func(*gin.Context, []byte, string, *gatewaycapture.ExecutionAccount, *int64) []byte
	ForwardAntigravity     func(context.Context, *gin.Context, *gatewaycapture.ExecutionAccount, []byte, bool) (*forwardcore.MessagesResult, error)
	ForwardAntigravityChat func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		_ *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	ForwardAntigravityGemini    func(context.Context, *gin.Context, *gatewaycapture.ExecutionAccount, string, string, bool, []byte, bool, ...forwardcore.GeminiSessionOption) (*forwardcore.MessagesResult, error)
	ForwardAntigravityResponses func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		_ *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	ForwardChat func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		parsed *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	ForwardGemini     func(context.Context, *gin.Context, *gatewaycapture.ExecutionAccount, []byte) (*forwardcore.MessagesResult, error)
	ForwardGeminiChat func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
	) (*forwardcore.MessagesResult, error)
	ForwardGeminiNative    func(ctx context.Context, c *gin.Context, account *gatewaycapture.ExecutionAccount, originalModel string, action string, stream bool, body []byte) (*forwardcore.MessagesResult, error)
	ForwardGeminiResponses func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		_ *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	ForwardMessages  func(context.Context, *gin.Context, *gatewaycapture.ExecutionAccount, *requeststate.ParsedRequest) (*forwardcore.MessagesResult, error)
	ForwardResponses func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		parsed *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	GeminiAvailable        bool
	ReplaceModel           func(body []byte, newModel string) []byte
	SaveGeminiSession      func(_ context.Context, groupID int64, prefixHash, digestChain, uuid string, accountID int64, oldDigestChain string) error
	WriteMappedClaudeError func(*gin.Context, *gatewaycapture.ExecutionAccount, int, string, []byte) error
}

// SelectionPorts 连接同一选择、会话及反馈拥有者，不创建第二份状态。
type SelectionPorts struct {
	AccountSwitched    func(*gatewaycapture.SelectionResult)
	BindSticky         func(context.Context, *int64, string, int64) error
	IncrementRPM       func(context.Context, int64) error
	NewSessionAttempts func() *scheduler.SessionAttempts
	ReportSchedule     func(*gatewaycapture.SelectionResult, int64, bool, *forwardcore.MessagesResult)
	ResolveGroup       func(context.Context, int64) (*routing.Group, error)
	SelectAccount      func(context.Context, *int64, string, string, map[int64]struct{}, string, int64) (*gatewaycapture.SelectionResult, error)
	SingleAccountGroup func(context.Context, *int64) bool
	TempUnschedule     func(context.Context, int64, *forwardcore.UpstreamFailoverError)
	TrackSession       func(*scheduler.SessionAttempts, *gatewaycapture.ExecutionAccount, string)
}

// Bindings 在构造期间固定依赖，Open 仅创建本请求和尝试的数据。
type Bindings struct {
	Forward      ForwardPorts
	Selection    SelectionPorts
	CheckFunding func(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error
	Diagnoser    routing.ModelAvailabilityDiagnoser
	Quota        gatewaycapture.QuotaUpdater
	Recorder     *completion.Recorder
	Concurrency  *gatewayhttp.ConcurrencyHelper
	Queue        *gatewayhttp.UserMsgQueueHelper
	QueueWait    time.Duration

	PlanRoute  func(context.Context, *apikey.APIKey, string) routing.RoutePlan
	QueueMode  string
	Errors     *errorpolicy.ErrorPassthroughService
	Submission gatewayhttp.CompletionSubmission
}
type messageExecutionDependencies struct {
	geminiAvailable, antigravityAvailable bool
	forwardResponses                      func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		parsed *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	forwardChat func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		parsed *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	forwardAntigravityResponses func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		_ *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	forwardAntigravityChat func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		_ *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	forwardGeminiResponses func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		_ *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	forwardGeminiChat func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
	) (*forwardcore.MessagesResult, error)
	forwardGeminiNative              func(ctx context.Context, c *gin.Context, account *gatewaycapture.ExecutionAccount, originalModel string, action string, stream bool, body []byte) (*forwardcore.MessagesResult, error)
	saveGeminiSession                func(_ context.Context, groupID int64, prefixHash, digestChain, uuid string, accountID int64, oldDigestChain string) error
	replaceModel                     func(body []byte, newModel string) []byte
	responsesErrorResponse           func(c *gin.Context, status int, code, message string)
	chatCompletionsErrorResponse     func(c *gin.Context, status int, errType, message string)
	handleResponsesFailoverExhausted func(c *gin.Context, lastErr *forwardcore.UpstreamFailoverError, streamStarted bool)
	handleCCFailoverExhausted        func(c *gin.Context, lastErr *forwardcore.UpstreamFailoverError, streamStarted bool)
	handleGeminiFailoverExhausted    func(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError)

	prepareGatewayAttemptRequest      func(context.Context, *requeststate.ParsedRequest, []byte, *apikey.APIKey, string) (*requeststate.ParsedRequest, routing.ChannelMappingResult, error)
	selectAccount                     func(context.Context, *int64, string, string, map[int64]struct{}, string, int64) (*gatewaycapture.SelectionResult, error)
	trackSession                      func(*scheduler.SessionAttempts, *gatewaycapture.ExecutionAccount, string)
	newSessionAttempts                func() *scheduler.SessionAttempts
	singleAccountGroup                func(context.Context, *int64) bool
	reportSchedule                    func(*gatewaycapture.SelectionResult, int64, bool, *forwardcore.MessagesResult)
	incrementRPM                      func(context.Context, int64) error
	bindSticky                        func(context.Context, *int64, string, int64) error
	resolveGroup                      func(context.Context, int64) (*routing.Group, error)
	accountSwitched                   func(*gatewaycapture.SelectionResult)
	tempUnschedule                    func(context.Context, int64, *forwardcore.UpstreamFailoverError)
	bedrockCompat                     func(*gin.Context, []byte, string, *gatewaycapture.ExecutionAccount, *int64) []byte
	forwardMessages                   func(context.Context, *gin.Context, *gatewaycapture.ExecutionAccount, *requeststate.ParsedRequest) (*forwardcore.MessagesResult, error)
	forwardAntigravity                func(context.Context, *gin.Context, *gatewaycapture.ExecutionAccount, []byte, bool) (*forwardcore.MessagesResult, error)
	forwardGemini                     func(context.Context, *gin.Context, *gatewaycapture.ExecutionAccount, []byte) (*forwardcore.MessagesResult, error)
	forwardAntigravityGemini          func(context.Context, *gin.Context, *gatewaycapture.ExecutionAccount, string, string, bool, []byte, bool, ...forwardcore.GeminiSessionOption) (*forwardcore.MessagesResult, error)
	writeMappedClaudeError            func(*gin.Context, *gatewaycapture.ExecutionAccount, int, string, []byte) error
	billingCheck                      func(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error
	diagnoser                         routing.ModelAvailabilityDiagnoser
	apiKeyService                     gatewaycapture.QuotaUpdater
	recorder                          *completion.Recorder
	concurrencyHelper                 *gatewayhttp.ConcurrencyHelper
	userMsgQueueHelper                *gatewayhttp.UserMsgQueueHelper
	messageWaitTimeout                time.Duration
	getUserMsgQueueMode               func(*gatewaycapture.ExecutionAccount, *requeststate.ParsedRequest) string
	errorResponse                     func(*gin.Context, int, string, string)
	handleStreamingAwareError         func(*gin.Context, int, string, string, bool)
	handleStreamingAwareErrorWithCode func(*gin.Context, int, string, string, string, bool)
	handleConcurrencyError            func(*gin.Context, error, string, bool)
	handleFailoverExhausted           func(*gin.Context, *forwardcore.UpstreamFailoverError, string, bool)
	handleFailoverExhaustedSimple     func(*gin.Context, int, bool)
	ensureForwardErrorResponse        func(*gin.Context, bool) bool
	submitUsageRecordTask             func(*gin.Context, completion.UsageRecordTask)
}

// New 不启动任务、不读取设置，所有原生资源由 app 持有。
func New(b Bindings) *Runtime {
	output := gatewayhttp.MessagesErrorOutput{Rules: b.Errors}
	d := &messageExecutionDependencies{
		antigravityAvailable:              b.Forward.AntigravityAvailable,
		bedrockCompat:                     b.Forward.BedrockCompat,
		forwardAntigravity:                b.Forward.ForwardAntigravity,
		forwardAntigravityChat:            b.Forward.ForwardAntigravityChat,
		forwardAntigravityGemini:          b.Forward.ForwardAntigravityGemini,
		forwardAntigravityResponses:       b.Forward.ForwardAntigravityResponses,
		forwardChat:                       b.Forward.ForwardChat,
		forwardGemini:                     b.Forward.ForwardGemini,
		forwardGeminiChat:                 b.Forward.ForwardGeminiChat,
		forwardGeminiNative:               b.Forward.ForwardGeminiNative,
		forwardGeminiResponses:            b.Forward.ForwardGeminiResponses,
		forwardMessages:                   b.Forward.ForwardMessages,
		forwardResponses:                  b.Forward.ForwardResponses,
		geminiAvailable:                   b.Forward.GeminiAvailable,
		replaceModel:                      b.Forward.ReplaceModel,
		saveGeminiSession:                 b.Forward.SaveGeminiSession,
		writeMappedClaudeError:            b.Forward.WriteMappedClaudeError,
		accountSwitched:                   b.Selection.AccountSwitched,
		bindSticky:                        b.Selection.BindSticky,
		incrementRPM:                      b.Selection.IncrementRPM,
		newSessionAttempts:                b.Selection.NewSessionAttempts,
		reportSchedule:                    b.Selection.ReportSchedule,
		resolveGroup:                      b.Selection.ResolveGroup,
		selectAccount:                     b.Selection.SelectAccount,
		singleAccountGroup:                b.Selection.SingleAccountGroup,
		tempUnschedule:                    b.Selection.TempUnschedule,
		trackSession:                      b.Selection.TrackSession,
		billingCheck:                      b.CheckFunding,
		diagnoser:                         b.Diagnoser,
		apiKeyService:                     b.Quota,
		recorder:                          b.Recorder,
		concurrencyHelper:                 b.Concurrency,
		userMsgQueueHelper:                b.Queue,
		messageWaitTimeout:                b.QueueWait,
		responsesErrorResponse:            output.ResponsesError,
		chatCompletionsErrorResponse:      output.ChatError,
		handleResponsesFailoverExhausted:  output.ResponsesExhausted,
		handleCCFailoverExhausted:         output.ChatExhausted,
		handleGeminiFailoverExhausted:     output.GeminiExhausted,
		errorResponse:                     output.Error,
		handleStreamingAwareError:         output.StreamError,
		handleStreamingAwareErrorWithCode: output.StreamErrorWithCode,
		handleConcurrencyError:            output.ConcurrencyError,
		handleFailoverExhausted:           output.Exhausted,
		handleFailoverExhaustedSimple:     output.ExhaustedStatus,
		ensureForwardErrorResponse:        output.EnsureResponse,
		submitUsageRecordTask:             b.Submission.Submit,
		prepareGatewayAttemptRequest: func(ctx context.Context, parsed *requeststate.ParsedRequest, body []byte, key *apikey.APIKey, model string) (*requeststate.ParsedRequest, routing.ChannelMappingResult, error) {
			return gatewayhttp.PrepareChannelAttempt(ctx, parsed, body, key, model, b.PlanRoute)
		},
		getUserMsgQueueMode: func(value *gatewaycapture.ExecutionAccount, parsed *requeststate.ParsedRequest) string {
			if b.Queue == nil || !value.View().IsAnthropicOAuthOrSetupToken() || !requeststate.IsRealUserMessage(parsed) {
				return ""
			}
			mode := gatewaycapture.ExecutionRuntimeConfig(value).GetUserMsgQueueMode()
			if mode == "" {
				mode = b.QueueMode
			}
			return mode
		},
	}
	return &Runtime{dependencies: d}
}

// 构造期绑定不可替换为每请求工厂，避免重新组装旧图。
func (b *messageAttemptBridge) binding() *messageExecutionDependencies { return b.fixed }
