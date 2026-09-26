// Responses 的重试与预算由 gateway/text 统一拥有，旧适配只投影既有能力。
package openaiattempt

import (
	"context"
	"errors"
	"net/http"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type responsesAttemptBridge struct {
	fixed                                                                    *openAIExecutionDependencies
	c                                                                        *gin.Context
	apiKey                                                                   *apikey.APIKey
	subject                                                                  authctx.AuthSubject
	subscription                                                             *billing.UserSubscription
	reqLog                                                                   *zap.Logger
	body, forwardBody, sessionHashBody                                       []byte
	reqModel, forwardModel, sessionHash, previousResponseID, requestPlatform string
	reqStream, nativeCompactionV2, legacyCompact, requireCompact             bool
	streamStarted                                                            *bool
	selectionCtx                                                             context.Context
	groupMapping                                                             routing.GroupMappingResult
	routingStart                                                             time.Time
	requiredCapability                                                       accountcore.OpenAIEndpointCapability
	selection                                                                *gatewaycapture.SelectionResult
	account                                                                  *gatewaycapture.ExecutionAccount
	accountReleaseFunc                                                       func()
	result                                                                   *forwardcore.OpenAIResult
	writerSizeBeforeForward                                                  int
	cyberPolicyHandled, wroteFallback                                        bool
	passthroughFailoverState                                                 openAIPassthroughFailoverState
	fields                                                                   []zap.Field
}

// Select 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) Select(excluded map[int64]struct{}) (textflow.ResponseSelection, error) {
	// Select account supporting the requested model
	b.reqLog.Debug("openai.account_selecting", zap.Int("excluded_account_count", len(excluded)))
	var scheduleDecision scheduler.PlatformDecision
	var err error
	b.selection, scheduleDecision, err = b.binding().selectAccountWithSchedulerForCapability(
		b.selectionCtx,
		b.apiKey.GroupID,
		b.previousResponseID,
		b.sessionHash,
		b.reqModel,
		excluded, egress.OpenAIUpstreamTransportAny, b.requiredCapability,
		b.requireCompact,
		false,
		b.requestPlatform,
	)
	if err != nil {
		return textflow.ResponseSelection{}, err
	}
	if b.selection == nil || b.selection.Account == nil {
		cls := ClassifyOpenAICompatibleNoAccountErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel)
		if !cls.ModelNotFound {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
		}
		b.binding().handleStreamingAwareError(b.c, cls.Status, cls.ErrType, cls.Message, *b.streamStarted)
		return textflow.ResponseSelection{}, nil
	}
	if b.previousResponseID != "" && b.selection != nil && b.selection.Account != nil {
		b.reqLog.Debug("openai.account_selected_with_previous_response_id", zap.Int64("account_id", b.selection.Account.Record.ID))
	}
	b.reqLog.Debug("openai.account_schedule_decision",
		zap.String("layer", scheduleDecision.Layer),
		zap.Bool("sticky_previous_hit", scheduleDecision.StickyPreviousHit),
		zap.Bool("sticky_session_hit", scheduleDecision.StickySessionHit),
		zap.Int("candidate_count", scheduleDecision.CandidateCount),
		zap.Int("top_k", scheduleDecision.TopK),
		zap.Int64("latency_ms", scheduleDecision.LatencyMs),
		zap.Float64("load_skew", scheduleDecision.LoadSkew),
	)
	b.account = b.selection.Account
	if b.previousResponseID != "" && b.requestPlatform == capability.PlatformOpenAI && !b.account.View().IsOpenAIApiKey() {
		// The public Responses HTTP API supports previous_response_id on API-key
		// accounts. OAuth/SetupToken upstreams do not, so keep searching instead
		// of silently deleting continuation state from a mixed account pool.

		if b.selection.ReleaseFunc != nil {
			b.selection.ReleaseFunc()
			b.selection.ReleaseFunc = nil
		}
		skipped := &forwardcore.UpstreamFailoverError{
			StatusCode:       http.StatusBadRequest,
			Stage:            forwardcore.GatewayFailureStageInference,
			Scope:            forwardcore.GatewayFailureScopeRequest,
			Reason:           forwardcore.OpenAIHTTPContinuationUnsupportedReason,
			ClientStatusCode: http.StatusBadRequest,
			ClientMessage:    "previous_response_id requires an OpenAI API-key account for HTTP requests",
		}
		b.reqLog.Debug("openai.account_skipped_http_continuation_unsupported",
			zap.Int64("account_id", b.account.Record.ID),
			zap.String("account_type", b.account.Record.Type),
		)
		picked := b.selectedView()
		picked.Skip = &textflow.AttemptFailure{Cause: skipped, Policy: skipped.RetryFailure()}
		return picked, nil
	}
	b.sessionHash = EnsureOpenAIPoolModeSessionHash(b.sessionHash, b.account)
	b.reqLog.Debug("openai.account_selected", zap.Int64("account_id", b.account.Record.ID), zap.String("account_name", b.account.Record.Name))
	gatewayhttp.SetOpsSelectedAccount(b.c, b.account.Record.ID, b.account.Record.Platform)

	return b.selectedView(), nil
}

