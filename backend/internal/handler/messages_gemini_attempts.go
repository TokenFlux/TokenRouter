package handler

import (
	"context"
	"errors"
	"net/http"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"go.uber.org/zap"
)

// geminiMessageAttemptBridge 不注册 Anthropic 空闲会话，不扩大部分失败完成资格。
type geminiMessageAttemptBridge struct {
	messageAttemptBridge
	forwardModel   string
	forwardBody    []byte
	channelMapping service.ChannelMappingResult
}

// Select 保留 Gemini Messages 的既有差异，循环复用 gateway/text。
func (b *geminiMessageAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	var err error
	b.selection, err = b.binding().selectAccount(b.c.Request.Context(), b.apiKey.GroupID, b.sessionKey, b.reqModel, excluded, "", int64(0)) // Gemini 不使用会话限制
	if err != nil {
		return textflow.Selection{}, err
	}
	b.account = b.selection.Account
	setOpsSelectedAccount(b.c, b.account.ID, b.account.Platform)
	return capturedTextSelection(b.account), nil

}

// FirstSelectionFailure 保留 Gemini Messages 的既有差异，循环复用 gateway/text。
func (b *geminiMessageAttemptBridge) FirstSelectionFailure(err error, _ bool) {

	if handleGroupSelectionBusinessError(b.c, err, (*b.streamStarted), func(status int, errType string, message string, responseStarted bool) {
		b.binding().handleStreamingAwareError(b.c, status, errType, message, responseStarted)
	}) {
		return
	}
	cls := classifyNoAccountErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, service.PlatformGemini)
	if !cls.ModelNotFound {
		markOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	b.reqLog.Warn("gateway.select_account_no_available",
		zap.String("model", b.reqModel),
		zap.Int64p("group_id", b.apiKey.GroupID),
		zap.String("platform", b.platform),
		zap.Bool("model_not_found", cls.ModelNotFound),
		zap.Error(err),
	)
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available accounts: " + err.Error()
	}
	b.binding().handleStreamingAwareError(b.c, cls.Status, cls.ErrType, message, *b.streamStarted)
}

// Acquire 保留 Gemini Messages 的既有差异，循环复用 gateway/text。
func (b *geminiMessageAttemptBridge) Acquire() bool {
	// 3. 获取账号并发槽位
	b.accountReleaseFunc = b.selection.ReleaseFunc
	if !b.selection.Acquired {
		if b.selection.WaitPlan == nil {
			markOpsRoutingCapacityLimited(b.c)
			b.reqLog.Warn("gateway.select_account_no_slot_no_wait_plan",
				zap.Int64("account_id", b.account.ID),
				zap.String("model", b.reqModel),
				zap.String("platform", b.platform),
			)
			b.binding().handleStreamingAwareError(b.c, http.StatusServiceUnavailable, "api_error", "No available accounts", (*b.streamStarted))
			return false
		}
		accountWaitCounted := false
		waitEntry, err := b.binding().concurrencyHelper.EnterAccountWait(b.c.Request.Context(), b.account.ID, b.selection.WaitPlan.MaxWaiting)
		canWait := waitEntry.Allowed
		if err != nil {
			b.reqLog.Warn("gateway.account_wait_counter_increment_failed", zap.Int64("account_id", b.account.ID), zap.Error(err))
		} else if !canWait {
			b.reqLog.Info("gateway.account_wait_queue_full",
				zap.Int64("account_id", b.account.ID),
				zap.Int("max_waiting", b.selection.WaitPlan.MaxWaiting),
			)
			b.binding().handleStreamingAwareErrorWithCode(b.c, http.StatusTooManyRequests, "rate_limit_error", gatewayQueueFullCode, "Too many pending requests, please retry later", (*b.streamStarted))
			return false
		}
		if err == nil && canWait {
			accountWaitCounted = true
		}
		releaseWait := func() {
			if accountWaitCounted {
				waitEntry.Release()
				accountWaitCounted = false
			}
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
			b.reqLog.Warn("gateway.account_slot_acquire_failed", zap.Int64("account_id", b.account.ID), zap.Error(err))
			releaseWait()
			b.binding().handleConcurrencyError(b.c, err, "account", (*b.streamStarted))
			return false
		}
		// Slot acquired: no longer waiting in queue.
		releaseWait()
		if err := b.binding().bindSticky(b.c.Request.Context(), b.apiKey.GroupID, b.sessionKey, b.account.ID); err != nil {
			b.reqLog.Warn("gateway.bind_sticky_session_failed", zap.Int64("account_id", b.account.ID), zap.Error(err))
		}
	}
	// 账号槽位/等待计数需要在超时或断开时安全回收
	b.accountReleaseFunc = wrapReleaseOnDone(b.c.Request.Context(), b.accountReleaseFunc)

	return true
}

