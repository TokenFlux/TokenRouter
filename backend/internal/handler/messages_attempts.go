// Messages 的旧执行端口只持有本请求投影，循环唯一位于 gateway/text。
package handler

import (
	admission "github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	protocol "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// messageAttemptBridge 保存尚未迁完的执行适配，异步完成仅捕获 completion.Input。
type messageAttemptBridge struct {
	fixed                                          *messageExecutionDependencies
	h                                              *GatewayHandler
	c                                              *gin.Context
	apiKey, currentAPIKey                          *apikey.APIKey
	subject                                        authctx.AuthSubject
	subscription, currentSubscription              *billing.UserSubscription
	parsedReq, attemptParsedReq                    *requeststate.ParsedRequest
	body                                           []byte
	reqModel, platform, sessionKey                 string
	reqStream, isClaudeCodeClient, hasBoundSession bool
	streamStarted                                  *bool
	sessionBoundAccountID                          int64
	reqLog                                         *zap.Logger
	fallbackGroupID                                *int64
	sessionAttempts                                *scheduler.SessionAttempts
	selection                                      *gatewaycapture.SelectionResult
	account                                        *gatewaycapture.ExecutionAccount
	accountReleaseFunc                             func()
	attemptChannelMapping                          routing.ChannelMappingResult
	writerSizeBeforeForward                        int
	result                                         *forwardcore.MessagesResult
}

// PrepareAttempt 只执行一次适配操作，重试与分组回退循环由 gateway/text 拥有。
func (b *messageAttemptBridge) PrepareAttempt() bool {
	var err error
	b.attemptParsedReq, b.attemptChannelMapping, err = b.binding().prepareGatewayAttemptRequest(
		b.c.Request.Context(), b.parsedReq, b.body, b.currentAPIKey, b.reqModel,
	)
	if err != nil {
		b.binding().errorResponse(b.c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return false
	}

	return true
}

// Select 只执行一次适配操作，重试与分组回退循环由 gateway/text 拥有。
func (b *messageAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	// 选择支持该模型的账号
	b.reqLog.Info("sticky.selecting_account",
		zap.String("session_key", b.sessionKey),
		zap.Int64("sticky_bound_account_id", b.sessionBoundAccountID),
		zap.Bool("has_bound_session", b.hasBoundSession),
		zap.Int("failed_account_count", len(excluded)),
	)
	var err error
	b.selection, err = b.binding().selectAccount(b.c.Request.Context(), b.currentAPIKey.GroupID, b.sessionKey, b.reqModel, excluded, b.parsedReq.MetadataUserID, b.subject.UserID)
	if err != nil {
		return textflow.Selection{}, err
	}
	b.account = b.selection.Account
	gatewayhttp.SetOpsSelectedAccount(b.c, b.account.Record.ID, b.account.Record.Platform)
	if b.sessionKey != "" {
		b.binding().trackSession(b.sessionAttempts, b.account, b.sessionKey)
	}

	// [DEBUG-STICKY] 打印账号选择结果
	b.reqLog.Info("sticky.account_selected",
		zap.Int64("selected_account_id", b.account.Record.ID),
		zap.String("account_name", b.account.Record.Name),
		zap.Bool("slot_acquired", b.selection.Acquired),
		zap.Bool("has_wait_plan", b.selection.WaitPlan != nil),
		zap.Int64("sticky_bound_account_id", b.sessionBoundAccountID),
		zap.Bool("sticky_honored", b.sessionBoundAccountID > 0 && b.sessionBoundAccountID == b.account.Record.ID),
	)

	return capturedTextSelection(b.account), nil
}

// FirstSelectionFailure 只执行一次适配操作，重试与分组回退循环由 gateway/text 拥有。
func (b *messageAttemptBridge) FirstSelectionFailure(err error, fallbackUsed bool) {

	if handleGroupSelectionBusinessError(b.c, err, (*b.streamStarted), func(status int, errType string, message string, responseStarted bool) {
		b.binding().handleStreamingAwareError(b.c, status, errType, message, responseStarted)
	}) {
		return
	}
	cls := classifyNoAccountErrorFromGin(b.c, b.binding().diagnoser, b.currentAPIKey, b.reqModel, b.reqModel, b.platform)
	if !cls.ModelNotFound {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	b.reqLog.Warn("gateway.select_account_no_available",
		zap.String("model", b.reqModel),
		zap.Int64p("group_id", b.currentAPIKey.GroupID),
		zap.String("platform", b.platform),
		zap.Bool("fallback_used", fallbackUsed),
		zap.Bool("model_not_found", cls.ModelNotFound),
		zap.Error(err),
	)
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available accounts: " + err.Error()
	}
	b.binding().handleStreamingAwareError(b.c, cls.Status, cls.ErrType, message, (*b.streamStarted))
}

// Intercept 只执行一次适配操作，重试与分组回退循环由 gateway/text 拥有。
func (b *messageAttemptBridge) Intercept() bool {
	if b.account.View().IsInterceptWarmupEnabled() {
		interceptType := detectInterceptType(b.body, b.reqModel, b.parsedReq.MaxTokens, b.isClaudeCodeClient)
		if interceptType != InterceptTypeNone {
			if b.selection.Acquired && b.selection.ReleaseFunc != nil {
				b.selection.ReleaseFunc()
			}
			if b.reqStream {
				sendMockInterceptStream(b.c, b.reqModel, interceptType)
			} else {
				sendMockInterceptResponse(b.c, b.reqModel, interceptType)
			}
			return true
		}
	}

	return false
}

// Acquire 只执行一次适配操作，重试与分组回退循环由 gateway/text 拥有。
func (b *messageAttemptBridge) Acquire() bool {
	// 3. 获取账号并发槽位
	b.accountReleaseFunc = b.selection.ReleaseFunc
	if !b.selection.Acquired {
		if b.selection.WaitPlan == nil {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
			b.reqLog.Warn("gateway.select_account_no_slot_no_wait_plan",
				zap.Int64("account_id", b.account.Record.ID),
				zap.String("model", b.reqModel),
				zap.String("platform", b.platform),
			)
			b.binding().handleStreamingAwareError(b.c, http.StatusServiceUnavailable, "api_error", "No available accounts", (*b.streamStarted))
			return false
		}
		accountWaitCounted := false
		waitEntry, err := b.binding().concurrencyHelper.EnterAccountWait(b.c.Request.Context(), b.account.Record.ID, b.selection.WaitPlan.MaxWaiting)
		canWait := waitEntry.Allowed
		if err != nil {
			b.reqLog.Warn("gateway.account_wait_counter_increment_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
		} else if !canWait {
			b.reqLog.Info("gateway.account_wait_queue_full",
				zap.Int64("account_id", b.account.Record.ID),
				zap.Int("max_waiting", b.selection.WaitPlan.MaxWaiting),
			)
			b.binding().handleStreamingAwareErrorWithCode(b.c, http.StatusTooManyRequests, "rate_limit_error", gatewayhttp.GatewayQueueFullCode, "Too many pending requests, please retry later", (*b.streamStarted))
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
			b.account.Record.ID,
			b.selection.WaitPlan.MaxConcurrency,
			b.selection.WaitPlan.Timeout,
			b.reqStream,
			b.streamStarted,
		)
		if err != nil {
			b.reqLog.Warn("gateway.account_slot_acquire_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
			releaseWait()
			b.binding().handleConcurrencyError(b.c, err, "account", (*b.streamStarted))
			return false
		}
		// Slot acquired: no longer waiting in queue.
		releaseWait()
		b.reqLog.Info("sticky.bind_after_wait",
			zap.String("session_key", b.sessionKey),
			zap.Int64("account_id", b.account.Record.ID),
		)
		if err := b.binding().bindSticky(b.c.Request.Context(), b.currentAPIKey.GroupID, b.sessionKey, b.account.Record.ID); err != nil {
			b.reqLog.Warn("gateway.bind_sticky_session_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
		}
	}
	// 账号槽位/等待计数需要在超时或断开时安全回收
	b.accountReleaseFunc = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, b.accountReleaseFunc)
	b.sessionAttempts.Own(b.account.Record.ID, b.accountReleaseFunc)

	return true
}

// Forward 只执行一次适配操作，重试与分组回退循环由 gateway/text 拥有。
func (b *messageAttemptBridge) Forward(state textflow.AttemptState) textflow.Outcome {
	var err error
	// ===== 用户消息串行队列 START =====
	var queueRelease func()
	umqMode := b.binding().getUserMsgQueueMode(b.account, b.attemptParsedReq)

	switch umqMode {
	case config.UMQModeSerialize:
		// 串行模式：获取锁 + RPM 延迟 + 释放（当前行为不变）
		baseRPM := gatewaycapture.ExecutionRuntimeConfig(b.account).GetBaseRPM()
		release, qErr := b.binding().userMsgQueueHelper.AcquireWithWait(
			b.c, b.account.Record.ID, baseRPM, b.reqStream, b.streamStarted,
			b.binding().messageWaitTimeout,
			b.reqLog,
		)
		if qErr != nil {
			// fail-open: 记录 warn，不阻止请求
			b.reqLog.Warn("gateway.umq_acquire_failed",
				zap.Int64("account_id", b.account.Record.ID),
				zap.Error(qErr),
			)
		} else {
			queueRelease = release
		}

	case config.UMQModeThrottle:
		// 软性限速：仅施加 RPM 自适应延迟，不阻塞并发
		baseRPM := gatewaycapture.ExecutionRuntimeConfig(b.account).GetBaseRPM()
		if tErr := b.binding().userMsgQueueHelper.ThrottleWithPing(
			b.c, b.account.Record.ID, baseRPM, b.reqStream, b.streamStarted,
			b.binding().messageWaitTimeout,
			b.reqLog,
		); tErr != nil {
			b.reqLog.Warn("gateway.umq_throttle_failed",
				zap.Int64("account_id", b.account.Record.ID),
				zap.Error(tErr),
			)
		}

	default:
		if umqMode != "" {
			b.reqLog.Warn("gateway.umq_unknown_mode",
				zap.String("mode", umqMode),
				zap.Int64("account_id", b.account.Record.ID),
			)
		}
	}

	// 用 wrapReleaseOnDone 确保 context 取消时自动释放（仅 serialize 模式有 queueRelease）
	queueRelease = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, queueRelease)
	b.sessionAttempts.Own(b.account.Record.ID, queueRelease)
	// 注入回调到 ParsedRequest：使用外层 wrapper 以便提前清理 AfterFunc
	b.attemptParsedReq.OnUpstreamAccepted = queueRelease
	// ===== 用户消息串行队列 END =====

	// Bedrock CC 兼容：清理 body 专有字段 + 过滤 anthropic-beta header，适用于所有转发路径
	if err := b.attemptParsedReq.ReplaceBody(b.binding().bedrockCompat(b.c, b.attemptParsedReq.Body.Bytes(), b.attemptParsedReq.Model, b.account, b.currentAPIKey.GroupID)); err != nil {
		b.binding().errorResponse(b.c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return textflow.Outcome{Stop: true}
	}
	attemptBody := b.attemptParsedReq.Body.Bytes()

	// 转发请求 - 根据账号平台分流
	b.c.Set("parsed_request", b.attemptParsedReq)

	requestCtx := b.c.Request.Context()
	if state.SwitchCount > 0 {
		requestCtx = requeststate.WithAccountSwitchCount(requestCtx, state.SwitchCount)
	}
	if state.ForceCacheBilling {
		// 将故障转移后的缓存计费语义传给同步响应改写逻辑。
		requestCtx = requeststate.WithForceCacheBilling(requestCtx)
	}
	// 记录 Forward 前已写入字节数，Forward 后若增加则说明 SSE 内容已发，禁止 failover
	b.writerSizeBeforeForward = b.c.Writer.Size()
	if b.account.Record.Platform == capability.PlatformAntigravity && b.account.Record.Type != capability.AccountTypeAPIKey {
		b.result, err = b.binding().forwardAntigravity(requestCtx, b.c, b.account, attemptBody, b.hasBoundSession)
	} else {
		b.result, err = b.binding().forwardMessages(requestCtx, b.c, b.account, b.attemptParsedReq)
	}

	// 兜底释放串行锁（正常情况已通过回调提前释放）
	if queueRelease != nil {
		queueRelease()
	}
	// 清理回调引用，防止 failover 重试时旧回调被错误调用
	b.attemptParsedReq.OnUpstreamAccepted = nil

	if b.accountReleaseFunc != nil {
		b.accountReleaseFunc()
	}
	b.binding().reportSchedule(b.selection, b.account.Record.ID, err == nil, b.result)

	out := textflow.Outcome{Attempt: messageObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil, OutputChanged: b.c.Writer.Size() != b.writerSizeBeforeForward}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	out.Attempt.RetryCommitted = out.OutputChanged
	var policy *anthropic.BetaBlockedError
	var prompt *antigravity.PromptTooLongError
	var retry *forwardcore.UpstreamFailoverError
	switch {
	case errors.As(err, &policy):
		out.Kind = textflow.FailurePolicy
	case errors.As(err, &prompt):
		out.Kind = textflow.FailurePromptTooLong
	case errors.As(err, &retry):
		out.Failure = &textflow.AttemptFailure{Cause: retry, Policy: retry.RetryFailure()}
	}
	return out

}

// Complete 只执行一次适配操作，重试与分组回退循环由 gateway/text 拥有。
func (b *messageAttemptBridge) Complete(state textflow.AttemptState) {
	usageResult := b.result

	if usageResult == nil {
		return
	}
	gatewayhttp.StampForwardRequestedReasoningEffort(usageResult, b.c)
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(b.c)
	requestPayloadHash := billing.HashUsageRequestPayload(b.attemptParsedReq.Body.Bytes())
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(b.c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(b.c, b.account.Record.Platform)

	if usageResult.ReasoningEffort == nil {
		usageResult.ReasoningEffort = protocol.NormalizeClaudeOutputEffort(b.attemptParsedReq.OutputEffort)
	}
	if usageResult.ReasoningEffort == nil && b.attemptParsedReq.ThinkingEnabled {
		protocolModel := usageResult.UpstreamModel
		if protocolModel == "" {
			protocolModel = usageResult.Model
		}
		usageResult.ReasoningEffort = gatewaycapture.DefaultEffortForThinkingEnabled(protocolModel)
	}

	// ForceCacheBilling 提前拍成标量，避免 worker 闭包保活 failover 状态里的响应体。
	forceCacheBilling := state.ForceCacheBilling
	quotaPlatform := admission.QuotaPlatform(b.c.Request.Context(), b.currentAPIKey)
	clientSessionID := gatewayhttp.ExtractClientSessionID(b.c)
	// 入队前固化资金与报文投影，worker 不再读取请求中的实体。
	completionInput := gatewaycapture.CaptureMessages(gatewayhttp.CompletionContext(b.c), &gatewaycapture.MessagesCapture{
		Result:             usageResult,
		QuotaPlatform:      quotaPlatform,
		APIKey:             b.currentAPIKey,
		User:               b.currentAPIKey.User,
		Account:            gatewaycapture.ExecutionCompletionRecord(b.account),
		Subscription:       b.currentSubscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		ClientSessionID:    clientSessionID,
		RequestPayloadHash: requestPayloadHash,
		RequestBody:        append([]byte(nil), b.body...),
		ForceCacheBilling:  forceCacheBilling,
		APIKeyService:      b.binding().apiKeyService,
		ChannelUsageFields: b.attemptChannelMapping.ToUsageFields(b.reqModel, usageResult.UpstreamModel),
	})
	completionUserID := b.subject.UserID
	completionModel := b.reqModel
	completionRuntime := b.binding().recorder
	b.binding().submitUsageRecordTask(b.c, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
			logging.L().With(
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

// Fallback 只执行一次适配操作，重试与分组回退循环由 gateway/text 拥有。
func (b *messageAttemptBridge) Fallback(err error, fallbackUsed bool) bool {
	var promptTooLongErr *antigravity.PromptTooLongError
	if errors.As(err, &promptTooLongErr) {
		b.reqLog.Warn("gateway.prompt_too_long_from_antigravity",
			zap.Any("current_group_id", b.currentAPIKey.GroupID),
			zap.Any("fallback_group_id", b.fallbackGroupID),
			zap.Bool("fallback_used", fallbackUsed),
		)
		if !fallbackUsed && b.fallbackGroupID != nil && *b.fallbackGroupID > 0 {
			fallbackGroup, err := b.binding().resolveGroup(b.c.Request.Context(), *b.fallbackGroupID)
			if err != nil {
				b.reqLog.Warn("gateway.resolve_fallback_group_failed", zap.Int64("fallback_group_id", *b.fallbackGroupID), zap.Error(err))
				_ = b.binding().writeMappedClaudeError(b.c, b.account, promptTooLongErr.StatusCode, promptTooLongErr.RequestID, promptTooLongErr.Body)
				return false
			}
			if fallbackGroup.Platform != capability.PlatformAnthropic ||
				fallbackGroup.FallbackGroupIDOnInvalidRequest != nil {
				b.reqLog.Warn("gateway.fallback_group_invalid",
					zap.Int64("fallback_group_id", fallbackGroup.ID),
					zap.String("fallback_platform", fallbackGroup.Platform),
				)
				_ = b.binding().writeMappedClaudeError(b.c, b.account, promptTooLongErr.StatusCode, promptTooLongErr.RequestID, promptTooLongErr.Body)
				return false
			}
			fallbackAPIKey := cloneAPIKeyWithGroup(b.apiKey, fallbackGroup)
			fallbackSubscription := (*billing.UserSubscription)(nil)
			if apikey.APIKeyEffectiveBillingMode(fallbackAPIKey) == apikey.APIKeyBillingModeSubscription {
				// 指定订阅的回退分组必须继续使用同一订阅，由资格检查再次验证套餐分组范围。
				fallbackSubscription = b.currentSubscription
			}
			if err := b.binding().billingCheck(b.c.Request.Context(), fallbackAPIKey, fallbackSubscription, admission.PlatformFromAPIKey(fallbackAPIKey), false); err != nil {
				status, code, message, retryAfter := gatewayhttp.BillingErrorDetails(err)
				if retryAfter > 0 {
					b.c.Header("Retry-After", strconv.Itoa(retryAfter))
				}
				b.binding().handleStreamingAwareError(b.c, status, code, message, (*b.streamStarted))
				return false
			}
			// 兜底重试按"直接请求兜底分组"处理：清除强制平台，允许按分组平台调度
			ctx := apikey.WithForcePlatform(b.c.Request.Context(), "")
			// 后续转发和用量计算必须读取兜底分组，而不是中间件写入的原分组。
			ctx = requeststate.WithGroup(ctx, fallbackGroup)
			b.c.Request = b.c.Request.WithContext(ctx)
			b.currentAPIKey = fallbackAPIKey
			b.currentSubscription = fallbackSubscription

			b.sessionAttempts.Reset()
			return true
		}
		_ = b.binding().writeMappedClaudeError(b.c, b.account, promptTooLongErr.StatusCode, promptTooLongErr.RequestID, promptTooLongErr.Body)
		return false
	}
	return false
}

// OtherFailure 只执行一次适配操作，重试与分组回退循环由 gateway/text 拥有。
func (b *messageAttemptBridge) OtherFailure(err error) {
	upstreamErrorAlreadyCommunicated := gatewayhttp.ForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	wroteFallback := false
	if !upstreamErrorAlreadyCommunicated {
		wroteFallback = b.binding().ensureForwardErrorResponse(b.c, (*b.streamStarted))
	}
	forwardFailedFields := []zap.Field{
		zap.Int64("account_id", b.account.Record.ID),
		zap.String("account_name", b.account.Record.Name),
		zap.String("account_platform", b.account.Record.Platform),
		zap.Bool("fallback_error_response_written", wroteFallback),
		zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
		zap.Error(err),
	}
	if b.account.Record.Proxy != nil {
		forwardFailedFields = append(forwardFailedFields,
			zap.Int64("proxy_id", b.account.Record.Proxy.ID),
			zap.String("proxy_name", b.account.Record.Proxy.Name),
			zap.String("proxy_host", b.account.Record.Proxy.Host),
			zap.Int("proxy_port", b.account.Record.Proxy.Port),
		)
	} else if b.account.Record.ProxyID != nil {
		forwardFailedFields = append(forwardFailedFields, zap.Int64p("proxy_id", b.account.Record.ProxyID))
	}
	b.reqLog.Error("gateway.forward_failed", forwardFailedFields...)
}

// Success 只执行一次适配操作，重试与分组回退循环由 gateway/text 拥有。
func (b *messageAttemptBridge) Success() {
	// RPM 计数递增（Forward 成功后）
	// 注意：TOCTOU 竞态是已知且可接受的设计权衡，与 WindowCost 一致的 soft-limit 模式。
	// 在高并发下可能短暂超出 RPM 限制，但不会导致请求失败。
	if b.account.View().IsAnthropicOAuthOrSetupToken() && gatewaycapture.ExecutionRuntimeConfig(b.account).GetBaseRPM() > 0 {
		if err := b.binding().incrementRPM(b.c.Request.Context(), b.account.Record.ID); err != nil {
			b.reqLog.Warn("gateway.rpm_increment_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
		}
	}

	// 绑定粘性会话（成功转发后绑定/刷新）
	// - 无现有绑定（首次请求）：创建绑定
	// - 选中账号与粘性账号一致：刷新 TTL
	// - 粘性账号因负载/RPM 被跳过、选中了其他账号：不覆盖原绑定，
	//   下次请求粘性账号恢复后仍可命中
	if b.sessionKey != "" && (b.sessionBoundAccountID == 0 || b.sessionBoundAccountID == b.account.Record.ID) {
		if err := b.binding().bindSticky(b.c.Request.Context(), b.currentAPIKey.GroupID, b.sessionKey, b.account.Record.ID); err != nil {
			b.reqLog.Warn("gateway.bind_sticky_session_failed", zap.Int64("account_id", b.account.Record.ID), zap.Error(err))
		}
	}

}

func (b *messageAttemptBridge) Context() context.Context { return b.c.Request.Context() }
func (b *messageAttemptBridge) Begin() {
	b.currentAPIKey = b.apiKey
	b.currentSubscription = b.subscription
	if b.apiKey.Group != nil {
		b.fallbackGroupID = b.apiKey.Group.FallbackGroupIDOnInvalidRequest
	}
	b.sessionAttempts = b.binding().newSessionAttempts()
	if b.binding().singleAccountGroup(b.Context(), b.currentAPIKey.GroupID) {
		b.SingleAccountRetry()
	}
}
func (b *messageAttemptBridge) Finish(served bool) {
	if b.sessionAttempts != nil {
		b.sessionAttempts.Finish(scheduler.AttemptOutcome{Served: served})
	}
}
func (b *messageAttemptBridge) SingleAccountRetry() {
	b.c.Request = b.c.Request.WithContext(requeststate.WithSingleAccountRetry(b.Context(), true))
}
func (b *messageAttemptBridge) Canceled() { gatewayhttp.FailoverClientGone(b.c) }
func (b *messageAttemptBridge) Exhausted(err *textflow.AttemptFailure, platform string, forceStream bool) {
	if platform == "" {
		platform = b.platform
	}
	if err == nil {
		b.binding().handleFailoverExhaustedSimple(b.c, 502, *b.streamStarted)
		return
	}
	var original *forwardcore.UpstreamFailoverError
	if errors.As(err.Cause, &original) {
		b.binding().handleFailoverExhausted(b.c, original, platform, forceStream || *b.streamStarted)
	}
}
func (b *messageAttemptBridge) PolicyFailure(err error) {
	var original *anthropic.BetaBlockedError
	if errors.As(err, &original) {
		gatewayhttp.MarkOpsClientBusinessLimited(b.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		b.binding().errorResponse(b.c, http.StatusBadRequest, "invalid_request_error", original.Message)
	}
}
func (b *messageAttemptBridge) Switched() {
	b.binding().accountSwitched(b.selection)
}
func (b *messageAttemptBridge) Abandon(id int64) { b.sessionAttempts.Abandon(id) }
func (b *messageAttemptBridge) TempUnscheduleRetryableError(ctx context.Context, id int64, err *textflow.AttemptFailure) {
	var original *forwardcore.UpstreamFailoverError
	if errors.As(err.Cause, &original) {
		b.binding().tempUnschedule(ctx, id, original)
	}
}
