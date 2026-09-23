package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 非法 service_tier 必须在两个 OpenAI 端点（/v1/responses、/v1/chat/completions）
// 上以 OpenAI 兼容错误结构返回 HTTP 400。这些用例在 handler 的 service_tier
// 校验处短路，不会进入账号选择/重试。
//
// 合法值（fast/priority/flex/auto/default/scale/ultrafast）与省略/null 的接受语义由
// service 层纯校验函数 TestValidateOpenAIServiceTierField 覆盖，避免 handler
// 测试走入真实账号选择/重试路径。

func newServiceTierHandlerTest(t *testing.T) *gatewayHTTPEndpointsFixture {
	t.Helper()
	return newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  &service.OpenAIGatewayService{},
		Funding: newFundingAdmissionFixture(newBillingEligibilityFixture(&config.Config{RunMode: config.RunModeSimple}), &config.Config{RunMode: config.RunModeSimple}),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(
			&httptestkit.ConcurrencySequence{UserSeq: []bool{true}}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
				Event: logging.Event},
		), gatewayhttp.SSEPingFormatNone, 0),
		Config: &config.Config{},
		Images: &scheduler.ImageConcurrencyLimiter{}, Availability: newExecutionAvailabilityForTest(nil, nil, nil),
	})
}

func runOpenAIHandlerServiceTierTest(t *testing.T, path, body string, handler func(h *gatewayHTTPEndpointsFixture, c *gin.Context)) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(6401)
	userID := int64(6402)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      6403,
		GroupID: &groupID,
		Group: &routing.Group{
			ID:       groupID,
			Platform: capability.PlatformOpenAI,
		},
		User: &identity.User{ID: userID, Status: billing.StatusActive},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: userID, Concurrency: 1})

	handler(newServiceTierHandlerTest(t), c)
	return rec
}

func TestOpenAIGatewayHandlerResponses_InvalidServiceTierRejected400(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-5.5","input":"hi","service_tier":"turbo"}`,
		`{"model":"gpt-5.5","input":"hi","service_tier":"SPEED"}`,
		`{"model":"gpt-5.5","input":"hi","service_tier":""}`,
		`{"model":"gpt-5.5","input":"hi","service_tier":123}`,
		`{"model":"gpt-5.5","input":"hi","service_tier":{}}`,
	} {
		rec := runOpenAIHandlerServiceTierTest(t, "/v1/responses", body, func(h *gatewayHTTPEndpointsFixture, c *gin.Context) {
			h.Responses(c)
		})
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", body)
		require.Contains(t, rec.Body.String(), "invalid_request_error", "body=%s", body)
		require.Contains(t, rec.Body.String(), "invalid service_tier", "body=%s", body)
	}
}

func TestOpenAIGatewayHandlerChatCompletions_InvalidServiceTierRejected400(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"service_tier":"turbo"}`,
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"service_tier":"ultra"}`,
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"service_tier":""}`,
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"service_tier":["priority"]}`,
	} {
		rec := runOpenAIHandlerServiceTierTest(t, "/v1/chat/completions", body, func(h *gatewayHTTPEndpointsFixture, c *gin.Context) {
			h.ChatCompletions(c)
		})
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", body)
		require.Contains(t, rec.Body.String(), "invalid_request_error", "body=%s", body)
		require.Contains(t, rec.Body.String(), "invalid service_tier", "body=%s", body)
	}
}
