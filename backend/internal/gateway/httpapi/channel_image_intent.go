package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
)

// ChannelMappedImageIntent 保留先改写渠道模型/报文，再判定图片意图的顺序。
func ChannelMappedImageIntent(endpoint, model string, body []byte, mapping routing.ChannelMappingResult, platform string, replace requeststate.ModelBodyReplacer) ([]byte, string, bool) {
	target := requeststate.ChannelMappedModel(model, mapping)
	rewritten := requeststate.ModelMappedBody(body, mapping.Mapped, target, replace)
	return rewritten, target, provider.ImageIntentForPlatform(endpoint, target, rewritten, platform)
}

// SeedOpenAIForwardImageIntentHint 在渠道改写后继续保持 unknown，不能提前固定旧报文的结论。
func SeedOpenAIForwardImageIntentHint(c *gin.Context, mapped, image bool) {
	if mapped {
		return
	}
	SetOpenAIImageIntentHint(c, image)
}
