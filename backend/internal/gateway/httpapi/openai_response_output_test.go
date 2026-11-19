package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	selectionadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	responseupstream "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// Responses streams can carry metadata before usable output and can lose
// client delivery before terminal usage arrives. These fixtures keep upstream
// reads, client writes and forwarding results separately observable, so tests
// distinguish a recoverable failure before output from a committed stream or
// partial result that still carries usage.
//
// Staged readers publish waiting signals before receiving segment gates and
// retain short-read remainders across sequential reads. Their Close method
// leaves gate release to the test. Asynchronous forwarding publishes result
// and error on separate buffered channels, which callers drain after releasing
// the required gates. Other fixtures count source closure separately from a
// guarded close notification, allowing timeout assertions to observe cleanup
// without treating that notification as proof that every delegated close
// succeeded.

// grokObservationErrorReader returns the configured failure after a preceding
// MultiReader payload ends.
type grokObservationErrorReader struct {
	err error // configured fixture read failure
}

// firstOutputCloseTrackingBody signals body closure while delegating the
// source read/close operations.
type firstOutputCloseTrackingBody struct {
	// ReadCloser supplies source reading and closure. Close forwards every call
	// while once guards the separate notification used by timeout assertions.
	io.ReadCloser
	// closed and once publish the fixture close signal at most once; callers
	// supply an open channel, while underlying ReadCloser.Close still runs for
	// every close invocation independently of that guarded notification.
	closed chan struct{} // body-close notification channel
	once   sync.Once     // close notification synchronization guard
}

// stagedOpenAISSEReadCloser supplies gated byte segments for controlled
// stream-flush interleavings.
type stagedOpenAISSEReadCloser struct {
	// segments, gates, waiting and index define one sequential read schedule.
	// Before each selected segment, Read publishes waiting and receives gates
	// at that index; current retains any segment remainder until later reads.
	// segments retains the supplied byte slices; callers keep each segment
	// unchanged until sequential reading has consumed it.
	segments [][]byte          // borrowed fixture segment bytes
	gates    []<-chan struct{} // per-segment admission gate channels
	waiting  []chan struct{}   // per-segment wait notification channels
	// eofReached is closed when segments are exhausted and then cleared.
	// Callers supply an open channel and leave this publication to Read.
	eofReached chan struct{} // segment-exhaustion notification channel
	current    []byte        // unconsumed current segment bytes
	index      int           // next segment index
}

// openAIResponseFlushReadError supplies one prefix copy before a terminal read
// failure.
type openAIResponseFlushReadError struct {
	// payload, err and sent describe one prefix followed by failure. Read marks
	// the first copy attempt before copying and later uses err, with unexpected
	// EOF selected when no error was configured for that tail.
	payload []byte // first fixture read bytes
	err     error  // configured fixture read failure
	sent    bool   // first-copy attempt already performed
}

// cancelReadCloser supplies an immediate cancellation result for stream-reader
// fixtures.
type cancelReadCloser struct{}

// errReadCloser supplies an immediate configured failure for upstream stream
// fixtures.
type errReadCloser struct {
	err error // configured fixture read failure
}

// streamSelectionDiagnosticSource supplies fixture provider and group values
// for post-stream scheduler diagnostics.
type streamSelectionDiagnosticSource struct {
	// value and group supply stable diagnostic fixtures during inspection. The
	// lookup methods copy their outer structs while nested maps, slices or
	// pointers retain storage shared with the stored values.
	value gatewayprovider.ExecutionProvider // provider value for diagnostic lookup
	group routing.Group                     // group value for diagnostic lookup
}

// Read returns zero bytes and the configured trailing fixture failure.
func (r grokObservationErrorReader) Read([]byte) (int, error) { return 0, r.err }

// TestGrokNativeObservationRetainsPartialUsageAfterReadError follows visible
// text/usage with a configured failure. It checks partial usage,
// served/commit/semantic observations and retained client text rather than an
// observation-only result.
func TestGrokNativeObservationRetainsPartialUsageAfterReadError(t *testing.T) {
	failure := errors.New("fixture truncated stream")
	payload := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"visible\",\"usage\":{\"input_tokens\":9,\"output_tokens\":2}}\n\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 470, Platform: capability.PlatformGrok, Type: capability.ProviderTypeAPIKey}}
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(io.MultiReader(strings.NewReader(payload), grokObservationErrorReader{failure}))}
	service := newResponseOutputForTest(OpenAIResponseOptions{})
	result, err := service.ReadStreamObservation(context.Background(), response, c, provider, time.Now(), "grok-fixture", "grok-fixture", "")
	require.Error(t, err)
	require.Contains(t, recorder.Body.String(), "visible")
	require.NotNil(t, result)
	require.True(t, result.Served)
	require.True(t, result.HasUsage)
	require.True(t, result.HttpCommitted)
	require.True(t, result.Served)
	require.False(t, result.ObservedOnly)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
}

// TestOpenAIStreamPairedFailureAppliesProviderSideEffectsOnce follows content
// with paired error and failed events carrying the same access-state evidence.
// It checks one error-store call in native and passthrough fixtures.
func TestOpenAIStreamPairedFailureAppliesProviderSideEffectsOnce(t *testing.T) {
	const upstream = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
		"data: {\"type\":\"error\",\"error\":{\"status_code\":403,\"code\":\"workspace_suspended\",\"message\":\"workspace is suspended\"}}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failed\",\"error\":{\"status_code\":403,\"code\":\"workspace_suspended\",\"message\":\"workspace is suspended\"}}}\n\n"

	t.Run("native", func(t *testing.T) {
		repo := &openAIStream403ProviderRepo{}
		svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, corrector: responseupstream.NewCodexToolCorrector(), health: newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil)})
		provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 921, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}}
		recorder := newOpenAIResponseFlushRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(upstream)),
		}

		result, err := svc.Output.ReadStreamObservation(context.Background(), resp, c, provider, time.Now(), "gpt-5", "gpt-5", "")

		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 1, repo.setErrorCalls)
	})

	t.Run("passthrough", func(t *testing.T) {
		repo := &openAIStream403ProviderRepo{}
		svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, health: newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil)})
		provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 922, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		writer := &passthroughFlushTestWriter{
			ResponseWriter:  c.Writer,
			recorder:        recorder,
			failAfterWrites: -1,
		}
		c.Writer = writer
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(upstream)),
		}

		result, err := responseupstream.ReadPassthroughStreaming(context.Background(), resp, upstreamcore.NewOutputContext(ResponseSink{Writer: c.Writer}), svc.Output.PassthroughOptions(context.Background(), c, provider), time.Now(), "gpt-5", "gpt-5")

		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 1, repo.setErrorCalls)
	})
}

// TestOpenAIStreamMetadataPreambleAndMessageOnlyOverloadFailOver supplies
// large metadata and empty reasoning before overload wording. It checks
// transient retry/client classification and no committed native or passthrough
// output.
func TestOpenAIStreamMetadataPreambleAndMessageOnlyOverloadFailOver(t *testing.T) {
	largeMetadata := strings.Repeat("x", 16*1024)
	stream := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_1","metadata":{"padding":"` + largeMetadata + `"}}}`,
		"",
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","item":{"type":"reasoning","summary":[]}}`,
		"",
		"event: response.reasoning_summary_part.added",
		`data: {"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":""}}`,
		"",
		"event: error",
		`data: {"type":"error","error":{"type":"service_unavailable_error","message":"Our servers are currently overloaded. Please try again later."}}`,
		"",
	}, "\n")

	tests := []struct {
		name string // fixture case name
		// selected fixture stream reader
		run func(*OpenAIResponsesExecutor, *gin.Context, *http.Response, *gatewayprovider.ExecutionProvider) error
	}{
		{
			name: "native",
			run: func(svc *OpenAIResponsesExecutor, c *gin.Context, resp *http.Response, provider *gatewayprovider.ExecutionProvider) error {
				_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, provider, time.Now(), "model", "model", "")
				return err
			},
		},
		{
			name: "passthrough",
			run: func(svc *OpenAIResponsesExecutor, c *gin.Context, resp *http.Response, provider *gatewayprovider.ExecutionProvider) error {
				_, err := responseupstream.ReadPassthroughStreaming(c.Request.Context(), resp, upstreamcore.NewOutputContext(ResponseSink{Writer: c.Writer}), svc.Output.PassthroughOptions(c.Request.Context(), c, provider), time.Now(), "model", "model")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Response: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize}}})
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(stream)),
				Header:     http.Header{"X-Request-Id": []string{"rid-message-only-overload"}},
			}
			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Name: "acc"}}

			err := tt.run(svc, c, resp, provider)
			require.Error(t, err)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.RetryableOnSameProvider)
			require.True(t, failoverErr.RequestScopedTransient)
			require.Equal(t, http.StatusServiceUnavailable, failoverErr.ClientStatusCode)
			require.Contains(t, failoverErr.ClientMessage, "servers are currently overloaded")
			require.False(t, c.Writer.Written())
			require.Empty(t, rec.Body.String())
		})
	}
}

// TestOpenAIStreamCapacityShedErrorFramePrecedingFailedStillFailsOver supplies
// metadata, overload error and failed terminal before content. It checks typed
// same-provider transient recovery and absence of downstream commitment or
// bytes.
func TestOpenAIStreamCapacityShedErrorFramePrecedingFailedStillFailsOver(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"},"sequence_number":0}`,
			"",
			"event: response.in_progress",
			`data: {"type":"response.in_progress","response":{"id":"resp_1"},"sequence_number":1}`,
			"",
			"event: error",
			`data: {"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."},"sequence_number":2}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}},"sequence_number":3}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-shed-error-then-failed"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameProvider)
	require.True(t, failoverErr.RequestScopedTransient)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

// TestOpenAIStreamCapacityShedAfterOutputRewritesCodeForClient follows text
// with paired overload events. It checks one server_error failed terminal,
// retained message/content and suppression logging, with no provider recovery
// wrapper.
func TestOpenAIStreamCapacityShedAfterOutputRewritesCodeForClient(t *testing.T) {
	// Capture shared logging while inspecting this suppression diagnostic, then
	// restore the hook so later tests use their normal sink.
	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"partial"}`,
			"",
			"event: error",
			`data: {"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."},"sequence_number":2}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}},"sequence_number":3}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-shed-after-output"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))

	body := rec.Body.String()
	require.Contains(t, body, "partial")
	require.NotContains(t, body, "event: error")
	require.Equal(t, 1, strings.Count(body, "event: response.failed"))
	require.Equal(t, 1, strings.Count(body, `"code":"server_error"`))
	require.Contains(t, body, `"code":"server_error"`)
	require.NotContains(t, body, "server_is_overloaded")
	require.Contains(t, body, "Our servers are currently overloaded")
	require.True(t, logSink.ContainsMessage("gateway.failover_suppressed_after_semantic_output"))
	require.True(t, logSink.ContainsFieldValue("path", "native_sse"))
	require.True(t, logSink.ContainsFieldValue("upstream_request_id", "rid-shed-after-output"))
}

// TestHandleNonStreamingResponse_CompactClientStreamBridgesToSSE returns
// Compact JSON for stream-marked admission. It checks compaction/completed
// events, response identity and returned token usage under the bridged SSE
// format.
func TestHandleNonStreamingResponse_CompactClientStreamBridgesToSSE(t *testing.T) {
	svc := newCompactBridgeTestService()
	c, rec := newCompactBridgeTestContext(t, true)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_compact_json",
			"object":"response",
			"model":"gpt-5.1-codex",
			"status":"completed",
			"output":[{"id":"cmp_1","type":"compaction","status":"completed","encrypted_content":"compact-payload"}],
			"usage":{"input_tokens":9,"output_tokens":4,"total_tokens":13}
		}`)),
	}

	result, err := svc.NonStream(context.Background(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.ProviderTypeOAuth}}, "gpt-5.5", "gpt-5.5")
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	events := httptestkit.ParseCompactSSE(t, rec.Body.String())
	require.Len(t, events, 2)
	require.Equal(t, "response.output_item.done", events[0][0])
	require.Equal(t, "compaction", gjson.Get(events[0][1], "item.type").String())
	require.Equal(t, "response.completed", events[1][0])
	require.Equal(t, "resp_compact_json", gjson.Get(events[1][1], "response.id").String())

	// The parsed usage and response ID remain available in the forwarding
	// result while the following assertions inspect separately framed client output.
	require.NotNil(t, result.Usage)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, "resp_compact_json", result.ResponseID)
}

// TestHandleNonStreamingResponse_PathBasedCompactStaysJSON returns Compact
// JSON without client-stream marking. It checks JSON response/compaction
// identity and absence of event-stream content type for that fixture.
func TestHandleNonStreamingResponse_PathBasedCompactStaysJSON(t *testing.T) {
	svc := newCompactBridgeTestService()
	c, rec := newCompactBridgeTestContext(t, false)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_compact_json",
			"output":[{"id":"cmp_1","type":"compaction","encrypted_content":"compact-payload"}],
			"usage":{"input_tokens":9,"output_tokens":4,"total_tokens":13}
		}`)),
	}

	result, err := svc.NonStream(context.Background(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.ProviderTypeOAuth}}, "gpt-5.5", "gpt-5.5")
	require.NoError(t, err)
	require.NotNil(t, result)

	require.NotContains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	body := rec.Body.String()
	require.Equal(t, "resp_compact_json", gjson.Get(body, "id").String())
	require.Equal(t, "compaction", gjson.Get(body, "output.0.type").String())
}

// TestHandleSSEToJSON_CompactClientStreamBridgesToSSE supplies a completed
// Compact response in upstream SSE. It checks two downstream bridged events
// with ciphertext, compaction item and the final response identifier.
func TestHandleSSEToJSON_CompactClientStreamBridgesToSSE(t *testing.T) {
	svc := newCompactBridgeTestService()
	c, rec := newCompactBridgeTestContext(t, true)
	upstreamSSE := `data: {"type":"response.completed","response":{"id":"resp_compact_sse","object":"response","model":"gpt-5.1-codex","status":"completed","output":[{"id":"cmp_sse_1","type":"compaction","status":"completed","encrypted_content":"compact-sse-payload"}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}` + "\n" + ""
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}

	result, err := svc.NonStream(context.Background(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.ProviderTypeOAuth}}, "gpt-5.5", "gpt-5.5")
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	events := httptestkit.ParseCompactSSE(t, rec.Body.String())
	require.Len(t, events, 2)
	require.Equal(t, "response.output_item.done", events[0][0])
	require.Equal(t, "compact-sse-payload", gjson.Get(events[0][1], "item.encrypted_content").String())
	require.Equal(t, "response.completed", events[1][0])
	require.Equal(t, "resp_compact_sse", gjson.Get(events[1][1], "response.id").String())
}

// TestHandleSSEToJSON_CompactRawOutputItemDoneRepairsEmptyTerminalOutput
// supplies a raw compaction-summary item before empty terminal output. It
// checks retained identity/ciphertext/summary/opaque fields, supplemented
// terminal output and observed usage.
func TestHandleSSEToJSON_CompactRawOutputItemDoneRepairsEmptyTerminalOutput(t *testing.T) {
	svc := newCompactBridgeTestService()
	c, rec := newCompactBridgeTestContext(t, true)
	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"cmp_1","type":"compaction_summary","status":"completed","summary":[{"type":"summary_text","text":"compact summary"}],"encrypted_content":"compact-payload","opaque":{"kept":true}}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_compact","object":"response","model":"gpt-5.1-codex","status":"completed","output":[],"usage":{"input_tokens":9,"output_tokens":4,"total_tokens":13}}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}

	result, err := svc.NonStream(context.Background(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.ProviderTypeOAuth}}, "gpt-5.5", "gpt-5.5")
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	events := httptestkit.ParseCompactSSE(t, rec.Body.String())
	require.Len(t, events, 2)
	require.Equal(t, "response.output_item.done", events[0][0])
	item := gjson.Get(events[0][1], "item")
	require.Equal(t, "compaction_summary", item.Get("type").String())
	require.Equal(t, "cmp_1", item.Get("id").String())
	require.Equal(t, "compact-payload", item.Get("encrypted_content").String())
	require.Equal(t, "compact summary", item.Get("summary.0.text").String())
	require.True(t, item.Get("opaque.kept").Bool(), "raw item 字段必须逐字节保留")
	require.Equal(t, "response.completed", events[1][0])
	require.Equal(t, "resp_compact", gjson.Get(events[1][1], "response.id").String())
	require.Len(t, gjson.Get(events[1][1], "response.output").Array(), 1)
	require.Equal(t, int64(13), gjson.Get(events[1][1], "response.usage.total_tokens").Int())

	require.NotNil(t, result.Usage)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
}

// TestHandleSSEToJSON_PathBasedCompactRawOutputItemDoneRepairsJSON supplies
// raw compaction before empty output without client-stream marking. It checks
// repaired JSON fields and absence of SSE framing in the client body.
func TestHandleSSEToJSON_PathBasedCompactRawOutputItemDoneRepairsJSON(t *testing.T) {
	svc := newCompactBridgeTestService()
	c, rec := newCompactBridgeTestContext(t, false)
	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"cmp_v1","type":"compaction_summary","encrypted_content":"compact-v1-raw"}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_compact_v1","object":"response","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}

	result, err := svc.NonStream(context.Background(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.ProviderTypeOAuth}}, "gpt-5.5", "gpt-5.5")
	require.NoError(t, err)
	require.NotNil(t, result)

	// The response body contains the repaired JSON document.
	body := rec.Body.String()
	require.NotContains(t, body, "event:")
	require.NotContains(t, body, "data:")
	require.Equal(t, "resp_compact_v1", gjson.Get(body, "id").String())
	require.Equal(t, "compaction_summary", gjson.Get(body, "output.0.type").String())
	require.Equal(t, "compact-v1-raw", gjson.Get(body, "output.0.encrypted_content").String())
}

// TestReconstructResponseOutputFromSSE_PrefersRawDoneItems combines text
// deltas with their complete message-done item. It checks one reconstructed
// item with the supplied ID and final text rather than duplicated content.
func TestReconstructResponseOutputFromSSE_PrefersRawDoneItems(t *testing.T) {
	bodyText := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hel"}`,
		`data: {"type":"response.output_text.delta","delta":"lo"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","output":[]}}`,
	}, "\n")

	outputJSON, ok := bridge.ReconstructResponseOutputFromSSE(bodyText)
	require.True(t, ok)
	items := gjson.ParseBytes(outputJSON).Array()
	require.Len(t, items, 1, "raw done item 与 delta 重建不得重复")
	require.Equal(t, "msg_1", items[0].Get("id").String())
	require.Equal(t, "hello", items[0].Get("content.0.text").String())
}

