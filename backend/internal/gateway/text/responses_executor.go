package text

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// responses_executor.go runs HTTP Responses sessions and retains their
// provider, route, and latest attempt evidence. Selection capture supplies
// route identity when a Forward invocation produces its result, while later
// selection or rejection errors can leave that earlier result available.
//
// Attempts counts Forward invocations and can include a local stop before
// network dispatch. Execute binds captured request hints/routing, opens one
// session, and selects standard or Responses options using entry kind. Its
// observation wrapper forwards optional lifecycle, group-fallback, and
// retry-preparation hooks to the underlying session when supported.
// Returned attempt collections remain shared with their producer, so this
// wrapper supplies observed execution evidence rather than an independent
// completion snapshot or proof of funds persistence.

// ResponseRuntime opens request-local ResponsePorts using shared execution
// dependencies and output bindings.
type ResponseRuntime interface {
	// Open creates usable request-local ResponsePorts or returns an initialization
	// error. The runtime receives request/output references whose handling stays
	// with its session, and optional lifecycle, group-fallback, and retry hooks on
	// that session are forwarded through the executor's observation wrapper.
	Open(context.Context, execution.Request, upstream.OutputSink) (ResponsePorts, error)
}

// ResponsesExecutor selects captured entry budgets and wraps one runtime-opened
// Responses session.
type ResponsesExecutor struct {
	runtime ResponseRuntime // shared request-session runtime binding
	// standard and responses are captured option values. Execute uses responses
	// for TextOpenAIResponses and standard for the other shared text entry kinds
	// without changing those stored options.
	standard, responses ResponseOptions // standard entry options and Responses entry options
}

// responseExecutionObservation retains selection/result evidence while exposing
// ResponsePorts, its four observation overrides, and optional lifecycle,
// group-fallback, and retry-preparation forwarding. Execute passes this wrapper
// to RunResponses after every nil-error runtime Open.
//
// Because embedding an interface promotes only its declared method set, this
// wrapper explicitly implements Begin, Finish, TryGroupFallback, and
// PrepareRetry and delegates each call to the underlying dynamic session when
// present. Sessions lacking an optional hook observe a safe no-op default.
type responseExecutionObservation struct {
	// selected supplies provider/plan metadata on Forward. result retains its
	// latest attempt and invocation count, while err can later represent a
	// rejected next attempt or a selection failure.
	ResponsePorts                           // delegated declared request operations
	selected      ResponseSelection         // most recent returned candidate selection
	result        execution.ExecutionResult // most recent forwarded execution evidence
	err           error                     // latest observed execution error
}

// NewResponsesExecutor captures runtime and standard/Responses option values
// without opening a session. It performs no runtime validation; callers provide
// a usable binding before Execute uses these captured request budgets.
func NewResponsesExecutor(runtime ResponseRuntime, standard, responses ResponseOptions) *ResponsesExecutor {
	return &ResponsesExecutor{runtime, standard, responses}
}

// Execute binds request hints/routing, opens a Responses session, and returns
// observed result plus the latest error. Callers provide a nonnil context and
// usable runtime; an Open error returns zero execution evidence immediately.
//
// After Open succeeds, entry kind selects captured options and
// responseExecutionObservation is passed to the loop, forwarding optional
// lifecycle, fallback, and retry hooks supported by the session.
// Returned attempt references remain shared, and later rejection/selection
// errors can accompany the last forwarded result, so callers inspect both
// values before deciding how to handle partial work.
func (e *ResponsesExecutor) Execute(ctx context.Context, in execution.Request, sink upstream.OutputSink) (execution.ExecutionResult, error) {
	ctx = requeststate.WithExecutionHints(ctx, in.Hints)
	ctx = requeststate.WithRoutingState(ctx, in.Routing)
	session, err := e.runtime.Open(ctx, in, sink)
	if err != nil {
		return execution.ExecutionResult{}, err
	}
	options := e.standard
	if in.Text.Kind == execution.TextOpenAIResponses {
		options = e.responses
	}
	// Capture result/error observations around the opened session while forwarding
	// optional lifecycle, group-fallback, and retry hooks to the dynamic session.
	observed := &responseExecutionObservation{ResponsePorts: session}
	RunResponses(options, observed)
	return observed.result, observed.err
}

