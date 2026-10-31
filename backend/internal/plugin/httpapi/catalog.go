package httpapi

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// catalog.go exposes source-qualified market reads and source administration.
// Index responses omit cached documents, keeping source management bounded by
// registration metadata rather than every release manifest. Mutation handlers
// use the same protected route declarations as executable installation changes.

// IndexDTO describes source authority and its latest transfer observations for administrators.
type IndexDTO struct {
	ID              int64      `json:"id"`               // durable source registration identifier
	Name            string     `json:"name"`             // administrator configured display name
	URL             string     `json:"url"`              // HTTPS catalog document location
	PublicKey       string     `json:"public_key"`       // optional base64 verification pin
	Builtin         bool       `json:"builtin"`          // host configured official registration
	Enabled         bool       `json:"enabled"`          // source contributes market entries
	SignatureStatus string     `json:"signature_status"` // latest observed signature classification
	CachedAt        *time.Time `json:"cached_at"`        // last accepted cache publication
	LastError       string     `json:"last_error"`       // bounded latest refresh diagnostic
	Refreshing      bool       `json:"refreshing"`       // current source transfer in progress
}

// Catalog lists accepted market entries with compatibility and installed-source relationships.
func (h *Handler) Catalog(c *gin.Context) {
	entries, err := h.catalog.List(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, entries)
}

// CatalogEntry reads every retained version of one explicitly selected source entry.
func (h *Handler) CatalogEntry(c *gin.Context) {
	id, ok := sourceID(c, "indexId")
	if !ok {
		return
	}
	entry, err := h.catalog.Get(c.Request.Context(), id, c.Param("pluginId"))
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, entry)
}

// RefreshCatalog waits for all enabled sources and reports any rejected refresh.
// Other sources can succeed independently. Callers should reload source metadata
// after either outcome because each completed attempt records its own diagnostics.
func (h *Handler) RefreshCatalog(c *gin.Context) {
	if err := h.catalog.RefreshAll(c.Request.Context()); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	h.Indexes(c)
}

// Indexes returns registration details without serializing their potentially large accepted caches.
func (h *Handler) Indexes(c *gin.Context) {
	items, err := h.catalog.ListIndexes(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	result := make([]IndexDTO, 0, len(items))
	for _, item := range items {
		result = append(result, h.indexDTO(item))
	}
	httpx.Success(c, result)
}

// CreateIndex persists one validated custom source before its asynchronous first refresh.
func (h *Handler) CreateIndex(c *gin.Context) {
	var input plugin.IndexInput
	if !decodeBody(c, &input) {
		return
	}
	item, err := h.catalog.CreateIndex(c.Request.Context(), input)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, h.indexDTO(*item))
}

// UpdateIndex applies supplied source fields while preserving omitted values and builtin authority.
func (h *Handler) UpdateIndex(c *gin.Context) {
	id, ok := sourceID(c, "id")
	if !ok {
		return
	}
	var input plugin.IndexInput
	if !decodeBody(c, &input) {
		return
	}
	item, err := h.catalog.UpdateIndex(c.Request.Context(), id, input)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, h.indexDTO(*item))
}

// DeleteIndex removes a custom source while preserving its already accepted installations.
func (h *Handler) DeleteIndex(c *gin.Context) {
	id, ok := sourceID(c, "id")
	if !ok {
		return
	}
	if err := h.catalog.DeleteIndex(c.Request.Context(), id); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, gin.H{"deleted": true})
}

// Update applies the reviewed release and exposes failed-start restoration through stable errors.
func (h *Handler) Update(c *gin.Context) {
	actor, ok := actorID(c)
	if !ok {
		return
	}
	var input plugin.UpdateInput
	if !decodeBody(c, &input) {
		return
	}
	result, err := h.service.Update(c.Request.Context(), c.Param("id"), input, actor)
	writeChange(c, result, err)
}

// Rollback exchanges the current release with its complete previously accepted version snapshot.
func (h *Handler) Rollback(c *gin.Context) {
	actor, ok := actorID(c)
	if !ok {
		return
	}
	result, err := h.service.Rollback(c.Request.Context(), c.Param("id"), actor)
	writeChange(c, result, err)
}

// indexDTO projects source metadata and adds a current local refresh observation.
func (h *Handler) indexDTO(item plugin.IndexSource) IndexDTO {
	return IndexDTO{
		ID: item.ID, Name: item.Name, URL: item.URL, PublicKey: item.PublicKey,
		Builtin: item.Builtin, Enabled: item.Enabled, SignatureStatus: item.SignatureStatus,
		CachedAt: item.CachedAt, LastError: item.LastError, Refreshing: h.catalog.Refreshing(item.ID),
	}
}

// sourceID requires a positive decimal registration identity before accessing catalog storage.
func sourceID(c *gin.Context, parameter string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(parameter), 10, 64)
	if err != nil || id <= 0 {
		httpx.ErrorFrom(c, plugin.ErrIndexNotFound)
		return 0, false
	}
	return id, true
}
