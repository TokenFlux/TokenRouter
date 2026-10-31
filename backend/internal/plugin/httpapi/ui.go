package httpapi

import (
	"context"
	"io"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// ui.go projects immutable frontend declarations into authenticated page and menu
// descriptions. Audience filtering happens before menu expansion, and display text
// uses the request language supplied by the shared application locale middleware.
// Process readiness does not hide a locally available frontend contribution.
//
// Script and style descriptors use accepted integrity hashes and require their
// files to remain present. This checks availability without reading every bundle
// during menu discovery. Icons are small declared SVG files, returned as text
// for the frontend's existing sanitization path. Missing optional icons leave
// the menu usable; missing required bundles omit that plugin from this response.

// UIAsset identifies one public versioned bundle with browser-enforced integrity.
type UIAsset struct {
	URL       string `json:"url"`       // public versioned asset request URL
	Integrity string `json:"integrity"` // SHA-384 browser integrity attribute
}

// UIPage exposes one audience-filtered static contribution route and localized title.
type UIPage struct {
	ID       string `json:"id"`       // plugin-defined page component identifier
	Path     string `json:"path"`     // plugin-relative static route path
	Audience string `json:"audience"` // authenticated user or administrator audience
	Title    string `json:"title"`    // request-localized page display title
}

// UIMenu associates a visible page with its localized sidebar presentation.
type UIMenu struct {
	Page     string `json:"page"`     // target page component identifier
	Audience string `json:"audience"` // audience inherited from target page
	Label    string `json:"label"`    // request-localized sidebar display text
	IconSVG  string `json:"icon_svg"` // declared SVG for frontend sanitization
	Order    int    `json:"order"`    // sidebar sorting priority with default applied
	Route    string `json:"route"`    // absolute host contribution page route
}

// UIPlugin contains only visible frontend metadata and accepted public bundle identities.
type UIPlugin struct {
	ID      string    `json:"id"`      // publisher-qualified plugin installation identity
	Version string    `json:"version"` // accepted installed release version
	Name    string    `json:"name"`    // request-localized plugin display name
	Script  UIAsset   `json:"script"`  // required IIFE script descriptor
	Styles  []UIAsset `json:"styles"`  // ordered declared stylesheet descriptors
	Pages   []UIPage  `json:"pages"`   // audience-filtered page contribution list
	Menus   []UIMenu  `json:"menus"`   // audience-filtered sidebar contribution list
}

// UI returns current local contributions visible to the authenticated panel role.
// Backend process failures do not suppress static pages. The response is private
// and uncached because audience and language can change independently of installed versions.
func (h *PublicHandler) UI(c *gin.Context) {
	role, _ := authctx.GetUserRoleFromContext(c)
	language := h.options.Language(c)
	plugins := make([]UIPlugin, 0)
	for _, loaded := range h.options.Runtime.Snapshots() {
		item := loaded.Installation
		if item == nil || !item.Enabled || item.Manifest == nil || item.Manifest.Frontend == nil {
			continue
		}
		view, ok := h.uiPlugin(c.Request.Context(), loaded, role, language)
		if ok {
			plugins = append(plugins, view)
		}
	}
	c.Header("Cache-Control", "no-store")
	httpx.Success(c, gin.H{"plugins": plugins})
}

// uiPlugin builds one visible contribution after confirming its required local bundles.
// Administrators can see both audiences; other authenticated roles see user pages.
// Declared menu order is preserved so the sidebar can apply its deterministic tie-breaks.
//
// Page filtering establishes the only menu targets eligible for inclusion. Bundle
// checks use accepted file metadata and bounded handles without loading scripts
// into memory. A missing script or stylesheet omits this plugin while preserving
// other installations' discovery; a missing optional SVG only clears that menu's
// icon, leaving its authorized route and localized label available to the caller.
func (h *PublicHandler) uiPlugin(ctx context.Context, loaded plugin.Loaded, role, language string) (UIPlugin, bool) {
	item := loaded.Installation
	frontend := item.Manifest.Frontend
	view := UIPlugin{ID: item.PluginID, Version: item.Version, Name: item.Manifest.Name.Localize(language), Styles: make([]UIAsset, 0), Pages: make([]UIPage, 0), Menus: make([]UIMenu, 0)}
	pages := make(map[string]manifest.Page)
	for _, page := range frontend.Pages {
		if page.Audience == "admin" && role != "admin" {
			continue
		}
		pages[page.ID] = page
		view.Pages = append(view.Pages, UIPage{ID: page.ID, Path: page.Path, Audience: page.Audience, Title: page.Title.Localize(language)})
	}
	if len(pages) == 0 {
		return UIPlugin{}, false
	}
	script, ok := h.bundle(ctx, loaded, frontend.Entry)
	if !ok {
		return UIPlugin{}, false
	}
	view.Script = script
	for _, name := range frontend.Styles {
		style, ok := h.bundle(ctx, loaded, name)
		if !ok {
			return UIPlugin{}, false
		}
		view.Styles = append(view.Styles, style)
	}
	for _, menu := range frontend.Menus {
		page, visible := pages[menu.Page]
		if !visible {
			continue
		}
		// Icons are optional decoration; keep the authorized page accessible when a concurrent update removes its icon before this read.
		icon := ""
		if menu.Icon != "" {
			file, err := h.options.Files.Open(ctx, loaded.Package, menu.Icon, 10<<10)
			if err == nil {
				data, readErr := io.ReadAll(file.Reader)
				_ = file.Reader.Close()
				if readErr == nil {
					icon = string(data)
				}
			}
		}
		route := "/ext/" + url.PathEscape(item.PluginID)
		if page.Audience == "admin" {
			route = "/admin" + route
		}
		if page.Path != "" {
			route += "/" + page.Path
		}
		view.Menus = append(view.Menus, UIMenu{Page: page.ID, Audience: page.Audience, Label: menu.Label.Localize(language), IconSVG: icon, Order: menu.SortOrder(), Route: route})
	}
	return view, true
}

// bundle confirms one required local file and returns its immutable public descriptor.
// File handles close before discovery returns. Missing files and absent integrity
// metadata omit the plugin instead of advertising a script that cannot load safely.
func (h *PublicHandler) bundle(ctx context.Context, loaded plugin.Loaded, name string) (UIAsset, bool) {
	if _, allowed := assetContentType(name); !allowed {
		return UIAsset{}, false
	}
	file, err := h.options.Files.Open(ctx, loaded.Package, name, 8<<20)
	if err != nil {
		h.options.Logger.Warn("Plugin UI bundle is unavailable", "plugin_id", loaded.Installation.PluginID, "asset", name)
		return UIAsset{}, false
	}
	defer func() { _ = file.Reader.Close() }()
	if !strings.HasPrefix(file.Metadata.SRI, "sha384-") {
		return UIAsset{}, false
	}
	return UIAsset{URL: assetURL(loaded.Installation.PluginID, loaded.Installation.Version, name), Integrity: file.Metadata.SRI}, true
}

// assetURL preserves each validated path segment within the versioned public route.
func assetURL(id, version, name string) string {
	segments := strings.Split(name, "/")
	for i := range segments {
		segments[i] = url.PathEscape(segments[i])
	}
	return "/api/v1/plugins/" + url.PathEscape(id) + "/assets/" + url.PathEscape(version) + "/" + strings.Join(segments, "/")
}
