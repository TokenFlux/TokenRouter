package ws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// stream.go relays pooled upstream events while collecting usage and
// classifying recoverable turn failures.
// A pooled response turn can fail before any client frame, after leading
// events, or after the client disconnects. Provider policy is evaluated on
// observed upstream data before model, tool, and capacity-code rewrites prepare
// the client copy, preserving failure classification separately from output
// presentation.
//
// RelayTurn permits first-turn pre-output failures to return for generic close
// or provider failover and suppresses an eligible missing-continuation error
// for ingress recovery. After output begins it keeps terminal handling inside
// the current stream. A recognized client disconnect stops further writes but
// continues collecting upstream usage and images until terminal, then marks the
// lease broken. The shared performance observer classifies upstream payloads
// before client rewriting. Final results retain those arrival observations,
// upstream model and tier, and successful downstream delivery separately.

// RelayTurn forwards one normalized pooled request and returns its terminal
// observations or final classified error. The caller owns the lease and
// release; Port, its image and replay collectors, and adapter-owned downstream
// write controls are required.
//
// Write and read errors retain the I/O stage and successful downstream-write
// state for ingress retry decisions. First-turn generic and failover policy can
// return before output; eligible missing-response recovery also returns before
// exposing its error event. Later failures finish through the active output
// path. A recognized downstream disconnect drains usage and image observations
// until terminal and marks the lease broken. Performance captures useful upstream
// content independently of diagnostic event-label counts. Model and tier are
// observed before client rewriting.
func RelayTurn(ctx context.Context, p StreamPort, lease StreamLease, input ClientPayload, turn int, o StreamOptions, hooks *StreamHooks) (*ForwardResult, error) {
	payload, payloadBytes, originalModel, routingModel := input.PayloadRaw, input.PayloadBytes, input.OriginalModel, input.RoutingModel
	imageBillingModel, imageSizeTier, imageInputSize := input.ImageBillingModel, input.ImageSizeTier, input.ImageInputSize
	requestedReasoningEffort := input.RequestedReasoningEffort
	p.BeginObservation()
	var modelObserver protocol.ResponseModelObserver
	if lease == nil {
		return nil, errors.New("upstream websocket lease is nil")
	}
	turnStart := time.Now()
	performance := timing.BeginPerformance(ctx, o.ProviderID, turnStart)
	wroteDownstream := false
	// Send the normalized request before observing events; a write failure records
	// the pre-output stage so ingress can consider its bounded connection retry.
	if err := lease.WriteRequest(ctx, payload, o.WriteTimeout); err != nil {
		return nil, WrapIngressTurnError(
			"write_upstream",
			fmt.Errorf("write upstream websocket request: %w", err),
			false,
		)
	}
	if o.Debug {
		p.Debug(fmt.Sprintf(
			"ingress_ws_turn_request_sent provider_id=%d turn=%d conn_id=%s payload_bytes=%d",
			o.ProviderID,
			turn,
			p.Truncate(lease.ConnID(), 64),
			payloadBytes,
		))
	}

	responseID := ""
	usage := wire.ForwardUsage{}
	imageCounter := p.ImageCounter()

	reqStream := p.Streaming(payload)
	turnPreviousResponseID := payloadString(payload, "previous_response_id")
	turnPreviousResponseIDKind := p.ClassifyPrevious(turnPreviousResponseID)
	turnPromptCacheKey := payloadString(payload, "prompt_cache_key")
	turnStoreDisabled := p.StoreDisabled(payload)
	turnHasFunctionCallOutput := p.HasToolOutput(payload)
	eventCount := 0
	tokenEventCount := 0
	terminalEventCount := 0
	replayCollector := p.ReplayCollector()
	firstEventType := ""
	lastEventType := ""
	needModelReplace := false
	clientDisconnected := false
	mappedModel := ""
	var mappedModelBytes []byte
	if routingModel != "" {
		mappedModel = p.MappedModel(routingModel)
		needModelReplace = mappedModel != "" && mappedModel != originalModel
		if needModelReplace {
			mappedModelBytes = []byte(mappedModel)
		}
	}
	// Build both successful and invalid-result observations from the same turn
	// state so stopping GIF delivery cannot discard already reported token usage.
	resultWithUsage := func(terminalEvent string) *ForwardResult {
		imageCount := imageCounter.Count()
		performance.FinishAt(time.Now())
		result := &ForwardResult{
			RequestID:                   responseID,
			Usage:                       usage,
			Model:                       originalModel,
			UpstreamModel:               mappedModel,
			UpstreamResponseServiceTier: p.ResponseTier(),
			UpstreamResponseModel:       modelObserver.Model(),
			ServiceTier:                 p.ResolvedTier(payload),
			ReasoningEffort:             p.Reasoning(payload, mappedModel, originalModel),
			RequestedReasoningEffort:    requestedReasoningEffort,
			Stream:                      reqStream,
			OpenAIWSMode:                true,
			UpstreamTerminalEvent:       terminalEvent,
			ResponseHeaders:             lease.Headers(),
			Duration:                    time.Since(turnStart),
			Timings:                     performance.Clone(),
		}
		if replayInput := replayCollector.Items(); len(replayInput) > 0 {
			result.WSReplayInput = replayInput
			result.WSReplayInputExists = true
		}
		if imageCount > 0 {
			result.ImageCount = imageCount
			result.ImageFrameCount = imageCounter.FrameCount()
			result.ImageSize = imageSizeTier
			result.ImageInputSize = imageInputSize
			result.ImageOutputSizes = imageCounter.Sizes()
			result.BillingModel = imageBillingModel
		}
		return result
	}
	for {
		upstreamMessage, readErr := lease.ReadEvent(ctx, o.ReadTimeout)
		// A failed upstream reader leaves connection health uncertain, so mark the
		// lease broken before returning its output-aware retry stage for session
		// cleanup.
		if readErr != nil {
			lease.MarkBroken()
			return nil, WrapIngressTurnError(
				"read_upstream",
				fmt.Errorf("read upstream websocket event: %w", readErr),
				wroteDownstream,
			)
		}
		// Repair compatible terminal envelopes before event observation and
		// classification, keeping model, usage, warning, and client output consumers
		// on the same normalized event form.
		if normalized, changed := p.NormalizeCompleted(upstreamMessage); changed {
			upstreamMessage = normalized
		}

		eventType, eventResponseID, _ := p.Envelope(upstreamMessage)
		p.ObserveModel(upstreamMessage, eventType)
		modelObserver.ObserveOpenAI(upstreamMessage, eventType)
		if responseID == "" && eventResponseID != "" {
			responseID = eventResponseID
		}
		if eventType != "" {
			eventCount++
			if firstEventType == "" {
				firstEventType = eventType
			}
			lastEventType = eventType
		}
		// Update usage before saving risk evidence, so marked policy handling sees
		// current counts even when a pre-output failure returns no terminal
		// ForwardResult.
		// @project-doc docs/domains/content_moderation.md#upstream_cyber_policy
		if p.ShouldParseUsage(eventType) {
			p.ParseUsage(upstreamMessage, &usage)
		}
		if eventType == "error" || eventType == "response.failed" {
			p.MarkCyber(upstreamMessage, &usage)
		}
		if eventType == "error" {
			canonicalModel := p.SchedulingModel(routingModel)
			errorDecision := p.ErrorDecision(ctx, canonicalModel, lease.Headers(), upstreamMessage)
			errCodeRaw, errTypeRaw, errMsgRaw := p.ErrorFields(upstreamMessage)
			errorStatus := p.ErrorStatus(upstreamMessage)
			fallbackReason, _ := p.ClassifyError(errCodeRaw, errTypeRaw, errMsgRaw)
			if fallbackReason == "invalid_encrypted_content" {
				// Remember ciphertext rejected in this request under the scoped session
				// before error output, allowing later turns to strip that lineage from
				// payload and replay history.
				if digests := p.EncryptedDigests(payload); len(digests) > 0 {
					p.MarkEncrypted(digests)
					p.Log(fmt.Sprintf(
						"ingress_ws_invalid_encrypted_lineage_mark provider_id=%d turn=%d digests=%d",
						o.ProviderID,
						turn,
						len(digests),
					))
				}
			}
			errCode, errType, errMessage := p.SummarizeError(errCodeRaw, errTypeRaw, errMsgRaw)
			recoverablePrevNotFound := fallbackReason == IngressStagePreviousResponseNotFound &&
				turnPreviousResponseID != "" &&
				!turnHasFunctionCallOutput &&
				o.PreviousRecovery &&
				!wroteDownstream
			if recoverablePrevNotFound {
				// Keep the recoverable log label free of error, because LegacyPrintf uses
				// that spelling to raise severity even when ingress can still recover.
				p.Log(fmt.Sprintf(
					"ingress_ws_prev_response_recoverable provider_id=%d turn=%d conn_id=%s idx=%d reason=%s code=%s type=%s message=%s previous_response_id=%s previous_response_id_kind=%s response_id=%s store_disabled=%v has_prompt_cache_key=%v",
					o.ProviderID,
					turn,
					p.Truncate(lease.ConnID(), 64),
					eventCount,
					p.Truncate(fallbackReason, 160),
					errCode,
					errType,
					errMessage,
					p.Truncate(turnPreviousResponseID, 64),
					p.NormalizeLog(turnPreviousResponseIDKind),
					p.Truncate(responseID, 64),
					turnStoreDisabled,
					turnPromptCacheKey != "",
				))
			} else {
				p.Log(fmt.Sprintf(
					"ingress_ws_error_event provider_id=%d turn=%d conn_id=%s idx=%d fallback_reason=%s err_code=%s err_type=%s err_message=%s previous_response_id=%s previous_response_id_kind=%s response_id=%s store_disabled=%v has_prompt_cache_key=%v",
					o.ProviderID,
					turn,
					p.Truncate(lease.ConnID(), 64),
					eventCount,
					p.Truncate(fallbackReason, 160),
					errCode,
					errType,
					errMessage,
					p.Truncate(turnPreviousResponseID, 64),
					p.NormalizeLog(turnPreviousResponseIDKind),
					p.Truncate(responseID, 64),
					turnStoreDisabled,
					turnPromptCacheKey != "",
				))
			}
			// Suppress a recoverable previous_response_not_found event while ingress
			// removes previous_response_id and replays the current turn once.
			if recoverablePrevNotFound {
				lease.MarkBroken()
				errMsg := strings.TrimSpace(errMsgRaw)
				if errMsg == "" {
					errMsg = "previous response not found"
				}
				return nil, WrapIngressTurnError(
					IngressStagePreviousResponseNotFound,
					errors.New(errMsg),
					false,
				)
			}
			// A first-turn error before output can escape as generic close or provider
			// failover. Mark that transport broken because this attempt will not consume
			// its remaining events.
			if turn == 1 && !wroteDownstream && errorDecision.Generic {
				lease.MarkBroken()
				return nil, p.GenericError(errorStatus)
			}
			if turn == 1 && !wroteDownstream && errorStatus != 0 && errorDecision.Failover {
				lease.MarkBroken()
				return nil, p.RawFailure(errorStatus, lease.Headers(), upstreamMessage, errorDecision.RetrySame)
			}
		}
		if warning := p.Warning(eventType, upstreamMessage); warning != nil && hooks != nil && hooks.OnUpstreamError != nil {
			hooks.OnUpstreamError(turn, originalModel, warning.StatusCode, warning.ResponseBody, warning.Message)
		}
		isTokenEvent := p.IsToken(eventType)
		if isTokenEvent {
			tokenEventCount++
		}
		isTerminalEvent := p.IsTerminal(eventType)
		if isTerminalEvent {
			terminalEventCount++
		}
		// Record the first label selected by the gateway token predicate. That
		// predicate can accept empty delta or structural output labels, so this
		// timestamp is not a content-validity check.
		performance.ObserveAt(string(upstreamMessage), eventType, time.Now())
		if err := wire.ValidateGIFImageOutputs(upstreamMessage); err != nil {
			lease.MarkBroken()
			return resultWithUsage("response.failed"), err
		}
		imageCounter.AddSSEData(upstreamMessage)
		terminalPolicy := TerminalPolicy{
			TerminalEvent: p.NormalizeTerminal(eventType),
			Decision:      ErrorPolicy{},
		}
		if isTerminalEvent {
			canonicalModel := p.SchedulingModel(routingModel)
			terminalPolicy = p.TerminalDecision(ctx, canonicalModel, lease.Headers(), upstreamMessage)
		}
		if eventType == "response.failed" {
			// Use the terminal provider decision before client presentation; first-turn
			// pre-output failure can return, while an active stream retains its already
			// delivered prefix.
			if turn == 1 && !wroteDownstream && terminalPolicy.Decision.Generic {
				lease.MarkBroken()
				return nil, p.GenericError(terminalPolicy.StatusCode)
			}
			if turn == 1 && !wroteDownstream && terminalPolicy.Decision.Failover {
				lease.MarkBroken()
				return nil, p.Failure(
					terminalPolicy.StatusCode,
					lease.Headers(),
					upstreamMessage,
					p.Message(upstreamMessage),
					terminalPolicy.Decision.RetrySame,
				)
			}
			if terminalPolicy.Decision.Generic {
				// Keep prior delivered events and replace the selected failure payload with
				// the generic client event, while terminalPolicy retains the original
				// classified completion outcome.
				upstreamMessage = p.GenericEvent()
			}
		}

		// Restore client model and tool identities before collecting forwarded replay
		// output, while independent upstream observers retain the provider
		// declarations used by completion.
		if !clientDisconnected {
			if needModelReplace && len(mappedModelBytes) > 0 && p.MayContainModel(eventType) && bytes.Contains(upstreamMessage, mappedModelBytes) {
				upstreamMessage = p.ReplaceModel(upstreamMessage, mappedModel, originalModel)
			}
			if p.MayContainTools(eventType) && p.LikelyTools(upstreamMessage) {
				if corrected, changed := p.CorrectTools(upstreamMessage); changed {
					upstreamMessage = corrected
				}
			}
			replayCollector.AddEvent(eventType, upstreamMessage)
			// Rewrite capacity codes in the client copy for Codex retry presentation.
			// Provider policy has already classified the upstream event; changing this
			// outgoing copy leaves that earlier decision and observed error evidence
			// available for completion.
			clientMessage := upstreamMessage
			if eventType == "error" || eventType == "response.failed" {
				if rewritten, changed := p.CapacityShed(clientMessage); changed {
					clientMessage = rewritten
				}
			}
			if err := p.WriteClient(clientMessage); err != nil {
				// A recognized client disconnect ends downstream delivery but keeps
				// upstream reads active, preserving terminal usage and generated image
				// counts before releasing this turn.
				if p.IsDisconnect(err) {
					clientDisconnected = true
					timing.ObserveClientDisconnect(ctx, time.Now())
					closeStatus, closeReason := p.SummarizeClose(err)
					p.Log(fmt.Sprintf(
						"ingress_ws_client_disconnected_drain provider_id=%d turn=%d conn_id=%s close_status=%s close_reason=%s",
						o.ProviderID,
						turn,
						p.Truncate(lease.ConnID(), 64),
						closeStatus,
						p.Truncate(closeReason, 120),
					))
				} else {
					return nil, WrapIngressTurnError(
						"write_client",
						fmt.Errorf("write client websocket event: %w", err),
						wroteDownstream,
					)
				}
			} else {
				wroteDownstream = true
				performance.Delivered(string(upstreamMessage), time.Now())
			}
		}
		if isTerminalEvent {
			// Discard a disconnected client's connection after terminal drain, because
			// this session can no longer use the transport for subsequent client turn
			// delivery.
			if clientDisconnected {
				lease.MarkBroken()
			}
			if o.Debug {
				p.Debug(fmt.Sprintf(
					"ingress_ws_turn_completed provider_id=%d turn=%d conn_id=%s response_id=%s duration_ms=%d events=%d token_events=%d terminal_events=%d first_event=%s last_event=%s client_disconnected=%v",
					o.ProviderID,
					turn,
					p.Truncate(lease.ConnID(), 64),
					p.Truncate(responseID, 64),
					time.Since(turnStart).Milliseconds(),
					eventCount,
					tokenEventCount,
					terminalEventCount,
					p.Truncate(firstEventType, 160),
					p.Truncate(lastEventType, 160),
					clientDisconnected,
				))
			}
			result := resultWithUsage(terminalPolicy.TerminalEvent)
			return result, nil
		}
	}
}
