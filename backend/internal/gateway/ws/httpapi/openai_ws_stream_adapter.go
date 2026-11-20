package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_ws_stream_adapter.go supplies protocol observation and provider error
// handling for pooled WebSocket turns.
// Pooled WS output keeps observed upstream model and tier separate from the
// outbound request's billing inputs. The session core chooses retry and drain
// behavior; this adapter supplies protocol parsing, provider health, client
// presentation and copied handshake diagnostics for that turn's result.

// wsStreamAdapter parses upstream frames and applies provider health rules.
type wsStreamAdapter struct {
	*wsPassthroughAdapter // shared provider and payload helpers
	// observer is replaced by BeginObservation for each pooled turn and stored
	// in the Gin context. RelayTurn serializes its event updates and result reads.
	observer    *forwardcore.ResponseObserver
	state       *gatewayws.IngressState // current session continuation state
	groupID     int64                   // authenticated request routing group
	turnEffort  string                  // normalized reasoning effort for the active turn
	writeClient func([]byte) error      // synchronous client event writer
}

// BeginObservation replaces the turn's upstream response observer and
// publishes it in Gin. The pooled relay calls it before reading events so a
// previous turn's model or tier evidence cannot enter the current result.
func (p *wsStreamAdapter) BeginObservation() {
	p.observer = gatewayhttp.BeginUpstreamResponseModelObservation(p.request)
}

// ObserveModel records actual upstream model and tier evidence before client
// restoration.
func (p *wsStreamAdapter) ObserveModel(body []byte, event string) {
	p.observer.ObserveOpenAI(body, event)
}

// ResponseTier returns the observer's preferred upstream tier, or empty when
// unobserved or conflicting. Terminal evidence wins over agreeing nonterminal
// evidence; this observed label remains separate from the request tier used in
// result reconciliation.
func (p *wsStreamAdapter) ResponseTier() string { return p.observer.ServiceTier() }

// ResolvedTier returns the outbound request's optional service-tier value for
// completion. The shared helper ignores the observer argument and returns that
// request value unchanged; observed upstream tier remains available separately
// through ResponseTier.
func (p *wsStreamAdapter) ResolvedTier(body []byte) *string {
	return gatewayhttp.ResolvedOpenAIUpstreamServiceTierFromObserver(p.observer, requeststate.ExtractOpenAIServiceTierFromBody(body))
}

// Reasoning returns reporting effort with the mapped-model thinking-enabled
// default when applicable. The original-model parameter is unused; supplied
// body effort is extracted before the shared fallback, and no usable effort
// can leave nil.
func (p *wsStreamAdapter) Reasoning(body []byte, mapped, original string) *string {
	return gatewayprovider.ApplyThinkingEnabledFallback(requeststate.ExtractOpenAIReasoningEffortFromBody(body), body, mapped)
}

// ImageCounter returns a new protocol counter for this pooled turn's observed
// image outputs.
func (p *wsStreamAdapter) ImageCounter() gatewayws.ImageCounter {
	return wire.NewOpenAIImageOutputCounter()
}

// ReplayCollector returns a fresh collector for this turn's tool and
// full-output replay.
func (p *wsStreamAdapter) ReplayCollector() gatewayws.ReplayCollector {
	return &openAIWSToolCallReplayCollector{}
}

// Streaming reads the request's stream boolean with true as its protocol
// default. Missing or unusable field values therefore preserve streaming
// interpretation; this result labels observations rather than selecting the
// upstream transport.
func (p *wsStreamAdapter) Streaming(body []byte) bool {
	return wire.WSPayloadBoolFromRaw(body, "stream", true)
}

// StoreDisabled reports the selected provider's disabled-store rule for this
// request body.
func (p *wsStreamAdapter) StoreDisabled(body []byte) bool {
	return p.service.isOpenAIWSStoreDisabledInRequestRaw(body, p.provider)
}

