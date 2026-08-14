package handler

import (
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestResolveOpenAIChannelMappedImageIntent 验证生图能力判断使用渠道映射后的模型 C 和请求体。
func TestResolveOpenAIChannelMappedImageIntent(t *testing.T) {
	tests := []struct {
		name           string
		requestedModel string
		mappedModel    string
		wantIntent     bool
	}{
		{
			name:           "普通别名映射为生图模型",
			requestedModel: "draw-alias",
			mappedModel:    "gpt-image-1",
			wantIntent:     true,
		},
		{
			name:           "生图别名映射为普通模型",
			requestedModel: "gpt-image-1",
			mappedModel:    "gpt-5.1",
			wantIntent:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"` + tt.requestedModel + `","input":"hello"}`)
			mappedBody, routingModel, imageIntent := resolveOpenAIChannelMappedImageIntent(
				"/v1/responses",
				tt.requestedModel,
				body,
				service.ChannelMappingResult{Mapped: true, MappedModel: tt.mappedModel},
				service.PlatformOpenAI,
				service.ReplaceModelInBody,
			)

			require.Equal(t, tt.mappedModel, routingModel)
			require.Equal(t, tt.mappedModel, gjson.GetBytes(mappedBody, "model").String())
			require.Equal(t, tt.wantIntent, imageIntent)
		})
	}
}

func TestSeedOpenAIForwardImageIntentHint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name          string
		channelMapped bool
		imageIntent   bool
		wantHint      bool
	}{
		{name: "seed true", imageIntent: true, wantHint: true},
		{name: "seed false", imageIntent: false, wantHint: true},
		{name: "mapped body stays unknown", channelMapped: true, imageIntent: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &gin.Context{}
			service.SetOpenAIClientTransport(c, service.OpenAIClientTransportHTTP)

			seedOpenAIForwardImageIntentHint(c, tt.channelMapped, tt.imageIntent)

			var hintValues []bool
			for _, value := range c.Keys {
				if hint, ok := value.(bool); ok {
					hintValues = append(hintValues, hint)
				}
			}
			if !tt.wantHint {
				require.Empty(t, hintValues)
				return
			}
			require.Equal(t, []bool{tt.imageIntent}, hintValues)
		})
	}
}
