package timing

import (
	"context"
	"io"
	"maps"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
)

// stages.go collects HTTP text admission intervals, payload byte counts, and
// stream milestones for one logical request. Body reads and raw wire bytes are
// observed on the original reader, while decoded payload size, compression, and
// preparation sub-stages accumulate alongside admission. Admission freezes the
// initial breakdown; later provider waits and per-attempt preparation belong to
// retries rather than changing the explanation of the first accepted provider.
//
// A mutex coordinates detached upstream work with request completion and
// snapshots. Each milestone keeps its first observation, and completion freezes
// unfinished admission without inventing a successful slot acquisition. The
// collector stores durations and byte sizes rather than payloads. Optional
// response callbacks let gateway adapters observe platform framing without
// placing protocol or HTTP framework dependencies in this shared timing package.

// stageKey isolates the request collector from other timing context values.
type stageKey struct{}

// Stages owns admission counters and first-event observations for one HTTP request.
type Stages struct {
	// mu protects end, admitted, body, bodyAccounted, user, provider, retry, byte
	// counters, substages, and events. start remains immutable; end freezes initial
	// durations, while admitted decides whether later provider waits belong to
	// retries or a completed failure.
	mu            sync.Mutex               // protects counters and milestones
	start         time.Time                // monotonic ingress reference
	end           time.Time                // frozen admission endpoint
	admitted      bool                     // successful admission observed
	body          time.Duration            // original reader duration
	bodyAccounted time.Duration            // reader duration already subtracted from decode
	user          time.Duration            // initial user queue duration
	provider      time.Duration            // initial provider queue duration
	retry         time.Duration            // subsequent provider queue duration
	substages     map[string]time.Duration // pre-admission preparation sub-stages
	stream        bool                     // client streaming applicability
	events        map[string]int64         // first observed offsets and sizes
	mode          string                   // captured request metric mode
	attempt       int64                    // actual upstream exchange ordinal
	performance   *Performance             // current exchange observations under mu

	// rawBytes and rawBytesObserved track wire payload bytes delivered by the
	// wrapped request reader so zero-byte bodies remain distinct from unread streams.
	rawBytes         int64 // cumulative wire bytes read
	rawBytesObserved bool  // original reader invoked at least once

	// pendingPrepare and pendingPrepareObserved accumulate post-slot provider
	// preparation before BeginAttempt transfers the total to that exchange.
	pendingPrepare         time.Duration // current attempt preparation duration
	pendingPrepareObserved bool          // current attempt preparation recorded

	// response observes each opened upstream response without consuming it. Set
	// before sharing the context; readers invoke it synchronously on their exchange.
	response func(context.Context, *http.Response)
	// upstreamRequestListener is replaced and copied under mu, then invoked
	// outside it so downstream writers can query streaming state without deadlock.
	upstreamRequestListener func(context.Context)
	lastUpstreamActivity    atomic.Int64 // latest upstream activity UnixNano
	// upstreamHeaderWaits counts HTTP dispatches that have not returned headers
	// or an error. Do owns increments and deferred decrements across all outcomes.
	upstreamHeaderWaits atomic.Int64
}

// stageBody measures original reads without buffering or retaining request bytes.
type stageBody struct {
	io.ReadCloser         // original caller-owned body
	stages        *Stages // shared request collector
}

// WithStages installs one request collector and optional response observer.
// An existing collector is reused. Callers install observation before sharing
// the context and retain the original request start for all subsequent offsets.
// A nonnil context and nonzero start are required before upstream execution.
//
// Install before the first generation dispatch. BeginHTTPTrace binds transport
// callbacks to this collector's ingress reference and a distinct exchange ordinal,
// so late callbacks from discarded attempts cannot replace current observations.
func WithStages(ctx context.Context, start time.Time, response func(context.Context, *http.Response)) (context.Context, *Stages) {
	if s := StagesFromContext(ctx); s != nil {
		return ctx, s
	}
	s := &Stages{start: start, events: make(map[string]int64), response: response}
	s.ObserveUpstreamActivity(start)
	return context.WithValue(ctx, stageKey{}, s), s
}

// StagesFromContext returns the optional HTTP text collector from context.
func StagesFromContext(ctx context.Context) *Stages {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(stageKey{}).(*Stages)
	return s
}

