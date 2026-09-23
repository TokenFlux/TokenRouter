package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type OpenAIDependencies struct{ Handler, Gateway, Funding, Keys, Concurrency bool }

// Missing 保留原错误条目及顺序，nil Handler 不继续探测其字段。
func (d OpenAIDependencies) Missing() []string {
	missing := make([]string, 0, 5)
	if !d.Handler {
		return append(missing, "handler")
	}
	if !d.Gateway {
		missing = append(missing, "gatewayService")
	}
	if !d.Funding {
		missing = append(missing, "billingCacheService")
	}
	if !d.Keys {
		missing = append(missing, "apiKeyService")
	}
	if !d.Concurrency {
		missing = append(missing, "concurrencyHelper")
	}
	return missing
}

func (d OpenAIDependencies) Ensure(c *gin.Context, log *zap.Logger) bool {
	missing := d.Missing()
	if len(missing) == 0 {
		return true
	}
	if log == nil {
		log = RequestLogger(c, "handler.openai_gateway.responses")
	}
	log.Error("openai.handler_dependencies_missing", zap.Strings("missing_dependencies", missing))
	if c != nil && c.Writer != nil && !c.Writer.Written() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "Service temporarily unavailable"}})
	}
	return false
}
