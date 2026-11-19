package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	openaiws "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws"
)

// openai_ws_ingress_execution_adapter.go selects and connects pooled,
// dedicated, or HTTP-bridged Responses execution.
// A selected Responses WS provider can run pooled turns, a dedicated relay, or
// an allowed HTTP bridge. Grok and an HTTP Responses route force bridging;
// oversized initial frames can choose it only when group conversion and
// upstream capability allow. Per-session providers otherwise receive a
// dedicated connection, while pooled WS requires the resolved v2 transport.
//
// Pooled execution shares one mutable ingress state across normalized turns
// and connection acquisitions. Handshake turn state is published to session
// storage and future acquisition headers after a successful lease. Header
// refresh for prewarming captures the stable identity service because turn
// settings replace the local executor reference. The gateway core owns pins,
// release and replay; typed acquisition failures preserve first-turn failover,
// forced-continuation loss and capacity-retry distinctions.

// executeWSIngressAdapter binds the upgraded client and selected provider to
// one sequential ingress session or dedicated relay. It rejects missing
// service, Gin context, client and provider before clearing tool state and
// preparing Codex identity. Bearer validation, TLS matching and optional
// global Fast settings precede session preemption and transport selection.
//
// Entry normally owns the preemption registration; direct calls can arm it
// here and run cleanup at return. A preempted context overrides the returned
// outcome with ErrSessionPreempted. The selected protocol determines forced
// HTTP bridging, and initial oversized dedicated requests require allowed HTTP
// conversion before transport changes.
//
// Pooled callbacks retain current affinity, prepare handshake headers, acquire
// under one turn budget, and classify failures before the core chooses
// recovery. Successful acquisition saves handshake state locally, requests its
// session-cache publication, and prepares future headers. Downstream writing
// uses the client's original lifetime
// when supplied by hooks, while admission, upstream reads and acquisition use
// control context. The core runs turns through terminal output and owns every
// retained pin and lease.
func (s *OpenAIWebSocketExecutor) executeWSIngressAdapter(
	ctx context.Context,
	c *gin.Context,
	clientConn *coderws.Conn,
	provider *gatewayprovider.ExecutionProvider,
	token string,
	firstClientMessage []byte,
	hooks *gatewayws.OpenAIIngressHooks,
) (returnErr error) {
	if s == nil {
		return errors.New("service is nil")
	}
	if c == nil {
		return errors.New("gin context is nil")
	}
	if clientConn == nil {
		return errors.New("client websocket is nil")
	}
	if provider == nil {
		return errors.New("provider is nil")
	}
	// A provider retry reuses Gin; clear its old reverse tool map before preparing
	// the new provider so response restoration cannot reuse another target's aliases.
	gatewayhttp.SetCodexToolNameReverse(c, nil)
	if _, err := gatewayhttp.PrepareCodexIdentity(ctx, c, s.Requests.Providers, provider); err != nil {
		return err
	}
	if err := validateOpenAIWSBearerToken(provider, token); err != nil {
		return err
	}

	tlsRouterMatch := s.Requests.MatchTLS(c, provider)

	// Bind a usable global Fast snapshot for the session before frame processing.
	// Failed or empty reads keep the existing context, leaving the shared evaluator
	// to resolve its own settings when no snapshot is bound.
	if s.Requests.Readers != nil {
		if settings, err := s.Requests.Readers.Gateway.GetOpenAIFastPolicySettings(ctx); err == nil && settings != nil {
			ctx = gatewayprovider.WithFastPolicyContext(ctx, settings)
		}
	}

	// Entry-owned registrations are reused across provider attempts; direct calls
	// obtain cleanup here, with the deferred cause check reporting confirmed session
	// replacement after transport cleanup has run.
	if preemptCtx, cleanupPreempt, armed := s.BeginOpenAIWSIngressSessionPreemption(ctx, c, provider, firstClientMessage); armed {
		ctx = preemptCtx
		defer cleanupPreempt()
		defer func() {
			if isOpenAIWSSessionPreempted(ctx) {
				returnErr = gatewayws.ErrSessionPreempted
			}
		}()
	}

	s = s.forTurn()
	var routeErr error
	provider, routeErr = gatewayprovider.ProviderForProtocolAttempt(ctx, provider)
	if routeErr != nil {
		return routeErr
	}
	wsDecision := s.Selection.ResolveTransport(provider)
	forceHTTPBridge := provider.Record.Platform == capability.PlatformGrok || provider.Route.Protocol() == protocol.ProtocolOpenAIResponses
	if !forceHTTPBridge && s.shouldBridgeOpenAIWSPassthroughFirstMessage(provider, firstClientMessage) && !responsesWSHTTPBridgeAllowed(c, provider) {
		return gatewayhttp.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "request exceeds the websocket size threshold and HTTP conversion is not allowed", nil)
	}
	ingressMode := provider.View().ResponsesWSConnectionMode()
	// Dedicated mode has one transport for the conversation; choose allowed
	// oversized HTTP conversion before dialing, otherwise bind its dedicated relay.
	if !forceHTTPBridge && ingressMode == providercore.ResponsesWSPerSession {
		if s.shouldBridgeOpenAIWSPassthroughFirstMessage(provider, firstClientMessage) && responsesWSHTTPBridgeAllowed(c, provider) {
			forceHTTPBridge = true
		} else {
			return s.proxyResponsesWebSocketV2Passthrough(ctx, c, clientConn, provider, token, firstClientMessage, hooks, wsDecision, tlsRouterMatch)
		}
	}
	if !forceHTTPBridge && wsDecision.Transport != egress.OpenAIUpstreamTransportResponsesWebsocketV2 {
		return fmt.Errorf("websocket upstream protocol is unavailable")
	}

	wsURL := ""
	wsHost := "-"
	wsPath := "-"
	if forceHTTPBridge {
		wsHost = "xai-http-bridge"
		wsPath = "/v1/responses"
	} else {
		var err error
		wsURL, err = s.buildOpenAIResponsesWSURL(provider)
		if err != nil {
			return fmt.Errorf("build ws url: %w", err)
		}
		// This parse supplies diagnostic labels after endpoint preparation; a failure
		// keeps placeholders while the prepared dial URL remains available for execution.
		if parsedURL, parseErr := url.Parse(wsURL); parseErr == nil && parsedURL != nil {
			wsHost = gatewayprovider.NormalizeOpenAIWSLogValue(parsedURL.Host)
			wsPath = gatewayprovider.NormalizeOpenAIWSLogValue(parsedURL.Path)
		}
	}
	debugEnabled := gatewayprovider.IsOpenAIWSModeDebugEnabled()
	isCodexCLI := openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator")) || (s.Options != nil && s.Requests.Options.ForceCLI)

	state := &gatewayws.IngressState{TurnState: strings.TrimSpace(c.GetHeader(openAIWSTurnStateHeader))}

	normalizer := &gatewayws.RequestNormalizer{State: state, Options: gatewayws.NormalizeOptions{ProviderID: provider.Record.ID, OAuth: provider.View().IsOpenAIOAuth(), ForceHTTPBridge: forceHTTPBridge}, Port: &wsRequestAdapter{wsPassthroughAdapter: &wsPassthroughAdapter{service: s, request: c, provider: provider, hooks: hooks}, client: clientConn, isCodex: isCodexCLI}}
	parseClientPayload := func(raw []byte, replace bool, turn int) (gatewayws.ClientPayload, error) {
		// Refresh acquisition and relay settings for this turn. The normalizer keeps
		// its initially bound service reference, including the blocked-frame write budget.
		s = s.forTurn()
		return normalizer.Normalize(ctx, raw, replace, turn)
	}

	writeClientMessage := func(message []byte) error {
		writeCtx, cancel := newOpenAIWSDownstreamWriteContext(ctx, hooks, s.openAIWSWriteTimeout())
		defer cancel()
		message = gatewayhttp.RestoreCodexToolNamesFromContext(c, message)
		return clientConn.Write(writeCtx, coderws.MessageText, message)
	}

	readClientMessage := func() ([]byte, error) {
		idleTimeout := s.openAIWSIngressInterTurnIdleTimeout()
		msgType, payload, readErr := gatewayhttp.ReadOpenAIWSClientMessage(
			ctx,
			clientConn,
			idleTimeout,
			coderws.StatusNormalClosure,
			"websocket idle timeout",
		)
		if readErr != nil {
			var closeErr *gatewayhttp.OpenAIWSClientCloseError
			if errors.As(readErr, &closeErr) && closeErr.StatusCode() == coderws.StatusNormalClosure {
				gatewayprovider.LogOpenAIWSModeInfo("ingress_ws_inter_turn_idle_timeout provider_id=%d timeout_seconds=%d", provider.Record.ID, int(idleTimeout.Seconds()))
			}
			return nil, readErr
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			return nil, gatewayhttp.NewOpenAIWSClientCloseError(
				coderws.StatusPolicyViolation,
				fmt.Sprintf("unsupported websocket client message type: %s", msgType.String()),
				nil,
			)
		}
		return payload, nil
	}

	groupID := gatewayhttp.OpenAIResponseGroupID(c)
	stateStore := s.State
	var baseAcquireReq openaiws.WSAcquireRequest
	var pool *openaiws.WSConnPool
	openPool := func(firstPayload gatewayws.ClientPayload) error {
		firstRoutingFields := gjson.GetManyBytes(firstPayload.PayloadRaw, "model", "service_tier")
		wsHeaders, _, buildHdrErr := s.buildOpenAIWSHeaders(
			ctx,
			c,
			provider,
			token,
			wsDecision,
			isCodexCLI,
			state.TurnState,
			strings.TrimSpace(c.GetHeader(openai.WSTurnMetadataHeader)),
			firstPayload.PromptCacheKey,
			firstRoutingFields[0].String(),
			firstRoutingFields[1].String(),
			tlsRouterMatch,
		)
		if buildHdrErr != nil {
			return fmt.Errorf("build ws headers: %w", buildHdrErr)
		}
		tlsProfile, tlsProfileKey := s.Requests.WSTLSProfile(provider, tlsRouterMatch)
		// Pool prewarming can refresh headers after a later turn replaces s. Capture
		// the shared identity service, avoiding reads of that changing executor reference.
		identity := s.Requests.Identity
		baseAcquireReq = openaiws.WSAcquireRequest{
			Provider: openAIWSPoolProviderView(provider),
			WSURL:    wsURL,
			Headers:  wsHeaders,
			HeadersFactory: func(factoryCtx context.Context, headers http.Header) (http.Header, error) {
				return identity.RefreshHeaders(factoryCtx, provider, headers)
			},
			TLSProfile:    tlsProfile,
			TLSProfileKey: tlsProfileKey,
			ProxyURL: func() string {
				if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
					return provider.Record.Proxy.URL()
				}
				return ""
			}(),
			ForceNewConn: false,
		}
		pool = s.Connections.Pool()
		if pool == nil {
			return errors.New("openai ws conn pool is nil")
		}
		gatewayprovider.LogOpenAIWSModeInfo(
			"ingress_ws_protocol_confirm provider_id=%d provider_type=%s transport=%s ws_host=%s ws_path=%s ws_mode=%s store_disabled=%v has_session_hash=%v has_previous_response_id=%v",
			provider.Record.ID,
			provider.Record.Type, gatewayprovider.NormalizeOpenAIWSLogValue(string(wsDecision.Transport)), wsHost,
			wsPath, gatewayprovider.NormalizeOpenAIWSLogValue(ingressMode), state.StoreDisabled,
			state.SessionHash != "",
			firstPayload.PreviousResponseID != "",
		)

		if debugEnabled {
			gatewayprovider.LogOpenAIWSModeDebug(
				"ingress_ws_start provider_id=%d provider_type=%s transport=%s ws_host=%s preferred_conn_id=%s has_session_hash=%v has_previous_response_id=%v store_disabled=%v",
				provider.Record.ID,
				provider.Record.Type, gatewayprovider.NormalizeOpenAIWSLogValue(string(wsDecision.Transport)), wsHost, gatewayprovider.TruncateOpenAIWSLogValue(state.PreferredConnID, gatewayprovider.OpenAIWSIDValueMaxLen), state.SessionHash != "",
				firstPayload.PreviousResponseID != "",
				state.StoreDisabled,
			)
		}
		if firstPayload.PreviousResponseID != "" {
			firstPreviousResponseIDKind := protocolopenai.ClassifyOpenAIPreviousResponseIDKind(firstPayload.PreviousResponseID)
			gatewayprovider.LogOpenAIWSModeInfo(
				"ingress_ws_continuation_probe provider_id=%d turn=%d previous_response_id=%s previous_response_id_kind=%s preferred_conn_id=%s session_hash=%s header_session_id=%s header_conversation_id=%s has_turn_state=%v turn_state_len=%d has_prompt_cache_key=%v store_disabled=%v",
				provider.Record.ID,
				1, gatewayprovider.TruncateOpenAIWSLogValue(firstPayload.PreviousResponseID, gatewayprovider.OpenAIWSIDValueMaxLen), gatewayprovider.NormalizeOpenAIWSLogValue(firstPreviousResponseIDKind), gatewayprovider.TruncateOpenAIWSLogValue(state.PreferredConnID, gatewayprovider.OpenAIWSIDValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(state.SessionHash, 12), gatewayprovider.OpenAIWSHeaderValueForLog(baseAcquireReq.Headers, "session_id"), gatewayprovider.OpenAIWSHeaderValueForLog(baseAcquireReq.Headers, "conversation_id"), state.TurnState != "",
				len(state.TurnState),
				firstPayload.PromptCacheKey != "",
				state.StoreDisabled,
			)
		}

		return nil
	}

	acquireTurnLease := func(turn int, preferred string, forcePreferredConn bool, allowRecovery bool) (*openaiws.WSConnLease, error) {
		req := openaiws.CloneWSAcquireRequest(baseAcquireReq)
		req.PreferredConnID = strings.TrimSpace(preferred)
		req.ForcePreferredConn = forcePreferredConn
		req.ForceNewConn = false
		// Freeze one deadline across reuse, queue wakeups and dialing for this lease;
		// later turns can take a newer Options snapshot before starting another acquire.
		acquireTimeout := s.openAIWSAcquireTimeout()
		acquireCtx, acquireCancel := context.WithTimeout(ctx, acquireTimeout)
		lease, acquireErr := pool.Acquire(acquireCtx, req)
		acquireCancel()
		var dialErr *openai.WSDialError
		// Return a distinct task-invalid marker before ordinary dial policy so the
		// core can use its single identity repair and retry this acquisition.
		if acquireErr != nil && s.Requests.Identity.UsesAgentIdentity(ctx, provider) && errors.As(acquireErr, &dialErr) && openai.IsAgentTaskInvalidWSDialError(dialErr) && allowRecovery {
			return nil, &gatewayws.AcquireRecoveryError{Err: acquireErr}
		}
		if acquireErr != nil {
			canonicalModel := gatewayprovider.ExecutionModelPolicy(provider).CanonicalSchedulingModel(state.OriginalModel)
			errorDecision := s.handleOpenAIWSDialTransientFailure(ctx, provider, canonicalModel, acquireErr)
			dialStatus, dialClass, dialCloseStatus, dialCloseReason, dialRespServer, dialRespVia, dialRespCFRay, dialRespReqID := gatewayprovider.SummarizeOpenAIWSDialError(acquireErr)
			gatewayprovider.LogOpenAIWSModeInfo(
				"ingress_ws_upstream_acquire_fail provider_id=%d turn=%d reason=%s dial_status=%d dial_class=%s dial_close_status=%s dial_close_reason=%s dial_resp_server=%s dial_resp_via=%s dial_resp_cf_ray=%s dial_resp_x_request_id=%s cause=%s preferred_conn_id=%s force_preferred_conn=%v ws_host=%s ws_path=%s proxy_enabled=%v",
				provider.Record.ID,
				turn, gatewayprovider.NormalizeOpenAIWSLogValue(openai.ClassifyWSAcquireError(acquireErr)), dialStatus,
				dialClass,
				dialCloseStatus, gatewayprovider.TruncateOpenAIWSLogValue(dialCloseReason, gatewayprovider.OpenAIWSHeaderValueMaxLen), dialRespServer,
				dialRespVia,
				dialRespCFRay,
				dialRespReqID, gatewayprovider.TruncateOpenAIWSLogValue(acquireErr.Error(), gatewayprovider.OpenAIWSLogValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(preferred, gatewayprovider.OpenAIWSIDValueMaxLen), forcePreferredConn,
				wsHost,
				wsPath,
				provider.Record.ProxyID != nil && provider.Record.Proxy != nil,
			)
			var dialErr *openai.WSDialError
			if errors.As(acquireErr, &dialErr) && dialErr != nil && dialErr.StatusCode != 0 {
				if turn == 1 && errorDecision.ShouldReturnGenericError() {
					return nil, openAIWSGenericPolicyCloseError(dialErr.StatusCode)
				}
				if turn == 1 && errorDecision.ShouldFailoverWithDefaults(
					gatewayprovider.ExecutionErrorPolicy(provider),
					dialErr.StatusCode,
					dialErr.StatusCode == http.StatusTooManyRequests,
					s.shouldFailoverOpenAIWSError(provider, dialErr.StatusCode, dialErr.ResponseBody),
				) {
					return nil, gatewayprovider.NewOpenAIUpstreamFailure(
						dialErr.StatusCode,
						dialErr.ResponseHeaders,
						dialErr.ResponseBody,
						upstream.ExtractErrorMessage(dialErr.ResponseBody),
						errorDecision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), dialErr.StatusCode),
					)
				}
			}
			if errors.Is(acquireErr, openai.ErrOpenAIWSPreferredConnUnavailable) {
				return nil, gatewayhttp.NewOpenAIWSClientCloseError(
					coderws.StatusPolicyViolation,
					"upstream continuation connection is unavailable; please restart the conversation",
					acquireErr,
				)
			}
			if errors.Is(acquireErr, context.DeadlineExceeded) || errors.Is(acquireErr, openai.ErrOpenAIWSConnQueueFull) {
				return nil, gatewayhttp.NewOpenAIWSClientCloseError(
					coderws.StatusTryAgainLater,
					"upstream websocket is busy, please retry later",
					acquireErr,
				)
			}
			return nil, acquireErr
		}
		connID := strings.TrimSpace(lease.ConnID())
		// Handshake state belongs to the newly acquired connection. Publish it to
		// session affinity and cloned future headers before any subsequent acquisition.
		if handshakeTurnState := strings.TrimSpace(lease.HandshakeHeader(openAIWSTurnStateHeader)); handshakeTurnState != "" {
			state.TurnState = handshakeTurnState
			if stateStore != nil && state.SessionHash != "" {
				stateStore.BindSessionTurnState(groupID, state.SessionHash, handshakeTurnState, s.Selection.SessionStickyTTL())
			}
			updatedHeaders := upstream.CloneHeader(baseAcquireReq.Headers)
			if updatedHeaders == nil {
				updatedHeaders = make(http.Header)
			}
			updatedHeaders.Set(openAIWSTurnStateHeader, handshakeTurnState)
			baseAcquireReq.Headers = updatedHeaders
		}
		gatewayprovider.LogOpenAIWSModeInfo(
			"ingress_ws_upstream_connected provider_id=%d turn=%d conn_id=%s conn_reused=%v conn_pick_ms=%d queue_wait_ms=%d preferred_conn_id=%s",
			provider.Record.ID,
			turn, gatewayprovider.TruncateOpenAIWSLogValue(connID, gatewayprovider.OpenAIWSIDValueMaxLen), lease.Reused(),
			lease.ConnPickDuration().Milliseconds(),
			lease.QueueWaitDuration().Milliseconds(), gatewayprovider.TruncateOpenAIWSLogValue(preferred, gatewayprovider.OpenAIWSIDValueMaxLen),
		)
		return lease, nil
	}

	port := &wsIngressAdapter{
		ParseFn: parseClientPayload, ReadFn: readClientMessage,
		GenerateHashFn:  func(body []byte) string { return gatewayhttp.GenerateOpenAISessionHash(c, body) },
		StoreDisabledFn: func(body []byte) bool { return s.isOpenAIWSStoreDisabledInRequestRaw(body, provider) },
		ShouldBridgeFn: func(payload gatewayws.ClientPayload) bool {
			return forceHTTPBridge || (responsesWSHTTPBridgeAllowed(c, provider) && s.shouldBridgeOpenAIWSHTTP(provider, payload.PayloadBytes, payload.PreviousResponseID))
		},
		InvalidFn: s.Lineage.Digests, StripFn: s.Lineage.Strip,
		BridgeIdentityFn: func(body []byte, model string, turn int) (string, error) {
			turnContext := ctx
			if hooks != nil && hooks.ContextForTurn != nil {
				turnContext = hooks.ContextForTurn(turn)
			}
			return s.resolveGrokWSCacheIdentity(turnContext, c, provider, body, model)
		},
		BridgeFn: func(ctx context.Context, input gatewayws.ClientPayload, body []byte, identity string, turn int) (*gatewayws.ForwardResult, error) {
			if hooks != nil && hooks.ContextForTurn != nil {
				ctx = hooks.ContextForTurn(turn)
			}
			result, err := s.proxyOpenAIWSHTTPBridgeTurn(ctx, c, wsHTTPBridgeTurnInput{
				Provider:          provider,
				Token:             token,
				Payload:           body,
				PayloadBytes:      len(body),
				OriginalModel:     input.OriginalModel,
				RoutingModel:      input.RoutingModel,
				ImageBillingModel: input.ImageBillingModel,
				ImageSizeTier:     input.ImageSizeTier,
				ImageInputSize:    input.ImageInputSize,
				GrokCacheIdentity: identity,
				Turn:              turn,
				WriteClient:       writeClientMessage,
				RouterMatch:       []egress.TLSFingerprintRouterMatchResult{tlsRouterMatch},
			})
			return gatewayprovider.ProjectWSResult(result), err
		},
		SetStateFn: func(turnState, sessionHash string) {
			if turnState != "" && c != nil && c.Request != nil {
				c.Request.Header.Set(openAIWSTurnStateHeader, turnState)
			}
			if c != nil && sessionHash != "" {
				c.Set(gatewayhttp.OpenAIWSIngressLineageContextKey, sessionHash)
			}
		},
		OpenPoolFn: openPool,
		RecoverAcquireFn: func(ctx context.Context) error {
			return s.Requests.Identity.Recover(ctx, provider, provider.View().GetCredential("task_id"))
		},
		AcquireFn: func(turn int, preferred string, force bool, allowRecovery bool) (gatewayws.ConnLease, error) {
			lease, err := acquireTurnLease(turn, preferred, force, allowRecovery)
			if err != nil || lease == nil {
				return nil, err
			}
			return &wsIngressLease{lease}, nil
		},
		RelayFn: func(turn int, lease gatewayws.ConnLease, input gatewayws.ClientPayload) (*gatewayws.ForwardResult, error) {
			streamPort := &wsStreamAdapter{wsPassthroughAdapter: &wsPassthroughAdapter{service: s, request: c, provider: provider, hooks: hooks}, state: state, groupID: groupID, writeClient: writeClientMessage}
			var streamHooks *gatewayws.StreamHooks
			if hooks != nil {
				streamHooks = &gatewayws.StreamHooks{OnUpstreamError: hooks.OnUpstreamError}
			}
			// AcquireFn returns this transport wrapper; require that type before exposing
			// the underlying pool lease to RelayTurn, so unrelated leases cannot be misused.
			typedLease, ok := lease.(*wsIngressLease)
			if !ok {
				panic("unexpected websocket ingress lease type")
			}
			return gatewayws.RelayTurn(ctx, streamPort, typedLease, input, turn, gatewayws.StreamOptions{ProviderID: provider.Record.ID, WriteTimeout: s.openAIWSWriteTimeout(), ReadTimeout: s.openAIWSReadTimeout(), PreviousRecovery: s.openAIWSIngressPreviousResponseRecoveryEnabled(), Debug: debugEnabled}, streamHooks)
		},

		PinFn:       func(id int64, conn string) bool { return pool.PinConn(id, conn) },
		UnpinFn:     func(id int64, conn string) { pool.UnpinConn(id, conn) },
		HeaderFn:    func(key string) string { return gatewayprovider.OpenAIWSHeaderValueForLog(baseAcquireReq.Headers, key) },
		BindOwnerFn: func(ctx context.Context, responseID string) { s.bindOpenAIWSResponseSessionOwner(ctx, c, responseID) },
		UpdateHeadersFn: func(nextPayload gatewayws.ClientPayload, turnState string) {
			nextRoutingFields := gjson.GetManyBytes(nextPayload.PayloadRaw, "model", "service_tier")
			if nextPayload.PromptCacheKey != "" {
				updatedHeaders, _, err := s.buildOpenAIWSHeaders(ctx, c, provider, token, wsDecision, isCodexCLI, turnState, strings.TrimSpace(c.GetHeader(openai.WSTurnMetadataHeader)), nextPayload.PromptCacheKey, nextRoutingFields[0].String(), nextRoutingFields[1].String(), tlsRouterMatch)
				// Keep prior handshake headers if identity rebuilding fails, while the
				// routing hint below still follows this accepted frame's model and tier.
				if err != nil {
					gatewayprovider.LogOpenAIWSModeInfo("ingress_ws_update_headers_failed provider_id=%d err=%v", provider.Record.ID, err)
				} else {
					baseAcquireReq.Headers = updatedHeaders
				}
			}
			gatewayhttp.SetOpenAICodexRoutingHint(baseAcquireReq.Headers, provider, nextRoutingFields[0].String(), nextRoutingFields[1].String())
		},
	}
	runtime := gatewayws.IngressSession{State: state, Store: stateStore, Codec: wsReplayCodec{}, Port: port, Hooks: wsIngressHooks(hooks), Refresh: func() gatewayws.Parameters {
		if s.Options != nil {
			return *s.Options
		}
		return gatewayws.DefaultParameters()
	}, Options: gatewayws.IngressOptions{
		ProviderID: provider.Record.ID, ProviderType: provider.Record.Type, Platform: provider.Record.Platform, GroupID: groupID,
		Debug: debugEnabled, BridgeThreshold: s.openAIWSHTTPBridgeThresholdBytes(), PreviousRecovery: s.openAIWSIngressPreviousResponseRecoveryEnabled(), StoreDisabledMode: s.openAIWSStoreDisabledConnMode(),
		PreflightPingIdle: openAIWSIngressPreflightPingIdle, HealthCheckTimeout: openaiws.WSConnHealthCheckTimeout, ResponseStickyTTL: s.OpenAIHTTPResponseStickyTTL(), SessionStickyTTL: s.Selection.SessionStickyTTL(),
	}}
	return runtime.Run(ctx, firstClientMessage)
}

