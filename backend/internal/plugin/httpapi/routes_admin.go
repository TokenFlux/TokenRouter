package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// routes_admin.go mounts plugin operations under the application's authenticated
// administrator group. Catalog step-up metadata is enforced with the same HTTP
// guard on direct browser requests, keeping executable and capability-policy changes
// subject to the application's existing administrator verification rules.

// RegisterAdminRoutes mounts every installed-plugin operation with its declared HTTP guard.
// The caller supplies administrator authentication, auditing, and rate limiting on
// admin. Sensitive writes receive stepUp before their handler can parse or mutate data.
// @project-doc docs/interfaces/plugin_contracts.md#administration
func RegisterAdminRoutes(admin *gin.RouterGroup, handler *Handler, stepUp gin.HandlerFunc) {
	for id, endpoint := range map[string]gin.HandlerFunc{
		"plugins.update": handler.Update, "plugins.rollback": handler.Rollback,
		"plugins.catalog.list": handler.Catalog, "plugins.catalog.get": handler.CatalogEntry, "plugins.catalog.refresh": handler.RefreshCatalog,
		"plugins.indexes.list": handler.Indexes, "plugins.indexes.create": handler.CreateIndex, "plugins.indexes.update": handler.UpdateIndex, "plugins.indexes.delete": handler.DeleteIndex,
		"plugins.list": handler.List, "plugins.get": handler.Get, "plugins.uploads.create": handler.Upload,
		"plugins.install": handler.Install, "plugins.enable": handler.Enable, "plugins.disable": handler.Disable,
		"plugins.restart": handler.Restart, "plugins.delete": handler.Delete, "plugins.config.get": handler.Config,
		"plugins.config.update": handler.SetConfig, "plugins.limits.get": handler.Limits, "plugins.limits.update": handler.SetLimits, "plugins.logs.list": handler.Logs,
	} {
		operation := ManagementOperations[id]
		chain := []gin.HandlerFunc{endpoint}
		if operation.StepUp {
			chain = append([]gin.HandlerFunc{stepUp}, chain...)
		}
		adminapi.Register(admin, operation, chain...)
	}
}
