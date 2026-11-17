package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/requestcontext"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// Executor fixtures exercise request cancellation and response identity without
// making a live Qoder call. The context probe distinguishes client disconnect
// from internal abort, while a fixed stream feeds every protocol and output mode
// to check caller-selected model names.

// contextTestClient probes the context passed into started inference for
// cancellation fixtures.
type contextTestClient struct {
	check func(context.Context) error // inference context probe
}

// modelTestClient returns the fixed stream used to inspect response model
// identity.
type modelTestClient struct{}

// modelTestSink retains serialized protocol output in a fixture-owned bytes
// buffer.
type modelTestSink struct {
	bytes.Buffer // captured protocol output
}

// StreamRequestContext passes the inference context to a usable configured
// probe and returns its error. The cancellation fixture supplies that callback;
// this client produces no response body to consume.
func (c contextTestClient) StreamRequestContext(ctx context.Context, _ *SessionContext, _ string, _ []byte, _ map[string]string) (*http.Response, error) {
	return nil, c.check(ctx)
}

// TestStreamExecutionInternalAbort begins inference for Chat, Responses, and
// Messages with a server abort signal. Client cancellation leaves the execution
// context live, while internal abort cancels it and propagates context.Canceled.
func TestStreamExecutionInternalAbort(t *testing.T) {
	for _, wire := range []protocol.ProtocolID{protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolOpenAIResponses, protocol.ProtocolAnthropicMessages} {
		t.Run(string(wire), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, cancelClient := context.WithCancel(context.Background())
				request, abort := requestcontext.WithAbort(client)
				defer abort()
				called := false
				target := &Target{
					ProviderID: 1,
					Session:    func(context.Context) (*SessionContext, error) { return &SessionContext{}, nil },
					Client: func() (StreamClient, error) {
						return contextTestClient{check: func(ctx context.Context) error {
							called = true
							// Client cancellation must reach its request context before the
							// assertion, while the retained internal abort remains the execution
							// context's independent stop source.
							cancelClient()
							synctest.Wait()
							require.NoError(t, ctx.Err())
							abort()
							synctest.Wait()
							require.ErrorIs(t, ctx.Err(), context.Canceled)
							return ctx.Err()
						}}, nil
					},
				}
				_, err := NewExecutor(ExecuteOptions{}).Execute(request, upstream.AttemptInput{
					Target: target, Protocol: wire, Stream: true,
					Body: []byte(`{"model":"auto","input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":64}`),
				}, &modelTestSink{})
				require.True(t, called)
				require.ErrorIs(t, err, context.Canceled)
			})
		})
	}
}

// StreamRequestContext returns a fresh response carrying the fixed text and
// usage stream for model-reporting fixtures. The executor owns consumption and
// closure of this independently allocated response body.
func (modelTestClient) StreamRequestContext(context.Context, *SessionContext, string, []byte, map[string]string) (*http.Response, error) {
	// The fixture uses JSON-supported strings, numbers, and container values.
	// Marshaling therefore supplies its request or response representation before
	// the client behavior is exercised.
	body, _ := json.Marshal(QoderSSEWrapper{Body: `{"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`})
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: " + string(body) + "\n\ndata: [DONE]\n\n"))}, nil
}

// Begin accepts response metadata for the in-memory model-output fixture.
func (*modelTestSink) Begin(upstream.OutputHead) error { return nil }

// Emit appends event bytes to the fixture buffer and returns its write error.
func (s *modelTestSink) Emit(e upstream.OutputEvent) error { _, err := s.Write(e.Data); return err }

// TestQoderDoesNotReportSynthesizedModel serves a caller alias through all three
// protocols in JSON and stream modes. It checks visible alias output and an
// empty upstream response-model observation.
func TestQoderDoesNotReportSynthesizedModel(t *testing.T) {
	for _, wire := range []protocol.ProtocolID{protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolAnthropicMessages} {
		for _, stream := range []bool{false, true} {
			t.Run(string(wire)+map[bool]string{false: "/json", true: "/stream"}[stream], func(t *testing.T) {
				executor := NewExecutor(ExecuteOptions{})
				target := &Target{ProviderID: 1, Session: func(context.Context) (*SessionContext, error) { return &SessionContext{}, nil }, Client: func() (StreamClient, error) { return modelTestClient{}, nil }}
				sink := &modelTestSink{}
				result, err := executor.Execute(context.Background(), upstream.AttemptInput{Target: target, Protocol: wire, Body: []byte(`{"model":"auto","input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":64}`), ResponseModel: "client-alias", Stream: stream}, sink)
				require.NoError(t, err)
				require.Contains(t, sink.String(), "client-alias")
				require.Empty(t, result.UpstreamResponseModel)
			})
		}
	}
}

// TestResponseStoragePreference exercises both response writers with an explicit
// opt-out. A successful first turn remains billable and its identifier cannot
// recover submitted text or assistant output on a later partial continuation.
func TestResponseStoragePreference(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, store := range []bool{false, true} {
			executor := NewExecutor(ExecuteOptions{})
			target := &Target{
				ProviderID: 1, Metadata: RequestMetadata{APIKeyID: 7},
				Session: func(context.Context) (*SessionContext, error) { return &SessionContext{}, nil },
				Client:  func() (StreamClient, error) { return modelTestClient{}, nil },
			}
			body, err := json.Marshal(map[string]any{"model": "auto", "input": "first turn", "store": store, "stream": stream})
			require.NoError(t, err)
			result, err := executor.Execute(t.Context(), upstream.AttemptInput{Target: target, Protocol: protocol.ProtocolOpenAIResponses, Body: body, Stream: stream}, &modelTestSink{})
			require.NoError(t, err)
			require.True(t, result.Served)
			require.NotEmpty(t, result.RequestID)
			restored, err := executor.conversations.RestoreResponseHistory(target.Metadata, QoderPayloadRequest{PreviousResponseID: result.RequestID, Messages: []QoderMessage{{Role: "user", Text: "next"}}})
			if store {
				require.NoError(t, err)
				require.Equal(t, "first turn", restored.Messages[0].Text)
			} else {
				var invalid *RequestError
				require.ErrorAs(t, err, &invalid)
				require.Equal(t, "previous_response_unavailable", invalid.Code)
				require.Zero(t, executor.conversations.responseHistoryBytes)
			}
		}
	}
}
