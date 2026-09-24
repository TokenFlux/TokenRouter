package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 三个公开入口必须在访问调度或上游之前执行拒绝策略。
func TestAnthropicReasoningPolicy_AllEntrypointsDeny(t *testing.T) {
	h := newMessageEndpointsFixture(nil, nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, nil, nil)
	for _, tc := range []struct {
		path, field string
		handle      func(*gin.Context)
	}{
		{"/v1/messages", `"output_config":{"effort":"max"}`, h.Messages},
		{"/v1/responses", `"reasoning":{"effort":"max"}`, h.Responses},
		{"/v1/chat/completions", `"reasoning_effort":"max"`, h.ChatCompletions},
	} {
		t.Run(tc.path, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"model":"claude-fable-5-1","messages":[{"role":"user","content":"hi"}],"input":"hi","max_tokens":100,`+tc.field+`}`))
			c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{Group: &routing.Group{
				Platform: capability.PlatformAnthropic, MaxReasoningEffort: "high", MaxReasoningEffortOverLimit: "deny",
			}})
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1})
			tc.handle(c)
			require.Equal(t, http.StatusForbidden, c.Writer.Status())
			require.Equal(t, "max", *requeststate.RequestedReasoningEffortFromContext(c.Request.Context()))
		})
	}
}
