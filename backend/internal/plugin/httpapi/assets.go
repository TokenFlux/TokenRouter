package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// assets.go serves versioned frontend files and root plugin icons through a
// fixed extension allowlist. Managed package lookup never downloads missing
// versions, while an active development snapshot can supply its inspected local
// directory. Bounded file handles retain package integrity metadata for immutable
// caching, and SVG responses receive a sandbox policy before any content is sent.
//
// One additional image path may be declared by each release's manifest. Current
// and previous snapshots select that exact path; if no loaded snapshot is
// available, the manager resolves accepted metadata from storage. This keeps
// custom publisher icons usable without making other package files public or
// applying the current version's icon declaration to an unrelated retained version.

// Asset serves one allowed local asset without authenticating a browser session.
// Missing versions, traversal, unsupported extensions, and invalid metadata return
// not found. The caller's global switch and public-address limits run before this handler.
//
// Declared SVG or PNG icons may live outside the standard frontend directory,
// but the requested release must explicitly name that same path. Image metadata
// uses current or retained previous snapshots before reading the matching package.
// Those icons and the fixed root icon names retain a 64 KiB ceiling; ordinary
// frontend files keep the package's 8 MiB asset bound.
func (h *PublicHandler) Asset(c *gin.Context) {
	name := strings.TrimPrefix(c.Param("file"), "/")
	contentType, allowed := assetContentType(name)
	id, version := c.Param("id"), c.Param("version")
	loaded, current := h.options.Runtime.Snapshot(id)
	maximum := int64(8 << 20)
	if name == "icon.svg" || name == "icon.png" {
		maximum = 64 << 10
	}
	if manifest.ValidPath(name) && path.Clean(name) == name && (path.Ext(name) == ".svg" || path.Ext(name) == ".png") {
		var declared string
		switch {
		case current && loaded.Installation != nil && loaded.Installation.Version == version && loaded.Installation.Manifest != nil:
			declared = loaded.Installation.Manifest.Icon
		case current && loaded.Installation != nil && loaded.Installation.Previous != nil && loaded.Installation.Previous.Version == version && loaded.Installation.Previous.Manifest != nil:
			declared = loaded.Installation.Previous.Manifest.Icon
		default:
			// Retained assets may outlive a failed current-package load; resolve their declaration from accepted installation metadata without downloading or decrypting anything.
			ref, _ := h.options.Runtime.AssetReference(c.Request.Context(), id, version)
			declared = ref.Icon
		}
		if declared == name {
			allowed = true
			maximum = 64 << 10
			contentType = "image/svg+xml"
			if path.Ext(name) == ".png" {
				contentType = "image/png"
			}
		}
	}
	if !allowed {
		httpx.NotFound(c, "Plugin asset not found")
		return
	}
	var local plugin.Package
	if current && loaded.Installation != nil && loaded.Installation.Version == version {
		local = loaded.Package
	} else {
		ref, err := h.options.Runtime.AssetReference(c.Request.Context(), id, version)
		if err != nil {
			httpx.NotFound(c, "Plugin asset not found")
			return
		}
		local, err = h.options.Files.Local(c.Request.Context(), ref)
		if err != nil {
			httpx.NotFound(c, "Plugin asset not found")
			return
		}
	}
	file, err := h.options.Files.Open(c.Request.Context(), local, name, maximum)
	if err != nil {
		httpx.NotFound(c, "Plugin asset not found")
		return
	}
	defer func() { _ = file.Reader.Close() }()
	digest, err := hex.DecodeString(file.Metadata.SHA256)
	if err != nil || len(digest) != sha256.Size {
		httpx.NotFound(c, "Plugin asset not found")
		return
	}
	c.Header("Content-Type", contentType)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	// The accepted digest supplies a stable validator across process restarts and filesystem timestamp changes without rehashing content for each browser request.
	c.Header("ETag", `"`+hex.EncodeToString(digest[:16])+`"`)
	if strings.EqualFold(path.Ext(name), ".svg") {
		c.Header("Content-Security-Policy", "sandbox")
		c.Header("Content-Disposition", "inline")
	}
	http.ServeContent(c.Writer, c.Request, path.Base(name), file.ModifiedAt, file.Reader)
}

// assetContentType admits only frontend descendants and the two public root icon names.
// Paths must already be canonical and satisfy package syntax. MIME selection
// follows an explicit extension table rather than filesystem or platform inference.
func assetContentType(name string) (string, bool) {
	if !manifest.ValidPath(name) || path.Clean(name) != name ||
		(!strings.HasPrefix(name, "frontend/") && name != "icon.svg" && name != "icon.png") {
		return "", false
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".js":
		return "text/javascript", true
	case ".css":
		return "text/css", true
	case ".svg":
		return "image/svg+xml", true
	case ".png":
		return "image/png", true
	case ".jpg":
		return "image/jpeg", true
	case ".webp":
		return "image/webp", true
	case ".json", ".map":
		return "application/json", true
	default:
		return "", false
	}
}
