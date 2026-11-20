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

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// passthrough.go runs dedicated WebSocket sessions and coordinates turn
// admission with terminal delivery.
// A dedicated WebSocket relays client frames and upstream events concurrently.
// Completion and delivery consume request metadata at different points when a
// bare error waits for later settlement evidence. PassthroughSession queues
// copied turn bodies and policy values, then retains a dequeued snapshot for
// response-terminal rewriting while paired write callbacks govern later
// admission. Immediate terminal settlement can prepare that snapshot before
// its write; a bare error may already have been delivered.
//
// Admission and terminal delivery share TurnLifecycle. The client filter
// reserves response.create before validation and cancels that reservation on
// preforward rejection; a successful terminal write publishes idle state and
// releases admission. Request timing and per-turn read/write parameters are
// captured for accepted turns, while session.update separately changes the
// omitted-model default. A provider handshake gets one eligible identity
// recovery attempt. Upstream first-output and active-read timeouts are managed
// by DeadlineConn; first-turn pre-output timeout can fail over, but later
// timeout closes for reconnection because entry's ordinary replay bytes belong
// to the initial request.

// Run filters and relays a dedicated provider connection while capturing
// accepted-turn completion metadata. Callers supply initialized Port, a usable
// client socket and context, and an embedded IngressHooks pointer whenever
// Hooks is present.
//
// The first request is normalized and written through DeadlineConn before
// bidirectional relay starts. Later response.create reservations reject
// overlap, apply current model and policy rules, acquire turn admission, and
// publish copied snapshots before upstream forwarding. Immediate terminal
// settlement dequeues metadata before its client write; a bare error can
// already have been sent when later evidence settles its turn. The saved
// snapshot supports response rewriting, while paired lifecycle writes govern
// admission after a response terminal. Runtime read and write budgets refresh
// on accepted turns; client idle budget refreshes on
// each wait. Non-control relay failures report a still-queued turn; parent
// control causes return before that completion path. Initial pre-output timeout
// can replace the provider, while later timeout requires reconnection.
func (s *PassthroughSession) Run(ctx context.Context, clientConn ClientSocket, firstClientMessage []byte) error {
	p, o, hooks := s.Port, s.Options, s.Hooks
	var turnParameters atomic.Pointer[Parameters]
	initial := DefaultParameters()
	// Truncate static durations to whole-second fallback parameters, dropping
	// sub-second remainders before accepted-turn budget readers consume them.
	initial.ReadTimeoutSeconds = int(o.IdleTimeout / time.Second)
	initial.WriteTimeoutSeconds = int(o.WriteTimeout / time.Second)
	if s.Parameters != nil {
		initial = s.Parameters()
	}
	turnParameters.Store(&initial)
	currentWriteTimeout := func() time.Duration { return time.Duration(turnParameters.Load().WriteTimeoutSeconds) * time.Second }
	currentIdleTimeout := func() time.Duration {
		if s.Parameters != nil {
			return time.Duration(s.Parameters().IngressInterTurnIdleTimeoutSeconds) * time.Second
		}
		return o.InterTurnIdleTimeout
	}

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
	next, policyErr := p.Reasoning(firstClientMessage, firstRequestModel)
	if policyErr != nil {
		return p.CloseError(1008, policyErr.Error(), policyErr)
	}
	firstClientMessage = next
	requestModel := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "model").String())
	requestPreviousResponseID := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "previous_response_id").String())
	initialRequestModel := ""
	if hooks != nil {
		initialRequestModel = hooks.InitialRequestModel
	}
	// Initialize the shared omitted-model default from entry or first-frame
	// metadata, then capture requested effort before reasoning and provider-model
	// rewrites affect accounting inputs.
	usageMeta := NewUsageMeta(initialRequestModel, firstClientMessage, p)
	usageMeta.CaptureRequestedReasoningEffort(originalFirstClientMessage)
	p.Log(fmt.Sprintf(
		"relay_start provider_id=%d model=%s previous_response_id=%s first_message_type=%s first_message_bytes=%d",
		o.ProviderID,
		p.Truncate(requestModel, 160),
		p.Truncate(requestPreviousResponseID, 64),
		"text",
		len(firstClientMessage),
	))

	// Resolve first-turn routing and provider models before Fast policy, so
	// administrator model rules see the actual upstream identifier. Later requests
	// inherit UsageMeta's client default, and session.update can replace that
	// default before resolving new routing and provider forms.
	firstRoutingModel, firstUpstreamModel, resolveModelErr := p.Models(1, requestModel, firstClientMessage)
	if resolveModelErr != nil {
		return resolveModelErr
	}
	firstClientMessage, resolveModelErr = p.ModelAttributes(ctx, 1, firstUpstreamModel, firstClientMessage)
	if resolveModelErr != nil {
		return resolveModelErr
	}
	// Opposite relay directions read these model defaults concurrently. Publish
	// each pointer atomically, while TurnPayloadQueue captures the combined values
	// for a specific accepted request.
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
	// Compatibility can remove parallel_tool_calls when tools are absent. Reapply
	// marked Lite requirements afterward so this request still sends its required
	// false value.
	if firstMessageResponsesLite {
		liteFirstMessage, liteErr := p.NormalizeLite(firstClientMessage)
		if liteErr != nil {
			return fmt.Errorf("normalize first websocket Lite payload: %w", liteErr)
		}
		firstClientMessage = liteFirstMessage
	}
	// Scope first-request identity after compatibility processing, so handshake
	// preparation receives this selected provider's namespace for session reuse.
	providerScopedFirst, providerScoped, scopeErr := p.ScopeIdentity(firstClientMessage)
	if scopeErr != nil {
		return p.CloseError(1008, "invalid websocket identity metadata", scopeErr)
	}
	if providerScoped {
		firstClientMessage = providerScopedFirst
	}
	firstPolicyCtx := ctx
	updatedFirst, blocked, policyErr := p.FastPolicy(firstPolicyCtx, 1, firstUpstreamModel, firstClientMessage, true)
	if policyErr != nil {
		return fmt.Errorf("apply openai fast policy on first ws frame: %w", policyErr)
	}
	if blocked != nil {
		p.PolicyDenied()
		// Attempt the policy event before returning the close instruction.
		// A successful synchronous Write orders the error event before the close frame.
		eventBytes := p.BlockedEvent(blocked)
		if eventBytes != nil {
			writeCtx, cancelWrite := context.WithTimeout(ctx, o.WriteTimeout)
			_ = clientConn.Write(writeCtx, TextFrame, eventBytes)
			cancelWrite()
		}
		return p.CloseError(1008, blocked.Message, blocked)
	}
	firstClientMessage = updatedFirst

	// Capture accepted service tier and recorded effort after policy filtering;
	// field removal becomes nil metadata rather than an earlier turn's value.
	// Queue snapshots retain each turn's choices, while the terminal response tier
	// remains separate for completion-time billing.
	usageMeta.InitFromFirstFrame(firstClientMessage)
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
	// Give an eligible handshake failure one provider-identity repair opportunity,
	// consuming that opportunity before repair so another failed handshake cannot
	// reopen the same recovery loop.
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
	// Close the dedicated upstream transport on every later exit. Cleanup is best
	// effort and its error does not replace the selected relay or policy result.
	defer func() { _ = upstreamFrameConn.Close() }()
	handshakeHeaders := dialResult.Headers

	relayUpstreamFrameConn := NewDeadlineConn(
		upstreamFrameConn, o.IdleTimeout, func(payload []byte) Deadline {
			reasoningEffort := ""
			if current := usageMeta.ReasoningEffort.Load(); current != nil {
				reasoningEffort = *current
			}
			timeout := p.FirstOutputTimeout(reasoningEffort)
			// Use this accepted turn's read budget when effort-specific first-output
			// timing is disabled, retaining the separate active-read budget for after
			// qualifying output.
			if timeout <= 0 {
				timeout = time.Duration(turnParameters.Load().ReadTimeoutSeconds) * time.Second
			}
			model := RequestModelForFrame(payload)
			if model == "" {
				model = usageMeta.RequestModelForFrame(payload)
			}
			if model == "" {
				model = requestModel
			}
			return Deadline{
				Timeout:           timeout,
				ActiveReadTimeout: time.Duration(turnParameters.Load().ReadTimeoutSeconds) * time.Second,
				StartedAt:         time.Now(),
				RequestModel:      model,
				ReasoningEffort:   reasoningEffort,
			}
		}, p.ClosedError())

	completedTurns := atomic.Int32{}
	var acceptedTurnStartedAt atomic.Pointer[time.Time]

	turnLifecycle := NewTurnLifecycle(true)
	clientFrameConn := &clientFrameConn{
		closeError: p.CloseError, closedError: p.ClosedError(), normalizeCompleted: p.NormalizeCompleted,
		conn:                 clientConn,
		controlCtx:           ctx,
		interTurnIdleTimeout: o.InterTurnIdleTimeout,
		idleTimeout:          currentIdleTimeout,
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
		// The client-to-upstream goroutine owns this filter.
		// Upstream callbacks also read session models through the atomic snapshots.
		filter: func(msgType int, payload []byte) (out []byte, blocked *PolicyBlocked, filterErr error) {
			if msgType != TextFrame && msgType != 2 {
				return payload, nil, nil
			}
			if err := wirejson.ValidateRequestObject(payload); err != nil {
				return payload, nil, p.CloseError(1008, "invalid websocket request payload", err)
			}
			// Apply user replacements before later-frame policy checks, so moderation
			// and provider normalization inspect the content selected for forwarding.
			payload = p.PromptReplace(ctx, payload)
			eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
			isResponseCreate := eventType == "response.create"
			responseCreateAt := time.Time{}
			if isResponseCreate {
				responseCreateAt = time.Now()
			}
			acceptedTurn := false
			if isResponseCreate {
				// Reserve admission before validating this frame, preventing another
				// response.create from entering while preforward checks run; rejection
				// releases only that provisional reservation.
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
					// Restore Lite-required fields after per-turn provider compatibility,
					// because its field removal can otherwise erase parameters required by
					// this marked request.
					litePayload, liteErr := p.NormalizeLite(payload)
					if liteErr != nil {
						return payload, nil, p.CloseError(1008, liteErr.Error(), liteErr)
					}
					payload = litePayload
				}
			}
			// Scope response and session identity with this provider's namespace so
			// shared client identity does not select another provider's upstream state.
			if isResponseCreate || eventType == "session.update" {
				providerScopedPayload, providerScoped, scopeErr := p.ScopeIdentity(payload)
				if scopeErr != nil {
					return payload, nil, p.CloseError(1008, "invalid websocket identity metadata", scopeErr)
				}
				if providerScoped {
					payload = providerScopedPayload
				}
			}
			// Retain requested effort before applying group reasoning rules on this
			// frame, keeping completion's client intent separate from its accepted
			// recorded effort.
			originalResponseCreate := payload
			if isResponseCreate {
				requestModelForPolicy := usageMeta.RequestModelForFrame(payload)
				next, policyErr := p.Reasoning(payload, requestModelForPolicy)
				if policyErr != nil {
					return payload, nil, p.CloseError(1008, policyErr.Error(), policyErr)
				}
				payload = next
			}
			if isResponseCreate {
				usageMeta.CaptureRequestedReasoningEffort(originalResponseCreate)
			}
			// The first request is already accepted. Later request numbering starts at
			// two even when no completion callback has advanced the shared counter.
			turnNo := max(int(completedTurns.Load())+1, 2)
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

			// Publish a session.update default before mapping and later policy checks;
			// rejection does not roll back that atomic value. Each response.create uses
			// its explicit model or this default before provider-model serialization.
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
			if policyErr == nil && blocked == nil && isResponseCreate {
				out, policyErr = p.ModelAttributes(ctx, turnNo, model, out)
			}

			// Publish accepted tier and recorded effort only after policy and admission
			// succeed. Rejected frames retain those two fields, while requested effort
			// was captured earlier and can already reflect this rejected request.
			if policyErr == nil && blocked == nil && isResponseCreate {
				if hooks != nil && hooks.BeforeTurn != nil {
					if err := hooks.BeforeTurn(turnNo); err != nil {
						return payload, nil, err
					}
				}
				usageMeta.UpdateFromResponseCreate(out)
				// Capture one parameter value after later-turn admission succeeds, so relay
				// write selection and upstream read timing use this accepted turn's
				// effective budgets.
				if s.Parameters != nil {
					next := s.Parameters()
					turnParameters.Store(&next)
				}
				// Queue accepted bytes and optional accounting values before the relay
				// forwards the request, allowing upstream completion to consume an
				// immutable snapshot for that same turn.
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
				// Strip wire effort only on the outbound copy after usageMeta and
				// turnPayloads have recorded the intact effective effort for accounting.
				out = stripOutboundEffort(ctx, p, routingModel, out)
			}
			return out, blocked, policyErr
		},
		// Select this filter's model rewrite from queued turn metadata. The inner
		// clientFrameConn subsequently applies its separate session-default model
		// restoration, while completion retains the queue's upstream identity.
		writeFilter: func(msgType int, payload []byte) ([]byte, error) {
			if msgType != TextFrame {
				return payload, nil
			}
			eventType := p.EventType(payload)
			turnPayload := turnPayloads.Peek()
			if !p.MayContainModel(eventType) {
				return payload, nil
			}
			// Missing queued identity falls back to independently loaded session
			// defaults. Those atomic fields can reflect separate publications; captured
			// turn metadata supplies the preferred request-specific identities.
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
			// Attempt the policy event before returning its close instruction, ignoring
			// write failure because the local block still requires the request to end.
			eventBytes := p.BlockedEvent(blocked)
			if eventBytes == nil {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, o.WriteTimeout)
			_ = clientConn.Write(writeCtx, TextFrame, eventBytes)
			cancel()
		},
	}
	// Write the first request through DeadlineConn before starting relay, arming
	// first-output timing and setting FirstMessageSent so the relay does not send
	// that request again.
	beginPerformance := func(turn int, started time.Time) *timing.Performance {
		if hooks != nil && hooks.BeginPerformance != nil {
			return hooks.BeginPerformance(turn, started)
		}
		return timing.NewPerformanceAt(timing.ModeFromContext(ctx), started, started)
	}
	firstPerformance := beginPerformance(1, time.Now())
	upstreamFirstMessageSent := false
	firstWriteCtx, cancelFirstWrite := context.WithTimeout(ctx, o.WriteTimeout)
	outboundFirstMessage := stripOutboundEffort(ctx, p, firstRoutingModel, firstClientMessage)
	firstWriteErr := relayUpstreamFrameConn.WriteFrame(firstWriteCtx, TextFrame, outboundFirstMessage)
	cancelFirstWrite()
	if firstWriteErr != nil {
		return WrapIngressTurnError(
			"write_upstream",
			fmt.Errorf("write first upstream websocket request: %w", firstWriteErr),
			false,
		)
	}
	upstreamFirstMessageSent = true

	// Forward auxiliary client events while waiting for the next response.create.
	// Only an accepted request returns to the relay's per-turn timing and request
	// write path.
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
			BeginPerformance: func(turn int, started time.Time) *timing.Performance {
				if turn == 1 {
					return firstPerformance
				}
				return beginPerformance(turn, started)
			},
			WriteTimeout:        o.WriteTimeout,
			WriteTimeoutForTurn: currentWriteTimeout,
			FirstTurnStartedAt:  firstTurnStartedAt,
			TakeNextTurnStartedAt: func() time.Time {
				startedAt := acceptedTurnStartedAt.Swap(nil)
				if startedAt == nil {
					return time.Time{}
				}
				return *startedAt
			},
			// clientFrameConn measures idle time after each completed turn.
			// A relay-wide watchdog could terminate an upstream turn still processing normally.
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
					turnNo := max(int(completedTurns.Load())+1, 1)
					turnPayload := turnPayloads.Peek()
					hooks.OnUpstreamError(turnNo, turnPayload.OriginalModel, warning.StatusCode, warning.ResponseBody, warning.Message)
				}
			},
			OnTurnComplete: func(turn RelayTurnResult) {
				// Settlement consumes one request snapshot for completion. An immediate
				// response terminal uses the saved copy at its following write; a pending
				// bare error can already have been sent when this callback runs. Paired
				// response-terminal writes separately control next-turn admission.
				turnNo := int(completedTurns.Add(1))
				turnPayload := turnPayloads.Pop()
				if !turn.StartedAt.IsZero() {
					turnPayload.StartedAt = turn.StartedAt
				}

				// Missing queue metadata leaves the relay's initial request model as the
				// completion fallback; its terminal response model remains a separate field.
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
						CacheWriteDetails:        turn.Usage.CacheWriteDetails,
						CacheReadInputTokens:     turn.Usage.CacheReadInputTokens,
						ImageOutputTokens:        turn.Usage.ImageOutputTokens,
					},
					Model:                       turnOriginalModel,
					UpstreamModel:               turnPayload.UpstreamModel,
					UpstreamResponseServiceTier: p.NormalizeTier(turn.ResponseServiceTier),
					UpstreamResponseModel:       turn.UpstreamResponseModel,
					ServiceTier:                 turnPayload.ServiceTier,
					ReasoningEffort:             turnPayload.ReasoningEffort,
					RequestedReasoningEffort:    turnPayload.RequestedReasoningEffort,
					Stream:                      true,
					OpenAIWSMode:                true,
					UpstreamTerminalEvent:       p.NormalizeTerminal(turn.TerminalEventType),
					ResponseHeaders:             cloneHeaders(handshakeHeaders),
					Duration:                    turn.Duration,
					Timings:                     turn.Timings,
					ClientDisconnect:            turn.Timings != nil && turn.Timings.Values["client_disconnected"] == 1,
				}
				p.Log(fmt.Sprintf(
					"relay_turn_completed provider_id=%d turn=%d request_id=%s terminal_event=%s duration_ms=%d input_tokens=%d output_tokens=%d cache_read_tokens=%d",
					o.ProviderID,
					turnNo,
					p.Truncate(turnResult.RequestID, 64),
					p.Truncate(turn.TerminalEventType, 160),
					turnResult.Duration.Milliseconds(),
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
			// Bracket response terminal delivery with the shared admission mutex. Failed
			// delivery keeps inFlight set, while success publishes the idle flag before
			// unlocking the next request.
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
				if (exit.Stage == "read_client" || exit.Stage == "write_client") && hooks != nil && hooks.ClientDisconnected != nil {
					stoppedAt := time.Now()
					turnLifecycle.ObserveActive(func() {
						if !turnPayloads.Peek().StartedAt.IsZero() {
							hooks.ClientDisconnected(int(completedTurns.Load())+1, stoppedAt)
						}
					})
				}
				// Parent control cancellation has its own lease-aware close handling after
				// relay returns; skip this ordinary exit close so it cannot replace that
				// control reason.
				if context.Cause(ctx) != nil {
					return
				}
				status, reason, ok := p.RelayClose(exit, int(completedTurns.Load()))
				if !ok {
					return
				}
				// Cap the close reason before transport closure, because an oversized
				// control-frame payload can prevent delivery of the selected close status.
				reason = p.TruncateReason(reason, 120)
				// Attempt the selected close code, then force cleanup even if that
				// handshake fails. Cleanup errors leave the relay's exit classification
				// available to the surrounding session.
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
					"relay_trace provider_id=%d stage=%s direction=%s msg_type=%s bytes=%d graceful=%v wrote_downstream=%v err=%s",
					o.ProviderID,
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
	// After relay stops, give parent control cause precedence over aggregate
	// success, using the ingress-lease reconnect status when that specific
	// cancellation is retained.
	if cause := context.Cause(ctx); cause != nil {
		status := 1001
		reason := "websocket request canceled"
		if errors.Is(cause, scheduler.ErrOpenAIWSIngressLeaseLost) {
			status = 1013
			reason = "websocket ingress capacity lease lost; please reconnect"
		}
		// Parent cancellation ends this connection. Attempt its selected close
		// instruction before force cleanup, retaining the control cause when either
		// transport close reports an error.
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
			CacheWriteDetails:        relayResult.Usage.CacheWriteDetails,
			CacheReadInputTokens:     relayResult.Usage.CacheReadInputTokens,
			ImageOutputTokens:        relayResult.Usage.ImageOutputTokens,
		},
		Model:                       relayResult.RequestModel,
		UpstreamResponseServiceTier: p.NormalizeTier(relayResult.ResponseServiceTier),
		UpstreamResponseModel:       relayResult.UpstreamResponseModel,
		ServiceTier:                 usageMeta.ServiceTier.Load(),
		ReasoningEffort:             usageMeta.ReasoningEffort.Load(),
		RequestedReasoningEffort:    usageMeta.RequestedReasoningEffort.Load(),
		Stream:                      true,
		OpenAIWSMode:                true,
		UpstreamTerminalEvent:       p.NormalizeTerminal(relayResult.TerminalEventType),
		ResponseHeaders:             cloneHeaders(handshakeHeaders),
		Duration:                    relayResult.Duration,
		Timings:                     relayResult.Timings,
	}

	turnCount := int(completedTurns.Load())
	if relayExit == nil {
		p.Log(fmt.Sprintf(
			"relay_completed provider_id=%d request_id=%s terminal_event=%s duration_ms=%d c2u_frames=%d u2c_frames=%d dropped_frames=%d turns=%d",
			o.ProviderID,
			p.Truncate(result.RequestID, 64),
			p.Truncate(relayResult.TerminalEventType, 160),
			result.Duration.Milliseconds(),
			relayResult.ClientToUpstreamFrames,
			relayResult.UpstreamToClientFrames,
			relayResult.DroppedDownstreamFrames,
			turnCount,
		))
		// A graceful relay can return without observing a terminal turn. Report its
		// aggregate result once through the retained first snapshot when no
		// completion callback consumed it.
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
		"relay_failed provider_id=%d stage=%s wrote_downstream=%v err=%s duration_ms=%d c2u_frames=%d u2c_frames=%d dropped_frames=%d turns=%d",
		o.ProviderID,
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
	if firstOutputTimeoutErr, ok := errors.AsType[*FirstOutputTimeoutError](relayErr); ok {
		deadline := firstOutputTimeoutErr.Deadline
		// Record timeout observations and provider effects before choosing the
		// returned error. Later-turn reconnect presentation still keeps those effects.
		failoverErr := p.FirstOutputFailure(ctx, deadline, handshakeHeaders)

		if turnCount == 0 && !relayExit.WroteDownstream {
			relayErr = failoverErr
		} else {
			// Entry's ordinary replay retains the first request. A later timeout closes
			// the session because sending those bytes again would repeat turn one.
			relayErr = p.CloseError(
				1001,
				"upstream produced no semantic output; please reconnect",
				firstOutputTimeoutErr,
			)
		}
	}
	if activeTurnTimeoutErr, ok := errors.AsType[*ActiveTurnTimeoutError](relayErr); ok {
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
	// Report only a still-queued accepted turn on final failure, so already
	// observed terminal completions are not submitted again from the relay exit
	// path.
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

// replaceRequestModel writes model into session.model for session.update and
// top-level model otherwise. The caller supplies the resolved provider
// identifier and closeError constructor; mutation failure returns a local
// invalid-payload close instruction.
func replaceRequestModel(payload []byte, eventType, model string, closeError func(int, string, error) error) ([]byte, error) {
	path := "model"
	// Session updates place their model inside session; patch that location so
	// the provider default follows the event's schema rather than a root field.
	if eventType == "session.update" {
		path = "session.model"
	}
	out, err := sjson.SetBytes(payload, path, model)
	if err != nil {
		return nil, closeError(1008, "invalid websocket request payload", err)
	}
	return out, nil
}

// cloneHeaders copies the header map and each value slice for completion
// snapshots. Nil headers return nil; returned map and slice edits are
// independent of the handshake map, while immutable string values are reused.
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

// errorText returns the supplied error's message for relay failure diagnostics.
// Nil returns empty, and callers apply their own truncation to the returned
// text before including it in log fields.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
