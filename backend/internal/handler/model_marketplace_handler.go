package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/handler/dto"
	"github.com/TokenFlux/TokenRouter/internal/pkg/response"
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

type ModelMarketplaceHandler struct {
	modelMarketplaceService *service.ModelMarketplaceService
}

func NewModelMarketplaceHandler(modelMarketplaceService *service.ModelMarketplaceService) *ModelMarketplaceHandler {
	return &ModelMarketplaceHandler{
		modelMarketplaceService: modelMarketplaceService,
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
