package httpapi

import (
	"maps"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// 保留三种管理目录 JSON 变体；零值字段和省略行为不做统一化。
type claudeCatalogModel struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}
type openAICatalogModel struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
}
type grokCatalogModel struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Type        string `json:"type,omitempty"`
	Created     int64  `json:"created,omitempty"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name,omitempty"`
}

// GetAvailableModels 读取提供商数据，查询模型目录并编码为接口响应。
func (h *ManagementHandler) GetAvailableModels(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid provider ID")
		return
	}
	v, err := h.adminService.GetProvider(c.Request.Context(), id)
	if err != nil {
		response.NotFound(c, "Provider not found")
		return
	}
	rules := h.modelRules(v)
	result, err := h.catalog.Available(routing.AdminCatalogInput{Accept: func(id string) bool {
		return rules.Supports(c.Request.Context(), id)
	}, Platform: v.Platform, Site: v.GetCredential("site"), OAuth: v.IsOAuth(), GoogleOne: v.IsGeminiGoogleOne(), Passthrough: v.IsOpenAIPassthroughEnabled()}, rules.ConfiguredModels)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, adminCatalogResponse(result))
}

func adminCatalogResponse(v routing.AdminCatalogResult) any {
	switch v.Kind {
	case routing.CatalogOpenAI:
		var out []openAICatalogModel
		if v.Models != nil {
			out = make([]openAICatalogModel, 0, len(v.Models))
		}
		for _, m := range v.Models {
			out = append(out, openAICatalogModel{ID: m.ID, Object: m.Object, Created: m.Created, OwnedBy: m.OwnedBy, Type: m.Type, DisplayName: m.DisplayName})
		}
		return out
	case routing.CatalogGrok:
		var out []grokCatalogModel
		if v.Models != nil {
			out = make([]grokCatalogModel, 0, len(v.Models))
		}
		for _, m := range v.Models {
			out = append(out, grokCatalogModel{ID: m.ID, Object: m.Object, Created: m.Created, OwnedBy: m.OwnedBy, Type: m.Type, DisplayName: m.DisplayName})
		}
		return out
	default:
		var out []claudeCatalogModel
		if v.Models != nil {
			out = make([]claudeCatalogModel, 0, len(v.Models))
		}
		for _, m := range v.Models {
			out = append(out, claudeCatalogModel{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName, CreatedAt: m.CreatedAt})
		}
		return out
	}
}

// GetAntigravityDefaultModelMapping 展示原默认表的独立副本；平台来源仍由装配提供。
func (h *ManagementHandler) GetAntigravityDefaultModelMapping(c *gin.Context) {
	response.Success(c, maps.Clone(h.modelDefaults.Antigravity()))
}
