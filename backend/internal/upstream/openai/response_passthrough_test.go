package openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// passthroughTestSink records output alongside buffered passthrough callback effects.
type passthroughTestSink struct {
	// calls belongs to one test invocation, which reads the sequence after the
	// synchronous reader returns and all output callbacks have completed.
	calls *[]string
	heads []upstream.OutputHead // emitted response metadata
	// bodies retains independent payload copies for assertions after Emit returns,
	// when the reader can release or reuse its borrowed byte storage.
	bodies [][]byte
}

// TestPassthroughBufferedToolRestorationOrder supplies namespace tool output as
// JSON and SSE through the nonstreaming entry point. It checks restoration
// order, replacement storage, header timing, usage observation, and compact delivery.
func TestPassthroughBufferedToolRestorationOrder(t *testing.T) {
	for _, asSSE := range []bool{false, true} {
		for _, compactBridge := range []bool{false, true} {
			t.Run(fmt.Sprintf("sse=%t/bridge=%t", asSSE, compactBridge), func(t *testing.T) {
				body := passthroughToolFixture()
				input := passthroughFixtureBody(body, asSSE)
				var calls []string
				var namespaceBody, namedBody, clientBody []byte
				options := passthroughTestOptions(&calls)
				options.NonStream.SupplementCompaction = func(payload []byte, stream string) []byte {
					calls = append(calls, "supplement")
					require.Equal(t, string(input), stream)
					require.Equal(t, "upstream-model", gjson.GetBytes(payload, "model").String())
					return bytes.ReplaceAll(payload, []byte("upstream_encrypted"), []byte("supplemented_encrypted"))
				}
				options.NonStream.CorrectToolCalls = func(payload []byte) []byte {
					calls = append(calls, "correct")
					require.Equal(t, "client-model", gjson.GetBytes(payload, "model").String())
					return bytes.ReplaceAll(payload, []byte("flat_tool"), []byte("corrected_tool"))
				}
				options.RestoreNamespace = func(payload []byte) ([]byte, error) {
					calls = append(calls, "namespace")
					require.Equal(t, "client-model", gjson.GetBytes(payload, "model").String())
					name := "flat_tool"
					if asSSE {
						name = "corrected_tool"
					}
					require.Equal(t, name, gjson.GetBytes(payload, "output.0.name").String())
					namespaceBody = bytes.ReplaceAll(payload, []byte(`"name":"`+name+`"`), []byte(`"name":"reserved_tool","namespace":"functions"`))
					// Changing visible usage during restoration exposes whether accounting
					// was captured from the upstream object before client identity rewrites.
					namespaceBody = bytes.ReplaceAll(namespaceBody, []byte(`"input_tokens":7`), []byte(`"input_tokens":999`))
					return namespaceBody, nil
				}
				options.NonStream.RestoreToolNames = func(payload []byte) []byte {
					calls = append(calls, "names")
					require.Same(t, &namespaceBody[0], &payload[0])
					require.Equal(t, "functions", gjson.GetBytes(payload, "output.0.namespace").String())
					namedBody = bytes.ReplaceAll(payload, []byte("reserved_tool"), []byte("client_tool"))
					return namedBody
				}
				options.NonStream.RestoreOpenAITools = func(payload []byte) ([]byte, error) {
					calls = append(calls, "openai")
					require.Same(t, &namedBody[0], &payload[0])
					require.Equal(t, "client_tool", gjson.GetBytes(payload, "output.0.name").String())
					clientBody = bytes.ReplaceAll(payload, []byte("function_call"), []byte("custom_tool_call"))
					clientBody = bytes.ReplaceAll(clientBody, []byte("fc_tool"), []byte("ctc_tool"))
					clientBody = bytes.ReplaceAll(clientBody, []byte("resp_upstream"), []byte("resp_client"))
					return clientBody, nil
				}
				expected := bytes.ReplaceAll(body, []byte("upstream-model"), []byte("client-model"))
				expected = bytes.ReplaceAll(expected, []byte(`"name":"flat_tool"`), []byte(`"name":"client_tool","namespace":"functions"`))
				expected = bytes.ReplaceAll(expected, []byte(`"input_tokens":7`), []byte(`"input_tokens":999`))
				expected = bytes.ReplaceAll(expected, []byte("function_call"), []byte("custom_tool_call"))
				expected = bytes.ReplaceAll(expected, []byte("fc_tool"), []byte("ctc_tool"))
				expected = bytes.ReplaceAll(expected, []byte("resp_upstream"), []byte("resp_client"))
				if asSSE {
					expected = bytes.ReplaceAll(expected, []byte("upstream_encrypted"), []byte("supplemented_encrypted"))
				}
				options.NonStream.WriteCompactBridge = func(status int, payload []byte) bool {
					calls = append(calls, "bridge")
					require.Equal(t, http.StatusAccepted, status)
					require.Same(t, &clientBody[0], &payload[0])
					require.Equal(t, expected, payload)
					return compactBridge
				}

				result, sink, err := readPassthroughFixture(input, asSSE, options, &calls)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, &wire.ForwardUsage{InputTokens: 7, OutputTokens: 11}, result.Usage)
				require.Equal(t, "resp_client", result.ResponseID)
				require.False(t, result.Served)
				require.Zero(t, result.SearchCount)
				require.Zero(t, result.ImageCount)
				require.Empty(t, result.ImageOutputSizes)
				wantCalls := []string{"read", "tier", "missing:json", "headers", "namespace", "names", "openai", "bridge"}
				contentType := "application/vnd.passthrough+json"
				if asSSE {
					wantCalls = []string{"read", "sse", "supplement", "correct", "namespace", "names", "openai", "headers", "missing:response.completed", "bridge"}
					contentType = "application/json; charset=utf-8"
				}
				if compactBridge {
					require.Empty(t, sink.heads)
					require.Empty(t, sink.bodies)
				} else {
					wantCalls = append(wantCalls, "begin", "emit")
					require.Len(t, sink.heads, 1)
					require.Equal(t, http.StatusAccepted, sink.heads[0].Status)
					require.Equal(t, contentType, sink.heads[0].Header.Get("Content-Type"))
					require.Equal(t, "copied", sink.heads[0].Header.Get("X-Passthrough-Test"))
					require.Equal(t, [][]byte{expected}, sink.bodies)
				}
				require.Equal(t, wantCalls, calls)
				require.Equal(t, passthroughFixtureBody(body, asSSE), input)
			})
		}
	}
}

