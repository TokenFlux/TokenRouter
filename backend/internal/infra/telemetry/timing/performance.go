package timing

import (
	"context"
	"maps"
	"math"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
)

// performance.go derives usage performance from one immutable observation
// snapshot. Request-relative offsets explain admission and delivery, while
// upstream comparisons use the start of the exact exchange producing usage.
// These two references prevent a failed provider or user queue from changing
// the measured latency of a later provider. Missing observations remain absent
// rather than borrowing another event or inventing a zero measurement.
//
// Completion owns a cloned snapshot until its acknowledged analytical write
// finishes. Only usage storage persists this complete value; request records
// retain lifecycle and failure diagnostics separately. TTFT follows the mode
// captured for the request, whereas TPS always uses the first and last useful
// upstream content and requires complete usage from a completed stream.
// Socket execution registers its attempt here; HTTP bridges take their ordinal
// from transport while using the same request clock and event calculations.

// FirstOutput and FirstEvent select the observed upstream event used for latency.
// The normalized default measures useful content; neither mode changes TPS.
const (
	FirstOutput = "first_output" // first useful upstream content
	FirstEvent  = "first_event"  // first non-heartbeat upstream event

	// Response content flags separate static tool identity from text and argument
	// arrivals, allowing completed snapshots to contribute fields not observed earlier.
	responseText      responseContentParts = 1 << 0 // text or reasoning content
	responseToolName  responseContentParts = 1 << 1 // explicit tool name
	responseToolInput responseContentParts = 1 << 2 // actual tool input
)

// responseContentParts identifies independently observed content within one Responses item.
type responseContentParts uint8

// Performance carries one usage-producing exchange and its request observations.
// Offsets are milliseconds from request ingress. Completion copies Values before
// publishing this value to another goroutine or retaining it in a usage row.
type Performance struct {
	// origin and onDisconnect belong to the live observer. Clone drops its
	// lifecycle callback so a stored snapshot cannot mutate request completion state.
	origin       time.Time       // runtime request clock reference
	onDisconnect func(time.Time) // active request lifecycle observer

	// Values and seenOutputParts mutate only while this exchange is observed. Clone
	// separates both maps before another goroutine retains the captured performance.
	Mode            string                          `json:"ttft_mode"`         // request mode snapshot
	Values          map[string]int64                `json:"values"`            // request-relative observations
	Stream          bool                            `json:"upstream_stream"`   // observed upstream streaming mode
	Complete        bool                            `json:"upstream_complete"` // successful upstream terminal observed
	UsageComplete   bool                            `json:"usage_complete"`    // final output usage observed
	seenOutputParts map[string]responseContentParts // observed per-item content kinds
	failed          bool                            // unsuccessful terminal already observed
}

// performanceKey carries immutable completion observations across detached contexts.
type performanceKey struct{}

// modeReaderKey carries the configured runtime reader across detached turn contexts.
type modeReaderKey struct{}

// NormalizeMode selects the supported event mode or the useful-content default.
// The first-event spelling is case insensitive and permits surrounding spaces;
// every other input selects the same default as an unconfigured gateway.
func NormalizeMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), FirstEvent) {
		return FirstEvent
	}
	return FirstOutput
}

// NewPerformance initializes one exchange using its request-relative start offset.
// Callers serialize Observe calls and clone before transferring ownership to a worker.
// Later observations belong to this instance until capture finishes.
func NewPerformance(mode string, started int64) *Performance {
	return &Performance{Mode: NormalizeMode(mode), Values: map[string]int64{"upstream_started_ms": started}}
}

// NewPerformanceAt creates an exchange with an explicit request clock reference.
// Socket relays retain this runtime-only reference while persisted offsets remain
// plain milliseconds, matching the representation used by HTTP observation.
func NewPerformanceAt(mode string, origin, started time.Time) *Performance {
	p := NewPerformance(mode, started.Sub(origin).Milliseconds())
	p.origin = origin
	return p
}