// ClassifyPrevious labels the supplied continuation ID for pooled response
// diagnostics.
func (p *wsStreamAdapter) ClassifyPrevious(id string) string {
	return wire.ClassifyOpenAIPreviousResponseIDKind(id)
}

// HasToolOutput reports protocol tool-call output items in this request's
// input.
func (p *wsStreamAdapter) HasToolOutput(body []byte) bool {
	return openai.OpenAIWSRawPayloadHasToolCallOutput(body)
}

// MappedModel returns the effort-aware provider-mapped and normalized OpenAI
// model for pooled execution, preferring the active turn's effort over the
// connection's initial request context.
func (p *wsStreamAdapter) MappedModel(model string) string {
	effort := p.turnEffort
	if effort == "" && p.request != nil && p.request.Request != nil {
		effort = requeststate.RoutingReasoningEffortFromContext(p.request.Request.Context())
	}
	return gatewayprovider.ExecutionModelPolicy(p.provider).WithEffort(effort).NormalizeOpenAI(
		providercore.ResolveForwardMappedModelForEffort(gatewayprovider.ExecutionRecord(p.provider), model, effort, provideradapter.ModelDefaults()),
	)
}

// Envelope returns event type and response ID parsed from the upstream
// message. Its third result is fixed false and is discarded by the pooled
// relay; protocol envelope parsing owns missing-field behavior.
func (p *wsStreamAdapter) Envelope(body []byte) (string, string, bool) {
	event, id, _ := wire.ParseWSEventEnvelope(body)
	return event, id, false
}

// ShouldParseUsage reports labels selected by the protocol's WS usage parser.
func (p *wsStreamAdapter) ShouldParseUsage(event string) bool {
	return wire.WSEventShouldParseUsage(event)
}

// ParseUsage applies terminal-response usage extraction to the supplied
// mutable usage accumulator.
func (p *wsStreamAdapter) ParseUsage(body []byte, usage *wire.ForwardUsage) {
	wire.ParseWSResponseUsageFromCompletedEvent(body, usage)
}

// MarkCyber records Cyber evidence with already parsed usage under the
// accepted stream's HTTP status.
func (p *wsStreamAdapter) MarkCyber(body []byte, usage *wire.ForwardUsage) {
	gatewayhttp.MarkOpenAICyberPolicyEvent(p.request, body, http.StatusOK, usage)
}

// SchedulingModel returns the provider's effort-aware canonical model key used
// by scheduler health feedback.
func (p *wsStreamAdapter) SchedulingModel(model string) string {
	policy := gatewayprovider.ExecutionModelPolicy(p.provider)
	if p.turnEffort != "" {
		policy = policy.WithEffort(p.turnEffort)
	} else if p.request != nil && p.request.Request != nil {
		policy = policy.WithContext(p.request.Request.Context())
	}
	return policy.CanonicalSchedulingModel(model)
}

// ErrorDecision applies standalone-event provider policy and exposes generic,
// failover and same-provider retry flags. Policy status and platform default
// failover classification come from the same body; the relay separately checks
// turn and output progress before acting on these flags.
func (p *wsStreamAdapter) ErrorDecision(ctx context.Context, model string, headers map[string][]string, body []byte) gatewayws.ErrorPolicy {
	decision := p.service.handleOpenAIWSErrorEventTransientFailure(ctx, p.provider, model, headers, body)
	status := openAIWSErrorPolicyStatus(body)
	return gatewayws.ErrorPolicy{Generic: decision.ShouldReturnGenericError(), Failover: decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(p.provider), status, status == http.StatusTooManyRequests, p.service.shouldFailoverOpenAIWSError(p.provider, status, body)), RetrySame: decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(p.provider), status)}
}

