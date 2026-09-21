package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
)

// S09 固定回归来自规划阶段原实现复现；保留真实输出、usage 与失败的联合断言。
func TestS09QoderPartialUsageOnError(t *testing.T) {

	body := qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "served"}}}}) + qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3, "total_tokens": 15}}) + qoderWrappedErrorSSELineForTest(t, 502, map[string]any{"code": "500", "message": "local fixture upstream failure"})
	writers := map[string]func(context.Context, *gin.Context, *http.Response) (*qoder.QoderStreamResult, error){
		"chat": func(ctx context.Context, c *gin.Context, r *http.Response) (*qoder.QoderStreamResult, error) {
			return qoder.WriteQoderOpenAIStreamResponse(ctx, &upstream.OutputContext{Writer: c.Writer}, "auto", r)
		},
		"messages": func(ctx context.Context, c *gin.Context, r *http.Response) (*qoder.QoderStreamResult, error) {
			return qoder.WriteQoderAnthropicStreamResponse(ctx, &upstream.OutputContext{Writer: c.Writer}, "auto", r)
		},
		"responses": func(ctx context.Context, c *gin.Context, r *http.Response) (*qoder.QoderStreamResult, error) {
			return qoder.WriteQoderResponsesStreamResponse(ctx, &upstream.OutputContext{Writer: c.Writer}, "auto", r)
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			result, err := write(context.Background(), c, &http.Response{Body: io.NopCloser(strings.NewReader(body))})
			if err == nil {
				t.Fatal("fixture must produce upstream error")
			}
			if !strings.Contains(rec.Body.String(), "served") {
				t.Fatal("fixture must emit real output")
			}
			if result == nil {
				t.Fatalf("observed input=12 output=3 before upstream error but result=nil; written=%d", rec.Body.Len())
			}
			if result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 3 || !result.HasOutput {
				t.Fatalf("lost partial result: %+v", result)
			}
		})
	}
}