// TestPassthroughBufferedToolRestorationFailure supplies errors from each
// fallible restoration callback. It checks the first error's wrapping, stopped
// callbacks, and the distinct header preparation timing for JSON and SSE.
func TestPassthroughBufferedToolRestorationFailure(t *testing.T) {
	for _, asSSE := range []bool{false, true} {
		for _, failNamespace := range []bool{false, true} {
			t.Run(fmt.Sprintf("sse=%t/namespace=%t", asSSE, failNamespace), func(t *testing.T) {
				var calls []string
				options := passthroughTestOptions(&calls)
				cause := errors.New("restoration failed")
				callbackErr := fmt.Errorf("callback: %w", cause)
				options.RestoreNamespace = func(payload []byte) ([]byte, error) {
					calls = append(calls, "namespace")
					if failNamespace {
						return []byte("failed namespace output"), callbackErr
					}
					return payload, nil
				}
				options.NonStream.RestoreOpenAITools = func([]byte) ([]byte, error) {
					calls = append(calls, "openai")
					return []byte("failed client tool output"), callbackErr
				}
				result, sink, err := readPassthroughFixture(passthroughFixtureBody(passthroughToolFixture(), asSSE), asSSE, options, &calls)
				message := "restore OpenAI Responses client tools"
				if failNamespace {
					message = "restore OpenAI passthrough namespace response"
				}
				require.EqualError(t, err, message+": callback: restoration failed")
				require.ErrorIs(t, err, cause)
				require.Same(t, callbackErr, errors.Unwrap(err))
				require.Nil(t, result)
				require.Empty(t, sink.heads)
				require.Empty(t, sink.bodies)
				wantCalls := []string{"read", "tier", "missing:json", "headers", "namespace"}
				if asSSE {
					wantCalls = []string{"read", "sse", "supplement", "correct", "namespace"}
				}
				if !failNamespace {
					wantCalls = append(wantCalls, "names", "openai")
				}
				require.Equal(t, wantCalls, calls)
			})
		}
	}
}