// TestReconstructResponseOutputFromSSE_CompactionAddedFallback supplies only a
// compaction-added item before an empty terminal. It checks one recovered
// compaction item with the fixture encrypted content.
func TestReconstructResponseOutputFromSSE_CompactionAddedFallback(t *testing.T) {
	bodyText := `data: {"type":"response.output_item.added","output_index":0,"item":{"id":"cmp_add","type":"compaction","encrypted_content":"added-only"}}` + "\n" + `data: {"type":"response.completed","response":{"id":"resp_1","output":[]}}`

	outputJSON, ok := bridge.ReconstructResponseOutputFromSSE(bodyText)
	require.True(t, ok)
	items := gjson.ParseBytes(outputJSON).Array()
	require.Len(t, items, 1)
	require.Equal(t, "compaction", items[0].Get("type").String())
	require.Equal(t, "added-only", items[0].Get("encrypted_content").String())
}

// TestReconstructResponseOutputFromSSE_MixedDoneAndCompactionAdded combines
// message-done with compaction-added, then paired anonymous compaction events.
// It checks recovered item ordering and one final compaction value for those
// two scenarios.
func TestReconstructResponseOutputFromSSE_MixedDoneAndCompactionAdded(t *testing.T) {
	bodyText := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"cmp_mixed","type":"compaction","encrypted_content":"mixed"}}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":"hi"}]}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","output":[]}}`,
	}, "\n")

	outputJSON, ok := bridge.ReconstructResponseOutputFromSSE(bodyText)
	require.True(t, ok)
	items := gjson.ParseBytes(outputJSON).Array()
	require.Len(t, items, 2)
	require.Equal(t, "msg_1", items[0].Get("id").String())
	require.Equal(t, "cmp_mixed", items[1].Get("id").String())

	// A completed compaction suppresses the collector's added-compaction pass,
	// even when the anonymous added object has different bytes. Assert the final
	// encrypted value rather than a client-specific compaction requirement.
	bodyText = strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"compaction","status":"in_progress"}}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"compaction","status":"completed","encrypted_content":"final"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","output":[]}}`,
	}, "\n")
	outputJSON, ok = bridge.ReconstructResponseOutputFromSSE(bodyText)
	require.True(t, ok)
	items = gjson.ParseBytes(outputJSON).Array()
	require.Len(t, items, 1)
	require.Equal(t, "final", items[0].Get("encrypted_content").String())
}

// TestHandleSSEToJSON_CompactSupplementsMissingCompactionIntoNonEmptyOutput
// supplies raw compaction before a terminal message item. It checks both item
// types and a completed terminal containing two output items.
func TestHandleSSEToJSON_CompactSupplementsMissingCompactionIntoNonEmptyOutput(t *testing.T) {
	svc := newCompactBridgeTestService()
	c, rec := newCompactBridgeTestContext(t, true)
	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"cmp_sup","type":"compaction","encrypted_content":"supplement"}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_sup","object":"response","status":"completed","output":[{"id":"msg_sup","type":"message","role":"assistant","content":[{"type":"output_text","text":"note"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}

	result, err := svc.NonStream(context.Background(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.ProviderTypeOAuth}}, "gpt-5.5", "gpt-5.5")
	require.NoError(t, err)
	require.NotNil(t, result)

	events := httptestkit.ParseCompactSSE(t, rec.Body.String())
	require.Len(t, events, 3)
	itemTypes := []string{
		gjson.Get(events[0][1], "item.type").String(),
		gjson.Get(events[1][1], "item.type").String(),
	}
	require.Contains(t, itemTypes, "compaction")
	require.Contains(t, itemTypes, "message")
	require.Equal(t, "response.completed", events[2][0])
	require.Len(t, gjson.Get(events[2][1], "response.output").Array(), 2)
}

// TestReconstructResponseOutputFromSSE_NonCompactionAddedStillUsesDeltas
// supplies an empty message-added item followed by text. It checks one
// reconstructed output containing the completed delta text for this fixture.
func TestReconstructResponseOutputFromSSE_NonCompactionAddedStillUsesDeltas(t *testing.T) {
	bodyText := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","content":[]}}`,
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","output":[]}}`,
	}, "\n")

	outputJSON, ok := bridge.ReconstructResponseOutputFromSSE(bodyText)
	require.True(t, ok)
	items := gjson.ParseBytes(outputJSON).Array()
	require.Len(t, items, 1)
	require.Equal(t, "hi", items[0].Get("content.0.text").String())
}

// Close signals closed once and delegates every invocation to the underlying
// ReadCloser. Callers supply an open signal channel and source; the once guard
// protects signal publication rather than suppressing repeated source-close
// calls.
func (b *firstOutputCloseTrackingBody) Close() error {
	// Publish the close-attempt signal before delegation so a waiting producer
	// can leave even if the underlying source close reports failure.
	b.once.Do(func() { close(b.closed) })
	return b.ReadCloser.Close()
}

// TestOpenAINativeFirstOutputTimeoutDisabledPreservesSynchronousStream
// configures a zero first-output budget and a completed fixture stream. It
// checks successful returned observation and delivered completed event under
// that disabled budget.
func TestOpenAINativeFirstOutputTimeoutDisabledPreservesSynchronousStream(t *testing.T) {
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Response: OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}})
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_disabled"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_disabled","usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
	}, "\n")))}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	result, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), "response.completed")
}

// TestOpenAINativeFirstOutputTimeoutIgnoresPreambleAndCleansReader supplies
// preamble under an already-expired first-output deadline. It checks typed 504
// safe recovery, empty client output, body-close signaling and pipe-writer
// exit.
func TestOpenAINativeFirstOutputTimeoutIgnoresPreambleAndCleansReader(t *testing.T) {
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Response: OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 1, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}})
	pr, pw := io.Pipe()
	writerDone := make(chan struct{})
	// Send metadata in another worker while synchronous reading enforces the
	// expired deadline. Discard producer errors from reader shutdown; deferred
	// pipe closure precedes writerDone publication.
	go func() {
		defer close(writerDone)
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_slow\"}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_slow\"}}\n\n"))
		// Hold EOF briefly after metadata; this delay remains below the writer-exit
		// guard so timeout-driven closure can be observed separately.
		time.Sleep(200 * time.Millisecond)
	}()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	body := &firstOutputCloseTrackingBody{ReadCloser: pr, closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body}

	// Backdate start by two seconds to exhaust the one-second first-output
	// budget before any metadata can become visible client output.
	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}, time.Now().Add(-2*time.Second), "model", "model", "")

	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusGatewayTimeout, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "first_output_timeout")
	require.True(t, failoverErr.SafeToFailoverAfterWrite)
	require.Empty(t, rec.Body.String())
	select {
	case <-body.closed:
	default:
		t.Fatal("first-output timeout did not close the upstream response body")
	}
	// Wait one second for deferred pipe closure and the producer notification;
	// the reader result by itself does not establish that this producer finished.
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("stream reader/writer goroutine did not exit after first-output timeout")
	}
}

// TestOpenAINativeFirstOutputTimeoutDisarmsAfterSemanticOutput delays the
// terminal beyond the budget after a text delta. It checks successful
// result/timing, delivered content/terminal and retained request/quota headers
// for the selected output.
func TestOpenAINativeFirstOutputTimeoutDisarmsAfterSemanticOutput(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 1, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, headers: compileHTTPFixtureHeaders(cfg)})
	pr, pw := io.Pipe()
	// Deliver text before a delayed terminal from a pipe producer. Write and
	// Close errors are discarded because this case inspects forwarding success
	// and the delivered text and terminal.
	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_ok\"}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
		// Delay the terminal past the one-second allowance, so earlier semantic
		// output is the evidence that can disarm this deadline.
		time.Sleep(1100 * time.Millisecond)
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
	}()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"X-Request-Id":                   []string{"request-winning"},
		"X-Ratelimit-Remaining-Requests": []string{"42"},
	}, Body: pr}

	result, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), "response.output_text.delta")
	require.Contains(t, rec.Body.String(), "response.completed")
	require.Equal(t, "request-winning", rec.Result().Header.Get("X-Request-Id"))
	require.Equal(t, "42", rec.Result().Header.Get("X-Ratelimit-Remaining-Requests"))
}

// TestOpenAINativeFirstOutputTimeoutWaitsForCompleteSemanticEvent leaves a
// large text event open after its data line. It delegates assertions for timed
// recovery, private staged bytes/headers and termination of the local writer.
func TestOpenAINativeFirstOutputTimeoutWaitsForCompleteSemanticEvent(t *testing.T) {
	// Exceed the initial scanner-buffer size while holding one SSE event open.
	const lineSize = 68106
	prefix := `data: {"type":"response.output_text.delta","delta":"`
	suffix := `"}`
	line := prefix + strings.Repeat("x", lineSize-len(prefix)-len(suffix)) + suffix
	require.Len(t, line, lineSize)
	assertOpenAINativeLargeOpenEventTimesOutWithoutLeak(t, line)
}

// TestOpenAINativeFirstOutputTimeoutDoesNotLeakLargePreambleEvent leaves a
// large created event open after its data line. It delegates timeout
// assertions checking private staged bytes/headers and local writer cleanup.
func TestOpenAINativeFirstOutputTimeoutDoesNotLeakLargePreambleEvent(t *testing.T) {
	// Exceed the initial scanner-buffer size while holding one SSE event open.
	const lineSize = 68106
	prefix := `data: {"type":"response.created","response":{"id":"resp_partial","padding":"`
	suffix := `"}}`
	line := prefix + strings.Repeat("x", lineSize-len(prefix)-len(suffix)) + suffix
	require.Len(t, line, lineSize)
	assertOpenAINativeLargeOpenEventTimesOutWithoutLeak(t, line)
}

// assertOpenAINativeLargeOpenEventTimesOutWithoutLeak holds one event without
// its blank boundary until timeout closes the source. It checks safe typed
// recovery, comment-only downstream output, absent attempt headers and exit of
// the local writer.
func assertOpenAINativeLargeOpenEventTimesOutWithoutLeak(t *testing.T, line string) {
	t.Helper()
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 1, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, headers: compileHTTPFixtureHeaders(cfg)})
	pr, pw := io.Pipe()
	body := &firstOutputCloseTrackingBody{ReadCloser: pr, closed: make(chan struct{})}
	writerDone := make(chan struct{})
	// Send the line without its blank boundary, then await a close-attempt signal
	// or the producer guard. Discard write/close errors; deferred pipe closure
	// precedes writerDone so the cleanup assertion observes producer exit.
	go func() {
		defer close(writerDone)
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte(line + "\n"))
		// The two-second producer guard permits exit when the expected close signal
		// is absent; the main assertion still checks timeout-driven cleanup.
		select {
		case <-body.closed:
		case <-time.After(2 * time.Second):
		}
	}()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"X-Request-Id":                   []string{"request-partial"},
		"X-Ratelimit-Remaining-Requests": []string{"1"},
	}, Body: body}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusGatewayTimeout, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "first_output_timeout")
	require.True(t, failoverErr.SafeToFailoverAfterWrite)
	require.NotContains(t, rec.Body.String(), "data:", "attempt JSON must remain private before the SSE boundary")
	require.NotContains(t, rec.Body.String(), `"type"`, "attempt JSON must remain private before the SSE boundary")
	for outputLine := range strings.SplitSeq(strings.TrimSpace(rec.Body.String()), "\n") {
		if outputLine != "" {
			require.True(t, strings.HasPrefix(outputLine, ":"), "only keepalive comments may precede failover: %q", outputLine)
		}
	}
	require.Empty(t, rec.Header().Values("X-Request-Id"))
	require.Empty(t, rec.Header().Values("X-Ratelimit-Remaining-Requests"))
	// Wait one second for deferred pipe closure and the producer notification;
	// the reader result by itself does not establish that this producer finished.
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("partial-event writer did not exit after timeout closed the body")
	}
}

// TestOpenAINativeFirstOutputEOFDispatchesTerminalEventWithoutBlankLine ends a
// usage terminal without a blank separator. It checks final ID/usage and
// forwarded headers, and preservation of the single trailing newline at EOF.
func TestOpenAINativeFirstOutputEOFDispatchesTerminalEventWithoutBlankLine(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 1, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, headers: compileHTTPFixtureHeaders(cfg)})
	payload := `data: {"type":"response.completed","response":{"id":"resp_eof","usage":{"input_tokens":3,"output_tokens":2}}}`
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"X-Request-Id":                   []string{"request-eof"},
			"X-Ratelimit-Remaining-Requests": []string{"17"},
		},
		Body: io.NopCloser(strings.NewReader(payload)),
	}

	result, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_eof", result.ResponseID)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Contains(t, rec.Body.String(), `"type":"response.completed"`)
	require.Contains(t, rec.Body.String(), `"id":"resp_eof"`)
	require.True(t, strings.HasSuffix(rec.Body.String(), "\n"))
	require.False(t, strings.HasSuffix(rec.Body.String(), "\n\n"), "EOF dispatch must not synthesize a blank line")
	require.Equal(t, "request-eof", rec.Result().Header.Get("X-Request-Id"))
	require.Equal(t, "17", rec.Result().Header.Get("X-Ratelimit-Remaining-Requests"))
}

// TestOpenAINativeFirstOutputStageOverflowFailsOverWithoutAttemptBytes
// supplies large unclosed semantic lines beyond staging capacity. It checks
// typed safe recovery with the staging diagnostic and no client bytes or
// attempt headers.
func TestOpenAINativeFirstOutputStageOverflowFailsOverWithoutAttemptBytes(t *testing.T) {
	// Each near-MiB line fits the two-MiB scanner ceiling; the thirty-second
	// allowance lets cumulative staging, rather than elapsed time, select failure.
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 30, MaxLineSize: 2 * 1024 * 1024}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, headers: compileHTTPFixtureHeaders(cfg)})
	// Several near-MiB lines without a blank event boundary exhaust staging
	// before their bytes become eligible for the downstream response.
	const lineSize = 1024*1024 - 256
	prefix := `data: {"type":"response.output_text.delta","delta":"`
	suffix := `"}`
	line := prefix + strings.Repeat("x", lineSize-len(prefix)-len(suffix)) + suffix
	body := strings.Repeat(line+"\n", 9)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"X-Request-Id":                   []string{"request-overflow"},
			"X-Ratelimit-Remaining-Requests": []string{"1"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.SafeToFailoverAfterWrite)
	require.Contains(t, string(failoverErr.ResponseBody), "staging limit exceeded")
	require.Empty(t, rec.Body.String())
	require.Empty(t, rec.Header().Values("X-Request-Id"))
	require.Empty(t, rec.Header().Values("X-Ratelimit-Remaining-Requests"))
}

// TestOpenAINativeFirstOutputScannerRejectsOversizedLineWithoutLeak exceeds
// the guarded scanner bound after a private preamble. It checks safe recovery,
// the guarded-line diagnostic and absence of attempt bytes and headers
// downstream.
func TestOpenAINativeFirstOutputScannerRejectsOversizedLineWithoutLeak(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 30, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, headers: compileHTTPFixtureHeaders(cfg)})
	// Exceed the staging/framing guard by another KiB while the normal scanner
	// ceiling stays larger, isolating the initial guarded-line rejection.
	oversizedLine := "data: " + strings.Repeat("x", responseupstream.OpenAIFirstOutputStageMaxBytes+responseupstream.OpenAIFirstOutputScannerFramingAllowance+1024)
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_private\"}}\n\n" + oversizedLine + "\n"
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"X-Request-Id":                   []string{"request-too-large"},
			"X-Ratelimit-Remaining-Requests": []string{"1"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.SafeToFailoverAfterWrite)
	require.Contains(t, string(failoverErr.ResponseBody), "line exceeds guarded first-output limit")
	require.Empty(t, rec.Body.String())
	require.Empty(t, rec.Header().Values("X-Request-Id"))
	require.Empty(t, rec.Header().Values("X-Ratelimit-Remaining-Requests"))
}

// TestOpenAINativeFirstOutputScannerAllowsLargeEventAfterSemanticBoundary
// sends a small complete text event before a larger delta. It checks
// successful usage/timing, delivered large content and selected request
// identity afterward.
func TestOpenAINativeFirstOutputScannerAllowsLargeEventAfterSemanticBoundary(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 30, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, headers: compileHTTPFixtureHeaders(cfg)})
	// Reuse a size above the initial staging/framing guard after semantic output
	// so successful delivery distinguishes that guard from the normal scanner limit.
	largeDelta := strings.Repeat("i", responseupstream.OpenAIFirstOutputStageMaxBytes+responseupstream.OpenAIFirstOutputScannerFramingAllowance+1024)
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"ready"}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"` + largeDelta + `"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_large_image","usage":{"input_tokens":4,"output_tokens":3}}}`,
		"",
	}, "\n")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"request-large-image"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	result, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_large_image", result.ResponseID)
	require.Equal(t, 4, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Contains(t, rec.Body.String(), `"delta":"ready"`)
	require.Contains(t, rec.Body.String(), `"id":"resp_large_image"`)
	require.Contains(t, rec.Body.String(), strings.Repeat("i", 1024))
	require.Equal(t, "request-large-image", rec.Result().Header.Get("X-Request-Id"))
}

