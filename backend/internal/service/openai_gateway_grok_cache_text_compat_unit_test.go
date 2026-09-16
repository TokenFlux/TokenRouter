//go:build unit

// 生产消费者已迁出；原 unit 断言通过唯一实现的兼容入口验证。
package service

import (
	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/gin-gonic/gin"
)

func explicitGrokCacheSeed(c *gin.Context, body []byte, explicitKey string) string {
	return nativegrok.ExplicitCacheSeed(grokCacheInput(c, explicitKey, ""), body)
}