// SelectionFailure 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) SelectionFailure(err error, excludedCount int, last *textflow.AttemptFailure) {
	var lastFailoverErr *forwardcore.UpstreamFailoverError
	if last != nil {
		errors.As(last.Cause, &lastFailoverErr)
	}

	if gatewayhttp.FailoverClientGone(b.c) {
		b.reqLog.Info("openai.account_select_aborted_client_disconnected", zap.Error(err))
		return
	}
	b.reqLog.Warn("openai.account_select_failed",
		zap.Error(gatewayhttp.OpenAICompatibleSelectionErrorForLog(err, b.requestPlatform)),
		zap.Int("excluded_account_count", excludedCount),
	)
	if excludedCount == 0 {
		if b.legacyCompact && errors.Is(err, scheduler.ErrNoAvailableCompactAccounts) {
			gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
			b.binding().handleStreamingAwareError(b.c, http.StatusServiceUnavailable, "compact_not_supported", "No available accounts support /responses/compact", *b.streamStarted)
			return
		}
		cls := ClassifyNoAccountErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, b.requestPlatform)
		cls = classifySelectionFailureError(err, cls)
		if !cls.ModelNotFound {
			gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
		}
		b.binding().handleStreamingAwareError(b.c, cls.Status, cls.ErrType, cls.Message, *b.streamStarted)
		return
	}
	if lastFailoverErr != nil {
		b.binding().handleFailoverExhausted(b.c, lastFailoverErr, *b.streamStarted)
	} else {
		b.binding().handleFailoverExhaustedSimple(b.c, 502, *b.streamStarted)
	}
}

// Acquire 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) Acquire() bool {
	var acquired bool
	b.accountReleaseFunc, acquired = b.binding().acquireResponsesAccountSlot(b.c, b.apiKey.GroupID, b.sessionHash, b.selection, b.reqStream, b.streamStarted, b.reqLog)
	return acquired
}

// Forward 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) Forward() textflow.ResponseOutcome {
	var err error
	// Forward request
	gatewayhttp.SetOpsLatencyMs(b.c, gatewayhttp.OpsRoutingLatencyMsKey, time.Since(b.routingStart).Milliseconds())
	forwardStart := time.Now()
	// 用扣除非语义心跳字节的口径快照：心跳注释不构成语义响应，
	// 不能因心跳字节变化而放弃 failover 换号（#3887）。
	b.writerSizeBeforeForward = gatewayhttp.OpenAICompactKeepaliveAdjustedWrittenSize(b.c)
	// 跨透传边界时，从不可变的 canonical 请求体派生当前尝试体，
	// 避免非透传上游拒绝透传账号产生的私有加密 reasoning 项。
	attemptBody := b.binding().deriveOpenAIForwardAttemptBody(b.reqLog, b.forwardBody, b.account, &b.passthroughFailoverState)
	b.result, err = func() (*forwardcore.OpenAIResult, error) {
		defer func() {
			if b.accountReleaseFunc != nil {
				b.accountReleaseFunc()
			}
		}()
		return b.binding().forward(b.c.Request.Context(), b.c, b.account, attemptBody)
	}()
	var cyberBlockBodyHTTP []byte
	if gatewayhttp.GetOpsCyberPolicy(b.c) != nil {
		cyberBlockBodyHTTP = b.sessionHashBody
	}
	b.cyberPolicyHandled = b.binding().recordCyberPolicyIfMarked(b.c, b.apiKey, b.account, b.subscription, b.reqModel, err != nil, cyberBlockBodyHTTP, gatewayhttp.ClientRequestedUsageFields(b.c, b.groupMapping, b.reqModel, ""), billing.HashUsageRequestPayload(b.body), b.nativeCompactionV2)
	forwardDurationMs := time.Since(forwardStart).Milliseconds()
	upstreamLatencyMs, _ := GetContextInt64(b.c, gatewayhttp.OpsUpstreamLatencyMsKey)
	responseLatencyMs := forwardDurationMs
	if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
		responseLatencyMs = forwardDurationMs - upstreamLatencyMs
	}
	gatewayhttp.SetOpsLatencyMs(b.c, gatewayhttp.OpsResponseLatencyMsKey, responseLatencyMs)
	if err == nil && b.result != nil && b.result.FirstTokenMs != nil {
		gatewayhttp.SetOpsLatencyMs(b.c, gatewayhttp.OpsTimeToFirstTokenMsKey, int64(*b.result.FirstTokenMs))
	}
	out := textflow.ResponseOutcome{Outcome: textflow.Outcome{Attempt: openAIObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil}, Images: b.result != nil && b.result.ImageCount > 0}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	var retry *forwardcore.UpstreamFailoverError
	if errors.As(err, &retry) {
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
		out.FirstOutputRecovery = retry.SafeToFailoverAfterWrite
	}
	return out
}

