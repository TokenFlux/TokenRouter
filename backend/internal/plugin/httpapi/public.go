package httpapi

import (
	"context"
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// public.go binds published plugin views to panel-facing contributions and
// static resources. Authentication, backend-mode policy, and rate-limit handlers
// come from application assembly; this module selects those existing handlers
// after matching the manifest and never parses or validates JWTs itself.
// Process availability remains separate from static UI discovery.

// PublishedRuntime exposes immutable releases and process leases to HTTP consumers.
type PublishedRuntime interface {
	plugin.Runtime
	// Enabled reports the host-wide emergency availability switch for plugin routes.
	Enabled() bool
	// Snapshot returns one immutable release view without waiting for management work.
	// Its nested metadata remains manager-owned. Consumers may retain the view for
	// their request but must never change configuration, manifests, or file metadata.
	Snapshot(string) (plugin.Loaded, bool)
	// Snapshots returns copied outer views in deterministic plugin identifier order.
	// Nested release metadata remains immutable. Process readiness does not remove
	// static UI contributions from this installation discovery view.
	Snapshots() []plugin.Loaded
	// AssetReference reads the archive identity for an accepted current or previous release.
	// It does not fetch files or decrypt settings. Missing versions fail, allowing
	// assets to expose only the explicitly declared extra SVG or PNG path.
	AssetReference(context.Context, string, string) (plugin.PackageRef, error)
}

// PublicOptions supplies shared owners and request metadata readers before serving routes.
type PublicOptions struct {
	// Runtime, Files, Profiles, Grants, ClientIP, and Language are assembled once before registration and remain available until HTTP requests have drained.
	Runtime  PublishedRuntime          // accepted release and lease owner
	Files    plugin.AssetFiles         // local-only package asset reader
	Profiles plugin.UserReader         // current account profile projection reader
	Grants   *plugin.Grants            // private caller capability ticket signer
	ClientIP func(*gin.Context) string // shared security client-address resolver
	Language func(*gin.Context) string // shared negotiated request-language reader
	Logger   *slog.Logger              // bounded contribution diagnostic destination
}

// PublicHandler serves declared HTTP endpoints, public assets, and authenticated UI descriptions.
type PublicHandler struct {
	options PublicOptions // immutable application-owned collaborators
}

// NewPublicHandler retains configured collaborators without opening files or starting processes.
// Callers supply every reader and metadata function before registering routes.
// A nil logger selects the application's default diagnostic destination.
func NewPublicHandler(options PublicOptions) *PublicHandler {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &PublicHandler{options: options}
}

// Ready rejects contribution traffic while the host's emergency switch is disabled.
// Administrative inspection and uninstall use their separate route family. This
// guard runs before optional authentication, file opening, or plugin request processing.
func (h *PublicHandler) Ready(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	if !h.options.Runtime.Enabled() {
		httpx.ErrorFrom(c, plugin.ErrPluginsDisabled)
		c.Abort()
		return
	}
	c.Next()
}
