package textattempt

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"context"
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"go.uber.org/zap"
)

// nativeGeminiAttemptBridge 不在失败路径新增完成提交，也不输出 Anthropic 心跳。
type nativeGeminiAttemptBridge struct {
	messageAttemptBridge
	modelName, action                                                          string
	stream                                                                     bool
	geminiConcurrency                                                          *gatewayhttp.ConcurrencyHelper
	useDigestFallback                                                          bool
	geminiDigestChain, geminiPrefixHash, geminiSessionUUID, matchedDigestChain string
	channelMapping                                                             routing.ChannelMappingResult
	signatureState                                                             requeststate.GeminiSignatureState
}

// Select 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	// 通用调度器会自行执行 R -> C，这里必须传原始模型，避免把 C 再做一次渠道映射。
	var err error
	b.selection, err = b.binding().selectAccount(b.c.Request.Context(), b.apiKey.GroupID, b.sessionKey, b.reqModel, excluded, "", int64(0)) // Gemini 不使用会话限制
	if err != nil {
		return textflow.Selection{}, err
	}
	b.account = b.selection.Account
	gatewayhttp.SetOpsSelectedAccount(b.c, b.account.Record.ID, b.account.Record.Platform)
	change := b.signatureState.Select(b.account.Record.ID, b.sessionKey != "", b.body)
	if change.Clean {
		if change.Missing {
			b.reqLog.Info("gemini.sticky_session_binding_missing", zap.Bool("clean_thought_signature", true))
		} else {
			b.reqLog.Info("gemini.sticky_session_account_switched", zap.Int64("from_account_id", change.PreviousAccountID), zap.Int64("to_account_id", b.account.Record.ID), zap.Bool("clean_thought_signature", true))
		}
		b.body = protocolgemini.CleanNativeThoughtSignatures(b.body, bridge.DummyThoughtSignature)
	}
	return gatewaycapture.CaptureTextSelection(b.account), nil

}

// FirstSelectionFailure 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) FirstSelectionFailure(err error, _ bool) {

	if handleGeminiGroupModelUnsupportedError(b.c, err) {
		return
	}
	cls := classifyNoAccountErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, capability.PlatformGemini)
	if !cls.ModelNotFound {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available Gemini accounts: " + err.Error()
	}
	gatewayhttp.WriteGoogleError(b.c, cls.Status, message)
}

