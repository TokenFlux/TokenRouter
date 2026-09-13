// Ops HTTP 保留原端点和响应契约；查询与运行状态由核心拥有。
package httpapi

import (
	"net/http"

	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// GetAuthCacheInvalidationHealth 返回持久化 outbox 延迟与订阅器健康状态。
func (h *OpsHandler) GetAuthCacheInvalidationHealth(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.opsService.GetAuthCacheInvalidationHealth(c.Request.Context()))
}
