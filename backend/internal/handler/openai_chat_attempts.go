package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"go.uber.org/zap"
)

// openAIChatAttemptBridge 持有本请求端口，不复制上游池或完成状态。
type openAIChatAttemptBridge struct {
	responsesAttemptBridge
	promptCacheKey string
}

// Select 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) Select(excluded map[int64]struct{}) (textflow.ResponseSelection, error) {
	b.reqLog.Debug("openai_chat_completions.account_selecting", zap.Int("excluded_account_count", len(excluded)))
	var scheduleDecision service.OpenAIAccountScheduleDecision
	var err error
	b.selection, scheduleDecision, err = b.binding().selectAccountWithSchedulerForCapability(
		b.c.Request.Context(),
		b.apiKey.GroupID,
		"",
		b.sessionHash,
		b.reqModel,
		excluded,
		service.OpenAIUpstreamTransportAny,
		service.OpenAIEndpointCapabilityTextGeneration,
		false,
		false,
		b.requestPlatform,
	)
	if err != nil {
		return textflow.ResponseSelection{}, err
	}
	if b.selection == nil || b.selection.Account == nil {
		cls := classifyOpenAICompatibleNoAccountErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel)
		if !cls.ModelNotFound {
			markOpsRoutingCapacityLimited(b.c)
		}
		b.binding().handleStreamingAwareError(b.c, cls.Status, cls.ErrType, cls.Message, (*b.streamStarted))
		return textflow.ResponseSelection{}, nil
	}
	b.account = b.selection.Account
	b.sessionHash = ensureOpenAIPoolModeSessionHash(b.sessionHash, b.account)
	b.reqLog.Debug("openai_chat_completions.account_selected", zap.Int64("account_id", b.account.ID), zap.String("account_name", b.account.Name))
	_ = scheduleDecision
	setOpsSelectedAccount(b.c, b.account.ID, b.account.Platform)

	return b.selectedView(), nil
}

// SelectionFailure 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) SelectionFailure(err error, excludedCount int, last *textflow.AttemptFailure) {
	var lastFailoverErr *service.UpstreamFailoverError
	if last != nil {
		errors.As(last.Cause, &lastFailoverErr)
	}

	if failoverClientGone(b.c) {
		b.reqLog.Info("openai_chat_completions.account_select_aborted_client_disconnected", zap.Error(err))
		return
	}
	b.reqLog.Warn("openai_chat_completions.account_select_failed",
		zap.Error(openAICompatibleSelectionErrorForLog(err, b.requestPlatform)),
		zap.Int("excluded_account_count", excludedCount),
	)
	if excludedCount == 0 {
		if b.binding().handleOpenAISelectionBusinessError(b.c, err, (*b.streamStarted)) {
			return
		}
		cls := classifyOpenAICompatibleNoAccountErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel)
		cls = classifySelectionFailureError(err, cls)
		if !cls.ModelNotFound {
			markOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
		}
		b.binding().handleStreamingAwareError(b.c, cls.Status, cls.ErrType, cls.Message, (*b.streamStarted))
		return
	} else {
		if lastFailoverErr != nil {
			b.binding().handleFailoverExhausted(b.c, lastFailoverErr, (*b.streamStarted))
		} else {
			b.binding().handleStreamingAwareError(b.c, http.StatusBadGateway, "api_error", "Upstream request failed", (*b.streamStarted))
		}
		return
	}
}

