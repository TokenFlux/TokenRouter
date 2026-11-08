package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// Buffered Responses restore client tool identities after observing upstream
// usage, and extracted SSE applies its correction before that restoration.
// These fixtures record callback and output effects so both representations
// exercise the same success, failure, and byte-sharing expectations.

// nonStreamingTestSink records synchronous output after buffered response handling.
type nonStreamingTestSink struct {
	// calls joins sink effects with callback effects during one synchronous read.
	// The owning test inspects the sequence after the reader returns.
	calls *[]string
	heads []upstream.OutputHead // received response metadata
	// bodies owns copies of emitted payloads so assertions can inspect them after
	// Emit returns and the reader's borrowed buffers are available for reuse.
	bodies [][]byte
}

// TestNonStreamingGIFActualFrames receives completed GIFs beyond request bounds
// through JSON and buffered SSE. Both paths must deliver original bytes and
// preserve actual frame counts independently from upstream token usage.
func TestNonStreamingGIFActualFrames(t *testing.T) {
	for _, count := range []int{1, 17} {
		animation := &gif.GIF{}
		for range count {
			animation.Image = append(animation.Image, image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black, color.White}))
			// GIF stores delays in centiseconds, making twenty equal 200 milliseconds.
			animation.Delay = append(animation.Delay, 20)
		}
		var buffer bytes.Buffer
		require.NoError(t, gif.EncodeAll(&buffer, animation))
		encoded := base64.StdEncoding.EncodeToString(buffer.Bytes())
		body := []byte(`{"id":"gif-response","model":"upstream-model","output":[{"id":"gif","type":"image_generation_call","output_format":"gif","result":"` + encoded + `"}],"usage":{"input_tokens":7,"output_tokens":11}}`)
		for _, asSSE := range []bool{false, true} {
			t.Run(fmt.Sprintf("frames=%d/sse=%t", count, asSSE), func(t *testing.T) {
				var calls []string
				result, _, sink, err := readNonStreamingFixture(nonStreamingFixtureBody(body, asSSE), asSSE, nonStreamingTestOptions(&calls), &calls, "client-model", "upstream-model")
				require.NoError(t, err)
				require.Equal(t, 1, result.ImageCount)
				require.Equal(t, count, result.ImageFrameCount)
				require.Equal(t, 7, result.Usage.InputTokens)
				require.Equal(t, 11, result.Usage.OutputTokens)
				require.Len(t, sink.bodies, 1)
				require.Equal(t, encoded, gjson.GetBytes(sink.bodies[0], "output.0.result").String())
			})
		}
	}
}

