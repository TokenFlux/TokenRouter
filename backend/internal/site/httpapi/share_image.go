package httpapi

import (
	"bytes"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// share_image.go serves the currently published cover without authentication.
// The service verifies its content address before bytes reach HTTP. Successful
// responses support conditional reads and HEAD, while errors prohibit caching
// so a temporary database failure cannot poison an otherwise valid image URL.

// GetShareImage serves a cover whose digest still matches the saved image.
// The route accepts GET and HEAD. Conditional requests use the content digest,
// and unsuccessful reads retain no-store rather than inheriting successful caching.
func (h *PublicHandler) GetShareImage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	cover, err := h.settingService.GetShareImage(c.Request.Context(), c.Param("hash"))
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	c.Header("Content-Type", cover.Metadata.Type)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("ETag", `"`+cover.Metadata.Hash+`"`)
	c.Header("Cache-Control", "public, max-age=3600, must-revalidate")
	http.ServeContent(c.Writer, c.Request, "cover", time.Time{}, bytes.NewReader(cover.Data))
}
