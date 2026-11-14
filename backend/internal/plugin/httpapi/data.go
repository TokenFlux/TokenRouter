package httpapi

import (
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// data.go exposes administrator-only management of persistent plugin files.
// Inventory includes uninstalled owners, while backup and restore use the core
// service's stopped-owner checks. File archives remain separate from host SQL
// backups and ordinary plugin contribution request and response limits.

// ListData returns retained directory usage without exposing absolute filesystem paths.
func (h *Handler) ListData(c *gin.Context) {
	items, err := h.service.ListData(c.Request.Context())
	if httpx.ErrorFrom(c, err) {
		return
	}
	httpx.Success(c, items)
}

// PurgeData removes an uninstalled owner's local files and retained KV records.
func (h *Handler) PurgeData(c *gin.Context) {
	if httpx.ErrorFrom(c, h.service.PurgeData(c.Request.Context(), c.Param("id"))) {
		return
	}
	httpx.Success(c, nil)
}

// BackupData streams a completed private snapshot after stopped-owner validation succeeds.
// The archive reader belongs to this response and is closed on interrupted
// delivery as well. Cache prevention applies to successful and failed responses.
func (h *Handler) BackupData(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	archive, err := h.service.BackupData(c.Request.Context(), c.Param("id"))
	if httpx.ErrorFrom(c, err) {
		return
	}
	defer func() { _ = archive.Reader.Close() }()
	c.DataFromReader(http.StatusOK, archive.Bytes, "application/zip", archive.Reader, map[string]string{
		"Content-Disposition":    mime.FormatMediaType("attachment", map[string]string{"filename": c.Param("id") + "-data.zip"}),
		"X-Content-Type-Options": "nosniff",
	})
}

// RestoreData accepts a bounded raw ZIP for an absent or empty stopped owner.
// Content validation and publication belong to the persistent file store. Failed
// archives return stable management errors and never overwrite existing business data.
func (h *Handler) RestoreData(c *gin.Context) {
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/zip" {
		httpx.ErrorFrom(c, plugin.ErrDataInvalid)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, plugin.DataMaxArchiveBytes+1)
	defer func() { _ = c.Request.Body.Close() }()
	if httpx.ErrorFrom(c, h.service.RestoreData(c.Request.Context(), c.Param("id"), c.Request.Body)) {
		return
	}
	httpx.Success(c, nil)
}
