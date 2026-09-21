package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestApplyOpenAIReasoningEffortPolicyForRequest 验证分组可自行决定不兼容档位的目标值。
func TestApplyOpenAIReasoningEffortPolicyForRequest_MapsConfiguredNoneForAstra(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	apiKey := &apikey.APIKey{
		Group: &routing.Group{
			Platform: capability.PlatformOpenAI,
			ReasoningEffortMappings: []routing.ReasoningEffortMapping{{
				From:      "none",
				To:        "low",
				MatchType: "exact",
				Model:     "gpt-6-astra",
			}},
		},
	}

	body := []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"none"}}`)
	updated, changed, err := applyOpenAIReasoningEffortPolicyForRequest(c, apiKey, body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "low", gjson.GetBytes(updated, "reasoning.effort").String())
	requested := service.RequestedReasoningEffortFromContext(c.Request.Context())
	require.NotNil(t, requested)
	require.Equal(t, "none", *requested)
	forwarded := "low"
	result := &forwardcore.OpenAIResult{ReasoningEffort: &forwarded}
	stampOpenAIRequestedReasoningEffort(result, c)
	require.NotNil(t, result.RequestedReasoningEffort)
	require.Equal(t, "none", *result.RequestedReasoningEffort)
}

func TestApplyAnthropicReasoningEffortPolicyForRequest_CapsOutputConfigEffort(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	apiKey := &apikey.APIKey{Group: &routing.Group{
		Platform:           capability.PlatformAnthropic,
		MaxReasoningEffort: "high",
	}}
	body := []byte(`{"model":"claude-fable-5-1","output_config":{"effort":"max"}}`)
	updated, changed, err := applyAnthropicReasoningEffortPolicyForRequest(c, apiKey, body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "high", gjson.GetBytes(updated, "output_config.effort").String())
	requested := service.RequestedReasoningEffortFromContext(c.Request.Context())
	require.NotNil(t, requested)
	require.Equal(t, "max", *requested)
}

// 三个公开入口必须在访问调度或上游之前执行拒绝策略。
func TestAnthropicReasoningPolicy_AllEntrypointsDeny(t *testing.T) {
	h := &GatewayHandler{}
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
			c.Set(string(middleware.ContextKeyAPIKey), &apikey.APIKey{Group: &routing.Group{
				Platform: capability.PlatformAnthropic, MaxReasoningEffort: "high", MaxReasoningEffortOverLimit: "deny",
			}})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			tc.handle(c)
			require.Equal(t, http.StatusForbidden, c.Writer.Status())
			require.Equal(t, "max", *service.RequestedReasoningEffortFromContext(c.Request.Context()))
		})
	}
}

// 强制平台入口与缺省请求都不能被 Anthropic 分组策略意外改写。
func TestAnthropicReasoningPolicy_PreservesForcedPlatformAndDefault(t *testing.T) {
	apiKey := &apikey.APIKey{Group: &routing.Group{Platform: capability.PlatformAnthropic, MaxReasoningEffort: "low"}}
	for _, forced := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		body := []byte(`{"model":"claude-fable-5-1"}`)
		if forced {
			c.Set(string(middleware.ContextKeyForcePlatform), capability.PlatformAntigravity)
			body = []byte(`{"model":"claude-fable-5-1","output_config":{"effort":"max"}}`)
		}
		updated, changed, err := applyAnthropicReasoningEffortPolicyForRequest(c, apiKey, body)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, updated)
	}
}