// TerminalDecision applies failed-terminal policy and retains canonical
// terminal identity and status. Generic, failover and retry flags include
// selected-provider defaults; the pooled relay determines whether its current
// turn and output progress permit the returned action.
func (p *wsStreamAdapter) TerminalDecision(ctx context.Context, model string, headers map[string][]string, body []byte) gatewayws.TerminalPolicy {
	policy := p.service.handleOpenAIWSTerminalTransientFailure(ctx, p.provider, model, headers, body)
	d := policy.Decision
	return gatewayws.TerminalPolicy{TerminalEvent: policy.TerminalEvent, StatusCode: policy.StatusCode, Decision: gatewayws.ErrorPolicy{Generic: d.ShouldReturnGenericError(), Failover: d.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(p.provider), policy.StatusCode, false, p.service.shouldFailoverOpenAIWSError(p.provider, policy.StatusCode, body)), RetrySame: d.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(p.provider), policy.StatusCode)}}
}

// ErrorFields returns protocol code, type and message strings for WS error
// diagnostics.
func (p *wsStreamAdapter) ErrorFields(body []byte) (string, string, string) {
	return wire.ParseWSErrorEventFields(body)
}

// ErrorStatus returns the explicit or inferred provider-policy HTTP status for
// this event. Empty payload returns zero; valid explicit 4xx or 5xx error
// fields take precedence over code and type classification.
func (p *wsStreamAdapter) ErrorStatus(body []byte) int { return openAIWSErrorPolicyStatus(body) }

// ClassifyError classifies parsed failure fields and returns the protocol's
// retry eligibility flag.
func (p *wsStreamAdapter) ClassifyError(code, kind, message string) (string, bool) {
	return openai.ClassifyWSErrorEventFromRaw(code, kind, message)
}

// EncryptedDigests collects raw encrypted-content digests from this rejected
// request body.
func (p *wsStreamAdapter) EncryptedDigests(body []byte) []string {
	return openai.CollectOpenAIEncryptedContentDigestsRaw(body)
}

// MarkEncrypted records rejected ciphertext digests for this group and current
// session hash. The lineage service owns optional cache writes and lifetime,
// letting later ingress normalization strip only previously rejected session
// content.
func (p *wsStreamAdapter) MarkEncrypted(digests []string) {
	p.service.Lineage.Mark(p.groupID, p.state.SessionHash, digests)
}

// SummarizeError returns normalized code, type and message labels for WS error
// logging.
func (p *wsStreamAdapter) SummarizeError(code, kind, message string) (string, string, string) {
	return gatewayprovider.SummarizeOpenAIWSErrorEventFieldsFromRaw(code, kind, message)
}

// GenericError returns a typed generic policy close instruction retaining the
// upstream status.
func (p *wsStreamAdapter) GenericError(status int) error {
	return openAIWSGenericPolicyCloseError(status)
}

// RawFailure constructs an upstream failover error with copied headers and
// body plus the supplied retry flag. Copying both mutable inputs lets entry
// retain diagnostics after pooled frame storage or header views change.
func (p *wsStreamAdapter) RawFailure(status int, headers map[string][]string, body []byte, retry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseHeaders: upstream.CloneHeader(headers), ResponseBody: append([]byte(nil), body...), RetryableOnSameProvider: retry}
}

// Failure constructs the shared OpenAI upstream failure from status, headers,
// body and message. That helper clones headers and derives request-scoped
// capacity and retry behavior; body ownership remains with the supplied
// retained event bytes.
func (p *wsStreamAdapter) Failure(status int, headers map[string][]string, body []byte, message string, retry bool) error {
	return gatewayprovider.NewOpenAIUpstreamFailure(status, headers, body, message, retry)
}

// IsToken selects content-event labels for pooled relay diagnostic event counts.
// The protocol predicate inspects no payload content and accepts some
// structural output or empty-delta labels; dedicated relay timing uses a
// different label set.
func (p *wsStreamAdapter) IsToken(event string) bool { return wire.IsWSTokenEvent(event) }

// Message extracts the OpenAI stream error message used by pooled failure
// presentation.
func (p *wsStreamAdapter) Message(body []byte) string {
	return openai.ExtractOpenAISSEErrorMessage(body)
}

