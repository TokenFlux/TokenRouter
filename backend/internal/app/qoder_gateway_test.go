package app_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
)

// fixtureClient 只替代供应商网络，真实平台转换、网关循环和 Lease 均照常运行。
type fixtureClient struct {
	body    string
	failure error
	calls   *atomic.Int32
}

func (c fixtureClient) StreamRequestContext(context.Context, *qoder.SessionContext, string, []byte, map[string]string) (*http.Response, error) {
	c.calls.Add(1)
	if c.failure != nil {
		return nil, c.failure
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(c.body))}, nil
}

const successfulQoderStream = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"served\\\"}}]}\"}\n\ndata: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":12,\\\"completion_tokens\\\":3}}\"}\n\ndata: {\"body\":\"[DONE]\"}\n\n"
const qoderFailureFrame = "data: {\"body\":\"{\\\"code\\\":\\\"500\\\",\\\"message\\\":\\\"fixture failure\\\"}\",\"statusCodeValue\":502}\n\n"

func TestQoderNativeGatewayAttemptsAndCompletion(t *testing.T) {
	for _, tc := range []struct {
		name         string
		stream       bool
		firstFailure bool
		partial      bool
	}{
		{name: "nonstream-success"}, {name: "stream-success", stream: true}, {name: "pre-output-failover", stream: true, firstFailure: true}, {name: "post-output-error", stream: true, partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, releases, completions, bindings atomic.Int32
			executor := qoder.NewExecutor(qoder.ExecuteOptions{})
			body := fmt.Sprintf(`{"model":"auto","stream":%t,"messages":[{"role":"user","content":"hi"}]}`, tc.stream)
			request := gateway.Request{Access: &apikey.AccessSnapshot{KeyID: 7}, UserID: 3, Concurrency: 1, Stream: tc.stream, Body: []byte(body), Model: "auto"}
			ports := gateway.RequestPorts{CanFailover: func(error) bool { return true }}
			selectedCount := 0
			ports.Select = func(_ context.Context, excluded map[int64]struct{}) (*gateway.Selection, error) {
				selectedCount++
				id := int64(selectedCount)
				client := fixtureClient{body: successfulQoderStream, calls: &calls}
				if tc.firstFailure && selectedCount == 1 {
					client.failure = &qoder.APIError{StatusCode: 503, Message: "fixture busy"}
				}
				if tc.partial {
					client.body = strings.Replace(successfulQoderStream, "data: {\"body\":\"[DONE]\"}\n\n", qoderFailureFrame, 1)
				}
				if selectedCount > 1 {
					_, ok := excluded[1]
					require.True(t, ok)
				}
				target := &qoder.Target{AccountID: id, Site: qoder.SiteGlobal, UserType: "personal_standard", Session: func(context.Context) (*qoder.SessionContext, error) { return &qoder.SessionContext{}, nil }, Client: func() (qoder.StreamClient, error) { return client, nil }}
				return &gateway.Selection{Snapshot: account.AccountSnapshot{ID: id}, Acquired: true, Release: func() { releases.Add(1) }, Executor: executor, Input: upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIChatCompletions, Body: request.Body, Stream: tc.stream, Target: target}, Bind: func(context.Context, upstream.AttemptResult) { bindings.Add(1) }, Complete: func(_ context.Context, r upstream.AttemptResult) {
					completions.Add(1)
					require.Equal(t, 12, r.Usage.InputTokens)
					require.Equal(t, 3, r.Usage.OutputTokens)
				}}, nil
			}
			rec := httptest.NewRecorder()
			output := &gateway.OutputTracker{Sink: gatewayhttp.ResponseSink{Writer: rec}}
			err := gateway.NewQoderUseCase(3, time.Second).Run(context.Background(), request, ports, output)
			if tc.partial {
				require.Error(t, err)
				require.Zero(t, bindings.Load())
			} else {
				require.NoError(t, err)
				require.EqualValues(t, 1, bindings.Load())
			}
			expected := int32(1)
			if tc.firstFailure {
				expected = 2
			}
			require.Equal(t, expected, calls.Load())
			require.Equal(t, expected, releases.Load())
			require.EqualValues(t, 1, completions.Load())
			require.Contains(t, rec.Body.String(), "served")
		})
	}
}

