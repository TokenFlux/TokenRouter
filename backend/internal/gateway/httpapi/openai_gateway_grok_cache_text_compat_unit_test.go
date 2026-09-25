//go:build unit

// unit 断言通过测试兼容入口验证生产实现。
package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/gin-gonic/gin"
)

func explicitGrokCacheSeed(c *gin.Context, body []byte, explicitKey string) string {
	return grok.ExplicitCacheSeed(grokCacheInput(c, explicitKey, ""), body)
}
