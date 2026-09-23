package ws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Run 保留双向 relay，在同一核心中编排每轮策略、完成和失败边界。
func (s *PassthroughSession) Run(ctx context.Context, clientConn ClientSocket, firstClientMessage []byte) error {
	p, o, hooks := s.Port, s.Options, s.Hooks
	firstTurnStartedAt := time.Now()
	if hooks != nil && !hooks.InitialTurnStartedAt.IsZero() {
		firstTurnStartedAt = hooks.InitialTurnStartedAt
	}
	if hooks != nil && hooks.TurnStarted != nil {
		hooks.TurnStarted(1, firstTurnStartedAt)
	}
	if p.IsLite(firstClientMessage) {
		liteFirstMessage, liteErr := p.NormalizeLite(firstClientMessage)
		if liteErr != nil {
			return p.CloseError(1008, liteErr.Error(), liteErr)
		}
		firstClientMessage = liteFirstMessage
	}
	originalFirstClientMessage := firstClientMessage
	firstRequestModel := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "model").String())
	if firstRequestModel == "" && hooks != nil {
		firstRequestModel = strings.TrimSpace(hooks.InitialRequestModel)
	}
	if next, policyErr := p.Reasoning(firstClientMessage, firstRequestModel); policyErr != nil {
		return p.CloseError(1008, policyErr.Error(), policyErr)
	} else {
		firstClientMessage = next
	}
	requestModel := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "model").String())
	requestPreviousResponseID := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "previous_response_id").String())
	initialRequestModel := ""
	if hooks != nil {
		initialRequestModel = hooks.InitialRequestModel
	}
	// usage 元数据必须在改写为 U 前捕获客户端模型 R。
	usageMeta := NewUsageMeta(initialRequestModel, firstClientMessage, p)
	usageMeta.CaptureRequestedReasoningEffort(originalFirstClientMessage, initialRequestModel)
	p.Log(fmt.Sprintf(
		"relay_start account_id=%d model=%s previous_response_id=%s first_message_type=%s first_message_bytes=%d",
		o.AccountID,
		p.Truncate(requestModel, 160),
		p.Truncate(requestPreviousResponseID, 64),
		"text",
		len(firstClientMessage),
	))

	// 在首个 response.create 帧上应用 OpenAI Fast Policy。后续帧会通过下方的
	// FrameConn 包装器过滤，确保每个 client -> upstream 帧都经过与 HTTP 入口相同的
	// 策略评估、归一化和 scope 处理。
	//
	// 这里从首帧分别捕获渠道模型 C 和最终模型 U，供后续省略 model 的帧回退使用。
	// Realtime 客户端允许发送不重复声明 model 的 response.create，此时上游会使用
	// session.update 协商得到的 model。没有这个 fallback 时，空 model 会绕过管理员
	// 配置的模型白名单并被静默透传，导致首帧之后的每一帧都无法命中该策略。
	firstRoutingModel, firstUpstreamModel, resolveModelErr := p.Models(1, requestModel, firstClientMessage)
	if resolveModelErr != nil {
		return resolveModelErr
	}
	// passthrough 的上下游 relay 分属两个 goroutine，逐轮模型快照必须原子发布。
	var capturedSessionRequestedModel atomic.Pointer[string]
	var capturedSessionRoutingModel atomic.Pointer[string]
	var capturedSessionUpstreamModel atomic.Pointer[string]
	storeCapturedSessionModels := func(requestedModel string, routingModel string, upstreamModel string) {
		requestedCopy := requestedModel
		routingCopy := routingModel
		upstreamCopy := upstreamModel
		capturedSessionRequestedModel.Store(&requestedCopy)
		capturedSessionRoutingModel.Store(&routingCopy)
		capturedSessionUpstreamModel.Store(&upstreamCopy)
	}
	loadCapturedModel := func(model *atomic.Pointer[string]) string {
		if value := model.Load(); value != nil {
			return *value
		}
		return ""
	}
	storeCapturedSessionModels(requestModel, firstRoutingModel, firstUpstreamModel)

	firstClientMessage, resolveModelErr = replaceRequestModel(firstClientMessage, "response.create", firstUpstreamModel, p.CloseError)
	if resolveModelErr != nil {
		return resolveModelErr
	}
	if o.OAuth {
		aliasedBody, aliasErr := p.AliasTools(firstClientMessage)
		if aliasErr != nil {
			return aliasErr
		}
		firstClientMessage = aliasedBody
	}
	firstMessageResponsesLite := p.IsLite(firstClientMessage)
	if normalized, compatibilityChanged, normalizeErr := p.Compatibility(firstClientMessage, firstMessageResponsesLite); normalizeErr != nil {
		return fmt.Errorf("normalize first websocket response.create: %w", normalizeErr)
	} else if compatibilityChanged {
		firstClientMessage = normalized
	}
	// API-Key 兼容清理可能删除无工具请求的 parallel_tool_calls；Lite 契约要求该字段显式为 false。
	if firstMessageResponsesLite {
		liteFirstMessage, liteErr := p.NormalizeLite(firstClientMessage)
		if liteErr != nil {
			return fmt.Errorf("normalize first websocket Lite payload: %w", liteErr)
		}
		firstClientMessage = liteFirstMessage
	}
	accountScopedFirst, accountScoped, scopeErr := p.ScopeIdentity(firstClientMessage)
	if scopeErr != nil {
		return p.CloseError(1008, "invalid websocket identity metadata", scopeErr)
	}
	if accountScoped {
		firstClientMessage = accountScopedFirst
	}
	firstPolicyCtx := ctx
	updatedFirst, blocked, policyErr := p.FastPolicy(firstPolicyCtx, 1, firstUpstreamModel, firstClientMessage, true)
	if policyErr != nil {
		return fmt.Errorf("apply openai fast policy on first ws frame: %w", policyErr)
	}
	if blocked != nil {
		p.PolicyDenied()
		// coder/websocket@v1.8.14 Conn.Write is synchronous: it acquires
		// writeFrameMu, writes the entire frame, and Flushes the underlying
		// bufio writer before returning (write.go:42 → write.go:307-311).
		// The subsequent close handshake re-acquires the same writeFrameMu
		// to send the close frame, so the error event is guaranteed to
		// reach the kernel send buffer before any close frame is queued.
		// No explicit flush hop is required here.
		eventBytes := p.BlockedEvent(blocked)
		if eventBytes != nil {
			writeCtx, cancelWrite := context.WithTimeout(ctx, o.WriteTimeout)
			_ = clientConn.Write(writeCtx, TextFrame, eventBytes)
			cancelWrite()
		}
		return p.CloseError(1008, blocked.Message, blocked)
	}
	firstClientMessage = updatedFirst

	// 在 policy filter 之后再提取 service_tier / reasoning_effort 用于
	// usage 上报：filter 命中时 service_tier 已经从 firstClientMessage 中删除，
	// 最终出站 tier 应为 nil，而不是用户最初请求的 "priority"。观察到的回包
	// tier 单独保存在 UpstreamResponseServiceTier，由 usage 阶段统一决策。
	// HTTP 入口（line ~2728 requeststate.ExtractOpenAIServiceTier(reqBody)）
	// 与 WS ingress（openai_ws_forwarder.go:2991 取自 payload）的语义一致。
	//
	// 多轮 passthrough：OpenAI Realtime / Responses WS 协议允许客户端在
	// 同一连接的不同 response.create 帧上发送不同 service_tier（参考
	// codex-rs/core/src/client.rs build_responses_request 每次重新填值）。
	// filter 会把每轮值固化到 turn 队列；原子值仅保存最新会话状态，供缺失
	// turn 快照的异常和最终汇总路径兜底使用。
	usageMeta.InitFromFirstFrame(firstClientMessage, firstUpstreamModel)
	promptCacheKey := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "prompt_cache_key").String())
	turnPayloads := NewTurnPayloadQueue()
	turnPayloads.Push(TurnPayload{
		StartedAt:                firstTurnStartedAt,
		RequestBody:              firstClientMessage,
		OriginalModel:            requestModel,
		RoutingModel:             firstRoutingModel,
		UpstreamModel:            firstUpstreamModel,
		ServiceTier:              usageMeta.ServiceTier.Load(),
		ReasoningEffort:          usageMeta.ReasoningEffort.Load(),
		RequestedReasoningEffort: usageMeta.RequestedReasoningEffort.Load(),
		PreviousResponseID:       requestPreviousResponseID,
		Source:                   "passthrough",
	})
	p.SetUpstreamModel(firstUpstreamModel)
	if err := p.PrepareDial(ctx, firstClientMessage, promptCacheKey); err != nil {
		return err
	}
	agentTaskRecoveryTried := false
	var dialResult DialResult
	var dialErr error
	for {
		dialResult, dialErr = p.DialOnce(ctx)
		if dialResult.PreparationError {
			return dialErr
		}
		if dialErr == nil {
			break
		}
		if p.CanRecover(ctx, dialResult, dialErr) && !agentTaskRecoveryTried {
			agentTaskRecoveryTried = true
			if err := p.Recover(ctx); err != nil {
				return fmt.Errorf("agent identity task recovery failed: %w", err)
			}
			continue
		}
		return p.DialFailure(ctx, loadCapturedModel(&capturedSessionRoutingModel), dialResult, dialErr)
	}
	upstreamFrameConn := dialResult.Conn
	if upstreamFrameConn == nil {
		return errors.New("openai ws passthrough upstream connection does not support frame relay")
	}
	defer func() { _ = upstreamFrameConn.Close() }()
	handshakeHeaders := dialResult.Headers

	relayUpstreamFrameConn := NewDeadlineConn(
		upstreamFrameConn, o.IdleTimeout, func(payload []byte) Deadline {
			reasoningEffort := ""
			if current := usageMeta.ReasoningEffort.Load(); current != nil {
				reasoningEffort = *current
			}
			timeout := p.FirstOutputTimeout(reasoningEffort)
			if timeout <= 0 {
				timeout = o.IdleTimeout
			}
			model := RequestModelForFrame(payload)
			if model == "" {
				model = usageMeta.RequestModelForFrame(payload)
			}
			if model == "" {
				model = requestModel
			}
			return Deadline{
				Timeout:         timeout,
				StartedAt:       time.Now(),
				RequestModel:    model,
				ReasoningEffort: reasoningEffort,
			}
		}, p.ClosedError())

	completedTurns := atomic.Int32{}
	var acceptedTurnStartedAt atomic.Pointer[time.Time]
	var terminalWritePayload atomic.Pointer[TurnPayload]
	turnLifecycle := NewTurnLifecycle(true)
	clientFrameConn := &clientFrameConn{
		closeError: p.CloseError, closedError: p.ClosedError(), normalizeCompleted: p.NormalizeCompleted,
		conn:                 clientConn,
		controlCtx:           ctx,
		interTurnIdleTimeout: o.InterTurnIdleTimeout,
		interTurnStarted:     make(chan struct{}, 1),
		restoreResponseModel: func(payload []byte) []byte {
			eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
			if !p.MayContainModel(eventType) {
				return payload
			}
			requestModel := usageMeta.RequestModelForFrame(nil)
			upstreamModel := loadCapturedModel(&capturedSessionUpstreamModel)
			if upstreamModel == "" {
				upstreamModel = requestModel
			}
			return p.ReplaceModel(payload, upstreamModel, requestModel)
		},
		restoreToolNames: func(payload []byte) []byte {
			return p.RestoreTools(payload)
		},
	}
	policyClientConn := &policyFrameConn{
		closeError: p.CloseError, closedError: p.ClosedError(),
		inner: clientFrameConn,
		// filter 仅在 runClientToUpstream 这一条 goroutine 中执行；
		// 会话模型还会被上游回调读取，因此通过上方原子快照同步。
		filter: func(msgType int, payload []byte) (out []byte, blocked *PolicyBlocked, filterErr error) {
			if msgType != TextFrame && msgType != 2 {
				return payload, nil, nil
			}
			// 后续 response.create 帧在策略过滤和上游转发前执行同一套用户提示词替换。
			payload = p.PromptReplace(ctx, payload)
			eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
			isResponseCreate := eventType == "response.create"
			responseCreateAt := time.Time{}
			if isResponseCreate {
				responseCreateAt = time.Now()
			}
			acceptedTurn := false
			if isResponseCreate {
				if !turnLifecycle.BeginResponseCreate(clientFrameConn.markTurnStarted) {
					err := errors.New("overlapping response.create is not supported")
					return payload, nil, p.CloseError(1008, err.Error(), err)
				}
				defer func() {
					if !acceptedTurn {
						turnLifecycle.CancelResponseCreate()
					}
				}()
			}
			if isResponseCreate {
				if o.OAuth {
					aliasedBody, aliasErr := p.AliasTools(payload)
					if aliasErr != nil {
						return payload, nil, p.CloseError(1008, aliasErr.Error(), aliasErr)
					}
					payload = aliasedBody
				}
				responsesLite := isResponseCreate && p.IsLite(payload)
				if normalized, compatibilityChanged, normalizeErr := p.Compatibility(payload, responsesLite); normalizeErr != nil {
					return payload, nil, p.CloseError(1008, "invalid websocket request payload", normalizeErr)
				} else if compatibilityChanged {
					payload = normalized
				}
				if responsesLite {
					litePayload, liteErr := p.NormalizeLite(payload)
					if liteErr != nil {
						return payload, nil, p.CloseError(1008, liteErr.Error(), liteErr)
					}
					payload = litePayload
				}
			}
			if isResponseCreate || eventType == "session.update" {
				accountScopedPayload, accountScoped, scopeErr := p.ScopeIdentity(payload)
				if scopeErr != nil {
					return payload, nil, p.CloseError(1008, "invalid websocket identity metadata", scopeErr)
				}
				if accountScoped {
					payload = accountScopedPayload
				}
			}
			originalResponseCreate := payload
			if isResponseCreate {
				requestModelForPolicy := usageMeta.RequestModelForFrame(payload)
				if next, policyErr := p.Reasoning(payload, requestModelForPolicy); policyErr != nil {
					return payload, nil, p.CloseError(1008, policyErr.Error(), policyErr)
				} else {
					payload = next
				}
			}
			if isResponseCreate {
				usageMeta.CaptureRequestedReasoningEffort(originalResponseCreate)
			}
			turnNo := int(completedTurns.Load()) + 1
			if turnNo < 2 {
				turnNo = 2
			}
			if isResponseCreate && hooks != nil && hooks.BeforeRequest != nil {
				requestModel := usageMeta.RequestModelForFrame(payload)
				if requestModel == "" {
					requestModel = usageMeta.LoadSessionRequestModel()
				}
				previousResponseID := strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String())
				updatedPayload, err := hooks.BeforeRequest(turnNo, payload, requestModel, previousResponseID)
				if err != nil {
					return payload, nil, err
				}
				if len(updatedPayload) > 0 {
					payload = updatedPayload
				}
			}

			// 在写入 U 前先保存客户端会话模型 R，避免后续省略 model 时把上游模型当成新请求再次映射。
			usageMeta.UpdateSessionRequestModel(payload)
			requestModelForThisFrame := usageMeta.RequestModelForFrame(payload)
			routingModel := loadCapturedModel(&capturedSessionRoutingModel)
			model := loadCapturedModel(&capturedSessionUpstreamModel)
			switch eventType {
			case "response.create":
				resolvedRoutingModel, upstreamModel, resolveErr := p.Models(turnNo, requestModelForThisFrame, payload)
				if resolveErr != nil {
					return payload, nil, resolveErr
				}
				payload, resolveErr = replaceRequestModel(payload, eventType, upstreamModel, p.CloseError)
				if resolveErr != nil {
					return payload, nil, resolveErr
				}
				routingModel = resolvedRoutingModel
				model = upstreamModel
				storeCapturedSessionModels(requestModelForThisFrame, resolvedRoutingModel, upstreamModel)
			case "session.update":
				sessionRequestedModel := RequestModelFromSessionFrame(payload)
				if sessionRequestedModel != "" {
					resolvedRoutingModel, upstreamModel, resolveErr := p.Models(turnNo, sessionRequestedModel, payload)
					if resolveErr != nil {
						return payload, nil, resolveErr
					}
					payload, resolveErr = replaceRequestModel(payload, eventType, upstreamModel, p.CloseError)
					if resolveErr != nil {
						return payload, nil, resolveErr
					}
					routingModel = resolvedRoutingModel
					model = upstreamModel
					storeCapturedSessionModels(sessionRequestedModel, resolvedRoutingModel, upstreamModel)
				}
			}
			out, blocked, policyErr := p.FastPolicy(ctx, turnNo, model, payload, eventType == "response.create")

			// 多轮 passthrough usage：仅在成功（non-block / non-err）
			// 的 response.create 帧上更新 usageMeta，使用
			// filter 处理后的 payload，与首帧 policy-after-extract 语义
			// 保持一致（参见上方 requeststate.ExtractOpenAIServiceTierFromBody 注释）。
			//   - 非 response.create 帧（response.cancel /
			//     conversation.item.create / session.update 等）不携带
			//     per-response metadata，不应覆盖前一轮值。
			//   - blocked != nil：该帧不会发送上游，usage metadata 应保持
			//     上一轮值。
			//   - policyErr != nil：异常路径，保持上一轮值。
			//   - 不带 service_tier 的 response.create 会让
			//     requeststate.ExtractOpenAIServiceTierFromBody 返回 nil；这里有意
			//     覆盖（Store(nil)），因为 OpenAI 上游对该帧实际不传
			//     service_tier 时按 default 处理，billing 应如实反映。
			if policyErr == nil && blocked == nil && isResponseCreate {
				if hooks != nil && hooks.BeforeTurn != nil {
					if err := hooks.BeforeTurn(turnNo); err != nil {
						return payload, nil, err
					}
				}
				usageMeta.UpdateFromResponseCreate(out, model, requestModelForThisFrame)
				turnPayloads.Push(TurnPayload{
					StartedAt:                responseCreateAt,
					RequestBody:              out,
					OriginalModel:            requestModelForThisFrame,
					RoutingModel:             routingModel,
					UpstreamModel:            model,
					ServiceTier:              usageMeta.ServiceTier.Load(),
					ReasoningEffort:          usageMeta.ReasoningEffort.Load(),
					RequestedReasoningEffort: usageMeta.RequestedReasoningEffort.Load(),
					PreviousResponseID:       strings.TrimSpace(gjson.GetBytes(out, "previous_response_id").String()),
					Source:                   "passthrough",
				})
				responseCreateAtCopy := responseCreateAt
				acceptedTurnStartedAt.Store(&responseCreateAtCopy)
				if hooks != nil && hooks.TurnStarted != nil {
					hooks.TurnStarted(turnNo, responseCreateAt)
				}
				acceptedTurn = true
			}
			return out, blocked, policyErr
		},
		// 客户端事件继续展示该轮请求模型 R，最终上游模型 U 只保留在内部结果和用量字段中。
		writeFilter: func(msgType int, payload []byte) ([]byte, error) {
			if msgType != TextFrame {
				return payload, nil
			}
			eventType := p.EventType(payload)
			turnPayload := turnPayloads.Peek()
			if p.IsTerminal(eventType) {
				if completed := terminalWritePayload.Swap(nil); completed != nil {
					turnPayload = *completed
				}
			}
			if !p.MayContainModel(eventType) {
				return payload, nil
			}
			requestedModel := strings.TrimSpace(turnPayload.OriginalModel)
			upstreamModel := strings.TrimSpace(turnPayload.UpstreamModel)
			if requestedModel == "" {
				requestedModel = loadCapturedModel(&capturedSessionRequestedModel)
			}
			if upstreamModel == "" {
				upstreamModel = loadCapturedModel(&capturedSessionUpstreamModel)
			}
			if requestedModel == "" || upstreamModel == "" || requestedModel == upstreamModel || !bytes.Contains(payload, []byte(upstreamModel)) {
				return payload, nil
			}
			return p.ReplaceModel(payload, upstreamModel, requestedModel), nil
		},
		onBlock: func(blocked *PolicyBlocked) {
			p.PolicyDenied()
			// Conn.Write 会同步刷新帧，因此错误事件会先于关闭帧到达，无需显式刷新。
			eventBytes := p.BlockedEvent(blocked)
			if eventBytes == nil {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, o.WriteTimeout)
			_ = clientConn.Write(writeCtx, TextFrame, eventBytes)
			cancel()
		},
	}
	upstreamFirstMessageSent := false
	firstWriteCtx, cancelFirstWrite := context.WithTimeout(ctx, o.WriteTimeout)
	firstWriteErr := relayUpstreamFrameConn.WriteFrame(firstWriteCtx, TextFrame, firstClientMessage)
	cancelFirstWrite()
	if firstWriteErr != nil {
		return WrapIngressTurnError(
			"write_upstream",
			fmt.Errorf("write first upstream websocket request: %w", firstWriteErr),
			false,
		)
	}
	upstreamFirstMessageSent = true

	readNextClientFrame := func(readCtx context.Context, conn FrameConn) (int, []byte, error) {
		for {
			msgType, payload, readErr := conn.ReadFrame(readCtx)
			if readErr != nil {
				return msgType, payload, readErr
			}
			if (msgType == TextFrame || msgType == 2) && strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.create" {
				return msgType, payload, nil
			}
			if writeErr := upstreamFrameConn.WriteFrame(readCtx, msgType, payload); writeErr != nil {
				return msgType, payload, writeErr
			}
		}
	}

	relayResult, relayExit := p.RunRelay(RelayInput{
		Ctx:                ctx,
		ClientConn:         policyClientConn,
		UpstreamConn:       relayUpstreamFrameConn,
		FirstClientMessage: firstClientMessage,
		Options: RelayOptions{
			WriteTimeout:       o.WriteTimeout,
			FirstTurnStartedAt: firstTurnStartedAt,
			TakeNextTurnStartedAt: func() time.Time {
				startedAt := acceptedTurnStartedAt.Swap(nil)
				if startedAt == nil {
					return time.Time{}
				}
				return *startedAt
			},
			// passthrough 的空闲超时仅由 clientFrameConn 在一轮完成后检测；
			// relay 全局活动看门狗会误终止仍在正常处理的上游轮次。
			IdleTimeout:                     0,
			FirstMessageType:                TextFrame,
			FirstMessageSent:                upstreamFirstMessageSent,
			StartClientAfterFirstDownstream: true,
			ReadClientFrame:                 readNextClientFrame,
			OnUsageParseFailure: func(eventType string, usageRaw string) {
				p.Log(fmt.Sprintf(
					"usage_parse_failed event_type=%s usage_raw=%s",
					p.Truncate(eventType, 160),
					p.Truncate(usageRaw, 160),
				))
			},
			OnUpstreamEvent: func(eventType string, payload []byte) {
				warning := p.Warning(eventType, payload)
				if warning != nil && hooks != nil && hooks.OnUpstreamError != nil {
					turnNo := int(completedTurns.Load()) + 1
					if turnNo < 1 {
						turnNo = 1
					}
					turnPayload := turnPayloads.Peek()
					hooks.OnUpstreamError(turnNo, turnPayload.OriginalModel, warning.StatusCode, warning.ResponseBody, warning.Message)
				}
			},
			OnTurnComplete: func(turn RelayTurnResult) {
				turnNo := int(completedTurns.Add(1))
				turnPayload := turnPayloads.Pop()
				if !turn.StartedAt.IsZero() {
					turnPayload.StartedAt = turn.StartedAt
				}
				turnPayloadForWrite := turnPayload
				terminalWritePayload.Store(&turnPayloadForWrite)
				turnOriginalModel := strings.TrimSpace(turnPayload.OriginalModel)
				if turnOriginalModel == "" {
					turnOriginalModel = turn.RequestModel
				}
				turnResult := &ForwardResult{
					RequestID: turn.RequestID,
					Usage: wire.ForwardUsage{
						InputTokens:              turn.Usage.InputTokens,
						OutputTokens:             turn.Usage.OutputTokens,
						CacheCreationInputTokens: turn.Usage.CacheCreationInputTokens,
						CacheReadInputTokens:     turn.Usage.CacheReadInputTokens,
						ImageOutputTokens:        turn.Usage.ImageOutputTokens,
					},
					Model:                       turnOriginalModel,
					UpstreamModel:               turnPayload.UpstreamModel,
					UpstreamResponseServiceTier: p.NormalizeTier(turn.ResponseServiceTier),
					ServiceTier:                 turnPayload.ServiceTier,
					ReasoningEffort:             turnPayload.ReasoningEffort,
					RequestedReasoningEffort:    turnPayload.RequestedReasoningEffort,
					Stream:                      true,
					OpenAIWSMode:                true,
					UpstreamTerminalEvent:       p.NormalizeTerminal(turn.TerminalEventType),
					ResponseHeaders:             cloneHeaders(handshakeHeaders),
					Duration:                    turn.Duration,
					FirstTokenMs:                turn.FirstTokenMs,
				}
				p.Log(fmt.Sprintf(
					"relay_turn_completed account_id=%d turn=%d request_id=%s terminal_event=%s duration_ms=%d first_token_ms=%d input_tokens=%d output_tokens=%d cache_read_tokens=%d",
					o.AccountID,
					turnNo,
					p.Truncate(turnResult.RequestID, 64),
					p.Truncate(turn.TerminalEventType, 160),
					turnResult.Duration.Milliseconds(),
					firstTokenForLog(turnResult.FirstTokenMs),
					turnResult.Usage.InputTokens,
					turnResult.Usage.OutputTokens,
					turnResult.Usage.CacheReadInputTokens,
				))
				if hooks != nil && hooks.AfterTurn != nil {
					hooks.AfterTurn(TurnCapture{
						Turn:               turnNo,
						StartedAt:          turnPayload.StartedAt,
						RequestBody:        turnPayload.RequestBody,
						OriginalModel:      turnPayload.OriginalModel,
						PreviousResponseID: turnPayload.PreviousResponseID,
						Result:             turnResult,
						PayloadSource:      turnPayload.Source,
					})
				}
			},
			BeforeClientWrite: func(msgType int, payload []byte) {
				if msgType == TextFrame && IsTerminalOutput(payload) {
					turnLifecycle.BeginTerminalWrite()
				}
			},
			AfterClientWrite: func(msgType int, payload []byte, writeErr error) {
				if msgType == TextFrame && IsTerminalOutput(payload) {
					turnLifecycle.FinishTerminalWrite(writeErr == nil, clientFrameConn.markTurnCompleted)
				}
			},
			BeforeRelayCancel: func(exit RelayExit) {
				if context.Cause(ctx) != nil {
					return
				}
				status, reason, ok := p.RelayClose(exit, int(completedTurns.Load()))
				if !ok {
					return
				}
				// 与 handler 的关闭路径保持一致，并限制在 WebSocket 控制帧大小内；
				// 原因过长会导致 coder/websocket 跳过关闭帧，客户端只能收到 EOF 而非状态码。
				reason = p.TruncateReason(reason, 120)
				_ = clientConn.Close(status, reason)
				_ = clientConn.CloseNow()
			},
			BeforeWriteClient: func(msgType int, payload []byte, wroteDownstream bool) error {
				if msgType != TextFrame {
					return nil
				}
				turnPayload := turnPayloads.Peek()
				routingModel := strings.TrimSpace(turnPayload.RoutingModel)
				if routingModel == "" {
					routingModel = loadCapturedModel(&capturedSessionRoutingModel)
				}
				return p.BeforeWrite(ctx, routingModel, payload, wroteDownstream, handshakeHeaders)
			},

			OnTrace: func(event RelayTraceEvent) {
				p.Log(fmt.Sprintf(
					"relay_trace account_id=%d stage=%s direction=%s msg_type=%s bytes=%d graceful=%v wrote_downstream=%v err=%s",
					o.AccountID,
					p.Truncate(event.Stage, 160),
					p.Truncate(event.Direction, 160),
					p.Truncate(event.MessageType, 160),
					event.PayloadBytes,
					event.Graceful,
					event.WroteDownstream,
					p.Truncate(event.Error, 160),
				))
			},
		},
	})
	if cause := context.Cause(ctx); cause != nil {
		status := 1001
		reason := "websocket request canceled"
		if errors.Is(cause, scheduler.ErrOpenAIWSIngressLeaseLost) {
			status = 1013
			reason = "websocket ingress capacity lease lost; please reconnect"
		}
		_ = clientConn.Close(status, reason)
		_ = clientConn.CloseNow()
		return p.CloseError(status, reason, cause)
	}

	result := &ForwardResult{
		RequestID: relayResult.RequestID,
		Usage: wire.ForwardUsage{
			InputTokens:              relayResult.Usage.InputTokens,
			OutputTokens:             relayResult.Usage.OutputTokens,
			CacheCreationInputTokens: relayResult.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     relayResult.Usage.CacheReadInputTokens,
			ImageOutputTokens:        relayResult.Usage.ImageOutputTokens,
		},
		Model:                       relayResult.RequestModel,
		UpstreamResponseServiceTier: p.NormalizeTier(relayResult.ResponseServiceTier),
		ServiceTier:                 usageMeta.ServiceTier.Load(),
		ReasoningEffort:             usageMeta.ReasoningEffort.Load(),
		RequestedReasoningEffort:    usageMeta.RequestedReasoningEffort.Load(),
		Stream:                      true,
		OpenAIWSMode:                true,
		UpstreamTerminalEvent:       p.NormalizeTerminal(relayResult.TerminalEventType),
		ResponseHeaders:             cloneHeaders(handshakeHeaders),
		Duration:                    relayResult.Duration,
		FirstTokenMs:                relayResult.FirstTokenMs,
	}

	turnCount := int(completedTurns.Load())
	if relayExit == nil {
		p.Log(fmt.Sprintf(
			"relay_completed account_id=%d request_id=%s terminal_event=%s duration_ms=%d c2u_frames=%d u2c_frames=%d dropped_frames=%d turns=%d",
			o.AccountID,
			p.Truncate(result.RequestID, 64),
			p.Truncate(relayResult.TerminalEventType, 160),
			result.Duration.Milliseconds(),
			relayResult.ClientToUpstreamFrames,
			relayResult.UpstreamToClientFrames,
			relayResult.DroppedDownstreamFrames,
			turnCount,
		))
		// 正常路径按 terminal 事件逐 turn 已回调；仅在零 turn 场景兜底回调一次。
		if turnCount == 0 && hooks != nil && hooks.AfterTurn != nil {
			turnPayload := turnPayloads.Pop()
			hooks.AfterTurn(TurnCapture{
				Turn:               1,
				StartedAt:          turnPayload.StartedAt,
				RequestBody:        turnPayload.RequestBody,
				OriginalModel:      turnPayload.OriginalModel,
				PreviousResponseID: turnPayload.PreviousResponseID,
				Result:             result,
				PayloadSource:      turnPayload.Source,
			})
		}
		return nil
	}
	p.Log(fmt.Sprintf(
		"relay_failed account_id=%d stage=%s wrote_downstream=%v err=%s duration_ms=%d c2u_frames=%d u2c_frames=%d dropped_frames=%d turns=%d",
		o.AccountID,
		p.Truncate(relayExit.Stage, 160),
		relayExit.WroteDownstream,
		p.Truncate(errorText(relayExit.Err), 160),
		result.Duration.Milliseconds(),
		relayResult.ClientToUpstreamFrames,
		relayResult.UpstreamToClientFrames,
		relayResult.DroppedDownstreamFrames,
		turnCount,
	))

	relayErr := relayExit.Err
	var firstOutputTimeoutErr *FirstOutputTimeoutError
	if errors.As(relayErr, &firstOutputTimeoutErr) {
		deadline := firstOutputTimeoutErr.Deadline
		failoverErr := p.FirstOutputFailure(ctx, deadline, handshakeHeaders)

		if turnCount == 0 && !relayExit.WroteDownstream {
			relayErr = failoverErr
		} else {
			// handler 在账号重试之间只保留首个 response.create；后续轮次超时后重放它
			// 会重复执行首轮，因此后续轮次直接结束客户端会话。
			relayErr = p.CloseError(
				1001,
				"upstream produced no semantic output; please reconnect",
				firstOutputTimeoutErr,
			)
		}
	}
	var activeTurnTimeoutErr *ActiveTurnTimeoutError
	if errors.As(relayErr, &activeTurnTimeoutErr) {
		relayErr = p.CloseError(
			1001,
			"upstream websocket read timeout; please reconnect",
			activeTurnTimeoutErr,
		)
	}
	if relayExit.Stage == "idle_timeout" {
		relayErr = p.CloseError(
			1008,
			"client websocket idle timeout",
			relayErr,
		)
	}
	turnErr := WrapIngressTurnError(
		relayExit.Stage,
		relayErr,
		relayExit.WroteDownstream,
	)
	if hooks != nil && hooks.AfterTurn != nil && turnPayloads.Len() > 0 {
		turnPayload := turnPayloads.Pop()
		hooks.AfterTurn(TurnCapture{
			Turn:               turnCount + 1,
			StartedAt:          turnPayload.StartedAt,
			RequestBody:        turnPayload.RequestBody,
			OriginalModel:      turnPayload.OriginalModel,
			PreviousResponseID: turnPayload.PreviousResponseID,
			Err:                turnErr,
			PayloadSource:      turnPayload.Source,
		})
	}
	return turnErr
}
func replaceRequestModel(payload []byte, eventType, model string, closeError func(int, string, error) error) ([]byte, error) {
	path := "model"
	if eventType == "session.update" {
		path = "session.model"
	}
	out, err := sjson.SetBytes(payload, path, model)
	if err != nil {
		return nil, closeError(1008, "invalid websocket request payload", err)
	}
	return out, nil
}
func cloneHeaders(headers map[string][]string) map[string][]string {
	if headers == nil {
		return nil
	}
	out := maps.Clone(headers)
	for key, values := range out {
		out[key] = slices.Clone(values)
	}
	return out
}
func firstTokenForLog(value *int) int {
	if value == nil {
		return -1
	}
	return *value
}
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
