package httpapi

import "github.com/gin-gonic/gin"

// routes_public.go registers public settings and cover or legal-document reads on the
// supplied route group.
// Public browser initialization reads display settings, covers, and legal copy through
// the settings route family. app supplies the shared public group with rate
// limiting already attached, and all reads inherit that policy when registered.

// RegisterPublicSettingsRoutes installs browser settings, cover images, and legal-document reads under group.
// app supplies the public group with its rate limiting in place. These handler
// bindings inherit that middleware and the caller's chosen URL prefix.
func RegisterPublicSettingsRoutes(group *gin.RouterGroup, endpoint *PublicHandler) {
	group.GET("/public", endpoint.GetPublicSettings)
	group.GET("/share-image/:hash", endpoint.GetShareImage)
	group.HEAD("/share-image/:hash", endpoint.GetShareImage)
	group.GET("/legal/:id", endpoint.GetLegalDocument)
}