// Complete 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) Complete() {
	res := b.result

	if res == nil {
		return
	}
	gatewayhttp.StampOpenAIRequestedReasoningEffort(res, b.c)
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(b.c)
	requestPayloadHash := billing.HashUsageRequestPayload(b.body)
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(b.c)
	upstreamEndpoint := ResolveOpenAIUpstreamEndpoint(b.c, b.account, res)
	quotaPlatform := admission.QuotaPlatform(b.c.Request.Context(), b.apiKey)
	clientSessionID := gatewayhttp.ExtractClientSessionID(b.c)
	// 入队前固化资金与报文投影，worker 不再读取请求中的实体。
	completionInput := gatewaycapture.CaptureOpenAI(gatewayhttp.CompletionContext(b.c), &gatewaycapture.OpenAICapture{
		Result:             res,
		APIKey:             b.apiKey,
		User:               b.apiKey.User,
		Account:            gatewaycapture.ExecutionCompletionRecord(b.account),
		Subscription:       b.subscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		RequestPayloadHash: requestPayloadHash,
		RequestBody:        b.body,
		APIKeyService:      b.binding().apiKeyService,
		QuotaPlatform:      quotaPlatform,
		ClientSessionID:    clientSessionID,
		PricingUsageFields: b.groupMapping.ToUsageFields(b.reqModel, res.UpstreamModel),
		CyberBlocked:       b.cyberPolicyHandled,
		NativeCompactionV2: b.nativeCompactionV2,
	})
	completionUserID := b.subject.UserID
	completionModel := b.reqModel
	completionRuntime := b.binding().recorder
	b.binding().submitOpenAIUsageRecordTask(b.c, res, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, true); err != nil {
			logging.L().With(
				zap.String("component", "handler.openai_gateway.responses"),
				zap.Int64("user_id", completionUserID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Any("group_id", completionInput.APIKey.GroupID),
				zap.String("model", completionModel),
				zap.Int64("account_id", completionInput.Account.ID),
			).Error("openai.record_usage_failed", zap.Error(err))
		}
	})
}

// PartialImages 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) PartialImages(err error) {
	b.reqLog.Warn("openai.forward_partial_error_with_image_result",
		zap.Int64("account_id", b.account.Record.ID),
		zap.Int("image_count", b.result.ImageCount),
		zap.Error(err),
	)
}

// RetryReady 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) RetryReady(failure *textflow.AttemptFailure) bool {
	err := failure.Cause
	var failoverErr *forwardcore.UpstreamFailoverError
	if !errors.As(err, &failoverErr) {
		return false
	}
	if gatewayhttp.FailoverClientGone(b.c) {
		b.reqLog.Info("openai.failover_aborted_client_disconnected",
			zap.Int64("account_id", b.account.Record.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
		)
		return false
	}
	b.binding().recordOpenAICyberWarning(b.c, b.reqLog, b.apiKey, b.account, b.reqModel, failoverErr.StatusCode, failoverErr.ResponseBody, err.Error())
	if !OpenAIForwardMayFailover(b.c, b.writerSizeBeforeForward, failoverErr) {
		b.binding().observeOpenAIAccountHealthFailure(b.c.Request.Context(), b.account, err)
		b.binding().handleFailoverExhausted(b.c, failoverErr, true)
		return false
	}
	// OpenAIForwardMayFailover 已确认写出的字节不含语义输出，
	// 但重试耗尽时仍须按已提交的 SSE 响应返回流内错误。
	if b.c.Writer.Written() {
		*b.streamStarted = true
	}
	if failoverErr.ShouldReportAccountScheduleFailure() {
		b.binding().reportOpenAIAccountScheduleResult(b.account, OpenAIAccountScheduleModel(b.c, b.account, b.forwardModel, b.requireCompact, nil), false, nil, err)
	}
	return true
}

// RetryWait 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) RetryWait(failure *textflow.AttemptFailure, retryLimit, retryCount int, retryDelay time.Duration) {
	var failoverErr *forwardcore.UpstreamFailoverError
	errors.As(failure.Cause, &failoverErr)
	b.reqLog.Warn("openai.pool_mode_same_account_retry",
		zap.Int64("account_id", b.account.Record.ID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("retry_limit", retryLimit),
		zap.Int("retry_count", retryCount),
		zap.Duration("retry_delay", retryDelay),
	)
}

