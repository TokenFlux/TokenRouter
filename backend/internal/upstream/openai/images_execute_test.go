package openai

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
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// imagesContractSink captures synchronous image output and progressive-delivery
// callback observations.
type imagesContractSink struct {
	chunks  []string            // captured image response fragments
	flushed int                 // observed image flush count
	head    upstream.OutputHead // captured image response head
	// onPartial runs once after a nonterminal semantic event; Emit clears it
	// immediately afterward so later progress cannot repeat the fixture
	// notification.
	onPartial func()
	// failAfterChunks rejects later nonempty writes after this many accepted
	// fragments. Zero keeps delivery enabled for ordinary successful fixtures.
	failAfterChunks int
}

// imagesCloseRecorder counts closure of the acquired image response body.
type imagesCloseRecorder struct {
	io.ReadCloser               // wrapped acquired image response
	closed        *atomic.Int32 // acquired response close counter
	// reads optionally counts body reads so protocol-rejection fixtures can
	// prove the executor closed an incompatible response without consuming it.
	reads *atomic.Int32
}

// Read records optional fixture read observations before delegating body input.
// A nil counter disables recording; the wrapped reader supplies all bytes and
// errors without changing response-body ownership.
func (b *imagesCloseRecorder) Read(data []byte) (int, error) {
	if b.reads != nil {
		b.reads.Add(1)
	}
	return b.ReadCloser.Read(data)
}

// TestImagesExecuteSelectsStreamFormat checks streaming media types for API-key
// and OAuth targets. Valid SSE uses its event reader; other types require a
// complete Images JSON document and reject an SSE body presented as JSON.
func TestImagesExecuteSelectsStreamFormat(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		for _, contentType := range []string{"text/event-stream; charset=utf-8", "Text/Event-Stream", "application/json", "", `application/json; note="text/event-stream"`, "text/event-stream; broken"} {
			t.Run(fmt.Sprintf("oauth=%t/type=%s", oauth, contentType), func(t *testing.T) {
				var closed, reads atomic.Int32
				body := "data: {\"type\":\"image_edit.completed\",\"b64_json\":\"image\"}\n\n"
				if oauth {
					body = "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"result\":\"image\"}]}}\n\n"
				}
				response := &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {contentType}},
					Body:       &imagesCloseRecorder{ReadCloser: io.NopCloser(strings.NewReader(body)), closed: &closed, reads: &reads},
				}
				request := httptest.NewRequest(http.MethodPost, "https://example.invalid/images/edits", strings.NewReader("{}"))
				var presented *OpenAIImagesUpstreamError
				observedStatus := 0
				options := imagesTestResponseOptions()
				options.WriteHTTPError = func(err *OpenAIImagesUpstreamError) bool {
					presented = err
					return true
				}
				options.ObserveError = func(status int, _, _ string) { observedStatus = status }
				target := &ImagesTarget{
					OAuth: oauth, Model: "gpt-image-2", StreamPrefix: "image_edit",
					Request: request, Options: options,
					Do: func(*http.Request) (*http.Response, error) { return response, nil },
				}
				result, err := (ImagesExecutor{}).Execute(t.Context(), upstream.AttemptInput{Protocol: protocol.ProtocolImagesEdits, Stream: true, Target: target}, &imagesContractSink{})
				require.EqualValues(t, 1, closed.Load())
				if contentType == "text/event-stream; charset=utf-8" || contentType == "Text/Event-Stream" {
					require.NoError(t, err)
					require.Nil(t, presented)
					require.Positive(t, reads.Load())
					require.Equal(t, 1, result.ObservedImages)
					return
				}
				require.ErrorAs(t, err, &presented)
				require.Equal(t, "invalid_upstream_response", presented.Code)
				require.Equal(t, http.StatusBadGateway, presented.StatusCode)
				require.Equal(t, http.StatusOK, observedStatus)
				require.Positive(t, reads.Load())
				require.Zero(t, result.ObservedImages)
				require.False(t, result.HasUsage)
			})
		}
	}
}

