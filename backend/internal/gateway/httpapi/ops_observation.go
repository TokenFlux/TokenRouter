package httpapi

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// ops_observation.go records per-request and WebSocket-turn Ops telemetry
// observations separately from committed HTTP wire status.
// A streaming response can commit HTTP 200 before an in-band failure ends the
// stream, and long-lived WebSocket connections reuse one Gin context across
// sequential turns. Internal turn, stream-error, capacity, and dedicated-log
// flags live in one typed opsRequestState value while exported diagnostic keys
// expose request and attempt metadata to sibling gateway adapters.
//
// Turn setup resets per-attempt rule and upstream observations, retains the
// first failure per HTTP request or WebSocket turn, and bounds WebSocket turn
// history so long-lived sessions cannot grow unbounded slices. Event snapshots
// copy struct values at capture time so later turns cannot mutate earlier turn
// attribution.

// Gin context keys expose request, upstream, stage-latency, and policy markers
// across gateway subpackages, while opsRequestStateKey holds internal stream
// and turn state.
const (
	OpsUpstreamStatusCodeKey   = "ops_upstream_status_code"   // latest positive upstream HTTP status
	OpsUpstreamErrorMessageKey = "ops_upstream_error_message" // latest trimmed upstream error summary
	OpsUpstreamErrorDetailKey  = "ops_upstream_error_detail"  // latest trimmed upstream error detail
	OpsUpstreamErrorsKey       = "ops_upstream_errors"        // ordered []*ops.OpsUpstreamErrorEvent slice
	OpsUpstreamModelKey        = "ops_upstream_model"         // effective upstream model slug

	OpsModelKey        = "ops_model"        // client-requested model attribution
	OpsStreamKey       = "ops_stream"       // whether the request streams output
	OpsProviderIDKey   = "ops_provider_id"  // selected provider ID (int64)
	opsRequestTypeKey  = "ops_request_type" // persisted request mode enum (int16)
	opsRequestStateKey = "ops_request_state"

	OpsAuthLatencyMsKey     = "ops_auth_latency_ms"     // authentication stage duration in ms
	OpsRoutingLatencyMsKey  = "ops_routing_latency_ms"  // routing and selection duration in ms
	OpsUpstreamLatencyMsKey = "ops_upstream_latency_ms" // upstream exchange duration in ms
	OpsResponseLatencyMsKey = "ops_response_latency_ms" // response delivery duration in ms

	OpsSkipPassthroughKey = "ops_skip_passthrough" // matched skip_monitoring=true rule flag
	ResponseCommittedKey  = "response_committed"   // handled error-response presentation latch

	OpsClientBusinessLimitedKey                           = "ops_client_business_limited"        // SLA-excluded client policy marker
	OpsClientBusinessLimitedReasonKey                     = "ops_client_business_limited_reason" // specific client policy rejection reason
	OpsClientBusinessLimitedReasonIPRestriction           = "api_key_ip_restriction"             // caller address rejected by Key policy
	OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable  = "api_key_group_unavailable"          // assigned group unavailable for this Key
	OpsClientBusinessLimitedReasonAPIKeyGroupUnassigned   = "api_key_group_unassigned"           // Key has no assigned routing group
	OpsClientBusinessLimitedReasonLocalFeatureGate        = "local_feature_gate"                 // request blocked by local feature settings
	OpsClientBusinessLimitedReasonLocalPolicyDenied       = "local_policy_denied"                // request denied by local policy evaluation
	OpsClientBusinessLimitedReasonLocalModelConfiguration = "local_model_configuration"          // model has invalid local routing configuration

	maxOpsStreamErrorsPerRequest = 64 // bounded WebSocket turn failure history per connection
)

// opsRequestState consolidates internal per-request Ops flags, WebSocket turn
// tracking, and retained in-band stream failures under one Gin context entry.
type opsRequestState struct {
	streamTurn             int              // active 1-based WebSocket turn or zero for HTTP
	streamError            *OpsStreamError  // first HTTP stream error or latest WebSocket turn error
	streamErrors           []OpsStreamError // bounded WebSocket per-turn stream failure history
	routingCapacityLimited bool             // whether local provider capacity exhaustion occurred
	dedicatedErrorRecorded bool             // whether a dedicated handler already queued an Ops entry
}