// Forward 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) Forward() textflow.ResponseOutcome {
	var err error
	service.SetOpsLatencyMs(b.c, service.OpsRoutingLatencyMsKey, time.Since(b.routingStart).Milliseconds())
	forwardStart := time.Now()

	b.forwardBody = b.body
	if b.channelMapping.Mapped {
		b.forwardBody = b.binding().replaceModelInBody(b.body, b.channelMapping.MappedModel)
	}
	b.writerSizeBeforeForward = b.c.Writer.Size()
	b.result, err = func() (*service.OpenAIForwardResult, error) {
		defer func() {
			if b.accountReleaseFunc != nil {
				b.accountReleaseFunc()
			}
		}()
		tlsRouterMatch := b.binding().matchOpenAITLSFingerprintRouterForRequest(b.c, b.account)
		if err := b.binding().enforceOpenAIClientPolicyForRequest(b.c.Request.Context(), b.c, b.account, b.forwardBody, tlsRouterMatch); err != nil {
			return nil, err
		}
		return b.binding().forwardAsChatCompletions(b.c.Request.Context(), b.c, b.account, b.forwardBody, b.promptCacheKey, "", tlsRouterMatch)
	}()
	var cyberBlockBodyChat []byte
	if service.GetOpsCyberPolicy(b.c) != nil {
		cyberBlockBodyChat = b.body
	}
	b.cyberPolicyHandled = b.binding().recordCyberPolicyIfMarked(b.c, b.apiKey, b.account, b.subscription, b.reqModel, err != nil, cyberBlockBodyChat, clientRequestedUsageFields(b.c, b.channelMapping, b.reqModel, ""), service.HashUsageRequestPayload(b.body))

	forwardDurationMs := time.Since(forwardStart).Milliseconds()
	upstreamLatencyMs, _ := getContextInt64(b.c, service.OpsUpstreamLatencyMsKey)
	responseLatencyMs := forwardDurationMs
	if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
		responseLatencyMs = forwardDurationMs - upstreamLatencyMs
	}
	service.SetOpsLatencyMs(b.c, service.OpsResponseLatencyMsKey, responseLatencyMs)
	if err == nil && b.result != nil && b.result.FirstTokenMs != nil {
		service.SetOpsLatencyMs(b.c, service.OpsTimeToFirstTokenMsKey, int64(*b.result.FirstTokenMs))
	}
	out := textflow.ResponseOutcome{Outcome: textflow.Outcome{Attempt: openAIObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil}, Images: b.result != nil && b.result.ImageCount > 0}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	var retry *service.UpstreamFailoverError
	if errors.As(err, &retry) {
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
	}
	return out

}

// Complete 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) Complete() {
	res := b.result

	if res == nil {
		return
	}
	stampOpenAIRequestedReasoningEffort(res, b.c)
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(b.c)
	inboundEndpoint := GetInboundEndpoint(b.c)
	upstreamEndpoint := resolveOpenAIUpstreamEndpoint(b.c, b.account, res)
	quotaPlatform := service.QuotaPlatform(b.c.Request.Context(), b.apiKey)
	clientSessionID := service.ExtractClientSessionID(b.c)
	// 入队前固化资金与报文投影，worker 不再读取请求中的实体。
	completionInput := service.CompletionOpenAIInput(usageRecordContextFromGin(b.c), &service.OpenAIRecordUsageInput{
		Result:             res,
		APIKey:             b.apiKey,
		User:               b.apiKey.User,
		Account:            b.account,
		Subscription:       b.subscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		RequestBody:        b.body,
		APIKeyService:      b.binding().apiKeyService,
		QuotaPlatform:      quotaPlatform,
		ClientSessionID:    clientSessionID,
		ChannelUsageFields: b.channelMapping.ToUsageFields(b.reqModel, res.UpstreamModel),
		CyberBlocked:       b.cyberPolicyHandled,
	})
	completionUserID := b.subject.UserID
	completionModel := b.reqModel
	completionRuntime := b.binding().recorder
	b.binding().submitOpenAIUsageRecordTask(b.c, res, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, true); err != nil {
			logger.L().With(
				zap.String("component", "handler.openai_gateway.chat_completions"),
				zap.Int64("user_id", completionUserID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Any("group_id", completionInput.APIKey.GroupID),
				zap.String("model", completionModel),
				zap.Int64("account_id", completionInput.Account.ID),
			).Error("openai_chat_completions.record_usage_failed", zap.Error(err))
		}
	})
}

// PartialImages 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) PartialImages(err error) {
	b.reqLog.Warn("openai_chat_completions.forward_partial_error_with_image_result",
		zap.Int64("account_id", b.account.ID),
		zap.Int("image_count", b.result.ImageCount),
		zap.Error(err),
	)
}