// TestImagesExecuteGIFSizeMetadata presents buffered generation and edit results
// for API keys and OAuth across JSON, SSE, and URL delivery. Both response size
// levels and billing dimensions must match the actual GIF while preserving
// original bytes, file counts, and frame billing units.
func TestImagesExecuteGIFSizeMetadata(t *testing.T) {
	encoded := "R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAkQBACwAAAAAAQABAAACAkQBADs="
	for _, transport := range []string{"apikey_json", "apikey_sse", "apikey_sse_url", "oauth_sse"} {
		for _, operation := range []protocol.ProtocolID{protocol.ProtocolImagesGenerations, protocol.ProtocolImagesEdits} {
			t.Run(fmt.Sprintf("%s/%s", transport, operation), func(t *testing.T) {
				body := `{"size":"3840x2160","output_format":"gif","data":[{"b64_json":"` + encoded + `"}]}`
				contentType := "application/json"
				if transport == "oauth_sse" {
					// OAuth carries the finished GIF through Responses lifecycle events even
					// when the Images client receives one buffered JSON document.
					body = `data: {"type":"response.completed","response":{"output":[{"type":"image_generation_call","id":"gif-output","result":"` + encoded + `","size":"3840x2160","output_format":"gif"}]}}` + "\n\n"
					contentType = "text/event-stream"
				} else if transport != "apikey_json" {
					kind := "image_generation.completed"
					if operation == protocol.ProtocolImagesEdits {
						kind = "image_edit.completed"
					}
					image := `"b64_json":"` + encoded + `"`
					if transport == "apikey_sse_url" {
						image = `"url":"https://example.invalid/result.gif"`
					}
					body = `data: {"type":"` + kind + `",` + image + `,"size":"3840x2160","output_format":"gif"}` + "\n\n"
					contentType = "text/event-stream"
				}
				options := imagesTestResponseOptions()
				options.ExpectedGIF = true
				downloads := 0
				options.FetchImage = func(url string) (string, error) {
					downloads++
					require.Equal(t, "https://example.invalid/result.gif", url)
					return encoded, nil
				}
				target := &ImagesTarget{
					OAuth: transport == "oauth_sse", Model: "gpt-image-2.6-flare", ResponseFormat: "b64_json", Options: options,
					Request: httptest.NewRequest(http.MethodPost, "https://example.invalid/images", nil),
					Do: func(*http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
					},
					ResponseError: func(_ *http.Response, _ int, err error) error { return err },
				}
				sink := &imagesContractSink{}
				result, err := (ImagesExecutor{}).Execute(t.Context(), upstream.AttemptInput{Protocol: operation, Target: target}, sink)
				require.NoError(t, err)
				require.Equal(t, 1, result.ObservedImages)
				require.Equal(t, 2, result.ImageFrameCount)
				require.Equal(t, []string{"1x1"}, result.ImageOutputSizes)
				delivered := strings.Join(sink.chunks, "")
				require.Equal(t, "1x1", gjson.Get(delivered, "size").String())
				require.Equal(t, "1x1", gjson.Get(delivered, "data.0.size").String())
				require.Equal(t, encoded, gjson.Get(delivered, "data.0.b64_json").String())
				if transport == "apikey_sse_url" {
					require.Equal(t, 1, downloads)
				} else {
					require.Zero(t, downloads)
				}
			})
		}
	}
}

