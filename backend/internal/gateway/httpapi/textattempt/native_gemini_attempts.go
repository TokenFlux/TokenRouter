package textattempt

import (
	"context"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// nativeGeminiAttemptBridge 不在失败路径新增完成提交，也不输出 Anthropic 心跳。
type nativeGeminiAttemptBridge struct {
	messageAttemptBridge
	modelName, action                                                          string
	stream                                                                     bool
	geminiConcurrency                                                          *gatewayhttp.ConcurrencyHelper
	useDigestFallback                                                          bool
	geminiDigestChain, geminiPrefixHash, geminiSessionUUID, matchedDigestChain string
	groupMapping                                                               routing.GroupMappingResult
	signatureState                                                             requeststate.GeminiSignatureState
}

// Select 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	// 传入原始模型 R，通用调度器负责执行 R -> G。
	var err error
	b.selection, err = b.binding().selectProvider(b.c.Request.Context(), b.apiKey.GroupID, b.sessionKey, b.reqModel, excluded, "", int64(0)) // Gemini 不使用会话限制
	if err != nil {
		return textflow.Selection{}, err
	}
	b.provider = b.selection.Provider
	gatewayhttp.SetOpsSelectedProvider(b.c, b.provider.Record.ID, b.provider.Record.Platform)
	change := b.signatureState.Select(b.provider.Record.ID, b.sessionKey != "", b.body)
	if change.Clean {
		if change.Missing {
			b.reqLog.Info("gemini.sticky_session_binding_missing", zap.Bool("clean_thought_signature", true))
		} else {
			b.reqLog.Info("gemini.sticky_session_provider_switched", zap.Int64("from_provider_id", change.PreviousProviderID), zap.Int64("to_provider_id", b.provider.Record.ID), zap.Bool("clean_thought_signature", true))
		}
		b.body = protocolgemini.CleanNativeThoughtSignatures(b.body, bridge.DummyThoughtSignature)
	}
	return gatewaycapture.CaptureTextSelection(b.provider), nil
}

// FirstSelectionFailure 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) FirstSelectionFailure(err error, _ bool) {
	if handleGeminiGroupModelUnsupportedError(b.c, err) {
		return
	}
	cls := classifyNoProviderErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, capability.PlatformGemini)
	if !cls.ModelNotFound {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available Gemini providers: " + err.Error()
	}
	gatewayhttp.WriteGoogleError(b.c, cls.Status, message)
}

