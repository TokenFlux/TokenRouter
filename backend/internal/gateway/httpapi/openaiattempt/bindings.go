package openaiattempt

import (
	"context"
	"net/http"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ForwardPorts 是一个账号的一次平台执行，账号切换由 gateway/text 拥有。
type ForwardPorts struct {
	EnforceOpenAIClientPolicyForRequest func(ctx context.Context, c *gin.Context, account *gatewaycapture.ExecutionAccount, body []byte, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) error
	Forward                             func(ctx context.Context, c *gin.Context, account *gatewaycapture.ExecutionAccount, body []byte) (*forwardcore.OpenAIResult, error)
	ForwardAsAnthropic                  func(ctx context.Context, c *gin.Context, account *gatewaycapture.ExecutionAccount, body []byte, promptCacheKey, defaultMappedModel string, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*forwardcore.OpenAIResult, error)
	ForwardAsChatCompletions            func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		promptCacheKey string,
		defaultMappedModel string,
		tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
	) (*forwardcore.OpenAIResult, error)
	MatchOpenAITLSFingerprintRouterForRequest func(c *gin.Context, account *gatewaycapture.ExecutionAccount) egress.TLSFingerprintRouterMatchResult
	ReplaceModelInBody                        func(body []byte, newModel string) []byte
}

// SelectionPorts 使用同一调度与健康实例。
type SelectionPorts struct {
	SelectImages    func(context.Context, *int64, string, string, map[int64]struct{}, accountcore.OpenAIImagesCapability) (*gatewaycapture.SelectionResult, scheduler.PlatformDecision, error)
	RecordSwitch    func()
	ReportSelection func(*gatewaycapture.SelectionResult, int64, string, bool, *int)

	ObserveOpenAIAccountHealthFailure       func(ctx context.Context, account *gatewaycapture.ExecutionAccount, observedErr error) bool
	RecordOpenAIAccountSwitchForSelection   func(selection *gatewaycapture.SelectionResult)
	ReportOpenAIAccountScheduleResult       func(accountOrID *gatewaycapture.ExecutionAccount, model string, success bool, firstTokenMs *int, observedErr ...error) bool
	SelectAccountWithSchedulerForCapability func(
		ctx context.Context,
		groupID *int64,
		previousResponseID string,
		sessionHash string,
		requestedModel string,
		excludedIDs map[int64]struct{},
		requiredTransport egress.OpenAIUpstreamTransport,
		requiredCapability accountcore.OpenAIEndpointCapability,
		requireCompact bool,
		previousResponseCanMove bool,
		platformOverride ...string,
	) (*gatewaycapture.SelectionResult, scheduler.PlatformDecision, error)
	SelectAccountWithSchedulerForCapabilityAndRoutingModel func(
		ctx context.Context,
		groupID *int64,
		previousResponseID string,
		sessionHash string,
		requestedModel string,
		routingModel string,
		excludedIDs map[int64]struct{},
		requiredTransport egress.OpenAIUpstreamTransport,
		requiredCapability accountcore.OpenAIEndpointCapability,
		requireCompact bool,
		previousResponseCanMove bool,
		platformOverride ...string,
	) (*gatewaycapture.SelectionResult, scheduler.PlatformDecision, error)
	UpdateCodexUsageSnapshotFromHeaders func(ctx context.Context, accountID int64, headers http.Header)
}

