package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/tidwall/gjson"
)

func payloadString(body []byte, key string) string {
	return strings.TrimSpace(gjson.GetBytes(body, key).String())
}

// Run 拥有整条入站连接的逐轮循环；平台 Adapter 只处理单次解析、租约和转发。
func (s *IngressSession) Run(ctx context.Context, firstMessage []byte) error {
	p, codec, o, state := s.Port, s.Codec, s.Options, s.State
	hooks, stateStore, groupID := s.Hooks, s.Store, s.Options.GroupID
	debugEnabled := o.Debug
	storeDisabledConnMode := o.StoreDisabledMode
	firstPayload, err := p.Parse(firstMessage, false, 1)
	if err != nil {
		return err
	}
	state.SessionHash = p.GenerateHash(firstPayload.RawForHash)
	if state.TurnState == "" && stateStore != nil && state.SessionHash != "" {
		if value, ok := stateStore.GetSessionTurnState(groupID, state.SessionHash); ok {
			state.TurnState = value
		}
	}
	state.PreferredConnID = ""
	if stateStore != nil && firstPayload.PreviousResponseID != "" {
		if value, ok := stateStore.GetResponseConn(firstPayload.PreviousResponseID); ok {
			state.PreferredConnID = value
		}
	}
	state.StoreDisabled = p.StoreDisabled(firstPayload.PayloadRaw)
	if stateStore != nil && state.StoreDisabled && firstPayload.PreviousResponseID == "" && state.SessionHash != "" {
		if value, ok := stateStore.GetSessionConn(groupID, state.SessionHash); ok {
			state.PreferredConnID = value
		}
	}
	if p.ShouldBridge(firstPayload) {
		p.Log(fmt.Sprintf(
			"ingress_ws_http_bridge_start account_id=%d account_type=%s payload_bytes=%d threshold_bytes=%d has_session_hash=%v store_disabled=%v",
			o.AccountID,
			o.AccountType,
			firstPayload.PayloadBytes,
			o.BridgeThreshold,
			state.SessionHash != "",
			state.StoreDisabled,
		))
		currentBridgePayload := firstPayload
		// 首轮请求固定作为稳定会话种子；后续每轮会重新解析映射模型，避免连接内切换模型时
		// 复用其它模型的上游缓存身份。
		grokCacheSeedPayload := firstPayload.PayloadRaw
		var bridgeReplayInput []json.RawMessage
		bridgeReplayInputExists := false
		var bridgeAccountFailoverInput []json.RawMessage
		bridgeAccountFailoverInputExists := false
		for turn := 1; ; turn++ {
			turnStartedAt := time.Now()
			if hooks != nil && hooks.TurnStarted != nil {
				hooks.TurnStarted(turn, turnStartedAt)
			}
			if turn > 1 && hooks != nil && hooks.BeforeRequest != nil {
				updatedPayload, err := hooks.BeforeRequest(turn, currentBridgePayload.PayloadRaw, currentBridgePayload.OriginalModel, currentBridgePayload.PreviousResponseID)
				if err != nil {
					return err
				}
				if len(updatedPayload) > 0 {
					currentBridgePayload.PayloadRaw = updatedPayload
					currentBridgePayload.PayloadBytes = len(updatedPayload)
					currentBridgePayload.PreviousResponseID = strings.TrimSpace(gjson.GetBytes(updatedPayload, "previous_response_id").String())
				}
			}
			if hooks != nil && hooks.BeforeTurn != nil {
				if err := hooks.BeforeTurn(turn); err != nil {
					return err
				}
			}
			p.SetRequestState(state.TurnState, state.SessionHash)
			// 剥离本会话已知失效的加密项，阻断同一失效密文随历史反复触发上游拒绝。
			// 历史序列须同步剥离，否则与已剥离的当前 input 项错位，prefix 复用失配。
			if invalidDigests := p.InvalidDigests(groupID, state.SessionHash); len(invalidDigests) > 0 {
				strippedPayload, strippedCount := p.StripInvalid(
					currentBridgePayload.PayloadRaw, invalidDigests, "ingress_ws_http_bridge_invalid_encrypted_lineage_strip", o.AccountID, turn,
				)
				if strippedCount > 0 {
					currentBridgePayload.PayloadRaw = strippedPayload
					currentBridgePayload.PayloadBytes = len(strippedPayload)
				}
				if bridgeReplayInputExists {
					bridgeReplayInput, _ = codec.StripItems(bridgeReplayInput, invalidDigests)
				}
				if bridgeAccountFailoverInputExists {
					bridgeAccountFailoverInput, _ = codec.StripItems(bridgeAccountFailoverInput, invalidDigests)
				}
			}
			bridgePayloadRaw := currentBridgePayload.PayloadRaw

			toolOutputCoverage := wire.AnalyzeToolCallOutputContextCoverageBytes(currentBridgePayload.PayloadRaw)
			needsBridgeReplay := currentBridgePayload.PreviousResponseID != "" ||
				(toolOutputCoverage.HasFunctionCallOutput && !toolOutputCoverage.ContextCoversAllCallIDs)
			// 一次解析当前 input，正常 replay 与 account-failover 两份序列共享同一批正文。
			bridgeCurrentItems, bridgeCurrentItemsExist, extractErr := codec.Extract(
				currentBridgePayload.PayloadRaw,
			)
			if extractErr != nil {
				return fmt.Errorf("build websocket http bridge replay input: %w", extractErr)
			}
			turnReplayInput, turnReplayInputExists := codec.BuildFromItems(
				bridgeReplayInput,
				bridgeReplayInputExists,
				bridgeCurrentItems,
				bridgeCurrentItemsExist,
				needsBridgeReplay,
			)
			turnAccountFailoverInput, turnAccountFailoverInputExists := codec.BuildFromItems(
				bridgeAccountFailoverInput,
				bridgeAccountFailoverInputExists,
				bridgeCurrentItems,
				bridgeCurrentItemsExist,
				needsBridgeReplay,
			)
			if needsBridgeReplay && turnReplayInputExists {
				updatedPayload, setInputErr := codec.SetInput(
					currentBridgePayload.PayloadRaw,
					turnReplayInput,
					true,
				)
				if setInputErr != nil {
					return fmt.Errorf("set websocket http bridge replay input: %w", setInputErr)
				}
				bridgePayloadRaw = updatedPayload

				p.Log(fmt.Sprintf(
					"ingress_ws_http_bridge_replay_input account_id=%d turn=%d input_items=%d previous_response_id_present=%v has_tool_output=%v",
					o.AccountID,
					turn,
					len(turnReplayInput),
					currentBridgePayload.PreviousResponseID != "",
					codec.HasOutput(currentBridgePayload.PayloadRaw),
				))
			}
			grokCacheIdentity := ""
			if o.Platform == "grok" {
				grokCacheIdentity, err = p.BridgeIdentity(grokCacheSeedPayload, currentBridgePayload.RoutingModel)
				if err != nil {
					return fmt.Errorf("resolve Grok websocket cache identity: %w", err)
				}
			}
			result, bridgeErr := p.Bridge(ctx, currentBridgePayload, bridgePayloadRaw, grokCacheIdentity, turn)

			if result != nil {
				result.RequestedReasoningEffort = currentBridgePayload.RequestedReasoningEffort
			}
			if hooks != nil && hooks.AfterTurn != nil {
				hooks.AfterTurn(TurnCapture{
					Turn:               turn,
					StartedAt:          turnStartedAt,
					RequestBody:        append([]byte(nil), bridgePayloadRaw...),
					OriginalModel:      currentBridgePayload.OriginalModel,
					PreviousResponseID: currentBridgePayload.PreviousResponseID,
					Result:             result,
					Err:                bridgeErr,
					PayloadSource:      "http_bridge",
				})
			}
			if bridgeErr != nil {
				if turn > 1 && p.IsFailover(bridgeErr) {
					retryPayload, retrySafe, retryPayloadErr := codec.RetryPayload(
						currentBridgePayload.AccountIdentitySourceRaw,
						turnAccountFailoverInput,
						turnAccountFailoverInputExists,
						currentBridgePayload.OriginalModel,
					)
					if retryPayloadErr != nil {
						return fmt.Errorf("build websocket current-turn failover payload: %w", retryPayloadErr)
					}
					if !retrySafe {
						retryPayload = nil
					}
					return NewCurrentTurnFailoverError(bridgeErr, retryPayload)
				}
				return bridgeErr
			}
			if result == nil {
				return errors.New("websocket http bridge turn result is nil")
			}
			// turnReplayInput/turnAccountFailoverInput 可能共享同一头数组（转移自
			// bridgeCurrentItems），保存历史必须经 combine 新建头，禁止就地 append。
			bridgeReplayInput = turnReplayInput
			bridgeReplayInputExists = turnReplayInputExists
			if result.WSReplayInputExists {
				bridgeReplayInput = codec.Combine(bridgeReplayInput, result.WSReplayInput)
				bridgeReplayInputExists = true
			}
			bridgeAccountFailoverInput = turnAccountFailoverInput
			bridgeAccountFailoverInputExists = turnAccountFailoverInputExists
			if len(result.WSAccountFailoverReplayInput) > 0 {
				bridgeAccountFailoverInput = codec.Combine(
					bridgeAccountFailoverInput,
					result.WSAccountFailoverReplayInput,
				)
				bridgeAccountFailoverInputExists = true
			}
			if bridgeTurnState := strings.TrimSpace(result.ResponseTurnState); bridgeTurnState != "" {
				state.TurnState = bridgeTurnState
				if stateStore != nil && state.SessionHash != "" {
					stateStore.BindSessionTurnState(groupID, state.SessionHash, bridgeTurnState, o.SessionStickyTTL)
				}
			}
			responseID := strings.TrimSpace(result.RequestID)
			if responseID != "" && stateStore != nil {
				ttl := o.ResponseStickyTTL
				p.BindWarning(groupID, o.AccountID, responseID, stateStore.BindResponseAccount(ctx, groupID, responseID, o.AccountID, ttl))
			}
			nextClientMessage, readErr := p.ReadClient()
			if readErr != nil {
				if p.IsDisconnect(readErr) {
					closeStatus, closeReason := p.SummarizeClose(readErr)
					p.Log(fmt.Sprintf(
						"ingress_ws_http_bridge_client_closed account_id=%d close_status=%s close_reason=%s",
						o.AccountID,
						closeStatus,
						p.TruncateLog(closeReason, 120),
					))
					return nil
				}
				return fmt.Errorf("read client websocket request: %w", readErr)
			}
			nextPayload, parseErr := p.Parse(nextClientMessage, true, turn+1)
			if parseErr != nil {
				return parseErr
			}
			currentBridgePayload = nextPayload
		}
	}
	if err := p.OpenPool(firstPayload); err != nil {
		return err
	}
	credentialRecovered := false
	acquireTurnLease := func(turn int, preferred string, force bool) (ConnLease, error) {
		for {
			lease, err := p.Acquire(turn, preferred, force, !credentialRecovered)
			var recovery *AcquireRecoveryError
			if errors.As(err, &recovery) && !credentialRecovered {
				credentialRecovered = true
				if recoveryErr := p.RecoverAcquire(ctx); recoveryErr != nil {
					return nil, fmt.Errorf("agent identity task recovery failed: %w", recoveryErr)
				}
				continue
			}
			return lease, err
		}
	}
	currentPayload := firstPayload.PayloadRaw
	currentOriginalModel := firstPayload.OriginalModel
	currentRoutingModel := firstPayload.RoutingModel
	currentImageBillingModel := firstPayload.ImageBillingModel
	currentImageSizeTier := firstPayload.ImageSizeTier
	currentImageInputSize := firstPayload.ImageInputSize
	currentPayloadBytes := firstPayload.PayloadBytes
	currentRequestedReasoningEffort := firstPayload.RequestedReasoningEffort
	isStrictAffinityTurn := func(payload []byte) bool {
		if !state.StoreDisabled {
			return false
		}
		return strings.TrimSpace(payloadString(payload, "previous_response_id")) != ""
	}
	var sessionLease ConnLease
	sessionConnID := ""
	pinnedSessionConnID := ""
	unpinSessionConn := func(connID string) {
		connID = strings.TrimSpace(connID)
		if connID == "" || pinnedSessionConnID != connID {
			return
		}
		p.UnpinConn(o.AccountID, connID)
		pinnedSessionConnID = ""
	}
	pinSessionConn := func(connID string) {
		if !state.StoreDisabled {
			return
		}
		connID = strings.TrimSpace(connID)
		if connID == "" || pinnedSessionConnID == connID {
			return
		}
		if pinnedSessionConnID != "" {
			p.UnpinConn(o.AccountID, pinnedSessionConnID)
			pinnedSessionConnID = ""
		}
		if p.PinConn(o.AccountID, connID) {
			pinnedSessionConnID = connID
		}
	}
	// lastTurnClean 标记最后一轮 sendAndRelay 是否正常完成（收到终端事件且客户端未断连）。
	// 所有异常路径（读写错误、error 事件、客户端断连）已在各自分支或上层（L3403）中 MarkBroken，
	// 因此 releaseSessionLease 中只需在非正常结束时 MarkBroken。
	lastTurnClean := false
	releaseSessionLease := func() {
		if sessionLease == nil {
			return
		}
		if !lastTurnClean {
			sessionLease.MarkBroken()
		}
		unpinSessionConn(sessionConnID)
		sessionLease.Release()
		if debugEnabled {
			p.Debug(fmt.Sprintf(
				"ingress_ws_upstream_released account_id=%d conn_id=%s",
				o.AccountID,
				p.TruncateLog(sessionConnID, 64),
			))
		}
	}
	defer releaseSessionLease()

	turn := 1
	turnRetry := 0
	turnPrevRecoveryTried := false
	lastTurnFinishedAt := time.Time{}
	lastTurnResponseID := ""
	lastTurnPayload := []byte(nil)
	var lastTurnStrictState PreviousTurn
	lastTurnReplayInput := []json.RawMessage(nil)
	lastTurnReplayInputExists := false
	currentTurnReplayInput := []json.RawMessage(nil)
	currentTurnReplayInputExists := false
	skipBeforeTurn := false
	hasCurrentOrReplayFunctionCallOutput := func(payload []byte) bool {
		if codec.HasOutput(payload) {
			return true
		}
		return currentTurnReplayInputExists && codec.ItemsHaveOutput(currentTurnReplayInput)
	}
	resetSessionLease := func(markBroken bool) {
		if sessionLease == nil {
			return
		}
		if markBroken {
			sessionLease.MarkBroken()
		}
		releaseSessionLease()
		sessionLease = nil
		sessionConnID = ""
		state.PreferredConnID = ""
	}
	recoverIngressPrevResponseNotFound := func(relayErr error, turn int, connID string) bool {
		if !IsPreviousResponseNotFound(relayErr) {
			return false
		}
		if turnPrevRecoveryTried || !o.PreviousRecovery {
			return false
		}
		// 携带 function_call_output 的请求不能丢弃 previous_response_id：
		// 上游 API 需要 response chain 来匹配 tool_result 与之前的 tool_use，
		// 丢弃后会导致 "No tool call found for function call output" 400 错误。
		if hasCurrentOrReplayFunctionCallOutput(currentPayload) {
			return false
		}
		if isStrictAffinityTurn(currentPayload) {
			// Layer 2：严格亲和链路命中 previous_response_not_found 时，降级为“去掉 previous_response_id 后重放一次”。
			// 该错误说明续链锚点已失效，继续 strict fail-close 只会直接中断本轮请求。
			p.Log(fmt.Sprintf(
				"ingress_ws_prev_response_recovery_layer2 account_id=%d turn=%d conn_id=%s store_disabled_conn_mode=%s action=drop_previous_response_id_retry",
				o.AccountID,
				turn,
				p.TruncateLog(connID, 64),
				p.NormalizeLog(storeDisabledConnMode),
			))
		}
		turnPrevRecoveryTried = true
		updatedPayload, removed, dropErr := codec.DropPrevious(currentPayload)
		if dropErr != nil || !removed {
			reason := "not_removed"
			if dropErr != nil {
				reason = "drop_error"
			}
			p.Log(fmt.Sprintf(
				"ingress_ws_prev_response_recovery_skip account_id=%d turn=%d conn_id=%s reason=%s",
				o.AccountID,
				turn,
				p.TruncateLog(connID, 64),
				p.NormalizeLog(reason),
			))
			return false
		}
		updatedWithInput, setInputErr := codec.SetInput(
			updatedPayload,
			currentTurnReplayInput,
			currentTurnReplayInputExists,
		)
		if setInputErr != nil {
			p.Log(fmt.Sprintf(
				"ingress_ws_prev_response_recovery_skip account_id=%d turn=%d conn_id=%s reason=set_full_input_error cause=%s",
				o.AccountID,
				turn,
				p.TruncateLog(connID, 64),
				p.TruncateLog(setInputErr.Error(), 160),
			))
			return false
		}
		p.Log(fmt.Sprintf(
			"ingress_ws_prev_response_recovery account_id=%d turn=%d conn_id=%s action=drop_previous_response_id retry=1",
			o.AccountID,
			turn,
			p.TruncateLog(connID, 64),
		))
		currentPayload = updatedWithInput
		currentPayloadBytes = len(updatedWithInput)
		resetSessionLease(true)
		skipBeforeTurn = true
		return true
	}
	retryIngressTurn := func(relayErr error, turn int, connID string) bool {
		if !IsIngressTurnRetryable(relayErr) || turnRetry >= 1 {
			return false
		}
		if isStrictAffinityTurn(currentPayload) {
			p.Log(fmt.Sprintf(
				"ingress_ws_turn_retry_skip account_id=%d turn=%d conn_id=%s reason=strict_affinity",
				o.AccountID,
				turn,
				p.TruncateLog(connID, 64),
			))
			return false
		}
		turnRetry++
		p.Log(fmt.Sprintf(
			"ingress_ws_turn_retry account_id=%d turn=%d retry=%d reason=%s conn_id=%s",
			o.AccountID,
			turn,
			turnRetry,
			p.TruncateLog(IngressTurnRetryReason(relayErr), 160),
			p.TruncateLog(connID, 64),
		))
		resetSessionLease(true)
		skipBeforeTurn = true
		return true
	}
	for {
		turnStartedAt := time.Now()
		if hooks != nil && hooks.TurnStarted != nil {
			hooks.TurnStarted(turn, turnStartedAt)
		}
		if turn > 1 && !skipBeforeTurn && hooks != nil && hooks.BeforeRequest != nil {
			currentPreviousResponseID := payloadString(currentPayload, "previous_response_id")
			updatedPayload, err := hooks.BeforeRequest(turn, currentPayload, currentOriginalModel, currentPreviousResponseID)
			if err != nil {
				return err
			}
			if len(updatedPayload) > 0 {
				currentPayload = updatedPayload
				currentPayloadBytes = len(updatedPayload)
			}
		}
		if !skipBeforeTurn && hooks != nil && hooks.BeforeTurn != nil {
			if err := hooks.BeforeTurn(turn); err != nil {
				return err
			}
		}
		skipBeforeTurn = false
		// 剥离本会话已知失效的加密项，阻断同一失效密文随历史反复触发上游拒绝。
		// 历史序列须同步剥离，否则与已剥离的当前 input 项错位，prefix 复用失配。
		if invalidDigests := p.InvalidDigests(groupID, state.SessionHash); len(invalidDigests) > 0 {
			strippedPayload, strippedCount := p.StripInvalid(
				currentPayload, invalidDigests, "ingress_ws_invalid_encrypted_lineage_strip", o.AccountID, turn,
			)
			if strippedCount > 0 {
				currentPayload = strippedPayload
				currentPayloadBytes = len(strippedPayload)
			}
			if lastTurnReplayInputExists {
				lastTurnReplayInput, _ = codec.StripItems(lastTurnReplayInput, invalidDigests)
			}
		}
		currentPreviousResponseID := payloadString(currentPayload, "previous_response_id")
		expectedPrev := strings.TrimSpace(lastTurnResponseID)
		toolSignals := wire.ToolContinuationSignals{
			HasFunctionCallOutput: codec.HasOutput(currentPayload),
		}
		if toolSignals.HasFunctionCallOutput {
			var currentReqBody map[string]any
			if err := json.Unmarshal(currentPayload, &currentReqBody); err == nil {
				toolSignals = wire.AnalyzeToolContinuationSignals(currentReqBody)
			}
		}
		hasFunctionCallOutput := toolSignals.HasFunctionCallOutput
		// store=false + function_call_output 场景必须有续链锚点。
		// 若客户端未传 previous_response_id，优先回填上一轮响应 ID，避免上游报 call_id 无法关联。
		if codec.ShouldInfer(
			state.StoreDisabled,
			turn,
			toolSignals,
			currentPreviousResponseID,
			expectedPrev,
		) {
			updatedPayload, setPrevErr := codec.SetPrevious(currentPayload, expectedPrev)
			if setPrevErr != nil {
				p.Log(fmt.Sprintf(
					"ingress_ws_function_call_output_prev_infer_skip account_id=%d turn=%d conn_id=%s reason=set_previous_response_id_error cause=%s expected_previous_response_id=%s",
					o.AccountID,
					turn,
					p.TruncateLog(sessionConnID, 64),
					p.TruncateLog(setPrevErr.Error(), 160),
					p.TruncateLog(expectedPrev, 64),
				))
			} else {
				currentPayload = updatedPayload
				currentPayloadBytes = len(updatedPayload)
				currentPreviousResponseID = expectedPrev
				p.Log(fmt.Sprintf(
					"ingress_ws_function_call_output_prev_infer account_id=%d turn=%d conn_id=%s action=set_previous_response_id previous_response_id=%s",
					o.AccountID,
					turn,
					p.TruncateLog(sessionConnID, 64),
					p.TruncateLog(expectedPrev, 64),
				))
			}
		}
		nextReplayInput, nextReplayInputExists, replayInputErr := codec.Build(
			lastTurnReplayInput,
			lastTurnReplayInputExists,
			currentPayload,
			currentPreviousResponseID != "",
		)
		if replayInputErr != nil {
			p.Log(fmt.Sprintf(
				"ingress_ws_replay_input_skip account_id=%d turn=%d conn_id=%s reason=build_error cause=%s",
				o.AccountID,
				turn,
				p.TruncateLog(sessionConnID, 64),
				p.TruncateLog(replayInputErr.Error(), 160),
			))
			currentTurnReplayInput = nil
			currentTurnReplayInputExists = false
		} else {
			currentTurnReplayInput = nextReplayInput
			currentTurnReplayInputExists = nextReplayInputExists
		}
		replayHasFunctionCallOutput := currentTurnReplayInputExists &&
			codec.ItemsHaveOutput(currentTurnReplayInput)
		hasFunctionCallOutput = hasFunctionCallOutput || replayHasFunctionCallOutput
		if state.StoreDisabled && turn > 1 && currentPreviousResponseID != "" {
			shouldKeepPreviousResponseID := false
			strictReason := ""
			var strictErr error
			if lastTurnStrictState != nil {
				shouldKeepPreviousResponseID, strictReason, strictErr = lastTurnStrictState.Keep(
					currentPayload,
					lastTurnResponseID,
					hasFunctionCallOutput,
				)
			} else {
				shouldKeepPreviousResponseID, strictReason, strictErr = codec.KeepPrevious(
					lastTurnPayload,
					currentPayload,
					lastTurnResponseID,
					hasFunctionCallOutput,
				)
			}
			if strictErr != nil {
				p.Log(fmt.Sprintf(
					"ingress_ws_prev_response_strict_eval account_id=%d turn=%d conn_id=%s action=keep_previous_response_id reason=%s cause=%s previous_response_id=%s expected_previous_response_id=%s has_function_call_output=%v",
					o.AccountID,
					turn,
					p.TruncateLog(sessionConnID, 64),
					p.NormalizeLog(strictReason),
					p.TruncateLog(strictErr.Error(), 160),
					p.TruncateLog(currentPreviousResponseID, 64),
					p.TruncateLog(expectedPrev, 64),
					hasFunctionCallOutput,
				))
			} else if !shouldKeepPreviousResponseID {
				updatedPayload, removed, dropErr := codec.DropPrevious(currentPayload)
				if dropErr != nil || !removed {
					dropReason := "not_removed"
					if dropErr != nil {
						dropReason = "drop_error"
					}
					p.Log(fmt.Sprintf(
						"ingress_ws_prev_response_strict_eval account_id=%d turn=%d conn_id=%s action=keep_previous_response_id reason=%s drop_reason=%s previous_response_id=%s expected_previous_response_id=%s has_function_call_output=%v",
						o.AccountID,
						turn,
						p.TruncateLog(sessionConnID, 64),
						p.NormalizeLog(strictReason),
						p.NormalizeLog(dropReason),
						p.TruncateLog(currentPreviousResponseID, 64),
						p.TruncateLog(expectedPrev, 64),
						hasFunctionCallOutput,
					))
				} else {
					updatedWithInput, setInputErr := codec.SetInput(
						updatedPayload,
						currentTurnReplayInput,
						currentTurnReplayInputExists,
					)
					if setInputErr != nil {
						p.Log(fmt.Sprintf(
							"ingress_ws_prev_response_strict_eval account_id=%d turn=%d conn_id=%s action=keep_previous_response_id reason=%s drop_reason=set_full_input_error previous_response_id=%s expected_previous_response_id=%s cause=%s has_function_call_output=%v",
							o.AccountID,
							turn,
							p.TruncateLog(sessionConnID, 64),
							p.NormalizeLog(strictReason),
							p.TruncateLog(currentPreviousResponseID, 64),
							p.TruncateLog(expectedPrev, 64),
							p.TruncateLog(setInputErr.Error(), 160),
							hasFunctionCallOutput,
						))
					} else {
						currentPayload = updatedWithInput
						currentPayloadBytes = len(updatedWithInput)
						p.Log(fmt.Sprintf(
							"ingress_ws_prev_response_strict_eval account_id=%d turn=%d conn_id=%s action=drop_previous_response_id_full_create reason=%s previous_response_id=%s expected_previous_response_id=%s has_function_call_output=%v",
							o.AccountID,
							turn,
							p.TruncateLog(sessionConnID, 64),
							p.NormalizeLog(strictReason),
							p.TruncateLog(currentPreviousResponseID, 64),
							p.TruncateLog(expectedPrev, 64),
							hasFunctionCallOutput,
						))
						currentPreviousResponseID = ""
					}
				}
			}
		}
		forcePreferredConn := isStrictAffinityTurn(currentPayload)
		if sessionLease == nil {
			acquiredLease, acquireErr := acquireTurnLease(turn, state.PreferredConnID, forcePreferredConn)
			if acquireErr != nil {
				return fmt.Errorf("acquire upstream websocket: %w", acquireErr)
			}
			sessionLease = acquiredLease
			sessionConnID = strings.TrimSpace(sessionLease.ConnID())
			if state.StoreDisabled {
				pinSessionConn(sessionConnID)
			} else {
				unpinSessionConn(sessionConnID)
			}
		}
		shouldPreflightPing := turn > 1 && sessionLease != nil && sessionLease.SupportsIdlePingWithoutReader() && turnRetry == 0
		if shouldPreflightPing && o.PreflightPingIdle > 0 && !lastTurnFinishedAt.IsZero() {
			if time.Since(lastTurnFinishedAt) < o.PreflightPingIdle {
				shouldPreflightPing = false
			}
		}
		if shouldPreflightPing {
			if pingErr := sessionLease.PingWithTimeout(o.HealthCheckTimeout); pingErr != nil {
				p.Log(fmt.Sprintf(
					"ingress_ws_upstream_preflight_ping_fail account_id=%d turn=%d conn_id=%s cause=%s",
					o.AccountID,
					turn,
					p.TruncateLog(sessionConnID, 64),
					p.TruncateLog(pingErr.Error(), 160),
				))
				if forcePreferredConn {
					// 携带 function_call_output 的请求不能丢弃 previous_response_id：
					// 上游 API 需要 response chain 来匹配 tool_result 与之前的 tool_use，
					// 除非 replay input 已经包含与每个 tool_result 匹配的 tool_use 上下文。
					hasFCOutput := hasFunctionCallOutput
					hasReplayToolContext := hasFCOutput &&
						currentTurnReplayInputExists &&
						codec.ItemsCoverOutput(currentTurnReplayInput)
					if !turnPrevRecoveryTried && currentPreviousResponseID != "" && (!hasFCOutput || hasReplayToolContext) {
						updatedPayload, removed, dropErr := codec.DropPrevious(currentPayload)
						if dropErr != nil || !removed {
							reason := "not_removed"
							if dropErr != nil {
								reason = "drop_error"
							}
							p.Log(fmt.Sprintf(
								"ingress_ws_preflight_ping_recovery_skip account_id=%d turn=%d conn_id=%s reason=%s previous_response_id=%s",
								o.AccountID,
								turn,
								p.TruncateLog(sessionConnID, 64),
								p.NormalizeLog(reason),
								p.TruncateLog(currentPreviousResponseID, 64),
							))
						} else {
							updatedWithInput, setInputErr := codec.SetInput(
								updatedPayload,
								currentTurnReplayInput,
								currentTurnReplayInputExists,
							)
							if setInputErr != nil {
								p.Log(fmt.Sprintf(
									"ingress_ws_preflight_ping_recovery_skip account_id=%d turn=%d conn_id=%s reason=set_full_input_error previous_response_id=%s cause=%s",
									o.AccountID,
									turn,
									p.TruncateLog(sessionConnID, 64),
									p.TruncateLog(currentPreviousResponseID, 64),
									p.TruncateLog(setInputErr.Error(), 160),
								))
							} else {
								p.Log(fmt.Sprintf(
									"ingress_ws_preflight_ping_recovery account_id=%d turn=%d conn_id=%s action=drop_previous_response_id_retry previous_response_id=%s has_function_call_output=%v has_replay_tool_context=%v",
									o.AccountID,
									turn,
									p.TruncateLog(sessionConnID, 64),
									p.TruncateLog(currentPreviousResponseID, 64),
									hasFCOutput,
									hasReplayToolContext,
								))
								turnPrevRecoveryTried = true
								currentPayload = updatedWithInput
								currentPayloadBytes = len(updatedWithInput)
								resetSessionLease(true)
								skipBeforeTurn = true
								continue
							}
						}
					}
					if hasFCOutput && currentPreviousResponseID != "" {
						reason := "function_call_output_missing_replay_context"
						if hasReplayToolContext {
							reason = "function_call_output_replay_not_applied"
						}
						p.Log(fmt.Sprintf(
							"ingress_ws_preflight_ping_recovery_skip account_id=%d turn=%d conn_id=%s reason=%s action=fail_close previous_response_id=%s has_replay_tool_context=%v",
							o.AccountID,
							turn,
							p.TruncateLog(sessionConnID, 64),
							reason,
							p.TruncateLog(currentPreviousResponseID, 64),
							hasReplayToolContext,
						))
					}
					resetSessionLease(true)
					return p.CloseError(
						1008,
						"upstream continuation connection is unavailable; please restart the conversation",
						pingErr,
					)
				}
				resetSessionLease(true)

				acquiredLease, acquireErr := acquireTurnLease(turn, state.PreferredConnID, forcePreferredConn)
				if acquireErr != nil {
					return fmt.Errorf("acquire upstream websocket after preflight ping fail: %w", acquireErr)
				}
				sessionLease = acquiredLease
				sessionConnID = strings.TrimSpace(sessionLease.ConnID())
				if state.StoreDisabled {
					pinSessionConn(sessionConnID)
				}
			}
		}
		connID := sessionConnID
		if currentPreviousResponseID != "" {
			chainedFromLast := expectedPrev != "" && currentPreviousResponseID == expectedPrev
			currentPreviousResponseIDKind := codec.ClassifyPrevious(currentPreviousResponseID)
			p.Log(fmt.Sprintf(
				"ingress_ws_turn_chain account_id=%d turn=%d conn_id=%s previous_response_id=%s previous_response_id_kind=%s last_turn_response_id=%s chained_from_last=%v preferred_conn_id=%s header_session_id=%s header_conversation_id=%s has_turn_state=%v turn_state_len=%d has_prompt_cache_key=%v store_disabled=%v",
				o.AccountID,
				turn,
				p.TruncateLog(connID, 64),
				p.TruncateLog(currentPreviousResponseID, 64),
				p.NormalizeLog(currentPreviousResponseIDKind),
				p.TruncateLog(expectedPrev, 64),
				chainedFromLast,
				p.TruncateLog(state.PreferredConnID, 64),
				p.Header("session_id"),
				p.Header("conversation_id"),
				state.TurnState != "",
				len(state.TurnState),
				payloadString(currentPayload, "prompt_cache_key") != "",
				state.StoreDisabled,
			))
		}

		result, relayErr := p.Relay(turn, sessionLease, ClientPayload{PayloadRaw: currentPayload, PayloadBytes: currentPayloadBytes, OriginalModel: currentOriginalModel, RoutingModel: currentRoutingModel, ImageBillingModel: currentImageBillingModel, ImageSizeTier: currentImageSizeTier, ImageInputSize: currentImageInputSize, RequestedReasoningEffort: currentRequestedReasoningEffort})
		if relayErr != nil {
			lastTurnClean = false
			if recoverIngressPrevResponseNotFound(relayErr, turn, connID) {
				continue
			}
			if retryIngressTurn(relayErr, turn, connID) {
				continue
			}
			finalErr := relayErr
			if unwrapped := errors.Unwrap(relayErr); unwrapped != nil {
				finalErr = unwrapped
			}
			if hooks != nil && hooks.AfterTurn != nil {
				hooks.AfterTurn(TurnCapture{
					Turn:               turn,
					StartedAt:          turnStartedAt,
					RequestBody:        append([]byte(nil), currentPayload...),
					OriginalModel:      currentOriginalModel,
					PreviousResponseID: currentPreviousResponseID,
					Err:                finalErr,
					PayloadSource:      "ctx_pool",
				})
			}
			sessionLease.MarkBroken()
			return finalErr
		}
		turnRetry = 0
		turnPrevRecoveryTried = false
		lastTurnFinishedAt = time.Now()
		lastTurnClean = true
		if hooks != nil && hooks.AfterTurn != nil {
			hooks.AfterTurn(TurnCapture{
				Turn:               turn,
				StartedAt:          turnStartedAt,
				RequestBody:        append([]byte(nil), currentPayload...),
				OriginalModel:      currentOriginalModel,
				PreviousResponseID: currentPreviousResponseID,
				Result:             result,
				PayloadSource:      "ctx_pool",
			})
		}
		if result == nil {
			return errors.New("websocket turn result is nil")
		}
		responseID := strings.TrimSpace(result.RequestID)
		lastTurnResponseID = responseID
		// 正文共享：currentPayload/currentTurnReplayInput 均不可变，历史直接引用；
		// collector 增量经 combine 合并（新头数组）。
		lastTurnReplayInput = currentTurnReplayInput
		lastTurnReplayInputExists = currentTurnReplayInputExists
		if result.WSReplayInputExists {
			lastTurnReplayInput = codec.Combine(lastTurnReplayInput, result.WSReplayInput)
			lastTurnReplayInputExists = true
		}
		nextStrictState, strictStateErr := codec.BuildStrict(currentPayload)
		if strictStateErr != nil {
			lastTurnStrictState = nil
			// strict 状态不可用时保留整份上一轮 payload 供慢路径比较。
			lastTurnPayload = currentPayload
			p.Log(fmt.Sprintf(
				"ingress_ws_prev_response_strict_state_skip account_id=%d turn=%d conn_id=%s reason=build_error cause=%s",
				o.AccountID,
				turn,
				p.TruncateLog(connID, 64),
				p.TruncateLog(strictStateErr.Error(), 160),
			))
		} else {
			lastTurnStrictState = nextStrictState
			lastTurnPayload = nil
		}

		if responseID != "" && stateStore != nil {
			ttl := o.ResponseStickyTTL
			p.BindWarning(groupID, o.AccountID, responseID, stateStore.BindResponseAccount(ctx, groupID, responseID, o.AccountID, ttl))
			stateStore.BindResponseConn(responseID, connID, ttl)
			p.BindOwner(ctx, responseID)
		}
		if stateStore != nil && state.StoreDisabled && state.SessionHash != "" {
			stateStore.BindSessionConn(groupID, state.SessionHash, connID, o.SessionStickyTTL)
		}
		if connID != "" {
			state.PreferredConnID = connID
		}

		nextClientMessage, readErr := p.ReadClient()
		if readErr != nil {
			if p.IsDisconnect(readErr) {
				closeStatus, closeReason := p.SummarizeClose(readErr)
				p.Log(fmt.Sprintf(
					"ingress_ws_client_closed account_id=%d conn_id=%s close_status=%s close_reason=%s",
					o.AccountID,
					p.TruncateLog(connID, 64),
					closeStatus,
					p.TruncateLog(closeReason, 120),
				))
				return nil
			}
			return fmt.Errorf("read client websocket request: %w", readErr)
		}

		nextPayload, parseErr := p.Parse(nextClientMessage, true, turn+1)
		if parseErr != nil {
			return parseErr
		}
		p.UpdateHeaders(nextPayload, state.TurnState)

		if nextPayload.PreviousResponseID != "" {
			expectedPrev := strings.TrimSpace(lastTurnResponseID)
			chainedFromLast := expectedPrev != "" && nextPayload.PreviousResponseID == expectedPrev
			nextPreviousResponseIDKind := codec.ClassifyPrevious(nextPayload.PreviousResponseID)
			p.Log(fmt.Sprintf(
				"ingress_ws_next_turn_chain account_id=%d turn=%d next_turn=%d conn_id=%s previous_response_id=%s previous_response_id_kind=%s last_turn_response_id=%s chained_from_last=%v has_prompt_cache_key=%v store_disabled=%v",
				o.AccountID,
				turn,
				turn+1,
				p.TruncateLog(connID, 64),
				p.TruncateLog(nextPayload.PreviousResponseID, 64),
				p.NormalizeLog(nextPreviousResponseIDKind),
				p.TruncateLog(expectedPrev, 64),
				chainedFromLast,
				nextPayload.PromptCacheKey != "",
				state.StoreDisabled,
			))
		}
		if stateStore != nil && nextPayload.PreviousResponseID != "" {
			if stickyConnID, ok := stateStore.GetResponseConn(nextPayload.PreviousResponseID); ok {
				if sessionConnID != "" && stickyConnID != "" && stickyConnID != sessionConnID {
					p.Log(fmt.Sprintf(
						"ingress_ws_keep_session_conn account_id=%d turn=%d conn_id=%s sticky_conn_id=%s previous_response_id=%s",
						o.AccountID,
						turn,
						p.TruncateLog(sessionConnID, 64),
						p.TruncateLog(stickyConnID, 64),
						p.TruncateLog(nextPayload.PreviousResponseID, 64),
					))
				} else {
					state.PreferredConnID = stickyConnID
				}
			}
		}
		currentPayload = nextPayload.PayloadRaw
		currentOriginalModel = nextPayload.OriginalModel
		currentRoutingModel = nextPayload.RoutingModel
		currentImageBillingModel = nextPayload.ImageBillingModel
		currentImageSizeTier = nextPayload.ImageSizeTier
		currentImageInputSize = nextPayload.ImageInputSize
		currentPayloadBytes = nextPayload.PayloadBytes
		currentRequestedReasoningEffort = nextPayload.RequestedReasoningEffort
		state.StoreDisabled = p.StoreDisabled(currentPayload)
		if !state.StoreDisabled {
			unpinSessionConn(sessionConnID)
		}
		turn++
	}
}
