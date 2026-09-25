package httpapi

import (
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/gin-gonic/gin"
)

func OpenAICompatibleRequestPlatform(apiKey *apikey.APIKey) string {
	if apiKey != nil && apiKey.Group != nil {
		switch apiKey.Group.Platform {
		case capability.PlatformGrok, capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek:
			return apiKey.Group.Platform
		}
	}
	return capability.PlatformOpenAI
}

// EffectiveAPIKeyPlatform 返回当前 API key 在 handler 层应使用的平台。
// 强制平台路由由中间件单独处理；没有可识别的平台时保持 OpenAI 兼容默认值。
func EffectiveAPIKeyPlatform(c *gin.Context, apiKey *apikey.APIKey) string {
	if c != nil {
		if forced, ok := keyhttp.GetForcePlatformFromContext(c); ok && strings.TrimSpace(forced) != "" {
			return strings.TrimSpace(forced)
		}
	}
	return OpenAICompatibleRequestPlatform(apiKey)
}
