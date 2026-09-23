package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayHandlerImages_DisabledGroupRejectsBeforeScheduling(t *testing.T) {

	body := []byte(`{"model":"gpt-image-2","prompt":"draw","size":"1024x1024"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	groupID := int64(111)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      222,
		GroupID: &groupID,
		Group: &routing.Group{
			ID:                   groupID,
			AllowImageGeneration: false,
		},
		User: &identity.User{ID: 333},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 333, Concurrency: 1})

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:      &service.OpenAIGatewayService{},
		Funding:     &admission.FundingAdmission{},
		Keys:        &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(&scheduler.ConcurrencyService{}, gatewayhttp.SSEPingFormatNone, 0),
	})

	h.Images(c)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Contains(t, rec.Body.String(), gatewaymedia.ImageGenerationPermissionMessage)
}

// TestOpenAIGatewayHandlerImagesValidatesChannelMappedModel 验证同步 Images 入口在渠道映射后校验模型族。
func TestOpenAIGatewayHandlerImagesValidatesChannelMappedModel(t *testing.T) {

	groupID := int64(112)
	channelService := newGatewayExecutionChannelServiceForTest(groupID, capability.PlatformOpenAI, routing.Channel{
		ID:     112,
		Status: billing.StatusActive,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {
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
		{name: "普通别名映射为生图模型", model: "draw-alias", wantStatus: http.StatusForbidden, wantText: gatewaymedia.ImageGenerationPermissionMessage},
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
			apiKey := &apikey.APIKey{
				ID:      223,
				GroupID: &groupID,
				Group: &routing.Group{
					ID:                   groupID,
					Platform:             capability.PlatformOpenAI,
					AllowImageGeneration: tt.allowImage,
				},
				User: &identity.User{ID: 334},
			}
			c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 334, Concurrency: 1})

			newOpenAIImageChatRejectionHandlerWithChannel(t, channelService).Images(c)

			require.Equal(t, tt.wantStatus, rec.Code)
			require.Contains(t, gjson.GetBytes(rec.Body.Bytes(), "error.message").String(), tt.wantText)
		})
	}
}