// ObserveResponse records headers using the dispatch's immutable attempt reference.
// It then invokes the optional protocol observer with that same context. Neither
// operation consumes the response body, and ownership remains with the caller.
func ObserveResponse(ctx context.Context, response *http.Response) {
	if response == nil {
		return
	}
	telemetry.ObserveRequestHeaders(ctx, telemetry.RequestAttemptFromContext(ctx), response.Header, response.StatusCode)
	if s := StagesFromContext(ctx); s != nil {
		if s.response != nil {
			s.response(ctx, response)
		}
	}
}

// SetStream records client streaming applicability without changing transport behavior.
func (s *Stages) SetStream(stream bool) { s.mu.Lock(); defer s.mu.Unlock(); s.stream = stream }

// Streaming reports whether the client requested streaming HTTP text output.
func (s *Stages) Streaming() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream
}

// SetMode fixes the request's event selection before any upstream exchange starts.
// Repeated calls after execution begins are ignored so an in-flight setting
// update cannot change the meaning of a retained usage observation.
func (s *Stages) SetMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempt == 0 {
		s.mode = NormalizeMode(mode)
	}
}

// BeginAttempt creates an isolated observation set for one actual upstream exchange.
// Call before requesting a transport connection. Later callbacks carry its ordinal
// so closing an abandoned response cannot update a subsequent provider's metrics.
//
// Initial admission stays frozen at its own endpoint, and request-level downstream
// milestones retain their first observation across retries. Pending provider
// preparation recorded before this dispatch moves onto the new exchange and resets
// for later retries. All mutations use the collector mutex; callers finish one
// dispatch before beginning another, while old transport callbacks may still return.
func (s *Stages) BeginAttempt() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempt++
	values := map[string]int64{
		"attempt_number":      s.attempt,
		"upstream_started_ms": time.Since(s.start).Milliseconds(),
	}
	if s.pendingPrepareObserved {
		values["provider_prepare_ms"] = s.pendingPrepare.Milliseconds()
		s.pendingPrepare = 0
		s.pendingPrepareObserved = false
	}
	s.performance = &Performance{Mode: NormalizeMode(s.mode), Values: values}
	return s.attempt
}

// Attempt returns the current exchange ordinal for a response observer binding.
func (s *Stages) Attempt() int64 { s.mu.Lock(); defer s.mu.Unlock(); return s.attempt }

// ObserveEvent records a decoded upstream event before downstream conversion begins.
// Metadata establishes first-event timing; useful deltas advance the content interval.
// Terminal snapshots cannot extend an interval already established by streamed content,
// and final usage evidence remains independent of successful stream termination.
func (s *Stages) ObserveEvent(attempt int64, data, event string) {
	s.ObserveEventAt(attempt, data, event, time.Now())
}

// ObserveEventAt records a decoded event using its underlying read completion time.
// Events coalesced in one read share an arrival time, preventing parser CPU time
// from manufacturing a positive output interval for an otherwise single delivery.
func (s *Stages) ObserveEventAt(attempt int64, data, event string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.performance
	if p == nil || s.attempt != attempt {
		return
	}
	p.Observe(data, event, at.Sub(s.start).Milliseconds())
}

// FinishAttempt records when this exchange's reader ends or closes after failure.
// Late callbacks from earlier exchanges are ignored. Successful protocol termination
// is observed separately, so EOF alone never invents complete stream usage.
func (s *Stages) FinishAttempt(attempt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.performance == nil || s.attempt != attempt {
		return
	}
	if _, ok := s.performance.Values["upstream_finished_ms"]; !ok {
		s.performance.Values["upstream_finished_ms"] = time.Since(s.start).Milliseconds()
	}
}

// PerformanceFromContext freezes observed request and exchange data for a usage row.
// Capture only after the upstream reader has returned. The returned map is independent
// of subsequent attempts, request completion, and asynchronous persistence work.
func PerformanceFromContext(ctx context.Context) *Performance {
	if ctx == nil {
		return nil
	}
	if p, ok := ctx.Value(performanceKey{}).(*Performance); ok {
		return p.Clone()
	}
	s := StagesFromContext(ctx)
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values := s.snapshotLocked()
	if s.performance == nil {
		return nil
	}
	p := s.performance.Clone()
	maps.Copy(p.Values, values)
	return p
}

