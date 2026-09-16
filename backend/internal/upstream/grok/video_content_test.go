package grok

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 本地 TLS 只替换网络目的地，核对签名下载不携带授权头、认证 relay 保留原头与 Range。
func TestVideoContentNativeLocalTLS(t *testing.T) {
	for _, signed := range []bool{true, false} {
		name := "relay"
		if signed {
			name = "signed"
		}
		t.Run(name, func(t *testing.T) {
			var requests, closes, active, fallback atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/status" {
					assert.Equal(t, "Bearer fixture-token", r.Header.Get("Authorization"))
					w.Header().Set("x-request-id", "status-id")
					if signed {
						_, _ = io.WriteString(w, `{"status":"done","video":{"url":"https://vidgen.x.ai/file?signature=fixture"}}`)
					} else {
						_, _ = io.WriteString(w, `{"status":"pending"}`)
					}
					return
				}
				assert.Equal(t, "bytes=1-3", r.Header.Get("Range"))
				if signed {
					assert.Empty(t, r.Header.Get("Authorization"))
					assert.Empty(t, r.Header.Get("X-Policy"))
				} else {
					assert.Equal(t, "Bearer fixture-token", r.Header.Get("Authorization"))
					assert.Equal(t, "fixture", r.Header.Get("X-Policy"))
				}
				w.Header().Set("Content-Range", "bytes 1-3/6")
				w.Header().Set("xai-request-id", "content-id")
				w.WriteHeader(http.StatusPartialContent)
				flusher, ok := w.(http.Flusher)
				if !ok {
					t.Error("local server does not support flushing")
					return
				}
				flusher.Flush()
				_, _ = io.WriteString(w, "bcd")
			}))
			defer server.Close()
			local, err := url.Parse(server.URL)
			require.NoError(t, err)
			options := VideoContentOptions{
				StatusURL:      server.URL + "/status",
				RequestID:      "video",
				Token:          "fixture-token",
				Range:          "bytes=1-3",
				Context:        func(ctx context.Context) context.Context { return ctx },
				ContentURL:     func() (string, error) { fallback.Add(1); return server.URL + "/file", nil },
				ApplyHeaders:   func(h http.Header, _ string) { h.Set("X-Policy", "fixture") },
				ReadStatus:     io.ReadAll,
				Latency:        func(time.Duration) {},
				TransportError: func(err error) error { return err },
				HTTPError:      func(*http.Response, string) error { t.Error("unexpected HTTP error"); return io.ErrUnexpectedEOF },
				Enter:          func() (func(), error) { active.Add(1); return func() { active.Add(-1) }, nil },
			}
			options.Do = func(req *http.Request) (*http.Response, error) {
				forward := req.Clone(req.Context())
				target := *req.URL
				target.Scheme = local.Scheme
				target.Host = local.Host
				forward.URL = &target
				resp, err := server.Client().Do(forward)
				if resp != nil {
					resp.Body = &voiceBody{resp.Body, &closes}
				}
				return resp, err
			}
			resource, err := OpenVideoContent(context.Background(), options)
			require.NoError(t, err)
			require.EqualValues(t, 1, active.Load())
			require.EqualValues(t, 1, closes.Load())
			require.Equal(t, int64(-1), resource.ContentLength, "chunked 响应不能被改成 Content-Length: 0")
			require.Equal(t, http.StatusPartialContent, resource.StatusCode)
			require.Equal(t, "content-id", resource.RequestID)
			data, err := io.ReadAll(resource)
			require.NoError(t, err)
			require.Equal(t, "bcd", string(data))
			require.NoError(t, resource.Close())
			require.NoError(t, resource.Close())
			require.EqualValues(t, 2, closes.Load())
			require.Zero(t, active.Load())
			require.EqualValues(t, 2, requests.Load())
			if signed {
				require.Zero(t, fallback.Load())
			} else {
				require.EqualValues(t, 1, fallback.Load())
			}
			raw, err := json.Marshal(resource)
			require.NoError(t, err)
			require.False(t, strings.Contains(string(raw), "signature"))
			require.NotContains(t, string(raw), "fixture-token")
		})
	}
}
