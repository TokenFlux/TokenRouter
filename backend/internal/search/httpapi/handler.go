// 搜索管理 HTTP 只调用 search 用例，保留管理字段与错误语义。
package httpapi

import (
	"strings"

	service "github.com/TokenFlux/TokenRouter/internal/search"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

type Handler struct{ settingService *service.ConfigService }

func New(s *service.ConfigService) *Handler { return &Handler{settingService: s} }

// TestWebSearchEmulation 测试 Web Search 搜索
// POST /api/v1/admin/settings/web-search-emulation/test
func (h *Handler) TestWebSearchEmulation(c *gin.Context) {
	var req struct {
		Query string `json:"query"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		req.Query = "搜索今年世界大事件"
	}

	result, err := service.TestWebSearch(c.Request.Context(), req.Query, h.settingService.Registry())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// ResetWebSearchUsage 重置指定 provider 的配额用量
// POST /api/v1/admin/settings/web-search-emulation/reset-usage
func (h *Handler) ResetWebSearchUsage(c *gin.Context) {
	var req struct {
		ProviderType string `json:"provider_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.ProviderType == "" {
		response.BadRequest(c, "provider_type is required")
		return
	}
	if err := service.ResetWebSearchUsage(c.Request.Context(), req.ProviderType, h.settingService.Registry()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}

// UpdateWebSearchEmulationConfig 更新 Web Search 模拟配置
// PUT /api/v1/admin/settings/web-search-emulation
func (h *Handler) UpdateWebSearchEmulationConfig(c *gin.Context) {
	var cfg service.WebSearchEmulationConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if err := h.settingService.SaveWebSearchEmulationConfig(c.Request.Context(), &cfg); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Re-read (with sanitized api keys) to return current state
	updated, err := h.settingService.GetWebSearchEmulationConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.PopulateWebSearchUsage(c.Request.Context(), updated, h.settingService.Registry()))
}

// GetWebSearchEmulationConfig 获取 Web Search 模拟配置
// GET /api/v1/admin/settings/web-search-emulation
func (h *Handler) GetWebSearchEmulationConfig(c *gin.Context) {
	cfg, err := h.settingService.GetWebSearchEmulationConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.PopulateWebSearchUsage(c.Request.Context(), cfg, h.settingService.Registry()))
}
