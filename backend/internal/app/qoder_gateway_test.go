package app

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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// Qoder gateway outcomes distinguish committed HTTP output, semantic service,
// and observed usage. The scenarios exercise switching before output, partial
// errors after output, completion-only failure, and nonstream cancellation,
// using production conversion and attempt leases with controlled supplier
// streams.
//
// A blocking sink reserves the first semantic frame and waits for a test
// release signal. That makes synchronous backpressure visible before provider
// release or completion occurs, and an injected write failure still allows tail
// usage to be collected. A separate cancellable supplier waits until the test
// cancels in-flight nonstream work, while error-rule fixtures expose the final
// protocol presentation independently of execution.

// qoderErrorPassthroughRepoStub supplies fixed error rules for Qoder HTTP
// presentation tests.
type qoderErrorPassthroughRepoStub struct {
	rules []*errorpolicy.ErrorPassthroughRule // configured presentation rules
}

// fixtureClient supplies a counted stream or failure for Qoder attempt
// scenarios.
type fixtureClient struct {
	body    string        // supplied native stream
	failure error         // configured exchange failure
	calls   *atomic.Int32 // shared exchange count
}

// blockingSink blocks the first semantic output until the test releases it.
type blockingSink struct {
	// entered announces the frame selected by once; proceed releases that Emit
	// call. The test creates both channels before execution and closes proceed after intermediate release/completion assertions.
	entered, proceed chan struct{} // entered: first semantic notification; proceed: blocked emission release
	once             atomic.Bool   // first semantic claim
	// failure is returned from the selected semantic call after proceed closes.
	// Other output events succeed, so the test injects one observable downstream failure.
	failure error
}

// cancelableQoderClient announces exchange entry and waits for request
// cancellation.
type cancelableQoderClient struct {
	entered chan struct{} // supplier entry notification
}

// TestQoderGatewayErrorDetailsAppliesPassthroughRule starts a custom Qoder
// error rule before presenting an API failure. It checks mapped status,
// upstream_error type, custom message, and the Ops skip-monitoring flag.
func TestQoderGatewayErrorDetailsAppliesPassthroughRule(t *testing.T) {
	customMessage := "Use another Qoder provider"
	responseCode := http.StatusTeapot
	svc := errorpolicy.NewErrorPassthroughService(&qoderErrorPassthroughRepoStub{
		rules: []*errorpolicy.ErrorPassthroughRule{
			{
				Name:            "qoder custom",
				Enabled:         true,
				Priority:        1,
				ErrorCodes:      []int{http.StatusUnprocessableEntity},
				MatchMode:       errorpolicy.MatchModeAny,
				Platforms:       []string{capability.PlatformQoder},
				PassthroughCode: false,
				ResponseCode:    &responseCode,
				PassthroughBody: false,
				CustomMessage:   &customMessage,
				SkipMonitoring:  true,
			},
		},
	}, nil, gatewaytelemetry.ErrorRules,
	)
	require.NoError(t, svc.StartContext(context.Background()))
	t.Cleanup(svc.Stop)
	h := gatewayhttp.QoderErrorPresenter{Rules: svc, Describe: gatewayprovider.DescribeQoderError}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	status, errType, message, ok := h.Details(c, &qoder.APIError{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       `{"message":"original upstream message"}`,
		Message:    "original upstream message",
	})

	require.True(t, ok)
	require.Equal(t, responseCode, status)
	require.Equal(t, "upstream_error", errType)
	require.Equal(t, customMessage, message)
	skip, exists := c.Get(gatewayhttp.OpsSkipPassthroughKey)
	require.True(t, exists)
	require.Equal(t, true, skip)
}

// List returns the fixture's shared rule slice for error-service loading.
// Callers retain stable rules during that load; this fixture supplies
// configuration rather than implementing persistent rule mutation.
func (r *qoderErrorPassthroughRepoStub) List(context.Context) ([]*errorpolicy.ErrorPassthroughRule, error) {
	return r.rules, nil
}

// GetByID returns no rule and no error in this read-only fixture. The tested
// path loads List, so individual lookup is left without stored identity
// behavior.
func (r *qoderErrorPassthroughRepoStub) GetByID(context.Context, int64) (*errorpolicy.ErrorPassthroughRule, error) {
	return nil, nil
}

// Create returns no rule and no error without persisting fixture state.
func (r *qoderErrorPassthroughRepoStub) Create(context.Context, *errorpolicy.ErrorPassthroughRule) (*errorpolicy.ErrorPassthroughRule, error) {
	return nil, nil
}

// Update returns no rule and no error without changing configured rules.
func (r *qoderErrorPassthroughRepoStub) Update(context.Context, *errorpolicy.ErrorPassthroughRule) (*errorpolicy.ErrorPassthroughRule, error) {
	return nil, nil
}

// Delete accepts the unused fixture mutation and retains its configured rules.
func (r *qoderErrorPassthroughRepoStub) Delete(context.Context, int64) error {
	return nil
}

// StreamRequestContext counts an exchange and returns the configured failure or
// stream body. calls is required, other request arguments are ignored, and the
// caller closes the owned response reader returned on success.
func (c fixtureClient) StreamRequestContext(context.Context, *qoder.SessionContext, string, []byte, map[string]string) (*http.Response, error) {
	c.calls.Add(1)
	if c.failure != nil {
		return nil, c.failure
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(c.body))}, nil
}

