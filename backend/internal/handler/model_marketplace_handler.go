package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/handler/dto"
	"github.com/TokenFlux/TokenRouter/internal/pkg/response"
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

type ModelMarketplaceHandler struct {
	modelMarketplaceService *service.ModelMarketplaceService
	dashboardService        *service.DashboardService
}

func NewModelMarketplaceHandler(modelMarketplaceService *service.ModelMarketplaceService, dashboardService *service.DashboardService) *ModelMarketplaceHandler {
	return &ModelMarketplaceHandler{
		modelMarketplaceService: modelMarketplaceService,
		dashboardService:        dashboardService,
	}
}

// ListPublic 返回公开模型广场列表。
// GET /api/v1/marketplace/models
func (h *ModelMarketplaceHandler) ListPublic(c *gin.Context) {
	groups, err := h.modelMarketplaceService.ListPublic(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.ModelMarketplaceGroupsFromService(groups))
}

// StatsPublic 返回首页公开统计，只暴露总 Token 和注册用户数。
// GET /api/v1/marketplace/stats
func (h *ModelMarketplaceHandler) StatsPublic(c *gin.Context) {
	stats, err := h.dashboardService.GetPublicDashboardStats(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.ModelMarketplaceStatsFromService(stats))
}