// OpsStreamError describes a client-visible failure after stream commitment.
// WebSocket turns retain separate snapshots; HTTP/SSE uses turn zero.
type OpsStreamError struct {
	ErrType string // client error category label
	Code    string // optional client error code
	Message string // trimmed client failure message
	// IntendedStatus supplies severity classification when positive; otherwise
	// logging uses wire status.
	IntendedStatus int
	// CountTowardsSLA asks logging to record an IntendedStatus of at least 400 as
	// the logical failure status instead of keeping the committed wire status.
	CountTowardsSLA bool
	// Turn identifies the captured WebSocket turn; zero denotes ordinary HTTP/SSE.
	Turn int
	// SkipMonitoring captures the active monitoring suppression decision when this
	// failure is marked so later WebSocket turns cannot overwrite it.
	SkipMonitoring bool
	// ProviderID, UpstreamModel, UpstreamStatus, UpstreamMessage, and
	// UpstreamDetail snapshot execution attribution when marking this failure so
	// later WebSocket turns can reuse context keys without altering prior turns.
	ProviderID      int64  // captured selected provider identity
	UpstreamModel   string // captured effective upstream model
	UpstreamStatus  int    // captured upstream HTTP status
	UpstreamMessage string // captured upstream diagnostic message
	UpstreamDetail  string // captured upstream diagnostic detail
	// UpstreamErrors owns copied event structs at capture time; consumers treat
	// retrieved slices and pointers as immutable.
	UpstreamErrors []*ops.OpsUpstreamErrorEvent
}

// getOrCreateOpsRequestState returns the mutable opsRequestState stored on c,
// allocating and registering one when absent. Nil contexts return nil.
func getOrCreateOpsRequestState(c *gin.Context) *opsRequestState {
	if c == nil {
		return nil
	}
	if existing, ok := c.Get(opsRequestStateKey); ok {
		if state, ok := existing.(*opsRequestState); ok && state != nil {
			return state
		}
	}
	state := &opsRequestState{}
	c.Set(opsRequestStateKey, state)
	return state
}

// opsRequestStateFromContext returns the existing opsRequestState on c without
// allocating when absent.
func opsRequestStateFromContext(c *gin.Context) *opsRequestState {
	if c == nil {
		return nil
	}
	existing, ok := c.Get(opsRequestStateKey)
	if !ok {
		return nil
	}
	state, _ := existing.(*opsRequestState)
	return state
}

// MarkResponseCommitted records that error output was handled on c so outer
// recovery paths avoid writing duplicate error bodies.
func MarkResponseCommitted(c *gin.Context) {
	if c != nil {
		c.Set(ResponseCommittedKey, true)
	}
}

// IsResponseCommitted reports whether handled error output was marked on c.
func IsResponseCommitted(c *gin.Context) bool {
	return c != nil && c.GetBool(ResponseCommittedKey)
}

// MarkOpsSkipMonitoring marks c to suppress final Ops error queueing after a
// skip_monitoring=true passthrough rule matches.
func MarkOpsSkipMonitoring(c *gin.Context) {
	if c != nil {
		c.Set(OpsSkipPassthroughKey, true)
	}
}

// MarkOpsDedicatedErrorRecorded records that a specialized handler such as
// session-block moderation already enqueued the Ops entry for c.
func MarkOpsDedicatedErrorRecorded(c *gin.Context) {
	if state := getOrCreateOpsRequestState(c); state != nil {
		state.dedicatedErrorRecorded = true
	}
}

// IsOpsDedicatedErrorRecorded reports whether a specialized handler already
// enqueued the Ops entry for c.
func IsOpsDedicatedErrorRecorded(c *gin.Context) bool {
	state := opsRequestStateFromContext(c)
	return state != nil && state.dedicatedErrorRecorded
}

// MarkOpsRoutingCapacityLimited records local routing capacity exhaustion on c
// so Ops classification excludes masked capacity errors from provider SLA failure.
func MarkOpsRoutingCapacityLimited(c *gin.Context) {
	if state := getOrCreateOpsRequestState(c); state != nil {
		state.routingCapacityLimited = true
	}
}

// MarkOpsRoutingCapacityLimitedIfNoAvailable marks local routing capacity
// exhaustion on c when err matches no-available-provider selection sentinels or
// messages.
func MarkOpsRoutingCapacityLimitedIfNoAvailable(c *gin.Context, err error) {
	if IsOpsNoAvailableProviderError(err) {
		MarkOpsRoutingCapacityLimited(c)
	}
}