// TestPassthroughBufferedToolRestorationEmptySlices returns nil and nonnil empty
// slices from namespace restoration. It verifies that each later callback and
// compact delivery receives that same nil distinction.
func TestPassthroughBufferedToolRestorationEmptySlices(t *testing.T) {
	for _, asSSE := range []bool{false, true} {
		for _, replacement := range [][]byte{nil, {}} {
			t.Run(fmt.Sprintf("sse=%t/nil=%t", asSSE, replacement == nil), func(t *testing.T) {
				var calls []string
				options := passthroughTestOptions(&calls)
				options.RestoreNamespace = func([]byte) ([]byte, error) { return replacement, nil }
				options.NonStream.RestoreToolNames = func(payload []byte) []byte {
					require.Equal(t, replacement, payload)
					return payload
				}
				options.NonStream.RestoreOpenAITools = func(payload []byte) ([]byte, error) {
					require.Equal(t, replacement, payload)
					return payload, nil
				}
				options.NonStream.WriteCompactBridge = func(_ int, payload []byte) bool {
					require.Equal(t, replacement, payload)
					return true
				}
				result, sink, err := readPassthroughFixture(passthroughFixtureBody(passthroughToolFixture(), asSSE), asSSE, options, &calls)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Empty(t, result.ResponseID)
				require.Empty(t, sink.heads)
				require.Empty(t, sink.bodies)
			})
		}
	}
}

// TestPassthroughBufferedToolRestorationRequiredCallbacks omits each required
// restoration callback while leaving unused standard-reader callbacks nil.
// Both buffered entry paths require namespace, name, and client-tool callbacks.
func TestPassthroughBufferedToolRestorationRequiredCallbacks(t *testing.T) {
	for _, asSSE := range []bool{false, true} {
		for _, missing := range []string{"namespace", "names", "openai"} {
			t.Run(fmt.Sprintf("sse=%t/%s", asSSE, missing), func(t *testing.T) {
				var calls []string
				options := passthroughTestOptions(&calls)
				switch missing {
				case "namespace":
					options.RestoreNamespace = nil
				case "names":
					options.NonStream.RestoreToolNames = nil
				case "openai":
					options.NonStream.RestoreOpenAITools = nil
				}
				require.Panics(t, func() {
					_, _, _ = readPassthroughFixture(passthroughFixtureBody(passthroughToolFixture(), asSSE), asSSE, options, &calls)
				})
			})
		}
	}
}

// readPassthroughFixture invokes the nonstreaming reader with the supplied bytes.
// Its content type selects JSON or SSE conversion, and the returned sink exposes
// the synchronous delivery effects for assertions.
func readPassthroughFixture(body []byte, asSSE bool, options PassthroughOptions, calls *[]string) (*NonStreamingResult, *passthroughTestSink, error) {
	sink := &passthroughTestSink{calls: calls}
	contentType := "application/vnd.passthrough+json"
	if asSSE {
		contentType = "text/event-stream"
	}
	resp := &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     http.Header{"Content-Type": {contentType}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	options.NonStream.ReadBody = func(io.Reader) ([]byte, error) {
		*calls = append(*calls, "read")
		return body, nil
	}
	result, err := ReadPassthroughNonStreaming(context.Background(), resp, sink, options, "client-model", "upstream-model")
	return result, sink, err
}

// passthroughTestOptions records callbacks used by successful buffered response
// handling. Identity callbacks return their input slice, and tests replace them
// to observe byte ownership or inject restoration failures.
func passthroughTestOptions(calls *[]string) PassthroughOptions {
	return PassthroughOptions{
		StreamOptions: StreamOptions{
			RestoreNamespace: func(body []byte) ([]byte, error) {
				*calls = append(*calls, "namespace")
				return body, nil
			},
		},
		NonStream: NonStreamOptions{
			ObserveTier: func([]byte) { *calls = append(*calls, "tier") },
			ObserveSSE:  func(string) { *calls = append(*calls, "sse") },
			RestoreToolNames: func(body []byte) []byte {
				*calls = append(*calls, "names")
				return body
			},
			RestoreOpenAITools: func(body []byte) ([]byte, error) {
				*calls = append(*calls, "openai")
				return body, nil
			},
			CorrectToolCalls: func(body []byte) []byte {
				*calls = append(*calls, "correct")
				return body
			},
			SupplementCompaction: func(body []byte, _ string) []byte {
				*calls = append(*calls, "supplement")
				return body
			},
			WriteCompactBridge: func(int, []byte) bool {
				*calls = append(*calls, "bridge")
				return false
			},
		},
		Headers: func(output, _ http.Header) {
			*calls = append(*calls, "headers")
			output.Set("X-Passthrough-Test", "copied")
		},
		MissingUsage: func(_ *http.Response, _ *wire.ForwardUsage, eventType string, _ bool) {
			*calls = append(*calls, "missing:"+eventType)
		},
	}
}

// passthroughToolFixture supplies raw tool, compaction, usage, and opaque JSON fields.
func passthroughToolFixture() []byte {
	return []byte(`{ "id":"resp_upstream", "model":"upstream-model", ` +
		`"output":[{"id":"fc_tool","type":"function_call","call_id":"call_tool","name":"flat_tool",` +
		`"arguments":"{\"text\":\"line\\n雪\"}"},{"type":"compaction","encrypted_content":"upstream_encrypted"}], ` +
		`"usage":{"input_tokens":7,"output_tokens":11}, ` +
		`"unrelated":{"large":9007199254740993,"decimal":1.234e+09,"text":"keep \u0061"} }`)
}

// passthroughFixtureBody frames final JSON when the test selects SSE conversion.
// JSON fixtures retain their supplied backing storage, while SSE framing creates
// a separate byte slice containing that same response object.
func passthroughFixtureBody(body []byte, asSSE bool) []byte {
	if asSSE {
		return []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + string(body) + "}\n\n")
	}
	return body
}