// ReadBody wraps the original request reader while preserving its Close ownership.
// Install this once before authentication can consume the body. Replaced readers
// intentionally remain unwrapped so decoding and memory replay count as processing.
func (s *Stages) ReadBody(body io.ReadCloser) io.ReadCloser {
	if body == nil {
		return nil
	}
	return &stageBody{ReadCloser: body, stages: s}
}

// Read forwards bytes and errors unchanged and records elapsed reader work plus
// raw wire byte totals. Failed and partial reads still consume time. Admission
// clipping excludes any late body activity from an already frozen initial breakdown.
//
// The underlying read runs outside the collector lock so cancellation and
// snapshot publication do not wait behind network input. The completed interval
// is clipped under that lock against admission or request completion; only its
// elapsed portion before the endpoint contributes to the original body duration.
func (r *stageBody) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := r.ReadCloser.Read(p)
	end := time.Now()
	r.stages.mu.Lock()
	if r.stages.end.IsZero() {
		if n > 0 {
			r.stages.rawBytes += int64(n)
		}
		if n > 0 || err == io.EOF {
			r.stages.rawBytesObserved = true
		}
	}
	if !r.stages.end.IsZero() && end.After(r.stages.end) {
		end = r.stages.end
	}
	if end.After(start) {
		r.stages.body += end.Sub(start)
	}
	r.stages.mu.Unlock()
	return n, err
}

// Wait adds an actual scheduler queue interval to its admission phase.
// Successful initial admission freezes the split, so subsequent provider waits
// accumulate separately. Callers report each completed wait exactly once.
//
// The admission decision and counter update share one lock. A provider wait
// reported after successful admission contributes to retry time, while a wait
// reported after failure-only completion is ignored. User admission remains a
// request-level resource, so no subsequent user wait can extend a frozen
// initial breakdown or borrow time from upstream execution.
func (s *Stages) Wait(kind string, elapsed time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == "provider" && s.admitted {
		s.retry += elapsed
		return
	}
	if !s.end.IsZero() {
		return
	}
	if kind == "user" {
		s.user += elapsed
	} else {
		s.provider += elapsed
	}
}

// Admit freezes the initial breakdown at the first accepted provider.
// Repeated calls retain the original endpoint; failure-only completion must
// never become a successful admission because detached work arrives later.
func (s *Stages) Admit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.end.IsZero() {
		s.end = time.Now()
		s.admitted = true
		s.events["provider_slot_acquired_ms"] = s.end.Sub(s.start).Milliseconds()
	}
}

// Mark retains the first request-relative observation for a stream event.
// Callers provide a recognized metric name only after their event condition
// holds. Repeated events across attempts cannot replace the first observation.
func (s *Stages) Mark(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.events[name]; !ok {
		s.events[name] = time.Since(s.start).Milliseconds()
	}
}

// Finish closes an unsuccessful admission interval at request completion.
// Successful admission retains its earlier endpoint. Repeated completion is
// harmless and does not create a provider-acquired milestone on failures.
func (s *Stages) Finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.end.IsZero() {
		s.end = time.Now()
	}
}

// Snapshot copies admission durations and event offsets for persistence.
// Whole-millisecond remainder belongs to processing so the four initial
// durations add to their endpoint. Timings never contain payloads or credentials.
//
// The snapshot owns its map independently of the collector. Missing stream
// events stay absent, while known queue counters retain numeric zero. Admission
// and failure completion use their frozen endpoint; an in-progress snapshot
// uses the current instant without freezing further observation. Each component
// is clipped to the remaining interval before calculating processing time.
func (s *Stages) Snapshot() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// snapshotLocked copies request and current-exchange observations while mu is held.
// A single lock interval prevents a retry from combining two providers' timestamps.
func (s *Stages) snapshotLocked() map[string]int64 {
	end := s.end
	if end.IsZero() {
		end = time.Now()
	}
	total := end.Sub(s.start).Milliseconds()
	body := min(s.body.Milliseconds(), total)
	user := min(s.user.Milliseconds(), total-body)
	provider := min(s.provider.Milliseconds(), total-body-user)
	out := map[string]int64{
		"timing_stream":   0,
		"timing_admitted": 0,

		"request_body_read_ms":        body,
		"preprocess_ms":               total - body - user - provider,
		"user_slot_wait_ms":           user,
		"provider_slot_wait_ms":       provider,
		"retry_provider_slot_wait_ms": s.retry.Milliseconds(),
	}
	if s.stream {
		out["timing_stream"] = 1
	}
	if s.admitted {
		out["timing_admitted"] = 1
	}
	if s.rawBytesObserved {
		out["request_raw_bytes"] = s.rawBytes
	}
	for name, elapsed := range s.substages {
		out[name] = elapsed.Milliseconds()
	}
	maps.Copy(out, s.events)
	if s.performance != nil {
		maps.Copy(out, s.performance.Values)
	} else if s.pendingPrepareObserved {
		// Retain post-slot preparation duration when a request fails before transport
		// dispatch creates an exchange performance map.
		out["provider_prepare_ms"] = s.pendingPrepare.Milliseconds()
	}
	return out
}

