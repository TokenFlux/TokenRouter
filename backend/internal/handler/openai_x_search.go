package handler

import (
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"
)

func (h *GatewayHandler) XSearch(c *gin.Context) { h.SearchHTTPHandler().XSearch(c) }
func resolveGrokStandaloneSearchModel() string {
	return xai.ResolveDefaultTextModel(xai.RuntimeModelMappingOptions().DefaultText)
}
