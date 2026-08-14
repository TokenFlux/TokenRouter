package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	middleware2 "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

var validSlugPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

const maxPageFileSize = 1 << 20 // Markdown 页面最大读取 1MB，避免误读大文件。

type PageHandler struct {
	pagesDir       string
	settingService *service.SettingService
}

func NewPageHandler(dataDir string, settingService *service.SettingService) *PageHandler {
	pagesDir := filepath.Join(dataDir, "pages")
	_ = os.MkdirAll(pagesDir, 0755)
	return &PageHandler{pagesDir: pagesDir, settingService: settingService}
}

// GetPageContent 返回指定 slug 的 Markdown 原文。
// GET /api/v1/pages/:slug
func (h *PageHandler) GetPageContent(c *gin.Context) {
	slug := c.Param("slug")
	if !validSlugPattern.MatchString(slug) || len(slug) > 64 {
		response.BadRequest(c, "Invalid page slug")
		return
	}

	if !h.checkSlugVisibility(c, slug) {
		c.JSON(http.StatusNotFound, gin.H{"error": "page not found"})
		return
	}

	filePath := filepath.Join(h.pagesDir, slug+".md")
	cleaned := filepath.Clean(filePath)
	if !isPathWithinBase(cleaned, h.pagesDir) {
		response.BadRequest(c, "Invalid page slug")
		return
	}

	info, err := os.Stat(cleaned)
	if err != nil || info.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{"error": "page not found"})
		return
	}
	if info.Size() > maxPageFileSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "page too large"})
		return
	}

	content, err := os.ReadFile(cleaned)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read page"})
		return
	}

	c.Data(http.StatusOK, "text/markdown; charset=utf-8", content)
}

// ListPages 返回 data/pages 下已有的 Markdown 页面 slug。
// GET /api/v1/pages
func (h *PageHandler) ListPages(c *gin.Context) {
	entries, err := os.ReadDir(h.pagesDir)
	if err != nil {
		response.Success(c, []string{})
		return
	}

	slugs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".md") {
			slugs = append(slugs, strings.TrimSuffix(name, ".md"))
		}
	}
	response.Success(c, slugs)
}

// ServePageImage 返回 data/pages/{slug}/ 目录下的图片资源。
// GET /api/v1/pages/:slug/images/*filename
func (h *PageHandler) ServePageImage(c *gin.Context) {
	slug := c.Param("slug")
	filename := strings.TrimPrefix(c.Param("filename"), "/")

	if !validSlugPattern.MatchString(slug) || len(slug) > 64 {
		c.Status(http.StatusNotFound)
		return
	}
	if !h.checkImageSlugVisibility(c, slug) {
		c.Status(http.StatusNotFound)
		return
	}

	imagesDir := filepath.Join(h.pagesDir, slug)
	cleaned, ok := resolvePageImagePath(h.pagesDir, imagesDir, filename)
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}

	info, err := os.Stat(cleaned)
	if err != nil || info.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}

	c.File(cleaned)
}

func resolvePageImagePath(pagesDir, imagesDir, filename string) (string, bool) {
	relPath, ok := cleanPageImageRelativePath(filename)
	if !ok {
		return "", false
	}

	cleanedPagesDir := filepath.Clean(pagesDir)
	cleanedImagesDir := filepath.Clean(imagesDir)
	cleanedTarget := filepath.Clean(filepath.Join(cleanedImagesDir, relPath))
	if !isPathWithinBase(cleanedTarget, cleanedImagesDir) {
		return "", false
	}

	realPagesDir, err := filepath.EvalSymlinks(cleanedPagesDir)
	if err != nil {
		return "", false
	}
	realImagesDir, err := filepath.EvalSymlinks(cleanedImagesDir)
	if err != nil || !isPathWithinBase(realImagesDir, realPagesDir) {
		return "", false
	}
	realTarget, err := filepath.EvalSymlinks(cleanedTarget)
	if err != nil || !isPathWithinBase(realTarget, realImagesDir) {
		return "", false
	}
	return realTarget, true
}

func cleanPageImageRelativePath(filename string) (string, bool) {
	if filename == "" || strings.HasPrefix(filename, "/") {
		return "", false
	}
	decoded, err := url.PathUnescape(filename)
	if err != nil {
		return "", false
	}
	if decoded == "" || strings.HasPrefix(decoded, "/") || strings.Contains(decoded, "\\") || strings.ContainsRune(decoded, 0) {
		return "", false
	}

	parts := make([]string, 0)
	for _, part := range strings.Split(decoded, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", false
		default:
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "", false
	}

	relPath := filepath.Join(parts...)
	if filepath.IsAbs(relPath) || filepath.VolumeName(relPath) != "" {
		return "", false
	}
	return relPath, true
}

func isPathWithinBase(path, base string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// findSlugVisibility 从 custom_menu_items 查找 slug 对应的可见范围。
func (h *PageHandler) findSlugVisibility(c *gin.Context, slug string) (string, bool) {
	if h.settingService == nil {
		return "", false
	}

	raw := h.settingService.GetCustomMenuItemsRaw(c.Request.Context())
	if raw == "" || raw == "[]" {
		return "", false
	}

	var items []struct {
		URL        string `json:"url"`
		PageSlug   string `json:"page_slug"`
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return "", false
	}

	for _, item := range items {
		itemSlug := item.PageSlug
		if itemSlug == "" && strings.HasPrefix(item.URL, "md:") {
			itemSlug = strings.TrimPrefix(item.URL, "md:")
		}
		if itemSlug == slug {
			return item.Visibility, true
		}
	}
	return "", false
}

// checkSlugVisibility 校验 Markdown 页面已配置到菜单，且当前用户有访问权限。
func (h *PageHandler) checkSlugVisibility(c *gin.Context, slug string) bool {
	visibility, found := h.findSlugVisibility(c, slug)
	if !found {
		return false
	}
	if visibility == "admin" {
		role, _ := middleware2.GetUserRoleFromContext(c)
		return role == service.RoleAdmin
	}
	return true
}

// checkImageSlugVisibility 校验图片所属页面可见范围。
// 浏览器 img 标签不能附带 JWT，这里只允许普通用户可见页面的图片。
func (h *PageHandler) checkImageSlugVisibility(c *gin.Context, slug string) bool {
	visibility, found := h.findSlugVisibility(c, slug)
	if !found {
		return false
	}
	return visibility != "admin"
}

// RegisterPageRoutes 注册 Markdown 页面接口。
func RegisterPageRoutes(
	v1 *gin.RouterGroup,
	dataDir string,
	jwtAuth gin.HandlerFunc,
	adminAuth gin.HandlerFunc,
	settingService *service.SettingService,
) {
	h := NewPageHandler(dataDir, settingService)

	pages := v1.Group("/pages")
	pages.Use(jwtAuth)
	{
		pages.GET("/:slug", h.GetPageContent)
	}

	pageImages := v1.Group("/pages")
	{
		pageImages.GET("/:slug/images/*filename", h.ServePageImage)
	}

	adminPages := v1.Group("/pages")
	adminPages.Use(adminAuth)
	{
		adminPages.GET("", h.ListPages)
	}
}