// responsesWSHTTPBridgeAllowed reports an HTTP Responses conversion permitted
// by the current group and provider. Missing Key, group or provider returns
// false. It removes the WS protocol from a copied enabled list before calling
// ResolveRoute, so native WS support cannot satisfy a query about HTTP
// conversion.
//
// The group fallback map and remaining provider protocols select the target
// together, and only OpenAIResponses returns true. Cloning the list before
// deletion leaves the provider snapshot stable for other request and route
// checks. The result gates size-based conversion even when a WS endpoint
// exists.
func responsesWSHTTPBridgeAllowed(c *gin.Context, provider *gatewayprovider.ExecutionProvider) bool {
	key := gatewayhttp.GetExecutionAPIKey(c)
	if key == nil || key.Group == nil || provider == nil {
		return false
	}
	value := provider.View().RoutingSnapshot().Protocols()
	// Query conversion using a copied protocol list; retaining the native WS
	// entry here would select WS before the group's HTTP fallback is considered.
	value.Enabled = slices.DeleteFunc(slices.Clone(value.Enabled), func(id capability.ProtocolID) bool { return id == capability.ProtocolResponsesWebSocket })
	var fallbacks map[capability.ProtocolID][]capability.ProtocolID
	if key.Group != nil {
		fallbacks = key.Group.ProtocolFallbacks
	}
	target, ok := capability.ResolveRoute(value, capability.ProtocolResponsesWebSocket, fallbacks)
	return ok && target == capability.ProtocolOpenAIResponses
}

