// OpenAI 文本固定依赖只绑定单步能力，执行会话不保存完整旧 handler/service。
package handler

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type openAIExecutionDependencies struct {
	recorder                            *completion.Recorder
	apiKeyService                       service.APIKeyQuotaUpdater
	diagnoser                           service.ModelAvailabilityDiagnoser
	resolvedDiagnoser                   service.ModelAvailabilityDiagnoser
	enforceOpenAIClientPolicyForRequest func(ctx context.Context, c *gin.Context, account *service.Account, body []byte, tlsRouterMatch service.TLSFingerprintRouterMatchResult) error
	forward                             func(ctx context.Context, c *gin.Context, account *service.Account, body []byte) (*service.OpenAIForwardResult, error)
	forwardAsAnthropic                  func(ctx context.Context, c *gin.Context, account *service.Account, body []byte, promptCacheKey, defaultMappedModel string, tlsRouterMatch ...service.TLSFingerprintRouterMatchResult) (*service.OpenAIForwardResult, error)
	forwardAsChatCompletions            func(
		ctx context.Context,
		c *gin.Context,
		account *service.Account,
		body []byte,
		promptCacheKey string,
		defaultMappedModel string,
		tlsRouterMatch ...service.TLSFingerprintRouterMatchResult,
	) (*service.OpenAIForwardResult, error)
	matchOpenAITLSFingerprintRouterForRequest func(c *gin.Context, account *service.Account) service.TLSFingerprintRouterMatchResult
	observeOpenAIAccountHealthFailure         func(ctx context.Context, account *service.Account, observedErr error) bool
	recordOpenAIAccountSwitchForSelection     func(selection *service.AccountSelectionResult)
	replaceModelInBody                        func(body []byte, newModel string) []byte
	reportOpenAIAccountScheduleResult         func(accountOrID *service.Account, model string, success bool, firstTokenMs *int, observedErr ...error) bool
	selectAccountWithSchedulerForCapability   func(
		ctx context.Context,
		groupID *int64,
		previousResponseID string,
		sessionHash string,
		requestedModel string,
		excludedIDs map[int64]struct{},
		requiredTransport service.OpenAIUpstreamTransport,
		requiredCapability service.OpenAIEndpointCapability,
		requireCompact bool,
		previousResponseCanMove bool,
		platformOverride ...string,
	) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error)
	selectAccountWithSchedulerForCapabilityAndRoutingModel func(
		ctx context.Context,
		groupID *int64,
		previousResponseID string,
		sessionHash string,
		requestedModel string,
		routingModel string,
		excludedIDs map[int64]struct{},
		requiredTransport service.OpenAIUpstreamTransport,
		requiredCapability service.OpenAIEndpointCapability,
		requireCompact bool,
		previousResponseCanMove bool,
		platformOverride ...string,
	) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error)
	updateCodexUsageSnapshotFromHeaders func(ctx context.Context, accountID int64, headers http.Header)
	acquireResponsesAccountSlot         func(
		c *gin.Context,
		groupID *int64,
		sessionHash string,
		selection *service.AccountSelectionResult,
		reqStream bool,
		streamStarted *bool,
		reqLog *zap.Logger,
	) (func(), bool)
	anthropicStreamingAwareError   func(c *gin.Context, status int, errType, message string, streamStarted bool)
	deriveOpenAIForwardAttemptBody func(
		reqLog *zap.Logger,
		canonicalBody []byte,
		account *service.Account,
		state *openAIPassthroughFailoverState,
	) []byte
	ensureAnthropicErrorResponse         func(c *gin.Context, streamStarted bool) bool
	ensureOpenAIForwardErrorResponse     func(c *gin.Context, streamStarted bool, err error) bool
	ensureOpenAIStreamReadErrorResponse  func(c *gin.Context, err error, streamStarted bool) bool
	handleAnthropicFailoverExhausted     func(c *gin.Context, failoverErr *service.UpstreamFailoverError, streamStarted bool)
	handleFailoverExhausted              func(c *gin.Context, failoverErr *service.UpstreamFailoverError, streamStarted bool)
	handleFailoverExhaustedSimple        func(c *gin.Context, statusCode int, streamStarted bool)
	handleOpenAISelectionBusinessError   func(c *gin.Context, err error, streamStarted bool) bool
	handleStreamingAwareError            func(c *gin.Context, status int, errType, message string, streamStarted bool)
	recordCyberPolicyIfMarked            func(c *gin.Context, apiKey *service.APIKey, account *service.Account, subscription *service.UserSubscription, model string, forwardErrored bool, cyberBlockArg []byte, channelFields service.ChannelUsageFields, requestPayloadHash string, nativeCompaction ...bool) bool
	recordOpenAICyberWarning             func(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, account *service.Account, model string, statusCode int, responseBody []byte, warningText string)
	recordOpenAIForwardErrorCyberWarning func(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, account *service.Account, model string, statusCode int, err error) bool
	submitOpenAIUsageRecordTask          func(c *gin.Context, result *service.OpenAIForwardResult, task service.UsageRecordTask)
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
	d.anthropicStreamingAwareError = h.anthropicStreamingAwareError
	d.deriveOpenAIForwardAttemptBody = h.deriveOpenAIForwardAttemptBody
	d.ensureAnthropicErrorResponse = h.ensureAnthropicErrorResponse
	d.ensureOpenAIForwardErrorResponse = h.ensureOpenAIForwardErrorResponse
	d.ensureOpenAIStreamReadErrorResponse = h.ensureOpenAIStreamReadErrorResponse
	d.handleAnthropicFailoverExhausted = h.handleAnthropicFailoverExhausted
	d.handleFailoverExhausted = h.handleFailoverExhausted
	d.handleFailoverExhaustedSimple = h.handleFailoverExhaustedSimple
	d.handleOpenAISelectionBusinessError = h.handleOpenAISelectionBusinessError
	d.handleStreamingAwareError = h.handleStreamingAwareError
	d.recordCyberPolicyIfMarked = func(c *gin.Context, key *service.APIKey, account *service.Account, subscription *service.UserSubscription, model string, failed bool, body []byte, fields service.ChannelUsageFields, hash string, compact ...bool) bool {
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
		d.reportOpenAIAccountScheduleResult = func(a *service.Account, model string, success bool, first *int, errs ...error) bool {
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
