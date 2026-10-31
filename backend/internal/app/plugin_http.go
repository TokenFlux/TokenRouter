package app

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	pluginhttp "github.com/TokenFlux/TokenRouter/internal/plugin/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
)

// plugin_http.go assembles contribution routes from the existing panel security
// and locale owners. The plugin HTTP module receives verified account projections,
// caller grants, local package readers, and the same security client-IP resolver
// used by native panel limits, avoiding separate token or proxy-header interpretation.

// providePluginPublicHandler binds local contributions to shared identity and runtime owners.
// Construction does not open assets or acquire plugin processes. Request language
// comes from the global locale middleware, and profile lookup exposes only the existing projection.
func providePluginPublicHandler(manager *plugin.Manager, files *provider.PackageFiles, grants *plugin.Grants, users *identity.UserService) *pluginhttp.PublicHandler {
	return pluginhttp.NewPublicHandler(pluginhttp.PublicOptions{
		Runtime:  manager,
		Files:    files,
		Profiles: &pluginUserQueries{service: users},
		Grants:   grants,
		ClientIP: identityhttp.SecurityClientIP,
		Language: func(c *gin.Context) string { return locale.FromContext(c.Request.Context()) },
		Logger:   slog.Default(),
	})
}

// providePluginRouteMount supplies the panel guards required by contribution registration.
// Public-address and authenticated-user limits remain distinct, matching native
// panel accounting. Plugin HTTP operations do not enter the command idempotency coordinator.
func providePluginRouteMount(handler *pluginhttp.PublicHandler) pluginRouteMount {
	return func(v1 *gin.RouterGroup, security httpRouteSecurity) {
		pluginhttp.RegisterPublicRoutes(v1, handler, pluginhttp.PublicMiddleware{
			JWT:         security.JWT,
			Admin:       security.Admin,
			BackendUser: security.BackendUser,
			Panel:       security.Panel.Global(),
			PublicIP:    security.Panel.PublicIP(),
		})
	}
}