// activeCodexFingerprintMode returns the configured fingerprint mode only when
// a seed exists. Nil provider, off configuration, or absent seed returns off;
// this value becomes part of the provider view passed to pool compatibility
// checks.
func activeCodexFingerprintMode(provider *gatewayprovider.ExecutionProvider) providercore.CodexFingerprintMode {
	if provider == nil || gatewayprovider.ExecutionProtocolRecord(provider).GetCodexFingerprintMode() == providercore.CodexFingerprintOff {
		return providercore.CodexFingerprintOff
	}
	if _, ok := providercore.CodexFingerprintSeed(provider.Record.Extra); !ok {
		return providercore.CodexFingerprintOff
	}
	return gatewayprovider.ExecutionProtocolRecord(provider).GetCodexFingerprintMode()
}

// openAIWSPoolProviderView returns the selected provider's identity,
// concurrency and active fingerprint mode for pool operations. Nil provider
// returns nil; the returned scalar view is independent of the full execution
// record and contains no credentials.
func openAIWSPoolProviderView(provider *gatewayprovider.ExecutionProvider) *openaiws.WSPoolProvider {
	if provider == nil {
		return nil
	}
	return &openaiws.WSPoolProvider{ID: provider.Record.ID, Concurrency: provider.Record.Concurrency, Type: provider.Record.Type, FingerprintMode: string(activeCodexFingerprintMode(provider))}
}
