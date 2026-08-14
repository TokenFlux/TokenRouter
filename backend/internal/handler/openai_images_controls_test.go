package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayHandlerImages_DisabledGroupRejectsBeforeScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-image-2","prompt":"draw","size":"1024x1024"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	groupID := int64(111)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      222,
		GroupID: &groupID,
		Group: &service.Group{
			ID:                   groupID,
			AllowImageGeneration: false,
		},
		User: &service.User{ID: 333},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 333, Concurrency: 1})

	h := &OpenAIGatewayHandler{
		gatewayService:      &service.OpenAIGatewayService{},
		billingCacheService: &service.BillingCacheService{},
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   &ConcurrencyHelper{concurrencyService: &service.ConcurrencyService{}},
	}

	h.Images(c)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Contains(t, rec.Body.String(), service.ImageGenerationPermissionMessage())
}

// TestOpenAIGatewayHandlerImagesValidatesChannelMappedModel 验证同步 Images 入口在渠道映射后校验模型族。
func TestOpenAIGatewayHandlerImagesValidatesChannelMappedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(112)
	channelService := newGatewayModelsChannelServiceForTest(groupID, service.PlatformOpenAI, service.Channel{
		ID:     112,
		Status: service.StatusActive,
		ModelMapping: map[string]map[string]string{
			service.PlatformOpenAI: {
				"draw-alias":  "gpt-image-1",
				"gpt-image-2": "gpt-5.4",
			},
		},
	})

	tests := []struct {
		name       string
		model      string
		allowImage bool
		wantStatus int
		wantText   string
	}{
		{name: "普通别名映射为生图模型", model: "draw-alias", wantStatus: http.StatusForbidden, wantText: service.ImageGenerationPermissionMessage()},
		{name: "生图别名映射为普通模型", model: "gpt-image-2", allowImage: true, wantStatus: http.StatusBadRequest, wantText: `got "gpt-5.4"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"` + tt.model + `","prompt":"draw"}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = req
			apiKey := &service.APIKey{
				ID:      223,
				GroupID: &groupID,
				Group: &service.Group{
					ID:                   groupID,
					Platform:             service.PlatformOpenAI,
					AllowImageGeneration: tt.allowImage,
				},
				User: &service.User{ID: 334},
			}
			c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 334, Concurrency: 1})

			newOpenAIImageChatRejectionHandlerWithChannel(t, channelService).Images(c)

			require.Equal(t, tt.wantStatus, rec.Code)
			require.Contains(t, gjson.GetBytes(rec.Body.Bytes(), "error.message").String(), tt.wantText)
		})
	}
}
