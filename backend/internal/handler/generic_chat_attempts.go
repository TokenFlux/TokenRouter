package handler

import (
	admission "github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"context"
	"errors"
	"net/http"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"go.uber.org/zap"
)

// 单请求适配不另存缓存或重试状态。
type genericChatAttemptBridge struct {
	messageAttemptBridge
	requestCtx                          context.Context
	forwardBody                         []byte
	groupPlatform, selectionSessionHash string
	channelMapping                      routing.ChannelMappingResult
}

// Select 保留通用 ChatCompletions 适配；循环复用 gateway/text。
func (b *genericChatAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	var err error
	b.selection, err = b.binding().selectAccount(b.c.Request.Context(), b.apiKey.GroupID, b.selectionSessionHash, b.reqModel, excluded, "", int64(0))
	if err != nil {
		return textflow.Selection{}, err
	}
	b.account = b.selection.Account
	gatewayhttp.SetOpsSelectedAccount(b.c, b.account.Record.ID, b.account.Record.Platform)
	return capturedTextSelection(b.account), nil

}

// FirstSelectionFailure 保留通用 ChatCompletions 适配；循环复用 gateway/text。
func (b *genericChatAttemptBridge) FirstSelectionFailure(err error, _ bool) {

	if handleGroupSelectionBusinessError(b.c, err, (*b.streamStarted), func(status int, errType string, message string, responseStarted bool) {
		b.binding().chatCompletionsErrorResponse(b.c, status, errType, message)
	}) {
		return
	}
	cls := classifyNoAccountErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, b.groupPlatform)
	cls = classifySelectionFailureError(err, cls)
	if !cls.ModelNotFound {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available accounts: " + err.Error()
	}
	b.binding().chatCompletionsErrorResponse(b.c, cls.Status, cls.ErrType, message)
}

// Acquire 保留通用 ChatCompletions 适配；循环复用 gateway/text。
func (b *genericChatAttemptBridge) Acquire() bool {
	var err error
	// 4. Acquire account concurrency slot
	b.accountReleaseFunc = b.selection.ReleaseFunc
	if !b.selection.Acquired {
		if b.selection.WaitPlan == nil {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
			b.binding().chatCompletionsErrorResponse(b.c, http.StatusServiceUnavailable, "api_error", "No available accounts")
			return false
		}
		b.accountReleaseFunc, err = b.binding().concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
			b.c,
			b.account.Record.ID,
			b.selection.WaitPlan.MaxConcurrency,
			b.selection.WaitPlan.Timeout,
			b.reqStream,
			b.streamStarted,
		)
		if err != nil {
			b.reqLog.Warn("gateway.cc.account_slot_acquire_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
			b.binding().handleConcurrencyError(b.c, err, "account", (*b.streamStarted))
			return false
		}
	}
	b.accountReleaseFunc = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, b.accountReleaseFunc)

	return true
}

// Forward 保留通用 ChatCompletions 适配；循环复用 gateway/text。
func (b *genericChatAttemptBridge) Forward(_ textflow.AttemptState) textflow.Outcome {
	var err error

	if b.groupPlatform == capability.PlatformGemini && b.account.Record.Platform != capability.PlatformGemini {
		if b.accountReleaseFunc != nil {
			b.accountReleaseFunc()
		}

		return textflow.Outcome{Skip: true}
	}
	// 5. Forward request
	b.writerSizeBeforeForward = b.c.Writer.Size()
	b.forwardBody = b.body
	if b.channelMapping.Mapped {
		b.forwardBody = b.binding().replaceModel(b.body, b.channelMapping.MappedModel)
	}
	gatewayhttp.SetActualUpstreamEndpoint(b.c, "")
	if b.account.Record.Platform == capability.PlatformGemini {
		if !b.binding().geminiAvailable {
			b.binding().chatCompletionsErrorResponse(b.c, http.StatusBadGateway, "upstream_error", "Gemini compatibility service is not configured")
			if b.accountReleaseFunc != nil {
				b.accountReleaseFunc()
			}
			return textflow.Outcome{Stop: true}
		}
		b.result, err = b.binding().forwardGeminiChat(b.c.Request.Context(), b.c, b.account, b.forwardBody)
	} else if shouldUseAntigravityCompat(b.account) {
		if !b.binding().antigravityAvailable {
			b.binding().chatCompletionsErrorResponse(b.c, http.StatusBadGateway, "upstream_error", "Antigravity compatibility service is not configured")
			if b.accountReleaseFunc != nil {
				b.accountReleaseFunc()
			}
			return textflow.Outcome{Stop: true}
		}
		gatewayhttp.SetActualUpstreamEndpoint(b.c, gatewayhttp.EndpointAntigravityGenerateContent)
		b.result, err = b.binding().forwardAntigravityChat(b.c.Request.Context(), b.c, b.account, b.forwardBody, b.parsedReq)
	} else {
		b.result, err = b.binding().forwardChat(b.c.Request.Context(), b.c, b.account, b.forwardBody, b.parsedReq)
	}

	if b.accountReleaseFunc != nil {
		b.accountReleaseFunc()
	}
	b.binding().reportSchedule(b.selection, b.account.Record.ID, err == nil, b.result)

	out := textflow.Outcome{Attempt: messageObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil, OutputChanged: b.c.Writer.Size() != b.writerSizeBeforeForward}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	out.Attempt.RetryCommitted = out.OutputChanged
	var policy *anthropic.BetaBlockedError
	var retry *forwardcore.UpstreamFailoverError
	switch {
	case errors.As(err, &policy):
		out.Kind = textflow.FailurePolicy
	case errors.As(err, &retry):
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
	}
	return out

}