func TestQoderCanceledBeforeNativeGatewayDoesNotSelect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := gateway.NewQoderUseCase(3, time.Second).Run(ctx, gateway.Request{}, gateway.RequestPorts{Select: func(context.Context, map[int64]struct{}) (*gateway.Selection, error) { called = true; return nil, nil }}, &gateway.OutputTracker{})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, called)
}

func TestQoderNativeGatewayCompletedFailureNeverRetriesSupplier(t *testing.T) {
	var calls atomic.Int32
	errSentinel := errors.New("completion store unavailable")
	var saved error
	executor := qoder.NewExecutor(qoder.ExecuteOptions{})
	input := upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIChatCompletions, Body: []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), Target: &qoder.Target{Site: qoder.SiteGlobal, Session: func(context.Context) (*qoder.SessionContext, error) { return &qoder.SessionContext{}, nil }, Client: func() (qoder.StreamClient, error) {
		return fixtureClient{body: successfulQoderStream, calls: &calls}, nil
	}}}
	ports := gateway.RequestPorts{Select: func(context.Context, map[int64]struct{}) (*gateway.Selection, error) {
		return &gateway.Selection{Acquired: true, Executor: executor, Input: input, Complete: func(context.Context, upstream.AttemptResult) { saved = errSentinel }}, nil
	}}
	err := gateway.NewQoderUseCase(3, time.Second).Run(context.Background(), gateway.Request{}, ports, &gateway.OutputTracker{Sink: gatewayhttp.ResponseSink{Writer: httptest.NewRecorder()}})
	require.NoError(t, err)
	require.ErrorIs(t, saved, errSentinel)
	require.EqualValues(t, 1, calls.Load())
}

// s09BlockingSink 模拟同步背压；下一帧必须等待当前写入结束。
type s09BlockingSink struct {
	entered, proceed chan struct{}
	once             atomic.Bool
	failure          error
}

