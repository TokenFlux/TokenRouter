package ws

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/tidwall/gjson"
)

// RunEntry 拥有入站升级后的准入、账号尝试及每 turn 调度/资金/审核/完成编排。
func RunEntry(ctx context.Context, p EntryPorts, in EntryInput, client ClientSocket, firstMessage []byte) {
	apiKey, subject, reqLog := in.Key, in.Subject, p.Logger()
	clientLifecycleCtx, firstTurnStartedAt := in.ClientLifecycleContext, in.FirstTurnStartedAt
	var err error
	// 用户提示词替换必须在首帧模型解析、内容审计和会话 hash 前执行，保证 WS 首轮请求与 HTTP 入口一致。
	firstMessage = p.Prompt(ctx, firstMessage)
	firstMessage, err = modeltrace.RewriteAPIKeyAdditionalModels(firstMessage, apiKey.ModelMapping)
	if err != nil {
		p.Close(1008, "invalid websocket tool model")
		return
	}

	reqModel := strings.TrimSpace(gjson.GetBytes(firstMessage, "model").String())
	if reqModel == "" {
		p.Close(1008, "model is required in first response.create payload")
		return
	}
	clientReqModel := reqModel
	ctx, reqModel = p.Redirect(ctx, clientReqModel)
	p.BindContext(ctx)
	previousResponseID := strings.TrimSpace(gjson.GetBytes(firstMessage, "previous_response_id").String())
	previousResponseIDKind := p.ClassifyPrevious(previousResponseID)
	if previousResponseID != "" && previousResponseIDKind == "message_id" {
		p.Close(1008, "previous_response_id must be a response.id (resp_*), not a message id")
		return
	}
	firstMessageToolCoverage := wire.AnalyzeToolCallOutputContextCoverageBytes(firstMessage)
	previousResponseCanMove := !firstMessageToolCoverage.HasFunctionCallOutput || firstMessageToolCoverage.ContextCoversAllCallIDs
	reqLog = reqLog.With(
		EntryBool("ws_ingress", true),
		EntryString("model", clientReqModel),
		EntryBool("has_previous_response_id", previousResponseID != ""),
		EntryString("previous_response_id_kind", previousResponseIDKind),
	)
	p.SetLogger(reqLog)
	p.ObserveFirst(clientReqModel)
	firstCyber := p.CaptureCyber(firstMessage)
	// WS passthrough 的客户端帧和上游事件可能并发回调，按 turn 保存提示词摘要需要加锁。
	cyberPromptExcerptByTurn := map[int]string{}
	cyberSnapshotByTurn := map[int]moderation.ContentModerationInput{}
	var cyberPromptExcerptMu sync.RWMutex
	setCyberPromptExcerpt := func(turn int, promptExcerpt string, snapshot moderation.ContentModerationInput) {
		if turn <= 0 {
			return
		}
		cyberPromptExcerptMu.Lock()
		cyberPromptExcerptByTurn[turn] = strings.TrimSpace(promptExcerpt)
		cyberSnapshotByTurn[turn] = snapshot
		cyberPromptExcerptMu.Unlock()
	}
	getCyberSnapshot := func(turn int) moderation.ContentModerationInput {
		cyberPromptExcerptMu.RLock()
		defer cyberPromptExcerptMu.RUnlock()
		return cyberSnapshotByTurn[turn]
	}
	getCyberPromptExcerpt := func(turn int) string {
		cyberPromptExcerptMu.RLock()
		defer cyberPromptExcerptMu.RUnlock()
		return cyberPromptExcerptByTurn[turn]
	}
	clearCyberPromptExcerpt := func(turn int) {
		cyberPromptExcerptMu.Lock()
		delete(cyberPromptExcerptByTurn, turn)
		delete(cyberSnapshotByTurn, turn)
		cyberPromptExcerptMu.Unlock()
	}
	setCyberPromptExcerpt(1, firstCyber.Excerpt, firstCyber.Input)

	if decision := p.Moderate(ctx, reqModel, firstMessage); decision != nil && decision.Blocked {
		p.ModerationError(ctx, decision)
		p.Close(1008, decision.Message)
		return
	}
	// 首帧已经完整可用，先检查显式会话及其派生会话是否被风控屏蔽，再建立上游连接。
	if cyberBlockKey := p.BlockedSession(ctx, firstMessage); cyberBlockKey != "" {
		p.BlockedError(ctx)
		p.Close(1008, p.BlockedMessage())
		p.BlockedOps(reqModel, cyberBlockKey)
		return
	}

	// 首轮账号选择必须按分组映射模型 G 判断生图能力，避免别名映射绕过 Responses 能力检查。
	// 当前分组和分组映射结果进入独立计划，不改变原解析位置。
	ctx, groupMappingWS := p.Plan(ctx, reqModel)
	mappedFirstMessage, routingModelWS, _ := p.ImageIntent(reqModel, firstMessage, groupMappingWS)
	imageIntent := p.ExplicitImage(routingModelWS, mappedFirstMessage)
	initialSchedulingCtx := ctx
	if imageIntent {
		// 首轮账号选择也要遵守显式生图请求的模型级限流。
		initialSchedulingCtx = p.ImageContext(initialSchedulingCtx)
	}
	if imageIntent && !p.ImagesAllowed() {
		p.FeatureDenied()
		p.Close(1008, p.ImageDeniedMessage())
		return
	}

	var currentUserRelease func()
	var currentAccountRelease func()
	releaseAccountSlot := func() {
		if currentAccountRelease != nil {
			currentAccountRelease()
			currentAccountRelease = nil
		}
	}
	releaseTurnSlots := func() {
		releaseAccountSlot()
		if currentUserRelease != nil {
			currentUserRelease()
			currentUserRelease = nil
		}
	}
	// 必须尽早注册，确保任何 early return 都能释放已获取的并发槽位。
	defer releaseTurnSlots()

	userReleaseFunc, userAcquired, err := p.AcquireUser(ctx)
	if err != nil {
		reqLog.Warn("openai.websocket_user_slot_acquire_failed", EntryError(err))
		p.Close(1011, "failed to acquire user concurrency slot")
		return
	}
	if !userAcquired {
		p.Close(1013, "too many concurrent requests, please retry later")
		return
	}
	currentUserRelease = p.WrapRelease(ctx, userReleaseFunc)
	ensureUserSlotHeld := func() bool {
		if currentUserRelease != nil {
			return true
		}
		userReleaseFunc, userAcquired, err := p.AcquireUser(ctx)
		if err != nil {
			reqLog.Warn("openai.websocket_user_slot_reacquire_failed", EntryError(err))
			p.Close(1011, "failed to acquire user concurrency slot")
			return false
		}
		if !userAcquired {
			p.Close(1013, "too many concurrent requests, please retry later")
			return false
		}
		currentUserRelease = p.WrapRelease(ctx, userReleaseFunc)
		return true
	}

	p.LoadSubscription()
	requestPlatform := p.Platform()
	if err := p.Eligibility(ctx); err != nil {
		reqLog.Info("openai.websocket_billing_eligibility_check_failed", EntryError(err))
		p.Close(1008, "billing check failed")
		return
	}

	sessionHash := p.SessionHash(
		firstMessage,
		EntryFallbackSeed(subject.UserID, apiKey.ID, apiKey.GroupID),
	)
	var cyberBlockedThisConn atomic.Bool
	explicitSessionHash := p.ExplicitHash(firstMessage)
	if explicitSessionHash != "" {
		if err := p.Isolate(ctx, "openai", explicitSessionHash); err != nil {
			p.IsolationError(ctx, err)
			p.Close(1008, p.IsolationReason(err))
			return
		}
	}
	if previousResponseID != "" {
		previousResponseHash := entrySeedHash(previousResponseID)
		if err := p.Isolate(ctx, "openai_previous_response", previousResponseHash); err != nil {
			p.IsolationError(ctx, err)
			p.Close(1008, p.IsolationReason(err))
			return
		}
	}
	ctx = p.Guardian(ctx, firstMessage, reqModel)
	maxAccountSwitches := in.MaxAccountSwitches
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	var lastFailoverErr *EntryFailure
	var oauth429FailoverState failover.OAuth429State
	wsAttemptMessage := append([]byte(nil), firstMessage...)
	handleWSFailover := func(selection *EntrySelection, account *EntryAccount, failoverErr *EntryFailure) bool {
		if ctx.Err() != nil {
			return false
		}
		if failoverErr.ReportScheduleFailure {
			selection.Target.Report(selection.Target.MappedModel(groupMappingWS.MappedModel), false, nil)
		}
		releaseAccountSlot()
		if !failoverErr.RetryNext {
			p.CloseFailover(failoverErr)
			return false
		}
		if ctx.Err() != nil {
			return false
		}
		selection.Target.Switched()
		failedAccountIDs[account.ID] = struct{}{}
		lastFailoverErr = failoverErr
		if switchCount >= maxAccountSwitches {
			p.CloseFailover(failoverErr)
			return false
		}
		switchCount++
		if selection.Target.Stop429(failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
			p.CloseFailover(failoverErr)
			return false
		}
		reqLog.Warn("openai.websocket_upstream_failover_switching",
			EntryInt64("account_id", account.ID),
			EntryInt("upstream_status", failoverErr.StatusCode),
			EntryInt("switch_count", switchCount),
			EntryInt("max_switches", maxAccountSwitches),
		)
		if ctx.Err() != nil {
			return false
		}
		return ensureUserSlotHeld()
	}

	// 与 HTTP Responses 路径保持一致：生图意图请求要求账号支持 Responses API（#4417）。
	// WSv2 传输本身已隐含 Responses 支持，此处为防御性对齐。
	// 首轮显式意图已按分组映射模型 G 判断，被动 namespace 不会误过滤账号（#4476）。
	requiredCapability := imageIntent && requestPlatform == "openai"

	for {
		if ctx.Err() != nil {
			return
		}
		reqLog.Debug("openai.websocket_account_selecting", EntryInt("excluded_account_count", len(failedAccountIDs)))
		selection, scheduleDecision, err := p.Select(initialSchedulingCtx, previousResponseID, sessionHash, reqModel, failedAccountIDs, requiredCapability, previousResponseCanMove, requestPlatform)
		if err != nil {
			reqLog.Warn("openai.websocket_account_select_failed",
				EntryError(p.SelectionLogError(err, requestPlatform)),
				EntryInt("excluded_account_count", len(failedAccountIDs)),
			)
			if lastFailoverErr != nil {
				p.CloseFailover(lastFailoverErr)
			} else {
				p.Close(1013, "no available account")
			}
			return
		}
		if selection == nil || selection.Account == nil {
			if lastFailoverErr != nil {
				p.CloseFailover(lastFailoverErr)
			} else {
				p.Close(1013, "no available account")
			}
			return
		}

		account := selection.Account
		accountMaxConcurrency := account.Concurrency
		if selection.WaitPlan != nil && selection.WaitPlan.MaxConcurrency > 0 {
			accountMaxConcurrency = selection.WaitPlan.MaxConcurrency
		}
		accountReleaseFunc := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				p.Close(1013, "account is busy, please retry later")
				return
			}
			fastReleaseFunc, fastAcquired, err := p.AcquireAccount(
				ctx,
				account.ID,
				selection.WaitPlan.MaxConcurrency,
			)
			if err != nil {
				reqLog.Warn("openai.websocket_account_slot_acquire_failed", EntryInt64("account_id", account.ID), EntryError(err))
				p.Close(1011, "failed to acquire account concurrency slot")
				return
			}
			if !fastAcquired {
				p.Close(1013, "account is busy, please retry later")
				return
			}
			accountReleaseFunc = fastReleaseFunc
		}
		currentAccountRelease = p.WrapRelease(ctx, accountReleaseFunc)
		if err := p.BindSticky(ctx, sessionHash, account.ID); err != nil {
			reqLog.Warn("openai.websocket_bind_sticky_session_failed", EntryInt64("account_id", account.ID), EntryError(err))
		}

		err = selection.Target.Credential(ctx)
		if err != nil {
			reqLog.Warn("openai.websocket_get_access_token_failed", EntryInt64("account_id", account.ID), EntryError(err))
			if ctx.Err() != nil {
				return
			}
			if failoverErr, ok := p.Failover(err); ok {
				if handleWSFailover(selection, account, failoverErr) {
					continue
				}
				return
			}
			p.Close(1011, "failed to get access token")
			return
		}
		if err := selection.Target.EnforceClient(ctx, firstMessage); err != nil {
			reqLog.Warn("openai.websocket_client_policy_rejected", EntryInt64("account_id", account.ID), EntryError(err))
			p.Close(1008, "client is not allowed")
			return
		}

		reqLog.Debug("openai.websocket_account_selected",
			EntryInt64("account_id", account.ID),
			EntryString("account_name", account.Name),
			EntryString("schedule_layer", scheduleDecision.Layer),
			EntryInt("candidate_count", scheduleDecision.CandidateCount),
		)

		// 首帧保持客户端模型 R，由 service 层与后续 turn 一样逐轮执行 R -> G -> U。
		wsFirstMessageForUsageFallback := append([]byte(nil), firstMessage...)
		// 每轮通过现有鉴权缓存刷新策略；刷新失败时沿用最近一次有效值，避免瞬时故障中断长连接。
		var currentFastModePolicy atomic.Value
		currentFastModePolicy.Store(apiKey.FastModePolicy)
		maxReasoningEffort := ""
		maxReasoningEffortOverLimit := ""
		var reasoningEffortMappings []routing.ReasoningEffortMapping
		if apiKey.Group != nil && apiKey.Group.Platform == "openai" {
			maxReasoningEffort = apiKey.Group.MaxReasoningEffort
			maxReasoningEffortOverLimit = apiKey.Group.MaxReasoningEffortOverLimit
			reasoningEffortMappings = apiKey.Group.ReasoningEffortMappings
		}
		hooks := &EntryHooks{
			ClientLifecycleContext:      clientLifecycleCtx,
			InitialRequestModel:         reqModel,
			InitialTurnStartedAt:        firstTurnStartedAt,
			MaxReasoningEffort:          maxReasoningEffort,
			MaxReasoningEffortOverLimit: maxReasoningEffortOverLimit,
			ReasoningEffortMappings:     reasoningEffortMappings,
			ResolveFastModePolicy: func(_ int) string {
				fallback, _ := currentFastModePolicy.Load().(string)
				if !apiKey.RefreshFastPolicy {
					return fallback
				}
				policy, ok := p.RefreshFast(ctx)
				if !ok {
					return fallback
				}

				currentFastModePolicy.Store(policy)
				return policy
			},
			ResolveRoutingModel: func(_ int, requestedModel string, payload []byte) (string, error) {
				requestedModel = strings.TrimSpace(requestedModel)
				if requestedModel == "" {
					requestedModel = clientReqModel
				}
				turnCtx, redirectedModel := p.Redirect(ctx, requestedModel)
				// 当前分组和分组映射结果进入独立计划，不改变原解析位置。
				turnCtx, turnMapping := p.Plan(turnCtx, redirectedModel)
				mappedPayload, turnRoutingModel, _ := p.ImageIntent(redirectedModel, payload, turnMapping)
				turnImageIntent := p.ExplicitImage(turnRoutingModel, mappedPayload)

				if turnImageIntent && !p.ImagesAllowed() {
					p.FeatureDenied()
					return "", p.CloseError(
						1008,
						p.ImageDeniedMessage(),
						nil,
					)
				}
				if turnImageIntent {
					// 后续 turn 的账号资格检查必须包含该轮生图限流范围。
					turnCtx = p.ImageContext(turnCtx)
				}
				turnCapability := turnImageIntent && requestPlatform == "openai"
				routingModel, resolveErr := selection.Target.ResolveRouting(turnCtx, redirectedModel, turnCapability)
				if resolveErr != nil {
					return "", p.CloseError(1008, EntryLocalRoutingReason(redirectedModel), EntryLocalRoutingCause(resolveErr))
				}
				return routingModel, nil
			},
			BeforeRequest: func(turn int, payload []byte, originalModel, _ string) ([]byte, error) {
				if turn == 1 {
					return payload, nil
				}
				if !gjson.ValidBytes(payload) {
					return payload, p.CloseError(1008, "invalid websocket request payload", errors.New("invalid json"))
				}
				rewrittenPayload, rewriteErr := modeltrace.RewriteAPIKeyAdditionalModels(payload, apiKey.ModelMapping)
				if rewriteErr != nil {
					return payload, p.CloseError(1008, "invalid websocket tool model", rewriteErr)
				}
				payload = rewrittenPayload
				payloadPreviousResponseID := strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String())
				model := strings.TrimSpace(originalModel)
				if model == "" {
					model = strings.TrimSpace(gjson.GetBytes(payload, "model").String())
				}
				if model == "" {
					model = clientReqModel
				}
				_, model = p.Redirect(ctx, model)
				snapshot := p.CaptureCyber(payload)
				setCyberPromptExcerpt(turn, snapshot.Excerpt, snapshot.Input)
				if decision := p.Moderate(ctx, model, payload); decision != nil && decision.Blocked {
					p.ModerationError(ctx, decision)
					return payload, p.CloseError(1008, decision.Message, nil)
				}
				if payloadPreviousResponseID != "" {
					previousResponseHash := entrySeedHash(payloadPreviousResponseID)
					if err := p.Isolate(ctx, "openai_previous_response", previousResponseHash); err != nil {
						p.IsolationError(ctx, err)
						return payload, p.CloseError(1008, p.IsolationReason(err), err)
					}
				}
				if explicitHash := p.ExplicitHash(payload); explicitHash != "" {
					if err := p.Isolate(ctx, "openai", explicitHash); err != nil {
						p.IsolationError(ctx, err)
						return payload, p.CloseError(1008, p.IsolationReason(err), err)
					}
				}
				return payload, nil
			},
			BeforeTurn: func(turn int) error {
				if cyberBlockedThisConn.Load() {
					return p.CloseError(1008, p.BlockedMessage(), nil)
				}
				if turn == 1 {
					return nil
				}
				// 防御式清理：避免异常路径下旧槽位覆盖导致泄漏。
				releaseTurnSlots()
				// 非首轮 turn 需要重新抢占并发槽位，避免长连接空闲占槽。
				userReleaseFunc, userAcquired, err := p.AcquireUser(ctx)
				if err != nil {
					return p.CloseError(1011, "failed to acquire user concurrency slot", err)
				}
				if !userAcquired {
					return p.CloseError(1013, "too many concurrent requests, please retry later", nil)
				}
				accountReleaseFunc, accountAcquired, err := p.AcquireAccount(ctx, account.ID, accountMaxConcurrency)
				if err != nil {
					if userReleaseFunc != nil {
						userReleaseFunc()
					}
					return p.CloseError(1011, "failed to acquire account concurrency slot", err)
				}
				if !accountAcquired {
					if userReleaseFunc != nil {
						userReleaseFunc()
					}
					return p.CloseError(1013, "account is busy, please retry later", nil)
				}
				currentUserRelease = p.WrapRelease(ctx, userReleaseFunc)
				currentAccountRelease = p.WrapRelease(ctx, accountReleaseFunc)
				return nil
			},
			OnUpstreamError: func(turn int, originalModel string, statusCode int, responseBody []byte, warningText string) {
				model := strings.TrimSpace(originalModel)
				if model == "" {
					model = clientReqModel
				}
				selection.Target.Warning(ctx, model, statusCode, responseBody, warningText, EntryCyberSnapshot{Excerpt: getCyberPromptExcerpt(turn), Input: getCyberSnapshot(turn)})
			},
			AfterTurn: func(capture TurnCapture) {
				turn := capture.Turn
				result := capture.Result
				turnErr := capture.Err
				turnClientModel := strings.TrimSpace(capture.OriginalModel)
				if turnClientModel == "" {
					turnClientModel = clientReqModel
				}
				turnCtx, turnModel := p.Redirect(ctx, turnClientModel)
				// 当前分组和分组映射结果进入独立计划，不改变原解析位置。
				turnCtx, turnGroupMapping := p.Plan(turnCtx, turnModel)
				releaseTurnSlots()
				defer p.ClearCyber()
				turnRequestBodyForCyber := capture.RequestBody
				if len(turnRequestBodyForCyber) == 0 {
					turnRequestBodyForCyber = wsFirstMessageForUsageFallback
				}
				cyberPolicyHandled := selection.Target.RecordMarked(turnCtx, turnModel, turnErr != nil, turnRequestBodyForCyber, turnGroupMapping.ToUsageFields(turnModel, ""), billing.HashUsageRequestPayload(turnRequestBodyForCyber))

				if cyberPolicyHandled {
					cyberBlockedThisConn.Store(true)
				}
				defer clearCyberPromptExcerpt(turn)
				if turnErr != nil {
					if result == nil || result.ImageCount <= 0 {
						return
					}
					if cyberPolicyHandled {
						return
					}
					reqLog.Warn("openai.websocket_partial_error_with_image_result",
						EntryInt64("account_id", account.ID),
						EntryInt("image_count", result.ImageCount),
						EntryError(turnErr),
					)
				}
				if result == nil {
					return
				}
				// WS 每个 turn 的分组映射可能覆盖默认计费模型，统一在记录用量前解析。
				result.BillingModel = EntryBillingModel(result, turnGroupMapping, turnModel, result.UpstreamModel)
				// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
				if account.Type == "oauth" && !account.Shadow {
					selection.Target.UpdateUsage(ctx, result.ResponseHeaders)
				}
				scheduleModel := strings.TrimSpace(result.UpstreamModel)
				if scheduleModel == "" {
					scheduleModel = selection.Target.MappedModel(turnGroupMapping.MappedModel)
				}
				selection.Target.Report(scheduleModel, entrySucceeded(result), result.FirstTokenMs)

				turnRequestBody := capture.RequestBody
				if len(turnRequestBody) == 0 {
					turnRequestBody = wsFirstMessageForUsageFallback
				}
				completionInput := selection.Target.PrepareCompletion(turnCtx, result, capture, turnModel, turnGroupMapping, turnRequestBody, cyberPolicyHandled)
				recorder, report := p.CompletionRecorder(), p.CompletionObserver()
				p.SubmitCompletion(result, func(taskCtx context.Context) {
					if err := recorder.Record(taskCtx, completionInput, true); err != nil {
						report(completionInput.Account.ID, completionInput.Result.RequestID, err)
					}
				})
			},
		}

		// 原生 WS turn 执行器在解析首帧时执行分组及账号映射，此处只处理会话链字段。
		wsFirstMessage := append([]byte(nil), wsAttemptMessage...)
		// 切组/会话失配防护：previous_response_id 未在当前分组命中粘连账号时，
		// 说明该会话链不属于本次调度到的账号；原样转发会触发上游会话链鉴权失败。
		// 因此只在上下文可迁移时剥离首包 previous_response_id，后续 turn 仍由 WS 转发层处理。
		if previousResponseID != "" && !scheduleDecision.StickyPreviousHit && previousResponseCanMove {
			wsFirstMessage = p.RemovePrevious(wsFirstMessage)
			reqLog.Debug("openai.websocket_previous_response_id_stripped_cross_group",
				EntryInt64("account_id", account.ID),
				EntryString("schedule_layer", scheduleDecision.Layer),
			)
		}

		if preemptCtx, cleanupPreempt, armed := selection.Target.BeginPreemption(ctx, wsFirstMessage); armed {
			ctx = preemptCtx
			defer cleanupPreempt()
		}

		if err := selection.Target.Run(ctx, client, wsFirstMessage, hooks); err != nil {
			if p.SessionPreempted(err) {
				return
			}
			if failoverErr, ok := p.Failover(err); ok {
				retryPayload, retryCurrentTurn := CurrentTurnRetryPayload(err)
				nextAttemptMessage, retrySafe := EntryNextAttemptMessage(wsAttemptMessage, retryPayload, retryCurrentTurn)
				if !retrySafe {
					p.CloseFailover(failoverErr)
					return
				}
				wsAttemptMessage = nextAttemptMessage
				if retryCurrentTurn {
					previousResponseID = ""
					reqLog.Warn("openai.websocket_current_turn_failover_retry",
						EntryInt64("account_id", account.ID),
						EntryInt("upstream_status", failoverErr.StatusCode),
						EntryInt("retry_payload_bytes", len(retryPayload)),
					)
				}
				if handleWSFailover(selection, account, failoverErr) {
					continue
				}
				return
			}

			if errors.Is(context.Cause(ctx), scheduler.ErrOpenAIWSIngressLeaseLost) {
				reqLog.Warn("openai.websocket_ingress_lease_lost",
					EntryInt64("account_id", account.ID),
					EntryError(err),
				)
				p.Close(1013, "websocket ingress capacity lease lost; please reconnect")
				return
			}

			closeErr := p.CloseInfo(err)
			hasClientCloseErr := closeErr.Present
			if p.LocalPolicyError(err) {
				p.PolicyDenied()
			}

			if p.EndedByClient(err) {
				closedFields := []EntryField{EntryInt64("account_id", account.ID)}
				if hasClientCloseErr {
					closedFields = append(closedFields, EntryString("reason", closeErr.Reason))
				} else {
					closedFields = append(closedFields, EntryError(err))
				}
				reqLog.Info("openai.websocket_ingress_closed_normally", closedFields...)
				// A bare coderws.CloseError or a plain cancellation carries no
				// gateway-chosen close frame; mirror the client's clean 1000
				// rather than the 1011 the proxy-failure tail would have sent.
				if hasClientCloseErr {
					p.Close(closeErr.Status, closeErr.Reason)
				} else {
					p.Close(1000, "")
				}
				return
			}

			if p.ReportFailure(err) {
				selection.Target.Report(selection.Target.MappedModel(groupMappingWS.MappedModel), false, nil)
			}
			selection.Target.LogFailure(err)
			if hasClientCloseErr {
				p.Close(closeErr.Status, closeErr.Reason)
				return
			}
			p.Close(1011, "upstream websocket proxy failed")
			return
		}
		reqLog.Info("openai.websocket_ingress_closed", EntryInt64("account_id", account.ID))
		return
	}
}