// TestOpenAINativeFirstOutputTimeoutDisabledKeepsPreamblePrivateAcrossKeepalive
// supplies only metadata while a heartbeat runs without first-output timeout.
// It checks eventual recovery and comment output without delivered
// created/in-progress frames.
func TestOpenAINativeFirstOutputTimeoutDisabledKeepsPreamblePrivateAcrossKeepalive(t *testing.T) {
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Response: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize}}})
	pr, pw := io.Pipe()
	// Hold EOF while metadata stays private, allowing heartbeat opportunities
	// before closing the pipe. Write/Close failures are discarded because the
	// assertions inspect recovery and the resulting client framing.
	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_stalled\"}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_stalled\"}}\n\n"))
		// Leave the metadata-only pipe open across two one-second heartbeat periods
		// before EOF, giving client framing assertions an idle interval to observe.
		time.Sleep(2100 * time.Millisecond)
	}()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	stopKeepalive := installDownstreamKeepaliveTest(t, c, time.Second)
	defer stopKeepalive()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")

	stopKeepalive()
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Contains(t, rec.Body.String(), ": keepalive\n\n")
	require.NotContains(t, rec.Body.String(), "response.created")
	require.NotContains(t, rec.Body.String(), "response.in_progress")
}

// TestOpenAINativeFirstOutputFailoverKeepsAttemptHeadersPrivateAfterKeepaliveCommit
// times out one preamble then directly reads a healthy second response. It
// checks comment-only first output, delivered second terminal, absent
// committed attempt headers and first writer exit.
func TestOpenAINativeFirstOutputFailoverKeepsAttemptHeadersPrivateAfterKeepaliveCommit(t *testing.T) {
	// Outlast the one-second heartbeat with a two-second first-output budget so
	// this attempt can commit an idle comment before recovery.
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 2, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, headers: compileHTTPFixtureHeaders(cfg)})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	stopKeepalive := installDownstreamKeepaliveTest(t, c, time.Second)
	defer stopKeepalive()
	firstBody, firstWriter := io.Pipe()
	trackedFirstBody := &firstOutputCloseTrackingBody{ReadCloser: firstBody, closed: make(chan struct{})}
	firstWriterDone := make(chan struct{})
	// Keep the first preamble source open until its close signal or guard fires.
	// Discard producer write/close errors and publish firstWriterDone after pipe
	// cleanup so the second observation can be checked separately.
	go func() {
		defer close(firstWriterDone)
		defer func() { _ = firstWriter.Close() }()
		_, _ = firstWriter.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_first\"}}\n\n"))
		// Give the expected two-second timeout room inside a four-second producer
		// guard; absent close signaling still permits deferred pipe cleanup.
		select {
		case <-trackedFirstBody.closed:
		case <-time.After(4 * time.Second):
		}
	}()
	firstResp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"text/event-stream"},
			"X-Request-Id":                   []string{"request-first"},
			"X-Ratelimit-Remaining-Requests": []string{"1"},
		},
		Body: trackedFirstBody,
	}

	_, firstErr := svc.Output.ReadStreamObservation(c.Request.Context(), firstResp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")
	stopKeepalive()
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, firstErr, &failoverErr)
	require.Contains(t, rec.Body.String(), ": keepalive\n\n", "first attempt should have committed only a stable keepalive")
	require.NotContains(t, rec.Body.String(), "resp_first")

	secondResp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"text/event-stream"},
			"X-Request-Id":                   []string{"request-second"},
			"X-Ratelimit-Remaining-Requests": []string{"99"},
		},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"hello"}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_second","usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n"))),
	}
	result, secondErr := svc.Output.ReadStreamObservation(c.Request.Context(), secondResp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")

	require.NoError(t, secondErr)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), "resp_second")
	wireHeaders := rec.Result().Header
	require.Empty(t, wireHeaders.Values("X-Request-Id"))
	require.Empty(t, wireHeaders.Values("X-Ratelimit-Remaining-Requests"))
	require.Empty(t, rec.Header().Values("X-Request-Id"))
	require.Empty(t, rec.Header().Values("X-Ratelimit-Remaining-Requests"))
	// Wait for the first producer after both observations because source-close
	// signaling precedes its deferred pipe cleanup and exit notification.
	select {
	case <-firstWriterDone:
	case <-time.After(time.Second):
		t.Fatal("first provider writer did not exit after timeout")
	}
}

// TestOpenAIFirstOutputStageDefaultLimitIsIndependentFromScannerLimit
// constructs and closes the default output stage. It checks its eight-MiB
// bound is above the fixture line size and below the compatibility scanner
// ceiling.
func TestOpenAIFirstOutputStageDefaultLimitIsIndependentFromScannerLimit(t *testing.T) {
	stage := responseupstream.NewDefaultOpenAIFirstOutputStage()
	defer func() { require.NoError(t, stage.Close()) }()

	require.EqualValues(t, 8*1024*1024, stage.Limit())
	require.Greater(t, stage.Limit(), int64(68106))
	require.Less(t, stage.Limit(), int64(OpenAIResponseDefaultMaxLineSize))
}

// TestResponsesStreamAccessStateFailoverPrecedesPassthroughRule installs a
// presentation override beside structured access-state failure. It checks
// credential recovery classification before client commitment in native and
// passthrough readers.
func TestResponsesStreamAccessStateFailoverPrecedesPassthroughRule(t *testing.T) {
	stream := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"status":"failed","error":{"code":"account_disabled","message":"Your provider is disabled"}}}` + "\n\n"
	tests := []struct {
		name string // fixture case name
		// selected fixture stream reader
		run func(*wsExecutionFixture, *gin.Context, *http.Response, *gatewayprovider.ExecutionProvider) error
	}{
		{
			name: "native",
			run: func(svc *wsExecutionFixture, c *gin.Context, resp *http.Response, provider *gatewayprovider.ExecutionProvider) error {
				_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, provider, time.Now(), "gpt-5", "gpt-5", "")
				return err
			},
		},
		{
			name: "passthrough",
			run: func(svc *wsExecutionFixture, c *gin.Context, resp *http.Response, provider *gatewayprovider.ExecutionProvider) error {
				_, err := responseupstream.ReadPassthroughStreaming(c.Request.Context(), resp, upstreamcore.NewOutputContext(ResponseSink{Writer: c.Writer}), svc.Output.PassthroughOptions(c.Request.Context(), c, provider), time.Now(), "gpt-5", "gpt-5")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			bindPassthroughRule(c, capability.PlatformOpenAI, []string{"provider is disabled"}, http.StatusTeapot)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(stream)),
			}
			svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize}}})
			err := tt.run(svc, c, resp, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 11, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}})

			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.IsCredentialFailure())
			require.Equal(t, forwardcore.OpenAIUpstreamAccessStateReason, failoverErr.Reason)
			require.False(t, failoverErr.RetryableOnSameProvider)
			require.Equal(t, http.StatusBadGateway, failoverErr.ClientStatusCode)
			require.False(t, c.Writer.Written(), "passthrough rule must not commit a response before provider failover")
		})
	}
}

// TestResponsesStreamCyberPolicyPrecedesPassthroughRule installs a
// presentation override beside cyber-policy evidence. It checks risk marking
// and client cyber output with no recovery wrapper or configured teapot
// status.
func TestResponsesStreamCyberPolicyPrecedesPassthroughRule(t *testing.T) {
	stream := "event: error\n" +
		`data: {"type":"error","error":{"code":"cyber_policy","message":"blocked by cyber policy"}}` + "\n\n"
	tests := []struct {
		name string // fixture case name
		// selected fixture stream reader
		run func(*wsExecutionFixture, *gin.Context, *http.Response, *gatewayprovider.ExecutionProvider) error
	}{
		{
			name: "native",
			run: func(svc *wsExecutionFixture, c *gin.Context, resp *http.Response, provider *gatewayprovider.ExecutionProvider) error {
				_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, provider, time.Now(), "gpt-5", "gpt-5", "")
				return err
			},
		},
		{
			name: "passthrough",
			run: func(svc *wsExecutionFixture, c *gin.Context, resp *http.Response, provider *gatewayprovider.ExecutionProvider) error {
				_, err := responseupstream.ReadPassthroughStreaming(c.Request.Context(), resp, upstreamcore.NewOutputContext(ResponseSink{Writer: c.Writer}), svc.Output.PassthroughOptions(c.Request.Context(), c, provider), time.Now(), "gpt-5", "gpt-5")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			bindPassthroughRule(c, capability.PlatformOpenAI, []string{"cyber policy"}, http.StatusTeapot)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(stream)),
			}
			svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize}}})
			err := tt.run(svc, c, resp, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 12, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}})

			require.Error(t, err)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))
			require.NotNil(t, GetOpsCyberPolicy(c))
			require.NotEqual(t, http.StatusTeapot, rec.Code)
			require.Contains(t, rec.Body.String(), "cyber_policy")
		})
	}
}

// TestOpenAIResponsesStreaming_ResponseFailedCustomStatusFailsOver admits
// semantic HTTP 422 inside HTTP 200 through provider policy. It checks one
// error-store write, typed recovery, no same-provider retry and no client
// commitment.
func TestOpenAIResponsesStreaming_ResponseFailedCustomStatusFailsOver(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	failed := `{"type":"response.failed","response":{"status":"failed","error":{"status_code":422,"code":"configured","message":"configured failure"}}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: " + failed + "\n\n")),
	}
	repo := &openAIWSPolicyRepo{}
	options := rawChatCompletionsTestConfig()
	svc := newWSFixture(wsFixtureInputs{options: options, health: newUpstreamHealthForTest(repo, options, nil, providercore.HealthOptions{}, nil), corrector: responseupstream.NewCodexToolCorrector()})
	provider := rawChatCompletionsTestProvider()
	provider.Record.Credentials["custom_error_codes_enabled"] = true
	provider.Record.Credentials["custom_error_codes"] = []any{float64(http.StatusUnprocessableEntity)}
	_, err := svc.Output.ReadStreamObservation(context.Background(), resp, c, provider, time.Now(), "gpt-5.4", "gpt-5.4", "")

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusUnprocessableEntity, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameProvider)
	require.False(t, c.Writer.Written())
	require.Equal(t, 1, repo.setErrorCalls)
}

// newOpenAIResponseFlushRecorder creates fresh headers, disables injected Write
// failures and buffers sixteen flush notifications. Flush sends synchronously,
// so callers consume notifications before the buffer fills and release any
// flush gate they configure.
func newOpenAIResponseFlushRecorder() *openAIResponseFlushRecorder {
	return &openAIResponseFlushRecorder{
		header:          make(http.Header),
		failAfterWrites: -1,
		flushEvents:     make(chan int, 16),
	}
}

// Read advances through the fixture segments, publishing each optional waiting
// signal before blocking on its corresponding gate. Callers supply open signal
// channels and release gates in the ordering under test; Close does not
// unblock a gated read.
//
// A segment can span multiple reads through current, while index advances
// before its gate wait. Missing gates permit immediate progress, and
// exhaustion closes eofReached once by clearing the channel field before
// subsequent EOF results. Use this reader sequentially and keep borrowed
// segment bytes stable until consumed so controlled interleavings do not
// introduce another concurrent state writer.
func (r *stagedOpenAISSEReadCloser) Read(data []byte) (int, error) {
	if len(r.current) == 0 {
		if r.index >= len(r.segments) {
			if r.eofReached != nil {
				// Publish exhaustion once and clear its channel so repeated EOF reads
				// leave the test notification closed instead of closing it again.
				close(r.eofReached)
				r.eofReached = nil
			}
			return 0, io.EOF
		}
		// Announce this selected segment before waiting for its gate: tests await
		// that notification before releasing the gate, so reversed ordering stalls.
		index := r.index
		r.index++
		if index < len(r.waiting) && r.waiting[index] != nil {
			close(r.waiting[index])
		}
		if index < len(r.gates) && r.gates[index] != nil {
			<-r.gates[index]
		}
		r.current = r.segments[index]
	}
	n := copy(data, r.current)
	r.current = r.current[n:]
	return n, nil
}

// Close satisfies the fixture body interface and returns nil with gate state
// unchanged. Tests release pending gates themselves before receiving async
// results, since cleanup cannot unblock a gated Read.
func (r *stagedOpenAISSEReadCloser) Close() error { return nil }

// Read copies payload on the first call, then returns err or unexpected EOF.
// It discards any payload remainder if the initial destination is short, so
// callers provide sufficient first-read capacity when the full prefix matters.
func (r *openAIResponseFlushReadError) Read(data []byte) (int, error) {
	if !r.sent {
		// Even an empty first destination consumes the prefix attempt. Mark it
		// before copying so later reads exercise the configured terminal failure.
		r.sent = true
		return copy(data, r.payload), nil
	}
	if r.err != nil {
		return 0, r.err
	}
	return 0, io.ErrUnexpectedEOF
}

// Close satisfies fixture cleanup without changing prefix or error state.
func (r *openAIResponseFlushReadError) Close() error { return nil }

// TestOpenAIResponseFlush_SlowEventsFlushOnceAtBoundaries supplies separately
// terminated content and DONE events. It checks exact bytes and one
// complete-event flush snapshot per supplied event in this fixture.
func TestOpenAIResponseFlush_SlowEventsFlushOnceAtBoundaries(t *testing.T) {
	events := []string{
		`data: {"type":"response.output_text.delta","delta":"a"}`,
		`data: {"type":"response.output_text.delta","delta":"b"}`,
		`data: {"type":"response.output_text.delta","delta":"c"}`,
		`data: [DONE]`,
	}
	body := strings.Join(events, "\n\n") + "\n\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), OpenAIResponseOptions{})

	require.NoError(t, err)
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, body, gotBody)
	require.Len(t, flushes, len(events))
	for _, flushed := range flushes {
		require.True(t, strings.HasSuffix(flushed, "\n\n"), "flush must occur after a complete SSE event")
	}
}

// TestOpenAIResponseFlush_DataQueuedButBlankDrainsFlushesOnce blocks the first
// flush while a second event queues. It checks three ordered snapshots and one
// second-event flush after that controlled queue drain.
func TestOpenAIResponseFlush_DataQueuedButBlankDrainsFlushesOnce(t *testing.T) {
	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"first\"}\n\n"
	second := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"second\"}\n\n"
	terminal := "data: [DONE]\n\n"
	allowSecond := make(chan struct{})
	allowTerminal := make(chan struct{})
	terminalWaiting := make(chan struct{})
	reader := &stagedOpenAISSEReadCloser{ // delegated upstream source body
		segments: [][]byte{[]byte(first), []byte(second), []byte(terminal)},
		gates:    []<-chan struct{}{nil, allowSecond, allowTerminal},
		waiting:  []chan struct{}{nil, nil, terminalWaiting},
	}
	releaseFirstFlush := make(chan struct{})
	recorder := newOpenAIResponseFlushRecorder()
	recorder.blockFlush = 1
	recorder.flushBlocked = make(chan struct{})
	recorder.releaseFlush = releaseFirstFlush
	resultCh, errCh := runOpenAIResponseFlushTestAsync(recorder, reader, OpenAIResponseOptions{StreamDataIntervalTimeout: 30})

	// Keep the first flush blocked while admitting the second event. Wait for
	// the terminal-read gate before releasing that flush so second-event lines
	// are already queued, then admit the terminal after its separate flush.
	waitOpenAIResponseFlushSignal(t, recorder.flushBlocked)
	close(allowSecond)
	waitOpenAIResponseFlushSignal(t, terminalWaiting)
	close(releaseFirstFlush)
	waitOpenAIResponseFlushCount(t, recorder, 2)
	close(allowTerminal)

	require.NoError(t, <-errCh)
	require.NotNil(t, <-resultCh)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, first+second+terminal, gotBody)
	require.Len(t, flushes, 3)
	require.Equal(t, first, flushes[0])
	require.Equal(t, first+second, flushes[1], "blank line that drains the queue must flush the complete event exactly once")
}