// FailureSnapshot returns admission and failure-location diagnostics without a
// successful stream's content interval. It never supplies usage performance or
// becomes a source for TTFT/TPS queries when analysis persistence fails.
func (s *Stages) FailureSnapshot() map[string]int64 {
	return failureTimings(s.Snapshot())
}

// AttemptFailureSnapshot preserves transport and failure location for one exchange.
// Content arrival times and usage performance never enter request-record diagnostics;
// a subsequent successful exchange therefore has one persisted performance owner.
func (s *Stages) AttemptFailureSnapshot() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.performance == nil {
		return nil
	}
	failedAt := time.Since(s.start).Milliseconds()
	if end, ok := s.performance.Values["upstream_finished_ms"]; ok {
		failedAt = end
	}
	values := map[string]int64{"failed_at_ms": failedAt}
	for _, key := range []string{"provider_prepare_ms", "upstream_request_bytes", "upstream_started_ms", "upstream_finished_ms", "upstream_get_conn_ms", "upstream_got_conn_ms", "upstream_wrote_request_ms", "upstream_first_response_byte_ms", "upstream_connection_reused", "upstream_wrote_request_error"} {
		if value, ok := s.performance.Values[key]; ok {
			values[key] = value
		}
	}
	return values
}

// Transport records an event only for the exchange that installed its callback.
// A fresh connection sequence clears earlier connection timestamps inside the same
// round trip, while counters retain retry evidence. Failed writes retain their
// diagnostic flag but cannot establish a successful request-write milestone.
//
// net/http can abandon a reused connection and retry inside one round trip.
// Connection milestones follow the replacement sequence while diagnostic counts
// retain that recovery. An exchange ordinal rejects callbacks arriving from a
// previous provider, and the mutex lets completion capture a consistent map while
// the transport reports its final events.
func (s *Stages) Transport(attempt int64, key string, at time.Time, flag bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.performance == nil || s.attempt != attempt {
		return
	}
	values := s.performance.Values
	counter := map[string]string{"upstream_get_conn_ms": "upstream_get_conn_count", "upstream_got_conn_ms": "upstream_got_conn_count", "upstream_first_response_byte_ms": "upstream_first_response_byte_count"}[key]
	if counter != "" {
		s.events[counter]++
	}
	s.events["upstream_attempt_count"] = attempt
	if key == "upstream_get_conn_ms" {
		for _, name := range []string{"upstream_get_conn_ms", "upstream_got_conn_ms", "upstream_wrote_request_ms", "upstream_first_response_byte_ms", "upstream_connection_reused"} {
			delete(values, name)
		}
	}
	if key == "upstream_got_conn_ms" {
		values["upstream_connection_reused"] = 0
		if flag {
			values["upstream_connection_reused"] = 1
		}
	}
	if key == "upstream_wrote_request_ms" && flag {
		values["upstream_wrote_request_error"] = 1
		return
	}
	if _, exists := values[key]; !exists {
		values[key] = at.Sub(s.start).Milliseconds()
	}
}

// SetContentLength records known ingress body size without reading or buffering it.
func (s *Stages) SetContentLength(length int64) {
	if length < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events["request_content_length"] = length
}

// RecordBodyDecode stores decoded body size, compression status, and pure decode
// CPU duration. It subtracts only newly accumulated wire reader time from
// totalReadAndDecode and latches positive compression across later replay reads.
func (s *Stages) RecordBodyDecode(decodedBytes int64, compressed bool, totalReadAndDecode time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.end.IsZero() {
		return
	}
	if decodedBytes >= 0 {
		s.events["request_decoded_bytes"] = decodedBytes
	}
	// Composite-key authentication can decompress and clear Content-Encoding before
	// the endpoint handler rereads restored bytes; retain an observed compressed=1.
	if compressed {
		s.events["request_compressed"] = 1
	} else if _, exists := s.events["request_compressed"]; !exists {
		s.events["request_compressed"] = 0
	}
	// Subtract only the wire-reader duration accumulated since the previous decode
	// observation so replay normalization does not re-subtract initial upload wait.
	unaccountedBody := max(time.Duration(0), s.body-s.bodyAccounted)
	s.bodyAccounted = s.body
	decodeElapsed := max(time.Duration(0), totalReadAndDecode-unaccountedBody)
	if s.substages == nil {
		s.substages = make(map[string]time.Duration)
	}
	s.substages["request_body_decode_ms"] += decodeElapsed
}

