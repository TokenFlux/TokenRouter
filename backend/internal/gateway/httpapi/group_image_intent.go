package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
)

// GroupMappedImageIntent 保留先改写分组映射模型/报文，再判定图片意图的顺序。
func GroupMappedImageIntent(endpoint, model string, body []byte, mapping routing.GroupMappingResult, platform string, replace requeststate.ModelBodyReplacer) ([]byte, string, bool) {
	target := requeststate.GroupMappedModel(model, mapping)
	rewritten := requeststate.ModelMappedBody(body, mapping.Mapped, target, replace)
	return rewritten, target, provider.ImageIntentForPlatform(endpoint, target, rewritten, platform)
}

// SeedOpenAIForwardImageIntentHint 在分组映射改写后继续保持 unknown，不能提前固定旧报文的结论。
func SeedOpenAIForwardImageIntentHint(c *gin.Context, mapped, image bool) {
	if mapped {
		return
	}
	SetOpenAIImageIntentHint(c, image)
}