// TestOpenAIResponseFlush_BurstDoesNotIncreaseFlushes releases a queued burst
// while the first flush is blocked. It checks exact bytes and two snapshots,
// retaining the queued burst as one drained batch.
func TestOpenAIResponseFlush_BurstDoesNotIncreaseFlushes(t *testing.T) {
	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"first\"}\n\n"
	burst := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"second"}`,
		`data: {"type":"response.output_text.delta","delta":"third"}`,
		`data: [DONE]`,
	}, "\n\n") + "\n\n"
	allowBurst := make(chan struct{})
	eofReached := make(chan struct{})     // segment-exhaustion notification channel
	reader := &stagedOpenAISSEReadCloser{ // delegated upstream source body
		segments:   [][]byte{[]byte(first), []byte(burst)},
		gates:      []<-chan struct{}{nil, allowBurst},
		eofReached: eofReached,
	}
	releaseFirstFlush := make(chan struct{})
	recorder := newOpenAIResponseFlushRecorder()
	recorder.blockFlush = 1
	recorder.flushBlocked = make(chan struct{})
	recorder.releaseFlush = releaseFirstFlush
	resultCh, errCh := runOpenAIResponseFlushTestAsync(recorder, reader, OpenAIResponseOptions{StreamDataIntervalTimeout: 30})

	// Admit the burst while the first flush is blocked, then await source EOF
	// before releasing it; that ordering puts the burst in the scanner queue
	// before the main loop can drain and flush it.
	waitOpenAIResponseFlushSignal(t, recorder.flushBlocked)
	close(allowBurst)
	waitOpenAIResponseFlushSignal(t, eofReached)
	close(releaseFirstFlush)

	require.NoError(t, <-errCh)
	require.NotNil(t, <-resultCh)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, first+burst, gotBody)
	require.Len(t, flushes, 2, "queued burst must remain batched until its drained event boundary")
	require.Equal(t, first, flushes[0])
	require.Equal(t, first+burst, flushes[1])
}

// TestOpenAIResponseFlush_CommentAndEOFOnlyFlushCompleteResidual mixes a text
// frame, comment and unterminated DONE line. It checks that the comment is
// removed and only content boundaries and final EOF bytes produce flushes.
func TestOpenAIResponseFlush_CommentAndEOFOnlyFlushCompleteResidual(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n" +
		": upstream-comment\n\n" +
		"data: [DONE]\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), OpenAIResponseOptions{})

	require.NoError(t, err)
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, strings.ReplaceAll(body, ": upstream-comment\n\n", ""), gotBody)
	require.Len(t, flushes, 2)
	require.True(t, strings.HasSuffix(flushes[0], "\n\n"))
	require.True(t, strings.HasSuffix(flushes[1], "data: [DONE]\n"), "EOF must flush only the remaining bytes")
}

// TestOpenAIResponseFlush_TerminalReadErrorFlushesResidual returns an error
// after an unterminated DONE line. It checks successful terminal observation
// and one exact residual flush despite that tail read failure.
func TestOpenAIResponseFlush_TerminalReadErrorFlushesResidual(t *testing.T) {
	body := "data: [DONE]\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, &openAIResponseFlushReadError{payload: []byte(body)}, OpenAIResponseOptions{})

	require.NoError(t, err)
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, body, gotBody)
	require.Equal(t, []string{body}, flushes)
}

// TestOpenAIResponseFlush_OutputWithoutTerminalFlushesResidualWithoutFailover
// ends after unterminated text output. It checks missing-terminal error,
// partial result and one retained residual flush with no provider recovery
// wrapper.
func TestOpenAIResponseFlush_OutputWithoutTerminalFlushesResidualWithoutFailover(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), OpenAIResponseOptions{})

	require.ErrorContains(t, err, "missing terminal event")
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, body, gotBody)
	require.Equal(t, []string{body}, flushes)
}

// TestOpenAIResponseFlush_PreambleWithoutTerminalRemainsBufferedForFailover
// ends after an unterminated created line. It checks typed recovery and
// returned observation while body and flush snapshots remain empty.
func TestOpenAIResponseFlush_PreambleWithoutTerminalRemainsBufferedForFailover(t *testing.T) {
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), OpenAIResponseOptions{})

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Empty(t, gotBody)
	require.Empty(t, flushes)
}

// TestOpenAIResponseFlush_CanceledAfterOutputFlushesResidualWithoutErrorEvent
// returns cancellation after an unterminated text line. It checks the
// cancellation chain and one residual flush with no stream-read error
// injection.
func TestOpenAIResponseFlush_CanceledAfterOutputFlushesResidualWithoutErrorEvent(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, &openAIResponseFlushReadError{payload: []byte(body), err: context.Canceled}, OpenAIResponseOptions{})

	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, body, gotBody)
	require.Equal(t, []string{body}, flushes)
	require.NotContains(t, gotBody, "stream_read_error")
}

// TestOpenAIResponseFlush_FailedAndErrorEventsFlushAtBoundaries compares
// failed terminals and bare-error sequences under OAuth output. It checks
// retained/drained usage and one selected failed terminal with the expected
// flush boundaries.
func TestOpenAIResponseFlush_FailedAndErrorEventsFlushAtBoundaries(t *testing.T) {
	t.Run("failed at EOF", func(t *testing.T) {
		body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n" +
			"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"safety_error\",\"message\":\"blocked\"},\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n"
		recorder := newOpenAIResponseFlushRecorder()

		result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), OpenAIResponseOptions{})

		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 3, result.Usage.InputTokens)
		gotBody, flushes := recorder.snapshot()
		expectedBody := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n" +
			"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"safety_error\",\"message\":\"blocked\"}}}\n"
		require.Equal(t, expectedBody, gotBody)
		require.Len(t, flushes, 2)
		require.Contains(t, flushes[1], "response.failed")
	})

	t.Run("bare error synthesizes failed before done", func(t *testing.T) {
		body := "data: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\n" +
			"data: [DONE]\n\n"
		recorder := newOpenAIResponseFlushRecorder()

		result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), OpenAIResponseOptions{})

		require.Error(t, err)
		require.NotNil(t, result)
		gotBody, flushes := recorder.snapshot()
		require.NotContains(t, gotBody, `"type":"error"`)
		require.Equal(t, 1, strings.Count(gotBody, `"type":"response.failed"`))
		require.Contains(t, gotBody, `"status":"failed"`)
		require.NotContains(t, gotBody, "[DONE]")
		require.Len(t, flushes, 1)
	})

	t.Run("bare error forwards following failed and drains terminal usage", func(t *testing.T) {
		errorEvent := "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request\",\"message\":\"bad request\"}}\n\n"
		body := errorEvent +
			"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failed\",\"usage\":{\"input_tokens\":9,\"output_tokens\":2},\"error\":{\"code\":\"invalid_request\",\"message\":\"bad request\"}}}\n\n" +
			"data: [DONE]\n\n"
		recorder := newOpenAIResponseFlushRecorder()

		result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), OpenAIResponseOptions{})

		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 9, result.Usage.InputTokens)
		require.Equal(t, 2, result.Usage.OutputTokens)
		gotBody, flushes := recorder.snapshot()
		require.NotContains(t, gotBody, `"type":"error"`)
		require.Equal(t, 1, strings.Count(gotBody, `"type":"response.failed"`))
		require.Contains(t, gotBody, `"id":"resp_failed"`)
		require.NotContains(t, gotBody, "[DONE]")
		require.Len(t, flushes, 1)
	})

	t.Run("non-retryable error event flushes at boundary", func(t *testing.T) {
		body := "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request\",\"message\":\"bad request\"}}\n\n" +
			"data: [DONE]\n\n"
		recorder := newOpenAIResponseFlushRecorder()

		result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), OpenAIResponseOptions{})

		require.Error(t, err)
		require.NotNil(t, result)
		gotBody, flushes := recorder.snapshot()
		require.NotContains(t, gotBody, `"type":"error"`)
		require.Equal(t, 1, strings.Count(gotBody, `"type":"response.failed"`))
		require.Len(t, flushes, 1)
	})
}

// TestOpenAIResponseFlush_BareErrorFollowedByCompletedUsesCompletedTerminal
// follows a transient bare error with success. It checks successful usage
// observation and completed output with neither the bare error nor a
// synthesized failure.
func TestOpenAIResponseFlush_BareErrorFollowedByCompletedUsesCompletedTerminal(t *testing.T) {
	body := "data: {\"type\":\"error\",\"error\":{\"code\":\"transient\",\"message\":\"retrying\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_recovered\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), OpenAIResponseOptions{})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	gotBody, _ := recorder.snapshot()
	require.NotContains(t, gotBody, `"type":"error"`)
	require.NotContains(t, gotBody, `"type":"response.failed"`)
	require.Contains(t, gotBody, `"type":"response.completed"`)
}

// TestOpenAIResponseFlush_CompatibleAPIKeyDoesNotUseCodexBareErrorSynthesis
// supplies a bare provider error under API-key output. It checks retained bare
// error and absence of the OAuth-specific failed terminal synthesis.
func TestOpenAIResponseFlush_CompatibleAPIKeyDoesNotUseCodexBareErrorSynthesis(t *testing.T) {
	body := "data: {\"type\":\"error\",\"error\":{\"code\":\"provider_error\",\"message\":\"provider failed\"}}\n\n"
	recorder := newOpenAIResponseFlushRecorder()
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}

	result, err := runOpenAIResponseFlushTestWithProvider(recorder, io.NopCloser(strings.NewReader(body)), OpenAIResponseOptions{}, provider)

	require.Error(t, err)
	require.NotNil(t, result)
	gotBody, _ := recorder.snapshot()
	require.Contains(t, gotBody, `"type":"error"`)
	require.NotContains(t, gotBody, `"type":"response.failed"`)
}

// TestOpenAIResponseFlush_RecentBareErrorAllowsCompletedBeforeIdleTimeout
// places error shortly before an idle tick and success afterward. It checks
// successful observed usage and completed output without failure synthesis in
// that timing fixture.
func TestOpenAIResponseFlush_RecentBareErrorAllowsCompletedBeforeIdleTimeout(t *testing.T) {
	reader, writer := io.Pipe()
	// Close the pipe on every exit to release upstream reading if an assertion
	// fails before the terminal; cleanup errors are discarded by this fixture.
	defer func() { _ = writer.Close() }()
	recorder := newOpenAIResponseFlushRecorder()
	resultCh, errCh := runOpenAIResponseFlushTestAsync(recorder, reader, OpenAIResponseOptions{StreamDataIntervalTimeout: 1})

	// Place the bare error shortly before the first ticker firing. It is fresh
	// data, so the ticker must leave the stream open for an authoritative event.
	time.Sleep(700 * time.Millisecond)
	_, err := io.WriteString(writer, "data: {\"type\":\"error\",\"error\":{\"code\":\"transient\",\"message\":\"retrying\"}}\n\n")
	require.NoError(t, err)
	// Completion follows the nominal first idle tick but arrives only half a
	// second after fresh error data, inside the one-second data-idle allowance.
	time.Sleep(500 * time.Millisecond)
	_, err = io.WriteString(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_late\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n")
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	require.NoError(t, <-errCh)
	result := <-resultCh
	require.NotNil(t, result)
	require.Equal(t, 5, result.Usage.InputTokens)
	gotBody, _ := recorder.snapshot()
	require.Contains(t, gotBody, `"type":"response.completed"`)
	require.NotContains(t, gotBody, `"type":"response.failed"`)
}

// TestOpenAIResponseFlush_BareErrorTimeoutSynthesizesFailed leaves a bare
// invalid-request error awaiting a terminal under two timeout settings. It
// checks a bounded error return and one flushed failed terminal replacing the
// bare event.
func TestOpenAIResponseFlush_BareErrorTimeoutSynthesizesFailed(t *testing.T) {
	tests := []struct {
		name string                // fixture case name
		cfg  OpenAIResponseOptions // selected fixture timeout policy
	}{
		{
			name: "stream interval timeout",
			cfg:  OpenAIResponseOptions{StreamDataIntervalTimeout: 1},
		},
		{
			name: "first output timeout",
			cfg:  OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, writer := io.Pipe()
			// Close on every case exit so a failed synthesis wait releases pipe reading;
			// cleanup errors are discarded while terminal assertions inspect the outcome.
			defer func() { _ = writer.Close() }()
			recorder := newOpenAIResponseFlushRecorder()
			resultCh, errCh := runOpenAIResponseFlushTestAsync(recorder, reader, tt.cfg)

			_, writeErr := io.WriteString(writer, "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request\",\"message\":\"bad request\"}}\n\n")
			require.NoError(t, writeErr)

			// Give the one-second synthesis setting a three-second observation guard;
			// deferred writer closure releases the source if that guard fails.
			select {
			case err := <-errCh:
				require.Error(t, err)
				require.Contains(t, err.Error(), "upstream response failed")
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for bare error terminal synthesis")
			}
			require.NotNil(t, <-resultCh)
			body, flushes := recorder.snapshot()
			require.NotContains(t, body, `"type":"error"`)
			require.Equal(t, 1, strings.Count(body, `"type":"response.failed"`))
			require.Len(t, flushes, 1)
		})
	}
}

// TestOpenAIResponseFlush_ReusedTypeKeepsSSEBytesAndTerminalSemantics compares
// whitespace DONE and invalid JSON before DONE. It checks successful returned
// observations, exact supplied bytes and the listed flush counts.
func TestOpenAIResponseFlush_ReusedTypeKeepsSSEBytesAndTerminalSemantics(t *testing.T) {
	tests := []struct {
		name       string // fixture case name
		body       string // upstream SSE fixture bytes
		flushCount int    // expected recorded flush count
	}{
		{
			name:       "whitespace around done",
			body:       "data: \t[DONE]  \n\n",
			flushCount: 1,
		},
		{
			name:       "invalid JSON before done",
			body:       "data: {\"type\":\n\ndata: [DONE]\n\n",
			flushCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newOpenAIResponseFlushRecorder()

			result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(tt.body)), OpenAIResponseOptions{})

			require.NoError(t, err)
			require.NotNil(t, result)
			gotBody, flushes := recorder.snapshot()
			require.Equal(t, tt.body, gotBody)
			require.Len(t, flushes, tt.flushCount)
		})
	}
}

// TestOpenAIResponseFlush_ClientDisconnectStillDrainsUsage fails writes after
// the first content frame. It checks terminal usage after draining and one
// retained/flush content snapshot before disconnection.
func TestOpenAIResponseFlush_ClientDisconnectStillDrainsUsage(t *testing.T) {
	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n"
	terminal := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":5,\"input_tokens_details\":{\"cached_tokens\":2}}}}\n\n"
	recorder := newOpenAIResponseFlushRecorder()
	recorder.failAfterWrites = 1

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(first+terminal)), OpenAIResponseOptions{})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, first, gotBody)
	require.Len(t, flushes, 1)
}

// runOpenAIResponseFlushTest delegates a configured stream fixture using its
// default OpenAI OAuth provider. The supplied recorder and body remain live
// through synchronous reading; callers retain cleanup for body and any
// controlled gates.
func runOpenAIResponseFlushTest(recorder *openAIResponseFlushRecorder, body io.ReadCloser, gatewayCfg OpenAIResponseOptions) (*responseupstream.StreamingResult, error) {
	return runOpenAIResponseFlushTestWithProvider(recorder, body, gatewayCfg, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}})
}

// runOpenAIResponseFlushTestWithProvider constructs one request/output fixture
// with the supplied provider and reader options. It forces Configured, a
// bounded read limit and tool correction, then returns synchronous stream
// observations and error without independently closing the supplied body.
func runOpenAIResponseFlushTestWithProvider(recorder *openAIResponseFlushRecorder, body io.ReadCloser, gatewayCfg OpenAIResponseOptions, provider *gatewayprovider.ExecutionProvider) (*responseupstream.StreamingResult, error) {
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	output := newAuxiliaryFixture(auxiliaryFixtureInputs{}).Output
	output.Options = gatewayCfg
	output.Options.Configured = true
	output.Options.ReadLimit = 128 * 1024 * 1024
	output.Corrector = responseupstream.NewCodexToolCorrector()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
	return output.ReadStreamObservation(context.Background(), resp, c, provider, time.Now(), "gpt-5", "gpt-5", "")
}

// runOpenAIResponseFlushTestAsync starts the synchronous fixture reader in one
// worker and returns separate result/error channels. Both channels have
// capacity one, so completion can publish result then error even when a caller
// waits for error first.
//
// Callers keep the recorder and body available, release every controlled
// read/flush gate, and consume both observations before final mutable
// assertions. The helper closes neither channel and owns no cancellation path.
// Reader/body cleanup remains with the fixture, and a worker blocked by a gate
// needs that gate released even if the test's own wait fails.
func runOpenAIResponseFlushTestAsync(recorder *openAIResponseFlushRecorder, body io.ReadCloser, gatewayCfg OpenAIResponseOptions) (<-chan *responseupstream.StreamingResult, <-chan error) {
	resultCh := make(chan *responseupstream.StreamingResult, 1)
	errCh := make(chan error, 1)
	// Run one reader worker, then publish result before error to the buffered
	// channels; their single-slot capacity lets a caller waiting on error proceed.
	go func() {
		result, err := runOpenAIResponseFlushTest(recorder, body, gatewayCfg)
		resultCh <- result
		errCh <- err
	}()
	return resultCh, errCh
}

// waitOpenAIResponseFlushCount consumes recorder notifications until count
// reaches want. It fails the test after a three-second wait, and callers keep
// the producing reader active or release its gates so the expected flush can
// occur.
func waitOpenAIResponseFlushCount(t *testing.T, recorder *openAIResponseFlushRecorder, want int) {
	t.Helper()
	// Share one three-second timer across all notifications so earlier flushes
	// do not restart the fixture's allowance for observing the requested count.
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case count := <-recorder.flushEvents:
			if count >= want {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for flush %d", want)
		}
	}
}

// waitOpenAIResponseFlushSignal waits for the selected fixture coordination
// signal and fails after three seconds. Callers arrange the corresponding
// producer and gate ordering before waiting; nil signals remain blocked until
// that fixture deadline.
func waitOpenAIResponseFlushSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	// Bound this coordination wait to three seconds so absent notification fails
	// the fixture while the caller continues to own gate-release ordering.
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for stream signal")
	}
}

// Read returns immediate cancellation with no bytes for the fixture stream.
func (c cancelReadCloser) Read(p []byte) (int, error) { return 0, context.Canceled }

// Close satisfies cleanup of the cancellation fixture without another side
// effect.
func (c cancelReadCloser) Close() error { return nil }

// Read returns zero bytes and the configured upstream fixture error.
func (r errReadCloser) Read([]byte) (int, error) { return 0, r.err }

// Close satisfies cleanup of the configured-error fixture without changing its
// error.
func (r errReadCloser) Close() error { return nil }

// TestExtractOpenAIResponseIDFromJSONBytes compares root and nested response
// identifiers with missing and invalid JSON. It checks the exact returned IDs
// and empty results for the listed unavailable inputs.
func TestExtractOpenAIResponseIDFromJSONBytes(t *testing.T) {
	require.Equal(t, "resp_json", protocolopenai.ExtractOpenAIResponseIDFromJSONBytes([]byte(`{"id":"resp_json"}`)))
	require.Equal(t, "resp_sse", protocolopenai.ExtractOpenAIResponseIDFromJSONBytes([]byte(`{"type":"response.completed","response":{"id":"resp_sse"}}`)))
	require.Empty(t, protocolopenai.ExtractOpenAIResponseIDFromJSONBytes([]byte(`{"response":{}}`)))
	require.Empty(t, protocolopenai.ExtractOpenAIResponseIDFromJSONBytes([]byte(`not-json`)))
}

// TestExtractOpenAIUsage_CapturesImageInputTokens compares Responses,
// Chat-alias and text-only input usage shapes. It checks image input/output
// quantities separately from aggregate totals and zero image attribution for
// the text fixture.
func TestExtractOpenAIUsage_CapturesImageInputTokens(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":371,"input_tokens_details":{"image_tokens":352,"text_tokens":19},"output_tokens":439,"output_tokens_details":{"image_tokens":439,"text_tokens":0},"total_tokens":810}}`)
	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(body)
	require.True(t, ok)
	require.Equal(t, 371, usage.InputTokens)
	require.Equal(t, 352, usage.ImageInputTokens)
	require.Equal(t, 439, usage.OutputTokens)
	require.Equal(t, 439, usage.ImageOutputTokens)

	// Some upstreams use prompt_tokens; check the prompt_tokens_details fallback.
	promptStyle := []byte(`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"image_tokens":80}}}`)
	pu, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(promptStyle)
	require.True(t, ok)
	require.Equal(t, 100, pu.InputTokens)
	require.Equal(t, 80, pu.ImageInputTokens)

	// Text-only usage leaves ImageInputTokens at zero.
	textOnly := []byte(`{"usage":{"input_tokens":50,"output_tokens":10}}`)
	tu, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(textOnly)
	require.True(t, ok)
	require.Zero(t, tu.ImageInputTokens)
}