// RecordSubstage accumulates elapsed time under name while initial admission is
// still open. Calls arriving after Admit or Finish are ignored so retries do not
// rewrite pre-admission preparation diagnostics; negative durations clamp to zero.
func (s *Stages) RecordSubstage(name string, elapsed time.Duration) {
	if name == "" {
		return
	}
	if elapsed < 0 {
		elapsed = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.end.IsZero() {
		return
	}
	if s.substages == nil {
		s.substages = make(map[string]time.Duration)
	}
	s.substages[name] += elapsed
}

// RecordProviderPrepare accumulates post-slot request preparation time for the
// upcoming upstream exchange. Multiple preparation steps before BeginAttempt sum
// together, and BeginAttempt transfers that total onto the exchange's isolated map.
func (s *Stages) RecordProviderPrepare(elapsed time.Duration) {
	if elapsed < 0 {
		elapsed = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingPrepare += elapsed
	s.pendingPrepareObserved = true
}

// SetAttemptRequestBytes records the serialized upstream request body size on the
// matching active exchange. Nonmatching ordinals and negative lengths are ignored
// so background dependency calls or abandoned attempts cannot overwrite the size.
func (s *Stages) SetAttemptRequestBytes(attempt int64, length int64) {
	if attempt <= 0 || length < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.performance == nil || s.attempt != attempt {
		return
	}
	s.performance.Values["upstream_request_bytes"] = length
}

// ObserveBodyDecode records decoded body metadata on ctx's collector when present;
// missing contexts or collectors are ignored without allocation.
func ObserveBodyDecode(ctx context.Context, decodedBytes int64, compressed bool, totalReadAndDecode time.Duration) {
	if s := StagesFromContext(ctx); s != nil {
		s.RecordBodyDecode(decodedBytes, compressed, totalReadAndDecode)
	}
}

// ObserveSubstage accumulates a pre-admission preparation sub-stage on ctx's
// collector when present; missing contexts or collectors are ignored.
func ObserveSubstage(ctx context.Context, name string, elapsed time.Duration) {
	if s := StagesFromContext(ctx); s != nil {
		s.RecordSubstage(name, elapsed)
	}
}

// ObserveProviderPrepare accumulates post-slot provider preparation time on ctx's
// collector when present; missing contexts or collectors are ignored.
func ObserveProviderPrepare(ctx context.Context, elapsed time.Duration) {
	if s := StagesFromContext(ctx); s != nil {
		s.RecordProviderPrepare(elapsed)
	}
}

// SetUpstreamRequestListener registers the downstream header-wait observer.
// Passing nil removes it; callers finish outstanding dispatches before releasing
// the writer captured by a previously copied callback.
func (s *Stages) SetUpstreamRequestListener(fn func(context.Context)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upstreamRequestListener = fn
}

// listener copies the current dispatch observer under the collector mutex.
// Invoke the returned callback after releasing this lock because observers may
// query other synchronized request observations, including Streaming.
func (s *Stages) listener() func(context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upstreamRequestListener
}

// ObserveUpstreamActivity records transport activity, including upstream heartbeat bytes.
func (s *Stages) ObserveUpstreamActivity(at time.Time) { s.lastUpstreamActivity.Store(at.UnixNano()) }

// LastUpstreamActivity returns the latest transport activity for downstream stall checks.
func (s *Stages) LastUpstreamActivity() time.Time { return time.Unix(0, s.lastUpstreamActivity.Load()) }

// UpstreamHeadersPending reports whether an HTTP dispatch still awaits response headers.
// It remains true across downstream heartbeat commitment; body inactivity starts
// governing keepalives only after those transport waits have finished.
func (s *Stages) UpstreamHeadersPending() bool { return s.upstreamHeaderWaits.Load() > 0 }