// Bindings 在构造时注入固定端口，运行时不创建共享资源。
type Bindings struct {
	Forward           ForwardPorts
	Selection         SelectionPorts
	Support           *Support
	Recorder          *completion.Recorder
	Diagnoser         routing.ModelAvailabilityDiagnoser
	ResolvedDiagnoser routing.ModelAvailabilityDiagnoser
}
type openAIExecutionDependencies struct {
	recorder                            *completion.Recorder
	apiKeyService                       gatewaycapture.QuotaUpdater
	diagnoser                           routing.ModelAvailabilityDiagnoser
	resolvedDiagnoser                   routing.ModelAvailabilityDiagnoser
	enforceOpenAIClientPolicyForRequest func(ctx context.Context, c *gin.Context, account *gatewaycapture.ExecutionAccount, body []byte, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) error
	forward                             func(ctx context.Context, c *gin.Context, account *gatewaycapture.ExecutionAccount, body []byte) (*forwardcore.OpenAIResult, error)
	forwardAsAnthropic                  func(ctx context.Context, c *gin.Context, account *gatewaycapture.ExecutionAccount, body []byte, promptCacheKey, defaultMappedModel string, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*forwardcore.OpenAIResult, error)
	forwardAsChatCompletions            func(
		ctx context.Context,
		c *gin.Context,
		account *gatewaycapture.ExecutionAccount,
		body []byte,
		promptCacheKey string,
		defaultMappedModel string,
		tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
	) (*forwardcore.OpenAIResult, error)
	matchOpenAITLSFingerprintRouterForRequest func(c *gin.Context, account *gatewaycapture.ExecutionAccount) egress.TLSFingerprintRouterMatchResult
	observeOpenAIAccountHealthFailure         func(ctx context.Context, account *gatewaycapture.ExecutionAccount, observedErr error) bool
	recordOpenAIAccountSwitchForSelection     func(selection *gatewaycapture.SelectionResult)
	replaceModelInBody                        func(body []byte, newModel string) []byte
	reportOpenAIAccountScheduleResult         func(accountOrID *gatewaycapture.ExecutionAccount, model string, success bool, firstTokenMs *int, observedErr ...error) bool
	selectAccountWithSchedulerForCapability   func(
		ctx context.Context,
		groupID *int64,
		previousResponseID string,
		sessionHash string,
		requestedModel string,
		excludedIDs map[int64]struct{},
		requiredTransport egress.OpenAIUpstreamTransport,
		requiredCapability accountcore.OpenAIEndpointCapability,
		requireCompact bool,
		previousResponseCanMove bool,
		platformOverride ...string,
	) (*gatewaycapture.SelectionResult, scheduler.PlatformDecision, error)
	selectAccountWithSchedulerForCapabilityAndRoutingModel func(
		ctx context.Context,
		groupID *int64,
		previousResponseID string,
		sessionHash string,
		requestedModel string,
		routingModel string,
		excludedIDs map[int64]struct{},
		requiredTransport egress.OpenAIUpstreamTransport,
		requiredCapability accountcore.OpenAIEndpointCapability,
		requireCompact bool,
		previousResponseCanMove bool,
		platformOverride ...string,
	) (*gatewaycapture.SelectionResult, scheduler.PlatformDecision, error)
	updateCodexUsageSnapshotFromHeaders func(ctx context.Context, accountID int64, headers http.Header)
	acquireResponsesAccountSlot         func(
		c *gin.Context,
		groupID *int64,
		sessionHash string,
		selection *gatewaycapture.SelectionResult,
		reqStream bool,
		streamStarted *bool,
		reqLog *zap.Logger,
	) (func(), bool)
	anthropicStreamingAwareError   func(c *gin.Context, status int, errType, message string, streamStarted bool)
	deriveOpenAIForwardAttemptBody func(
		reqLog *zap.Logger,
		canonicalBody []byte,
		account *gatewaycapture.ExecutionAccount,
		state *openAIPassthroughFailoverState,
	) []byte
	ensureAnthropicErrorResponse         func(c *gin.Context, streamStarted bool) bool
	ensureOpenAIForwardErrorResponse     func(c *gin.Context, streamStarted bool, err error) bool
	ensureOpenAIStreamReadErrorResponse  func(c *gin.Context, err error, streamStarted bool) bool
	handleAnthropicFailoverExhausted     func(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, streamStarted bool)
	handleFailoverExhausted              func(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, streamStarted bool)
	handleFailoverExhaustedSimple        func(c *gin.Context, statusCode int, streamStarted bool)
	handleOpenAISelectionBusinessError   func(c *gin.Context, err error, streamStarted bool) bool
	handleStreamingAwareError            func(c *gin.Context, status int, errType, message string, streamStarted bool)
	recordCyberPolicyIfMarked            func(c *gin.Context, apiKey *apikey.APIKey, account *gatewaycapture.ExecutionAccount, subscription *billing.UserSubscription, model string, forwardErrored bool, cyberBlockArg []byte, channelFields routing.ChannelUsageFields, requestPayloadHash string, nativeCompaction ...bool) bool
	recordOpenAICyberWarning             func(c *gin.Context, reqLog *zap.Logger, apiKey *apikey.APIKey, account *gatewaycapture.ExecutionAccount, model string, statusCode int, responseBody []byte, warningText string)
	recordOpenAIForwardErrorCyberWarning func(c *gin.Context, reqLog *zap.Logger, apiKey *apikey.APIKey, account *gatewaycapture.ExecutionAccount, model string, statusCode int, err error) bool
	submitOpenAIUsageRecordTask          func(c *gin.Context, result *forwardcore.OpenAIResult, task completion.UsageRecordTask)
}