// NewRequestPerformance creates an observer using the logical request's clock.
// HTTP bridges use it when the transport owns attempt registration, then copy
// the returned response's dispatch ordinal into Values before completion.
//
// A nonzero context start takes precedence. When that value is absent, the
// captured record can supply its start; otherwise started remains the reference.
// The live disconnect callback retains the request capture,
// while Clone removes it before background completion. This constructor leaves
// attempt registration to its caller so HTTP dispatch and socket dispatch can
// share the same duration and event calculations without recording two attempts.
func NewRequestPerformance(ctx context.Context, started time.Time) *Performance {
	if ctx == nil {
		ctx = context.Background()
	}
	origin := started
	if at, ok := ctx.Value(telemetry.RequestStartedAt).(time.Time); ok && !at.IsZero() {
		origin = at
	}
	telemetry.UpdateRequest(ctx, func(record *telemetry.RequestRecord) {
		if _, ok := ctx.Value(telemetry.RequestStartedAt).(time.Time); !ok && !record.StartedAt.IsZero() {
			origin = record.StartedAt
		}
	})
	p := NewPerformanceAt(ModeFromContext(ctx), origin, started)
	p.onDisconnect = func(at time.Time) { ObserveClientDisconnect(ctx, at) }
	return p
}

// Clone isolates the mutable observation map for asynchronous completion ownership.
// Nil remains nil, preserving unobserved historical and unsupported request paths.
// Returned maps can be retained or modified independently of their source.
func (p *Performance) Clone() *Performance {
	if p == nil {
		return nil
	}
	out := *p
	out.onDisconnect = nil
	out.Values = maps.Clone(p.Values)
	out.seenOutputParts = maps.Clone(p.seenOutputParts)
	return &out
}

// TTFTMs derives upstream latency using the request's captured event selection.
// Both observations must exist in this exchange and be chronologically ordered.
// Nonstreaming exchanges have no observed first-token or first-event latency.
//
// The start and endpoint come from the same usage-producing attempt, excluding
// initial admission and earlier failures. The policy is captured at request start,
// so reading historical usage cannot reinterpret a result after an administrator
// changes the global setting. Missing endpoints remain absent even when another
// latency field is available.
func (p *Performance) TTFTMs() *int {
	if p == nil || !p.Stream {
		return nil
	}
	key := "upstream_first_output_ms"
	if NormalizeMode(p.Mode) == FirstEvent {
		key = "upstream_first_event_ms"
	}
	return p.elapsed("upstream_started_ms", key)
}

// DurationMs derives the completed or failed exchange's observed execution duration.
// A missing endpoint leaves duration absent rather than substituting request lifetime.
// Callers keep that absence when the exchange was never dispatched or observed.
func (p *Performance) DurationMs() *int {
	if p == nil {
		return nil
	}
	return p.elapsed("upstream_started_ms", "upstream_finished_ms")
}

// TPS estimates useful-content throughput from complete output usage and arrival times.
// Single-chunk output, incomplete usage and a nonpositive interval have no estimate.
// Packet coalescing and hidden reasoning remain limitations of this gateway measure.
//
// The count comes from final provider usage, while the interval excludes metadata,
// repeated terminal snapshots and waiting before the first useful event. It is an
// arrival-based estimate, not an observation of the model's internal decode clock.
// Rounding happens here so the API, CSV and spreadsheet consumers use one value.
func (p *Performance) TPS(tokens int) *float64 {
	if p == nil || !p.Stream || !p.Complete || !p.UsageComplete || tokens <= 0 || p.Values["upstream_output_event_count"] < 2 {
		return nil
	}
	duration := p.elapsed("upstream_first_output_ms", "upstream_last_output_ms")
	if duration == nil || *duration <= 0 {
		return nil
	}
	value := math.Round(float64(tokens)*10000/float64(*duration)) / 10
	return &value
}

// elapsed subtracts present ordered offsets belonging to this immutable snapshot.
// Zero is an observed duration; absent or inverted offsets return nil.
// Both lookups use the same captured exchange rather than another request or retry.
func (p *Performance) elapsed(start, end string) *int {
	a, ok := p.Values[start]
	if !ok {
		return nil
	}
	b, ok := p.Values[end]
	if !ok || b < a {
		return nil
	}
	value := int(b - a)
	return &value
}

// CopyPerformance captures observations before a completion task loses live context.
// Destination cancellation remains authoritative; mutable collector state is replaced
// by an isolated snapshot so later attempts cannot change an earlier task's usage.
func CopyPerformance(destination, source context.Context) context.Context {
	return context.WithValue(destination, performanceKey{}, PerformanceFromContext(source))
}

// WithPerformance attaches a detached exchange snapshot to its completion context.
// Socket callers supply the result captured for that logical turn; cloning here
// prevents later relay mutation from changing queued diagnostics or usage observations.
func WithPerformance(ctx context.Context, p *Performance) context.Context {
	return context.WithValue(ctx, performanceKey{}, p.Clone())
}

