package text

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// ResponseRuntime 在构造时绑定一次，Open 只创建当前请求状态。
type ResponseRuntime interface {
	Open(context.Context, execution.Request, upstream.OutputSink) (ResponsePorts, error)
}
type ResponsesExecutor struct {
	runtime             ResponseRuntime
	standard, responses ResponseOptions
}

func NewResponsesExecutor(runtime ResponseRuntime, standard, responses ResponseOptions) *ResponsesExecutor {
	return &ResponsesExecutor{runtime, standard, responses}
}
func (e *ResponsesExecutor) Execute(ctx context.Context, in execution.Request, sink upstream.OutputSink) (execution.ExecutionResult, error) {
	session, err := e.runtime.Open(ctx, in, sink)
	if err != nil {
		return execution.ExecutionResult{}, err
	}
	options := e.standard
	if in.Text.Kind == execution.TextOpenAIResponses {
		options = e.responses
	}
	observed := &responseExecutionObservation{ResponsePorts: session}
	RunResponses(options, observed)
	return observed.result, observed.err
}

type responseExecutionObservation struct {
	ResponsePorts
	selected ResponseSelection
	result   execution.ExecutionResult
	err      error
}

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
func (o *responseExecutionObservation) Select(excluded map[int64]struct{}) (ResponseSelection, error) {
	s, err := o.ResponsePorts.Select(excluded)
	o.selected = s
	o.err = err
	if err == nil && !s.Available {
		o.err = ErrExecutionRejected
	}
	return s, err
}
func (o *responseExecutionObservation) Acquire() bool {
	ok := o.ResponsePorts.Acquire()
	if !ok {
		o.err = ErrExecutionRejected
	}
	return ok
}
func (o *responseExecutionObservation) Forward() ResponseOutcome {
	out := o.ResponsePorts.Forward()
	o.err = out.Err
	o.result.Attempts++
	o.result.Attempt = out.Attempt
	o.result.Account = o.selected.Account
	o.result.Plan = o.selected.Plan
	o.result.PlanProvided = o.selected.PlanProvided
	if out.Stop && o.err == nil {
		o.err = ErrExecutionRejected
	}
	return out
}