// TestImagesExecuteMixedGIFSizeMetadata preserves static size evidence while
// correcting an animation's reported canvas. Reading the normalized response for
// billing must retain both outputs rather than lose the static entry's size.
func TestImagesExecuteMixedGIFSizeMetadata(t *testing.T) {
	encoded := "R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAkQBACwAAAAAAQABAAACAkQBADs="
	body := `data: {"type":"image_generation.completed","id":"static","b64_json":"c3RhdGlj","output_format":"png","size":"2048x1536"}` + "\n\n" +
		`data: {"type":"image_generation.completed","id":"gif","b64_json":"` + encoded + `","output_format":"gif","size":"3840x2160"}` + "\n\n"
	options := imagesTestResponseOptions()
	options.ExpectedImages = 2
	target := &ImagesTarget{
		Options: options,
		Request: httptest.NewRequest(http.MethodPost, "https://example.invalid/images", nil),
		Do: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		},
	}
	sink := &imagesContractSink{}
	result, err := (ImagesExecutor{}).Execute(t.Context(), upstream.AttemptInput{Protocol: protocol.ProtocolImagesGenerations, Target: target}, sink)
	require.NoError(t, err)
	require.Equal(t, 2, result.ObservedImages)
	require.Equal(t, 3, result.ImageFrameCount)
	require.Equal(t, []string{"2048x1536", "1x1"}, result.ImageOutputSizes)
	delivered := strings.Join(sink.chunks, "")
	require.False(t, gjson.Get(delivered, "size").Exists())
	require.Equal(t, "2048x1536", gjson.Get(delivered, "data.0.size").String())
	require.Equal(t, "1x1", gjson.Get(delivered, "data.1.size").String())
}

// Begin captures the synchronous image response head for assertions.
func (s *imagesContractSink) Begin(head upstream.OutputHead) error { s.head = head; return nil }

// Emit records nonempty chunks and flushes, then runs onPartial once for a
// nonterminal semantic event. The callback is cleared after invocation so later
// progressive events cannot close its observation channel twice.
func (s *imagesContractSink) Emit(event upstream.OutputEvent) error {
	if len(event.Data) > 0 {
		if s.failAfterChunks > 0 && len(s.chunks) >= s.failAfterChunks {
			return errors.New("fixture client disconnected")
		}
		s.chunks = append(s.chunks, string(event.Data))
	}
	if event.Flush {
		s.flushed++
	}
	if event.Semantic && !event.Terminal && s.onPartial != nil {
		s.onPartial()
		s.onPartial = nil
	}
	return nil
}