// TestQoderNativeGatewayAttemptsAndCompletion runs success, pre-output failure,
// and post-output partial failure through the native loop. It checks provider
// calls and releases, exclusion during failover, one usage completion,
// success-only sticky binding, and rendered semantic content.
func TestQoderNativeGatewayAttemptsAndCompletion(t *testing.T) {
	for _, tc := range []struct {
		name         string // gateway outcome scenario
		stream       bool   // client streaming request
		firstFailure bool   // initial provider rejection
		partial      bool   // served stream failure
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
				target := &qoder.Target{ProviderID: id, Site: qoder.SiteGlobal, Session: func(context.Context) (*qoder.SessionContext, error) { return &qoder.SessionContext{}, nil }, Client: func() (qoder.StreamClient, error) { return client, nil }}
				return &gateway.Selection{Snapshot: provider.ProviderSnapshot{ID: id}, Acquired: true, Release: func() { releases.Add(1) }, Executor: executor, Input: upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIChatCompletions, Body: request.Body, Stream: tc.stream, Target: target}, Bind: func(context.Context, upstream.AttemptResult) { bindings.Add(1) }, Complete: func(_ context.Context, r upstream.AttemptResult) {
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

// TestQoderCanceledBeforeNativeGatewayDoesNotSelect cancels the context before
// starting native orchestration. It checks context.Canceled is returned and the
// configured provider selector is never invoked for that request.
func TestQoderCanceledBeforeNativeGatewayDoesNotSelect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := gateway.NewQoderUseCase(3, time.Second).Run(ctx, gateway.Request{}, gateway.RequestPorts{Select: func(context.Context, map[int64]struct{}) (*gateway.Selection, error) { called = true; return nil, nil }}, &gateway.OutputTracker{})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, called)
}

// TestQoderNativeGatewayCompletedFailureNeverRetriesSupplier records a
// completion-side sentinel after successful supplier execution. It checks the
// loop retains successful execution, saves that observation, and makes only one
// supplier call.
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

// Begin accepts the native output head before semantic backpressure is
// injected.
func (s *blockingSink) Begin(upstream.OutputHead) error { return nil }

// Emit reserves the first semantic frame and blocks that call until proceed
// closes. Callers initialize entered and proceed before output and release
// proceed when their intermediate assertions finish.
//
// CompareAndSwap selects one call to close entered, so that notification is
// issued once even if more semantic events arrive. The selected call returns
// failure after release, while other events return nil. Production execution
// calls Emit synchronously, making that selected wait observable before attempt
// completion and provider release.
func (s *blockingSink) Emit(event upstream.OutputEvent) error {
	// Claim one semantic call before signaling the test, preventing duplicate
	// channel closure while the chosen output waits for the test-controlled backpressure release.
	if event.Semantic && s.once.CompareAndSwap(false, true) {
		close(s.entered)
		<-s.proceed
		return s.failure
	}
	return nil
}

// TestQoderNativeGatewaySlowSinkAndWriteFailure blocks first semantic output
// with optional downstream failure. It checks no release or completion occurs
// before the sink proceeds, then checks one release/completion, collected tail
// usage, and the client-disconnect observation.
func TestQoderNativeGatewaySlowSinkAndWriteFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var calls, releases, completed atomic.Int32
			sink := &blockingSink{entered: make(chan struct{}), proceed: make(chan struct{})}
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
			// Run forwarding independently so the test can inspect the blocked Emit
			// call; receiving done afterward joins its result before reading final completion state.
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
			require.True(t, result.Served)
		})
	}
}

// StreamRequestContext closes entered before waiting for ctx cancellation and
// returning its error. Callers initialize a fresh channel for this single
// exchange; session, model, body, and headers are ignored by the fixture.
func (c cancelableQoderClient) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, _ []byte, _ map[string]string) (*http.Response, error) {
	// Announce exchange entry before waiting so cancellation exercises the
	// in-flight supplier path rather than terminating the request before provider dispatch begins.
	close(c.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestQoderNativeGatewayNonstreamCancellation waits until the supplier exchange
// begins before canceling its request. It checks context.Canceled, one provider
// release, and no completion recording for the canceled nonstream attempt.
func TestQoderNativeGatewayNonstreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	var releases, completed atomic.Int32
	executor := qoder.NewExecutor(qoder.ExecuteOptions{})
	ports := gateway.RequestPorts{CanFailover: func(error) bool { return true }, Select: func(context.Context, map[int64]struct{}) (*gateway.Selection, error) {
		return &gateway.Selection{Acquired: true, Release: func() { releases.Add(1) }, Executor: executor, Input: upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIChatCompletions, Body: []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), Target: &qoder.Target{Site: qoder.SiteGlobal, Session: func(context.Context) (*qoder.SessionContext, error) { return &qoder.SessionContext{}, nil }, Client: func() (qoder.StreamClient, error) { return cancelableQoderClient{entered}, nil }}}, Complete: func(context.Context, upstream.AttemptResult) { completed.Add(1) }}, nil
	}}
	// Start nonstream execution independently and wait for its supplier entry.
	// The later done receive joins that call before assertions inspect provider cleanup and completion counts.
	go func() {
		done <- gateway.NewQoderUseCase(3, time.Second).Run(ctx, gateway.Request{}, ports, &gateway.OutputTracker{Sink: gatewayhttp.ResponseSink{Writer: httptest.NewRecorder()}})
	}()
	<-entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.EqualValues(t, 1, releases.Load())
	require.Zero(t, completed.Load())
}

// TestQoderNativeGatewayLeadingUsageFailureClosesRetryWithoutSemanticOutput
// supplies usage and an error without a semantic text frame. It checks
// committed HTTP/attempt output prevents another supplier call while Semantic
// stays false and no usage completion is submitted.
func TestQoderNativeGatewayLeadingUsageFailureClosesRetryWithoutSemanticOutput(t *testing.T) {
	var calls, completed atomic.Int32
	body := `{"model":"auto","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`
	// Leading frames and usage commit HTTP output, but failure before semantic output produces no billable service.
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
