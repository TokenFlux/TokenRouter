// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// MarketplaceStatsReader 只提供首页公开统计投影，不暴露 Dashboard 的其它能力。
type MarketplaceStatsReader interface {
	PublicStats(context.Context) (dto.ModelMarketplaceStats, error)
}
type MarketplaceHandler struct {
	marketplace *routing.Marketplace
	stats       MarketplaceStatsReader
}

func NewMarketplaceHandler(marketplace *routing.Marketplace, stats MarketplaceStatsReader) *MarketplaceHandler {
	return &MarketplaceHandler{marketplace: marketplace, stats: stats}
}

// ListPublic 保留公开模型市场的 URL、错误 envelope 和模型数组顺序。
func (h *MarketplaceHandler) ListPublic(c *gin.Context) {
	groups, err := h.marketplace.ListPublic(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, dto.ModelMarketplaceGroupsFromRouting(groups))
}

// StatsPublic 仅返回原公开 Token/用户计数。
func (h *MarketplaceHandler) StatsPublic(c *gin.Context) {
	stats, err := h.stats.PublicStats(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, stats)
}