// TestExtractOpenAIUsage_ReadsClineDataEnvelope supplies Chat usage inside a
// data wrapper with success metadata. It checks extracted input, output and
// cache-read counts from that specific envelope.
func TestExtractOpenAIUsage_ReadsClineDataEnvelope(t *testing.T) {
	body := []byte(`{"data":{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":8,"completion_tokens":27,"total_tokens":35,"prompt_tokens_details":{"cached_tokens":4}}},"success":true}`)

	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(body)

	require.True(t, ok)
	require.Equal(t, 8, usage.InputTokens)
	require.Equal(t, 27, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
}

// TestExtractOpenAIUsage_ReadsWrappedResponsesDataEnvelope supplies usage
// inside data.response. It checks recognition and exact input, output and
// cached-token quantities from that wrapped Responses fixture.
func TestExtractOpenAIUsage_ReadsWrappedResponsesDataEnvelope(t *testing.T) {
	body := []byte(`{"data":{"response":{"usage":{"input_tokens":11,"output_tokens":5,"total_tokens":16,"input_tokens_details":{"cached_tokens":2}}}}}`)

	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(body)

	require.True(t, ok)
	require.Equal(t, 11, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 2, usage.CacheReadInputTokens)
}

// TestExtractOpenAIUsage_PreservesResponseUsagePriority conflicts data usage
// with native response.usage values. It checks the returned input and output
// counts follow the native response envelope in this fixture.
func TestExtractOpenAIUsage_PreservesResponseUsagePriority(t *testing.T) {
	body := []byte(`{"data":{"usage":{"prompt_tokens":100,"completion_tokens":50}},"response":{"usage":{"input_tokens":11,"output_tokens":5}}}`)

	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(body)

	require.True(t, ok)
	require.Equal(t, 11, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
}

// TestOpenAIStreamingTimeout leaves an upstream pipe empty under a one-second
// idle interval. It checks the idle-timeout diagnostic and OpenAI-compatible
// stream_timeout error payload after the reader returns.
func TestOpenAIStreamingTimeout(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 1, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	start := time.Now()
	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1}}, start, "model", "model", "")
	// Close both pipe ends after timeout to release any scanner still waiting
	// on this empty source; discarded cleanup errors do not alter the timeout
	// and client-payload assertions.
	_ = pw.Close()
	_ = pr.Close()

	if err == nil || !strings.Contains(err.Error(), "stream data interval timeout") {
		t.Fatalf("expected stream timeout error, got %v", err)
	}
	if !strings.Contains(rec.Body.String(), "\"type\":\"error\"") || !strings.Contains(rec.Body.String(), "stream_timeout") {
		t.Fatalf("expected OpenAI-compatible error SSE event, got %q", rec.Body.String())
	}
}

// TestOpenAIStreamingTimeoutTiming separates local timeout output from empty,
// buffered-metadata and already-delivered upstream streams. Real upstream bytes
// retain their first flush, while a local error or failed write creates none.
func TestOpenAIStreamingTimeoutTiming(t *testing.T) {
	cases := []struct {
		name        string // diagnostic scenario name
		prefix      string // upstream bytes before timeout
		failWrite   bool   // reject downstream error bytes
		wantFlush   bool   // upstream delivery was flushed
		wantVisible bool   // useful upstream content arrived
	}{
		{name: "empty"},
		{name: "buffered_metadata", prefix: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\",\"model\":\"model\"}}\n\n", wantFlush: true},
		{name: "first_output", prefix: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", wantFlush: true, wantVisible: true},
		{name: "failed_write", failWrite: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 1, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}})
				router := gin.New()
				router.Use(TextRequestTiming())
				var stages *timing.Stages
				router.POST("/v1/responses", func(c *gin.Context) {
					stages = timing.StagesFromContext(c.Request.Context())
					SetOpsRequestContext(c, "model", true)
					MarkOpsProviderSlotAcquired(c)
					if tt.failWrite {
						c.Writer = timingFailedWriter{ResponseWriter: c.Writer}
					}
					pr, pw := io.Pipe()
					// Keep the pipe open after its prefix so idle expiry, rather than EOF,
					// ends the reader. Closing both ends releases its scanner before test exit.
					defer func() { _ = pr.Close() }()
					defer func() { _ = pw.Close() }()
					go func() { _, _ = io.WriteString(pw, tt.prefix) }()
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: pr}
					timing.ObserveResponse(timing.BeginHTTPTrace(c.Request.Context()), resp)
					target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: providercore.PlatformOpenAI}}
					result, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, target, time.Now(), "model", "model", "")
					require.ErrorContains(t, err, "stream data interval timeout")
					require.NotNil(t, result)
					require.Equal(t, tt.failWrite, result.ClientDisconnected)
				})
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)))
				if tt.failWrite {
					require.Empty(t, recorder.Body.String())
				} else {
					require.Contains(t, recorder.Body.String(), "stream_timeout")
				}
				snapshot := stages.Snapshot()
				_, event := snapshot["upstream_first_event_ms"]
				_, visible := snapshot["first_visible_output_ms"]
				flushAt, flushed := snapshot["first_downstream_flush_ms"]
				require.Equal(t, tt.prefix != "", event)
				require.Equal(t, tt.wantVisible, visible)
				require.Equal(t, tt.wantFlush, flushed)
				if tt.wantVisible {
					require.Zero(t, flushAt)
					require.Contains(t, recorder.Body.String(), "hello")
				} else if tt.wantFlush {
					require.Equal(t, int64(1000), flushAt)
					require.Less(t, strings.Index(recorder.Body.String(), "response.created"), strings.Index(recorder.Body.String(), "stream_timeout"))
				}
			})
		})
	}
}

// TestOpenAIStreamingContextCanceledReturnsIncompleteErrorWithoutInjectingErrorEvent
// uses an already-cancelled context and cancellation reader. It checks
// incomplete-usage error text with no generic SSE or stream-read error
// injection.
func TestOpenAIStreamingContextCanceledReturnsIncompleteErrorWithoutInjectingErrorEvent(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel before reading so this fixture exercises client termination from
	// its first observation, rather than waiting for an upstream failure.
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       cancelReadCloser{},
		Header:     http.Header{},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", "")
	if err == nil || !strings.Contains(err.Error(), "stream usage incomplete") {
		t.Fatalf("expected incomplete stream error, got %v", err)
	}
	if strings.Contains(rec.Body.String(), "event: error") || strings.Contains(rec.Body.String(), "stream_read_error") {
		t.Fatalf("expected no injected SSE error event, got %q", rec.Body.String())
	}
}

// TestOpenAIStreamingReadErrorBeforeOutputReturnsFailover injects unexpected
// EOF before native content. It checks typed HTTP 502 recovery and no
// committed client response or body for this attempt.
func TestOpenAIStreamingReadErrorBeforeOutputReturnsFailover(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       errReadCloser{err: io.ErrUnexpectedEOF},
		Header:     http.Header{"X-Request-Id": []string{"rid-disconnect"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

// TestOpenAIStreamingPostOutputDisconnectQuarantinesSharedProxyWithoutSameStreamFailover
// injects two post-content failures with coalescing disabled. It checks
// retained content, absent same-stream recovery and exclusion by the shared
// proxy diagnostic.
func TestOpenAIStreamingPostOutputDisconnectQuarantinesSharedProxyWithoutSameStreamFailover(t *testing.T) {
	proxyID := int64(4698)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 469801,
			Name:     "oauth-on-shared-proxy",
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			ProxyID:  &proxyID,
		},
	}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Response: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize}}})
	// Disable coalescing for the two fixture disconnects so each observation
	// contributes independently to the shared proxy quarantine threshold being checked.
	svc.Output.ProxyCircuit = egress.NewProxyStreamCircuit(egress.ProxyStreamCircuitSettings{
		FailureThreshold: 2,
		FailureWindow:    time.Minute,
		QuarantineTTL:    10 * time.Minute,
		MaxEntries:       16,
	})

	for _, readErr := range []error{
		io.ErrUnexpectedEOF,
		errors.New("http2: client connection lost"),
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body: &openAIStreamReadThenErrorCloser{
				reader: strings.NewReader(strings.Join([]string{
					"event: response.output_text.delta",
					`data: {"type":"response.output_text.delta","delta":"partial"}`,
					"",
				}, "\n")),
				err: readErr,
			},
			Header: http.Header{"X-Request-Id": []string{"rid-proxy-disconnect"}},
		}

		_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, provider, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol", "")
		require.Error(t, err)
		var failoverErr *forwardcore.UpstreamFailoverError
		require.False(t, errors.As(err, &failoverErr), "post-output disconnect must not fail over inside the same stream")
		require.Contains(t, rec.Body.String(), "partial")
	}

	compatible, reason := selectionDiagnosticForStreamTest(t, svc, provider)
	require.False(t, compatible, "the next request must exclude providers sharing the quarantined proxy")
	require.Equal(t, "proxy_stream_quarantined", reason)
}

// TestOpenAIStreamingTerminalAndClientCancellationDoNotQuarantineProxy
// combines a successful terminal before read failure with two client-cancelled
// streams. It checks the selected provider remains eligible in the post-stream
// proxy diagnostic.
func TestOpenAIStreamingTerminalAndClientCancellationDoNotQuarantineProxy(t *testing.T) {
	proxyID := int64(4699)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 469901, Name: "oauth", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, ProxyID: &proxyID}}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Response: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize}}})

	terminalRecorder := httptest.NewRecorder()
	terminalCtx, _ := gin.CreateTestContext(terminalRecorder)
	terminalCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	terminalResp := &http.Response{
		StatusCode: http.StatusOK,
		Body: &openAIStreamReadThenErrorCloser{
			reader: strings.NewReader(strings.Join([]string{
				"event: response.completed",
				`data: {"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`,
				"",
			}, "\n")),
			err: io.ErrUnexpectedEOF,
		},
		Header: http.Header{},
	}
	_, err := svc.Output.ReadStreamObservation(terminalCtx.Request.Context(), terminalResp, terminalCtx, provider, time.Now(), "model", "model", "")
	require.NoError(t, err)

	for range 2 {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		ctx, cancel := context.WithCancel(context.Background())
		// Cancel each client before its read while injecting the same cancellation
		// error; the later diagnostic checks that repeated client endings do not
		// exclude this provider through the shared proxy.
		cancel()
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body: &openAIStreamReadThenErrorCloser{
				reader: strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"),
				err:    context.Canceled,
			},
			Header: http.Header{},
		}
		_, err = svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, provider, time.Now(), "model", "model", "")
		require.Error(t, err)
	}

	compatible, reason := selectionDiagnosticForStreamTest(t, svc, provider)
	require.True(t, compatible)
	require.Empty(t, reason)
}

// TestOpenAIStreamingResponseFailedBeforeOutputReturnsFailover supplies
// metadata followed by temporary-processing failure. It checks typed recovery,
// captured message, no same-provider retry and no downstream bytes or
// commitment.
func TestOpenAIStreamingResponseFailedBeforeOutputReturnsFailover(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.in_progress",
			`data: {"type":"response.in_progress","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","error":{"message":"An error occurred while processing your request."}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-failed"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameProvider)
	require.Contains(t, string(failoverErr.ResponseBody), "An error occurred while processing your request")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

// TestOpenAIStreamingResponseFailedCapacityBeforeOutputReturnsFailover
// supplies capacity wording to an API-key pool target with an empty retry list.
// It checks that the capacity classifier permits same-provider recovery before
// any client output even when status-code matching is disabled.
func TestOpenAIStreamingResponseFailedCapacityBeforeOutputReturnsFailover(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.failed",
			`data: {"type":"response.failed","response":{"error":{"message":"The selected model is at capacity. Please try again later."}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-capacity"}},
	}

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Name:     "pool-provider",
			Credentials: map[string]any{
				"pool_mode":                    true,
				"pool_mode_retry_status_codes": []any{},
			},
		},
	}
	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, provider, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameProvider)
	require.Contains(t, string(failoverErr.ResponseBody), "selected model is at capacity")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

// TestOpenAIStreamingResponseFailedBeforeOutputServerOverloadedCodeReturnsFailover
// supplies created then an overload-code failure. It checks typed HTTP 503
// recovery, retained message and an untouched client writer/body.
func TestOpenAIStreamingResponseFailedBeforeOutputServerOverloadedCodeReturnsFailover(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","error":{"code":"server_is_overloaded","message":"Please retry later."}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-overloaded-failed"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "Please retry later")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

// TestOpenAIStreamingResponseFailedBeforeOutputRateLimitUsesPoolRetryPolicy
// admits HTTP 429 through pool settings. It checks semantic status,
// same-provider eligibility, Retry-After and rate-limit envelope plus an
// upstream status observation before commitment.
func TestOpenAIStreamingResponseFailedBeforeOutputRateLimitUsesPoolRetryPolicy(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"type":"invalid_request_error","code":"rate_limit_exceeded","message":"Concurrency limit exceeded for provider, please retry later"}}}`,
			"",
		}, "\n"))),
		Header: http.Header{
			"X-Request-Id": []string{"rid-rate-limit-failed"},
			"Retry-After":  []string{"1"},
		},
	}
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Name:     "pool-provider",
			Credentials: map[string]any{
				"pool_mode":                    true,
				"pool_mode_retry_count":        float64(1),
				"pool_mode_retry_status_codes": []any{float64(http.StatusTooManyRequests)},
			},
		},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, provider, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameProvider)
	require.Equal(t, "1", http.Header(failoverErr.ResponseHeaders).Get("Retry-After"))
	require.Equal(t, "rate_limit_error", gjson.GetBytes(failoverErr.ResponseBody, "error.type").String())
	require.Contains(t, string(failoverErr.ResponseBody), "Concurrency limit exceeded")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())

	opsValue, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	opsEvents, ok := opsValue.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.NotEmpty(t, opsEvents)
	require.Equal(t, http.StatusTooManyRequests, opsEvents[len(opsEvents)-1].UpstreamStatusCode)
}

// TestOpenAIStreamingResponseFailedRateLimitDoesNotBlockProviderScheduling
// supplies an OAuth rate-limit terminal beside successful HTTP quota headers.
// It checks same-provider deadline eligibility and no immediate provider
// runtime block in the fixture.
func TestOpenAIStreamingResponseFailedRateLimitDoesNotBlockProviderScheduling(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"rate_limit_exceeded","message":"Concurrency limit exceeded for provider, please retry later"}}}`,
			"",
		}, "\n"))),
		Header: http.Header{
			"X-Codex-Primary-Used-Percent":        []string{"12"},
			"X-Codex-Primary-Reset-After-Seconds": []string{"604800"},
			"Retry-After":                         []string{"1"},
		},
	}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 11, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Name: "oauth-provider"}}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, provider, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameProvider)
	require.False(t, failoverErr.SameProviderRetryDeadline.IsZero())
	require.False(t, httpFixtureRuntimeBlocked(svc, provider))
}