// Forward 保留 Gemini Messages 的既有差异，循环复用 gateway/text。
func (b *geminiMessageAttemptBridge) Forward(state textflow.AttemptState) textflow.Outcome {
	var err error
	// 转发请求 - 根据账号平台分流

	requestCtx := b.c.Request.Context()
	if state.SwitchCount > 0 {
		requestCtx = service.WithAccountSwitchCount(requestCtx, state.SwitchCount, b.binding().bridgeEnabled)
	}
	// 记录 Forward 前已写入字节数，Forward 后若增加则说明 SSE 内容已发，禁止 failover
	b.writerSizeBeforeForward = b.c.Writer.Size()
	if b.account.Platform == service.PlatformAntigravity {
		b.result, err = b.binding().forwardAntigravityGemini(
			requestCtx,
			b.c,
			b.account,
			b.forwardModel,
			"generateContent",
			b.reqStream,
			b.forwardBody,
			b.hasBoundSession,
			service.WithForwardGeminiSession(derefGroupID(b.apiKey.GroupID), b.sessionKey),
		)
	} else {
		b.result, err = b.binding().forwardGemini(requestCtx, b.c, b.account, b.forwardBody)
	}
	if b.accountReleaseFunc != nil {
		b.accountReleaseFunc()
	}
	b.binding().reportSchedule(b.selection, b.account.ID, err == nil, b.result)
	out := textflow.Outcome{Attempt: messageObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil, OutputChanged: b.c.Writer.Size() != b.writerSizeBeforeForward}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	out.Attempt.RetryCommitted = out.OutputChanged
	var retry *service.UpstreamFailoverError
	if errors.As(err, &retry) {
		out.Failure = &textflow.AttemptFailure{Cause: retry, Policy: retry.RetryFailure()}
	}
	return out

}

// Success 保留 Gemini Messages 的既有差异，循环复用 gateway/text。
func (b *geminiMessageAttemptBridge) Success() {
	// RPM 计数递增（Forward 成功后）
	// 注意：TOCTOU 竞态是已知且可接受的设计权衡，与 WindowCost 一致的 soft-limit 模式。
	// 在高并发下可能短暂超出 RPM 限制，但不会导致请求失败。
	if b.account.IsAnthropicOAuthOrSetupToken() && b.account.GetBaseRPM() > 0 {
		if err := b.binding().incrementRPM(b.c.Request.Context(), b.account.ID); err != nil {
			b.reqLog.Warn("gateway.rpm_increment_failed", zap.Int64("account_id", b.account.ID), zap.Error(err))
		}
	}

}

// Complete 保留 Gemini Messages 的既有差异，循环复用 gateway/text。
func (b *geminiMessageAttemptBridge) Complete(state textflow.AttemptState) {
	// 捕获请求信息（用于异步记录，避免在 goroutine 中访问 gin.Context）
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(b.c)
	requestPayloadHash := service.HashUsageRequestPayload(b.body)
	inboundEndpoint := GetInboundEndpoint(b.c)
	upstreamEndpoint := GetUpstreamEndpoint(b.c, b.account.Platform)

	if b.result.ReasoningEffort == nil {
		b.result.ReasoningEffort = service.NormalizeClaudeOutputEffort(b.parsedReq.OutputEffort)
	}
	if b.result.ReasoningEffort == nil && b.parsedReq.ThinkingEnabled {
		protocolModel := b.result.UpstreamModel
		if protocolModel == "" {
			protocolModel = b.result.Model
		}
		b.result.ReasoningEffort = service.DefaultEffortForThinkingEnabled(protocolModel)
	}

	// 使用量记录通过有界 worker 池提交，避免请求热路径创建无界 goroutine。
	// ForceCacheBilling 提前拍成标量，避免 worker 闭包保活 failover 状态里的响应体。
	forceCacheBilling := state.ForceCacheBilling
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
		ClientSessionID:    clientSessionID,
		RequestPayloadHash: requestPayloadHash,
		RequestBody:        append([]byte(nil), b.body...),
		ForceCacheBilling:  forceCacheBilling,
		APIKeyService:      b.binding().apiKeyService,
		ChannelUsageFields: b.channelMapping.ToUsageFields(b.reqModel, b.result.UpstreamModel),
	})
	completionUserID := b.subject.UserID
	completionModel := b.reqModel
	completionRuntime := b.binding().recorder
	b.binding().submitUsageRecordTask(b.c, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
			logger.L().With(
				zap.String("component", "handler.gateway.messages"),
				zap.Int64("user_id", completionUserID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Any("group_id", completionInput.APIKey.GroupID),
				zap.String("model", completionModel),
				zap.Int64("account_id", completionInput.Account.ID),
			).Error("gateway.record_usage_failed", zap.Error(err))
		}
	})
}

func (b *geminiMessageAttemptBridge) Begin() {
	b.currentAPIKey = b.apiKey
	b.currentSubscription = b.subscription
	if b.binding().singleAccountGroup(b.Context(), b.apiKey.GroupID) {
		b.SingleAccountRetry()
	}
}
func (b *geminiMessageAttemptBridge) PrepareAttempt() bool { return true }
func (b *geminiMessageAttemptBridge) Abandon(int64)        {}
func (b *geminiMessageAttemptBridge) Exhausted(err *textflow.AttemptFailure, _ string, stream bool) {
	b.messageAttemptBridge.Exhausted(err, service.PlatformGemini, stream)
}
