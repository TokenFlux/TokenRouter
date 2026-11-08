package upstream

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// attempt.go defines platform execution inputs and the result observations
// returned to gateway callers.
// A platform attempt receives a selected target and reports the evidence
// observed during that one exchange. The gateway consumes output commitment
// and usage separately when deciding provider switching, request completion,
// and settlement after an execution error.
// Media executors attach the originating HTTP dispatch before
// reading the body. Finish can then record failures that return no billable
// result, while later completion can identify retries with absent response IDs.
//
// Diagnostic outcome and accounting evidence serve separate consumers. Finish
// closes the referenced exchange and freezes performance, while observed tokens,
// served output, and commitment flags remain available to gateway policy. Partial
// output can therefore retain its usage after an error, and a path without a
// performance collector still records its diagnostic terminal state.

// ExecutionTarget identifies a provider through the concrete target accepted
// by its executor.
type ExecutionTarget interface {
	// TargetID returns the selected provider identifier for this execution target.
	TargetID() int64
}

// AttemptInput supplies the protocol and selected platform target for one exchange.
type AttemptInput struct {
	Protocol protocol.ProtocolID // requested upstream protocol identifier
	// Body retains the supplied payload storage. Keep its bytes stable while
	// the executor or a retained request consumer reads them.
	Body          []byte // shared request payload bytes
	ResponseModel string // client-facing response model name
	Stream        bool   // requested streaming output mode
	// Target needs the concrete target type accepted by the chosen executor.
	// A matching provider ID alone does not configure its required callbacks.
	Target ExecutionTarget // selected platform execution target
}

// AttemptResult carries output, usage, and failure observations from a platform exchange.
// Executors can return partial evidence together with an error; the gateway
// interprets completion eligibility and retry policy for its request protocol.
type AttemptResult struct {
	RequestAttempt telemetry.RequestAttemptRef `json:"-"` // originating diagnostic dispatch reference
	// UpstreamResponseModel records the model declared before client-facing
	// conversion. Empty means this attempt supplied no retained model declaration.
	UpstreamResponseModel string // observed vendor model name
	// HTTPCommitted and RetryCommitted describe different output decisions.
	// A gateway keepalive may commit HTTP headers while leaving provider switching
	// available until an output event closes the attempt's retry window.
	// HTTPCommitted: HTTP output started. RetryCommitted: retry window closed.
	HTTPCommitted, RetryCommitted bool
	RequestID                     string // upstream request correlation identifier
	ResponseID                    string // upstream Responses object identifier
	// SearchCount: observed search operations. ImageInputTokens: image input tokens.
	ImageCacheReadTokens          int // explicit cached image token subset
	SearchCount, ImageInputTokens int
	// ImageOutputSizes retains per-image size evidence for completion pricing.
	// Some executors share the reader's slice, so copy before independent mutation.
	ImageOutputSizes []string // observed image size labels
	// UpstreamHeaders retains response headers from the platform exchange.
	// Some executors share the response map; copy before changing retained values.
	UpstreamHeaders http.Header // observed upstream response headers
	// Model and UpstreamModel retain the client-facing model and selected vendor
	// model respectively. UpstreamResponseModel separately records a declaration
	// found in response content and can differ from either requested name.
	// Model: client-facing model name. UpstreamModel: selected vendor model.
	Model, UpstreamModel string
	Usage                TokenUsage // extracted protocol token usage
	// HasUsage, Served, Stream, and ClientDisconnect retain independent evidence:
	// observed usage, served output, requested streaming, and a failed client write.
	// Completion rules interpret these flags alongside errors for the active protocol.
	// HasUsage: usage evidence present. Served: output served by executor.
	// Stream: streaming output requested. ClientDisconnect: client write failed.
	HasUsage, Served, Stream, ClientDisconnect bool
	// EstimatedTokenCount carries the local countTokens estimate when available.
	// Settlement consumes Usage separately and decides whether this estimate is usable.
	EstimatedTokenCount *int // optional local token estimate
	ImageFrameCount     int  // encoded output frame billing units
	// ObservedImages counts upstream image results before pricing defaults are applied.
	// Completion combines this observation with transport format and failure state.
	ObservedImages int // observed upstream image count
	// AudioUsage retains observed quantities for audio pricing and completion.
	// The pointed value can remain shared with the platform reader until completion.
	AudioUsage *protocol.AudioUsage // observed audio usage quantities
	// MediaBody retains bounded media JSON for output and completion inspection.
	// Treat the shared byte storage as immutable while either consumer reads it.
	MediaBody    []byte        // retained media response bytes
	FailureClass string        // platform failure classification label
	Cancelled    bool          // execution reported request cancellation
	Duration     time.Duration // elapsed attempt execution time

	// Timings carries the shared collector's observations for this exchange.
	// Nil means no collector was installed; completion clones recorded maps before asynchronous use.
	Timings *timing.Performance // frozen exchange performance observations
	// ServiceTier: recorded service tier. ReasoningEffort: recorded reasoning effort.
	ServiceTier, ReasoningEffort string
}

// Executor performs one selected provider exchange and reports its observed outcome.
// @project-doc docs/architecture/gateway_request_lifecycle.md#upstream_attempt_ownership
type Executor interface {
	// Execute consumes the target type required by its platform and emits through
	// the supplied sink. Inspect returned observations even on error because
	// output or usage can precede the failure.
	Execute(context.Context, AttemptInput, OutputSink) (AttemptResult, error)
}

// TokenUsage aliases the protocol token counts consumed by attempt completion.
type TokenUsage = protocol.TokenUsage

// Finish finalizes the exchange's diagnostic outcome and freezes its performance.
// A conversion error makes final usage ineligible for throughput. A client
// disconnect can still retain complete upstream usage collected by bounded drain. Missing
// performance collectors remain absent; a captured dispatch still receives its
// terminal outcome. Call after body processing finishes, including error returns.
// Request lifecycle and billing ownership remain with their entrypoint and completion.
func (r *AttemptResult) Finish(ctx context.Context, failure error) {
	if r == nil {
		return
	}
	telemetry.ObserveRequestResult(ctx, r.RequestAttempt, failure)
	r.Timings = timing.PerformanceFromContext(ctx)
	if r.Timings == nil {
		return
	}
	if failure != nil && !r.ClientDisconnect {
		r.Timings.UsageComplete = false
	}
	if !r.Timings.Stream {
		r.Timings.Complete = failure == nil
		r.Timings.UsageComplete = failure == nil && r.HasUsage
	}
}

// CaptureRequestAttempt attaches the response's immutable dispatch to this result.
// Call as soon as the transport returns, before body reading can fail. Missing
// response request metadata leaves the reference empty; Finish requires that
// fixed reference to close the same diagnostic exchange on success or failure.
func (r *AttemptResult) CaptureRequestAttempt(response *http.Response) {
	r.RequestAttempt = telemetry.RequestAttemptFromResponse(response)
}
