package httpapi

import (
	"testing"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestResolveOpenAIGroupMappedImageIntent 验证生图能力判断使用分组映射后的模型 C 和请求体。
func TestResolveOpenAIGroupMappedImageIntent(t *testing.T) {
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
			mappedBody, routingModel, imageIntent := GroupMappedImageIntent(
				"/v1/responses",
				tt.requestedModel,
				body,
				routing.GroupMappingResult{Mapped: true, MappedModel: tt.mappedModel},
				capability.PlatformOpenAI,
				openaiprotocol.ReplaceModelInBody,
			)

			require.Equal(t, tt.mappedModel, routingModel)
			require.Equal(t, tt.mappedModel, gjson.GetBytes(mappedBody, "model").String())
			require.Equal(t, tt.wantIntent, imageIntent)
		})
	}
}

func TestSeedOpenAIForwardImageIntentHint(t *testing.T) {
	tests := []struct {
		name        string
		groupMapped bool
		imageIntent bool
		wantHint    bool
	}{
		{name: "seed true", imageIntent: true, wantHint: true},
		{name: "seed false", imageIntent: false, wantHint: true},
		{name: "mapped body stays unknown", groupMapped: true, imageIntent: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &gin.Context{}
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			SeedOpenAIForwardImageIntentHint(c, tt.groupMapped, tt.imageIntent)

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
