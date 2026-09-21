package text

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// MessageRuntime 是构造时绑定的单步依赖集合；Open 仅建立本次状态，不组装业务回调。
// 适配会话继续实现原 MessagePorts，唯一账号循环仍由 RunMessages 拥有。
type MessageRuntime interface {
	Open(context.Context, execution.Request, upstream.OutputSink) (MessagePorts, error)
}
type MessagesExecutor struct {
	runtime          MessageRuntime
	messages, gemini MessageOptions
}

func NewMessagesExecutor(runtime MessageRuntime, messages, gemini MessageOptions) *MessagesExecutor {
	return &MessagesExecutor{runtime: runtime, messages: messages, gemini: gemini}
}

// Execute 接管本次执行及结果观测，HTTP 不再取得 ports 或调用 RunMessages。
func (e *MessagesExecutor) Execute(ctx context.Context, in execution.Request, sink upstream.OutputSink) (execution.ExecutionResult, error) {
	ctx = requeststate.WithExecutionHints(ctx, in.Hints)
	ctx = requeststate.WithRoutingState(ctx, in.Routing)
	session, err := e.runtime.Open(ctx, in, sink)
	if err != nil {
		return execution.ExecutionResult{}, err
	}
	options := e.messages
	if in.Text.Kind == execution.TextGeminiMessages || in.Text.AlternateBudget {
		options = e.gemini
	}
	options.HasBoundSession = in.Text.HasBoundSession
	observed := &messageExecutionObservation{MessagePorts: session}
	RunMessages(options, observed)
	return observed.result, observed.err
}

// ErrExecutionRejected 表示前置之后的同步操作已拒绝请求，具体 HTTP 错误已由输出适配器写出。
var ErrExecutionRejected = errors.New("text execution rejected by request operation")

type messageExecutionObservation struct {
	selected Selection
	MessagePorts
	result execution.ExecutionResult
	err    error
}

func (o *messageExecutionObservation) PrepareAttempt() bool {
	ok := o.MessagePorts.PrepareAttempt()
	if !ok {
		o.err = ErrExecutionRejected
	}
	return ok
}
func (o *messageExecutionObservation) Select(excluded map[int64]struct{}) (Selection, error) {
	s, err := o.MessagePorts.Select(excluded)
	if err != nil {
		o.err = err
	} else {
		o.selected = s
		o.err = nil
	}
	return s, err
}
func (o *messageExecutionObservation) Acquire() bool {
	ok := o.MessagePorts.Acquire()
	if !ok {
		o.err = ErrExecutionRejected
	}
	return ok
}
func (o *messageExecutionObservation) Forward(state AttemptState) Outcome {
	out := o.MessagePorts.Forward(state)
	o.result.Account = o.selected.Account
	o.result.Plan = o.selected.Plan
	o.result.PlanProvided = o.selected.PlanProvided
	o.result.Attempts++
	o.result.Attempt = out.Attempt
	o.err = out.Err
	if out.Stop && o.err == nil {
		o.err = ErrExecutionRejected
	}
	return out
}
func (o *messageExecutionObservation) Canceled() {
	o.err = o.Context().Err()
	o.MessagePorts.Canceled()
}