// TestImagesExecuteJSONStreamAccounting converts buffered image responses for
// both credentials and operations. Actual image count and total usage survive
// client write failure, while successful delivery includes total usage once.
func TestImagesExecuteJSONStreamAccounting(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		for _, operation := range []protocol.ProtocolID{protocol.ProtocolImagesGenerations, protocol.ProtocolImagesEdits} {
			for _, disconnect := range []bool{false, true} {
				t.Run(fmt.Sprintf("oauth_%t/%s/disconnect_%t", oauth, operation, disconnect), func(t *testing.T) {
					body := `{"created":1710000000,"size":"1024x1024","background":"transparent","usage":{"input_tokens":12,"output_tokens":21,"output_tokens_details":{"image_tokens":9},"input_tokens_details":{"image_tokens":2}},"data":[{"b64_json":"first","size":"512x512","revised_prompt":"revised"},{"url":"https://example.invalid/second.png"}]}`
					var closed atomic.Int32
					active := false
					stops := 0
					options := imagesTestResponseOptions()
					options.FetchImage = func(rawURL string) (string, error) {
						require.Equal(t, "https://example.invalid/second.png", rawURL)
						return "downloaded-image", nil
					}
					options.StartKeepalive = func() func() {
						active = true
						return func() {
							if active {
								stops++
								active = false
							}
						}
					}
					options.ReadBody = func(reader io.Reader) ([]byte, error) {
						require.True(t, active, "JSON reads retain the waiting heartbeat")
						return io.ReadAll(reader)
					}
					copyHeaders := options.ResponseHeaders
					options.ResponseHeaders = func(dst, src http.Header) {
						require.False(t, active, "response construction takes over after heartbeat stop")
						copyHeaders(dst, src)
					}
					calls := 0
					target := &ImagesTarget{
						OAuth:   oauth,
						Model:   "gpt-image-2",
						Options: options,
						Request: httptest.NewRequest(http.MethodPost, "https://example.invalid/images", nil),
						Do: func(*http.Request) (*http.Response, error) {
							calls++
							require.True(t, active)
							return &http.Response{
								StatusCode: http.StatusOK,
								Header:     http.Header{"Content-Type": {"application/json; charset=utf-8"}, "Content-Length": {"123"}, "X-Request-Id": {"json-request"}},
								Body:       &imagesCloseRecorder{ReadCloser: io.NopCloser(strings.NewReader(body)), closed: &closed},
							}, nil
						},
					}
					sink := &imagesContractSink{}
					if disconnect {
						sink.failAfterChunks = 2
					}
					result, err := (ImagesExecutor{}).Execute(t.Context(), upstream.AttemptInput{Protocol: operation, Stream: true, Target: target}, sink)
					require.NoError(t, err)
					require.Equal(t, 1, calls)
					require.Equal(t, 1, stops)
					require.EqualValues(t, 1, closed.Load())
					require.Equal(t, 2, result.ObservedImages)
					require.Equal(t, []string{"512x512", "1024x1024"}, result.ImageOutputSizes)
					require.Equal(t, 12, result.Usage.InputTokens)
					require.Equal(t, 21, result.Usage.OutputTokens)
					require.Equal(t, 9, result.Usage.ImageOutputTokens)
					require.Equal(t, 2, result.ImageInputTokens)
					require.True(t, result.HasUsage)
					require.Equal(t, disconnect, result.ClientDisconnect)
					require.Equal(t, "text/event-stream", sink.head.Header.Get("Content-Type"))
					require.Empty(t, sink.head.Header.Get("Content-Length"))
					if disconnect {
						return
					}
					eventName := "image_generation.completed"
					if operation == protocol.ProtocolImagesEdits {
						eventName = "image_edit.completed"
					}
					stream := strings.Join(sink.chunks, "")
					require.Equal(t, 2, strings.Count(stream, "event: "+eventName+"\n"))
					require.Equal(t, 1, strings.Count(stream, `"usage":`))
					require.Contains(t, stream, `"url":"https://example.invalid/second.png"`)
					if !oauth {
						require.Contains(t, stream, `"b64_json":"downloaded-image"`)
					}
					require.Contains(t, stream, `"revised_prompt":"revised"`)
					require.Contains(t, stream, `"created_at":1710000000`)
					require.Contains(t, stream, "data: [DONE]\n\n")
				})
			}
		}
	}
}

// TestImagesExecuteJSONStreamWithoutUsage observes one JSON image carrying both
// supported content fields. Streaming converts it once; nonstream delivery
// retains JSON, and neither path invents token usage or a second image.
func TestImagesExecuteJSONStreamWithoutUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			body := `{"data":[{"b64_json":"first","url":"https://example.invalid/first.png"}]}`
			starts := 0
			options := imagesTestResponseOptions()
			options.StartKeepalive = func() func() {
				starts++
				return func() {}
			}
			target := &ImagesTarget{
				Options: options,
				Request: httptest.NewRequest(http.MethodPost, "https://example.invalid/images", nil),
				Do: func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				},
			}
			sink := &imagesContractSink{}
			result, err := (ImagesExecutor{}).Execute(t.Context(), upstream.AttemptInput{Protocol: protocol.ProtocolImagesGenerations, Stream: stream, Target: target}, sink)
			require.NoError(t, err)
			require.Equal(t, 1, result.ObservedImages)
			require.False(t, result.HasUsage)
			require.Zero(t, result.Usage.InputTokens)
			require.Zero(t, result.Usage.OutputTokens)
			output := strings.Join(sink.chunks, "")
			if stream {
				require.Equal(t, 1, starts)
				require.Equal(t, 1, strings.Count(output, "event: image_generation.completed\n"))
				require.NotContains(t, output, `"usage":`)
			} else {
				require.Zero(t, starts)
				require.JSONEq(t, body, output)
				require.Equal(t, "application/json", sink.head.Header.Get("Content-Type"))
			}
		})
	}
}