// Switching 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) Switching(failure *textflow.AttemptFailure, switchCount, maxAccountSwitches int) {
	var failoverErr *forwardcore.UpstreamFailoverError
	errors.As(failure.Cause, &failoverErr)
	failoverSwitchFields := []zap.Field{
		zap.Int64("account_id", b.account.Record.ID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("switch_count", switchCount),
		zap.Int("max_switches", maxAccountSwitches),
	}
	failoverSwitchFields = AppendOpenAIAccountProxyLogFields(failoverSwitchFields, b.account)
	b.reqLog.Warn("openai.upstream_failover_switching", failoverSwitchFields...)
}

// OtherFailure 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) OtherFailure(err error) {
	statusCode := 0
	if v, ok := GetContextInt64(b.c, gatewayhttp.OpsUpstreamStatusCodeKey); ok {
		statusCode = int(v)
	}
	recordedWarning := b.binding().recordOpenAIForwardErrorCyberWarning(b.c, b.reqLog, b.apiKey, b.account, b.reqModel, statusCode, err)
	if !recordedWarning {
		b.binding().recordOpenAICyberWarning(b.c, b.reqLog, b.apiKey, b.account, b.reqModel, statusCode, nil, err.Error())
	}
	b.binding().reportOpenAIAccountScheduleResult(b.account, OpenAIAccountScheduleModel(b.c, b.account, b.forwardModel, b.requireCompact, b.result), false, nil, err)
	upstreamErrorAlreadyCommunicated := gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	b.wroteFallback = false
	// cyber warning 场景下，service 层可能已经把上游 response.failed/JSON 错误写给下游。
	// 此时不再补写第二个 fallback，避免客户端看到重复的终止事件。
	if !upstreamErrorAlreadyCommunicated && (!recordedWarning || gatewayhttp.OpenAICompactKeepaliveAdjustedWrittenSize(b.c) == b.writerSizeBeforeForward) {
		b.wroteFallback = b.binding().ensureOpenAIForwardErrorResponse(b.c, *b.streamStarted, err)
	}
	b.fields = []zap.Field{
		zap.Int64("account_id", b.account.Record.ID),
		zap.Bool("fallback_error_response_written", b.wroteFallback),
		zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
		zap.Error(err),
	}
}

// Failed 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) Failed() {
	if gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(b.c, b.wroteFallback) {
		b.reqLog.Warn("openai.forward_failed", b.fields...)
		return
	}
	b.reqLog.Error("openai.forward_failed", b.fields...)
}

// Success 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) Success() {
	if b.result != nil {
		// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
		if b.account.Record.Type == capability.AccountTypeOAuth && !b.account.View().IsShadow() {
			b.binding().updateCodexUsageSnapshotFromHeaders(b.c.Request.Context(), b.account.Record.ID, b.result.ResponseHeaders)
		}
		b.binding().reportOpenAIAccountScheduleResult(b.account, OpenAIAccountScheduleModel(b.c, b.account, b.forwardModel, b.requireCompact, b.result), b.result.SucceededForScheduling(), b.result.FirstTokenMs)
	} else {
		b.binding().reportOpenAIAccountScheduleResult(b.account, OpenAIAccountScheduleModel(b.c, b.account, b.forwardModel, b.requireCompact, b.result), b.result.SucceededForScheduling(), nil)
	}
}

// Completed 只执行单次 Responses 适配操作，不持有重试循环。
func (b *responsesAttemptBridge) Completed(switchCount int) {
	b.reqLog.Debug("openai.request_completed",
		zap.Int64("account_id", b.account.Record.ID),
		zap.Int("switch_count", switchCount),
	)
}

func (b *responsesAttemptBridge) Context() context.Context { return b.c.Request.Context() }
func (b *responsesAttemptBridge) CanAttempt() bool         { return OpenAIRequestAllowsFailoverReplay(b.c) }

func (b *responsesAttemptBridge) selectedView() textflow.ResponseSelection {
	return textflow.ResponseSelection{Selection: gatewaycapture.CaptureTextSelection(b.account), Available: true, OAuth: failover.OAuth429Account{OpenAI: b.account.View().IsOpenAIOAuthLike(), Grok: b.account.Record.Platform == capability.PlatformGrok && b.account.Record.Type == capability.AccountTypeOAuth}}
}

func (b *responsesAttemptBridge) Exhausted(failure *textflow.AttemptFailure) {
	var original *forwardcore.UpstreamFailoverError
	if failure != nil && errors.As(failure.Cause, &original) {
		b.binding().handleFailoverExhausted(b.c, original, *b.streamStarted)
	} else {
		b.binding().handleFailoverExhaustedSimple(b.c, 502, *b.streamStarted)
	}
}

func (b *responsesAttemptBridge) Switched() {
	b.binding().recordOpenAIAccountSwitchForSelection(b.selection)
}
