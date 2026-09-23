// OpenAI 文本固定依赖只绑定单步能力，执行会话不保存完整旧 handler/service。
package handler

import (
	"context"
	"net/http"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

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

func newOpenAIExecutionDependencies(h *OpenAIGatewayHandler) *openAIExecutionDependencies {
	d := &openAIExecutionDependencies{}
	if h == nil {
		return d
	}
	if h.apiKeyService != nil {
		d.apiKeyService = h.apiKeyService
	}
	if h.completionRecorder != nil {
		d.recorder = h.completionRecorder
	} else if h.gatewayService != nil {
		d.recorder = h.completionRuntime()
	}
	d.diagnoser = h.gatewayService
	d.resolvedDiagnoser = openAIResolvedRoutingModelDiagnoser{service: h.gatewayService}
	d.acquireResponsesAccountSlot = h.acquireResponsesAccountSlot
	d.anthropicStreamingAwareError = gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError
	d.deriveOpenAIForwardAttemptBody = h.deriveOpenAIForwardAttemptBody
	d.ensureAnthropicErrorResponse = h.ensureAnthropicErrorResponse
	d.ensureOpenAIForwardErrorResponse = gatewayhttp.DefaultOpenAIErrorOutput().EnsureResponse
	d.ensureOpenAIStreamReadErrorResponse = h.ensureOpenAIStreamReadErrorResponse
	d.handleAnthropicFailoverExhausted = h.handleAnthropicFailoverExhausted
	d.handleFailoverExhausted = h.handleFailoverExhausted
	d.handleFailoverExhaustedSimple = h.handleFailoverExhaustedSimple
	d.handleOpenAISelectionBusinessError = h.handleOpenAISelectionBusinessError
	d.handleStreamingAwareError = gatewayhttp.DefaultOpenAIErrorOutput().StreamError
	d.recordCyberPolicyIfMarked = func(c *gin.Context, key *apikey.APIKey, account *gatewaycapture.ExecutionAccount, subscription *billing.UserSubscription, model string, failed bool, body []byte, fields routing.ChannelUsageFields, hash string, compact ...bool) bool {
		return h.recordCyberPolicyIfMarked(c, key, account, subscription, model, failed, body, fields, hash, compact...)
	}
	d.recordOpenAICyberWarning = h.recordOpenAICyberWarning
	d.recordOpenAIForwardErrorCyberWarning = h.recordOpenAIForwardErrorCyberWarning
	d.submitOpenAIUsageRecordTask = h.submitOpenAIUsageRecordTask
	if s := h.gatewayService; s != nil {
		d.enforceOpenAIClientPolicyForRequest = s.EnforceOpenAIClientPolicyForRequest
		d.forward = s.Forward
		d.forwardAsAnthropic = s.ForwardAsAnthropic
		d.forwardAsChatCompletions = s.ForwardAsChatCompletions
		d.matchOpenAITLSFingerprintRouterForRequest = s.MatchOpenAITLSFingerprintRouterForRequest
		d.observeOpenAIAccountHealthFailure = s.ObserveOpenAIAccountHealthFailure
		d.recordOpenAIAccountSwitchForSelection = s.RecordOpenAIAccountSwitchForSelection
		d.replaceModelInBody = s.ReplaceModelInBody
		d.reportOpenAIAccountScheduleResult = func(a *gatewaycapture.ExecutionAccount, model string, success bool, first *int, errs ...error) bool {
			return s.ReportOpenAIAccountScheduleResult(a, model, success, first, errs...)
		}
		d.selectAccountWithSchedulerForCapability = s.SelectAccountWithSchedulerForCapability
		d.selectAccountWithSchedulerForCapabilityAndRoutingModel = s.SelectAccountWithSchedulerForCapabilityAndRoutingModel
		d.updateCodexUsageSnapshotFromHeaders = s.UpdateCodexUsageSnapshotFromHeaders
	}
	return d
}

// binding 兼容独立旧测试，生产 OpenAI 会话在 Open 前已有固定依赖。
func (b *responsesAttemptBridge) binding() *openAIExecutionDependencies {
	if b.fixed == nil {
		b.fixed = newOpenAIExecutionDependencies(b.h)
	}
	return b.fixed
}
