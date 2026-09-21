package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qoderContextClientFixture struct {
	request func(context.Context) (*http.Response, error)
}

func (c qoderContextClientFixture) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, _ []byte, _ map[string]string) (*http.Response, error) {
	return c.request(ctx)
}

// 原预算断言直接穿过真实 Execute，取消发生在供应商已进入之后。
func TestQoderForwardContextDetachesStreamingFromClientCancellation(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			value, fixture, _ := gatewaytestkit.NewDefaultQoderFixture()
			calls := 0
			fixture.Client = qoderContextClientFixture{request: func(execution context.Context) (*http.Response, error) {
				calls++
				deadline, ok := execution.Deadline()
				require.True(t, ok)
				remaining := time.Until(deadline)
				require.LessOrEqual(t, remaining, qoder.QoderStreamTimeout)
				require.Greater(t, remaining, qoder.QoderStreamTimeout-time.Minute)
				cancel()
				if !stream {
					require.ErrorIs(t, execution.Err(), context.Canceled)
					return nil, execution.Err()
				}
				require.NoError(t, execution.Err())
				body := qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "served"}}}}) + qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3}}) + "data: {\"body\":\"[DONE]\"}\n\n"
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			body := []byte(`{"model":"auto","stream":` + strconv.FormatBool(stream) + `,"messages":[{"role":"user","content":"hi"}]}`)
			result, err := ForwardQoderAttempt(ctx, c, fixture.Runtime, value, body, protocol.ProtocolOpenAIChatCompletions)
			require.Equal(t, 1, calls)
			if stream {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 3, result.Usage.OutputTokens)
			} else {
				require.ErrorIs(t, err, context.Canceled)
				require.Nil(t, result)
			}
		})
	}
}