// TestImagesExecuteJSONStreamReadCancellation preserves cancellation and timeout
// causes returned by a buffered read. Diagnostics retain the canceled flag,
// and no image completion or protocol-error presentation follows that stop.
func TestImagesExecuteJSONStreamReadCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			readErr := fmt.Errorf("fixture JSON read stopped: %w", cause)
			var closed atomic.Int32
			presented := false
			options := imagesTestResponseOptions()
			options.WriteHTTPError = func(*OpenAIImagesUpstreamError) bool {
				presented = true
				return true
			}
			target := &ImagesTarget{
				Options: options,
				Request: httptest.NewRequest(http.MethodPost, "https://example.invalid/images", nil),
				Do: func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       &imagesCloseRecorder{ReadCloser: io.NopCloser(iotest.ErrReader(readErr)), closed: &closed},
					}, nil
				},
			}
			sink := &imagesContractSink{}
			result, err := (ImagesExecutor{}).Execute(t.Context(), upstream.AttemptInput{Protocol: protocol.ProtocolImagesEdits, Stream: true, Target: target}, sink)
			require.ErrorIs(t, err, cause)
			require.True(t, result.Cancelled)
			require.False(t, presented)
			require.Zero(t, result.ObservedImages)
			require.False(t, result.HasUsage)
			require.Empty(t, sink.chunks)
			require.EqualValues(t, 1, closed.Load())
		})
	}
}