// RetryReady 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) RetryReady(failure *textflow.AttemptFailure) bool {
	err := failure.Cause
	var failoverErr *service.UpstreamFailoverError
	if !errors.As(err, &failoverErr) {
		return false
	}
	if failoverClientGone(b.c) {
		b.reqLog.Info("openai_chat_completions.failover_aborted_client_disconnected",
			zap.Int64("account_id", b.account.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
		)
		return false
	}
	b.binding().recordOpenAICyberWarning(b.c, b.reqLog, b.apiKey, b.account, b.reqModel, failoverErr.StatusCode, failoverErr.ResponseBody, err.Error())
	if b.c.Writer.Size() != b.writerSizeBeforeForward {
		b.binding().observeOpenAIAccountHealthFailure(b.c.Request.Context(), b.account, err)
		b.binding().handleFailoverExhausted(b.c, failoverErr, true)
		return false
	}
	if failoverErr.ShouldReportAccountScheduleFailure() {
		b.binding().reportOpenAIAccountScheduleResult(b.account, openAIAccountScheduleModel(b.c, b.account, b.reqModel, false, nil), false, nil, err)
	}
	return true
}

// RetryWait 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) RetryWait(failure *textflow.AttemptFailure, retryLimit, retryCount int, retryDelay time.Duration) {
	var failoverErr *service.UpstreamFailoverError
	errors.As(failure.Cause, &failoverErr)
	b.reqLog.Warn("openai_chat_completions.pool_mode_same_account_retry",
		zap.Int64("account_id", b.account.ID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("retry_limit", retryLimit),
		zap.Int("retry_count", retryCount),
		zap.Duration("retry_delay", retryDelay),
	)
}

// Switching 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) Switching(failure *textflow.AttemptFailure, switchCount, maxAccountSwitches int) {
	var failoverErr *service.UpstreamFailoverError
	errors.As(failure.Cause, &failoverErr)
	b.reqLog.Warn("openai_chat_completions.upstream_failover_switching",
		zap.Int64("account_id", b.account.ID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("switch_count", switchCount),
		zap.Int("max_switches", maxAccountSwitches),
	)
}

// OtherFailure 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) OtherFailure(err error) {
	statusCode := 0
	if v, ok := getContextInt64(b.c, service.OpsUpstreamStatusCodeKey); ok {
		statusCode = int(v)
	}
	recordedWarning := b.binding().recordOpenAIForwardErrorCyberWarning(b.c, b.reqLog, b.apiKey, b.account, b.reqModel, statusCode, err)
	if !recordedWarning {
		b.binding().recordOpenAICyberWarning(b.c, b.reqLog, b.apiKey, b.account, b.reqModel, statusCode, nil, err.Error())
	}
	b.binding().reportOpenAIAccountScheduleResult(b.account, openAIAccountScheduleModel(b.c, b.account, b.reqModel, false, nil), false, nil, err)
	upstreamErrorAlreadyCommunicated := openAIForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	b.wroteFallback = false
	if !upstreamErrorAlreadyCommunicated && (!recordedWarning || b.c.Writer.Size() == b.writerSizeBeforeForward) {
		b.wroteFallback = b.binding().ensureOpenAIStreamReadErrorResponse(b.c, err, (*b.streamStarted))
		if !b.wroteFallback {
			b.wroteFallback = b.binding().ensureOpenAIForwardErrorResponse(b.c, (*b.streamStarted), err)
		}
	}
	b.reqLog.Warn("openai_chat_completions.forward_failed",
		zap.Int64("account_id", b.account.ID),
		zap.Bool("fallback_error_response_written", b.wroteFallback),
		zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
		zap.Error(err),
	)
}

// Success 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) Success() {
	if b.result != nil {
		b.binding().reportOpenAIAccountScheduleResult(b.account, openAIAccountScheduleModel(b.c, b.account, b.reqModel, false, b.result), true, b.result.FirstTokenMs)
	} else {
		b.binding().reportOpenAIAccountScheduleResult(b.account, openAIAccountScheduleModel(b.c, b.account, b.reqModel, false, b.result), true, nil)
	}

}

// Completed 保留 OpenAI Chat 适配差异，循环复用 gateway/text。
func (b *openAIChatAttemptBridge) Completed(switchCount int) {
	b.reqLog.Debug("openai_chat_completions.request_completed",
		zap.Int64("account_id", b.account.ID),
		zap.Int("switch_count", switchCount),
	)
}

func (b *openAIChatAttemptBridge) CanAttempt() bool { return !failoverClientGone(b.c) }
func (b *openAIChatAttemptBridge) Failed()          {}