// GenericEvent renders the generic 500 upstream-gateway error as a client WS
// event.
func (p *wsStreamAdapter) GenericEvent() []byte {
	return buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error")
}

// MayContainTools reports whether this event label can contain protocol tool
// calls.
func (p *wsStreamAdapter) MayContainTools(event string) bool {
	return wire.WSEventMayContainToolCalls(event)
}

// LikelyTools screens event bytes for probable tool calls before correction
// parsing.
func (p *wsStreamAdapter) LikelyTools(body []byte) bool {
	return wire.WSMessageLikelyContainsToolCalls(body)
}

// CorrectTools applies the configured Codex tool-call correction and returns
// its changed flag. Callers bind an initialized Output.Corrector when tool
// event and payload screens select this callback; actual correction records
// statistics on that service. Unchanged eligible input retains its bytes
// through the helper.
func (p *wsStreamAdapter) CorrectTools(body []byte) ([]byte, bool) {
	return p.service.Output.Corrector.CorrectToolCallsInSSEBytes(body)
}

// CapacityShed sanitizes capacity-error codes selected for client delivery and
// reports changed bytes. Provider policy has already run in the relay before
// this presentation step, retaining its decision from the prior event.
func (p *wsStreamAdapter) CapacityShed(body []byte) ([]byte, bool) {
	return openai.SanitizeOpenAICapacityShedErrorCodeForClient(body)
}

// WriteClient synchronously sends this event through the bound downstream
// frame writer.
func (p *wsStreamAdapter) WriteClient(body []byte) error { return p.writeClient(body) }

// IsDisconnect recognizes downstream client transport errors for the pooled
// drain path.
func (p *wsStreamAdapter) IsDisconnect(err error) bool {
	return gatewayprovider.IsOpenAIWSClientDisconnectError(err)
}

// SummarizeClose returns close labels for pooled upstream or downstream read
// diagnostics.
func (p *wsStreamAdapter) SummarizeClose(err error) (string, string) {
	return gatewayprovider.SummarizeOpenAIWSReadCloseError(err)
}

// NormalizeLog trims diagnostic text and applies the provider helper's fixed
// substring substitutions. Blank input returns "-"; nonblank input rewrites
// lowercase error, fallback, warning and failed to their diagnostic labels.
func (p *wsStreamAdapter) NormalizeLog(value string) string {
	return gatewayprovider.NormalizeOpenAIWSLogValue(value)
}

// Log writes a pooled WS information diagnostic through the provider logger.
func (p *wsStreamAdapter) Log(message string) { gatewayprovider.LogOpenAIWSModeInfo("%s", message) }

// Debug writes a pooled WS debug diagnostic through the provider logger.
func (p *wsStreamAdapter) Debug(message string) { gatewayprovider.LogOpenAIWSModeDebug("%s", message) }

// WriteRequest sends raw JSON through the active wrapped pool lease under ctx.
// A positive timeout adds a deadline; nonpositive timeout keeps the parent
// context alone, and inactive leases return the connection-closed sentinel.
// The caller retains lease cleanup ownership.
func (l *wsIngressLease) WriteRequest(ctx context.Context, body []byte, timeout time.Duration) error {
	return l.lease.WriteJSONWithContextTimeout(ctx, json.RawMessage(body), timeout)
}

// ReadEvent reads the next message from the active wrapped pool lease under
// ctx. A positive timeout adds a per-read deadline; nonpositive timeout keeps
// the parent context alone, and inactive leases return the connection-closed
// sentinel.
func (l *wsIngressLease) ReadEvent(ctx context.Context, timeout time.Duration) ([]byte, error) {
	return l.lease.ReadMessageWithContextTimeout(ctx, timeout)
}

// Headers returns cloned handshake map and value slices from the wrapped pool
// lease. A missing underlying lease or connection returns nil; the copied
// result can be retained for completion diagnostics independently of pool
// header storage.
func (l *wsIngressLease) Headers() map[string][]string { return l.lease.HandshakeHeaders() }
