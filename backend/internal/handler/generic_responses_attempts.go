package handler

import (
	"context"
	"errors"
	"net/http"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"go.uber.org/zap"
)

// 单请求适配不另存缓存或重试状态。
type genericResponsesAttemptBridge struct {
	messageAttemptBridge
	requestCtx     context.Context
	forwardBody    []byte
	channelMapping service.ChannelMappingResult
}

// Select 保留通用 Responses 适配；循环复用 gateway/text。
func (b *genericResponsesAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	var err error
	b.selection, err = b.binding().selectAccount(b.requestCtx, b.apiKey.GroupID, b.sessionKey, b.reqModel, excluded, "", int64(0))
	if err != nil {
		return textflow.Selection{}, err
	}
	b.account = b.selection.Account
	setOpsSelectedAccount(b.c, b.account.ID, b.account.Platform)
	return capturedTextSelection(b.account), nil

}

// FirstSelectionFailure 保留通用 Responses 适配；循环复用 gateway/text。
func (b *genericResponsesAttemptBridge) FirstSelectionFailure(err error, _ bool) {

	cls := classifyNoAccountErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, effectiveAPIKeyPlatform(b.c, b.apiKey))
	cls = classifySelectionFailureError(err, cls)
	if !cls.ModelNotFound {
		markOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available accounts: " + err.Error()
	}
	b.binding().responsesErrorResponse(b.c, cls.Status, cls.ErrType, message)
}

// Acquire 保留通用 Responses 适配；循环复用 gateway/text。
func (b *genericResponsesAttemptBridge) Acquire() bool {
	var err error
	// 4. Acquire account concurrency slot
	b.accountReleaseFunc = b.selection.ReleaseFunc
	if !b.selection.Acquired {
		if b.selection.WaitPlan == nil {
			markOpsRoutingCapacityLimited(b.c)
			b.binding().responsesErrorResponse(b.c, http.StatusServiceUnavailable, "api_error", "No available accounts")
			return false
		}
		b.accountReleaseFunc, err = b.binding().concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
			b.c,
			b.account.ID,
			b.selection.WaitPlan.MaxConcurrency,
			b.selection.WaitPlan.Timeout,
			b.reqStream,
			b.streamStarted,
		)
		if err != nil {
			b.reqLog.Warn("gateway.responses.account_slot_acquire_failed", zap.Int64("account_id", b.account.ID), zap.Error(err))
			b.binding().handleConcurrencyError(b.c, err, "account", (*b.streamStarted))
			return false
		}
	}
	b.accountReleaseFunc = wrapReleaseOnDone(b.c.Request.Context(), b.accountReleaseFunc)

	return true
}

// Forward 保留通用 Responses 适配；循环复用 gateway/text。
func (b *genericResponsesAttemptBridge) Forward(_ textflow.AttemptState) textflow.Outcome {
	var err error
	// 5. Forward request
	b.writerSizeBeforeForward = b.c.Writer.Size()

	setActualUpstreamEndpoint(b.c, "")
	if b.account.Platform == service.PlatformGemini {
		if !b.binding().geminiAvailable {
			b.binding().responsesErrorResponse(b.c, http.StatusBadGateway, "upstream_error", "Gemini compatibility service is not configured")
			if b.accountReleaseFunc != nil {
				b.accountReleaseFunc()
			}
			return textflow.Outcome{Stop: true}
		}
		setActualUpstreamEndpoint(b.c, EndpointGeminiModels)
		b.result, err = b.binding().forwardGeminiResponses(b.requestCtx, b.c, b.account, b.forwardBody, b.parsedReq)
	} else if shouldUseAntigravityCompat(b.account) {
		if !b.binding().antigravityAvailable {
			b.binding().responsesErrorResponse(b.c, http.StatusBadGateway, "upstream_error", "Antigravity compatibility service is not configured")
			if b.accountReleaseFunc != nil {
				b.accountReleaseFunc()
			}
			return textflow.Outcome{Stop: true}
		}
		setActualUpstreamEndpoint(b.c, EndpointAntigravityGenerateContent)
		b.result, err = b.binding().forwardAntigravityResponses(b.requestCtx, b.c, b.account, b.forwardBody, b.parsedReq)
	} else {
		b.result, err = b.binding().forwardResponses(b.requestCtx, b.c, b.account, b.forwardBody, b.parsedReq)
	}

	if b.accountReleaseFunc != nil {
		b.accountReleaseFunc()
	}
	b.binding().reportSchedule(b.selection, b.account.ID, err == nil, b.result)

	out := textflow.Outcome{Attempt: messageObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil, OutputChanged: b.c.Writer.Size() != b.writerSizeBeforeForward}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	out.Attempt.RetryCommitted = out.OutputChanged
	var policy *service.BetaBlockedError
	var retry *service.UpstreamFailoverError
	switch {
	case errors.As(err, &policy):
		out.Kind = textflow.FailurePolicy
	case errors.As(err, &retry):
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
	}
	return out

}