// New 不启动后台任务，Open 仅构造本请求的尝试状态。
func New(b Bindings) *Runtime {
	support := b.Support
	output := gatewayhttp.DefaultOpenAIErrorOutput()
	d := &openAIExecutionDependencies{recorder: b.Recorder, apiKeyService: support.Quota, diagnoser: b.Diagnoser, resolvedDiagnoser: b.ResolvedDiagnoser,
		enforceOpenAIClientPolicyForRequest:                    b.Forward.EnforceOpenAIClientPolicyForRequest,
		forward:                                                b.Forward.Forward,
		forwardAsAnthropic:                                     b.Forward.ForwardAsAnthropic,
		forwardAsChatCompletions:                               b.Forward.ForwardAsChatCompletions,
		matchOpenAITLSFingerprintRouterForRequest:              b.Forward.MatchOpenAITLSFingerprintRouterForRequest,
		replaceModelInBody:                                     b.Forward.ReplaceModelInBody,
		observeOpenAIAccountHealthFailure:                      b.Selection.ObserveOpenAIAccountHealthFailure,
		recordOpenAIAccountSwitchForSelection:                  b.Selection.RecordOpenAIAccountSwitchForSelection,
		reportOpenAIAccountScheduleResult:                      b.Selection.ReportOpenAIAccountScheduleResult,
		selectAccountWithSchedulerForCapability:                b.Selection.SelectAccountWithSchedulerForCapability,
		selectAccountWithSchedulerForCapabilityAndRoutingModel: b.Selection.SelectAccountWithSchedulerForCapabilityAndRoutingModel,
		updateCodexUsageSnapshotFromHeaders:                    b.Selection.UpdateCodexUsageSnapshotFromHeaders,
		acquireResponsesAccountSlot:                            support.AcquireResponsesAccountSlot,
		ensureAnthropicErrorResponse:                           support.EnsureAnthropicErrorResponse,
		ensureOpenAIStreamReadErrorResponse:                    support.EnsureOpenAIStreamReadErrorResponse,
		handleAnthropicFailoverExhausted:                       support.HandleAnthropicFailoverExhausted,
		handleFailoverExhausted:                                support.HandleFailoverExhausted,
		handleFailoverExhaustedSimple:                          support.HandleFailoverExhaustedSimple,
		handleOpenAISelectionBusinessError:                     support.HandleOpenAISelectionBusinessError,
		recordOpenAICyberWarning:                               support.RecordOpenAICyberWarning,
		recordOpenAIForwardErrorCyberWarning:                   support.RecordOpenAIForwardErrorCyberWarning,
		anthropicStreamingAwareError:                           output.WriteAnthropicStreamingError,
		ensureOpenAIForwardErrorResponse:                       output.EnsureResponse,
		handleStreamingAwareError:                              output.StreamError,
		deriveOpenAIForwardAttemptBody:                         deriveOpenAIForwardAttemptBody,
		recordCyberPolicyIfMarked: func(c *gin.Context, key *apikey.APIKey, account *gatewaycapture.ExecutionAccount, sub *billing.UserSubscription, model string, failed bool, body []byte, fields routing.ChannelUsageFields, hash string, compact ...bool) bool {
			return support.RecordCyberPolicyIfMarked(c, key, account, sub, model, failed, body, fields, hash, compact...)
		},
		submitOpenAIUsageRecordTask: func(c *gin.Context, result *forwardcore.OpenAIResult, task completion.UsageRecordTask) {
			images := 0
			if result != nil {
				images = result.ImageCount
			}
			support.Submission.SubmitImages(c, images, task)
		},
	}
	return &Runtime{dependencies: d}
}
func (b *responsesAttemptBridge) binding() *openAIExecutionDependencies { return b.fixed }
