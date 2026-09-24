package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestChatCompletionsRejectsGPTImageModelsBeforeScheduling(t *testing.T) {

	for _, model := range []string{"gpt-image-1", "gpt-image-1.5", "gpt-image-2"} {
		for _, tc := range []struct {
			name string
			call func(*gin.Context)
		}{
			{
				name: "gateway",
				call: newGatewayExecutionHandlerForTest(nil).ChatCompletions,
			},
			{
				name: "openai_gateway",
				call: newOpenAIImageChatRejectionHandler(t).ChatCompletions,
			},
		} {
			t.Run(tc.name+"/"+model, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				body := []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"draw"}]}`)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
				setImageChatTestAuth(c)

				tc.call(c)

				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
				require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "Chat Completions")
				_, selected := c.Get(gatewayhttp.OpsAccountIDKey)
				require.False(t, selected, "rejection must happen before account selection")
			})
		}
	}
}

// TestChatCompletionsRejectsChannelMappedImageModel 验证两个 Chat Completions 入口都按渠道模型 C 校验端点能力。
func TestChatCompletionsRejectsChannelMappedImageModel(t *testing.T) {

	groupID := int64(4349)
	channelService := newGatewayExecutionChannelServiceForTest(groupID, capability.PlatformOpenAI, routing.Channel{
		ID:     4349,
		Status: billing.StatusActive,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {"draw-alias": "gpt-image-1"},
		},
	})

	tests := []struct {
		name string
		call func(*gin.Context)
	}{
		{
			name: "gateway",
			call: newGatewayExecutionHandlerWithChannelForTest(nil, channelService).ChatCompletions,
		},
		{
			name: "openai_gateway",
			call: newOpenAIImageChatRejectionHandlerWithChannel(t, channelService).ChatCompletions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := []byte(`{"model":"draw-alias","messages":[{"role":"user","content":"draw"}]}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			setImageChatTestAuthForGroup(c, groupID)

			tt.call(c)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "Chat Completions")
			_, selected := c.Get(gatewayhttp.OpsAccountIDKey)
			require.False(t, selected, "渠道映射后的端点拒绝必须发生在账号选择之前")
		})
	}
}

// TestOpenAIChatCompletionsImageModelRejectionDoesNotAcquireConcurrency 确认无效请求不会占用并发额度。
func TestOpenAIChatCompletionsImageModelRejectionDoesNotAcquireConcurrency(t *testing.T) {
	var acquireCalls atomic.Int64
	cache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) {
			acquireCalls.Add(1)
			return true, nil
		},
	}
	h := newOpenAIImageChatRejectionHandlerWithCache(t, cache)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"gpt-image-2","messages":[{"role":"user","content":"draw"}]}`,
	))
	setImageChatTestAuth(c)

	h.ChatCompletions(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, acquireCalls.Load(), "rejection must happen before user/account concurrency and scheduling")
}

func newOpenAIImageChatRejectionHandler(t *testing.T) *gatewayHTTPEndpointsFixture {
	t.Helper()
	return newOpenAIImageChatRejectionHandlerWithCache(t, &httptestkit.ConcurrencyHooks{})
}

func newOpenAIImageChatRejectionHandlerWithCache(t *testing.T, cache *httptestkit.ConcurrencyHooks) *gatewayHTTPEndpointsFixture {
	t.Helper()
	return newOpenAIImageChatRejectionHandlerWithService(t, cache, &service.OpenAIGatewayService{}, newExecutionAvailabilityForTest(

		// newOpenAIImageChatRejectionHandlerWithChannel 构造带渠道映射的 OpenAI Chat 测试处理器。
		nil, nil, nil), newEmptyCompatibleSelectionFixture())
}

func newOpenAIImageChatRejectionHandlerWithChannel(t *testing.T, channelService *routing.ChannelService) *gatewayHTTPEndpointsFixture {
	t.Helper()
	gatewayService, gatewayServiceChoices, _ := newOpenAIExecutionAndSelectionFixture(
		nil, nil, nil, nil, nil, nil,
		nil, nil, nil, newOpenAIExecutionCredentialsForTest(nil,
			nil), nil, nil, channelService, nil, nil, responseHeaderFilterForTest(nil), nil, nil, nil,
	)
	gatewayService.BindCompletionRecorder(newHTTPCompletionFixture(nil, nil, nil,
		nil, nil, channelService, nil, true))

	return newOpenAIImageChatRejectionHandlerWithService(t, &httptestkit.ConcurrencyHooks{}, gatewayService, newExecutionAvailabilityForTest(nil,

		channelService, nil), gatewayServiceChoices,
	)
}

// newOpenAIImageChatRejectionHandlerWithService 复用最小依赖构造 Chat 端点测试处理器。
func newOpenAIImageChatRejectionHandlerWithService(t *testing.T, cache *httptestkit.ConcurrencyHooks, gatewayService *service.OpenAIGatewayService, availability *gatewayModelAvailability, choices *selection.Compatible) *gatewayHTTPEndpointsFixture {
	t.Helper()

	return newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source: gatewayService, Availability: availability, Choices: choices,
		Funding: &admission.FundingAdmission{},
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
			Event: logging.Event},
		), gatewayhttp.SSEPingFormatNone, time.Second),
	})
}

func setImageChatTestAuth(c *gin.Context) {
	setImageChatTestAuthForGroup(c, 0)
}

// setImageChatTestAuthForGroup 注入带可选分组的图片端点测试身份。
func setImageChatTestAuthForGroup(c *gin.Context, groupID int64) {
	apiKey := &apikey.APIKey{ID: 4348, UserID: 4348, User: &identity.User{ID: 4348}}
	if groupID > 0 {
		apiKey.GroupID = &groupID
		apiKey.Group = &routing.Group{ID: groupID, Platform: capability.PlatformOpenAI}
	}
	c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.UserID, Concurrency: 1})
}
