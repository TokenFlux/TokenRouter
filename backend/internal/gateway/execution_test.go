package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

// fixedExecutionRuntime 使用同一固定实例记录实际调用，验证请求状态不被实例复用污染。
type fixedExecutionRuntime struct {
	prepares  atomic.Int64
	checks    atomic.Int64
	completed atomic.Int64
	released  atomic.Int64
	execute   func(context.Context, upstream.AttemptInput, upstream.OutputSink) (upstream.AttemptResult, error)
}

func (r *fixedExecutionRuntime) Prepare(_ context.Context, request Request) (Request, error) {
	r.prepares.Add(1)
	return request, nil
}
func (r *fixedExecutionRuntime) Check(context.Context, Request, bool) error {
	r.checks.Add(1)
	return nil
}
func (r *fixedExecutionRuntime) Select(_ context.Context, request Request, _ map[int64]struct{}) (*Selection, error) {
	return &Selection{Acquired: true, Snapshot: account.AccountSnapshot{ID: request.UserID}, Input: upstream.AttemptInput{Body: request.Body, ResponseModel: request.Model}, Executor: fixedExecutionFunc(r.execute), Release: func() { r.released.Add(1) }, Complete: func(context.Context, upstream.AttemptResult) { r.completed.Add(1) }}, nil
}
func (*fixedExecutionRuntime) CanRefresh(error) bool      { return false }
func (*fixedExecutionRuntime) CanFailover(error) bool     { return false }
func (*fixedExecutionRuntime) RefreshPending(error) bool  { return false }
func (*fixedExecutionRuntime) QueueFailure(string, error) {}

type fixedExecutionFunc func(context.Context, upstream.AttemptInput, upstream.OutputSink) (upstream.AttemptResult, error)

func (f fixedExecutionFunc) Execute(ctx context.Context, in upstream.AttemptInput, out upstream.OutputSink) (upstream.AttemptResult, error) {
	return f(ctx, in, out)
}

type discardExecutionOutput struct{}

func (discardExecutionOutput) Begin(upstream.OutputHead) error { return nil }
func (discardExecutionOutput) Emit(upstream.OutputEvent) error { return nil }

func TestExecuteRetainsPartialFailureAndCompletesOnce(t *testing.T) {
	upstreamErr := errors.New("upstream failed after output")
	runtime := &fixedExecutionRuntime{execute: func(_ context.Context, in upstream.AttemptInput, out upstream.OutputSink) (upstream.AttemptResult, error) {
		require.NoError(t, out.Emit(upstream.OutputEvent{Data: []byte("partial"), CommitForRetry: true, Semantic: true}))
		return upstream.AttemptResult{Model: in.ResponseModel, Served: true, HasUsage: true, Usage: upstream.TokenUsage{OutputTokens: 3}}, upstreamErr
	}}
	core := NewQoderExecutor(3, time.Second, nil, runtime)
	result, err := core.Execute(context.Background(), Request{UserID: 7, Model: "model", Stream: true}, discardExecutionOutput{})
	require.ErrorIs(t, err, upstreamErr)
	require.Equal(t, 1, result.Attempts)
	require.Equal(t, int64(7), result.Account.ID)
	require.Equal(t, 3, result.Attempt.Usage.OutputTokens)
	require.Equal(t, int64(1), runtime.completed.Load())
	require.Equal(t, int64(1), runtime.released.Load())
}

func TestExecuteCanceledBeforePreparation(t *testing.T) {
	runtime := &fixedExecutionRuntime{}
	core := NewQoderExecutor(3, time.Second, nil, runtime)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := core.Execute(ctx, Request{}, discardExecutionOutput{})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result.Attempts)
	require.Zero(t, runtime.prepares.Load())
	require.Zero(t, runtime.checks.Load())
}

func TestExecuteFixedDependenciesKeepRequestsIndependent(t *testing.T) {
	runtime := &fixedExecutionRuntime{execute: func(_ context.Context, in upstream.AttemptInput, _ upstream.OutputSink) (upstream.AttemptResult, error) {
		return upstream.AttemptResult{Model: in.ResponseModel, RequestID: string(in.Body)}, nil
	}}
	core := NewQoderExecutor(3, time.Second, nil, runtime)
	var workers sync.WaitGroup
	for _, model := range []string{"first", "second"} {
		workers.Go(func() {
			result, err := core.Execute(context.Background(), Request{Model: model, Body: []byte(model)}, discardExecutionOutput{})
			if err != nil {
				t.Error(err)
				return
			}
			if result.Attempt.Model != model || result.Attempt.RequestID != model {
				t.Errorf("request changed: %+v", result)
			}
		})
	}
	workers.Wait()
	require.Equal(t, int64(2), runtime.completed.Load())
	require.Equal(t, int64(2), runtime.released.Load())
}