// TestNonStreamingToolRestorationOrder combines model remapping with each tool
// restoration and both output destinations. It checks upstream usage timing,
// SSE correction order, and exact outgoing bytes including unrelated JSON fields.
func TestNonStreamingToolRestorationOrder(t *testing.T) {
	for _, asSSE := range []bool{false, true} {
		for _, compactBridge := range []bool{false, true} {
			t.Run(fmt.Sprintf("sse=%t/bridge=%t", asSSE, compactBridge), func(t *testing.T) {
				body := nonStreamingToolFixture()
				input := nonStreamingFixtureBody(body, asSSE)
				var calls []string
				options := nonStreamingTestOptions(&calls)
				options.SupplementCompaction = func(payload []byte, stream string) []byte {
					calls = append(calls, "supplement")
					require.Equal(t, string(input), stream)
					require.Equal(t, "upstream-model", gjson.GetBytes(payload, "model").String())
					return payload
				}
				options.CorrectToolCalls = func(payload []byte) []byte {
					calls = append(calls, "correct")
					require.Equal(t, "client-model", gjson.GetBytes(payload, "model").String())
					return bytes.ReplaceAll(payload, []byte("upstream_tool"), []byte("corrected_tool"))
				}
				options.RestoreClientTools = func(payload []byte) ([]byte, error) {
					calls = append(calls, "client")
					require.Equal(t, "client-model", gjson.GetBytes(payload, "model").String())
					name := "upstream_tool"
					if asSSE {
						name = "corrected_tool"
					}
					require.Equal(t, name, gjson.GetBytes(payload, "output.0.name").String())
					payload = bytes.ReplaceAll(payload, []byte(name), []byte("grok_tool"))
					// Change visible usage during restoration so returned accounting can
					// prove it was captured from the upstream object before callbacks ran.
					return bytes.ReplaceAll(payload, []byte(`"input_tokens":7`), []byte(`"input_tokens":999`)), nil
				}
				options.RestoreOpenAITools = func(payload []byte) ([]byte, error) {
					calls = append(calls, "openai")
					require.Equal(t, "grok_tool", gjson.GetBytes(payload, "output.0.name").String())
					return bytes.ReplaceAll(payload, []byte("grok_tool"), []byte("openai_tool")), nil
				}
				options.RestoreNamespace = func(payload []byte) ([]byte, error) {
					calls = append(calls, "namespace")
					require.Equal(t, "openai_tool", gjson.GetBytes(payload, "output.0.name").String())
					return bytes.ReplaceAll(payload, []byte("openai_tool"), []byte("namespace_tool")), nil
				}
				options.RestoreToolNames = func(payload []byte) []byte {
					calls = append(calls, "names")
					require.Equal(t, "namespace_tool", gjson.GetBytes(payload, "output.0.name").String())
					return bytes.ReplaceAll(payload, []byte("namespace_tool"), []byte("client_tool"))
				}
				expected := bytes.ReplaceAll(body, []byte("upstream-model"), []byte("client-model"))
				expected = bytes.ReplaceAll(expected, []byte("upstream_tool"), []byte("client_tool"))
				expected = bytes.ReplaceAll(expected, []byte(`"input_tokens":7`), []byte(`"input_tokens":999`))
				options.PreserveContentType = true
				options.WriteCompactBridge = func(status int, payload []byte) bool {
					calls = append(calls, "bridge")
					require.Equal(t, http.StatusAccepted, status)
					require.Equal(t, expected, payload)
					return compactBridge
				}
				options.CountJSONSearch = func(payload []byte) int {
					calls = append(calls, "search-json")
					require.Equal(t, expected, payload)
					return 9
				}
				options.CountSSESearch = func(stream string) int {
					calls = append(calls, "search-sse")
					require.Equal(t, string(input), stream)
					return 9
				}

				result, output, sink, err := readNonStreamingFixture(input, asSSE, options, &calls, "client-model", "upstream-model")
				require.NoError(t, err)
				require.NoError(t, output.Err())
				require.NotNil(t, result)
				require.Equal(t, &wire.ForwardUsage{InputTokens: 7, OutputTokens: 11, CacheReadInputTokens: 2}, result.Usage)
				require.True(t, result.Served)
				require.Equal(t, "resp_tool", result.ResponseID)
				require.Equal(t, 9, result.SearchCount)
				require.Zero(t, result.ImageCount)
				require.Empty(t, result.ImageOutputSizes)
				wantCalls := []string{"read", "tier"}
				searchCall := "search-json"
				contentType := "application/vnd.buffered-response+json"
				if asSSE {
					wantCalls = []string{"sse", "supplement", "correct"}
					searchCall = "search-sse"
					contentType = "application/json; charset=utf-8"
				}
				wantCalls = append(wantCalls, "client", "openai", "namespace", "names", "headers", "bridge")
				if compactBridge {
					require.Empty(t, sink.heads)
					require.Empty(t, sink.bodies)
					require.False(t, output.Writer.Written())
				} else {
					wantCalls = append(wantCalls, "begin", "emit")
					require.Len(t, sink.heads, 1)
					require.Equal(t, http.StatusAccepted, sink.heads[0].Status)
					require.Equal(t, contentType, sink.heads[0].Header.Get("Content-Type"))
					require.Equal(t, "copied", sink.heads[0].Header.Get("X-Buffered-Test"))
					require.Equal(t, [][]byte{expected}, sink.bodies)
				}
				require.Equal(t, append(wantCalls, searchCall), calls)
				require.Equal(t, nonStreamingFixtureBody(body, asSSE), input)
			})
		}
	}
}