// TestImagesExecuteJSONStreamFailures rejects error and malformed JSON bodies
// before presenting a completion. No failed body retains image usage, and
// terminal presentation occurs once after waiting heartbeats have stopped.
func TestImagesExecuteJSONStreamFailures(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"invalid_request_error","code":"content_policy_violation","param":"prompt","message":"fixture policy"}}`,
		`{"error":{"type":"invalid_request_error","message":"fixture policy"},"data":[{"b64_json":"hidden"}]}`,
		`{"data":{"b64_json":"hidden"}}`,
		`{"data":[]}`, `{"data":[{"url":123}]}`, `{"data":[{"id":"metadata"}]}`,
		`{"data":[{"b64_json":"valid"},{}]}`, `{"data":[{"b64_json":"valid"}],"usage":[]}`, `{"data":[`,
		"read failure",
	} {
		t.Run(body, func(t *testing.T) {
			var closed atomic.Int32
			reader := io.Reader(strings.NewReader(body))
			if body == "read failure" {
				reader = iotest.ErrReader(errors.New("fixture JSON read failure"))
			}
			options := imagesTestResponseOptions()
			presentations := 0
			options.WriteHTTPError = func(err *OpenAIImagesUpstreamError) bool {
				presentations++
				if strings.Contains(body, "content_policy_violation") {
					require.Equal(t, "content_policy_violation", err.Code)
					require.Equal(t, "prompt", err.Param)
				}
				return true
			}
			target := &ImagesTarget{
				OAuth:   true,
				Options: options,
				Request: httptest.NewRequest(http.MethodPost, "https://example.invalid/images", nil),
				Do: func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: &imagesCloseRecorder{ReadCloser: io.NopCloser(reader), closed: &closed}}, nil
				},
			}
			sink := &imagesContractSink{}
			result, err := (ImagesExecutor{}).Execute(t.Context(), upstream.AttemptInput{Protocol: protocol.ProtocolImagesEdits, Stream: true, Target: target}, sink)
			var imageErr *OpenAIImagesUpstreamError
			require.ErrorAs(t, err, &imageErr)
			require.Equal(t, 1, presentations)
			require.EqualValues(t, 1, closed.Load())
			require.Zero(t, result.ObservedImages)
			require.False(t, result.HasUsage)
			require.Empty(t, sink.chunks)
		})
	}
}

// Close increments the fixture close count before delegating response-body
// closure.
func (b *imagesCloseRecorder) Close() error { b.closed.Add(1); return b.ReadCloser.Close() }

// TestImagesExecuteTLSResponseOwnership serves API-key JSON/SSE, OAuth
// converted JSON, and HTTP failure fixtures over TLS. It checks request
// identity, body closure before activity release, model/usage results, and
// completed image output.
func TestImagesExecuteTLSResponseOwnership(t *testing.T) {
	for _, tc := range []struct {
		name string // image executor fixture name
		// oauth selects OAuth conversion, and stream selects progressive output.
		// The two flags combine with the fixture Content-Type to exercise the
		// executor selection rules for this prepared request.
		oauth, stream bool
		status        int    // upstream fixture HTTP status
		body          string // upstream fixture response body
	}{
		{name: "apikey_json", status: 200, body: `{"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":7,"output_tokens":3}}`},
		{
			name:   "oauth_json",
			oauth:  true,
			status: 200,
			body:   "data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000000,\"output\":[{\"type\":\"image_generation_call\",\"result\":\"aW1hZ2U=\"}]}}\n\n",
		},
		{
			name:   "apikey_sse",
			stream: true,
			status: 200,
			body:   "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"aW1hZ2U=\",\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}\n\n",
		},
		{name: "http_error", status: 429, body: `{"error":{"message":"limited"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests, closed, released atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				require.Equal(t, "Bearer fixture", r.Header.Get("Authorization"))
				require.Equal(t, "/images/generations", r.URL.Path)
				if tc.stream || tc.oauth {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				w.Header().Set("x-request-id", "img-request")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/images/generations", strings.NewReader(`{"model":"gpt-image-2"}`))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer fixture")
			options := imagesTestResponseOptions()
			marker := errors.New("original HTTP policy")
			target := &ImagesTarget{
				ProviderID:     9,
				OAuth:          tc.oauth,
				Model:          "gpt-image-2",
				ResponseFormat: "b64_json",
				StreamPrefix:   "image_generation",
				Request:        request,
				Options:        options,

				Enter: func() (func(), error) {
					return func() { require.EqualValues(t, 1, closed.Load()); released.Add(1) }, nil
				},

				Do: func(r *http.Request) (*http.Response, error) {
					resp, err := server.Client().Do(r)
					if err == nil {
						resp.Body = &imagesCloseRecorder{ReadCloser: resp.Body, closed: &closed}
					}
					return resp, err
				},

				TransportError: func(err error) error { return err },

				ReadErrorBody: func(resp *http.Response) []byte {
					body, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					return body
				},

				RedactErrorBody: func(body []byte) []byte { return body },

				HTTPError: func(resp *http.Response, body []byte) error {
					require.Equal(t, tc.body, string(body))
					require.EqualValues(t, 1, closed.Load())
					return marker
				},

				ResponseError: func(_ *http.Response, _ int, err error) error { return err },
			}
			sink := &imagesContractSink{}
			result, err := (ImagesExecutor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolImagesGenerations, ResponseModel: "client-image", Stream: tc.stream, Target: target}, sink)
			require.EqualValues(t, 1, requests.Load())
			require.EqualValues(t, 1, closed.Load())
			require.EqualValues(t, 1, released.Load())
			if tc.status >= 400 {
				require.ErrorIs(t, err, marker)
				require.Empty(t, sink.chunks)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, result.ObservedImages)
			require.Equal(t, "img-request", result.RequestID)
			require.Equal(t, "client-image", result.Model)
			require.True(t, result.HTTPCommitted)
			require.True(t, result.Served)
			require.Contains(t, strings.Join(sink.chunks, ""), "aW1hZ2U=")
			if !tc.oauth {
				require.Equal(t, 7, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
			}
		})
	}
}

