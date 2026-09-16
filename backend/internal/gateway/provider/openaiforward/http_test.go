package openaiforward

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
)

type trackedBody struct {
	io.Reader
	closes int
}

func (b *trackedBody) Close() error { b.closes++; return nil }

// 使用固定上游错误验证同账号恢复预算；此处不存在账号选择或完成提交端口。
func TestRunHTTPRecoveryBoundaries(t *testing.T) {
	for _, kind := range []string{"agent", "encrypted", "plain"} {
		t.Run(kind, func(t *testing.T) {
			bodies := []*trackedBody{}
			requests, recoveries, lineages := 0, 0, 0
			terminal := errors.New("original upstream failure")
			options := HTTPOptions{
				Exchange: native.HTTPExchangeOptions{
					RequestContext: func(ctx context.Context) (context.Context, context.CancelFunc) { return ctx, func() {} },
					Build: func(ctx context.Context, body []byte) (*http.Request, error) {
						return http.NewRequestWithContext(ctx, http.MethodPost, "http://fixture.invalid/responses", strings.NewReader(string(body)))
					},
					ApplyHeaders: func(http.Header) {}, Latency: func(time.Duration) {}, TransportError: func(err error) error { return err },
					Do: func(*http.Request) (*http.Response, error) {
						requests++
						b := &trackedBody{Reader: strings.NewReader(`{"error":{"message":"rejected"}}`)}
						bodies = append(bodies, b)
						return &http.Response{StatusCode: 400, Header: make(http.Header), Body: b}, nil
					},
				},
				ReadErrorBody:    func(resp *http.Response) []byte { b, _ := io.ReadAll(resp.Body); return b },
				IsAgentIdentity:  func(context.Context) bool { return kind == "agent" },
				InvalidAgentTask: func(int, []byte) bool { return true },
				RecoverAgentTask: func(context.Context) error { recoveries++; return nil },
				RedactErrorBody:  func(_ context.Context, b []byte) []byte { return b },
				ErrorDetails: func([]byte) (string, string) {
					if kind == "encrypted" {
						return "rejected", "invalid_encrypted_content"
					}
					return "rejected", ""
				},
				RetryEncrypted: func([]byte) ([]byte, bool, error) {
					recoveries++
					return []byte(`{"model":"gpt-5","input":[]}`), true, nil
				},
				MarkInvalidLineage: func([]byte) { lineages++ },
				CompactRetry:       func([]byte, int, string, []byte, bool) ([]byte, string, bool) { return nil, "", false },
				ShouldFailover:     func(int, string, []byte) bool { return false },
				ErrorResponse:      func(*http.Response, []byte, string) error { return terminal },
				Log:                func(string, ...any) {},
			}
			result, err := RunHTTP(context.Background(), HTTPInput{Body: []byte(`{"model":"gpt-5"}`)}, options)
			require.Nil(t, result)
			require.ErrorIs(t, err, terminal)
			expected := 2
			if kind == "plain" {
				expected = 1
			}
			require.Equal(t, expected, requests)
			if kind == "plain" {
				require.Zero(t, recoveries)
			} else {
				require.Equal(t, 1, recoveries)
			}
			if kind == "encrypted" {
				require.Equal(t, 1, lineages)
			} else {
				require.Zero(t, lineages)
			}
			for _, b := range bodies {
				require.Equal(t, 1, b.closes, "实际网络响应体在恢复前关闭")
			}
		})
	}
}

func TestRunHTTPTransportFailureDoesNotRecover(t *testing.T) {
	failure := errors.New("transport canceled")
	options := HTTPOptions{Exchange: native.HTTPExchangeOptions{
		RequestContext: func(ctx context.Context) (context.Context, context.CancelFunc) { return ctx, func() {} },
		Build:          func(context.Context, []byte) (*http.Request, error) { return nil, failure },
	}}
	result, err := RunHTTP(context.Background(), HTTPInput{}, options)
	require.Nil(t, result)
	require.ErrorIs(t, err, failure)
}
