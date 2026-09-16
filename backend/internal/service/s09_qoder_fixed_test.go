package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// S09 固定回归来自规划阶段原实现复现；保留真实输出、usage 与失败的联合断言。
func TestS09QoderPartialUsageOnError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "served"}}}}) + qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3, "total_tokens": 15}}) + qoderWrappedErrorSSELineForTest(t, 502, map[string]any{"code": "500", "message": "local fixture upstream failure"})
	writers := map[string]func(context.Context, *gin.Context, *http.Response) (*qoderStreamResult, error){
		"chat": func(ctx context.Context, c *gin.Context, r *http.Response) (*qoderStreamResult, error) {
			return WriteQoderOpenAIStreamResponse(ctx, c, "auto", r)
		},
		"messages": func(ctx context.Context, c *gin.Context, r *http.Response) (*qoderStreamResult, error) {
			return WriteQoderAnthropicStreamResponse(ctx, c, "auto", r)
		},
		"responses": func(ctx context.Context, c *gin.Context, r *http.Response) (*qoderStreamResult, error) {
			return WriteQoderResponsesStreamResponse(ctx, c, "auto", r)
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
func TestS09QoderForwardPartialResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a, s, client := newQoderGatewayForwardTestService()
	client.body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "served"}}}}) + qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3}}) + qoderWrappedErrorSSELineForTest(t, 502, map[string]any{"code": "500", "message": "fixture failure"})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	result, err := s.ForwardChatCompletions(c.Request.Context(), c, a, []byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err == nil || !strings.Contains(rec.Body.String(), "served") {
		t.Fatalf("fixture did not reach post-output failure: %v", err)
	}
	if result == nil {
		t.Fatal("ForwardChatCompletions discards observed partial usage before handler completion")
	}
	if result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 3 {
		t.Fatalf("incorrect observed usage: %+v", result.Usage)
	}
}
func TestS09QoderCanceledBeforeForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a, s, client := newQoderGatewayForwardTestService()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	_, err := s.ForwardChatCompletions(ctx, c, a, []byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if len(client.requests) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled request starts %d upstream inference(s); err=%v", len(client.requests), err)
	}
}