// Acquire 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) Acquire() bool {
	// 4) provider concurrency slot
	b.providerReleaseFunc = b.selection.ReleaseFunc
	if !b.selection.Acquired {
		if b.selection.WaitPlan == nil {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
			gatewayhttp.WriteGoogleError(b.c, http.StatusServiceUnavailable, "No available Gemini providers")
			return false
		}
		providerWaitCounted := false
		waitEntry, err := b.geminiConcurrency.EnterProviderWait(b.c.Request.Context(), b.provider.Record.ID, b.selection.WaitPlan.MaxWaiting)
		canWait := waitEntry.Allowed
		if err != nil {
			b.reqLog.Warn("gemini.provider_wait_counter_increment_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		} else if !canWait {
			b.reqLog.Info("gemini.provider_wait_queue_full",
				zap.Int64("provider_id", b.provider.Record.ID),
				zap.Int("max_waiting", b.selection.WaitPlan.MaxWaiting),
			)
			gatewayhttp.WriteGoogleError(b.c, http.StatusTooManyRequests, "Too many pending requests, please retry later")
			return false
		}
		if err == nil && canWait {
			providerWaitCounted = true
		}
		defer func() {
			if providerWaitCounted {
				waitEntry.Release()
			}
		}()

		b.providerReleaseFunc, err = b.geminiConcurrency.AcquireProviderSlotWithWaitTimeout(
			b.c,
			b.provider.Record.ID,
			b.selection.WaitPlan.MaxConcurrency,
			b.selection.WaitPlan.Timeout,
			b.stream,
			b.streamStarted,
		)
		if err != nil {
			b.reqLog.Warn("gemini.provider_slot_acquire_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
			gatewayhttp.WriteGoogleError(b.c, http.StatusTooManyRequests, err.Error())
			return false
		}
		if providerWaitCounted {
			waitEntry.Release()
			providerWaitCounted = false
		}
		if err := b.binding().bindSticky(b.c.Request.Context(), b.apiKey.GroupID, b.sessionKey, b.provider.Record.ID); err != nil {
			b.reqLog.Warn("gemini.bind_sticky_session_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		}
	}
	// 提供商槽位/等待计数需要在超时或断开时安全回收
	b.providerReleaseFunc = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, b.providerReleaseFunc)

	return true
}

// Forward 在生成请求通过价格检查后调用选中的提供商。
// @project-doc docs/domains/routing_and_billing.md#missing_model_pricing
func (b *nativeGeminiAttemptBridge) Forward(state textflow.AttemptState) textflow.Outcome {
	if b.providerReleaseFunc != nil {
		defer b.providerReleaseFunc()
	}

	requestCtx := b.c.Request.Context()
	if state.SwitchCount > 0 {
		requestCtx = requeststate.WithProviderSwitchCount(requestCtx, state.SwitchCount)
	}
	// 计数动作由平台执行器处理，生成动作在每次换号后重新查价。
	if b.action == "generateContent" || b.action == "streamGenerateContent" {
		err := gatewayhttp.CheckTextModelPricing(
			requestCtx, b.binding().pricing, b.apiKey, b.provider,
			b.reqModel, b.modelName, nil, protocol.ProtocolGeminiGenerateContent,
		)
		if err != nil {
			if errors.Is(err, admission.ErrModelPricingRejected) {
				gatewayhttp.MarkOpsClientBusinessLimited(b.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
				gatewayhttp.WriteGoogleError(b.c, http.StatusBadRequest, admission.ModelPricingUnavailableMessage)
			} else {
				b.reqLog.Error("gemini.pricing_check_failed", zap.Error(err))
				gatewayhttp.WriteGoogleError(b.c, http.StatusInternalServerError, "Failed to check model pricing")
			}
			return textflow.Outcome{Err: err, Stop: true}
		}
	}
	var err error
	sessionGroupID := derefGroupID(b.apiKey.GroupID)
	if b.provider.Record.Platform == capability.PlatformAntigravity {
		b.result, err = b.binding().forwardAntigravityGemini(
			requestCtx,
			b.c,
			b.provider,
			b.modelName,
			b.action,
			b.stream,
			b.body,
			b.hasBoundSession,
			forwardcore.WithGeminiSession(sessionGroupID, b.sessionKey),
		)
	} else {
		b.result, err = b.binding().forwardGeminiNative(requestCtx, b.c, b.provider, b.modelName, b.action, b.stream, b.body)
	}
	b.binding().reportSchedule(b.selection, b.provider.Record.ID, err == nil, b.result)
	out := textflow.Outcome{Attempt: messageObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	if retry, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err); ok {
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
	}
	return out
}

// OtherFailure 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) OtherFailure(err error) {
	// ForwardNative already wrote the response
	b.reqLog.Error("gemini.forward_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
}

// Complete 保留 Gemini 原生适配，重试与完成资格由文本核心控制。
func (b *nativeGeminiAttemptBridge) Complete(state textflow.AttemptState) {
	completionActorID := b.subject.UserID
	completionModel := b.modelName
	// 捕获请求信息，供异步任务记录。
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
			b.provider.Record.ID,
			b.matchedDigestChain,
		); err != nil {
			b.reqLog.Warn("gemini.digest_session_save_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		}
	}

	// 用量记录通过有界 worker 池提交。
	requestPayloadHash := billing.HashUsageRequestPayload(b.body)
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(b.c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(b.c, b.provider.Record.Platform)
	// 完成任务捕获 ForceCacheBilling 的布尔值，failover 响应体可随请求释放。
	forceCacheBilling := state.ForceCacheBilling

	clientSessionID := gatewayhttp.ExtractClientSessionID(b.c)
	// 入队前捕获资金和报文数据，worker 使用这份快照。
	completionInput := gatewaycapture.CaptureMessages(gatewayhttp.CompletionContext(b.c), &gatewaycapture.MessagesCapture{
		Result: b.result,

		APIKey:             b.apiKey,
		User:               b.apiKey.User,
		Provider:           gatewaycapture.ExecutionCompletionRecord(b.provider),
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
		PricingUsageFields: b.groupMapping.ToUsageFields(b.reqModel, b.result.UpstreamModel),
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
				zap.Int64("provider_id", completionInput.Provider.ID),
			).Error("gemini.record_usage_failed", zap.Error(err))
		}
	})
	b.reqLog.Debug("gemini.request_completed",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("switch_count", state.SwitchCount),
	)
}

func (b *nativeGeminiAttemptBridge) Begin() {
	if b.binding().singleProviderGroup(b.Context(), b.apiKey.GroupID) {
		b.SingleProviderRetry()
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