// TestOpenAIStreamingResponseFailedAfterOutputSanitizesVerboseResponseForClient
// follows content with a terminal carrying instructions, output and usage. It
// checks partial usage and reduced client failure fields with no provider
// recovery wrapper.
func TestOpenAIStreamingResponseFailedAfterOutputSanitizesVerboseResponseForClient(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	longInstructions := strings.Repeat("You are GPT-5.1 running in the Codex CLI. ", 20)
	failedPayload := fmt.Sprintf(
		`{"type":"response.failed","response":{"id":"resp_failed","object":"response","created_at":1782446336,"status":"failed","instructions":%q,"output":[{"type":"message","content":[{"type":"output_text","text":"large"}]}],"usage":{"input_tokens":123,"output_tokens":0},"error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again."}}}`,
		longInstructions,
	)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_failed"}}`,
			"",
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"partial"}`,
			"",
			"event: response.failed",
			"data: " + failedPayload,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-failed-after-output"}},
	}

	result, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "流已向客户端输出后不得重放请求")
	require.NotNil(t, result)
	require.Equal(t, 123, result.Usage.InputTokens)

	body := rec.Body.String()
	require.Contains(t, body, "event: response.failed")
	require.Contains(t, body, "context_length_exceeded")
	require.Contains(t, body, `"type":"invalid_request_error"`)
	require.Contains(t, body, "Your input exceeds the context window")
	require.NotContains(t, body, "You are GPT-5.1 running in the Codex CLI")
	require.NotContains(t, body, `"instructions"`)
	require.NotContains(t, body, `"output"`)
	require.NotContains(t, body, `"usage"`)
}

// TestOpenAIStreamingContextWindowResponseFailedBeforeOutputPassesThrough
// supplies metadata followed by context-window rejection. It checks written
// failed output and retained type/message with no typed provider recovery.
func TestOpenAIStreamingContextWindowResponseFailedBeforeOutputPassesThrough(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","error":{"type":"upstream_error","message":"Your input exceeds the context window of this model. Please adjust your input and try again.","code":null}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-context-window-failed"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.True(t, c.Writer.Written())
	require.Contains(t, rec.Body.String(), "response.failed")
	require.Contains(t, rec.Body.String(), `"type":"upstream_error"`)
	require.Contains(t, rec.Body.String(), "Your input exceeds the context window")
}

// TestOpenAIStreamingContextWindowResponseFailedBeforeOutputAppliesPassthroughRule
// matches inferred context failure against an administrator rule. It checks
// committed JSON 400, retained message, absent failed-event framing and an Ops
// observation.
func TestOpenAIStreamingContextWindowResponseFailedBeforeOutputAppliesPassthroughRule(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	rule := gatewaytestkit.NonFailoverRule(http.StatusBadRequest, "context_length_exceeded", http.StatusBadRequest, "")
	rule.Platforms = []string{capability.PlatformOpenAI}
	rule.PassthroughBody = true
	rule.CustomMessage = nil
	ruleSvc := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule})
	BindErrorPassthroughService(c, ruleSvc)

	upstreamMessage := "Your input exceeds the context window of this model. Please adjust your input and try again."
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"` + upstreamMessage + `"}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-context-window-passthrough-rule"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.True(t, IsResponseCommitted(c))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	body := rec.Body.String()
	require.Equal(t, "upstream_error", gjson.Get(body, "error.type").String())
	require.Equal(t, upstreamMessage, gjson.Get(body, "error.message").String())
	require.NotContains(t, body, "response.failed")
	require.NotContains(t, body, "Upstream request failed")
	// A matched passthrough rule still records an Ops upstream error event.
	opsVal, opsRecorded := c.Get(OpsUpstreamErrorsKey)
	require.True(t, opsRecorded, "passthrough hit should record an ops upstream error event")
	opsEvents, _ := opsVal.([]*ops.OpsUpstreamErrorEvent)
	require.NotEmpty(t, opsEvents)
}

// TestOpenAIStreamingPreambleOnlyMissingTerminalReturnsFailover ends after
// created and in-progress metadata. It checks typed provider recovery while
// the client writer remains uncommitted and its body empty.
func TestOpenAIStreamingPreambleOnlyMissingTerminalReturnsFailover(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.in_progress",
			`data: {"type":"response.in_progress","response":{"id":"resp_1"}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-missing-terminal"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

// TestOpenAIStreamingNormalizesTerminalOutputFromDeltas ends with null
// terminal output after two text fragments. It checks a completed terminal
// containing one message with their assembled pong text.
func TestOpenAIStreamingNormalizesTerminalOutputFromDeltas(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_sdk_parse"}}`,
			"",
			`data: {"type":"response.output_text.delta","delta":"pon"}`,
			"",
			`data: {"type":"response.output_text.delta","delta":"g"}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_sdk_parse","status":"completed","output":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-sdk-parse"}},
	}

	result, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	terminalType, terminalPayload, ok := protocolopenai.ExtractOpenAISSETerminalEvent(rec.Body.String())
	require.True(t, ok)
	require.Equal(t, "response.completed", terminalType)
	output := gjson.GetBytes(terminalPayload, "response.output")
	require.True(t, output.IsArray())
	require.Len(t, output.Array(), 1)
	require.Equal(t, "pong", gjson.GetBytes(terminalPayload, "response.output.0.content.0.text").String())
}

// TestOpenAIStreamingNormalizesTerminalOutputToEmptyArray supplies null
// terminal output with no earlier content. It checks a successful completed
// event whose normalized output is an empty JSON array.
func TestOpenAIStreamingNormalizesTerminalOutputToEmptyArray(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"resp_empty","status":"completed","output":null,"usage":{"input_tokens":1,"output_tokens":0}}}` + "\n" + "")),
		Header:     http.Header{"X-Request-Id": []string{"rid-empty-output"}},
	}

	result, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	terminalType, terminalPayload, ok := protocolopenai.ExtractOpenAISSETerminalEvent(rec.Body.String())
	require.True(t, ok)
	require.Equal(t, "response.completed", terminalType)
	output := gjson.GetBytes(terminalPayload, "response.output")
	require.True(t, output.IsArray())
	require.Len(t, output.Array(), 0)
}

// TestOpenAIStreamingPolicyResponseFailedBeforeOutputPassesThrough supplies
// risk-warning text in a failed terminal before content. It checks visible
// failure/message and no typed provider recovery for that request rejection.
func TestOpenAIStreamingPolicyResponseFailedBeforeOutputPassesThrough(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","error":{"type":"safety_error","message":"This request has been flagged for potentially high-risk cyber activity."}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-policy-failed"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.True(t, c.Writer.Written())
	require.Contains(t, rec.Body.String(), "response.failed")
	require.Contains(t, rec.Body.String(), "high-risk cyber activity")
}

// TestOpenAIStreamingPolicyResponseFailedCarriesHTTPStatusWarning wraps
// risk-warning text in an HTTP 200 stream. It checks the error-chain warning
// retains that transport status, message and failed-event evidence.
func TestOpenAIStreamingPolicyResponseFailedCarriesHTTPStatusWarning(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","error":{"type":"safety_error","message":"This request has been flagged for potentially high-risk cyber activity."}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-policy-failed"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")

	require.Error(t, err)
	warning, ok := forwardcore.WarningFromError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, warning.StatusCode)
	require.Contains(t, warning.Message, "high-risk cyber activity")
	require.Contains(t, string(warning.ResponseBody), "response.failed")
}

// TestOpenAIStreamingCybersecurityRiskResponseFailedCarriesHTTPStatusWarning
// supplies the listed cybersecurity warning in nested terminal error text. It
// checks no recovery wrapper and warning-chain HTTP 200, message and event
// evidence.
func TestOpenAIStreamingCybersecurityRiskResponseFailedCarriesHTTPStatusWarning(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"error":{"message":"This content was flagged for possible cybersecurity risk. If this seems wrong, try rephrasing your request."}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-cybersecurity-risk"}},
	}

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")

	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	warning, ok := forwardcore.WarningFromError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, warning.StatusCode)
	require.Contains(t, warning.Message, "cybersecurity risk")
	require.Contains(t, string(warning.ResponseBody), "response.failed")
}

// TestOpenAIStreamingClientDisconnectDrainsUpstreamUsage rejects downstream
// writes while a pipe supplies terminal usage. It checks successful exact
// token observation and absence of injected generic write/error events.
func TestOpenAIStreamingClientDisconnectDrainsUpstreamUsage(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Writer = &httptestkit.FailingWriter{ResponseWriter: c.Writer, FailAfter: 0}

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	// Supply terminal usage despite rejected client writes, then close the pipe
	// to finish upstream reading. Discard producer errors because assertions
	// inspect collected usage and generic error-event suppression.
	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.in_progress\",\"response\":{}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":5,\"input_tokens_details\":{\"cached_tokens\":1}}}}\n\n"))
	}()

	result, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", "")
	// Release the caller-owned reader after observation so a producer cannot
	// remain blocked on writing; cleanup errors are discarded while assertions
	// inspect the stream result.
	_ = pr.Close()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result == nil || result.Usage == nil {
		t.Fatalf("expected usage result")
	}
	if result.Usage.InputTokens != 3 || result.Usage.OutputTokens != 5 || result.Usage.CacheReadInputTokens != 1 {
		t.Fatalf("unexpected usage: %+v", *result.Usage)
	}
	if strings.Contains(rec.Body.String(), "event: error") || strings.Contains(rec.Body.String(), "write_failed") {
		t.Fatalf("expected no injected SSE error event, got %q", rec.Body.String())
	}
}

// TestOpenAIStreamingMissingTerminalEventReturnsIncompleteError closes the
// source pipe after one text delta without a terminal. It checks the reader
// returns the missing-terminal diagnostic for that partial stream.
func TestOpenAIStreamingMissingTerminalEventReturnsIncompleteError(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	// Close after one text delta so EOF supplies the missing-terminal condition.
	// Producer write/close errors are discarded; the synchronous reader error
	// below identifies the outcome of this partial stream.
	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\",\"output_index\":0}\n\n"))
	}()

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", "")
	// Release the caller-owned reader after observation so a producer cannot
	// remain blocked on writing; cleanup errors are discarded while assertions
	// inspect the stream result.
	_ = pr.Close()
	if err == nil || !strings.Contains(err.Error(), "missing terminal event") {
		t.Fatalf("expected missing terminal event error, got %v", err)
	}
}

// TestOpenAIStreamingTooLong supplies a 128-KiB line to a reader configured
// with a 64-KiB ceiling. It checks bufio.ErrTooLong and the response_too_large client
// event after reading fails.
func TestOpenAIStreamingTooLong(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: 64 * 1024}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	// Feed an oversized line from another worker so scanner failure can stop
	// pipe consumption. Discard its write/close errors; reader and client-event
	// assertions identify the size failure.
	go func() {
		defer func() { _ = pw.Close() }()
		// A line larger than MaxLineSize triggers ErrTooLong.
		payload := "data: " + strings.Repeat("a", 128*1024) + "\n"
		_, _ = pw.Write([]byte(payload))
	}()

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 2}}, time.Now(), "model", "model", "")
	// Release the caller-owned reader after observation so a producer cannot
	// remain blocked on writing; cleanup errors are discarded while assertions
	// inspect the stream result.
	_ = pr.Close()

	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("expected ErrTooLong, got %v", err)
	}
	if !strings.Contains(rec.Body.String(), "\"type\":\"error\"") || !strings.Contains(rec.Body.String(), "response_too_large") {
		t.Fatalf("expected OpenAI-compatible error SSE event, got %q", rec.Body.String())
	}
}

// TestOpenAINonStreamingContentTypePassThrough disables response-header
// filtering and supplies a custom JSON content type. It checks successful
// buffered reading and retention of that type on client output.
func TestOpenAINonStreamingContentTypePassThrough(t *testing.T) {
	cfg := &responsesFixtureOptions{Headers: egress.ResponseHeaderOptions{Enabled: false}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	body := []byte(`{"usage":{"input_tokens":1,"output_tokens":2,"input_tokens_details":{"cached_tokens":0}}}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/vnd.test+json"}},
	}

	_, err := svc.Output.NonStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation}}, "model", "model")
	if err != nil {
		t.Fatalf("handleNonStreamingResponse error: %v", err)
	}

	if !strings.Contains(rec.Header().Get("Content-Type"), "application/vnd.test+json") {
		t.Fatalf("expected Content-Type passthrough, got %q", rec.Header().Get("Content-Type"))
	}
}

// TestOpenAINonStreamingContentTypeDefault disables response-header filtering
// and omits upstream content type. It checks successful buffered reading and
// the default JSON type in the client response.
func TestOpenAINonStreamingContentTypeDefault(t *testing.T) {
	cfg := &responsesFixtureOptions{Headers: egress.ResponseHeaderOptions{Enabled: false}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	body := []byte(`{"usage":{"input_tokens":1,"output_tokens":2,"input_tokens_details":{"cached_tokens":0}}}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     http.Header{},
	}

	_, err := svc.Output.NonStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation}}, "model", "model")
	if err != nil {
		t.Fatalf("handleNonStreamingResponse error: %v", err)
	}

	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("expected default Content-Type, got %q", rec.Header().Get("Content-Type"))
	}
}

// TestOpenAIStreamingHeadersOverride supplies upstream custom content/cache
// headers and a request identifier. It checks client SSE/no-cache overrides
// and request-ID relay after successful stream reading.
func TestOpenAIStreamingHeadersOverride(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}, Headers: egress.ResponseHeaderOptions{Enabled: false}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header: http.Header{
			"Cache-Control": []string{"upstream"},
			"X-Request-Id":  []string{"req-123"},
			"Content-Type":  []string{"application/custom"},
		},
	}

	// Feed a bare completed frame and close the pipe for this header fixture.
	// Discard producer write/close errors; the synchronous reader result and
	// captured headers provide its observed outcome.
	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{}}\n\n"))
	}()

	_, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", "")
	// Release the caller-owned reader after observation so a producer cannot
	// remain blocked on writing; cleanup errors are discarded while assertions
	// inspect the stream result.
	_ = pr.Close()
	if err != nil {
		t.Fatalf("handleStreamingResponse error: %v", err)
	}

	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("expected Cache-Control override, got %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected Content-Type override, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("X-Request-Id") != "req-123" {
		t.Fatalf("expected X-Request-Id passthrough, got %q", rec.Header().Get("X-Request-Id"))
	}
}

// TestOpenAIStreamingReuseScannerBufferAndStillWorks supplies a completed
// usage terminal through a pipe. It checks successful token extraction for
// this fixture; the assertions do not inspect internal buffer reuse.
func TestOpenAIStreamingReuseScannerBufferAndStillWorks(t *testing.T) {
	cfg := &responsesFixtureOptions{Response: OpenAIResponseOptions{StreamDataIntervalTimeout: 0, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	// Deliver the usage terminal from a pipe producer and close after the send.
	// Discard producer write/close errors while result assertions compare the
	// extracted counts for this fixture.
	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":3}}}}\n\n"))
	}()

	result, err := svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", "")
	// Release the caller-owned reader after observation so a producer cannot
	// remain blocked on writing; cleanup errors are discarded while assertions
	// inspect the stream result.
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Usage)
	require.Equal(t, 1, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
}

// TestExtractOpenAISSEDataLine enumerates spaced, unspaced, empty and nondata
// lines. It checks the parser's success flag and exact extracted data string
// for each fixture case.
func TestExtractOpenAISSEDataLine(t *testing.T) {
	tests := []struct {
		name     string // fixture case name
		line     string // supplied SSE input line
		wantData string // expected extracted data text
		wantOK   bool   // expected data-line recognition
	}{
		{name: "标准格式", line: `data: {"type":"x"}`, wantData: `{"type":"x"}`, wantOK: true},
		{name: "无空格格式", line: `data:{"type":"x"}`, wantData: `{"type":"x"}`, wantOK: true},
		{name: "纯空数据", line: `data:   `, wantData: ``, wantOK: true},
		{name: "非 data 行", line: `event: message`, wantData: ``, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := protocolopenai.ExtractSSEDataLine(tt.line)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantData, got)
		})
	}
}

// TestExtractOpenAIUsageFromJSONBytes_AcceptsResponseAndChatUsageShapes
// compares native and alias usage fields, explicit nested zeros and reasoning
// totals. It checks token/image/cache quantities and arithmetic-sensitive
// output normalization for the listed shapes.
func TestExtractOpenAIUsageFromJSONBytes_AcceptsResponseAndChatUsageShapes(t *testing.T) {
	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"id":"resp_1","usage":{"input_tokens":9,"output_tokens":5,"input_tokens_details":{"cached_tokens":2,"cache_write_tokens":4}}}`))
	require.True(t, ok)
	require.Equal(t, 9, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 2, usage.CacheReadInputTokens)
	require.Equal(t, 4, usage.CacheCreationInputTokens)

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"type":"response.completed","response":{"usage":{"prompt_tokens":13,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4,"cache_creation_tokens":3,"image_tokens":2},"completion_tokens_details":{"image_tokens":1}}}}`))
	require.True(t, ok)
	require.Equal(t, 13, usage.InputTokens)
	require.Equal(t, 2, usage.ImageInputTokens)
	require.Equal(t, 7, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
	require.Equal(t, 3, usage.CacheCreationInputTokens)
	require.Equal(t, 1, usage.ImageOutputTokens)

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":11,"output_tokens":2,"cache_write_input_tokens":6}}`))
	require.True(t, ok)
	require.Equal(t, 6, usage.CacheCreationInputTokens)

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":20,"output_tokens":2,"cache_creation_input_tokens":19,"input_tokens_details":{"cache_write_tokens":7}}}`))
	require.True(t, ok)
	require.Equal(t, 7, usage.CacheCreationInputTokens, "官方嵌套字段应优先于兼容顶层别名")

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":20,"output_tokens":2,"cache_creation_input_tokens":19,"input_tokens_details":{"cache_write_tokens":0}}}`))
	require.True(t, ok)
	require.Zero(t, usage.CacheCreationInputTokens, "官方嵌套字段显式为零时仍应优先于兼容顶层别名")

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":20,"output_tokens":2,"cache_read_input_tokens":19,"input_tokens_details":{"cached_tokens":0}}}`))
	require.True(t, ok)
	require.Zero(t, usage.CacheReadInputTokens, "官方嵌套缓存读取字段显式为零时仍应优先于兼容顶层别名")

	// Total 10800 accounts for input 10000, visible output 500 and reasoning
	// 300, so normalization adds reasoning. Total 10500 already matches input
	// plus output and leaves the reported output unchanged.
	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":10000,"output_tokens":500,"total_tokens":10800,"output_tokens_details":{"reasoning_tokens":300}}}`))
	require.True(t, ok)
	require.Equal(t, 800, usage.OutputTokens)
	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":10000,"output_tokens":500,"total_tokens":10500,"output_tokens_details":{"reasoning_tokens":300}}}`))
	require.True(t, ok)
	require.Equal(t, 500, usage.OutputTokens)
}