// OtherFailure 保留通用 Responses 适配；循环复用 gateway/text。
func (b *genericResponsesAttemptBridge) OtherFailure(err error) {
	upstreamErrorAlreadyCommunicated := gatewayForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	wroteFallback := false
	if !upstreamErrorAlreadyCommunicated {
		wroteFallback = b.binding().ensureForwardErrorResponse(b.c, (*b.streamStarted))
	}
	b.reqLog.Error("gateway.responses.forward_failed",
		zap.Int64("account_id", b.account.ID),
		zap.Bool("fallback_error_response_written", wroteFallback),
		zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
		zap.Error(err),
	)
}

// Complete 保留通用 Responses 适配；循环复用 gateway/text。
func (b *genericResponsesAttemptBridge) Complete(_ textflow.AttemptState) {
	// 6. Record usage
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(b.c)
	requestPayloadHash := service.HashUsageRequestPayload(b.body)
	inboundEndpoint := GetInboundEndpoint(b.c)
	upstreamEndpoint := GetUpstreamEndpoint(b.c, b.account.Platform)

	quotaPlatform := service.QuotaPlatform(b.c.Request.Context(), b.apiKey)
	clientSessionID := service.ExtractClientSessionID(b.c)
	stampForwardRequestedReasoningEffort(b.result, b.c)
	// 入队前固化资金与报文投影，worker 不再读取请求中的实体。
	completionInput := service.CompletionForwardInput(usageRecordContextFromGin(b.c), &service.RecordUsageInput{
		Result:             b.result,
		QuotaPlatform:      quotaPlatform,
		APIKey:             b.apiKey,
		User:               b.apiKey.User,
		Account:            b.account,
		Subscription:       b.subscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		RequestPayloadHash: requestPayloadHash,
		RequestBody:        b.body,
		APIKeyService:      b.binding().apiKeyService,
		ClientSessionID:    clientSessionID,
		ChannelUsageFields: b.channelMapping.ToUsageFields(b.reqModel, b.result.UpstreamModel),
	})
	completionRuntime := b.binding().recorder
	completionLog := b.reqLog
	b.binding().submitUsageRecordTask(b.c, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
			completionLog.Error("gateway.responses.record_usage_failed",
				zap.Int64("account_id", completionInput.Account.ID),
				zap.Error(err),
			)
		}
	})
}

func (b *genericResponsesAttemptBridge) Context() context.Context { return b.requestCtx }
func (b *genericResponsesAttemptBridge) Begin()                   {}
func (b *genericResponsesAttemptBridge) PrepareAttempt() bool     { return true }
func (b *genericResponsesAttemptBridge) Intercept() bool          { return false }
func (b *genericResponsesAttemptBridge) SingleAccountRetry()      {}
func (b *genericResponsesAttemptBridge) Abandon(int64)            {}
func (b *genericResponsesAttemptBridge) Success()                 {}
func (b *genericResponsesAttemptBridge) Exhausted(err *textflow.AttemptFailure, _ string, stream bool) {
	var original *service.UpstreamFailoverError
	if err != nil && errors.As(err.Cause, &original) {
		b.binding().handleResponsesFailoverExhausted(b.c, original, stream || *b.streamStarted)
	} else {
		b.binding().responsesErrorResponse(b.c, http.StatusBadGateway, "server_error", "All available accounts exhausted")
	}
}
func (b *genericResponsesAttemptBridge) PolicyFailure(err error) {
	var original *service.BetaBlockedError
	if errors.As(err, &original) {
		service.MarkOpsClientBusinessLimited(b.c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		b.binding().responsesErrorResponse(b.c, http.StatusBadRequest, "invalid_request_error", original.Message)
	}
}
