// Messages 的生产执行器在构造时绑定单步依赖，不保存完整 GatewayHandler 或 GatewayService。
package handler

import (
	"context"
	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

type messageExecutionDependencies struct {
	geminiAvailable, antigravityAvailable bool
	forwardResponses                      func(
		ctx context.Context,
		c *gin.Context,
		account *service.Account,
		body []byte,
		parsed *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	forwardChat func(
		ctx context.Context,
		c *gin.Context,
		account *service.Account,
		body []byte,
		parsed *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	forwardAntigravityResponses func(
		ctx context.Context,
		c *gin.Context,
		account *service.Account,
		body []byte,
		_ *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	forwardAntigravityChat func(
		ctx context.Context,
		c *gin.Context,
		account *service.Account,
		body []byte,
		_ *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	forwardGeminiResponses func(
		ctx context.Context,
		c *gin.Context,
		account *service.Account,
		body []byte,
		_ *requeststate.ParsedRequest,
	) (*forwardcore.MessagesResult, error)
	forwardGeminiChat func(
		ctx context.Context,
		c *gin.Context,
		account *service.Account,
		body []byte,
	) (*forwardcore.MessagesResult, error)
	forwardGeminiNative              func(ctx context.Context, c *gin.Context, account *service.Account, originalModel string, action string, stream bool, body []byte) (*forwardcore.MessagesResult, error)
	saveGeminiSession                func(_ context.Context, groupID int64, prefixHash, digestChain, uuid string, accountID int64, oldDigestChain string) error
	replaceModel                     func(body []byte, newModel string) []byte
	responsesErrorResponse           func(c *gin.Context, status int, code, message string)
	chatCompletionsErrorResponse     func(c *gin.Context, status int, errType, message string)
	handleResponsesFailoverExhausted func(c *gin.Context, lastErr *forwardcore.UpstreamFailoverError, streamStarted bool)
	handleCCFailoverExhausted        func(c *gin.Context, lastErr *forwardcore.UpstreamFailoverError, streamStarted bool)
	handleGeminiFailoverExhausted    func(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError)

	prepareGatewayAttemptRequest      func(context.Context, *requeststate.ParsedRequest, []byte, *apikey.APIKey, string) (*requeststate.ParsedRequest, routing.ChannelMappingResult, error)
	selectAccount                     func(context.Context, *int64, string, string, map[int64]struct{}, string, int64) (*service.AccountSelectionResult, error)
	trackSession                      func(*scheduler.SessionAttempts, *service.Account, string)
	newSessionAttempts                func() *scheduler.SessionAttempts
	singleAccountGroup                func(context.Context, *int64) bool
	reportSchedule                    func(*service.AccountSelectionResult, int64, bool, *forwardcore.MessagesResult)
	incrementRPM                      func(context.Context, int64) error
	bindSticky                        func(context.Context, *int64, string, int64) error
	resolveGroup                      func(context.Context, int64) (*routing.Group, error)
	accountSwitched                   func(*service.AccountSelectionResult)
	tempUnschedule                    func(context.Context, int64, *forwardcore.UpstreamFailoverError)
	bedrockCompat                     func(*gin.Context, []byte, string, *service.Account, *int64) []byte
	forwardMessages                   func(context.Context, *gin.Context, *service.Account, *requeststate.ParsedRequest) (*forwardcore.MessagesResult, error)
	forwardAntigravity                func(context.Context, *gin.Context, *service.Account, []byte, bool) (*forwardcore.MessagesResult, error)
	forwardGemini                     func(context.Context, *gin.Context, *service.Account, []byte) (*forwardcore.MessagesResult, error)
	forwardAntigravityGemini          func(context.Context, *gin.Context, *service.Account, string, string, bool, []byte, bool, ...service.ForwardGeminiOption) (*forwardcore.MessagesResult, error)
	writeMappedClaudeError            func(*gin.Context, *service.Account, int, string, []byte) error
	billingCheck                      func(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error
	diagnoser                         routing.ModelAvailabilityDiagnoser
	apiKeyService                     service.APIKeyQuotaUpdater
	recorder                          *completion.Recorder
	concurrencyHelper                 *gatewayhttp.ConcurrencyHelper
	userMsgQueueHelper                *UserMsgQueueHelper
	messageWaitTimeout                time.Duration
	getUserMsgQueueMode               func(*service.Account, *requeststate.ParsedRequest) string
	errorResponse                     func(*gin.Context, int, string, string)
	handleStreamingAwareError         func(*gin.Context, int, string, string, bool)
	handleStreamingAwareErrorWithCode func(*gin.Context, int, string, string, string, bool)
	handleConcurrencyError            func(*gin.Context, error, string, bool)
	handleFailoverExhausted           func(*gin.Context, *forwardcore.UpstreamFailoverError, string, bool)
	handleFailoverExhaustedSimple     func(*gin.Context, int, bool)
	ensureForwardErrorResponse        func(*gin.Context, bool) bool
	submitUsageRecordTask             func(*gin.Context, completion.UsageRecordTask)
}

func newMessageExecutionDependencies(h *GatewayHandler) *messageExecutionDependencies {
	d := &messageExecutionDependencies{
		prepareGatewayAttemptRequest: h.prepareGatewayAttemptRequest,
		diagnoser:                    h.gatewayService,
		concurrencyHelper:            h.concurrencyHelper, userMsgQueueHelper: h.userMsgQueueHelper,
		getUserMsgQueueMode: h.getUserMsgQueueMode,
		errorResponse:       h.errorResponse, handleStreamingAwareError: h.handleStreamingAwareError,
		handleStreamingAwareErrorWithCode: h.handleStreamingAwareErrorWithCode, handleConcurrencyError: h.handleConcurrencyError,
		handleFailoverExhausted: h.handleFailoverExhausted, handleFailoverExhaustedSimple: h.handleFailoverExhaustedSimple,
		ensureForwardErrorResponse: h.ensureForwardErrorResponse, submitUsageRecordTask: h.submitUsageRecordTask,
	}
	if h.cfg != nil {
		d.messageWaitTimeout = h.cfg.Gateway.UserMessageQueue.WaitTimeout()
	}
	if h.apiKeyService != nil {
		d.apiKeyService = h.apiKeyService
	}
	if h.completionRecorder != nil {
		d.recorder = h.completionRecorder
	} else if h.gatewayService != nil {
		d.recorder = h.completionRuntime()
	}
	if s := h.gatewayService; s != nil {
		d.selectAccount = s.SelectAccountWithLoadAwareness
		d.trackSession = s.TrackSessionAttempt
		d.newSessionAttempts = s.NewSessionAttempts
		d.singleAccountGroup = s.IsSingleAntigravityAccountGroup
		d.reportSchedule = s.ReportAdvancedAccountScheduleResult
		d.incrementRPM = s.IncrementAccountRPM
		d.bindSticky = s.BindStickySession
		d.resolveGroup = s.ResolveGroupByID
		d.accountSwitched = s.RecordAdvancedAccountSwitch
		d.tempUnschedule = s.TempUnscheduleRetryableError
		d.bedrockCompat = s.ApplyBedrockCCCompat
		d.forwardMessages = s.Forward
	}
	if s := h.antigravityGatewayService; s != nil {
		d.forwardAntigravity = s.Forward
		d.forwardAntigravityGemini = s.ForwardGemini
		d.writeMappedClaudeError = s.WriteMappedClaudeError
	}
	if s := h.geminiCompatService; s != nil {
		d.forwardGemini = s.Forward
	}
	if s := h.billingCacheService; s != nil {
		d.billingCheck = s.CheckKey
	}
	if s := h.gatewayService; s != nil {
		d.forwardResponses = s.ForwardAsResponses
		d.forwardChat = s.ForwardAsChatCompletions
		d.saveGeminiSession = s.SaveGeminiSession
		d.replaceModel = s.ReplaceModelInBody
	}
	if s := h.antigravityGatewayService; s != nil {
		d.forwardAntigravityResponses = s.ForwardAsResponses
		d.forwardAntigravityChat = s.ForwardAsChatCompletions
	}
	if s := h.geminiCompatService; s != nil {
		d.forwardGeminiResponses = s.ForwardAsResponses
		d.forwardGeminiChat = s.ForwardAsChatCompletions
		d.forwardGeminiNative = s.ForwardNative
	}
	d.geminiAvailable = h.geminiCompatService != nil
	d.antigravityAvailable = h.antigravityGatewayService != nil
	d.responsesErrorResponse = h.responsesErrorResponse
	d.chatCompletionsErrorResponse = h.chatCompletionsErrorResponse
	d.handleResponsesFailoverExhausted = h.handleResponsesFailoverExhausted
	d.handleCCFailoverExhausted = h.handleCCFailoverExhausted
	d.handleGeminiFailoverExhausted = h.handleGeminiFailoverExhausted
	return d
}

// binding 仅为尚未改绑的其它入口维持旧兼容；固定 Messages 链在 Open 时已有构造期依赖。
func (b *messageAttemptBridge) binding() *messageExecutionDependencies {
	if b.fixed == nil {
		b.fixed = newMessageExecutionDependencies(b.h)
	}
	return b.fixed
}