// TestExtractOpenAIUsageFromJSONBytes_IncludesGrokReasoningTokens compares
// visible-plus-reasoning totals with already-inclusive Responses output. It
// checks normalized output quantities so the supplied fixtures count reasoning
// once.
func TestExtractOpenAIUsageFromJSONBytes_IncludesGrokReasoningTokens(t *testing.T) {
	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"prompt_tokens":32,"completion_tokens":9,"total_tokens":135,"completion_tokens_details":{"reasoning_tokens":94}}}`))
	require.True(t, ok)
	require.Equal(t, 103, usage.OutputTokens, "Grok Chat usage bills visible completion plus reasoning tokens")

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":32,"output_tokens":103,"total_tokens":135,"output_tokens_details":{"reasoning_tokens":94}}}`))
	require.True(t, ok)
	require.Equal(t, 103, usage.OutputTokens, "Responses output_tokens already includes reasoning when total confirms it")

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":32,"output_tokens":9,"total_tokens":135,"output_tokens_details":{"reasoning_tokens":94}}}`))
	require.True(t, ok)
	require.Equal(t, 103, usage.OutputTokens, "Responses detail-only shape is normalized when total exposes the full output")
}

// TestExtractCodexFinalResponse_SampleReplay supplies in-progress, completed
// and DONE sample lines. It checks extracted terminal bytes contain the
// expected response identifier and input-token quantity from that replay.
func TestExtractCodexFinalResponse_SampleReplay(t *testing.T) {
	body := strings.Join([]string{
		`event: message`,
		`data: {"type":"response.in_progress","response":{"id":"resp_1"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-4o","usage":{"input_tokens":11,"output_tokens":22,"input_tokens_details":{"cached_tokens":3}}}}`,
		`data: [DONE]`,
	}, "\n")

	finalResp, ok := protocolopenai.ExtractCodexFinalResponse(body)
	require.True(t, ok)
	require.Contains(t, string(finalResp), `"id":"resp_1"`)
	require.Contains(t, string(finalResp), `"input_tokens":11`)
}

// TestHandleNonStreamingResponse_APIKeyFallsBackToSSEBodyWhenContentTypeIsWrong
// supplies SSE bytes under JSON content type. It checks reconstructed hello
// output, response ID, observed usage and removal of client SSE framing.
func TestHandleNonStreamingResponse_APIKeyFallsBackToSSEBodyWhenContentTypeIsWrong(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}})
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"hel"}`,
			`data: {"type":"response.output_text.delta","delta":"lo"}`,
			`data: {"type":"response.completed","response":{"id":"resp_api_key_sse","object":"response","model":"gpt-5.4","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
			`data: [DONE]`,
		}, "\n"))),
	}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.ProviderTypeAPIKey}}

	result, err := svc.Output.NonStream(context.Background(), resp, c, provider, "gpt-5.4", "gpt-5.4")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.NotContains(t, rec.Body.String(), "data:")
	require.Equal(t, "resp_api_key_sse", gjson.Get(rec.Body.String(), "id").String())
	require.Equal(t, "hello", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
}

// TestHandleNonStreamingResponse_OAuthJSONBodyWithDataEventTextKeepsJSONUsage
// places data/event words inside Compact JSON text. It checks JSON content,
// response identity, exact usage and retained text after buffered reading.
func TestHandleNonStreamingResponse_OAuthJSONBodyWithDataEventTextKeepsJSONUsage(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}})
	// Ordinary data:/event: text inside Compact JSON must not cause SSE detection.
	jsonBody := `{"id":"resp_oauth_compact","object":"response","model":"gpt-5.4","status":"completed",` +
		`"output":[{"type":"message","content":[{"type":"output_text",` +
		`"text":"processing data: 1,2,3 then event: click finished"}]}],` +
		`"usage":{"input_tokens":11,"output_tokens":22,"total_tokens":33}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(jsonBody)),
	}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 146, Type: capability.ProviderTypeOAuth}}

	result, err := svc.Output.NonStream(context.Background(), resp, c, provider, "gpt-5.4", "gpt-5.4")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 22, result.Usage.OutputTokens)
	// Return the JSON response with upstream usage.
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Equal(t, "resp_oauth_compact", gjson.Get(rec.Body.String(), "id").String())
	require.Equal(t, int64(33), gjson.Get(rec.Body.String(), "usage.total_tokens").Int())
	require.Contains(t, rec.Body.String(), "processing data: 1,2,3 then event: click finished")
}

// TestOpenAICompatSSEFrameParserResetsEventTypeAtFrameBoundary names one frame
// then supplies unnamed data after its blank boundary. It checks emitted data
// and that the earlier event type does not label the later frame.
func TestOpenAICompatSSEFrameParserResetsEventTypeAtFrameBoundary(t *testing.T) {
	var parser protocolopenai.OpenAICompatSSEFrameParser

	frame, ok := parser.AddLine("event: response.created")
	require.False(t, ok)
	require.Empty(t, frame)

	frame, ok = parser.AddLine(`data: {"response":{"id":"resp_1"}}`)
	require.False(t, ok)
	require.Empty(t, frame)

	frame, ok = parser.AddLine("")
	require.True(t, ok)
	require.Equal(t, "response.created", frame.EventType)
	require.JSONEq(t, `{"response":{"id":"resp_1"}}`, frame.Data)

	frame, ok = parser.AddLine(`data: {"delta":"ok"}`)
	require.False(t, ok)
	require.Empty(t, frame.EventType)

	frame, ok = parser.AddLine("")
	require.True(t, ok)
	require.Empty(t, frame.EventType)
	require.JSONEq(t, `{"delta":"ok"}`, frame.Data)
}

// TestOpenAIGatewayServiceHandleResponsesImageOutputs_NonStreaming supplies
// one image-generation result in JSON. It checks one image count and separate
// aggregate input/output and image-output token observations.
func TestOpenAIGatewayServiceHandleResponsesImageOutputs_NonStreaming(t *testing.T) {
	svc := newOpenAIImageGenerationControlTestService(&auxiliaryHTTPRecorder{})
	c, _ := newOpenAIImageGenerationControlTestContext(true, "unit-test-agent/1.0")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_image_json",
			"model":"gpt-5.4",
			"output":[{"id":"ig_json_1","type":"image_generation_call","result":"final-image"}],
			"usage":{"input_tokens":7,"output_tokens":3,"output_tokens_details":{"image_tokens":2}}
		}`)),
	}

	result, err := svc.Output.NonStream(context.Background(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.ProviderTypeAPIKey}}, "gpt-5.4", "gpt-5.4")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.NotNil(t, result.Usage)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.ImageOutputTokens)
}

// TestOpenAIGatewayServiceHandleResponsesImageOutputs_Streaming repeats one
// completed image in item and terminal events. It checks one image count,
// observed usage and normalized completed statuses in both downstream
// occurrences.
func TestOpenAIGatewayServiceHandleResponsesImageOutputs_Streaming(t *testing.T) {
	svc := newOpenAIImageGenerationControlTestService(&auxiliaryHTTPRecorder{})
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "unit-test-agent/1.0")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_stream_1\",\"type\":\"image_generation_call\",\"status\":\"generating\",\"result\":\"final-image\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_image_stream\",\"model\":\"gpt-5.5\",\"output\":[{\"id\":\"ig_stream_1\",\"type\":\"image_generation_call\",\"status\":\"generating\",\"result\":\"final-image\"}],\"usage\":{\"input_tokens\":11,\"output_tokens\":5,\"output_tokens_details\":{\"image_tokens\":4}}}}\n\n",
		)),
	}

	result, err := svc.Output.ReadStreamObservation(context.Background(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "gpt-5.5", "gpt-5.5", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.NotNil(t, result.Usage)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.ImageOutputTokens)
	require.NotContains(t, recorder.Body.String(), `"status":"generating"`)
	require.Equal(t, 2, strings.Count(recorder.Body.String(), `"status":"completed"`))
}

// TestNormalizeCompletedImageGenerationStatus compares finished, failed,
// missing-result and nonfinal image events. It checks each expected change
// flag and JSON result, including retained Base64 content and failed status.
func TestNormalizeCompletedImageGenerationStatus(t *testing.T) {
	tests := []struct {
		name        string // fixture case name
		input       string // image-event JSON fixture
		want        string // normalized image-event JSON
		wantChanged bool   // expected adopted status rewrite
	}{
		{
			name:        "output item done with result",
			input:       `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"generating","result":"image-data"}}`,
			want:        `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"completed","result":"image-data"}}`,
			wantChanged: true,
		},
		{
			name:        "terminal response only changes completed image result",
			input:       `{"type":"response.completed","response":{"output":[{"type":"image_generation_call","status":"in_progress","result":"image-data"},{"type":"image_generation_call","status":"failed","result":"partial-data"}]}}`,
			want:        `{"type":"response.completed","response":{"output":[{"type":"image_generation_call","status":"completed","result":"image-data"},{"type":"image_generation_call","status":"failed","result":"partial-data"}]}}`,
			wantChanged: true,
		},
		{
			name:        "done item without result",
			input:       `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"generating"}}`,
			want:        `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"generating"}}`,
			wantChanged: false,
		},
		{
			name:        "non-final image event",
			input:       `{"type":"response.output_item.added","item":{"type":"image_generation_call","status":"generating","result":"image-data"}}`,
			want:        `{"type":"response.output_item.added","item":{"type":"image_generation_call","status":"generating","result":"image-data"}}`,
			wantChanged: false,
		},
		{
			name:        "done preserves base64 result",
			input:       `{"type":"response.done","response":{"output":[{"type":"image_generation_call","status":"generating","result":"iVBORw0KGgoAAAANSUhEUg/+=="}]}}`,
			want:        `{"type":"response.done","response":{"output":[{"type":"image_generation_call","status":"completed","result":"iVBORw0KGgoAAAANSUhEUg/+=="}]}}`,
			wantChanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := protocolopenai.NormalizeCompletedImageGenerationStatus([]byte(tt.input))

			require.Equal(t, tt.wantChanged, changed)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

// TestOpenAIGatewayService_NativeOAuth_NamespaceNonStreamingResponse binds a
// reverse namespace name before buffered OAuth output. It checks restored
// namespace/function fields and absence of the combined upstream name.
func TestOpenAIGatewayService_NativeOAuth_NamespaceNonStreamingResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	SetOpenAIResponsesNamespaceNames(c, map[string]bridge.ResponsesNamespaceName{
		"collaboration__spawn_agent": {Namespace: "collaboration", Name: "spawn_agent"},
	})
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_1","output":[{"type":"function_call","name":"collaboration__spawn_agent","call_id":"call_1","arguments":"{}"}],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`)),
	}

	result, err := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}}).Output.NonStream(
		context.Background(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Type: capability.ProviderTypeOAuth}}, "gpt-5.5", "gpt-5.5",
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotContains(t, rec.Body.String(), "collaboration__spawn_agent")
	require.Contains(t, rec.Body.String(), `"name":"spawn_agent"`)
	require.Contains(t, rec.Body.String(), `"namespace":"collaboration"`)
}

// TestResponsesLifecycleCompatibility enumerates bare, legacy,
// repeated-success, failed, incomplete and truncated frames in three reader
// modes. It checks delivered event types, usage, restored client model,
// retained vendor field and sequence presence for supplied successful
// terminals.
func TestResponsesLifecycleCompatibility(t *testing.T) {
	const delta = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello!\"}\n\n"
	const completed = `{"object":"response","id":"resp_compat","model":"upstream-model","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello!","annotations":[]}]}],"usage":{"input_tokens":7,"output_tokens":9},"vendor":{"opaque":"keep"}}`
	frame := func(eventType, payload string) string {
		if eventType == "" {
			return "data: " + payload + "\n\n"
		}
		return "event: " + eventType + "\ndata: " + payload + "\n\n"
	}
	tests := []struct {
		// name identifies the lifecycle fixture and body supplies its input stream.
		// The three reader modes consume the same body while types, output and
		// wantError describe their expected delivered observations.
		// name: fixture lifecycle case name.
		// body: upstream lifecycle SSE bytes.
		name, body string
		types      []string // expected delivered lifecycle event types
		wantError  bool     // expected returned read failure
		output     int      // expected observed output tokens
	}{
		{
			name: "bare lifecycle followed by duplicate done",
			body: frame("response.created", `{"object":"response","id":"resp_compat","status":"in_progress","output":[]}`) +
				frame("response.in_progress", `{"object":"response","id":"resp_compat","status":"in_progress","output":[]}`) + delta +
				frame("response.completed", strings.Replace(completed, `"output_tokens":9`, `"output_tokens":3`, 1)) +
				frame("response.done", `{"type":"response.done","response":`+completed+`}`),
			types:  []string{"response.created", "response.in_progress", "response.output_text.delta", "response.completed"},
			output: 9,
		},
		{
			name:  "done only",
			body:  frame("response.done", `{"type":"response.done","response":`+completed+`}`),
			types: []string{"response.completed"}, output: 9,
		},
		{
			name:  "data only done",
			body:  delta + frame("", `{"type":"response.done","response":`+completed+`}`),
			types: []string{"response.output_text.delta", "response.completed"}, output: 9,
		},
		{
			name:  "standard completed",
			body:  delta + frame("response.completed", `{"type":"response.completed","sequence_number":10,"response":`+completed+`}`),
			types: []string{"response.output_text.delta", "response.completed"}, output: 9,
		},
		{
			name:  "failed done after output",
			body:  delta + frame("response.done", `{"type":"response.done","response":{"id":"resp_compat","status":"failed","error":{"code":"server_error","message":"upstream failed"},"usage":{"input_tokens":7,"output_tokens":9}}}`),
			types: []string{"response.output_text.delta", "response.failed"}, wantError: true, output: 9,
		},
		{
			name:  "incomplete done",
			body:  delta + frame("response.done", `{"type":"response.done","response":{"id":"resp_compat","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":7,"output_tokens":9}}}`),
			types: []string{"response.output_text.delta", "response.incomplete"}, output: 9,
		},
		{
			name:  "error without status",
			body:  delta + frame("response.done", `{"type":"response.done","response":{"id":"resp_compat","error":{"code":"server_error","message":"upstream failed"},"usage":{"input_tokens":7,"output_tokens":9}}}`),
			types: []string{"response.output_text.delta", "response.failed"}, wantError: true, output: 9,
		},
		{
			name:  "incomplete without status",
			body:  delta + frame("response.done", `{"type":"response.done","response":{"id":"resp_compat","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":7,"output_tokens":9}}}`),
			types: []string{"response.output_text.delta", "response.incomplete"}, output: 9,
		},
		{
			name: "truncated stream stays failed",
			body: delta, types: []string{"response.output_text.delta"}, wantError: true,
		},
	}
	for _, mode := range []string{"standard", "async", "passthrough"} {
		for _, tt := range tests {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				options := &wsFixtureOptions{Output: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
				if mode == "async" {
					options.Output.StreamDataIntervalTimeout = 30
				}
				svc := newWSFixture(wsFixtureInputs{options: options, corrector: responseupstream.NewCodexToolCorrector()})
				target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tt.body))}
				var result *responseupstream.StreamingResult
				var err error
				if mode == "passthrough" {
					result, err = responseupstream.ReadPassthroughStreaming(c.Request.Context(), resp, upstreamcore.NewOutputContext(ResponseSink{Writer: c.Writer}), svc.Output.PassthroughOptions(c.Request.Context(), c, target), time.Now(), "client-model", "upstream-model")
				} else {
					result, err = svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, target, time.Now(), "client-model", "upstream-model", "")
				}
				if tt.wantError {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				require.NotNil(t, result)
				require.Equal(t, tt.output, result.Usage.OutputTokens)
				assertOpenAISSEFrames(t, recorder.Body.String(), tt.types)
				// Inspect retained model, vendor and sequence fields when the delivered
				// JSON declares completion, independently of the event-line label
				// supplied to this callback.
				protocolopenai.ForEachOpenAISSEFrame(recorder.Body.String(), func(_ string, data []byte) {
					if gjson.GetBytes(data, "type").String() == "response.completed" {
						require.Equal(t, "completed", gjson.GetBytes(data, "response.status").String())
						require.Equal(t, "client-model", gjson.GetBytes(data, "response.model").String())
						require.Equal(t, "keep", gjson.GetBytes(data, "response.vendor.opaque").String())
						require.True(t, gjson.GetBytes(data, "sequence_number").Exists())
					}
				})
			})
		}
	}
}

// TestResponsesLifecycleErrorThenFailedDone follows text and a bare error with
// legacy failed done under three reader modes. It checks one normalized failed
// terminal and retained output usage with or without an event header.
func TestResponsesLifecycleErrorThenFailedDone(t *testing.T) {
	for _, mode := range []string{"standard", "async", "passthrough"} {
		for _, header := range []string{"", "event: response.done\n"} {
			t.Run(mode+"/"+header, func(t *testing.T) {
				body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
					"event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"server_error\",\"message\":\"failed\"}}\n\n" +
					header + "data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_review\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"failed\"},\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				options := &wsFixtureOptions{Output: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize}}
				if mode == "async" {
					options.Output.StreamDataIntervalTimeout = 30
				}
				svc := newWSFixture(wsFixtureInputs{options: options, corrector: responseupstream.NewCodexToolCorrector()})
				target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}}
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
				var result *responseupstream.StreamingResult
				var err error
				if mode == "passthrough" {
					result, err = responseupstream.ReadPassthroughStreaming(c.Request.Context(), resp, upstreamcore.NewOutputContext(ResponseSink{Writer: c.Writer}), svc.Output.PassthroughOptions(c.Request.Context(), c, target), time.Now(), "model", "model")
				} else {
					result, err = svc.Output.ReadStreamObservation(c.Request.Context(), resp, c, target, time.Now(), "model", "model", "")
				}
				require.ErrorContains(t, err, "failed")
				require.NotNil(t, result)
				require.Equal(t, 1, result.Usage.OutputTokens)
				assertOpenAISSEFrames(t, recorder.Body.String(), []string{"response.output_text.delta", "response.failed"})
			})
		}
	}
}