// TestImagesExecuteOAuthProgressBeforeFailure waits for progressive image
// delivery before sending a failed terminal event. It checks served and
// committed observations survive failure while completed-image count remains
// zero and the partial event was flushed.
func TestImagesExecuteOAuthProgressBeforeFailure(t *testing.T) {
	delivered := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"cGFydGlhbA==\"}\n\n")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("本地 TLS 响应不支持 Flush")
			return
		}
		flusher.Flush()
		// Wait for the synchronous sink callback before emitting failure, proving
		// partial image output reached the client path while no terminal image existed yet.
		select {
		case <-delivered:
		case <-time.After(3 * time.Second):
			t.Error("图片未渐进交付")
			return
		}
		_, _ = io.WriteString(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"failed\",\"code\":\"server_error\"},\"usage\":{\"input_tokens\":9}}}\n\n")
	}))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	options := imagesTestResponseOptions()
	sink := &imagesContractSink{onPartial: func() { close(delivered) }}
	options.AdjustedWrittenSize = func() int { return len(sink.chunks) }
	target := &ImagesTarget{
		OAuth:          true,
		Request:        request,
		Model:          "gpt-image-2",
		ResponseFormat: "b64_json",
		StreamPrefix:   "image_generation",
		Options:        options,
		Do:             server.Client().Do,
		TransportError: func(err error) error { return err },
		ResponseError:  func(_ *http.Response, _ int, err error) error { return err },
	}
	result, err := (ImagesExecutor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolImagesGenerations, ResponseModel: "gpt-image-2", Stream: true, Target: target}, sink)
	require.Error(t, err)
	require.Equal(t, 0, result.ObservedImages)
	require.True(t, result.Served)
	require.True(t, result.HTTPCommitted)
	require.Contains(t, strings.Join(sink.chunks, ""), ".partial_image")
	require.Contains(t, strings.Join(sink.chunks, ""), "event: error\n")
	require.Greater(t, sink.flushed, 0)
}

// imagesTestResponseOptions supplies image reader callbacks with inactive
// interval and heartbeat timers. It uses ordinary read errors, no backfill
// rewrite, simple header copying, and test-local output sizing to exercise
// executor branches.
func imagesTestResponseOptions() ImageResponseOptions {
	return ImageResponseOptions{
		ReadBody:          io.ReadAll,
		ClassifyReadError: func(err error) error { return err },

		ResponseHeaders: func(dst, src http.Header) {
			for key, values := range src {
				if key != "Content-Type" {
					dst[key] = append([]string(nil), values...)
				}
			}
		},

		ObserveError:   func(int, string, string) {},
		WriteHTTPError: func(*OpenAIImagesUpstreamError) bool { return true },
		EmptyOutput:    func([]byte) error { return errors.New("empty image") },
		Summary:        SummarizeOpenAIImagesNoOutputBody,

		AdjustedWrittenSize: func() int { return -1 },

		StreamInterval:    func() time.Duration { return 0 },
		KeepaliveInterval: func() time.Duration { return 0 },
		ReadLimit:         func() int64 { return 1 << 20 },
		Backfill:          func(body []byte) []byte { return body },
		Logf:              func(string, ...any) {},
	}
}

// TestImagesExecuteDeliveryFailureKeepsObservations rejects an image URL download
// after upstream generation completed. Both client representations retain one
// observed image and usage while a single exchange returns a terminal error.
func TestImagesExecuteDeliveryFailureKeepsObservations(t *testing.T) {
	for _, stream := range []bool{false, true} {
		options := imagesTestResponseOptions()
		options.FetchImage = func(string) (string, error) { return "", errors.New("download unavailable") }
		body := `{"data":[{"url":"https://image.invalid/result"}],"usage":{"output_tokens":9}}`
		mediaType := "application/json"
		if !stream {
			body = "data: {\"type\":\"image_generation.completed\",\"url\":\"https://image.invalid/result\",\"usage\":{\"output_tokens\":9}}\n\n"
			mediaType = "text/event-stream"
		}
		calls := 0
		target := &ImagesTarget{Model: "gpt-image-2", Options: options, Request: httptest.NewRequest(http.MethodPost, "https://example.invalid/images", nil), Do: func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {mediaType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		}}
		sink := &imagesContractSink{}
		result, err := (ImagesExecutor{}).Execute(t.Context(), upstream.AttemptInput{Protocol: protocol.ProtocolImagesGenerations, Stream: stream, Target: target}, sink)
		require.Error(t, err)
		require.Equal(t, 1, calls)
		require.Equal(t, 1, result.ObservedImages)
		require.Equal(t, 9, result.Usage.OutputTokens)
		require.Empty(t, sink.chunks)
	}
}
