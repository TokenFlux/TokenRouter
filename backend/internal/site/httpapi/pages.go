// PageHandler 保留页面 HTTP 契约，不直接访问文件系统或设置仓储。
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/gin-gonic/gin"
)

type PageHandler struct{ pages *site.Pages }

func NewPageHandler(pages *site.Pages) *PageHandler { return &PageHandler{pages: pages} }
func (h *PageHandler) GetPageContent(c *gin.Context) {
	role, _ := authctx.GetUserRoleFromContext(c)
	content, err := h.pages.ReadMarkdown(c.Request.Context(), c.Param("slug"), role == "admin")
	switch {
	case errors.Is(err, site.ErrPageSlug):
		response.BadRequest(c, "Invalid page slug")
	case errors.Is(err, site.ErrPageNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "page not found"})
	case errors.Is(err, site.ErrPageTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "page too large"})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read page"})
	default:
		c.Data(http.StatusOK, "text/markdown; charset=utf-8", content)
	}
}
func (h *PageHandler) ListPages(c *gin.Context) {
	slugs, _ := h.pages.ListPages(c.Request.Context())
	response.Success(c, slugs)
}
func (h *PageHandler) ServePageImage(c *gin.Context) {
	path, err := h.pages.ImagePath(c.Request.Context(), c.Param("slug"), strings.TrimPrefix(c.Param("filename"), "/"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.File(path)
}
func (h *PageHandler) Register(v1 *gin.RouterGroup, jwtAuth, adminAuth gin.HandlerFunc) {
	pages := v1.Group("/pages")
	pages.Use(jwtAuth)
	pages.GET("/:slug", h.GetPageContent)
	images := v1.Group("/pages")
	images.GET("/:slug/images/*filename", h.ServePageImage)
	admin := v1.Group("/pages")
	admin.Use(adminAuth)
	admin.GET("", h.ListPages)
}
