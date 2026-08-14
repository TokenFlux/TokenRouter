package admin

import (
	"net/http"

	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
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