// Begin records emitted response metadata in the owning test's callback sequence.
func (s *passthroughTestSink) Begin(head upstream.OutputHead) error {
	*s.calls = append(*s.calls, "begin")
	s.heads = append(s.heads, head)
	return nil
}

// Emit records synchronous delivery and retains a copy of the payload.
// Assertions inspect that copy after the reader returns, when the borrowed
// source storage may already be reusable.
func (s *passthroughTestSink) Emit(event upstream.OutputEvent) error {
	*s.calls = append(*s.calls, "emit")
	// The reader lends payload storage for this synchronous call, so retain an
	// independent copy for assertions that run after response handling returns.
	s.bodies = append(s.bodies, bytes.Clone(event.Data))
	return nil
}

// TestReadPassthroughStreamingBareErrorClearedByCompleted streams semantic
// output, a standalone Codex error frame, and a valid response.completed
// terminal through ReadPassthroughStreaming. The completed terminal clears the
// withheld bare error without synthesizing response.failed or firing deferred
// terminal side effects.
func TestReadPassthroughStreamingBareErrorClearedByCompleted(t *testing.T) {
	sse := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		``,
		`event: error`,
		`data: {"type":"error","error":{"message":"transient warning"}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_recovered","model":"upstream-model","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":4,"output_tokens":2}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	var calls []string
	sink := &passthroughTestSink{calls: &calls}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}
	sideEffects := 0
	clearDisconnects := 0
	options := passthroughTestOptions(&calls)
	options.NativeOpenAI = true
	options.MaxLineSize = 1 << 20
	options.ClientOutputStarted = func(started bool) bool { return started }
	options.RestoreToolNames = func(payload []byte, _ string) []byte { return payload }
	options.OpenAIStreamEventIsTerminalWithType = wire.OpenAIStreamEventIsTerminalWithType
	options.TerminalSideEffects = func([]byte, string, http.Header, string) { sideEffects++ }
	options.ClearDisconnect = func() { clearDisconnects++ }
	options.BuildOpenAIResponseFailedSSE = func(_, _ string, _ []byte, msg string) string {
		t.Fatalf("unexpected synthetic response.failed: %s", msg)
		return ""
	}

	result, err := ReadPassthroughStreaming(
		context.Background(),
		resp,
		upstream.NewOutputContext(sink),
		options,
		time.Now(),
		"client-model",
		"upstream-model",
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_recovered", result.ResponseID)
	require.Equal(t, &wire.ForwardUsage{InputTokens: 4, OutputTokens: 2}, result.Usage)
	require.Zero(t, sideEffects)
	require.Equal(t, 1, clearDisconnects)
	var emitted strings.Builder
	for _, chunk := range sink.bodies {
		_, _ = emitted.Write(chunk)
	}
	require.Contains(t, emitted.String(), `"type":"response.completed"`)
	require.Contains(t, emitted.String(), `"model":"client-model"`)
	require.NotContains(t, emitted.String(), `"transient warning"`)
	require.NotContains(t, emitted.String(), `response.failed`)
}