// Begin forwards request-lifecycle startup when the underlying session provides
// both Begin and Finish. Sessions without the paired lifecycle contract skip
// startup cleanly.
func (o *responseExecutionObservation) Begin() {
	if lifecycle, ok := o.ResponsePorts.(interface {
		Begin()
		Finish(bool)
	}); ok {
		lifecycle.Begin()
	}
}

// Finish forwards request-lifecycle completion with the loop's served flag when
// the underlying session provides both Begin and Finish.
func (o *responseExecutionObservation) Finish(served bool) {
	if lifecycle, ok := o.ResponsePorts.(interface {
		Begin()
		Finish(bool)
	}); ok {
		lifecycle.Finish(served)
	}
}

// TryGroupFallback delegates group recovery to the underlying session when
// supported and records ErrExecutionRejected when a handled fallback stops
// execution without an existing error.
func (o *responseExecutionObservation) TryGroupFallback(err error) (bool, bool) {
	fallback, ok := o.ResponsePorts.(interface {
		TryGroupFallback(error) (bool, bool)
	})
	if !ok {
		return false, false
	}
	handled, retry := fallback.TryGroupFallback(err)
	if handled && !retry && o.err == nil {
		o.err = ErrExecutionRejected
	}
	return handled, retry
}

// PrepareRetry forwards classified retry preparation to the underlying session
// when supported so forced-cache billing and derived context survive into the
// next attempt.
func (o *responseExecutionObservation) PrepareRetry(failure *AttemptFailure, sameProvider bool) {
	if observer, ok := o.ResponsePorts.(interface {
		PrepareRetry(*AttemptFailure, bool)
	}); ok {
		observer.PrepareRetry(failure, sameProvider)
	}
}

// CanAttempt delegates the next-attempt decision and captures cancellation when
// execution stops. A false decision with no current context error records
// ErrExecutionRejected, letting Execute return a rejection rather than apparent
// success.
func (o *responseExecutionObservation) CanAttempt() bool {
	ok := o.ResponsePorts.CanAttempt()
	if !ok {
		o.err = o.Context().Err()
		if o.err == nil {
			o.err = ErrExecutionRejected
		}
	}
	return ok
}

// Select delegates selection and captures its returned candidate and error.
// Unavailable selection with no error records ErrExecutionRejected, while the
// forwarded result stays unchanged until another Forward invocation occurs.
func (o *responseExecutionObservation) Select(excluded map[int64]struct{}) (ResponseSelection, error) {
	s, err := o.ResponsePorts.Select(excluded)
	o.selected = s
	o.err = err
	if err == nil && !s.Available {
		o.err = ErrExecutionRejected
	}
	return s, err
}

// Acquire delegates resource admission and records ErrExecutionRejected on
// false. It returns the decision unchanged, and the underlying session owns any
// resource cleanup and specific client-facing rejection.
func (o *responseExecutionObservation) Acquire() bool {
	ok := o.ResponsePorts.Acquire()
	if !ok {
		o.err = ErrExecutionRejected
	}
	return ok
}

// Forward delegates one selected attempt and captures its result, provider/plan
// metadata and invocation count. Result references are retained; Stop with no
// supplied error records ErrExecutionRejected for the caller inspecting final
// execution evidence.
func (o *responseExecutionObservation) Forward() ResponseOutcome {
	out := o.ResponsePorts.Forward()
	o.err = out.Err
	o.result.Attempts++
	o.result.Attempt = out.Attempt
	o.result.Provider = o.selected.Provider
	o.result.Plan = o.selected.Plan
	o.result.PlanProvided = o.selected.PlanProvided
	if out.Stop && o.err == nil {
		o.err = ErrExecutionRejected
	}
	return out
}