// TestNonStreamingToolRestorationFailure injects an error at each fallible
// restoration step for JSON and extracted SSE. It checks the first error's
// message and chain, and observes that later callbacks and output never begin.
func TestNonStreamingToolRestorationFailure(t *testing.T) {
	stages := []struct {
		name    string // failing callback name
		message string // required error context
	}{
		{name: "client", message: "restore Grok Responses client tool response"},
		{name: "openai", message: "restore OpenAI Responses client tool response"},
		{name: "namespace", message: "restore OpenAI namespace response"},
	}
	for _, asSSE := range []bool{false, true} {
		for stageIndex, stage := range stages {
			t.Run(fmt.Sprintf("sse=%t/%s", asSSE, stage.name), func(t *testing.T) {
				var calls []string
				options := nonStreamingTestOptions(&calls)
				cause := errors.New("tool failure")
				callbackErr := fmt.Errorf("callback: %w", cause)
				for _, callback := range []struct {
					name    string                        // ordered callback name
					restore *func([]byte) ([]byte, error) // callback selected for failure
				}{
					{name: "client", restore: &options.RestoreClientTools},
					{name: "openai", restore: &options.RestoreOpenAITools},
					{name: "namespace", restore: &options.RestoreNamespace},
				} {
					*callback.restore = func(payload []byte) ([]byte, error) {
						calls = append(calls, callback.name)
						if callback.name == stage.name {
							return []byte("discard failed callback bytes"), callbackErr
						}
						return payload, nil
					}
				}
				input := nonStreamingFixtureBody(nonStreamingToolFixture(), asSSE)
				result, output, sink, err := readNonStreamingFixture(input, asSSE, options, &calls, "client-model", "upstream-model")
				require.EqualError(t, err, stage.message+": callback: tool failure")
				require.ErrorIs(t, err, cause)
				require.Same(t, callbackErr, errors.Unwrap(err))
				require.Nil(t, result)
				require.Empty(t, output.Writer.Header())
				require.False(t, output.Writer.Written())
				require.Empty(t, sink.heads)
				require.Empty(t, sink.bodies)
				wantCalls := []string{"read", "tier"}
				if asSSE {
					wantCalls = []string{"sse", "supplement", "correct"}
				}
				for _, preceding := range stages[:stageIndex+1] {
					wantCalls = append(wantCalls, preceding.name)
				}
				require.Equal(t, wantCalls, calls)
			})
		}
	}
}

// TestNonStreamingToolRestorationByteOwnership returns a replacement buffer
// from the first restoration and mutates it in a later callback. It checks
// that subsequent callbacks and compact output receive that exact storage.
func TestNonStreamingToolRestorationByteOwnership(t *testing.T) {
	for _, asSSE := range []bool{false, true} {
		t.Run(fmt.Sprintf("sse=%t", asSSE), func(t *testing.T) {
			body := nonStreamingToolFixture()
			input := nonStreamingFixtureBody(body, asSSE)
			var calls []string
			var replacement []byte
			options := nonStreamingTestOptions(&calls)
			options.RestoreClientTools = func(payload []byte) ([]byte, error) {
				if asSSE {
					require.NotSame(t, &input[0], &payload[0])
				} else {
					require.Same(t, &input[0], &payload[0])
				}
				replacement = bytes.Clone(payload)
				return replacement, nil
			}
			options.RestoreOpenAITools = func(payload []byte) ([]byte, error) {
				require.Same(t, &replacement[0], &payload[0])
				// Equal-length tool names allow an in-place edit that exposes extra
				// copying between callbacks while keeping the outgoing response valid.
				copy(payload[bytes.Index(payload, []byte("upstream_tool")):], "restored_tool")
				return payload, nil
			}
			options.RestoreNamespace = func(payload []byte) ([]byte, error) {
				require.Same(t, &replacement[0], &payload[0])
				require.Equal(t, "restored_tool", gjson.GetBytes(payload, "output.0.name").String())
				return payload, nil
			}
			options.RestoreToolNames = func(payload []byte) []byte {
				require.Same(t, &replacement[0], &payload[0])
				return payload
			}
			options.WriteCompactBridge = func(_ int, payload []byte) bool {
				require.Same(t, &replacement[0], &payload[0])
				return false
			}
			result, _, sink, err := readNonStreamingFixture(input, asSSE, options, &calls, "upstream-model", "upstream-model")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, [][]byte{replacement}, sink.bodies)
			require.Equal(t, nonStreamingFixtureBody(body, asSSE), input)
		})
	}
}

// TestReadSSEAsJSONUnextractedStream supplies visible deltas with no final
// response object. It verifies that raw SSE output keeps its framing and
// bypasses the buffered JSON tool-restoration callbacks.
func TestReadSSEAsJSONUnextractedStream(t *testing.T) {
	input := []byte("event: response.output_text.delta\ndata: " +
		`{"type":"response.output_text.delta","delta":"hello","model":"upstream-model"}` + "\n\n")
	var calls []string
	options := nonStreamingTestOptions(&calls)
	result, _, sink, err := readNonStreamingFixture(input, true, options, &calls, "client-model", "upstream-model")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Served)
	require.Equal(t, []string{"sse", "headers", "bridge", "begin", "emit", "search-sse"}, calls)
	require.Len(t, sink.heads, 1)
	require.Equal(t, "text/event-stream", sink.heads[0].Header.Get("Content-Type"))
	require.Equal(t, [][]byte{bytes.ReplaceAll(input, []byte("upstream-model"), []byte("client-model"))}, sink.bodies)
}

