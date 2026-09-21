package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/gin-gonic/gin"
)

func TestS09QoderForwardPartialResult(t *testing.T) {

	a, s, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "served"}}}}) + qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3}}) + qoderWrappedErrorSSELineForTest(t, 502, map[string]any{"code": "500", "message": "fixture failure"})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	result, err := ForwardQoderAttempt(c.Request.Context(), c, s.Runtime, a, []byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`), protocolcore.ProtocolOpenAIChatCompletions)
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

	a, s, client := gatewaytestkit.NewDefaultQoderFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	_, err := ForwardQoderAttempt(ctx, c, s.Runtime, a, []byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`), protocolcore.ProtocolOpenAIChatCompletions)
	if len(client.Requests) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled request starts %d upstream inference(s); err=%v", len(client.Requests), err)
	}
}