// compileHTTPFixtureHeaders compiles the fixture's configured response header
// policy. A nil options pointer returns nil rather than compiling a filter,
// allowing the output binding's explicit no-filter behavior to be exercised.
func compileHTTPFixtureHeaders(options *responsesFixtureOptions) *egress.CompiledHeaderFilter {
	if options == nil {
		return nil
	}
	return egress.CompileHeaderFilter(options.Headers)
}

// TestOpenAIStreamingRepairsConcatenatedJSONDocumentsInSingleDataLine
// delegates the paired-document fixture to native synchronous reading. It
// checks the shared scenario's repaired frames and observations with
// asynchronous interval scanning disabled.
func TestOpenAIStreamingRepairsConcatenatedJSONDocumentsInSingleDataLine(t *testing.T) {
	testOpenAIStreamingRepairsConcatenatedJSONDocuments(t, false, 0)
}

// TestOpenAIStreamingAsyncScannerRepairsConcatenatedJSONDocumentsInSingleDataLine
// delegates the same paired-document fixture to native interval scanning. It
// checks the shared repaired stream scenario with a thirty-second async read
// interval enabled.
func TestOpenAIStreamingAsyncScannerRepairsConcatenatedJSONDocumentsInSingleDataLine(t *testing.T) {
	testOpenAIStreamingRepairsConcatenatedJSONDocuments(t, false, 30)
}

// TestSplitOpenAIConcatenatedJSONDocumentsRejectsPayloadOverRepairLimit
// exceeds the repair cap with a padded first document. It checks no adopted
// split and one exact original data line through a separately enlarged
// scanner.
func TestSplitOpenAIConcatenatedJSONDocumentsRejectsPayloadOverRepairLimit(t *testing.T) {
	first := `{"type":"response.in_progress","padding":"` + strings.Repeat("x", 16*1024*1024) + `"}`
	second := `{"type":"response.completed"}`
	payload := first + second

	documents, repaired := protocolopenai.SplitConcatenatedJSONDocuments([]byte(payload))
	require.False(t, repaired)
	require.Nil(t, documents)

	line := "data: " + payload
	scanner := bufio.NewScanner(strings.NewReader(line))
	scanner.Buffer(make([]byte, 1024), len(line)+1)
	documentScanner := protocolopenai.NewSSEJSONDocumentScanner(scanner)
	require.True(t, documentScanner.Scan())
	require.Equal(t, line, documentScanner.Text())
	require.False(t, documentScanner.Scan())
	require.NoError(t, documentScanner.Err())
}

// TestOpenAIVisibleOutputClassification enumerates metadata, text, tool,
// image, terminal and DONE payloads. It checks each listed visible-output
// predicate independently of writer commitment and elapsed first-token
// measurement.
func TestOpenAIVisibleOutputClassification(t *testing.T) {
	tests := []struct {
		name      string // fixture case name
		data      string // upstream SSE data payload
		eventType string // declared SSE event type
		want      bool   // expected visible-output verdict
	}{
		{name: "keepalive", data: `{"type":"keepalive"}`, want: false},
		{name: "created", data: `{"type":"response.created"}`, want: false},
		{name: "empty output item", data: `{"type":"response.output_item.added","item":{"id":"item_test","type":"reasoning","summary":[]}}`, want: false},
		{name: "empty delta", data: `{"type":"response.output_text.delta","delta":""}`, want: false},
		{name: "text delta", data: `{"type":"response.output_text.delta","delta":"test output"}`, want: true},
		{name: "tool arguments", data: `{"type":"response.function_call_arguments.delta","delta":"{}"}`, want: true},
		{name: "partial image", data: `{"type":"response.image_generation_call.partial_image","partial_image_b64":"dGVzdA=="}`, want: true},
		{name: "completed image item", data: `{"type":"response.output_item.done","item":{"id":"item_test","type":"image_generation_call","result":"dGVzdA=="}}`, want: true},
		{name: "empty completed", data: `{"type":"response.completed","response":{"id":"resp_test","output":[]}}`, want: false},
		{name: "completed with output usage only", data: `{"type":"response.completed","response":{"id":"resp_test","usage":{"input_tokens":1,"output_tokens":2}}}`, want: false},
		{name: "completed with text", data: `{"type":"response.completed","response":{"id":"resp_test","output":[{"type":"message","content":[{"type":"output_text","text":"test output"}]}]}}`, want: true},
		{name: "done marker", data: `[DONE]`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, protocolopenai.StreamDataStartsVisibleOutput(tt.data, tt.eventType))
		})
	}
}

// TestOpenAIResponsesTTFTStartsAtVisibleOutput delays a text delta by 120 ms
// behind metadata in both reader modes. It checks visible first-token timing
// is present and meets a 100-ms lower bound.
func TestOpenAIResponsesTTFTStartsAtVisibleOutput(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			result := runSyntheticVisibleTTFTStream(t, passthrough, 120*time.Millisecond, 0,
				`{"type":"response.output_text.delta","delta":"test output"}`)
			require.NotNil(t, result.TTFTMs())
			// Leave a twenty-millisecond gap below the 120-ms visible-event delay.
			require.GreaterOrEqual(t, *result.TTFTMs(), 100)
		})
	}
}

// TestOpenAIResponsesTTFTStartsAtCompletedImage delays a completed image item
// by 120 ms behind metadata in both reader modes. It verifies that an image
// artifact alone leaves the shared text TTFT measurement absent.
func TestOpenAIResponsesTTFTStartsAtCompletedImage(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			result := runSyntheticVisibleTTFTStream(t, passthrough, 120*time.Millisecond, 0,
				`{"type":"response.output_item.done","item":{"id":"item_test","type":"image_generation_call","result":"dGVzdA=="}}`)
			require.Nil(t, result.TTFTMs(), "image artifacts do not establish text performance")
		})
	}
}

// TestOpenAINativeMetadataDoesNotDisarmFirstOutputTimeout supplies only
// created and empty reasoning metadata before expiry. It checks safe typed
// recovery, absent client body and eventual local writer exit.
func TestOpenAINativeMetadataDoesNotDisarmFirstOutputTimeout(t *testing.T) {
	svc := newResponsesFixture(responsesFixtureInputs{readers: newHTTPReadersFixture(&gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{gateway.SettingKeyTTFTMode: gateway.TTFTModeFirstOutput}}, nil), options: &responsesFixtureOptions{Response: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize, OpenAIFirstOutputTimeoutSeconds: 1}}})
	reader, writer := io.Pipe()
	writerDone := make(chan struct{})
	// Send only created and empty-reasoning metadata, then keep EOF unavailable
	// beyond the first-output budget. Discard producer errors from cancellation
	// and publish writerDone after deferred pipe cleanup.
	go func() {
		defer close(writerDone)
		defer func() { _ = writer.Close() }()
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\"}}\n\n")
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_test\",\"type\":\"reasoning\",\"summary\":[]}}\n\n")
		// Keep EOF unavailable past the one-second budget so metadata alone cannot
		// make this timeout case finish by ordinary source exhaustion.
		time.Sleep(1200 * time.Millisecond)
	}()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Name: "provider_test", Platform: capability.PlatformOpenAI}}

	_, err := svc.Output.ReadStreamObservation(context.Background(), resp, c, provider, time.Now(), "test-model", "test-model", "")
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.SafeToFailoverAfterWrite)
	require.Empty(t, recorder.Body.String())
	// Wait one second for deferred pipe closure and the producer notification;
	// the reader result by itself does not establish that this producer finished.
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("synthetic upstream writer did not exit")
	}
}

// runSyntheticVisibleTTFTStream supplies two metadata events, delayed
// visibleEvent and terminal usage through a pipe. Initial writes block until
// the selected native or passthrough reader starts, so the visible delay
// follows metadata reading rather than asynchronous fixture construction.
// Callers supply valid JSON event text; timeoutSeconds selects the reader
// first-output budget.
//
// The helper installs the shared request collector for both reader modes and
// returns its performance snapshot after asserting output and waiting for writer
// exit. Producer errors are discarded while the read and output assertions
// observe the exchange, and a one-second wait bounds writer-exit observation.
func runSyntheticVisibleTTFTStream(t *testing.T, passthrough bool, visibleDelay time.Duration, timeoutSeconds int, visibleEvent string) *timing.Performance {
	t.Helper()

	svc := newResponsesFixture(responsesFixtureInputs{readers: newHTTPReadersFixture(&gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{gateway.SettingKeyTTFTMode: gateway.TTFTModeFirstOutput}}, nil), options: &responsesFixtureOptions{Response: OpenAIResponseOptions{MaxLineSize: OpenAIResponseDefaultMaxLineSize, OpenAIFirstOutputTimeoutSeconds: timeoutSeconds}}})
	reader, writer := io.Pipe()
	writerDone := make(chan struct{})
	// Publish metadata before delaying visible output, then send terminal usage.
	// Discard write/close errors while output assertions assess the exchange;
	// deferred pipe closure precedes writerDone for the final exit wait.
	go func() {
		defer close(writerDone)
		defer func() { _ = writer.Close() }()
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\"}}\n\n")
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_test\",\"type\":\"reasoning\",\"summary\":[]}}\n\n")
		time.Sleep(visibleDelay)
		_, _ = io.WriteString(writer, "data: "+visibleEvent+"\n\n")
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Name: "provider_test", Platform: capability.PlatformOpenAI}}
	started := time.Now()
	TextRequestTiming()(c)
	observedCtx := timing.BeginHTTPTrace(c.Request.Context())
	resp.Header.Set("Content-Type", "text/event-stream")
	timing.ObserveResponse(observedCtx, resp)

	var result *responseupstream.StreamingResult
	var err error
	if passthrough {
		var passthroughResult *responseupstream.StreamingResult
		passthroughResult, err = responseupstream.ReadPassthroughStreaming(context.Background(), resp, upstreamcore.NewOutputContext(ResponseSink{Writer: c.Writer}), svc.Output.PassthroughOptions(context.Background(), c, provider), started, "test-model", "test-model")
		if passthroughResult != nil {
			result = passthroughResult
		}
	} else {
		result, err = svc.Output.ReadStreamObservation(context.Background(), resp, c, provider, started, "test-model", "test-model", "")
	}
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, recorder.Body.String(), `"type":"response.output_item.added"`)
	require.Contains(t, recorder.Body.String(), visibleEvent)
	// Wait one second for deferred pipe closure and the producer notification;
	// the reader result by itself does not establish that this producer finished.
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("synthetic upstream writer did not exit")
	}
	return timing.PerformanceFromContext(observedCtx)
}

// GetProvider returns the address of this value receiver's provider copy,
// ignoring lookup ID. Nested maps and pointers remain shared with its stored
// fixture value, so diagnostic callers keep those referenced values stable.
func (s streamSelectionDiagnosticSource) GetProvider(context.Context, int64) (*gatewayprovider.ExecutionProvider, error) {
	return &s.value, nil
}

// GetGroup returns the address of this value receiver's group copy, ignoring
// lookup ID. Nested group references remain shared; the diagnostic fixture
// supplies its active hydrated group before this synchronous lookup.
func (s streamSelectionDiagnosticSource) GetGroup(context.Context, int64) (*routing.Group, error) {
	return &s.group, nil
}

// ListProvidersForSchedulerScoreFilter returns one provider value copy and
// ignores filtering inputs. The result slice is new, while nested references
// retain fixture storage and stay stable for the synchronous scheduler
// diagnostic.
func (s streamSelectionDiagnosticSource) ListProvidersForSchedulerScoreFilter(context.Context, string, string, string, string, int64, string) ([]gatewayprovider.ExecutionProvider, error) {
	return []gatewayprovider.ExecutionProvider{s.value}, nil
}

// ListSchedulableProvidersForAdvancedSchedulerScore returns one provider value
// copy for the supplied diagnostic, ignoring filter arguments. Its new slice
// retains nested provider references from the fixture, which callers keep
// stable while reading.
func (s streamSelectionDiagnosticSource) ListSchedulableProvidersForAdvancedSchedulerScore(context.Context, *int64, string) ([]gatewayprovider.ExecutionProvider, error) {
	return []gatewayprovider.ExecutionProvider{s.value}, nil
}

// selectionDiagnosticForStreamTest prepares active/group fixture copies before
// querying shared runtime/model/proxy eligibility. Nested provider references
// remain shared, so callers keep the source stable during this synchronous
// diagnostic; the result returns eligible status and joined hard-filter
// reasons.
func selectionDiagnosticForStreamTest(t *testing.T, source *OpenAIResponsesExecutor, value *gatewayprovider.ExecutionProvider) (bool, string) {
	t.Helper()
	copy := *value
	copy.Record.Status = "active"
	copy.Record.Schedulable = true
	copy.Record.GroupIDs = []int64{1}
	projection := streamSelectionDiagnosticSource{value: copy, group: routing.Group{ID: 1, Status: "active", Hydrated: true, SchedulerType: routing.GroupSchedulerTypeAdvanced}}
	parameters := scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, scheduler.DefaultParameters())
	choices := selectionadapter.NewCompatible(selectionadapter.CompatibleDependencies{Responses: source.Lineage.Store, RuntimeBlocks: source.Output.Health.Runtime, ModelTransient: source.Output.Health.ModelTransient, ProxyCircuit: source.Output.ProxyCircuit}, selectionadapter.DefaultOptions())
	diagnostic := selectionadapter.NewDiagnostics(projection, selectionadapter.Shared{Parameters: parameters}, nil, choices)
	result, err := diagnostic.GetDetail(context.Background(), value.Record.ID, policy.AdvancedSchedulerScoreDiagnosticRequest{GroupID: 1})
	require.NoError(t, err)
	require.NotNil(t, result.Detail)
	return result.Detail.Eligible, strings.Join(result.Detail.HardFilterReasons, ",")
}

// TestStreamSinkDoesNotClaimOwnershipAfterRacingHeartbeat reproduces commitment
// between the reader's Written check and actual header application. Provider
// turn-state ownership must remain unknown when heartbeat headers won that race.
func TestStreamSinkDoesNotClaimOwnershipAfterRacingHeartbeat(t *testing.T) {
	runSSEKeepaliveTest(t, protocol.ProtocolOpenAIResponses, "", true, 0, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
		c.Request.Header.Set("session-id", "staged-race")
		c.Set("api_key", &apikey.APIKey{ID: 7})
		origins := session.NewCodexTurnOrigins(time.Now)
		fixture := newResponsesFixture(responsesFixtureInputs{})
		fixture.Output.Turns = &CodexTurnStateHeaders{Origins: origins}
		target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{ID: 42, Platform: capability.PlatformOpenAI}}
		writer, ok := c.Writer.(*sseKeepaliveWriter)
		require.True(t, ok)
		writer.openHeaderWait(c.Request.Context())
		output := upstreamcore.NewOutputContext(fixture.Output.streamSink(c, target))
		output.Writer.Header().Set(CodexTurnStateHeader, "undelivered-state")
		require.False(t, output.Writer.Written())
		// The reader can be preempted after checking Written and before applying headers.
		// Advance the heartbeat now to exercise that otherwise tiny scheduling window.
		time.Sleep(15 * time.Millisecond)
		synctest.Wait()
		_, err := output.Writer.Write([]byte("data: {}\n\n"))
		require.NoError(t, err)
		_, known := origins.Owner("7\x00staged-race")
		require.False(t, known, "undelivered header must not claim provider ownership")
		require.Empty(t, rec.Header().Get(CodexTurnStateHeader))
	})
}

// TestStreamSinkRecordsAppliedHeaderOwnership delivers provider headers before
// any waiting heartbeat commits HTTP. The actual header and remembered owner
// must agree, so provider switching can later reject foreign echoed turn state.
func TestStreamSinkRecordsAppliedHeaderOwnership(t *testing.T) {
	runSSEKeepaliveTest(t, protocol.ProtocolOpenAIResponses, "", true, 0, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
		c.Request.Header.Set("session-id", "applied-header")
		c.Set("api_key", &apikey.APIKey{ID: 7})
		origins := session.NewCodexTurnOrigins(time.Now)
		fixture := newResponsesFixture(responsesFixtureInputs{})
		fixture.Output.Turns = &CodexTurnStateHeaders{Origins: origins}
		target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{ID: 42, Platform: capability.PlatformOpenAI}}
		output := upstreamcore.NewOutputContext(fixture.Output.streamSink(c, target))
		output.Writer.Header().Set(CodexTurnStateHeader, "delivered-state")
		_, err := output.Writer.Write([]byte("data: {}\n\n"))
		require.NoError(t, err)
		owner, known := origins.Owner("7\x00applied-header")
		require.True(t, known)
		require.Equal(t, int64(42), owner)
		require.Equal(t, "delivered-state", rec.Result().Header.Get(CodexTurnStateHeader))
	})
}

// installDownstreamKeepaliveTest binds the text controller to a direct reader fixture.
// Call the returned stop function before inspecting its recorder; cleanup also
// stops the worker if an assertion ends the test before normal reader return.
func installDownstreamKeepaliveTest(t *testing.T, c *gin.Context, interval time.Duration) func() {
	t.Helper()
	ctx := requeststate.WithClientProtocol(c.Request.Context(), protocol.ProtocolOpenAIResponses)
	ctx, stages := timing.WithStages(ctx, time.Now(), nil)
	stages.SetStream(true)
	c.Request = c.Request.WithContext(ctx)
	w := &sseKeepaliveWriter{
		ResponseWriter: c.Writer, c: c, stages: stages, interval: interval,
		lastWrite: time.Now(), atBoundary: true, anthropicBlockIndex: -1,
		wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
	c.Writer = w
	c.Set(sseKeepaliveWriterKey, w)
	w.openHeaderWait(ctx)
	t.Cleanup(w.close)
	return w.close
}
