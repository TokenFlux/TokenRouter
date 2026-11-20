package httpapi

import (
	"maps"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// management_catalog.go returns administrator model candidates in
// platform-specific response formats. Administrator model candidates come from
// provider configuration and platform rules, while the unified catalogue
// supplies display values. Response encoding follows the selected platform
// shape: OpenAI includes zero-valued fields, Grok omits optional empty fields,
// and the default shape carries a textual creation timestamp.

// claudeCatalogModel encodes the default catalogue response with textual
// creation time.
type claudeCatalogModel struct {
	ID          string `json:"id"`           // requestable model identifier
	Type        string `json:"type"`         // model record kind
	DisplayName string `json:"display_name"` // administrator model display label
	CreatedAt   string `json:"created_at"`   // catalogue creation time text
}

// openAICatalogModel encodes OpenAI catalogue fields, including their explicit
// zero values.
type openAICatalogModel struct {
	ID          string `json:"id"`           // requestable model identifier
	Object      string `json:"object"`       // model object label
	Created     int64  `json:"created"`      // Unix seconds
	OwnedBy     string `json:"owned_by"`     // catalogue owner label
	Type        string `json:"type"`         // model record kind
	DisplayName string `json:"display_name"` // administrator model display label
}

// grokCatalogModel encodes Grok catalogue entries with optional zero-valued
// fields omitted.
type grokCatalogModel struct {
	ID          string `json:"id"`                     // requestable model identifier
	Object      string `json:"object"`                 // model object label
	Type        string `json:"type,omitempty"`         // model record kind
	Created     int64  `json:"created,omitempty"`      // Unix seconds
	OwnedBy     string `json:"owned_by"`               // catalogue owner label
	DisplayName string `json:"display_name,omitempty"` // administrator model display label
}

// GetAvailableModels loads provider configuration and checks its candidate
// models against requestability rules. Every provider lookup failure maps to
// 404, catalogue failures map to 400, and success uses the selected platform
// response shape.
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
	}, Platform: v.Platform, Site: v.GetCredential("site"), OAuth: v.IsOAuth(), Passthrough: v.IsOpenAIPassthroughEnabled()}, rules.ConfiguredModels)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, adminCatalogResponse(result))
}

// adminCatalogResponse maps catalogue entries into their platform JSON shape.
// Nil model slices remain null and nonnil empty slices remain empty arrays;
// unsupported kinds use the Claude-shaped response values.
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

// GetAntigravityDefaultModelMapping returns a cloned Antigravity mapping from
// the injected defaults. This copies the string map for response ownership, and
// no provider lookup or live model query is performed.
func (h *ManagementHandler) GetAntigravityDefaultModelMapping(c *gin.Context) {
	response.Success(c, maps.Clone(h.modelDefaults.Antigravity()))
}
