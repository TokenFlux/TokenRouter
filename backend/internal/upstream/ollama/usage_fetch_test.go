package ollama

import (
	"context"
	"fmt"
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

// 本地 TLS 测试只替换拨号目标；供应商输入 URL 和响应身份保持原样核对。
type fetchBody struct {
	io.ReadCloser
	closed *atomic.Int64
}

func (b *fetchBody) Close() error { b.closed.Add(1); return b.ReadCloser.Close() }
func TestFetchUsageLocalTLSContract(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body, failure string
		unauthorized  bool
	}{
		{"success", 200, string(ollamaUsageFixture(t)), "", false},
		{"redirect", 302, "", "redirect_blocked", false},
		{"unauthorized", 401, "", "unauthorized", true},
		{"sign in HTML", 200, "<p>Sign in to Ollama</p>", "unauthorized", true},
		{"oversized", 200, strings.Repeat("x", MaxBodyBytes+1), "response_too_large", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls, closed atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "/settings", r.URL.Path)
				assert.Equal(t, "wos-session=cookie-secret", r.Header.Get("Cookie"))
				assert.Equal(t, "sub2api-ollama-usage/1", r.UserAgent())
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			target, err := url.Parse(server.URL)
			require.NoError(t, err)
			input := FetchInput{ObservedAt: time.Now(), Cookie: "wos-session=cookie-secret"}
			result, err := FetchUsage(context.Background(), input, FetchOptions{Context: func(ctx context.Context) context.Context { return ctx }, Do: func(r *http.Request) (*http.Response, error) {
				assert.Equal(t, SettingsURL, r.URL.String())
				forward := r.Clone(r.Context())
				next := *r.URL
				next.Scheme = target.Scheme
				next.Host = target.Host
				forward.URL = &next
				resp, err := server.Client().Do(forward)
				if resp != nil {
					resp.Request = r
					resp.Body = &fetchBody{resp.Body, &closed}
				}
				return resp, err
			}})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.failure, result.Failure)
			require.Equal(t, tt.unauthorized, result.Unauthorized)
			require.EqualValues(t, 1, calls.Load())
			require.Equal(t, calls.Load(), closed.Load())
			require.NotContains(t, fmt.Sprintf("%v %#v", input, input), "cookie-secret")
			if tt.name == "success" {
				require.Equal(t, "max", result.Data.Plan)
			}
			if tt.name == "redirect" {
				require.Equal(t, 30*time.Second, result.RetryAfter)
			}
		})
	}
}