// isOpsRoutingCapacityLimited reports whether local routing capacity exhaustion
// was recorded on c.
func isOpsRoutingCapacityLimited(c *gin.Context) bool {
	state := opsRequestStateFromContext(c)
	return state != nil && state.routingCapacityLimited
}

// IsOpsNoAvailableProviderError reports whether err matches scheduler
// no-provider sentinels or shared no-available-provider diagnostic text.
func IsOpsNoAvailableProviderError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, scheduler.ErrNoAvailableProviders) || errors.Is(err, scheduler.ErrNoAvailableCompactProviders) {
		return true
	}
	return ops.IsNoAvailableProviderMessage(err.Error())
}

// SetOpsLatencyMs stores a non-negative millisecond stage observation under key
// when c is non-nil and key is non-blank.
func SetOpsLatencyMs(c *gin.Context, key string, value int64) {
	if c == nil || strings.TrimSpace(key) == "" || value < 0 {
		return
	}
	c.Set(key, value)
}

// GetOpsLatencyMs converts a stored numeric latency observation under key to a
// non-negative int64 pointer, returning nil when absent or negative.
func GetOpsLatencyMs(c *gin.Context, key string) *int64 {
	if c == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	v, ok := c.Get(key)
	if !ok {
		return nil
	}
	var ms int64
	switch t := v.(type) {
	case int:
		ms = int64(t)
	case int32:
		ms = int64(t)
	case int64:
		ms = t
	case float64:
		ms = int64(t)
	default:
		return nil
	}
	if ms < 0 {
		return nil
	}
	return &ms
}

// MarkOpsProviderSlotAcquired freezes initial provider admission timing on the
// request stage collector when present.
func MarkOpsProviderSlotAcquired(c *gin.Context) {
	if c != nil && c.Request != nil {
		if stages := timing.StagesFromContext(c.Request.Context()); stages != nil {
			stages.Admit()
		}
	}
}

// SetOpsUpstreamModel stores a non-blank trimmed effective upstream model on c
// while leaving prior attribution intact when model is blank.
func SetOpsUpstreamModel(c *gin.Context, model string) {
	if c == nil {
		return
	}
	if model = strings.TrimSpace(model); model != "" {
		c.Set(OpsUpstreamModelKey, model)
	}
}

// ClearOpsUpstreamModel clears retained upstream model attribution on c before
// preparing another provider attempt.
func ClearOpsUpstreamModel(c *gin.Context) {
	if c != nil {
		c.Set(OpsUpstreamModelKey, "")
	}
}

