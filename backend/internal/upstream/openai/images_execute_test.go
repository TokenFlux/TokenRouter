package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

// imagesContractSink 保留每次同步输出，允许在读到终态前验证渐进交付。
type imagesContractSink struct {
	chunks    []string
	flushed   int
	head      upstream.OutputHead
	onPartial func()
}

func (s *imagesContractSink) Begin(head upstream.OutputHead) error { s.head = head; return nil }
func (s *imagesContractSink) Emit(event upstream.OutputEvent) error {
	if len(event.Data) > 0 {
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

// imagesCloseRecorder 验证真实网络响应体只在本次执行拥有者处释放。
type imagesCloseRecorder struct {
	io.ReadCloser
	closed *atomic.Int32
}

func (b *imagesCloseRecorder) Close() error { b.closed.Add(1); return b.ReadCloser.Close() }

func TestImagesExecuteTLSResponseOwnership(t *testing.T) {
	for _, tc := range []struct {
		name          string
		oauth, stream bool
		status        int
		body          string
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
				AccountID:      9,
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
	require.Greater(t, sink.flushed, 0)
}

// 只提供测试所需的 I/O 与观察端口，不复制任何图片解析或业务算法。
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
		WrittenSize:         func() int { return 1 },

		StreamInterval:    func() time.Duration { return 0 },
		KeepaliveInterval: func() time.Duration { return 0 },
		ReadLimit:         func() int64 { return 1 << 20 },
		Backfill:          func(body []byte) []byte { return body },
		Logf:              func(string, ...any) {},
	}
}
