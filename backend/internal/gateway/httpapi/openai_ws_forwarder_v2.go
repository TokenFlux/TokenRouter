package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	protocolwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func (s *OpenAIWebSocketExecutor) forwardOpenAIWSV2(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	reqBody map[string]any,
	clientPromptCacheKey string,
	token string,
	decision egress.OpenAIWSProtocolDecision,
	isCodexCLI bool,
	reqStream bool,
	originalModel string,
	mappedModel string,
	startTime time.Time,
	attempt int,
	lastFailureReason string,
	tlsRouterMatch egress.TLSFingerprintRouterMatchResult,
	agentTaskRecoveryTried *bool,
) (*forwardcore.OpenAIResult, error) {
	if s == nil || account == nil {
		return nil, ws.WrapFallback("invalid_state", errors.New("service or account is nil"))
	}
	responseModelObserver := UpstreamResponseModelObserverFromContext(c)
	if responseModelObserver == nil {
		responseModelObserver = BeginUpstreamResponseModelObservation(c)
	}

	wsURL, err := s.buildOpenAIResponsesWSURL(account)
	if err != nil {
		return nil, ws.WrapFallback("build_ws_url", err)
	}
	wsHost := "-"
	wsPath := "-"
	if parsed, parseErr := url.Parse(wsURL); parseErr == nil && parsed != nil {
		if h := strings.TrimSpace(parsed.Host); h != "" {
			wsHost = gatewayprovider.NormalizeOpenAIWSLogValue(h)
		}
		if p := strings.TrimSpace(parsed.Path); p != "" {
			wsPath = gatewayprovider.NormalizeOpenAIWSLogValue(p)
		}
	}
	gatewayprovider.LogOpenAIWSModeDebug(
		"dial_target account_id=%d account_type=%s ws_host=%s ws_path=%s",
		account.Record.ID,
		account.Record.Type,
		wsHost,
		wsPath,
	)

	payload := s.buildOpenAIWSCreatePayload(reqBody, account)
	payloadStrategy, removedKeys := openai.ApplyWSRetryPayloadStrategy(payload, attempt)
	turnState := ""
	turnMetadata := ""
	if c != nil && c.Request != nil {
		turnState = strings.TrimSpace(c.GetHeader(openAIWSTurnStateHeader))
		turnMetadata = strings.TrimSpace(c.GetHeader(openai.WSTurnMetadataHeader))
	}
	openai.SetOpenAIWSTurnMetadata(payload, turnMetadata)
	ApplyStagedCodexFingerprintClientMetadata(c, account.View(), payload)
	previousResponseID := protocolwire.WSPayloadString(payload, "previous_response_id")
	previousResponseIDKind := protocolwire.ClassifyOpenAIPreviousResponseIDKind(previousResponseID)
	promptCacheKey := strings.TrimSpace(clientPromptCacheKey)
	if promptCacheKey == "" {
		// Fingerprint convergence may inject a default key when the client did
		// not send one; retain that fallback without replacing an explicit raw key.
		promptCacheKey = protocolwire.WSPayloadString(payload, "prompt_cache_key")
	}
	_, hasTools := payload["tools"]
	debugEnabled := gatewayprovider.IsOpenAIWSModeDebugEnabled()
	payloadBytes := -1
	resolvePayloadBytes := func() int {
		if payloadBytes >= 0 {
			return payloadBytes
		}
		payloadBytes = len(payloadAsJSONBytes(payload))
		return payloadBytes
	}
	streamValue := "-"
	if raw, ok := payload["stream"]; ok {
		streamValue = gatewayprovider.NormalizeOpenAIWSLogValue(strings.TrimSpace(fmt.Sprintf("%v", raw)))
	}
	payloadEventType := protocolwire.WSPayloadString(payload, "type")
	if payloadEventType == "" {
		payloadEventType = "response.create"
	}
	if s.shouldEmitOpenAIWSPayloadSchema(attempt) {
		gatewayprovider.LogOpenAIWSModeInfo(
			"[debug] payload_schema account_id=%d attempt=%d event=%s payload_keys=%s payload_bytes=%d payload_key_sizes=%s input_summary=%s stream=%s payload_strategy=%s removed_keys=%s has_previous_response_id=%v has_prompt_cache_key=%v has_tools=%v",
			account.Record.ID,
			attempt,
			payloadEventType, gatewayprovider.NormalizeOpenAIWSLogValue(strings.Join(gatewayprovider.SortedOpenAIWSPayloadKeys(payload), ",")), resolvePayloadBytes(), gatewayprovider.NormalizeOpenAIWSLogValue(gatewayprovider.SummarizeOpenAIWSPayloadKeySizes(payload, openAIWSPayloadKeySizeTopN)), gatewayprovider.NormalizeOpenAIWSLogValue(gatewayprovider.SummarizeOpenAIWSInput(payload["input"])), streamValue, gatewayprovider.NormalizeOpenAIWSLogValue(payloadStrategy), gatewayprovider.NormalizeOpenAIWSLogValue(strings.Join(removedKeys, ",")), previousResponseID != "",
			promptCacheKey != "",
			hasTools,
		)
	}

	stateStore := s.State
	groupID := OpenAIResponseGroupID(c)
	sessionHash := GenerateOpenAISessionHash(c, nil)
	if sessionHash == "" {
		var legacySessionHash string
		sessionHash, legacySessionHash = scheduler.DeriveSessionHashes(promptCacheKey)
		AttachOpenAILegacySessionHash(c, legacySessionHash)
	}
	if turnState == "" && stateStore != nil && sessionHash != "" {
		if savedTurnState, ok := stateStore.GetSessionTurnState(groupID, sessionHash); ok {
			turnState = savedTurnState
		}
	}
	preferredConnID := ""
	if stateStore != nil && previousResponseID != "" {
		if connID, ok := stateStore.GetResponseConn(previousResponseID); ok {
			preferredConnID = connID
		}
	}
	storeDisabled := s.isOpenAIWSStoreDisabledInRequest(reqBody, account)
	if stateStore != nil && storeDisabled && previousResponseID == "" && sessionHash != "" {
		if connID, ok := stateStore.GetSessionConn(groupID, sessionHash); ok {
			preferredConnID = connID
		}
	}
	storeDisabledConnMode := s.openAIWSStoreDisabledConnMode()
	forceNewConnByPolicy := openai.ShouldForceNewConnOnStoreDisabled(storeDisabledConnMode, lastFailureReason)
	forceNewConn := forceNewConnByPolicy && storeDisabled && previousResponseID == "" && sessionHash != "" && preferredConnID == ""
	wsHeaders, sessionResolution, buildHdrErr := s.buildOpenAIWSHeaders(
		ctx,
		c,
		account,
		token,
		decision,
		isCodexCLI,
		turnState,
		turnMetadata,
		promptCacheKey, protocolwire.WSPayloadString(payload, "model"), protocolwire.WSPayloadString(payload, "service_tier"), tlsRouterMatch,
	)
	if buildHdrErr != nil {
		return nil, fmt.Errorf("build ws headers: %w", buildHdrErr)
	}
	gatewayprovider.LogOpenAIWSModeDebug(
		"acquire_start account_id=%d account_type=%s transport=%s preferred_conn_id=%s has_previous_response_id=%v session_hash=%s has_turn_state=%v turn_state_len=%d has_turn_metadata=%v turn_metadata_len=%d store_disabled=%v store_disabled_conn_mode=%s retry_last_reason=%s force_new_conn=%v header_user_agent=%s header_openai_beta=%s header_originator=%s header_accept_language=%s header_session_id=%s header_conversation_id=%s session_id_source=%s conversation_id_source=%s has_prompt_cache_key=%v has_chatgpt_account_id=%v has_authorization=%v has_session_id=%v has_conversation_id=%v proxy_enabled=%v",
		account.Record.ID,
		account.Record.Type, gatewayprovider.NormalizeOpenAIWSLogValue(string(decision.Transport)), gatewayprovider.TruncateOpenAIWSLogValue(preferredConnID, gatewayprovider.OpenAIWSIDValueMaxLen), previousResponseID != "", gatewayprovider.TruncateOpenAIWSLogValue(sessionHash, 12), turnState != "",
		len(turnState),
		turnMetadata != "",
		len(turnMetadata),
		storeDisabled, gatewayprovider.NormalizeOpenAIWSLogValue(storeDisabledConnMode), gatewayprovider.TruncateOpenAIWSLogValue(lastFailureReason, gatewayprovider.OpenAIWSLogValueMaxLen), forceNewConn, gatewayprovider.OpenAIWSHeaderValueForLog(wsHeaders, "user-agent"), gatewayprovider.OpenAIWSHeaderValueForLog(wsHeaders, "openai-beta"), gatewayprovider.OpenAIWSHeaderValueForLog(wsHeaders, "originator"), gatewayprovider.OpenAIWSHeaderValueForLog(wsHeaders, "accept-language"), gatewayprovider.OpenAIWSHeaderValueForLog(wsHeaders, "session_id"), gatewayprovider.OpenAIWSHeaderValueForLog(wsHeaders, "conversation_id"), gatewayprovider.NormalizeOpenAIWSLogValue(sessionResolution.SessionSource), gatewayprovider.NormalizeOpenAIWSLogValue(sessionResolution.ConversationSource), promptCacheKey != "", gatewayprovider.HasOpenAIWSHeader(wsHeaders, "chatgpt-account-id"), gatewayprovider.HasOpenAIWSHeader(wsHeaders, "authorization"), gatewayprovider.HasOpenAIWSHeader(wsHeaders, "session_id"), gatewayprovider.HasOpenAIWSHeader(wsHeaders, "conversation_id"), account.Record.ProxyID != nil && account.Record.Proxy != nil,
	)

	acquireCtx, acquireCancel := context.WithTimeout(ctx, s.openAIWSAcquireTimeout())
	defer acquireCancel()
	tlsProfile, tlsProfileKey := s.Requests.WSTLSProfile(account, tlsRouterMatch)

	lease, err := s.Connections.Pool().Acquire(acquireCtx, openai.WSAcquireRequest{
		Account: openAIWSPoolAccountView(account),
		WSURL:   wsURL,
		Headers: wsHeaders,
		HeadersFactory: func(factoryCtx context.Context, headers http.Header) (http.Header, error) {
			return s.Requests.Identity.RefreshHeaders(factoryCtx, account, headers)
		},
		PreferredConnID: preferredConnID,
		ForceNewConn:    forceNewConn,
		TLSProfile:      tlsProfile,
		TLSProfileKey:   tlsProfileKey,
		ProxyURL: func() string {
			if account.Record.ProxyID != nil && account.Record.Proxy != nil {
				return account.Record.Proxy.URL()
			}
			return ""
		}(),
	})
	if err != nil {
		var agentDialErr *openai.WSDialError
		if s.Requests.Identity.UsesAgentIdentity(ctx, account) && errors.As(err, &agentDialErr) && openai.IsAgentTaskInvalidWSDialError(agentDialErr) && agentTaskRecoveryTried != nil && !*agentTaskRecoveryTried {
			*agentTaskRecoveryTried = true
			if recoveryErr := s.Requests.Identity.Recover(ctx, account, account.View().GetCredential("task_id")); recoveryErr != nil {
				return nil, fmt.Errorf("agent identity task recovery failed: %w", recoveryErr)
			}
			return nil, &forwardcore.AgentIdentityTaskRecoveredError{}
		}
		errorDecision := s.handleOpenAIWSDialTransientFailure(ctx, account, mappedModel, err)
		dialStatus, dialClass, dialCloseStatus, dialCloseReason, dialRespServer, dialRespVia, dialRespCFRay, dialRespReqID := gatewayprovider.SummarizeOpenAIWSDialError(err)
		gatewayprovider.LogOpenAIWSModeInfo(
			"acquire_fail account_id=%d account_type=%s transport=%s reason=%s dial_status=%d dial_class=%s dial_close_status=%s dial_close_reason=%s dial_resp_server=%s dial_resp_via=%s dial_resp_cf_ray=%s dial_resp_x_request_id=%s cause=%s preferred_conn_id=%s force_new_conn=%v ws_host=%s ws_path=%s proxy_enabled=%v",
			account.Record.ID,
			account.Record.Type, gatewayprovider.NormalizeOpenAIWSLogValue(string(decision.Transport)), gatewayprovider.NormalizeOpenAIWSLogValue(openai.ClassifyWSAcquireError(err)), dialStatus,
			dialClass,
			dialCloseStatus, gatewayprovider.TruncateOpenAIWSLogValue(dialCloseReason, gatewayprovider.OpenAIWSHeaderValueMaxLen), dialRespServer,
			dialRespVia,
			dialRespCFRay,
			dialRespReqID, gatewayprovider.TruncateOpenAIWSLogValue(err.Error(), gatewayprovider.OpenAIWSLogValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(preferredConnID, gatewayprovider.OpenAIWSIDValueMaxLen), forceNewConn,
			wsHost,
			wsPath,
			account.Record.ProxyID != nil && account.Record.Proxy != nil,
		)
		var policyDialErr *openai.WSDialError
		if errors.As(err, &policyDialErr) && policyDialErr != nil && policyDialErr.StatusCode != 0 {
			if errorDecision.ShouldReturnGenericError() {
				return nil, ws.NewGenericPolicyError(policyDialErr.StatusCode)
			}
			if errorDecision.ShouldFailoverWithDefaults(
				gatewayprovider.ExecutionErrorPolicy(account),
				policyDialErr.StatusCode,
				false,
				s.shouldFailoverOpenAIWSError(account, policyDialErr.StatusCode, policyDialErr.ResponseBody),
			) {
				return nil, gatewayprovider.NewOpenAIUpstreamFailure(
					policyDialErr.StatusCode,
					policyDialErr.ResponseHeaders,
					policyDialErr.ResponseBody,
					upstream.ExtractErrorMessage(policyDialErr.ResponseBody),
					errorDecision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), policyDialErr.StatusCode),
				)
			}
		}
		return nil, ws.WrapFallback(openai.ClassifyWSAcquireError(err), err)
	}
	// cleanExit 标记正常终端事件退出，此时上游不会再发送帧，连接可安全归还复用。
	// 所有异常路径（读写错误、error 事件等）已在各自分支中提前调用 MarkBroken，
	// 因此 defer 中只需处理正常退出时不 MarkBroken 即可。
	cleanExit := false
	defer func() {
		if !cleanExit {
			lease.MarkBroken()
		}
		lease.Release()
	}()
	connID := strings.TrimSpace(lease.ConnID())
	gatewayprovider.LogOpenAIWSModeDebug(
		"connected account_id=%d account_type=%s transport=%s conn_id=%s conn_reused=%v conn_pick_ms=%d queue_wait_ms=%d has_previous_response_id=%v",
		account.Record.ID,
		account.Record.Type, gatewayprovider.NormalizeOpenAIWSLogValue(string(decision.Transport)), connID,
		lease.Reused(),
		lease.ConnPickDuration().Milliseconds(),
		lease.QueueWaitDuration().Milliseconds(),
		previousResponseID != "",
	)
	if previousResponseID != "" {
		gatewayprovider.LogOpenAIWSModeInfo(
			"continuation_probe account_id=%d account_type=%s conn_id=%s previous_response_id=%s previous_response_id_kind=%s preferred_conn_id=%s conn_reused=%v store_disabled=%v session_hash=%s header_session_id=%s header_conversation_id=%s session_id_source=%s conversation_id_source=%s has_turn_state=%v turn_state_len=%d has_prompt_cache_key=%v",
			account.Record.ID,
			account.Record.Type, gatewayprovider.TruncateOpenAIWSLogValue(connID, gatewayprovider.OpenAIWSIDValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(previousResponseID, gatewayprovider.OpenAIWSIDValueMaxLen), gatewayprovider.NormalizeOpenAIWSLogValue(previousResponseIDKind), gatewayprovider.TruncateOpenAIWSLogValue(preferredConnID, gatewayprovider.OpenAIWSIDValueMaxLen), lease.Reused(),
			storeDisabled, gatewayprovider.TruncateOpenAIWSLogValue(sessionHash, 12), gatewayprovider.OpenAIWSHeaderValueForLog(wsHeaders, "session_id"), gatewayprovider.OpenAIWSHeaderValueForLog(wsHeaders, "conversation_id"), gatewayprovider.NormalizeOpenAIWSLogValue(sessionResolution.SessionSource), gatewayprovider.NormalizeOpenAIWSLogValue(sessionResolution.ConversationSource), turnState != "",
			len(turnState),
			promptCacheKey != "",
		)
	}
	if c != nil {
		SetOpsLatencyMs(c, OpsOpenAIWSConnPickMsKey, lease.ConnPickDuration().Milliseconds())
		SetOpsLatencyMs(c, OpsOpenAIWSQueueWaitMsKey, lease.QueueWaitDuration().Milliseconds())
		c.Set(OpsOpenAIWSConnReusedKey, lease.Reused())
		if connID != "" {
			c.Set(OpsOpenAIWSConnIDKey, connID)
		}
	}

	handshakeTurnState := strings.TrimSpace(lease.HandshakeHeader(openAIWSTurnStateHeader))
	gatewayprovider.LogOpenAIWSModeDebug(
		"handshake account_id=%d conn_id=%s has_turn_state=%v turn_state_len=%d",
		account.Record.ID,
		connID,
		handshakeTurnState != "",
		len(handshakeTurnState),
	)
	if handshakeTurnState != "" {
		if stateStore != nil && sessionHash != "" {
			stateStore.BindSessionTurnState(groupID, sessionHash, handshakeTurnState, s.Selection.SessionStickyTTL())
		}
		if c != nil {
			c.Header(http.CanonicalHeaderKey(openAIWSTurnStateHeader), handshakeTurnState)
		}
	}

	if err := s.performOpenAIWSGeneratePrewarm(
		ctx,
		lease,
		decision,
		payload,
		previousResponseID,
		reqBody,
		mappedModel,
		account,
		stateStore,
		groupID,
	); err != nil {
		return nil, err
	}

	if err := lease.WriteJSONWithContextTimeout(ctx, payload, s.openAIWSWriteTimeout()); err != nil {
		lease.MarkBroken()
		gatewayprovider.LogOpenAIWSModeInfo(
			"write_request_fail account_id=%d conn_id=%s cause=%s payload_bytes=%d",
			account.Record.ID,
			connID, gatewayprovider.TruncateOpenAIWSLogValue(err.Error(), gatewayprovider.OpenAIWSLogValueMaxLen), resolvePayloadBytes(),
		)
		return nil, ws.WrapFallback("write_request", err)
	}
	if debugEnabled {
		gatewayprovider.LogOpenAIWSModeDebug(
			"write_request_sent account_id=%d conn_id=%s stream=%v payload_bytes=%d previous_response_id=%s",
			account.Record.ID,
			connID,
			reqStream,
			resolvePayloadBytes(), gatewayprovider.TruncateOpenAIWSLogValue(previousResponseID, gatewayprovider.OpenAIWSIDValueMaxLen),
		)
	}

	usage := &protocolwire.ForwardUsage{}
	imageCounter := protocolwire.NewOpenAIImageOutputCounter()
	var firstTokenMs *int
	responseID := ""
	var finalResponse []byte
	responseAccumulator := bridge.NewBufferedResponseAccumulator()
	wroteDownstream := false
	needModelReplace := originalModel != mappedModel
	var mappedModelBytes []byte
	if needModelReplace && mappedModel != "" {
		mappedModelBytes = []byte(mappedModel)
	}
	bufferedStreamEvents := make([][]byte, 0, 4)
	eventCount := 0
	tokenEventCount := 0
	terminalEventCount := 0
	bufferedEventCount := 0
	flushedBufferedEventCount := 0
	firstEventType := ""
	lastEventType := ""
	var upstreamWarning *forwardcore.UpstreamWarning
	upstreamTerminalEvent := ""

	var flusher http.Flusher
	if reqStream {
		if s.Output.Headers != nil {
			provider.WriteFilteredHeaders(c.Writer.Header(), http.Header{}, s.Output.Headers)
		}
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		f, ok := c.Writer.(http.Flusher)
		if !ok {
			lease.MarkBroken()
			return nil, ws.WrapFallback("streaming_not_supported", errors.New("streaming not supported"))
		}
		flusher = f
	}

	clientDisconnected := false
	flushBatchSize := s.openAIWSEventFlushBatchSize()
	flushInterval := s.openAIWSEventFlushInterval()
	pendingFlushEvents := 0
	lastFlushAt := time.Now()
	flushStreamWriter := func(force bool) {
		if clientDisconnected || flusher == nil || pendingFlushEvents <= 0 {
			return
		}
		if !force && flushBatchSize > 1 && pendingFlushEvents < flushBatchSize {
			if flushInterval <= 0 || time.Since(lastFlushAt) < flushInterval {
				return
			}
		}
		flusher.Flush()
		pendingFlushEvents = 0
		lastFlushAt = time.Now()
	}
	emitStreamMessage := func(message []byte, forceFlush bool) {
		if clientDisconnected {
			return
		}
		frame := make([]byte, 0, len(message)+8)
		frame = append(frame, "data: "...)
		frame = append(frame, message...)
		frame = append(frame, '\n', '\n')
		_, wErr := c.Writer.Write(frame)
		if wErr == nil {
			wroteDownstream = true
			pendingFlushEvents++
			flushStreamWriter(forceFlush)
			return
		}
		clientDisconnected = true
		logging.LegacyPrintf("service.openai_gateway", "[OpenAI WS Mode] client disconnected, continue draining upstream: account=%d", account.Record.ID)
	}
	flushBufferedStreamEvents := func(reason string) {
		if len(bufferedStreamEvents) == 0 {
			return
		}
		flushed := len(bufferedStreamEvents)
		for _, buffered := range bufferedStreamEvents {
			emitStreamMessage(buffered, false)
		}
		bufferedStreamEvents = bufferedStreamEvents[:0]
		flushStreamWriter(true)
		flushedBufferedEventCount += flushed
		if debugEnabled {
			gatewayprovider.LogOpenAIWSModeDebug(
				"buffer_flush account_id=%d conn_id=%s reason=%s flushed=%d total_flushed=%d client_disconnected=%v",
				account.Record.ID,
				connID, gatewayprovider.TruncateOpenAIWSLogValue(reason, gatewayprovider.OpenAIWSLogValueMaxLen), flushed,
				flushedBufferedEventCount,
				clientDisconnected,
			)
		}
	}

	readTimeout := s.openAIWSReadTimeout()
	var pendingJSONDocuments [][]byte

	for {
		var message []byte
		var readErr error
		if len(pendingJSONDocuments) > 0 {
			message = pendingJSONDocuments[0]
			pendingJSONDocuments = pendingJSONDocuments[1:]
		} else {
			message, readErr = lease.ReadMessageWithContextTimeout(ctx, readTimeout)
			if readErr == nil {
				if documents, repaired := protocolwire.SplitConcatenatedJSONDocuments(message); repaired {
					gatewayprovider.LogOpenAIWSModeInfo(
						"concatenated_json_repaired account_id=%d conn_id=%s documents=%d bytes=%d",
						account.Record.ID, gatewayprovider.TruncateOpenAIWSLogValue(connID, gatewayprovider.OpenAIWSIDValueMaxLen), len(documents),
						len(message),
					)
					message = documents[0]
					pendingJSONDocuments = append(pendingJSONDocuments, documents[1:]...)
				}
			}
		}
		// 拼接文档修复后仍不是完整 JSON 的事件不得进入解析或下游输出链路。
		if readErr == nil && !json.Valid(message) {
			eventType, _, _ := protocolwire.ParseWSEventEnvelope(message)
			if eventType == "" {
				eventType = "unknown"
			}
			lease.MarkBroken()
			gatewayprovider.LogOpenAIWSModeInfo(
				"invalid_event_json account_id=%d conn_id=%s event_type=%s bytes=%d wrote_downstream=%v",
				account.Record.ID, gatewayprovider.TruncateOpenAIWSLogValue(connID, gatewayprovider.OpenAIWSIDValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(eventType, gatewayprovider.OpenAIWSLogValueMaxLen), len(message),
				wroteDownstream,
			)
			if !wroteDownstream {
				return nil, ws.WrapFallback("invalid_event_json", errors.New("upstream websocket returned malformed Responses event JSON"))
			}
			return nil, errors.New("upstream websocket returned malformed Responses event JSON after downstream output")
		}
		if readErr != nil {
			lease.MarkBroken()
			closeStatus, closeReason := gatewayprovider.SummarizeOpenAIWSReadCloseError(readErr)
			gatewayprovider.LogOpenAIWSModeInfo(
				"read_fail account_id=%d conn_id=%s wrote_downstream=%v close_status=%s close_reason=%s cause=%s events=%d token_events=%d terminal_events=%d buffered_pending=%d buffered_flushed=%d first_event=%s last_event=%s",
				account.Record.ID,
				connID,
				wroteDownstream,
				closeStatus,
				closeReason, gatewayprovider.TruncateOpenAIWSLogValue(readErr.Error(), gatewayprovider.OpenAIWSLogValueMaxLen), eventCount,
				tokenEventCount,
				terminalEventCount,
				len(bufferedStreamEvents),
				flushedBufferedEventCount, gatewayprovider.TruncateOpenAIWSLogValue(firstEventType, gatewayprovider.OpenAIWSLogValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(lastEventType, gatewayprovider.OpenAIWSLogValueMaxLen),
			)
			if !wroteDownstream {
				return nil, ws.WrapFallback(gatewayprovider.ClassifyOpenAIWSReadFallbackReason(readErr), readErr)
			}
			if clientDisconnected {
				break
			}
			SetOpsUpstreamError(c, 0, logredact.SanitizeUpstreamQueries(readErr.Error()), "")
			return nil, fmt.Errorf("openai ws read event: %w", readErr)
		}
		if normalized, changed := protocolwire.NormalizeCompletedImageGenerationStatus(message); changed {
			message = normalized
		}

		eventType, eventResponseID, responseField := protocolwire.ParseWSEventEnvelope(message)
		if eventType == "" {
			continue
		}
		responseModelObserver.ObserveOpenAI(message, eventType)
		eventCount++
		if firstEventType == "" {
			firstEventType = eventType
		}
		lastEventType = eventType

		if responseID == "" && eventResponseID != "" {
			responseID = eventResponseID
		}

		isTokenEvent := protocolwire.IsWSTokenEvent(eventType)
		if isTokenEvent {
			tokenEventCount++
		}
		isTerminalEvent := protocolwire.IsWSTerminalEvent(eventType)
		if isTerminalEvent {
			terminalEventCount++
		}
		if firstTokenMs == nil && isTokenEvent {
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}
		if debugEnabled && gatewayprovider.ShouldLogOpenAIWSEvent(eventCount, eventType) {
			gatewayprovider.LogOpenAIWSModeDebug(
				"event_received account_id=%d conn_id=%s idx=%d type=%s bytes=%d token=%v terminal=%v buffered_pending=%d",
				account.Record.ID,
				connID,
				eventCount, gatewayprovider.TruncateOpenAIWSLogValue(eventType, gatewayprovider.OpenAIWSLogValueMaxLen), len(message),
				isTokenEvent,
				isTerminalEvent,
				len(bufferedStreamEvents),
			)
		}

		if !clientDisconnected {
			if needModelReplace && len(mappedModelBytes) > 0 && protocolwire.WSEventMayContainModel(eventType) && bytes.Contains(message, mappedModelBytes) {
				message = protocolwire.ReplaceWSMessageModel(message, mappedModel, originalModel)
			}
			if protocolwire.WSEventMayContainToolCalls(eventType) && protocolwire.WSMessageLikelyContainsToolCalls(message) {
				if corrected, changed := s.Output.Corrector.CorrectToolCallsInSSEBytes(message); changed {
					message = corrected
				}
			}
			message = RestoreCodexToolNamesFromContext(c, message)
		}
		if protocolwire.WSEventShouldParseUsage(eventType) {
			protocolwire.ParseWSResponseUsageFromCompletedEvent(message, usage)
		}
		if eventType == "error" || eventType == "response.failed" {
			MarkOpenAICyberPolicyEvent(c, message, http.StatusOK, usage)
		}
		var responseEvent protocolwire.ResponsesStreamEvent
		if err := json.Unmarshal(message, &responseEvent); err == nil {
			responseAccumulator.ProcessEvent(&responseEvent)
		}
		imageCounter.AddSSEData(message)
		if warning := buildOpenAIWSUpstreamWarning(eventType, message); warning != nil {
			upstreamWarning = warning
		}
		terminalPolicy := openAIWSTerminalPolicyDecision{
			TerminalEvent: normalizeOpenAIWSTerminalEvent(eventType),
			Decision:      accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone},
		}
		if isTerminalEvent {
			terminalPolicy = s.handleOpenAIWSTerminalTransientFailure(ctx, account, mappedModel, lease.HandshakeHeaders(), message)
		}
		if eventType == "response.failed" {
			if terminalPolicy.Decision.ShouldReturnGenericError() {
				if !wroteDownstream {
					lease.MarkBroken()
					return nil, ws.NewGenericPolicyError(terminalPolicy.StatusCode)
				}
				// 流已提交时无法改写 HTTP 状态，只下发净化后的通用终止事件。
				message = protocolwire.GenericFailedEventPayload()
			}
			if !wroteDownstream && terminalPolicy.Decision.ShouldFailoverWithDefaults(
				gatewayprovider.ExecutionErrorPolicy(account),
				terminalPolicy.StatusCode,
				false,
				s.shouldFailoverOpenAIWSError(account, terminalPolicy.StatusCode, message),
			) {
				lease.MarkBroken()
				return nil, gatewayprovider.NewOpenAIUpstreamFailure(
					terminalPolicy.StatusCode,
					lease.HandshakeHeaders(),
					message,
					openai.ExtractOpenAISSEErrorMessage(message),
					terminalPolicy.Decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), terminalPolicy.StatusCode),
				)
			}
		}

		if eventType == "error" {
			errCodeRaw, errTypeRaw, errMsgRaw := protocolwire.ParseWSErrorEventFields(message)
			statusCode := openAIWSErrorPolicyStatus(message)
			errorDecision := s.handleOpenAIWSErrorEventTransientFailure(ctx, account, mappedModel, lease.HandshakeHeaders(), message)
			errMsg := strings.TrimSpace(errMsgRaw)
			if errMsg == "" {
				errMsg = "Upstream websocket error"
			}
			fallbackReason, canFallback := openai.ClassifyWSErrorEventFromRaw(errCodeRaw, errTypeRaw, errMsgRaw)
			errCode, errType, errMessage := gatewayprovider.SummarizeOpenAIWSErrorEventFieldsFromRaw(errCodeRaw, errTypeRaw, errMsgRaw)
			gatewayprovider.LogOpenAIWSModeInfo(
				"error_event account_id=%d conn_id=%s idx=%d fallback_reason=%s can_fallback=%v err_code=%s err_type=%s err_message=%s",
				account.Record.ID,
				connID,
				eventCount, gatewayprovider.TruncateOpenAIWSLogValue(fallbackReason, gatewayprovider.OpenAIWSLogValueMaxLen), canFallback,
				errCode,
				errType,
				errMessage,
			)
			if fallbackReason == "previous_response_not_found" {
				gatewayprovider.LogOpenAIWSModeInfo(
					"previous_response_not_found_diag account_id=%d account_type=%s conn_id=%s previous_response_id=%s previous_response_id_kind=%s response_id=%s event_idx=%d req_stream=%v store_disabled=%v conn_reused=%v session_hash=%s header_session_id=%s header_conversation_id=%s session_id_source=%s conversation_id_source=%s has_turn_state=%v turn_state_len=%d has_prompt_cache_key=%v err_code=%s err_type=%s err_message=%s",
					account.Record.ID,
					account.Record.Type,
					connID, gatewayprovider.TruncateOpenAIWSLogValue(previousResponseID, gatewayprovider.OpenAIWSIDValueMaxLen), gatewayprovider.NormalizeOpenAIWSLogValue(previousResponseIDKind), gatewayprovider.TruncateOpenAIWSLogValue(responseID, gatewayprovider.OpenAIWSIDValueMaxLen), eventCount,
					reqStream,
					storeDisabled,
					lease.Reused(), gatewayprovider.TruncateOpenAIWSLogValue(sessionHash, 12), gatewayprovider.OpenAIWSHeaderValueForLog(wsHeaders, "session_id"), gatewayprovider.OpenAIWSHeaderValueForLog(wsHeaders, "conversation_id"), gatewayprovider.NormalizeOpenAIWSLogValue(sessionResolution.SessionSource), gatewayprovider.NormalizeOpenAIWSLogValue(sessionResolution.ConversationSource), turnState != "",
					len(turnState),
					promptCacheKey != "",
					errCode,
					errType,
					errMessage,
				)
			}
			// error 事件后连接不再可复用，避免回池后污染下一请求。
			lease.MarkBroken()
			if upstreamWarning != nil {
				upstreamWarning.StatusCode = statusCode
			}
			if !wroteDownstream && errorDecision.ShouldReturnGenericError() {
				return nil, ws.NewGenericPolicyError(statusCode)
			}
			if !wroteDownstream && errorDecision.ShouldFailoverWithDefaults(
				gatewayprovider.ExecutionErrorPolicy(account),
				statusCode,
				false,
				s.shouldFailoverOpenAIWSError(account, statusCode, message),
			) {
				return nil, gatewayprovider.NewOpenAIUpstreamFailure(
					statusCode,
					lease.HandshakeHeaders(),
					message,
					errMsg,
					errorDecision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), statusCode),
				)
			}
			if !wroteDownstream && canFallback {
				if gatewayprovider.OpenAIUpstreamWarningIsCyber(upstreamWarning) {
					// 可 fallback 的 error 事件也可能是 cyber 风控拒绝，必须保留原始 warning 防止重试覆盖。
					return nil, ws.WrapFallback(fallbackReason, &openAIWSUpstreamWarningError{
						warning: upstreamWarning,
						err:     errors.New(errMsg),
					})
				}
				return nil, ws.WrapFallback(fallbackReason, errors.New(errMsg))
			}
			SetOpsUpstreamError(c, statusCode, errMsg, "")
			if reqStream && !clientDisconnected {
				flushBufferedStreamEvents("error_event")
				emitStreamMessage(message, true)
			}
			if !reqStream {
				c.JSON(statusCode, gin.H{
					"error": gin.H{
						"type":    "upstream_error",
						"message": errMsg,
					},
				})
			}
			return nil, fmt.Errorf("openai ws error event: %s", errMsg)
		}

		if reqStream {
			if responseField.Exists() && responseField.Type == gjson.JSON {
				finalResponse = []byte(responseField.Raw)
				// 流式响应缺少 output 时，以已维护的协议状态补齐最终响应。
				if len(gjson.GetBytes(finalResponse, "output").Array()) == 0 && responseAccumulator.HasContent() {
					if outputJSON, err := json.Marshal(responseAccumulator.BuildOutput()); err == nil {
						if patched, err := sjson.SetRawBytes(finalResponse, "output", outputJSON); err == nil {
							finalResponse = patched
						}
					}
				}
			}
			// 在首个 token 前先缓冲事件（如 response.created），
			// 以便上游早期断连时仍可安全回退到 HTTP，不给下游发送半截流。
			shouldBuffer := firstTokenMs == nil && !isTokenEvent && !isTerminalEvent
			if shouldBuffer {
				buffered := make([]byte, len(message))
				copy(buffered, message)
				bufferedStreamEvents = append(bufferedStreamEvents, buffered)
				bufferedEventCount++
				if debugEnabled && gatewayprovider.ShouldLogOpenAIWSBufferedEvent(bufferedEventCount) {
					gatewayprovider.LogOpenAIWSModeDebug(
						"buffer_enqueue account_id=%d conn_id=%s idx=%d event_idx=%d event_type=%s buffer_size=%d",
						account.Record.ID,
						connID,
						bufferedEventCount,
						eventCount, gatewayprovider.TruncateOpenAIWSLogValue(eventType, gatewayprovider.OpenAIWSLogValueMaxLen), len(bufferedStreamEvents),
					)
				}
			} else {
				flushBufferedStreamEvents(eventType)
				emitStreamMessage(message, isTerminalEvent)
			}
		} else {
			if responseField.Exists() && responseField.Type == gjson.JSON {
				finalResponse = []byte(responseField.Raw)
				// 终端 response 可能只有 usage 而 output 为空，用前序 delta 还原输出。
				if len(gjson.GetBytes(finalResponse, "output").Array()) == 0 && responseAccumulator.HasContent() {
					if outputJSON, err := json.Marshal(responseAccumulator.BuildOutput()); err == nil {
						if patched, err := sjson.SetRawBytes(finalResponse, "output", outputJSON); err == nil {
							finalResponse = patched
						}
					}
				}
			}
		}

		if isTerminalEvent {
			upstreamTerminalEvent = terminalPolicy.TerminalEvent
			// 终止事件必须是当前 WS 消息中的最后一个 JSON 文档；尾随文档不再写给已完成的
			// 客户端请求，同时禁止复用语义不明确的上游连接。
			cleanExit = len(pendingJSONDocuments) == 0
			break
		}
	}

	if !reqStream {
		if len(finalResponse) == 0 {
			gatewayprovider.LogOpenAIWSModeInfo(
				"missing_final_response account_id=%d conn_id=%s events=%d token_events=%d terminal_events=%d wrote_downstream=%v",
				account.Record.ID,
				connID,
				eventCount,
				tokenEventCount,
				terminalEventCount,
				wroteDownstream,
			)
			if !wroteDownstream {
				if upstreamWarning != nil {
					// 非流式 terminal 事件可能只有顶层 error，没有 response 对象；错误回退时仍需保留原始风控信号。
					return nil, ws.WrapFallback("missing_final_response", &openAIWSUpstreamWarningError{
						warning: upstreamWarning,
						err:     errors.New("no terminal response payload"),
					})
				}
				return nil, ws.WrapFallback("missing_final_response", errors.New("no terminal response payload"))
			}
			return nil, errors.New("ws finished without final response")
		}

		if needModelReplace {
			finalResponse = protocolwire.ReplaceModelInResponseBody(finalResponse, mappedModel, originalModel)
		}
		finalResponse = s.Output.Corrector.CorrectResponseBody(finalResponse)
		protocolwire.PopulateUsageFromResponseJSON(finalResponse, usage)
		if responseID == "" {
			responseID = strings.TrimSpace(gjson.GetBytes(finalResponse, "id").String())
		}

		c.Data(http.StatusOK, "application/json", finalResponse)
	} else {
		flushStreamWriter(true)
	}

	if responseID != "" && stateStore != nil {
		ttl := s.OpenAIHTTPResponseStickyTTL()
		gatewayprovider.LogOpenAIWSBindResponseAccountWarn(groupID, account.Record.ID, responseID, stateStore.BindResponseAccount(ctx, groupID, responseID, account.Record.ID, ttl))
		stateStore.BindResponseConn(responseID, lease.ConnID(), ttl)
		s.bindOpenAIWSResponseSessionOwner(ctx, c, responseID)
	}
	if stateStore != nil && storeDisabled && sessionHash != "" {
		stateStore.BindSessionConn(groupID, sessionHash, lease.ConnID(), s.Selection.SessionStickyTTL())
	}
	firstTokenMsValue := -1
	if firstTokenMs != nil {
		firstTokenMsValue = *firstTokenMs
	}
	gatewayprovider.LogOpenAIWSModeDebug(
		"completed account_id=%d conn_id=%s response_id=%s stream=%v duration_ms=%d events=%d token_events=%d terminal_events=%d buffered_events=%d buffered_flushed=%d first_event=%s last_event=%s first_token_ms=%d wrote_downstream=%v client_disconnected=%v",
		account.Record.ID,
		connID, gatewayprovider.TruncateOpenAIWSLogValue(strings.TrimSpace(responseID), gatewayprovider.OpenAIWSIDValueMaxLen), reqStream,
		time.Since(startTime).Milliseconds(),
		eventCount,
		tokenEventCount,
		terminalEventCount,
		bufferedEventCount,
		flushedBufferedEventCount, gatewayprovider.TruncateOpenAIWSLogValue(firstEventType, gatewayprovider.OpenAIWSLogValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(lastEventType, gatewayprovider.OpenAIWSLogValueMaxLen), firstTokenMsValue,
		wroteDownstream,
		clientDisconnected,
	)

	return &forwardcore.OpenAIResult{
		RequestID:                   responseID,
		Usage:                       *usage,
		Model:                       originalModel,
		UpstreamModel:               mappedModel,
		UpstreamResponseServiceTier: responseModelObserver.ServiceTier(),
		ImageCount:                  imageCounter.Count(),
		ImageOutputSizes:            imageCounter.Sizes(),
		ServiceTier:                 ResolvedOpenAIUpstreamServiceTierFromObserver(responseModelObserver, requeststate.ExtractOpenAIServiceTier(reqBody)),
		ReasoningEffort:             gatewayprovider.ApplyThinkingEnabledFallback(requeststate.ExtractOpenAIReasoningEffort(reqBody, mappedModel, originalModel), payloadAsJSONBytes(payload), mappedModel),
		RequestedReasoningEffort:    requeststate.CanonicalRequestedReasoningEffortFromReqBody(reqBody, originalModel, mappedModel),
		Stream:                      reqStream,
		OpenAIWSMode:                true,
		UpstreamTerminalEvent:       upstreamTerminalEvent,
		ResponseHeaders:             lease.HandshakeHeaders(),
		Duration:                    time.Since(startTime),
		FirstTokenMs:                firstTokenMs,
		UpstreamWarning:             upstreamWarning,
	}, nil
}

// stripCodexSparkImageGenerationToolFromRawPayload 会在上游模型为 Spark 时，从原始
// /responses payload 中剥离生图声明。Spark 上游会以 param=tools 拒绝该工具，
// 而 Codex 客户端默认会携带它；返回值包含可能更新后的 payload、是否修改以及 JSON 错误。
func stripCodexSparkImageGenerationToolFromRawPayload(payload []byte, model string) ([]byte, bool, error) {
	if !gatewayprovider.IsCodexSparkModel(model) {
		return payload, false, nil
	}
	return gatewayprovider.StripOpenAIImageGenerationToolsFromRawPayload(payload)
}