// GetOpsUpstreamModel returns the trimmed effective upstream model stored on c,
// or empty text when absent.
func GetOpsUpstreamModel(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, ok := c.Get(OpsUpstreamModelKey)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// GetOpsModel returns the client model attribution stored on c, or empty text
// when absent.
func GetOpsModel(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, ok := c.Get(OpsModelKey)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// GetOpsStream reports whether streaming output was recorded on c.
func GetOpsStream(c *gin.Context) bool {
	return c != nil && c.GetBool(OpsStreamKey)
}

// GetOpsProviderID returns the positive selected provider ID stored on c and
// whether one was present.
func GetOpsProviderID(c *gin.Context) (int64, bool) {
	if c == nil {
		return 0, false
	}
	v, ok := c.Get(OpsProviderIDKey)
	if !ok {
		return 0, false
	}
	id, ok := v.(int64)
	if !ok || id <= 0 {
		return 0, false
	}
	return id, true
}

// GetOpsUpstreamStatusCode returns a stored upstream HTTP status code from int
// or int64 context values and reports whether a typed value was present.
func GetOpsUpstreamStatusCode(c *gin.Context) (int, bool) {
	if c == nil {
		return 0, false
	}
	v, ok := c.Get(OpsUpstreamStatusCodeKey)
	if !ok {
		return 0, false
	}
	switch code := v.(type) {
	case int:
		return code, true
	case int64:
		return int(code), true
	default:
		return 0, false
	}
}

// GetOpsUpstreamErrors returns the recorded upstream error event slice on c, or
// nil when absent.
func GetOpsUpstreamErrors(c *gin.Context) []*ops.OpsUpstreamErrorEvent {
	if c == nil {
		return nil
	}
	v, ok := c.Get(OpsUpstreamErrorsKey)
	if !ok {
		return nil
	}
	events, _ := v.([]*ops.OpsUpstreamErrorEvent)
	return events
}

// MarkOpsClientBusinessLimited marks c as an SLA-excluded client policy
// rejection and stores reason when non-blank.
func MarkOpsClientBusinessLimited(c *gin.Context, reason string) {
	if c == nil {
		return
	}
	c.Set(OpsClientBusinessLimitedKey, true)
	if reason = strings.TrimSpace(reason); reason != "" {
		c.Set(OpsClientBusinessLimitedReasonKey, reason)
	}
}

// HasOpsClientBusinessLimited reports whether c carries the client business
// restriction marker.
func HasOpsClientBusinessLimited(c *gin.Context) bool {
	return c != nil && c.GetBool(OpsClientBusinessLimitedKey)
}

// OpsClientBusinessLimitedReason returns the trimmed client policy rejection
// reason stored on c, or empty text when absent.
func OpsClientBusinessLimitedReason(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, ok := c.Get(OpsClientBusinessLimitedReasonKey)
	if !ok {
		return ""
	}
	reason, _ := v.(string)
	return strings.TrimSpace(reason)
}

// BeginOpsStreamTurn advances the 1-based WebSocket turn counter on c and
// clears live rule and upstream-error fields before the new turn executes,
// leaving earlier turn failure snapshots intact on opsRequestState.
func BeginOpsStreamTurn(c *gin.Context, turn int) {
	if c == nil || turn <= 0 {
		return
	}
	state := getOrCreateOpsRequestState(c)
	state.streamTurn = turn
	// Reset live per-turn rule and upstream fields so the next turn starts clean
	// while prior turn snapshots remain preserved in state.streamErrors.
	c.Set(OpsSkipPassthroughKey, false)
	c.Set(OpsUpstreamErrorsKey, []*ops.OpsUpstreamErrorEvent{})
	c.Set(OpsUpstreamStatusCodeKey, 0)
	c.Set(OpsUpstreamErrorMessageKey, "")
	c.Set(OpsUpstreamErrorDetailKey, "")
}

// MarkOpsStreamError records the first ordinary in-band failure for the current
// HTTP request or WebSocket turn without forcing SLA failure status.
func MarkOpsStreamError(c *gin.Context, errType, message string, intendedStatus int) {
	markOpsStreamError(c, OpsStreamError{
		ErrType:        errType,
		Message:        message,
		IntendedStatus: intendedStatus,
	})
}

// MarkOpsStreamFailure records an in-band failure with an explicit error code
// and SLA failure status promotion.
func MarkOpsStreamFailure(c *gin.Context, errType, code, message string, intendedStatus int) {
	markOpsStreamError(c, OpsStreamError{
		ErrType:         errType,
		Code:            code,
		Message:         message,
		IntendedStatus:  intendedStatus,
		CountTowardsSLA: true,
	})
}

// markOpsStreamError snapshots upstream attribution and retains the first
// marker for HTTP or each distinct WebSocket turn on opsRequestState.
//
// SLA-counted observations mark request failure telemetry before deduplication
// so a later SLA failure still updates telemetry even when an earlier client
// marker was already retained for the same request or turn.
func markOpsStreamError(c *gin.Context, streamErr OpsStreamError) {
	if c == nil {
		return
	}
	if streamErr.CountTowardsSLA && c.Request != nil {
		telemetry.UpdateRequest(c.Request.Context(), func(record *telemetry.RequestRecord) {
			record.State = "failed"
			record.ErrorCode = "stream_error"
		})
	}
	state := getOrCreateOpsRequestState(c)
	streamErr.ErrType = strings.TrimSpace(streamErr.ErrType)
	streamErr.Code = strings.TrimSpace(streamErr.Code)
	streamErr.Message = strings.TrimSpace(streamErr.Message)
	streamErr.SkipMonitoring = shouldSkipFinalOpsFailure(c)
	snapshotOpsStreamErrorContext(c, &streamErr)
	if GetOpenAIClientTransport(c) == OpenAIClientTransportWS {
		streamErr.Turn = state.streamTurn
		if len(state.streamErrors) > 0 && state.streamErrors[len(state.streamErrors)-1].Turn == streamErr.Turn {
			return
		}
		state.streamErrors = append(state.streamErrors, streamErr)
		if len(state.streamErrors) > maxOpsStreamErrorsPerRequest {
			state.streamErrors = append(
				[]OpsStreamError(nil),
				state.streamErrors[len(state.streamErrors)-maxOpsStreamErrorsPerRequest:]...,
			)
		}
		latest := streamErr
		state.streamError = &latest
		return
	}
	if state.streamError != nil {
		return
	}
	first := streamErr
	state.streamError = &first
}

// snapshotOpsStreamErrorContext copies current provider and upstream context
// evidence into streamErr so later WebSocket turns cannot mutate captured turns.
func snapshotOpsStreamErrorContext(c *gin.Context, streamErr *OpsStreamError) {
	if c == nil || streamErr == nil {
		return
	}
	if c.Request != nil {
		if providerID, ok := c.Request.Context().Value(telemetry.ProviderID).(int64); ok && providerID > 0 {
			streamErr.ProviderID = providerID
		}
	}
	streamErr.UpstreamModel = GetOpsUpstreamModel(c)
	if status, ok := GetOpsUpstreamStatusCode(c); ok {
		streamErr.UpstreamStatus = status
	}
	if value, ok := c.Get(OpsUpstreamErrorMessageKey); ok {
		streamErr.UpstreamMessage, _ = value.(string)
		streamErr.UpstreamMessage = strings.TrimSpace(streamErr.UpstreamMessage)
	}
	if value, ok := c.Get(OpsUpstreamErrorDetailKey); ok {
		streamErr.UpstreamDetail, _ = value.(string)
		streamErr.UpstreamDetail = strings.TrimSpace(streamErr.UpstreamDetail)
	}
	if events := GetOpsUpstreamErrors(c); events != nil {
		streamErr.UpstreamErrors = make([]*ops.OpsUpstreamErrorEvent, 0, len(events))
		for _, event := range events {
			if event == nil {
				streamErr.UpstreamErrors = append(streamErr.UpstreamErrors, nil)
				continue
			}
			// Copy each event struct so later rule or turn mutations cannot alter
			// the snapshot captured for an earlier WebSocket turn.
			copyOfEvent := *event
			streamErr.UpstreamErrors = append(streamErr.UpstreamErrors, &copyOfEvent)
		}
	}
}

// shouldSkipFinalOpsFailure reports whether monitoring is suppressed by an
// explicit request skip flag or by the final non-nil upstream error event.
func shouldSkipFinalOpsFailure(c *gin.Context) bool {
	if c == nil {
		return false
	}
	return ops.ShouldSkipMonitoringFromUpstreamEvents(
		c.GetBool(OpsSkipPassthroughKey),
		GetOpsUpstreamErrors(c),
	)
}

// GetOpsStreamError returns the current retained in-band stream failure on c
// and whether one exists.
func GetOpsStreamError(c *gin.Context) (OpsStreamError, bool) {
	state := opsRequestStateFromContext(c)
	if state == nil || state.streamError == nil {
		return OpsStreamError{}, false
	}
	return *state.streamError, true
}

// GetOpsStreamErrors returns a copy of retained WebSocket turn failures or a
// single-element slice holding the HTTP stream failure when present.
func GetOpsStreamErrors(c *gin.Context) []OpsStreamError {
	state := opsRequestStateFromContext(c)
	if state == nil {
		return nil
	}
	if len(state.streamErrors) > 0 {
		return append([]OpsStreamError(nil), state.streamErrors...)
	}
	if state.streamError != nil {
		return []OpsStreamError{*state.streamError}
	}
	return nil
}

// SetOpsUpstreamError stores positive upstream status and non-blank trimmed
// diagnostic strings on c while leaving blank or zero inputs untouched.
func SetOpsUpstreamError(c *gin.Context, upstreamStatusCode int, upstreamMessage, upstreamDetail string) {
	if c == nil {
		return
	}
	if upstreamStatusCode > 0 {
		c.Set(OpsUpstreamStatusCodeKey, upstreamStatusCode)
	}
	if msg := strings.TrimSpace(upstreamMessage); msg != "" {
		c.Set(OpsUpstreamErrorMessageKey, msg)
	}
	if detail := strings.TrimSpace(upstreamDetail); detail != "" {
		c.Set(OpsUpstreamErrorDetailKey, detail)
	}
}

// AppendOpsUpstreamError records one upstream attempt error event on c, updates
// request attempt telemetry, and evaluates passthrough monitoring suppression.
//
// Attempt correlation prefers an explicit response-bound dispatch ref before
// stage-timing ordinals, rejecting provider ID mismatches so delayed stream
// failures never overwrite a replacement provider's attempt record.
func AppendOpsUpstreamError(c *gin.Context, ev ops.OpsUpstreamErrorEvent, dispatch ...telemetry.RequestAttemptRef) {
	if c == nil {
		return
	}
	if ev.AtUnixMs <= 0 {
		ev.AtUnixMs = time.Now().UnixMilli()
	}
	ev.Platform = strings.TrimSpace(ev.Platform)
	ev.UpstreamRequestID = strings.TrimSpace(ev.UpstreamRequestID)
	ev.UpstreamResponseBody = strings.TrimSpace(ev.UpstreamResponseBody)
	ev.Kind = strings.TrimSpace(ev.Kind)
	ev.Stage = strings.TrimSpace(ev.Stage)
	ev.Scope = strings.TrimSpace(ev.Scope)
	ev.Reason = strings.TrimSpace(ev.Reason)
	ev.UpstreamURL = strings.TrimSpace(ev.UpstreamURL)
	ev.Message = strings.TrimSpace(ev.Message)
	ev.Detail = strings.TrimSpace(ev.Detail)
	if ev.Message != "" {
		ev.Message = logredact.SanitizeUpstreamQueries(ev.Message)
	}

	existing := GetOpsUpstreamErrors(c)
	if c.Request != nil {
		number := 0
		beforeDispatch := ev.Stage == string(forward.GatewayFailureStageProviderAuth) && ev.UpstreamStatusCode == 0
		bound := len(dispatch) > 0 && dispatch[0].Number > 0
		if !beforeDispatch {
			if bound {
				number = dispatch[0].Number
			} else if stages := timing.StagesFromContext(c.Request.Context()); stages != nil {
				number = int(stages.Attempt())
			} else {
				number = telemetry.FindRequestAttempt(c.Request.Context(), 0, ev.ProviderID).Number
			}
		}
		telemetry.UpdateRequest(c.Request.Context(), func(record *telemetry.RequestRecord) {
			if bound && dispatch[0].ProviderID != ev.ProviderID {
				return
			}
			outcome := "failed"
			if ev.Kind == "usage_invalid" {
				outcome = "usage_unknown"
			}
			matched := false
			providerObserved := false
			for i := range record.Attempts {
				attempt := &record.Attempts[i]
				providerObserved = providerObserved || attempt.ProviderID == ev.ProviderID
				if attempt.Number == number && attempt.ProviderID == ev.ProviderID && (number > 0 || attempt.RequestID == ev.UpstreamRequestID) {
					attempt.Outcome, attempt.Status = outcome, ev.UpstreamStatusCode
					attempt.Stage, attempt.Reason = ev.Stage, ev.Reason
					matched = true
					break
				}
			}
			if !matched && len(record.Attempts) < 256 && (beforeDispatch || !providerObserved) {
				// Record pre-dispatch credential failures without an HTTP ordinal while
				// keeping late errors for already observed providers strictly in Ops.
				record.Attempts = append(record.Attempts, telemetry.RequestAttempt{
					Number:     0,
					ProviderID: ev.ProviderID,
					Status:     ev.UpstreamStatusCode,
					Outcome:    outcome,
					Stage:      ev.Stage,
					Reason:     ev.Reason,
				})
			}
		})
	}
	evCopy := ev
	existing = append(existing, &evCopy)
	c.Set(OpsUpstreamErrorsKey, existing)
	checkSkipMonitoringForUpstreamEvent(c, &evCopy)
}

// checkSkipMonitoringForUpstreamEvent marks ev.SkipMonitoring when a bound
// error passthrough rule matches the attempt's platform, status, and body text.
func checkSkipMonitoringForUpstreamEvent(c *gin.Context, ev *ops.OpsUpstreamErrorEvent) {
	if ev.UpstreamStatusCode == 0 {
		return
	}
	svc := BoundErrorPassthroughService(c)
	if svc == nil {
		return
	}
	body := ev.Detail
	if body == "" {
		body = ev.Message
	}
	rule := svc.MatchRule(ev.Platform, ev.UpstreamStatusCode, []byte(body))
	if rule != nil && rule.SkipMonitoring {
		ev.SkipMonitoring = true
	}
}