// OtherFailure 保留通用 ChatCompletions 适配；循环复用 gateway/text。
func (b *genericChatAttemptBridge) OtherFailure(err error) {
	upstreamErrorAlreadyCommunicated := gatewayhttp.ForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	wroteFallback := false
	if !upstreamErrorAlreadyCommunicated {
		wroteFallback = b.binding().ensureForwardErrorResponse(b.c, (*b.streamStarted))
	}
	b.reqLog.Error("gateway.cc.forward_failed",
		zap.Int64("account_id", b.account.Record.ID),
		zap.Bool("fallback_error_response_written", wroteFallback),
		zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
		zap.Error(err),
	)
}

// Complete 保留通用 ChatCompletions 适配；循环复用 gateway/text。
func (b *genericChatAttemptBridge) Complete(_ textflow.AttemptState) {
	// 6. Record usage
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(b.c)
	requestPayloadHash := billing.HashUsageRequestPayload(b.body)
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(b.c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(b.c, b.account.Record.Platform)

	quotaPlatform := admission.QuotaPlatform(b.c.Request.Context(), b.apiKey)
	clientSessionID := gatewayhttp.ExtractClientSessionID(b.c)
	gatewayhttp.StampForwardRequestedReasoningEffort(b.result, b.c)
	// 入队前固化资金与报文投影，worker 不再读取请求中的实体。
	completionInput := gatewaycapture.CaptureMessages(gatewayhttp.CompletionContext(b.c), &gatewaycapture.MessagesCapture{
		Result:             b.result,
		QuotaPlatform:      quotaPlatform,
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
		ClientSessionID:    clientSessionID,
		ChannelUsageFields: b.channelMapping.ToUsageFields(b.reqModel, b.result.UpstreamModel),
	})
	completionRuntime := b.binding().recorder
	completionLog := b.reqLog
	b.binding().submitUsageRecordTask(b.c, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
			completionLog.Error("gateway.cc.record_usage_failed",
				zap.Int64("account_id", completionInput.Account.ID),
				zap.Error(err),
			)
		}
	})
}

func (b *genericChatAttemptBridge) Context() context.Context { return b.requestCtx }
func (b *genericChatAttemptBridge) Begin()                   {}
func (b *genericChatAttemptBridge) PrepareAttempt() bool     { return true }
func (b *genericChatAttemptBridge) Intercept() bool          { return false }
func (b *genericChatAttemptBridge) SingleAccountRetry()      {}
func (b *genericChatAttemptBridge) Abandon(int64)            {}
func (b *genericChatAttemptBridge) Success()                 {}
func (b *genericChatAttemptBridge) Exhausted(err *textflow.AttemptFailure, _ string, stream bool) {
	var original *forwardcore.UpstreamFailoverError
	if err != nil && errors.As(err.Cause, &original) {
		b.binding().handleCCFailoverExhausted(b.c, original, stream || *b.streamStarted)
	} else {
		b.binding().chatCompletionsErrorResponse(b.c, http.StatusBadGateway, "server_error", "All available accounts exhausted")
	}
}
func (b *genericChatAttemptBridge) PolicyFailure(err error) {
	var original *anthropic.BetaBlockedError
	if errors.As(err, &original) {
		gatewayhttp.MarkOpsClientBusinessLimited(b.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		b.binding().chatCompletionsErrorResponse(b.c, http.StatusBadRequest, "invalid_request_error", original.Message)
	}
}