// Observe classifies one complete upstream event at its request-relative arrival time.
// Callers serialize mutation. Metadata, useful content, terminal success and final
// usage are independent observations, allowing partial failures to retain evidence
// without treating EOF or an error frame as a successful generation.
//
// Final usage is recognized through protocol-specific terminal or usage envelopes.
// Item identities and content kinds distinguish new fields from repeated snapshots, so
// bookkeeping at the end cannot extend the output interval. Only identities and
// numeric observations survive this call; content payloads remain borrowed input.
func (p *Performance) Observe(data, event string, now int64) {
	if p == nil {
		return
	}
	p.Stream = true
	v := gjson.Parse(data)
	wrappedBody := false
	// Qoder envelopes carry the original chat payload as a JSON string. Classify
	// that payload at arrival, before any downstream converter emits generated events.
	if inner := v.Get("body"); inner.Type == gjson.String && inner.String() != "" {
		wrappedBody = true
		data = inner.String()
		v = gjson.Parse(data)
	}
	// Qoder's event envelopes keep the protocol body under data. Use the outer
	// event label only when normalizing that known wrapper representation.
	envelopeKind := ""
	if wrapped := v.Get("event").String(); wrappedBody && wrapped != "" {
		event, envelopeKind = wrapped, wrapped
		if v.Get("data").IsObject() {
			data = v.Get("data").Raw
			v = gjson.Parse(data)
		}
		if wrapped == "content_block_delta" {
			event, envelopeKind = "qoder_content_delta", "qoder_content_delta"
		}
	}
	if data != "[DONE]" && !gjson.Valid(data) {
		return
	}
	kind := v.Get("type").String()
	if envelopeKind != "" {
		kind = envelopeKind
	} else if kind == "" {
		kind = event
	}
	terminal := data == "[DONE]" || kind == "message_stop" || kind == "response.completed" || kind == "response.done"
	if v.Get("candidates.0.finishReason").String() != "" || v.Get("response.candidates.0.finishReason").String() != "" {
		terminal = true
	}

	usageOnly := !v.Get("choices").Exists() || len(v.Get("choices").Array()) == 0
	chatFinished := false
	finalMessage := false
	for _, choice := range v.Get("choices").Array() {
		finished := nonemptyString(choice.Get("finish_reason")) && strings.TrimSpace(choice.Get("finish_reason").String()) != ""
		chatFinished = chatFinished || finished
		finalMessage = finalMessage || wrappedBody && finished && content(choice.Get("message"))
	}
	// Chat readers accept finish reasons and final usage without a DONE sentinel.
	// Incomplete counters still leave TPS unavailable even after a finish reason.
	chatOutput := v.Get("usage.completion_tokens")
	chatUsage := usageOnly && chatOutput.Type == gjson.Number && chatOutput.Int() >= 0 && (kind == "" || v.Get("choices").IsArray())
	terminal = terminal || chatFinished || chatUsage || finalMessage
	if terminal {
		p.Complete = !p.failed
		if _, recorded := p.Values["upstream_completed_ms"]; !recorded {
			p.Values["upstream_completed_ms"] = now
		}
	}
	finalUsage := terminal || p.Complete || kind == "message_delta" || ((usageOnly || chatFinished) && (v.Get("usage.completion_tokens").Exists() || wrappedBody && v.Get("usage.output_tokens").Exists()))
	for _, key := range []string{"usage.output_tokens", "usage.completion_tokens", "response.usage.output_tokens", "response.usage.completion_tokens", "usageMetadata.candidatesTokenCount", "response.usageMetadata.candidatesTokenCount"} {
		if value := v.Get(key); finalUsage && value.Exists() && value.Type == gjson.Number && value.Int() >= 0 {
			p.UsageComplete = true
		}
	}
	if !DataEvent(data, event) || data == "[DONE]" {
		return
	}
	if _, ok := p.Values["upstream_first_event_ms"]; !ok {
		p.Values["upstream_first_event_ms"] = now
	}
	status := v.Get("response.status").String()
	if kind == "error" || kind == "response.failed" || kind == "response.incomplete" || kind == "response.cancelled" || kind == "response.canceled" || (terminal && (status == "failed" || status == "incomplete" || status == "cancelled" || status == "canceled")) {
		p.failed = true
		p.Complete = false
		return
	}
	useful := UsefulOutput(data, event)
	if strings.HasPrefix(kind, "response.") {
		useful = p.observeResponseContent(v, kind, useful)
	} else if finalMessage && p.Values["upstream_output_event_count"] > 0 {
		// Qoder final messages repeat the accumulated Chat output. Its incremental
		// deltas have already established the content interval before this snapshot arrives.
		useful = false
	}
	if event == "qoder_content_delta" {
		useful = nonemptyString(v.Get("text"))
	}
	if kind == "tool_use_start" || kind == "tool_use_delta" || kind == "tool_call_delta" {
		useful = nonemptyString(v.Get("name")) || nonemptyString(v.Get("tool_name")) || toolInput(v.Get("arguments")) || toolInput(v.Get("input")) || nonemptyString(v.Get("partial_json"))
	}
	if useful {
		if _, ok := p.Values["upstream_first_output_ms"]; !ok {
			p.Values["upstream_first_output_ms"] = now
		}
		p.Values["upstream_last_output_ms"] = now
		p.Values["upstream_output_event_count"]++
	}
}