// readNonStreamingFixture runs one reader against caller-supplied body storage.
// JSON reads borrow that storage directly; SSE reads receive its framed bytes,
// and the returned context and sink expose output commitment for assertions.
func readNonStreamingFixture(body []byte, asSSE bool, options NonStreamOptions, calls *[]string, originalModel, mappedModel string) (*NonStreamingResult, *upstream.OutputContext, *nonStreamingTestSink, error) {
	sink := &nonStreamingTestSink{calls: calls}
	output := upstream.NewOutputContext(sink)
	contentType := "application/vnd.buffered-response+json"
	if asSSE {
		contentType = "text/event-stream"
	}
	resp := &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     http.Header{"Content-Type": {contentType}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	options.ReadBody = func(io.Reader) ([]byte, error) {
		*calls = append(*calls, "read")
		return body, nil
	}
	var result *NonStreamingResult
	var err error
	if asSSE {
		result, err = ReadSSEAsJSON(context.Background(), resp, output, options, body, originalModel, mappedModel)
	} else {
		result, err = ReadNonStreamingResponse(context.Background(), resp, output, options, originalModel, mappedModel)
	}
	return result, output, sink, err
}

// nonStreamingTestOptions records ordinary buffered callbacks for each fixture.
// Identity callbacks pass their slices through, and tests replace individual
// callbacks to observe transformations, inject failures, or select compact output.
func nonStreamingTestOptions(calls *[]string) NonStreamOptions {
	return NonStreamOptions{
		ObserveTier: func([]byte) { *calls = append(*calls, "tier") },
		ObserveSSE:  func(string) { *calls = append(*calls, "sse") },
		RestoreClientTools: func(body []byte) ([]byte, error) {
			*calls = append(*calls, "client")
			return body, nil
		},
		RestoreOpenAITools: func(body []byte) ([]byte, error) {
			*calls = append(*calls, "openai")
			return body, nil
		},
		RestoreNamespace: func(body []byte) ([]byte, error) {
			*calls = append(*calls, "namespace")
			return body, nil
		},
		RestoreToolNames: func(body []byte) []byte {
			*calls = append(*calls, "names")
			return body
		},
		CorrectToolCalls: func(body []byte) []byte {
			*calls = append(*calls, "correct")
			return body
		},
		ResponseHeaders: func(output, _ http.Header) {
			*calls = append(*calls, "headers")
			output.Set("X-Buffered-Test", "copied")
		},
		WriteCompactBridge: func(int, []byte) bool {
			*calls = append(*calls, "bridge")
			return false
		},
		CountJSONSearch: func([]byte) int {
			*calls = append(*calls, "search-json")
			return 9
		},
		CountSSESearch: func(string) int {
			*calls = append(*calls, "search-sse")
			return 9
		},
		SupplementCompaction: func(body []byte, _ string) []byte {
			*calls = append(*calls, "supplement")
			return body
		},
	}
}

// nonStreamingToolFixture supplies raw JSON with tool output and upstream usage.
func nonStreamingToolFixture() []byte {
	return []byte(`{ "id":"resp_tool", "model":"upstream-model", ` +
		`"output":[{"id":"fc_tool","type":"function_call","call_id":"call_tool","name":"upstream_tool",` +
		`"arguments":"{\"text\":\"line\\n雪\"}"}], ` +
		`"usage":{"input_tokens":7,"output_tokens":11,"input_tokens_details":{"cached_tokens":2}}, ` +
		`"unrelated":{"decimal":1.234e+09,"text":"keep \u0061"} }`)
}

// nonStreamingFixtureBody wraps final JSON when the fixture uses buffered SSE.
// SSE framing allocates separate storage containing the supplied JSON bytes,
// while the JSON fixture returns its source slice for ownership assertions.
func nonStreamingFixtureBody(body []byte, asSSE bool) []byte {
	if asSSE {
		return []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + string(body) + "}\n\n")
	}
	return body
}

// Begin records response metadata alongside the owning test's callback sequence.
// Buffered readers invoke this after preparing headers, and the sink leaves
// that supplied metadata available for commitment assertions.
func (s *nonStreamingTestSink) Begin(head upstream.OutputHead) error {
	*s.calls = append(*s.calls, "begin")
	s.heads = append(s.heads, head)
	return nil
}

// Emit records each synchronous byte delivery in the callback sequence.
// Retained payloads use independent storage so later assertions do not depend
// on the lifetime of a reader's borrowed buffer.
func (s *nonStreamingTestSink) Emit(event upstream.OutputEvent) error {
	*s.calls = append(*s.calls, "emit")
	// The sink keeps emitted bytes for assertions after synchronous output
	// returns, so copy the borrowed payload before the reader can reuse it.
	s.bodies = append(s.bodies, bytes.Clone(event.Data))
	return nil
}