// Acquire 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) Acquire() bool {
	// 4) account concurrency slot
	b.accountReleaseFunc = b.selection.ReleaseFunc
	if !b.selection.Acquired {
		if b.selection.WaitPlan == nil {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
			gatewayhttp.WriteGoogleError(b.c, http.StatusServiceUnavailable, "No available Gemini accounts")
			return false
		}
		accountWaitCounted := false
		waitEntry, err := b.geminiConcurrency.EnterAccountWait(b.c.Request.Context(), b.account.Record.ID, b.selection.WaitPlan.MaxWaiting)
		canWait := waitEntry.Allowed
		if err != nil {
			b.reqLog.Warn("gemini.account_wait_counter_increment_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
		} else if !canWait {
			b.reqLog.Info("gemini.account_wait_queue_full",
				zap.Int64("account_id", b.account.Record.ID),
				zap.Int("max_waiting", b.selection.WaitPlan.MaxWaiting),
			)
			gatewayhttp.WriteGoogleError(b.c, http.StatusTooManyRequests, "Too many pending requests, please retry later")
			return false
		}
		if err == nil && canWait {
			accountWaitCounted = true
		}
		defer func() {
			if accountWaitCounted {
				waitEntry.Release()
			}
		}()

		b.accountReleaseFunc, err = b.geminiConcurrency.AcquireAccountSlotWithWaitTimeout(
			b.c,
			b.account.Record.ID,
			b.selection.WaitPlan.MaxConcurrency,
			b.selection.WaitPlan.Timeout,
			b.stream,
			b.streamStarted,
		)
		if err != nil {
			b.reqLog.Warn("gemini.account_slot_acquire_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
			gatewayhttp.WriteGoogleError(b.c, http.StatusTooManyRequests, err.Error())
			return false
		}
		if accountWaitCounted {
			waitEntry.Release()
			accountWaitCounted = false
		}
		if err := b.binding().bindSticky(b.c.Request.Context(), b.apiKey.GroupID, b.sessionKey, b.account.Record.ID); err != nil {
			b.reqLog.Warn("gemini.bind_sticky_session_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
		}
	}
	// 账号槽位/等待计数需要在超时或断开时安全回收
	b.accountReleaseFunc = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, b.accountReleaseFunc)

	return true
}

// Forward 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) Forward(state textflow.AttemptState) textflow.Outcome {
	var err error
	// 5) forward (根据平台分流)

	requestCtx := b.c.Request.Context()
	if state.SwitchCount > 0 {
		requestCtx = requeststate.WithAccountSwitchCount(requestCtx, state.SwitchCount)
	}
	sessionGroupID := derefGroupID(b.apiKey.GroupID)
	if b.account.Record.Platform == capability.PlatformAntigravity && b.account.Record.Type != capability.AccountTypeAPIKey {
		b.result, err = b.binding().forwardAntigravityGemini(
			requestCtx,
			b.c,
			b.account,
			b.modelName,
			b.action,
			b.stream,
			b.body,
			b.hasBoundSession,
			forwardcore.WithGeminiSession(sessionGroupID, b.sessionKey),
		)
	} else {
		b.result, err = b.binding().forwardGeminiNative(requestCtx, b.c, b.account, b.modelName, b.action, b.stream, b.body)
	}
	if b.accountReleaseFunc != nil {
		b.accountReleaseFunc()
	}
	b.binding().reportSchedule(b.selection, b.account.Record.ID, err == nil, b.result)
	out := textflow.Outcome{Attempt: messageObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	var retry *forwardcore.UpstreamFailoverError
	if errors.As(err, &retry) {
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
	}
	return out

}

// OtherFailure 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) OtherFailure(err error) {
	// ForwardNative already wrote the response
	b.reqLog.Error("gemini.forward_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
}

// Complete 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) Complete(state textflow.AttemptState) {
	completionActorID := b.subject.UserID
	completionModel := b.modelName
	// 捕获请求信息（用于异步记录，避免在 goroutine 中访问 gin.Context）
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(b.c)

	// 保存 Gemini 内容摘要会话（用于 Fallback 匹配）
	if b.useDigestFallback && b.geminiDigestChain != "" && b.geminiPrefixHash != "" {
		if err := b.binding().saveGeminiSession(
			b.c.Request.Context(),
			derefGroupID(b.apiKey.GroupID),
			b.geminiPrefixHash,
			b.geminiDigestChain,
			b.geminiSessionUUID,
			b.account.Record.ID,
			b.matchedDigestChain,
		); err != nil {
			b.reqLog.Warn("gemini.digest_session_save_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
		}
	}

	// 使用量记录通过有界 worker 池提交，避免请求热路径创建无界 goroutine。
	requestPayloadHash := billing.HashUsageRequestPayload(b.body)
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(b.c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(b.c, b.account.Record.Platform)
	// ForceCacheBilling 提前拍成标量，避免 worker 闭包保活 failover 状态里的响应体。
	forceCacheBilling := state.ForceCacheBilling
	quotaPlatform := admission.QuotaPlatform(b.c.Request.Context(), b.apiKey)
	clientSessionID := gatewayhttp.ExtractClientSessionID(b.c)
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
		ForceCacheBilling:  forceCacheBilling,
		APIKeyService:      b.binding().apiKeyService,
		ClientSessionID:    clientSessionID,
		ChannelUsageFields: b.channelMapping.ToUsageFields(b.reqModel, b.result.UpstreamModel),
	})
	completionRuntime := b.binding().recorder
	b.binding().submitUsageRecordTask(b.c, func(ctx context.Context) {
		// 长上下文阶梯由价格目录驱动，统一计费路径会根据模型与分组开关处理。
		if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
			logging.L().With(
				zap.String("component", "handler.gemini_v1beta.models"),
				zap.Int64("user_id", completionActorID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Any("group_id", completionInput.APIKey.GroupID),
				zap.String("model", completionModel),
				zap.Int64("account_id", completionInput.Account.ID),
			).Error("gemini.record_usage_failed", zap.Error(err))
		}
	})
	b.reqLog.Debug("gemini.request_completed",
		zap.Int64("account_id", b.account.Record.ID),
		zap.Int("switch_count", state.SwitchCount),
	)
}

func (b *nativeGeminiAttemptBridge) Begin() {
	if b.binding().singleAccountGroup(b.Context(), b.apiKey.GroupID) {
		b.SingleAccountRetry()
	}
}
func (b *nativeGeminiAttemptBridge) PrepareAttempt() bool { return true }
func (b *nativeGeminiAttemptBridge) Intercept() bool      { return false }
func (b *nativeGeminiAttemptBridge) Abandon(int64)        {}
func (b *nativeGeminiAttemptBridge) Success()             {}
func (b *nativeGeminiAttemptBridge) Exhausted(failure *textflow.AttemptFailure, _ string, _ bool) {
	var original *forwardcore.UpstreamFailoverError
	if failure != nil {
		errors.As(failure.Cause, &original)
	}
	b.binding().handleGeminiFailoverExhausted(b.c, original)
}