// observeResponseContent distinguishes new Responses fields from completed echoes.
// Incremental deltas always advance their content interval; full item and response
// snapshots contribute only content kinds that were not already observed. A tool
// name therefore cannot suppress the first arrival of that tool's actual input.
//
// Completed responses can contain several items, so every item updates its own
// evidence even after another item qualifies this event. The caller counts the
// entire event once, and these flags retain no generated content or persistence data.
func (p *Performance) observeResponseContent(v gjson.Result, kind string, useful bool) bool {
	if !useful {
		return false
	}
	if output := v.Get("response.output"); output.IsArray() {
		fresh := false
		for _, item := range output.Array() {
			observed := p.observeResponseParts(item.Get("id").String(), responseItemParts(item), false)
			fresh = fresh || observed
		}
		return fresh
	}
	if item := v.Get("item"); item.IsObject() {
		return p.observeResponseParts(item.Get("id").String(), responseItemParts(item), false)
	}
	parts := responseText
	if strings.Contains(kind, "function_call_arguments") || strings.Contains(kind, "custom_tool_call_input") {
		parts = responseToolInput
	}
	return p.observeResponseParts(v.Get("item_id").String(), parts, strings.HasSuffix(kind, ".delta"))
}

// responseItemParts selects meaningful text, tool name and input fields in one item.
// Empty input placeholders contribute no input flag. Other item types use the
// shared content predicate, keeping signatures, image artifacts and metadata excluded.
func responseItemParts(item gjson.Result) responseContentParts {
	if kind := item.Get("type").String(); kind == "function_call" || kind == "custom_tool_call" {
		var parts responseContentParts
		if nonemptyString(item.Get("name")) {
			parts |= responseToolName
		}
		if toolInput(item.Get("arguments")) || toolInput(item.Get("input")) {
			parts |= responseToolInput
		}
		return parts
	}
	if content(item) {
		return responseText
	}
	return 0
}

// observeResponseParts remembers which content fields arrived for an output item.
// A full snapshot advances timing only when it introduces another kind of content;
// deltas advance on every call because repeated text can represent additional tokens.
//
// Some compatible payloads omit item IDs. An unidentified snapshot compares the
// fields observed across the exchange, while an identified item can reuse earlier
// anonymous evidence. This conservative association avoids extending the interval
// merely because an otherwise repeated terminal gained or lost its identifier.
func (p *Performance) observeResponseParts(item string, parts responseContentParts, incremental bool) bool {
	if parts == 0 {
		return false
	}
	if p.seenOutputParts == nil {
		p.seenOutputParts = make(map[string]responseContentParts)
	}
	seen := p.seenOutputParts[item] | p.seenOutputParts[""]
	if item == "" {
		for _, previous := range p.seenOutputParts {
			seen |= previous
		}
	}
	p.seenOutputParts[item] |= parts
	return incremental || parts&^seen != 0
}

// WithModeReader installs the global setting reader before request authorization.
// Streaming turns call ModeFromContext once at admission; the collector stores that
// returned value so later configuration changes cannot alter its measurement.
func WithModeReader(ctx context.Context, reader func(context.Context) string) context.Context {
	return context.WithValue(ctx, modeReaderKey{}, reader)
}

// ModeFromContext reads the current policy for a newly accepted request or turn.
// Missing contexts or readers select first useful output; supplied readers are
// normalized before their result becomes the request snapshot.
func ModeFromContext(ctx context.Context) string {
	if ctx != nil {
		if read, ok := ctx.Value(modeReaderKey{}).(func(context.Context) string); ok && read != nil {
			return NormalizeMode(read(ctx))
		}
	}
	return FirstOutput
}