func (s *s09BlockingSink) Begin(upstream.OutputHead) error { return nil }
func (s *s09BlockingSink) Emit(event upstream.OutputEvent) error {
	if event.Semantic && s.once.CompareAndSwap(false, true) {
		close(s.entered)
		<-s.proceed
		return s.failure
	}
	return nil
}
func TestQoderNativeGatewaySlowSinkAndWriteFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var calls, releases, completed atomic.Int32
			sink := &s09BlockingSink{entered: make(chan struct{}), proceed: make(chan struct{})}
			if fail {
				sink.failure = errors.New("fixture downstream closed")
			}
			done := make(chan error, 1)
			executor := qoder.NewExecutor(qoder.ExecuteOptions{})
			var result upstream.AttemptResult
			ports := gateway.RequestPorts{Select: func(context.Context, map[int64]struct{}) (*gateway.Selection, error) {
				return &gateway.Selection{Acquired: true, Release: func() { releases.Add(1) }, Executor: executor, Input: upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIChatCompletions, Stream: true, Body: []byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`), Target: &qoder.Target{Site: qoder.SiteGlobal, Session: func(context.Context) (*qoder.SessionContext, error) { return &qoder.SessionContext{}, nil }, Client: func() (qoder.StreamClient, error) {
					return fixtureClient{body: successfulQoderStream, calls: &calls}, nil
				}}}, Complete: func(_ context.Context, r upstream.AttemptResult) { result = r; completed.Add(1) }}, nil
			}}
			go func() {
				done <- gateway.NewQoderUseCase(3, time.Second).Run(context.Background(), gateway.Request{Stream: true}, ports, &gateway.OutputTracker{Sink: sink})
			}()
			select {
			case <-sink.entered:
			case <-time.After(time.Second):
				t.Fatal("未产生渐进语义输出")
			}
			require.EqualValues(t, 1, calls.Load())
			require.Zero(t, releases.Load())
			require.Zero(t, completed.Load())
			close(sink.proceed)
			require.NoError(t, <-done)
			require.EqualValues(t, 1, releases.Load())
			require.EqualValues(t, 1, completed.Load())
			require.Equal(t, fail, result.ClientDisconnect)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.NotNil(t, result.FirstSemanticOutput)
		})
	}
}

type s09CancelableQoderClient struct{ entered chan struct{} }

func (c s09CancelableQoderClient) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, _ []byte, _ map[string]string) (*http.Response, error) {
	close(c.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestQoderNativeGatewayNonstreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	var releases, completed atomic.Int32
	executor := qoder.NewExecutor(qoder.ExecuteOptions{})
	ports := gateway.RequestPorts{CanFailover: func(error) bool { return true }, Select: func(context.Context, map[int64]struct{}) (*gateway.Selection, error) {
		return &gateway.Selection{Acquired: true, Release: func() { releases.Add(1) }, Executor: executor, Input: upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIChatCompletions, Body: []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), Target: &qoder.Target{Site: qoder.SiteGlobal, Session: func(context.Context) (*qoder.SessionContext, error) { return &qoder.SessionContext{}, nil }, Client: func() (qoder.StreamClient, error) { return s09CancelableQoderClient{entered}, nil }}}, Complete: func(context.Context, upstream.AttemptResult) { completed.Add(1) }}, nil
	}}
	go func() {
		done <- gateway.NewQoderUseCase(3, time.Second).Run(ctx, gateway.Request{}, ports, &gateway.OutputTracker{Sink: gatewayhttp.ResponseSink{Writer: httptest.NewRecorder()}})
	}()
	<-entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.EqualValues(t, 1, releases.Load())
	require.Zero(t, completed.Load())
}

func TestQoderNativeGatewayLeadingUsageFailureClosesRetryWithoutSemanticOutput(t *testing.T) {
	var calls, completed atomic.Int32
	body := `{"model":"auto","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`
	// 只有前导和 usage；随后报错必须保留已提交 HTTP，不能视为已发生可结算服务。
	_, tail, _ := strings.Cut(successfulQoderStream, "\n\n")
	frames := strings.Replace(tail, "data: {\"body\":\"[DONE]\"}\n\n", qoderFailureFrame, 1)
	target := &qoder.Target{Site: qoder.SiteGlobal, Session: func(context.Context) (*qoder.SessionContext, error) { return &qoder.SessionContext{}, nil }, Client: func() (qoder.StreamClient, error) { return fixtureClient{body: frames, calls: &calls}, nil }}
	ports := gateway.RequestPorts{CanFailover: func(error) bool { return true }, Select: func(context.Context, map[int64]struct{}) (*gateway.Selection, error) {
		return &gateway.Selection{Acquired: true, Executor: qoder.NewExecutor(qoder.ExecuteOptions{}), Input: upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIChatCompletions, Body: []byte(body), Stream: true, Target: target}, Complete: func(context.Context, upstream.AttemptResult) { completed.Add(1) }}, nil
	}}
	output := &gateway.OutputTracker{Sink: gatewayhttp.ResponseSink{Writer: httptest.NewRecorder()}}
	err := gateway.NewQoderUseCase(3, time.Second).Run(context.Background(), gateway.Request{Stream: true}, ports, output)
	require.Error(t, err)
	require.True(t, output.HTTPCommitted)
	require.True(t, output.AttemptCommitted)
	require.False(t, output.Semantic)
	require.EqualValues(t, 1, calls.Load())
	require.Zero(t, completed.Load())
}
