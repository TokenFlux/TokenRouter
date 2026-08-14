package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	middleware "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestChatCompletionsRejectsGPTImageModelsBeforeScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, model := range []string{"gpt-image-1", "gpt-image-1.5", "gpt-image-2"} {
		for _, tc := range []struct {
			name string
			call func(*gin.Context)
		}{
			{
				name: "gateway",
				call: newGatewayModelsHandlerForTest(nil).ChatCompletions,
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
				_, selected := c.Get(opsAccountIDKey)
				require.False(t, selected, "rejection must happen before account selection")
			})
		}
	}
}

// TestChatCompletionsRejectsChannelMappedImageModel 验证两个 Chat Completions 入口都按渠道模型 C 校验端点能力。
func TestChatCompletionsRejectsChannelMappedImageModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(4349)
	channelService := newGatewayModelsChannelServiceForTest(groupID, service.PlatformOpenAI, service.Channel{
		ID:     4349,
		Status: service.StatusActive,
		ModelMapping: map[string]map[string]string{
			service.PlatformOpenAI: {"draw-alias": "gpt-image-1"},
		},
	})

	tests := []struct {
		name string
		call func(*gin.Context)
	}{
		{
			name: "gateway",
			call: newGatewayModelsHandlerWithChannelForTest(nil, channelService).ChatCompletions,
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
			_, selected := c.Get(opsAccountIDKey)
			require.False(t, selected, "渠道映射后的端点拒绝必须发生在账号选择之前")
		})
	}
}

// TestOpenAIChatCompletionsImageModelRejectionDoesNotAcquireConcurrency 确认无效请求不会占用并发额度。
func TestOpenAIChatCompletionsImageModelRejectionDoesNotAcquireConcurrency(t *testing.T) {
	var acquireCalls atomic.Int64
	cache := &concurrencyCacheMock{
		acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) {
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

func newOpenAIImageChatRejectionHandler(t *testing.T) *OpenAIGatewayHandler {
	t.Helper()
	return newOpenAIImageChatRejectionHandlerWithCache(t, &concurrencyCacheMock{})
}

func newOpenAIImageChatRejectionHandlerWithCache(t *testing.T, cache *concurrencyCacheMock) *OpenAIGatewayHandler {
	t.Helper()
	return newOpenAIImageChatRejectionHandlerWithService(t, cache, &service.OpenAIGatewayService{})
}

// newOpenAIImageChatRejectionHandlerWithChannel 构造带渠道映射的 OpenAI Chat 测试处理器。
func newOpenAIImageChatRejectionHandlerWithChannel(t *testing.T, channelService *service.ChannelService) *OpenAIGatewayHandler {
	t.Helper()
	gatewayService := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, channelService, nil, nil, nil, nil,
	)
	return newOpenAIImageChatRejectionHandlerWithService(t, &concurrencyCacheMock{}, gatewayService)
}

// newOpenAIImageChatRejectionHandlerWithService 复用最小依赖构造 Chat 端点测试处理器。
func newOpenAIImageChatRejectionHandlerWithService(t *testing.T, cache *concurrencyCacheMock, gatewayService *service.OpenAIGatewayService) *OpenAIGatewayHandler {
	t.Helper()
	return &OpenAIGatewayHandler{
		gatewayService:      gatewayService,
		billingCacheService: &service.BillingCacheService{},
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Second),
	}
}

func setImageChatTestAuth(c *gin.Context) {
	setImageChatTestAuthForGroup(c, 0)
}

// setImageChatTestAuthForGroup 注入带可选分组的图片端点测试身份。
func setImageChatTestAuthForGroup(c *gin.Context, groupID int64) {
	apiKey := &service.APIKey{ID: 4348, UserID: 4348, User: &service.User{ID: 4348}}
	if groupID > 0 {
		apiKey.GroupID = &groupID
		apiKey.Group = &service.Group{ID: groupID, Platform: service.PlatformOpenAI}
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 1})
}