// BeginPerformance registers a socket generation before its request is written.
// The request capture supplies identity and lifecycle origin, while each dispatch
// gets another ordinal even when the provider or logical turn is unchanged.
//
// Registration precedes frame dispatch so interrupted writes remain diagnosable.
// The live observer retains a request-lifecycle callback; Clone discards it before
// background completion owns the numeric snapshot. Retries reuse the request origin
// while allocating another ordinal. Without either context or capture origin, the
// dispatch time becomes the reference, and no request record is fabricated.
func BeginPerformance(ctx context.Context, providerID int64, started time.Time) *Performance {
	p := NewRequestPerformance(ctx, started)
	number := int64(1)
	telemetry.UpdateRequest(ctx, func(record *telemetry.RequestRecord) {
		for _, previous := range record.Attempts {
			number = max(number, int64(previous.Number)+1)
		}
		if len(record.Attempts) < 256 {
			at := started
			record.Attempts = append(record.Attempts, telemetry.RequestAttempt{Number: int(number), ProviderID: providerID, StartedAt: &at, Outcome: "running"})
		}
	})
	p.Values["attempt_number"] = number
	return p
}

// ObserveAt converts one socket frame's arrival to its request-relative offset.
// Nil observers are ignored. Callers serialize this observation with other changes
// to the same exchange and supply timestamps from its runtime clock.
func (p *Performance) ObserveAt(data, event string, at time.Time) {
	if p != nil {
		p.Observe(data, event, at.Sub(p.origin).Milliseconds())
	}
}

// FinishAt freezes the socket exchange endpoint before asynchronous completion capture.
// Nil observers are ignored, and repeated completion retains the first endpoint so
// later resource cleanup cannot extend an already observed exchange.
func (p *Performance) FinishAt(at time.Time) {
	if p == nil {
		return
	}
	if _, recorded := p.Values["upstream_finished_ms"]; !recorded {
		p.Values["upstream_finished_ms"] = at.Sub(p.origin).Milliseconds()
	}
}

// ObserveClientDisconnect records the earliest downstream stop independently of drain.
// Request completion retains this time even when upstream accounting continues, while
// a prior explicit failure remains the request's terminal classification.
func ObserveClientDisconnect(ctx context.Context, at time.Time) {
	telemetry.UpdateRequest(ctx, func(record *telemetry.RequestRecord) {
		if record.ClientDisconnectedAt == nil {
			record.ClientDisconnectedAt = &at
		}
		if record.State != "failed" {
			record.State = "canceled"
		}
	})
}

// Delivered records useful socket content only after a successful complete frame write.
// WebSocket has no HTTP Flush milestone; its write observation cannot fabricate one.
func (p *Performance) Delivered(data string, at time.Time) {
	if p == nil || !UsefulOutput(data, "") {
		return
	}
	if _, exists := p.Values["first_visible_output_ms"]; !exists {
		p.Values["first_visible_output_ms"] = at.Sub(p.origin).Milliseconds()
	}
}

// Disconnected reports a socket delivery failure before its completed usage is captured.
// Only the live observer retains a lifecycle callback; cloned snapshots carry the
// immutable fact and cannot mutate the request from a background storage worker.
func (p *Performance) Disconnected(at time.Time) {
	if p == nil {
		return
	}
	p.Values["client_disconnected"] = 1
	if p.onDisconnect != nil {
		p.onDisconnect(at)
	}
}

// FailureTimings selects admission and transport evidence for a failed usage write.
// It excludes content arrival times, event counts and TTFT policy so diagnostics
// cannot become a duplicate performance source. Nil observations remain absent.
func (p *Performance) FailureTimings() map[string]int64 {
	if p == nil {
		return nil
	}
	return failureTimings(p.Values)
}

// failureTimings copies the bounded stage set needed to locate unsuccessful work.
// Both early request failures and background write failures use this selection;
// returned maps are independent of the live collector and frozen usage snapshots.
func failureTimings(values map[string]int64) map[string]int64 {
	out := make(map[string]int64)
	for _, key := range []string{"request_content_length", "timing_stream", "timing_admitted", "request_body_read_ms", "preprocess_ms", "user_slot_wait_ms", "provider_slot_wait_ms", "retry_provider_slot_wait_ms", "provider_slot_acquired_ms", "attempt_number", "upstream_started_ms", "upstream_finished_ms", "upstream_get_conn_ms", "upstream_got_conn_ms", "upstream_wrote_request_ms", "upstream_first_response_byte_ms", "upstream_connection_reused", "upstream_wrote_request_error"} {
		if value, ok := values[key]; ok {
			out[key] = value
		}
	}
	return out
}
